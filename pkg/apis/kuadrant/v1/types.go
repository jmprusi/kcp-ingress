package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DNSRecord is a DNS record managed by the HCG.
type DNSRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the specification of the desired behavior of the dnsRecord.
	Spec DNSRecordSpec `json:"spec"`
	// status is the most recently observed status of the dnsRecord.
	Status DNSRecordStatus `json:"status"`
}

// DNSRecordSpec contains the details of a DNS record.
type DNSRecordSpec struct {
	// dnsName is the hostname of the DNS record
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +required
	DNSName string `json:"dnsName"`
	// targets are record targets.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +required
	Targets []string `json:"targets"`
	// recordType is the DNS record type. For example, "A" or "CNAME".
	// +kubebuilder:validation:Required
	// +required
	RecordType DNSRecordType `json:"recordType"`
	// recordTTL is the record TTL in seconds. If zero, the default is 30.
	// RecordTTL will not be used in AWS regions Alias targets, but
	// will be used in CNAME targets, per AWS API contract.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +required
	RecordTTL int64 `json:"recordTTL"`
}

// DNSRecordStatus is the most recently observed status of each record.
type DNSRecordStatus struct {
	// zones are the status of the record in each zone.
	Zones []DNSZoneStatus `json:"zones,omitempty"`

	// observedGeneration is the most recently observed generation of the
	// DNSRecord.  When the DNSRecord is updated, the controller updates the
	// corresponding record in each managed zone.  If an update for a
	// particular zone fails, that failure is recorded in the status
	// condition for the zone so that the controller can determine that it
	// needs to retry the update for that specific zone.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// DNSZone is used to define a DNS hosted zone.
// A zone can be identified by an ID or tags.
type DNSZone struct {
	// id is the identifier that can be used to find the DNS hosted zone.
	//
	// on AWS zone can be fetched using `ID` as id in [1]
	// on Azure zone can be fetched using `ID` as a pre-determined name in [2],
	// on GCP zone can be fetched using `ID` as a pre-determined name in [3].
	//
	// [1]: https://docs.aws.amazon.com/cli/latest/reference/route53/get-hosted-zone.html#options
	// [2]: https://docs.microsoft.com/en-us/cli/azure/network/dns/zone?view=azure-cli-latest#az-network-dns-zone-show
	// [3]: https://cloud.google.com/dns/docs/reference/v1/managedZones/get
	// +optional
	ID string `json:"id,omitempty"`

	// tags can be used to query the DNS hosted zone.
	//
	// on AWS, resourcegroupstaggingapi [1] can be used to fetch a zone using `Tags` as tag-filters,
	//
	// [1]: https://docs.aws.amazon.com/cli/latest/reference/resourcegroupstaggingapi/get-resources.html#options
	// +optional
	Tags map[string]string `json:"tags,omitempty"`
}

// DNSZoneStatus is the status of a record within a specific zone.
type DNSZoneStatus struct {
	// dnsZone is the zone where the record is published.
	DNSZone DNSZone `json:"dnsZone"`
	// conditions are any conditions associated with the record in the zone.
	//
	// If publishing the record fails, the "Failed" condition will be set with a
	// reason and message describing the cause of the failure.
	Conditions []DNSZoneCondition `json:"conditions,omitempty"`
}

var (
	// Failed means the record is not available within a zone.
	DNSRecordFailedConditionType = "Failed"
)

// DNSZoneCondition is just the standard condition fields.
type DNSZoneCondition struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +required
	Type string `json:"type"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +required
	Status             string      `json:"status"`
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	Reason             string      `json:"reason,omitempty"`
	Message            string      `json:"message,omitempty"`
}

// DNSRecordType is a DNS resource record type.
// +kubebuilder:validation:Enum=CNAME;A
type DNSRecordType string

const (
	// CNAMERecordType is an RFC 1035 CNAME record.
	CNAMERecordType DNSRecordType = "CNAME"

	// ARecordType is an RFC 1035 A record.
	ARecordType DNSRecordType = "A"
)

// +kubebuilder:object:root=true

// DNSRecordList contains a list of dnsrecords.
type DNSRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DNSRecord `json:"items"`
}
