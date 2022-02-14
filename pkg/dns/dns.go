package dns

import (
	v1 "github.com/kuadrant/kcp-ingress/pkg/apis/kuadrant/v1"
)

// Provider knows how to manage DNS zones only as pertains to routing.
type Provider interface {
	// Ensure will create or update record.
	Ensure(record *v1.DNSRecord, zone v1.DNSZone) error

	// Delete will delete record.
	Delete(record *v1.DNSRecord, zone v1.DNSZone) error
}

var _ Provider = &FakeProvider{}

type FakeProvider struct{}

func (_ *FakeProvider) Ensure(record *v1.DNSRecord, zone v1.DNSZone) error { return nil }
func (_ *FakeProvider) Delete(record *v1.DNSRecord, zone v1.DNSZone) error { return nil }
