package api

import (
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

// NewSecretHandler returns a handler for the Secret kind. Get and List
// delegate to an embedded *ResourceHandler. Create and Update are its own,
// to validate the path and keep it fixed (spec 017 T011). Delete is the
// ordinary one: the delete's Approval records the Secret, and the
// approval reconciler erases its value afterwards (spec 019).
func NewSecretHandler(s store.Store[resource.SecretSpec, resource.SecretStatus], r *reconcile.SecretReconciler) *SecretHandler {
	return &SecretHandler{
		ResourceHandler: NewResourceHandler[resource.SecretSpec, resource.SecretStatus](s, "secrets"),
		reconciler:      r,
	}
}

// Register wires the 5 ordinary CRUD routes plus the two value routes:
//
//	POST /v1/secrets/{id}/value   Write a value through to the provider
//	GET  /v1/secrets/{id}/value   Read the current value from the provider
func (h *SecretHandler) Register(mux Mux) {
	mux.HandleFunc("POST /v1/secrets", h.Create)
	mux.HandleFunc("GET /v1/secrets", h.List)
	mux.HandleFunc("GET /v1/secrets/{id}", h.Get)
	mux.HandleFunc("PUT /v1/secrets/{id}", h.Update)
	mux.HandleFunc("DELETE /v1/secrets/{id}", h.Delete)
	mux.HandleFunc("POST /v1/secrets/{id}/value", h.WriteValue)
	mux.HandleFunc("GET /v1/secrets/{id}/value", h.ReadValue)
}

// Create validates the path before storing the Secret.
func (h *SecretHandler) Create(w http.ResponseWriter, r *http.Request) {
	var sec resource.Secret
	if !decodeBody(w, r, &sec) {
		return
	}
	if err := resource.ValidateSecretSpec(sec.Spec); err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	// The server names a Secret: its ID is part of where its value is
	// stored, so a client-chosen one could aim at another Secret's value.
	sec.Metadata.ID = ""
	created, err := h.store.Create(r.Context(), sec)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// Update validates the path and refuses to change it: the value is stored
// under the Secret's identity and path, so a new path would orphan it.
func (h *SecretHandler) Update(w http.ResponseWriter, r *http.Request) {
	var sec resource.Secret
	if !decodeBody(w, r, &sec) {
		return
	}
	sec.Metadata.ID = r.PathValue("id")
	if err := resource.ValidateSecretSpec(sec.Spec); err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	current, err := h.store.Get(r.Context(), sec.Metadata.ID)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	if current.Spec.Path != sec.Spec.Path {
		h.writeError(w, r, http.StatusBadRequest, "validation failed", "a Secret's path can't change; create a new Secret instead")
		return
	}
	updated, err := h.store.Update(r.Context(), sec)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// secretValueBody mirrors providers/secrets/openbao's own KV v2 wire
// shape ({"data": {...}}) for consistency, even though this is a
// different layer (Serendip's own API, not OpenBao's).
type secretValueBody struct {
	Data map[string]any `json:"data"`
}

func (h *SecretHandler) WriteValue(w http.ResponseWriter, r *http.Request) {
	var body secretValueBody
	if !decodeBody(w, r, &body) {
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
