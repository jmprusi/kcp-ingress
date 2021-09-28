package ingress

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"

	"k8s.io/apimachinery/pkg/api/errors"

	corev1 "k8s.io/api/core/v1"

	"github.com/jmprusi/kcp-ingress/pkg/apis/globalloadbalancer/v1alpha1"

	v1 "k8s.io/api/networking/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog"
)

const (
	clusterLabel = "kcp.dev/cluster"
	ownedByLabel = "kcp.dev/owned-by"
	pollInterval = time.Minute
)

func (c *Controller) reconcile(ctx context.Context, ingress *v1.Ingress) error {
	klog.Infof("reconciling Ingress %q", ingress.Name)

	if ingress.Labels == nil || ingress.Labels[clusterLabel] == "" {
		// This is a root Ingress; get its leaves.
		sel, err := labels.Parse(fmt.Sprintf("%s=%s", ownedByLabel, ingress.Name))
		if err != nil {
			return err
		}

		leaves, err := c.lister.List(sel)
		if err != nil {
			return err
		}

		// If there are no leaves, let's check for the GlobalLoadBalancer Object.
		if len(leaves) == 0 {

			// Now let's try to get the current GlobalLoadBalancer if it exist,
			// so we can actually get the providers status.
			currentGlb, err := c.glbClient.GlobalLoadBalancers(ingress.Namespace).Get(ctx, ingress.Name, metav1.GetOptions{})

			if err != nil && errors.IsNotFound(err) {
				// GlobalBalancer doesn't exist, we need to create it.
				desiredGlb := c.IngressToGLB(nil, ingress)
				_, err = c.glbClient.GlobalLoadBalancers(ingress.Namespace).Create(ctx, desiredGlb, metav1.CreateOptions{})

				return err

			} else if err != nil {
				return err
			}

			if currentGlb.Status.Conditions.IsReady() {
				// If the Glb object has the Accepted condition, we create the leaves ingresses
				// by using the Hostname that the provided set.
				// TODO(jmprusi): ADD STATUS TO INGRESS FROM GLB
			}
			// If we are here, the GlobalLoadBalancer already exists,
			// let's extract its information an update our Ingress object.
			if currentGlb.Status.Conditions.IsAccepted() {
				// If the Glb object has the Accepted condition, we create the leaves ingresses
				// by using the Hostname that the provided set.
				if err := c.createLeaves(ctx, ingress, currentGlb.Status.Hostname); err != nil {
					return err
				}
			}
		}

	} else {

		rootIngressName := ingress.Labels[ownedByLabel]
		// A leaf Ingress was updated; get others and generate the GlobalLoadBalancer object.
		sel, err := labels.Parse(fmt.Sprintf("%s=%s", ownedByLabel, rootIngressName))
		if err != nil {
			return err
		}
		leafs, err := c.lister.List(sel)
		if err != nil {
			return err
		}

		var rootIngress *v1.Ingress

		rootIf, exists, err := c.indexer.Get(&v1.Ingress{
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
			return fmt.Errorf("ingress has been deleted")
		}

		rootIngress = rootIf.(*v1.Ingress)

		desiredGlb := c.IngressToGLB(leafs, rootIngress)

		_, err = c.glbClient.GlobalLoadBalancers(rootIngress.Namespace).Create(ctx, desiredGlb, metav1.CreateOptions{})

		if err != nil && errors.IsAlreadyExists(err) {
			klog.Infof("GlobalLoadBalancer for ingress %s already exists, updating.", rootIngress.Name)
			currentGlb, err := c.glbClient.GlobalLoadBalancers(rootIngress.Namespace).Get(ctx, rootIngressName, metav1.GetOptions{})
			if err != nil {
				return err
			}

			if !equality.Semantic.DeepEqual(desiredGlb, currentGlb) {
				// Update the already existing GlB with our desired values
				currentGlb.Spec.RequestedHostnames = desiredGlb.Spec.RequestedHostnames
				currentGlb.Spec.Endpoints = desiredGlb.Spec.Endpoints

				_, err = c.glbClient.GlobalLoadBalancers(rootIngress.Namespace).Update(ctx, currentGlb, metav1.UpdateOptions{})
				if err != nil {
					return err
				}
			}
		} else if err != nil {
			return err
		}
	}

	return nil
}

func (c *Controller) IngressToGLB(leaves []*v1.Ingress, rootIngress *v1.Ingress) *v1alpha1.GlobalLoadBalancer {

	var endpoints []v1alpha1.Endpoint
	if len(leaves) != 0 {
		endpoints = getLeafsEndpoints(leaves)
	}

	// Reconcile the GlobalLoadBalancer object
	glb := &v1alpha1.GlobalLoadBalancer{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rootIngress.Name,
			Namespace: rootIngress.Namespace,
		},
		Spec: v1alpha1.GlobalLoadBalancerSpec{
			RequestedHostnames: getHostnames(rootIngress),
			Endpoints:          endpoints,
		},
		Status: v1alpha1.GlobalLoadBalancerStatus{},
	}

	// Add Owner reference.
	references := glb.GetOwnerReferences()
	references = append(references, metav1.OwnerReference{
		APIVersion: "networking.k8s.io/v1",
		Kind:       "Ingress",
		Name:       rootIngress.Name,
		UID:        rootIngress.UID,
	})
	glb.SetOwnerReferences(references)

	return glb
}

