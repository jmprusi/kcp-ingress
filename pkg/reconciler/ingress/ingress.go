package ingress

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/jmprusi/kcp-ingress/pkg/envoy"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/api/networking/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

const (
	clusterLabel = "kcp.dev/cluster"
	ownedByLabel = "kcp.dev/owned-by"
	pollInterval = time.Minute
)

func (c *Controller) reconcile(ctx context.Context, ingress *networkingv1.Ingress) error {
	klog.Infof("reconciling Ingress %q", ingress.Name)

	if ingress.Labels == nil || ingress.Labels[clusterLabel] == "" {
		// This is a root Ingress; get its leafs.
		sel, err := labels.Parse(fmt.Sprintf("%s=%s", ownedByLabel, ingress.Name))
		if err != nil {
			return err
		}

		// Get the current Leaves
		currentLeaves, err := c.lister.List(sel)
		if err != nil {
			return err
		}

		// Generate the desired leaves
		desiredLeaves, err := c.desiredLeaves(ctx, ingress)
		if err != nil {
			return err
		}

		// Clean the leaves that are not desired anymore
		for _, leaftoremove := range findNonDesiredLeaves(currentLeaves, desiredLeaves) {
			klog.Infof("Deleting non desired leaf %q", leaftoremove.Name)
			if err := c.kubeClient.NetworkingV1().Ingresses(leaftoremove.Namespace).Delete(ctx, leaftoremove.Name, metav1.DeleteOptions{}); err != nil {
				return err
			}
		}

		// TODO(jmprusi): ugly. fix. use indexer, etc.
		// Create and/or update the desired leaves
		for _, desiredleaf := range desiredLeaves {
			if _, err := c.kubeClient.NetworkingV1().Ingresses(desiredleaf.Namespace).Create(ctx, desiredleaf, metav1.CreateOptions{}); err != nil {
				if errors.IsAlreadyExists(err) {
					existingLeaf, err := c.kubeClient.NetworkingV1().Ingresses(desiredleaf.Namespace).Get(ctx, desiredleaf.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}

					// Set the resourceVersion and UID to update the desired leaf.
					desiredleaf.ResourceVersion = existingLeaf.ResourceVersion
					desiredleaf.UID = existingLeaf.UID

					if _, err := c.kubeClient.NetworkingV1().Ingresses(desiredleaf.Namespace).Update(ctx, desiredleaf, metav1.UpdateOptions{}); err != nil {
						return err
					}

				} else {
					return err
				}
			}
		}

	} else {
		// If the ingress has the clusterLabel set, that means that it is a leaf and it's synced with
		// a cluster.
		//
		// This update can come from the creation or because the syncer has update the status.

		rootIngressName := ingress.Labels[ownedByLabel]
		// A leaf Ingress was updated; get others and aggregate status.
		sel, err := labels.Parse(fmt.Sprintf("%s=%s", ownedByLabel, rootIngressName))
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
				Namespace:   ingress.Namespace,
				Name:        rootIngressName,
				ClusterName: ingress.GetClusterName(),
			},
		})
		if err != nil {
			return err
		}

		// TODO(jmprusi): A leaf without rootIngress?
		if !exists {
			return fmt.Errorf("Root Ingress not found: %s", rootIngressName)
		}

		rootIngress = rootIf.(*networkingv1.Ingress).DeepCopy()

		// Clean the current status, and then recreate if from the other leafs.
		rootIngress.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{}
		for _, o := range others {
			rootIngress.Status.LoadBalancer.Ingress = append(rootIngress.Status.LoadBalancer.Ingress, o.Status.LoadBalancer.Ingress...)
		}

		// If the envoy controlplane is enabled, we update the cache and generate and send to envoy a new snapshot.
		if c.envoyXDS != nil {
			c.cache.UpdateIngress(*rootIngress)
			err = c.envoyXDS.SetSnapshot(envoy.NodeID, c.cache.ToEnvoySnapshot())
			if err != nil {
				return err
			}

			statusHost := generateStatusHost(c.domain, rootIngress)
			// Now overwrite the Status of the rootIngress with our desired LB
			rootIngress.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{{
				Hostname: statusHost,
			}}
		}

		// Update the rootIngress status with our desired LB.
		if _, err := c.client.Ingresses(rootIngress.Namespace).UpdateStatus(ctx, rootIngress, metav1.UpdateOptions{}); err != nil {
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
	}
	return nil
}

func (c *Controller) desiredLeaves(ctx context.Context, root *networkingv1.Ingress) ([]*networkingv1.Ingress, error) {
	// This will parse the ingresses and extract all the destination services,
	// then create a new ingress leaf for each of them.
	services, err := c.getServices(ctx, root)
	if err != nil {
		return nil, err
	}

	var clusterDests []string
	for _, service := range services {
		if service.Labels[clusterLabel] != "" {
			clusterDests = append(clusterDests, service.Labels[clusterLabel])
		} else {
			klog.Infof("Skipping service %q because it is not assigned to any cluster", service.Name)
		}

		// Trigger reconciliation of the root ingress when this service changes.
		c.tracker.add(root, service)
	}

	if len(clusterDests) == 0 {
		// No status conditions... let's just leave it blank for now.
		root.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{{
			IP:       "",
			Hostname: "",
		}}
		return nil, nil
	}

	desiredLeaves := make([]*networkingv1.Ingress, 0, len(clusterDests))
	for _, cl := range clusterDests {
		vd := root.DeepCopy()
		// TODO: munge cluster name
		vd.Name = fmt.Sprintf("%s--%s", root.Name, cl)

		vd.Labels = map[string]string{}
		vd.Labels[clusterLabel] = cl
		vd.Labels[ownedByLabel] = root.Name

		// Cleanup all the other owner references.
		// TODO(jmprusi): Right now the syncer is syncing the OwnerReferences causing the ingresses to be deleted.
		vd.OwnerReferences = []metav1.OwnerReference{}
		vd.SetResourceVersion("")

		desiredLeaves = append(desiredLeaves, vd)
	}

	return desiredLeaves, nil
}

func hashString(s string) string {
	h := fnv.New32a()
	h.Write([]byte(s))
	return fmt.Sprint(h.Sum32())
}

func generateStatusHost(domain *string, ingress *networkingv1.Ingress) string {

	// TODO(jmprusi): using "contains" is a bad idea as it could be abused by crafting a malicious hostname, but for a PoC it should be good enough?
	allRulesAreDomain := true
	for _, rule := range ingress.Spec.Rules {
		if !strings.Contains(rule.Host, *domain) {
			allRulesAreDomain = false
			break
		}
	}

	//TODO(jmprusi): Hardcoded to the first one...
	if allRulesAreDomain {
		return ingress.Spec.Rules[0].Host
	}

	return hashString(ingress.Name+ingress.Namespace+ingress.ClusterName) + "." + *domain
}

// getServices will parse the ingress object and return a list of the services.
func (c *Controller) getServices(ctx context.Context, ingress *networkingv1.Ingress) ([]*v1.Service, error) {
	var services []*v1.Service
	for _, rule := range ingress.Spec.Rules {
		for _, path := range rule.HTTP.Paths {
			svc, err := c.kubeClient.CoreV1().Services(ingress.Namespace).Get(ctx, path.Backend.Service.Name, metav1.GetOptions{})
			// TODO(jmprusi): If one of the services doesn't exist, we invalidate all the other ones.. review this.
			if err != nil {
				return nil, err
			}
			services = append(services, svc)
		}
	}
	return services, nil
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
