package ingressSyncing

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/jmprusi/kcp-ingress/pkg/envoy"
	"github.com/kcp-dev/kcp/pkg/syncer"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog"
	envoyserver "knative.dev/net-kourier/pkg/envoy/server"
)

const (
	transformerClass    = "ingress-transformer"
	locationsAnnotation = "kcp.dev/assigned-locations"
	clusterLabel        = "kcp.dev/cluster"
	ownedByLabel        = "kcp.dev/owned-by"
	numThreads          = 2
)

type SyncingConfig struct {
	Kubeconfig      *api.Config
	EnvoyXDS        *envoyserver.XdsServer
	Domain          *string
	EnvoyListenPort *uint
}

func NewIngressSyncing(config *SyncingConfig) *syncer.Syncer {

	ingressSyncing := &ingressSyncing{}

	currentContext := config.Kubeconfig.CurrentContext

	// Create the upstream (public) and the downstream (private) kubeconfigs.
	kubeConfigPublic := switchConfigContext(config.Kubeconfig, "public")
	kubeConfigPrivate := switchConfigContext(config.Kubeconfig, "private")

	upstreamConfig, err := clientcmd.NewNonInteractiveClientConfig(*kubeConfigPublic, currentContext, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		klog.Fatal(err)
	}
	downstreamConfig, err := clientcmd.NewNonInteractiveClientConfig(*kubeConfigPrivate, currentContext, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		klog.Fatal(err)
	}

	inSyncing := syncer.NewDelegateSyncing(syncer.NewIdentitySyncing(nil))

	// Signal to KCP that it should use our syncing to handle Ingress resources.
	inSyncing.Delegate(networkingv1.SchemeGroupVersion.WithResource("ingresses"), ingressSyncing)

	// Only handle Ingress resources
	syncerBuilder, err := syncer.BuildCustomSyncerToPrivateLogicalCluster(transformerClass, inSyncing)
	if err != nil {
		klog.Fatal(err)
	}

	syncer, err := syncerBuilder(upstreamConfig, downstreamConfig, sets.NewString("ingresses"), numThreads)
	if err != nil {
		klog.Fatal(err)
	}

	if config.EnvoyXDS != nil {
		ingressSyncing.envoyXDS = config.EnvoyXDS
		ingressSyncing.cache = envoy.NewCache(envoy.NewTranslator(config.EnvoyListenPort))
		ingressSyncing.domain = config.Domain
		ingressSyncing.envoyListenPort = config.EnvoyListenPort

		go func() {
			snapshot := ingressSyncing.cache.ToEnvoySnapshot()
			_ = ingressSyncing.envoyXDS.SetSnapshot(envoy.NodeID, snapshot)
			err := ingressSyncing.envoyXDS.RunManagementServer()
			if err != nil {
				panic(err)
			}
		}()
	}
	return syncer
}

type ingressSyncing struct {
	envoyXDS        *envoyserver.XdsServer
	cache           *envoy.Cache
	domain          *string
	envoyListenPort *uint
}

func (is *ingressSyncing) UpsertIntoDownstream() syncer.UpsertFunc {
	return func(c *syncer.Controller, ctx context.Context, gvr schema.GroupVersionResource, namespace string, unstrob *unstructured.Unstructured, labelsToAdd map[string]string) error {
		var ingress networkingv1.Ingress
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstrob.UnstructuredContent(), &ingress)
		if err != nil {
			return err
		}

		toClient := c.GetClient(gvr, ingress.GetNamespace())

		assignedLocationsAnnot, exists := ingress.GetAnnotations()["kcp.dev/assigned-locations"]
		if !exists {
			return nil
		}
		var assignedLocations []string
		for _, location := range strings.Split(assignedLocationsAnnot, ",") {
			location = strings.TrimSpace(location)
			if location != "" {
				assignedLocations = append(assignedLocations, location)
			}
		}

		for _, location := range assignedLocations {
			vd := ingress.DeepCopy()

			// TODO: munge cluster name
			vd.Name = fmt.Sprintf("%s--%s", ingress.Name, location)

			if vd.Labels == nil {
				vd.Labels = map[string]string{}
			}
			vd.Labels[clusterLabel] = location
			vd.Labels[ownedByLabel] = ingress.Name

			vd.SetResourceVersion("")
			vd.SetUID("")
			vdUnstr := unstructured.Unstructured{}
			unstrContent, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&vd)
			if err != nil {
				return err
			}
			vdUnstr.SetUnstructuredContent(unstrContent)

			if _, err := toClient.Create(ctx, &vdUnstr, metav1.CreateOptions{}); err != nil {
				if !errors.IsAlreadyExists(err) {
					return err
				}
				var existing *unstructured.Unstructured
				existing, err = toClient.Get(ctx, vdUnstr.GetName(), metav1.GetOptions{})
				if err != nil {
					return err
				}
				vdUnstr.SetResourceVersion(existing.GetResourceVersion())
				vdUnstr.SetUID(existing.GetUID())
				if _, err := toClient.Update(ctx, &vdUnstr, metav1.UpdateOptions{}); err != nil {
					return err
				}
			}
			klog.Infof("created child ingress %q", vd.Name)
		}

		return nil
	}
}

