package dns

import (
	"context"

	v1 "github.com/kuadrant/kcp-ingress/pkg/apis/kuadrant/v1"
	"k8s.io/klog"
)

func (c *Controller) reconcile(ctx context.Context, dnsRecord *v1.DNSRecord) error {
	klog.Infof("reconciling DNSRecord %q", dnsRecord.Name)

	// TODO: Reconcile record to DNS server

	return nil
}
