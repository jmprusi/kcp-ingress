package cloudflaredns

import (
	"context"
	"fmt"

	"github.com/jmprusi/kcp-ingress/pkg/apis/globalloadbalancer/v1alpha1"

	"github.com/cloudflare/cloudflare-go"
)

type CloudflareConfig struct {
	RootDomain string
	Client     *cloudflare.API
}

func (cfg *CloudflareConfig) getZone(ctx context.Context) (string, error) {
	zones, err := cfg.Client.ListZones(ctx)
	if err != nil {
		return "", err
	}
	zoneID := ""

	for _, zone := range zones {
		if zone.Name == cfg.RootDomain {
			zoneID = zone.ID
		}
	}

	if zoneID == "" {
		return "", fmt.Errorf("domain: %s has no defined zone in your cloudflare account", cfg.RootDomain)
	}

	return zoneID, nil
}

func (cfg *CloudflareConfig) CreateDNSEntry(ctx context.Context, subdomain string, endpoints []v1alpha1.Endpoint) (string, error) {

	zoneID, err := cfg.getZone(ctx)
	if err != nil {
		return "", err
	}

	for _, endpoint := range endpoints {
		// What if the endpoint is a hostname instead of IP? let's reject it for now.
		if endpoint.IP == "" {
			return "", fmt.Errorf("endpoint IP not found, not valid for this provider")
		}
		// Only handling IPv4 ips for now.
		_, err = cfg.Client.CreateDNSRecord(ctx, zoneID,
			cloudflare.DNSRecord{
				Type:    "A",
				Name:    subdomain,
				Content: endpoint.IP,
				Proxied: &[]bool{false}[0], // Not proxied by clouflare
			})
		if err != nil {
			return "", err
		}
	}
	return subdomain + "." + cfg.RootDomain, nil
}