func (is *ingressSyncing) DeleteFromDownstream() syncer.DeleteFunc {
	return func(c *syncer.Controller, ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) error {
		toClient := c.GetClient(gvr, namespace)
		sel, err := labels.Parse(fmt.Sprintf("%s=%s", ownedByLabel, name))
		if err != nil {
			return err
		}
		if err := toClient.DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
			LabelSelector: sel.String(),
		}); err != nil {
			return err
		}

		if is.envoyXDS != nil {
			// TODO(jmprusi): This should include the location information to avoid clashes
			is.cache.DeleteIngress(name, namespace)
			err = is.envoyXDS.SetSnapshot(envoy.NodeID, is.cache.ToEnvoySnapshot())
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func (is *ingressSyncing) UpdateStatusInUpstream() syncer.UpdateStatusFunc {
	return func(c *syncer.Controller, ctx context.Context, gvr schema.GroupVersionResource, namespace string, unstrob *unstructured.Unstructured) (notFound bool, err error) {
		var ingress networkingv1.Ingress
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstrob.UnstructuredContent(), &ingress)
		if err != nil {
			return false, err
		}

		toClient := c.GetClient(gvr, ingress.GetNamespace())

		rootIngressName := ingress.Labels[ownedByLabel]
		// A leaf deployment was updated; get others and aggregate status.
		sel, err := labels.Parse(fmt.Sprintf("%s=%s", ownedByLabel, rootIngressName))
		if err != nil {
			return false, err
		}
		others, err := c.GetFromLister(gvr, namespace).List(sel)
		if err != nil {
			return false, err
		}

		rootIngressUnstr, err := toClient.Get(ctx, rootIngressName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			klog.Errorf("Getting resource %s/%s: %v", namespace, rootIngressName, err)
			return false, err
		}
		var rootIngress networkingv1.Ingress
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(rootIngressUnstr.UnstructuredContent(), &rootIngress)
		if err != nil {
			return false, err
		}

		// Aggregate .status from all leafs.
		rootIngress.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{}

		for _, o := range others {
			var child *networkingv1.Ingress
			switch typed := o.(type) {
			case *unstructured.Unstructured:
				child = &networkingv1.Ingress{}
				err = runtime.DefaultUnstructuredConverter.FromUnstructured(typed.UnstructuredContent(), child)
				if err != nil {
					return false, err
				}
			case *networkingv1.Ingress:
				child = typed
			default:
				return false, fmt.Errorf("type mismatch, expected unstructured or ingress, got: %v", o)
			}

			rootIngress.Status.LoadBalancer.Ingress = append(rootIngress.Status.LoadBalancer.Ingress, child.Status.LoadBalancer.Ingress...)
		}

		// If the envoy controlplane is enabled, we update the cache and generate and send to envoy a new snapshot.
		if is.envoyXDS != nil {
			is.cache.UpdateIngress(rootIngress)
			err = is.envoyXDS.SetSnapshot(envoy.NodeID, is.cache.ToEnvoySnapshot())
			if err != nil {
				return false, err
			}

			statusHost := generateStatusHost(is.domain, &rootIngress)
			// Now overwrite the Status of the rootIngress with our desired LB
			rootIngress.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{{
				Hostname: statusHost,
			}}
		}

		unstrContent, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&rootIngress)
		if err != nil {
			return false, err
		}

		rootIngressUnstr.SetUnstructuredContent(unstrContent)

		if _, err := toClient.UpdateStatus(ctx, rootIngressUnstr, metav1.UpdateOptions{}); err != nil {
			return false, err
		}

		return false, nil
	}
}

func (ingressSyncing) LabelsToAdd() map[string]string {
	return nil
}

func switchConfigContext(kubeconfig *api.Config, contextName string) *api.Config {
	newKubeconfig := kubeconfig.DeepCopy()

	server := ""
	cluster := ""
	if _, exists := newKubeconfig.Contexts[newKubeconfig.CurrentContext]; exists {
		cluster = newKubeconfig.Contexts[newKubeconfig.CurrentContext].Cluster
		if _, exists := newKubeconfig.Clusters[cluster]; exists {
			server = newKubeconfig.Clusters[cluster].Server
		}
	}

	// TODO(jmprusi): Handle not existing cluster/context.

	if contextName == "public" {
		if !strings.HasSuffix(server, "clusters/"+cluster) {
			server = server + "/clusters/admin"
		}
		newKubeconfig.Clusters[cluster].Server = server
	} else if contextName == "private" {
		if !strings.HasSuffix(server, "clusters/"+cluster) {
			server = server + "/clusters/_admin_"
		} else {
			server = strings.Replace(server, "/clusters/"+cluster, "/clusters/_"+cluster+"_", -1)
		}
	}

	newKubeconfig.Clusters[cluster].Server = server
	return newKubeconfig
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

func hashString(s string) string {
	h := fnv.New32a()
	h.Write([]byte(s))
	return fmt.Sprint(h.Sum32())
}
