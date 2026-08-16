package store

import (
	"context"
	"errors"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// ErrNotFound is returned by Get/Update/Delete when no resource matches
// the given id.
var ErrNotFound = errors.New("store: resource not found")

// ErrConflict is returned by Update when the caller's ResourceVersion
// doesn't match the currently stored one (optimistic concurrency).
var ErrConflict = errors.New("store: resource version conflict")

// ListOptions filters Store.List. Label matching is exact-match only in
// spec 001 -- full Kubernetes-style set-based selectors (in/notin/exists)
// are deferred until a real consumer needs them
// (specs/001-resource-model/plan.md open-question resolution).
type ListOptions struct {
	Kind    resource.Kind
	OwnerID string
	Labels  map[string]string
}

// Store is the generic persistence interface every resource kind uses,
// instantiated per kind (e.g. Store[resource.OrganizationSpec,
// resource.OrganizationStatus]). See core/store/sqlite for the default
// implementation (ADR 0003) and specs/001-resource-model/plan.md for why
// audit and graph-edge sync happen inside the same transaction as every
// mutation here, rather than as an optional wrapper.
type Store[S, St any] interface {
	Get(ctx context.Context, id string) (resource.Resource[S, St], error)
	List(ctx context.Context, opts ListOptions) ([]resource.Resource[S, St], error)
	Create(ctx context.Context, r resource.Resource[S, St]) (resource.Resource[S, St], error)
	// Update requires r.Metadata.ResourceVersion to match the currently
	// stored value; mismatch returns ErrConflict.
	Update(ctx context.Context, r resource.Resource[S, St]) (resource.Resource[S, St], error)
	// Delete rejects removing a resource that has "owns" children unless
	// cascade is true (docs/architecture/resource-model.md "Relationships").
	Delete(ctx context.Context, id string, cascade bool) error
}
