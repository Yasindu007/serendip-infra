package provider

import (
	"maps"
	"sync"
	"time"
)

// Registry maps a capability name (e.g. "storage") to whatever live Go
// value its currently-active provider registered -- populated once, at
// cmd/serendipd startup, by code that imports both core/ and the
// specific providers/ package it wants active. core/ itself never
// imports a providers/ package (constitution rule 2); the composition
// root (cmd/) is where that import happens, not here.
type Registry struct {
	mu        sync.RWMutex
	impls     map[string]any
	readiness map[string]Readiness
}

// Readiness is a capability's live setup state, reported by the
// composition root while it sets providers up (and repairs their
// foundations) in the background. In-memory only, like the rest of this
// Registry. Reason is provider-neutral (e.g. "SettingUp", "SetupFailed",
// "Ready", "RepairFailed").
type Readiness struct {
	Ready   bool      `json:"ready"`
	Reason  string    `json:"reason"`
	Message string    `json:"message,omitempty"`
	Since   time.Time `json:"since"`
}

func NewRegistry() *Registry {
	return &Registry{impls: make(map[string]any), readiness: make(map[string]Readiness)}
}

// SetReadiness records capability's readiness. Since is stamped here, and
// only when Ready or Reason changes -- a retry loop re-reporting the same
// state doesn't move it, so Since reads as "in this state since".
func (r *Registry) SetReadiness(capability string, rd Readiness) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev, ok := r.readiness[capability]
	if ok && prev.Ready == rd.Ready && prev.Reason == rd.Reason {
		rd.Since = prev.Since
	} else {
		rd.Since = time.Now().UTC()
	}
	r.readiness[capability] = rd
}

// Readiness returns a copy of every capability's reported readiness.
func (r *Registry) Readiness() map[string]Readiness {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return maps.Clone(r.readiness)
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
