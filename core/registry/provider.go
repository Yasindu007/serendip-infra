package registry

import (
	"context"

	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// ProviderRegistry is a thin typed convenience layer over
// store.Store[ProviderSpec, ProviderStatus] -- registering a provider
// descriptor is an ordinary Create through the underlying store
// (docs/architecture/provider-model.md "Registration"). It does not
// execute health checks or instantiate providers -- that's the
// reconciliation engine's job (spec 003).
type ProviderRegistry struct {
	store store.Store[resource.ProviderSpec, resource.ProviderStatus]
}

func NewProviderRegistry(s store.Store[resource.ProviderSpec, resource.ProviderStatus]) *ProviderRegistry {
	return &ProviderRegistry{store: s}
}

// Register creates a new Provider descriptor: which capabilities it
// implements, its declared resource requirements, and its health-check
// contract (stored as data only -- nothing here calls it).
func (r *ProviderRegistry) Register(ctx context.Context, name string, capabilities []string, requirements resource.ResourceRequirements, healthCheck resource.HealthCheckContract) (resource.Provider, error) {
	return r.store.Create(ctx, resource.Provider{
		Metadata: resource.Metadata{Name: name, Kind: resource.KindProvider},
		Spec: resource.ProviderSpec{
			Capabilities:         capabilities,
			ResourceRequirements: requirements,
			HealthCheck:          healthCheck,
		},
	})
}

// ByName finds a registered Provider by its logical name (see
// CapabilityRegistry.findByName's doc comment -- same rationale).
func (r *ProviderRegistry) ByName(ctx context.Context, name string) (resource.Provider, error) {
	all, err := r.store.List(ctx, store.ListOptions{})
	if err != nil {
		return resource.Provider{}, err
	}
	for _, p := range all {
		if p.Metadata.Name == name {
			return p, nil
		}
	}
	return resource.Provider{}, store.ErrNotFound
}
