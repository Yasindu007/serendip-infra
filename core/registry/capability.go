package registry

import (
	"context"
	"fmt"
	"slices"

	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// CapabilityRegistry is a thin typed convenience layer over
// store.Store[CapabilitySpec, CapabilityStatus] -- registering a
// Capability, or switching its default provider, is an ordinary Create/
// Update through the underlying store; nothing here is a separate storage
// mechanism (docs/architecture/capability-model.md "Capability spec
// format", specs/001-resource-model/plan.md).
type CapabilityRegistry struct {
	store store.Store[resource.CapabilitySpec, resource.CapabilityStatus]
}

func NewCapabilityRegistry(s store.Store[resource.CapabilitySpec, resource.CapabilityStatus]) *CapabilityRegistry {
	return &CapabilityRegistry{store: s}
}

// Register creates a new Capability: whether a fresh install can run
// without it, which providers are registered for it, and which one is
// the default.
func (r *CapabilityRegistry) Register(ctx context.Context, name string, required bool, providers []string, defaultProvider string) (resource.Capability, error) {
	return r.store.Create(ctx, resource.Capability{
		Metadata: resource.Metadata{Name: name, Kind: resource.KindCapability},
		Spec: resource.CapabilitySpec{
			Required:  required,
			Providers: providers,
			Default:   defaultProvider,
		},
	})
}

// Default returns the provider name currently configured as default for
// the named capability.
func (r *CapabilityRegistry) Default(ctx context.Context, name string) (string, error) {
	cap, err := r.findByName(ctx, name)
	if err != nil {
		return "", err
	}
	return cap.Spec.Default, nil
}

// SetDefault switches which provider is used by default for a capability.
// This is an ordinary Update call on registry data -- acceptance
// criterion 3 (specs/001-resource-model/spec.md): "registering two
// providers for one capability and switching the default requires no
// changes outside registry data." Rejects switching to a provider that
// isn't registered for this capability.
func (r *CapabilityRegistry) SetDefault(ctx context.Context, name, provider string) error {
	cap, err := r.findByName(ctx, name)
	if err != nil {
		return err
	}
	if !slices.Contains(cap.Spec.Providers, provider) {
		return fmt.Errorf("registry: provider %q is not registered for capability %q", provider, name)
	}
	cap.Spec.Default = provider
	_, err = r.store.Update(ctx, cap)
	return err
}

// findByName looks up a Capability by its logical name (e.g. "identity"),
// since store.Store.Get only indexes by generated ID and callers of a
// capability registry naturally think in names, not IDs
// (capability-model.md's catalog is name-keyed). Nothing in spec 001
// enforces Capability name uniqueness; the first match wins, matching
// resource-model.md's stated latitude that field-level enforcement is an
// implementation detail decided as needed.
func (r *CapabilityRegistry) findByName(ctx context.Context, name string) (resource.Capability, error) {
	all, err := r.store.List(ctx, store.ListOptions{})
	if err != nil {
		return resource.Capability{}, err
	}
	for _, c := range all {
		if c.Metadata.Name == name {
			return c, nil
		}
	}
	return resource.Capability{}, store.ErrNotFound
}
