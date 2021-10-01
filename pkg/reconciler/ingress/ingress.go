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
		leafs, err := c.lister.List(sel)
		if err != nil {
			return err
		}

		if len(leafs) == 0 {
			if err := c.createLeafs(ctx, ingress); err != nil {
				return err
			}
		}

	} else {
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

		if !exists {
			return fmt.Errorf("Root Ingress not found: %s", rootIngressName)
		}

		rootIngress = rootIf.(*networkingv1.Ingress)

		// Aggregating all the status from all the leafs for now.
		// but we should just reflect the DNS returned by the global load balancer.
		rootIngress = rootIngress.DeepCopy()

		// Clean the current status, and then recreate if from the other leafs.
		rootIngress.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{}
		for _, o := range others {
			rootIngress.Status.LoadBalancer.Ingress = append(rootIngress.Status.LoadBalancer.Ingress, o.Status.LoadBalancer.Ingress...)
		}

		// If the envoy controlplane is enabled, we update the cache and generate/send to envoy a new snapshot.
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

// TODO(jmprusi): Parse the ingresses and find out in which cluster the destination service is.
func (c *Controller) createLeafs(ctx context.Context, root *networkingv1.Ingress) error {
	cls, err := c.clusterLister.List(labels.Everything())
	if err != nil {
		return err
	}

	if len(cls) == 0 {
		// No status conditions... let's just leave it blank for now.
		root.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{{
			IP:       "",
			Hostname: "",
		}}
		return nil
	}

	for _, cl := range cls {
		vd := root.DeepCopy()
		// TODO: munge cluster name
		vd.Name = fmt.Sprintf("%s--%s", root.Name, cl.Name)

		vd.Labels = map[string]string{}
		vd.Labels[clusterLabel] = cl.Name
		vd.Labels[ownedByLabel] = root.Name

		// Cleanup all the other owner references.
		// TODO(jmprusi): Right now the syncer is syncing the OwnerReferences causing the ingresses to be deleted.
		vd.OwnerReferences = []metav1.OwnerReference{}
		vd.SetResourceVersion("")

		if _, err := c.kubeClient.NetworkingV1().Ingresses(root.Namespace).Create(ctx, vd, metav1.CreateOptions{}); err != nil {
			return err
		}
		klog.Infof("created virtual Ingress %q", vd.Name)
	}

	return nil
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
