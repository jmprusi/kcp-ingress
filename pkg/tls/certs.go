package tls

import (
	"context"
)

type Provider interface {
	Create(ctx context.Context, cr CertificateRequest) error
	Delete(ctx context.Context, cr CertificateRequest) error
	Initialize(ctx context.Context) error
}

type CertificateRequest interface {
	Name() string
	Labels() map[string]string
	Annotations() map[string]string
	Host() string
}

type FakeProvider struct{}

var _ Provider = &FakeProvider{}

func (p *FakeProvider) Create(_ context.Context, _ CertificateRequest) error {
	return nil
}
func (p *FakeProvider) Delete(_ context.Context, _ CertificateRequest) error {
	return nil
}

func (p *FakeProvider) Initialize(_ context.Context) error {
	return nil
}
