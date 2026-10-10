package api

import (
	"encoding/json"
	"net/http"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// SecretHandler is the first api/ handler to depend on a core/reconcile
// type instead of only core/store (api/CLAUDE.md's documented status
// quo until now) -- a deliberate one-off, not a new general pattern:
// a Secret's value can never live in Spec/Status (core/resource/
// secrets.go's doc comment), so the generic ResourceHandler[S,St]
// (which only ever reads/writes whole resources through store.Store)
// cannot be how a value gets set, read, or cleaned up on delete. That
// orchestration -- fetch the Secret to get its Path, call the
// SecretsProvider, then (for writes) write back through the store --
// is business logic (api/CLAUDE.md: "no business logic" in handlers),
// so it lives on *reconcile.SecretReconciler, not here. This handler
// only ever decodes, calls one of SecretReconciler's three
// synchronous methods, and encodes.
type SecretHandler struct {
	*ResourceHandler[resource.SecretSpec, resource.SecretStatus]
	reconciler *reconcile.SecretReconciler
}

// NewSecretHandler returns a handler for the Secret kind. The four
// plain-CRUD routes (Create/Get/List/Update) delegate to an embedded
// *ResourceHandler -- reuse, not reimplementation, since those really
// are plain Spec/Status CRUD, nothing provider-specific.
func NewSecretHandler(s store.Store[resource.SecretSpec, resource.SecretStatus], r *reconcile.SecretReconciler) *SecretHandler {
	return &SecretHandler{
		ResourceHandler: NewResourceHandler[resource.SecretSpec, resource.SecretStatus](s, "secrets"),
		reconciler:      r,
	}
}

// Register wires the 5 ordinary CRUD routes (DELETE overridden below)
// plus the two value routes:
//
//	POST /v1/secrets/{id}/value   Write a value through to the provider
//	GET  /v1/secrets/{id}/value   Read the current value from the provider
func (h *SecretHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/secrets", h.Create)
	mux.HandleFunc("GET /v1/secrets", h.List)
	mux.HandleFunc("GET /v1/secrets/{id}", h.Get)
	mux.HandleFunc("PUT /v1/secrets/{id}", h.Update)
	mux.HandleFunc("DELETE /v1/secrets/{id}", h.Delete)
	mux.HandleFunc("POST /v1/secrets/{id}/value", h.WriteValue)
	mux.HandleFunc("GET /v1/secrets/{id}/value", h.ReadValue)
}

// Delete shadows the embedded ResourceHandler's store-only Delete --
// it must remove the value from the provider before the Serendip-side
// record disappears (reconcile.SecretReconciler.DeleteWithValue's own
// doc comment explains why this can't be deferred to the reconcile
// loop the way VolumeReconciler's delete-cascade is).
func (h *SecretHandler) Delete(w http.ResponseWriter, r *http.Request) {
	cascade := r.URL.Query().Get("cascade") == "true"
	if err := h.reconciler.DeleteWithValue(r.Context(), r.PathValue("id"), cascade); err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// secretValueBody mirrors providers/secrets/openbao's own KV v2 wire
// shape ({"data": {...}}) for consistency, even though this is a
// different layer (Serendip's own API, not OpenBao's).
type secretValueBody struct {
	Data map[string]any `json:"data"`
}

func (h *SecretHandler) WriteValue(w http.ResponseWriter, r *http.Request) {
	var body secretValueBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "malformed request body", err.Error())
		return
	}
	updated, err := h.reconciler.WriteValue(r.Context(), r.PathValue("id"), body.Data)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *SecretHandler) ReadValue(w http.ResponseWriter, r *http.Request) {
	data, err := h.reconciler.ReadValue(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, secretValueBody{Data: data})
}
