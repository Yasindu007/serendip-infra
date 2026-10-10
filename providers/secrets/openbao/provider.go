package openbao

import (
	"context"
	"errors"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// Provider implements core/reconcile.SecretsProvider by wrapping a
// *Client -- translating a resource.Secret's Spec.Path (core/resource
// never says "mount", that's this package's own concept) into the
// ReadSecret/WriteSecret/DeleteSecret calls Client already has (spec
// 008). No new wire-protocol code -- this is composition, not a new
// client. Mirrors providers/storage/localpath.Provider's naming and
// placement exactly.
type Provider struct {
	client *Client
}

// NewProvider builds a Provider around client. Every Secret this
// Provider touches lives under DefaultKVMount -- this spec has no
// per-Secret mount selection (ADR 0009/spec 008 established one
// deployment-wide KV v2 mount; multiple mounts is out of scope).
func NewProvider(client *Client) *Provider {
	return &Provider{client: client}
}

var _ reconcile.SecretsProvider = (*Provider)(nil)

// Exists reports whether a value currently exists at sec.Spec.Path,
// mapping ErrSecretNotFound to (false, nil) -- "doesn't exist" is the
// expected, non-error outcome for a Secret that's never been written
// to, not a failure.
func (p *Provider) Exists(ctx context.Context, sec resource.Secret) (bool, error) {
	_, err := p.client.ReadSecret(ctx, DefaultKVMount, sec.Spec.Path)
	if errors.Is(err, ErrSecretNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (p *Provider) WriteValue(ctx context.Context, sec resource.Secret, value map[string]any) error {
	return p.client.WriteSecret(ctx, DefaultKVMount, sec.Spec.Path, value)
}

func (p *Provider) ReadValue(ctx context.Context, sec resource.Secret) (map[string]any, error) {
	return p.client.ReadSecret(ctx, DefaultKVMount, sec.Spec.Path)
}

func (p *Provider) Delete(ctx context.Context, sec resource.Secret) error {
	return p.client.DeleteSecret(ctx, DefaultKVMount, sec.Spec.Path)
}
