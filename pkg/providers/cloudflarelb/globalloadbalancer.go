package cloudflarelb

import (
	"context"

	v1 "k8s.io/api/core/v1"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jmprusi/kcp-ingress/pkg/apis/globalloadbalancer/v1alpha1"

	"k8s.io/klog"
)

func (c *Controller) reconcile(ctx context.Context, glb *v1alpha1.GlobalLoadBalancer) error {
	klog.Infof("reconciling GlobalLoadBalancer %q", glb.Name)

	if glb.Status.Conditions.IsReady() {
		return nil
	}
	// Check if the glb has already been accepted, perhaps another thing should be to check for some annotation or
	// something in the spec that specifies the desired provider.
	if glb.Status.Conditions.IsAccepted() {

		if len(glb.Spec.Endpoints) != 0 {
			//TODO(jmprusi): This doesn't use the endpoints or create the LB, or the origins or so...
			assignedHostname, err := c.cloudflareConfig.CreateLBEntry(ctx, glb.Name+glb.Namespace, glb.Spec.Endpoints)
			if err != nil {
				return err
			}
			glb.Status.Hostname = assignedHostname
			glb.Status.SetConditionReady(v1.ConditionTrue, "Entry created and accepted", "working.")
			_, _ = c.networkingClient.NetworkingV1alpha1().GlobalLoadBalancers(glb.Namespace).UpdateStatus(ctx, glb, v12.UpdateOptions{})
		}

		glb.Status.SetConditionReady(v1.ConditionFalse, "no endpoints defined", "no endpoints have been defined")
		_, _ = c.networkingClient.NetworkingV1alpha1().GlobalLoadBalancers(glb.Namespace).UpdateStatus(ctx, glb, v12.UpdateOptions{})
		return nil

	} else {
		// Example of a validating a globalloadbalancer

		// If there's an empty hostname, we reject the globalloadbalancer object, as an example.
		// We could basically generate a random DNS instead... but then we will need to map the requested hostnames with the generated hostname.
		for _, hostname := range glb.Spec.RequestedHostnames {
			if hostname == "" {
				glb.Status.SetConditionAccepted(v1.ConditionFalse, "provider-rejected", "cloudflare-lb rejected due to empty hostname")
				_, _ = c.networkingClient.NetworkingV1alpha1().GlobalLoadBalancers(glb.Namespace).UpdateStatus(ctx, glb, v12.UpdateOptions{})
				return nil
			}
		}

		// We accept the globalloadbalancerobject.
		glb.Status.SetConditionAccepted(v1.ConditionTrue, "cloudflare-lb accepted", "valid globalloadbalancer")
		_, _ = c.networkingClient.NetworkingV1alpha1().GlobalLoadBalancers(glb.Namespace).UpdateStatus(ctx, glb, v12.UpdateOptions{})
	}

	return nil
}
