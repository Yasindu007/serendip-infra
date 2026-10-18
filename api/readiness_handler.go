package api

import (
	"net/http"

	"github.com/serendip-infra/serendip-infra/core/provider"
)

// ReadinessHandler serves GET /v1/readiness: each capability's live setup
// state as the composition root reports it (core/provider.Readiness). Read
// only, in-memory, so no store call and nothing to audit. Distinct from
// GET /healthz, which stays pure process liveness for supervisors.
type ReadinessHandler struct {
	registry *provider.Registry
}

// NewReadinessHandler returns a handler over registry. A nil registry
// reports no capabilities (an empty object), not an error.
func NewReadinessHandler(registry *provider.Registry) *ReadinessHandler {
	return &ReadinessHandler{registry: registry}
}

func (h *ReadinessHandler) Register(mux Mux) {
	mux.HandleFunc("GET /v1/readiness", h.Get)
}

// Get writes {capability: {ready, reason, message, since}}. encoding/json
// sorts map keys, so the output order is stable.
func (h *ReadinessHandler) Get(w http.ResponseWriter, r *http.Request) {
	out := map[string]provider.Readiness{}
	if h.registry != nil {
		out = h.registry.Readiness()
	}
	writeJSON(w, http.StatusOK, out)
}