func getHostnames(ingress *v1.Ingress) []string {
	var hostnames []string

	// TODO(jmprusi): Handle empty hostnames...
	for _, rule := range ingress.Spec.Rules {
		hostnames = append(hostnames, rule.Host)
	}

	return hostnames
}

func getLeafsEndpoints(leafs []*v1.Ingress) []v1alpha1.Endpoint {
	// Let's extract all the endpoints populated by the physical cluster ingresses from the status fields and create the
	//GlobalLoadBalancer object.

	// TODO(jmprusi): Handle empty stuff
	var endpoints []v1alpha1.Endpoint
	for _, o := range leafs {
		for _, ingress := range o.Status.LoadBalancer.Ingress {
			endpoint := v1alpha1.Endpoint{
				IP:       ingress.IP,
				Hostname: ingress.Hostname,
			}
			endpoints = append(endpoints, endpoint)
		}
	}
	return endpoints
}

func (c *Controller) createLeaves(ctx context.Context, root *v1.Ingress, assignedHostname string) error {
	cls, err := c.clusterLister.List(labels.Everything())
	if err != nil {
		return err
	}

	if len(cls) == 0 {
		// No status conditions... let's just leave it blank for now.
		root.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
			IP:       "",
			Hostname: "",
		}}
		return nil
	}

	if len(cls) == 1 {
		// nothing to split, just label Ingress for the only cluster.
		if root.Labels == nil {
			root.Labels = map[string]string{}
		}

		// TODO: munge cluster name
		root.Labels[clusterLabel] = cls[0].Name
		return nil
	}

	for _, cl := range cls {
		vd := root.DeepCopy()
		// TODO: munge cluster name
		vd.Name = fmt.Sprintf("%s--%s", root.Name, cl.Name)

		if vd.Labels == nil {
			vd.Labels = map[string]string{}
		}
		vd.Labels[clusterLabel] = cl.Name
		vd.Labels[ownedByLabel] = root.Name

		// TODO(jmprusi): GarbageCollection is not running on KCP...
		// Set OwnerReference so deleting the root Ingress deletes all virtual Ingresses
		vd.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "networking.k8s.io/v1beta1",
			Kind:       "Ingress",
			Name:       root.Name,
			UID:        root.UID,
		}}
		// TODO: munge namespace
		vd.SetResourceVersion("")

		// Create the Ingress
		if _, err := c.kubeClient.NetworkingV1().Ingresses(root.Namespace).Create(ctx, vd,
			metav1.CreateOptions{}); err != nil {
			return err
		}
		klog.Infof("created virtual Ingress %q", vd.Name)
	}

	return nil
}
