package aws

import (
	"fmt"
	"strings"

	"github.com/kuadrant/kcp-ingress/pkg/apis/kuadrant/v1"
	"github.com/kuadrant/kcp-ingress/pkg/dns"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/route53"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog"
)

const (
	// chinaRoute53Endpoint is the Route 53 service endpoint used for AWS China regions.
	chinaRoute53Endpoint = "https://route53.amazonaws.com.cn"
)

var (
	_ dns.Provider = &Provider{}
)

// Inspired by https://github.com/openshift/cluster-ingress-operator/blob/master/pkg/dns/aws/dns.go
type Provider struct {
	route53 *route53.Route53
	config  Config
}

// Config is the necessary input to configure the manager.
type Config struct {
	// Region is the AWS region ELBs are created in.
	Region string
}

func NewProvider(config Config) (*Provider, error) {
	var region string
	if len(config.Region) > 0 {
		region = config.Region
		klog.Infof("using region from operator config region name %s", region)
	}

	sess, err := session.NewSession(&aws.Config{Region: aws.String(region)})
	if err != nil {
		return nil, fmt.Errorf("couldn't create AWS client session: %v", err)
	}

	r53Config := aws.NewConfig()

	// If the region is in aws china, cn-north-1 or cn-northwest-1, we should:
	// 1. hard code route53 api endpoint to https://route53.amazonaws.com.cn and region to "cn-northwest-1"
	//    as route53 is not GA in AWS China and aws sdk didn't have the endpoint.
	// 2. use the aws china region cn-northwest-1 to setup tagging api correctly instead of "us-east-1"
	switch region {
	case endpoints.CnNorth1RegionID, endpoints.CnNorthwest1RegionID:
		r53Config = r53Config.WithRegion(endpoints.CnNorthwest1RegionID).WithEndpoint(chinaRoute53Endpoint)
	case endpoints.UsGovEast1RegionID, endpoints.UsGovWest1RegionID:
		// Route53 for GovCloud uses the "us-gov-west-1" region id:
		// https://docs.aws.amazon.com/govcloud-us/latest/UserGuide/using-govcloud-endpoints.html
		r53Config = r53Config.WithRegion(endpoints.UsGovWest1RegionID)
	case endpoints.UsIsoEast1RegionID:
		// Do not override the region in C2s
		r53Config = r53Config.WithRegion(region)
	default:
		// Use us-east-1 for Route 53 in AWS Regions other than China or GovCloud Regions.
		// See https://docs.aws.amazon.com/general/latest/gr/r53.html for details.
		r53Config = r53Config.WithRegion(endpoints.UsEast1RegionID)
	}
	p := &Provider{
		route53: route53.New(sess, r53Config),
		config:  config,
	}
	if err := validateServiceEndpoints(p); err != nil {
		return nil, fmt.Errorf("failed to validate aws provider service endpoints: %v", err)
	}
	return p, nil
}

// validateServiceEndpoints validates that provider clients can communicate with
// associated API endpoints by having each client make a list/describe/get call.
func validateServiceEndpoints(provider *Provider) error {
	var errs []error
	zoneInput := route53.ListHostedZonesInput{MaxItems: aws.String("1")}
	if _, err := provider.route53.ListHostedZones(&zoneInput); err != nil {
		errs = append(errs, fmt.Errorf("failed to list route53 hosted zones: %v", err))
	}
	return kerrors.NewAggregate(errs)
}

type action string

const (
	upsertAction action = "UPSERT"
	deleteAction action = "DELETE"
)

func (m *Provider) Ensure(record *v1.DNSRecord, zone v1.DNSZone) error {
	return m.change(record, zone, upsertAction)
}

func (m *Provider) Delete(record *v1.DNSRecord, zone v1.DNSZone) error {
	return m.change(record, zone, deleteAction)
}

// change will perform an action on a record.
func (m *Provider) change(record *v1.DNSRecord, zone v1.DNSZone, action action) error {
	if record.Spec.RecordType != v1.ARecordType {
		return fmt.Errorf("unsupported record type %s", record.Spec.RecordType)
	}
	domain, targets := record.Spec.DNSName, record.Spec.Targets
	if len(domain) == 0 {
		return fmt.Errorf("domain is required")
	}
	if len(targets) == 0 {
		return fmt.Errorf("targets is required")
	}

	// Configure records.
	err := m.updateRecord(domain, zone.ID, string(action), targets, record.Spec.RecordTTL)
	if err != nil {
		return fmt.Errorf("failed to update alias in zone %s: %v", zone.ID, err)
	}
	switch action {
	case upsertAction:
		klog.Infof("upserted DNS record %v, zone %v", record.Spec, zone)
	case deleteAction:
		klog.Infof("deleted DNS record %v, zone %v", record.Spec, zone)
	}
	return nil
}

func (m *Provider) updateRecord(domain, zoneID, action string, targets []string, ttl int64) error {
	input := route53.ChangeResourceRecordSetsInput{HostedZoneId: aws.String(zoneID)}

	var resourceRecords []*route53.ResourceRecord
	for _, target := range targets {
		resourceRecords = append(resourceRecords, &route53.ResourceRecord{Value: aws.String(target)})
	}

	input.ChangeBatch = &route53.ChangeBatch{
		Changes: []*route53.Change{
			{
				Action: aws.String(action),
				ResourceRecordSet: &route53.ResourceRecordSet{
					Name:            aws.String(domain),
					Type:            aws.String(route53.RRTypeA),
					TTL:             aws.Int64(ttl),
					ResourceRecords: resourceRecords,
				},
			},
		},
	}
	resp, err := m.route53.ChangeResourceRecordSets(&input)
	if err != nil {
		if action == string(deleteAction) {
			if aerr, ok := err.(awserr.Error); ok {
				if strings.Contains(aerr.Message(), "not found") {
					klog.Infof("record not found: zone id: %s, domain: %s, targets: %s", zoneID, domain, targets)
					return nil
				}
			}
		}
		return fmt.Errorf("couldn't update DNS record in zone %s: %v", zoneID, err)
	}
	klog.Infof("updated DNS record: zone id: %s, domain: %s, targets: %s, resp: %v", zoneID, domain, targets, resp)
	return nil
}
