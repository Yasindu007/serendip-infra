package provider

import "sync"

// Registry maps a capability name (e.g. "storage") to whatever live Go
// value its currently-active provider registered -- populated once, at
// cmd/serendipd startup, by code that imports both core/ and the
// specific providers/ package it wants active. core/ itself never
// imports a providers/ package (constitution rule 2); the composition
// root (cmd/) is where that import happens, not here.
type Registry struct {
	mu    sync.RWMutex
	impls map[string]any
}

func NewRegistry() *Registry {
	return &Registry{impls: make(map[string]any)}
}

// Register associates impl with capability, replacing whatever was
// previously registered for it.
func (r *Registry) Register(capability string, impl any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.impls[capability] = impl
}

// Lookup returns the implementation registered for capability, or
// (nil, false) if nothing is registered -- callers must treat that as
// "capability not configured yet," not panic or error loudly, mirroring
// core/reconcile.ErrNotConfigured's existing precedent for "nothing to
// do yet" states. Returns any because this package is capability-
// agnostic by design (core/provider/CLAUDE.md) -- callers type-assert
// to whatever interface their own capability expects.
func (r *Registry) Lookup(capability string) (any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	impl, ok := r.impls[capability]
	return impl, ok
}
