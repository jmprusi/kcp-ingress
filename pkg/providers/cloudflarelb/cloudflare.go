package cloudflarelb

import (
	"context"
	"crypto/sha1"
	"fmt"
	"strings"

	"github.com/jmprusi/kcp-ingress/pkg/apis/globalloadbalancer/v1alpha1"

	"github.com/cloudflare/cloudflare-go"
)

type CloudflareConfig struct {
	LBDomain string
	Client   *cloudflare.API
}

func (cfg *CloudflareConfig) getZone(ctx context.Context) (string, error) {
	zones, err := cfg.Client.ListZones(ctx)
	if err != nil {
		return "", err
	}
	zoneID := ""
	splitHost := strings.Split(cfg.LBDomain, ".")
	rootDomain := strings.Join(splitHost[1:], ".")

	for _, zone := range zones {
		if zone.Name == rootDomain {
			zoneID = zone.ID
		}
	}

	if zoneID == "" {
		return "", fmt.Errorf("domain: %s has no defined zone in your cloudflare account", rootDomain)
	}

	return zoneID, nil
}

func (cfg *CloudflareConfig) hashString(input string) string {
	h := sha1.New()
	h.Write([]byte(input))
	bs := h.Sum(nil)
	return fmt.Sprintf("%x\n", bs)
}

func (cfg *CloudflareConfig) CreateLBEntry(ctx context.Context, hostname string, endpoints []v1alpha1.Endpoint) (string, error) {

	zoneID, err := cfg.getZone(ctx)
	if err != nil {
		return "", err
	}

	generatedHostname := cfg.hashString(hostname)

	_, err = cfg.Client.CreateDNSRecord(ctx, zoneID,
		cloudflare.DNSRecord{
			Type:    "CNAME",
			Name:    generatedHostname,
			Content: cfg.LBDomain,
			Proxied: &[]bool{true}[0],
		})
	if err != nil {
		return "", err
	}

	return generatedHostname + "." + cfg.LBDomain, nil

}

//
//import (
//	"fmt"
//	"log"
//	"os"
//
//	"github.com/cloudflare/cloudflare-go"
//)
//
//type Cloudflare struct {
//	api      *cloudflare.API
//	APIToken string
//	Domain   string
//}
//
//func New(globalDomain, cloudflareAPIToken string) loadbalancer.LBProvider {
//
//	api, err := cloudflare.New(os.Getenv("CF_API_KEY"), os.Getenv("CF_API_EMAIL"))
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	zones, err := cld.api.ListZones(ctx)
//	if err != nil {
//		return err
//	}
//	zoneID := ""
//
//	for _, zone := range zones {
//		if zone.Name == cld.Domain {
//			zoneID = zone.ID
//		}
//	}
//
//	if zoneID == "" {
//		return fmt.Errorf("Domain: %s has no defined zone in your cloudflare account", cld.Domain)
//	}
//
//	cld.api.CreateDNSRecord(ctx, zoneID, cloudflare.DNSRecord{
//		ID:      zoneID,
//		Type:    "CNAME",
//		Name:    domain,
//		Content: "glb.obs.io",
//		Proxied: func(b bool) *bool { return &b }(true),
//		ZoneID:  zoneID,
//	})
//	return nil
//}
