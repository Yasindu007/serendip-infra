package api

import (
	"encoding/json"
	"net/http"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/registry"
)

// RegistryHandler exposes core/registry.CapabilityRegistry's get/set
// default-provider operation over HTTP (specs/002-http-api/spec.md
// Scope-IN item 2). Capability/Provider register+list are already covered
// by the generic ResourceHandler mounted at /v1/capabilities and
// /v1/providers -- POST there IS Register, GET there IS List, both
// ordinary Create/List calls per CapabilityRegistry's and
// ProviderRegistry's own doc comments. This handler exists only for the
// one operation the generic CRUD path cannot correctly perform:
// CapabilityRegistry.SetDefault enforces "the new default must already be
// a registered provider for this capability" -- a domain rule the generic
// PUT /v1/capabilities/{id} has no way to apply, since it talks to
// store.Store directly and has no reason to know about that rule.
type RegistryHandler struct {
	capabilities *registry.CapabilityRegistry
}

func NewRegistryHandler(c *registry.CapabilityRegistry) *RegistryHandler {
	return &RegistryHandler{capabilities: c}
}

// Register wires GET/PUT /v1/capabilities/{name}/default. {name} is the
// Capability's logical Metadata.Name (e.g. "identity"), not its
// generated ID -- matching how CapabilityRegistry itself is keyed
// (findByName), since capability-model.md's catalog is name-keyed.
func (h *RegistryHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/capabilities/{name}/default", h.getDefault)
	mux.HandleFunc("PUT /v1/capabilities/{name}/default", h.setDefault)
}

type defaultProviderBody struct {
	Provider string `json:"provider"`
}

func (h *RegistryHandler) getDefault(w http.ResponseWriter, r *http.Request) {
	provider, err := h.capabilities.Default(r.Context(), r.PathValue("name"))
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, defaultProviderBody{Provider: provider})
}

func (h *RegistryHandler) setDefault(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body defaultProviderBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteProblem(w, http.StatusBadRequest, "malformed request body", err.Error(), audit.RequestIDFrom(r.Context()))
		return
	}
	if err := h.capabilities.SetDefault(r.Context(), name, body.Provider); err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, defaultProviderBody{Provider: body.Provider})
}

func (h *RegistryHandler) writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	status, title := MapStoreError(err)
	WriteProblem(w, status, title, err.Error(), audit.RequestIDFrom(r.Context()))
}
