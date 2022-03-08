package ingress

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/xid"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/api/networking/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	v1 "github.com/kuadrant/kcp-glbc/pkg/apis/kuadrant/v1"
	"github.com/kuadrant/kcp-glbc/pkg/cluster"
)

const (
	clusterLabel = "kcp.dev/cluster"
	ownedByLabel = "kcp.dev/owned-by"

	hostGeneratedAnnotation = "kuadrant.dev/host.generated"
	customHostReplaced      = "kuadrant.dev/custom-hosts.replaced"

	manager                 = "kcp-ingress"
	cascadeCleanupFinalizer = "kcp.dev/cascade-cleanup"
)

func (c *Controller) reconcileRoot(ctx context.Context, ingress *networkingv1.Ingress) error {
	// is deleting
	if ingress.DeletionTimestamp != nil && !ingress.DeletionTimestamp.IsZero() {
		klog.Infof("deleting root ingress '%v'", ingress.Name)
		sel, err := labels.Parse(fmt.Sprintf("%s=%s", ownedByLabel, ingress.Name))
		if err != nil {
			return err
		}
		currentLeaves, err := c.lister.List(sel)
		if err != nil {
			return err
		}
		klog.Infof("found %v leaf ingresses", len(currentLeaves))
		for _, leaf := range currentLeaves {
			err = c.kubeClient.Cluster(leaf.ClusterName).NetworkingV1().Ingresses(leaf.Namespace).Delete(ctx, leaf.Name, metav1.DeleteOptions{})
			if err != nil {
				return err
			}
			// delete copied leaf secret
			host := ingress.Annotations[cluster.ANNOTATION_HCG_HOST]
			leafSecretName := getTLSSecretName(host, leaf)
			if leafSecretName != "" {
				if err := c.kubeClient.Cluster(leaf.ClusterName).CoreV1().Secrets(leaf.Namespace).Delete(ctx, leafSecretName, metav1.DeleteOptions{}); err != nil {
					return err
				}
			}
		}
		// delete DNSRecord
		err = c.dnsRecordClient.Cluster(ingress.ClusterName).KuadrantV1().DNSRecords(ingress.Namespace).Delete(ctx, ingress.Name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
		// delete any certificates
		if err := c.ensureCertificate(ctx, ingress); err != nil {
			return err
		}
		// all leaves removed, remove finalizer
		klog.Infof("'%v' ingress leaves cleaned up - removing finalizer", ingress.Name)
		removeFinalizer(ingress, cascadeCleanupFinalizer)

		c.hostsWatcher.StopWatching(ingressKey(ingress))
		for _, leaf := range currentLeaves {
			c.hostsWatcher.StopWatching(ingressKey(leaf))
		}

		return nil
	}

	AddFinalizer(ingress, cascadeCleanupFinalizer)
	if ingress.Annotations == nil || ingress.Annotations[cluster.ANNOTATION_HCG_HOST] == "" {
		// Let's assign it a global hostname if any
		generatedHost := fmt.Sprintf("%s.%s", xid.New(), *c.domain)
		patch := fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`, cluster.ANNOTATION_HCG_HOST, generatedHost)
		i, err := c.patchIngress(ctx, ingress, []byte(patch))
		if err != nil {
			return err
		}
		ingress = i
	}

	// if custom hosts are not enabled all the hosts in the ingress
	// will be replaced to the generated host
	if !*c.customHostsEnabled {
		err := c.replaceCustomHosts(ctx, ingress)
		if err != nil {
			return err
		}
	}

	// Get the current leaves
	sel, err := labels.Parse(fmt.Sprintf("%s=%s", ownedByLabel, ingress.Name))
	if err != nil {
		return err
	}
	currentLeaves, err := c.lister.List(sel)
	if err != nil {
		return err
	}
	// setup DNS
	if err := c.ensureDNS(ctx, ingress); err != nil {
		return err
	}
	// setup certificates
	if err := c.ensureCertificate(ctx, ingress); err != nil {
		return err
	}

	// Generate the desired leaves
	desiredLeaves, err := c.desiredLeaves(ctx, ingress)
	if err != nil {
		return err
	}

	// Delete the leaves that are not desired anymore
	for _, leftover := range findNonDesiredLeaves(currentLeaves, desiredLeaves) {
		// The clean-up, i.e., removal of the DNS records, will be done in the next reconciliation cycle
		klog.Infof("Deleting non desired leaf %q", leftover.Name)
		if err := c.kubeClient.Cluster(leftover.ClusterName).NetworkingV1().Ingresses(leftover.Namespace).Delete(ctx, leftover.Name, metav1.DeleteOptions{}); err != nil {
			return err
		}
	}

	// TODO(jmprusi): ugly. fix. use indexer, etc.
	// Create and/or update the desired leaves
	for _, leaf := range desiredLeaves {
		// before we create if tls is enabled we need to wait for the tls secret to be present
		if c.tlsEnabled {
			// copy root tls secret
			klog.Info("TLS is enabled copy tls secret for leaf ingress ", leaf.Name)
			if err := c.copyRootTLSSecretForLeafs(ctx, ingress, leaf); err != nil {
				return err
			}
		}
		if _, err := c.kubeClient.Cluster(leaf.ClusterName).NetworkingV1().Ingresses(leaf.Namespace).Create(ctx, leaf, metav1.CreateOptions{}); err != nil {
			if errors.IsAlreadyExists(err) {
				existingLeaf, err := c.kubeClient.Cluster(leaf.ClusterName).NetworkingV1().Ingresses(leaf.Namespace).Get(ctx, leaf.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}

				// Set the resourceVersion and UID to update the desired leaf.
				leaf.ResourceVersion = existingLeaf.ResourceVersion
				leaf.UID = existingLeaf.UID

				if _, err := c.kubeClient.Cluster(leaf.ClusterName).NetworkingV1().Ingresses(leaf.Namespace).Update(ctx, leaf, metav1.UpdateOptions{}); err != nil {
					return err
				}
			} else {
				return err
			}
		}
	}

	return nil
}

// ensureCertificate creates a certificate request for the root ingress into the control cluster
func (c *Controller) ensureCertificate(ctx context.Context, rootIngress *networkingv1.Ingress) error {
	if !c.tlsEnabled {
		klog.Info("tls support not enabled. not creating certificates")
		return nil
	}
	controlClusterContext, err := cluster.NewControlObjectMapper(rootIngress)
	if err != nil {
		return err
	}
	if rootIngress.DeletionTimestamp != nil && !rootIngress.DeletionTimestamp.IsZero() {
		if err := c.certProvider.Delete(ctx, controlClusterContext); err != nil {
			return err
		}
		return nil
	}
	err = c.certProvider.Create(ctx, controlClusterContext)
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	klog.Info("Patching Ingress With TLS ", rootIngress.Name)
	patch := fmt.Sprintf(`{"spec":{"tls":[{"hosts":[%q],"secretName":%q}]}}`, controlClusterContext.Host(), controlClusterContext.Name())
	if _, err := c.patchIngress(ctx, rootIngress, []byte(patch)); err != nil {
		klog.Info("failed to patch ingress *** ", err)
		return err
	}

	return nil
}

func getTLSSecretName(host string, ingress *networkingv1.Ingress) string {
	for _, tls := range ingress.Spec.TLS {
		for _, tlsHost := range tls.Hosts {
			if tlsHost == host {
				return tls.SecretName
			}
		}
	}
	return ""
}

func (c *Controller) copyRootTLSSecretForLeafs(ctx context.Context, root *networkingv1.Ingress, leaf *networkingv1.Ingress) error {
	host := root.Annotations[cluster.ANNOTATION_HCG_HOST]
	if host == "" {
		return fmt.Errorf("no host set yet cannot set up TLS")
	}
	var rootSecretName = getTLSSecretName(host, root)
	var leafSecretName = getTLSSecretName(host, leaf)

	if leafSecretName == "" || rootSecretName == "" {
		return fmt.Errorf("cannot copy secrets yet as secrets names not present")
	}
	secretClient := c.kubeClient.Cluster(root.ClusterName).CoreV1().Secrets(root.Namespace)
	// get the root secret
	rootSecret, err := secretClient.Get(ctx, rootSecretName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	leafSecret := rootSecret.DeepCopy()
	leafSecret.Name = leafSecretName
	leafSecret.Labels = map[string]string{}
	leafSecret.Labels[clusterLabel] = leaf.Labels[clusterLabel]
	leafSecret.Labels[ownedByLabel] = root.Name

	// Cleanup finalizers
	leafSecret.Finalizers = []string{}
	// Cleanup owner references
	leafSecret.OwnerReferences = []metav1.OwnerReference{}
	leafSecret.SetResourceVersion("")

	_, err = secretClient.Create(ctx, leafSecret, metav1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		ls, err := secretClient.Get(ctx, leafSecretName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		ls.Data = leafSecret.Data
		if _, err := secretClient.Update(ctx, ls, metav1.UpdateOptions{}); err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	return nil
}

func (c *Controller) ensureDNS(ctx context.Context, ingress *networkingv1.Ingress) error {
	if len(ingress.Status.LoadBalancer.Ingress) > 0 {
		// Start watching for address changes in the LBs hostnames
		for _, lbs := range ingress.Status.LoadBalancer.Ingress {
			if lbs.Hostname != "" {
				c.hostsWatcher.StartWatching(ctx, ingressKey(ingress), lbs.Hostname)
			}
		}

		// The ingress has been admitted, let's expose the local load-balancing point to the global LB.
		record, err := c.getDNSRecord(ctx, ingress.Annotations[cluster.ANNOTATION_HCG_HOST], ingress)
		if err != nil {
			return err
		}
		_, err = c.dnsRecordClient.Cluster(record.ClusterName).KuadrantV1().DNSRecords(record.Namespace).Create(ctx, record, metav1.CreateOptions{})
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				return err
			}
			data, err := json.Marshal(record)
			if err != nil {
				return err
			}
			_, err = c.dnsRecordClient.Cluster(record.ClusterName).KuadrantV1().DNSRecords(record.Namespace).Patch(ctx, record.Name, types.ApplyPatchType, data, metav1.PatchOptions{FieldManager: manager, Force: pointer.Bool(true)})
			if err != nil {
				return err
			}
		}
	} else {
		err := c.dnsRecordClient.Cluster(ingress.ClusterName).KuadrantV1().DNSRecords(ingress.Namespace).Delete(ctx, ingress.Name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (c *Controller) reconcileLeaf(ctx context.Context, rootName string, ingress *networkingv1.Ingress) error {
	// The leaf Ingress was updated, get others and aggregate status.
	sel, err := labels.Parse(fmt.Sprintf("%s=%s", ownedByLabel, rootName))
	if err != nil {
		return err
	}
	others, err := c.lister.List(sel)
	if err != nil {
		return err
	}

	// Get the rootIngress based on the labels.
	var rootIngress *networkingv1.Ingress

	rootIf, exists, err := c.indexer.Get(&v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			ClusterName: ingress.ClusterName,
			Namespace:   ingress.Namespace,
			Name:        rootName,
		},
	})
	if err != nil {
		return err
	}

	if !exists {
		klog.Infof("deleting orphaned leaf ingress '%v' of missing root ingress '%v'", ingress.Name, rootName)
		return c.kubeClient.Cluster(ingress.ClusterName).NetworkingV1().Ingresses(ingress.Namespace).Delete(ctx, ingress.Name, metav1.DeleteOptions{})
	}

	if ingress.DeletionTimestamp != nil && !ingress.DeletionTimestamp.IsZero() {
		// The Ingress is being deleted. KCP doesn't currently cascade deletion to owned resources
		removeFinalizer(ingress, cascadeCleanupFinalizer)
	}

	// Clean the current status, and then recreate if from the other leafs.
	rootIngress = rootIf.(*networkingv1.Ingress).DeepCopy()
	rootIngress.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{}
	for _, o := range others {
		// Should the root Ingress status be updated only once the DNS record is successfully created / updated?
		rootIngress.Status.LoadBalancer.Ingress = append(rootIngress.Status.LoadBalancer.Ingress, o.Status.LoadBalancer.Ingress...)
	}

	// Update the root Ingress status with our desired LB.
	if _, err := c.kubeClient.Cluster(rootIngress.ClusterName).NetworkingV1().Ingresses(rootIngress.Namespace).UpdateStatus(ctx, rootIngress, metav1.UpdateOptions{}); err != nil {
		if errors.IsConflict(err) {
			key, err := cache.MetaNamespaceKeyFunc(ingress)
			if err != nil {
				return err
			}
			c.queue.AddRateLimited(key)
			return nil
		}
		return err
	}

	return nil
}

// TODO may want to move this to its own package in the future
func (c *Controller) getDNSRecord(ctx context.Context, hostname string, ingress *networkingv1.Ingress) (*v1.DNSRecord, error) {
	var targets []string
	for _, lbs := range ingress.Status.LoadBalancer.Ingress {
		if lbs.Hostname != "" {
			// TODO: once we are adding tests abstract to interface
			ips, err := c.hostResolver.LookupIPAddr(ctx, lbs.Hostname)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				targets = append(targets, ip.IP.String())
			}
		}
		if lbs.IP != "" {
			targets = append(targets, lbs.IP)
		}
	}

	record := &v1.DNSRecord{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1.SchemeGroupVersion.String(),
			Kind:       "DNSRecord",
		},
		ObjectMeta: metav1.ObjectMeta{
			ClusterName: ingress.ClusterName,
			Namespace:   ingress.Namespace,
			Name:        ingress.Name,
		},
		Spec: v1.DNSRecordSpec{
			DNSName:    hostname,
			RecordType: "A",
			Targets:    targets,
			RecordTTL:  60,
		},
	}

	// Sets the Ingress as the owner reference
	record.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion:         networkingv1.SchemeGroupVersion.String(),
			Kind:               "Ingress",
			Name:               ingress.Name,
			UID:                ingress.UID,
			Controller:         pointer.Bool(true),
			BlockOwnerDeletion: pointer.Bool(true),
		},
	})

	return record, nil
}

func (c *Controller) desiredLeaves(ctx context.Context, root *networkingv1.Ingress) ([]*networkingv1.Ingress, error) {
	// This will parse the ingresses and extract all the destination services,
	// then create a new ingress leaf for each of them.
	services, err := c.getServices(ctx, root)
	if err != nil {
		return nil, err
	}

	var clusters []string
	for _, service := range services {
		if service.Labels[clusterLabel] != "" {
			clusters = append(clusters, service.Labels[clusterLabel])
		} else {
			klog.Infof("Skipping service %q because it is not assigned to any cluster", service.Name)
		}

		// Trigger reconciliation of the root ingress when this service changes.
		c.tracker.add(root, service)
	}

	desiredLeaves := make([]*networkingv1.Ingress, 0, len(clusters))
	for _, cl := range clusters {
		vd := root.DeepCopy()
		// TODO: munge cluster name
		vd.Name = fmt.Sprintf("%s--%s", root.Name, cl)

		vd.Labels = map[string]string{}
		vd.Labels[clusterLabel] = cl
		vd.Labels[ownedByLabel] = root.Name

		// Cleanup finalizers
		vd.Finalizers = []string{}
		// Cleanup owner references
		vd.OwnerReferences = []metav1.OwnerReference{}
		vd.SetResourceVersion("")
		if hostname, ok := root.Annotations[cluster.ANNOTATION_HCG_HOST]; ok {
			// Duplicate the existing rules for the global hostname
			if c.tlsEnabled {
				klog.Info("tls is enabled updating leaf ingress with secret name")
				for tlsIndex, tls := range root.Spec.TLS {
					// find the RH host
					for _, th := range tls.Hosts {
						if hostname == th {
							// set the tls section on the leaf at the right index. The secret will be created when the leaf is created
							vd.Spec.TLS[tlsIndex].SecretName = fmt.Sprintf("%s-tls", vd.Name)
						}
					}

				}
			}
			globalRules := make([]networkingv1.IngressRule, len(vd.Spec.Rules))
			for i, rule := range vd.Spec.Rules {
				r := rule.DeepCopy()
				r.Host = hostname
				globalRules[i] = *r
			}
			vd.Spec.Rules = append(vd.Spec.Rules, globalRules...)
		}

		desiredLeaves = append(desiredLeaves, vd)
	}
	klog.Infof("desired leaves generated %v ", len(desiredLeaves))
	return desiredLeaves, nil
}

// getServices will parse the ingress object and return a list of the services.
func (c *Controller) getServices(ctx context.Context, ingress *networkingv1.Ingress) ([]*corev1.Service, error) {
	var services []*corev1.Service
	for _, rule := range ingress.Spec.Rules {
		for _, path := range rule.HTTP.Paths {
			svc, err := c.kubeClient.Cluster(ingress.ClusterName).CoreV1().Services(ingress.Namespace).Get(ctx, path.Backend.Service.Name, metav1.GetOptions{})
			// TODO(jmprusi): If one of the services doesn't exist, we invalidate all the other ones.. review this.
			if err != nil {
				return nil, err
			}
			services = append(services, svc)
		}
	}
	return services, nil
}

func (c *Controller) patchIngress(ctx context.Context, ingress *networkingv1.Ingress, data []byte) (*networkingv1.Ingress, error) {
	return c.kubeClient.Cluster(ingress.ClusterName).NetworkingV1().Ingresses(ingress.Namespace).
		Patch(ctx, ingress.Name, types.MergePatchType, data, metav1.PatchOptions{FieldManager: manager})
}

func findNonDesiredLeaves(current, desired []*networkingv1.Ingress) []*networkingv1.Ingress {
	var missing []*networkingv1.Ingress

	for _, c := range current {
		found := false
		for _, d := range desired {
			if c.Name == d.Name {
				found = true
			}
		}
		if !found {
			missing = append(missing, c)
		}
	}

	return missing
}

func getRootName(ingress *networkingv1.Ingress) (rootName string, isLeaf bool) {
	if ingress.Labels != nil {
		rootName, isLeaf = ingress.Labels[ownedByLabel]
	}

	return
}

func AddFinalizer(ingress *networkingv1.Ingress, finalizer string) {
	for _, v := range ingress.Finalizers {
		if v == finalizer {
			return
		}
	}
	ingress.Finalizers = append(ingress.Finalizers, finalizer)
}

func removeFinalizer(ingress *networkingv1.Ingress, finalizer string) {
	for i, v := range ingress.Finalizers {
		if v == finalizer {
			ingress.Finalizers[i] = ingress.Finalizers[len(ingress.Finalizers)-1]
			ingress.Finalizers = ingress.Finalizers[:len(ingress.Finalizers)-1]
			return
		}
	}
}

func ingressKey(ingress *networkingv1.Ingress) interface{} {
	key, _ := cache.MetaNamespaceKeyFunc(ingress)
	return cache.ExplicitKey(key)
}

func (c *Controller) replaceCustomHosts(ctx context.Context, ingress *networkingv1.Ingress) error {
	generatedHost := ingress.Annotations[hostGeneratedAnnotation]
	hosts := []interface{}{}
	for i, rule := range ingress.Spec.Rules {
		if rule.Host != generatedHost {
			ingress.Spec.Rules[i].Host = generatedHost
			hosts = append(hosts, rule.Host)
		}
	}

	//TODO: TLS

	if len(hosts) > 0 {
		ingress.Annotations[customHostReplaced] = fmt.Sprintf(" replaced custom hosts ("+strings.Repeat("%s ", len(hosts))+") to the glbc host due to custom host policy not being allowed",
			hosts...)
		if _, err := c.kubeClient.Cluster(ingress.ClusterName).NetworkingV1().Ingresses(ingress.Namespace).Update(ctx, ingress, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}

	return nil
}
