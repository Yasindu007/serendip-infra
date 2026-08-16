package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// ResourceHandler is a generic REST-CRUD handler for one resource kind --
// mirrors core/store/sqlite.Store[S, St]'s pattern (one type, one
// instantiation per kind, api/server.go/T012 does the 14 instantiations).
// Handlers stay thin per api/CLAUDE.md: decode -> core call -> encode, no
// business logic here.
//
// Routes registered by Register:
//
//	POST   /v1/{path}          Create
//	GET    /v1/{path}          List (?ownerId=, ?label.<key>=<value>)
//	GET    /v1/{path}/{id}     Get
//	PUT    /v1/{path}/{id}     Update (full replace; body's Metadata.ID is
//	                           overridden by the path, ResourceVersion must
//	                           match the currently stored value)
//	DELETE /v1/{path}/{id}     Delete (?cascade=true)
type ResourceHandler[S, St any] struct {
	store store.Store[S, St]
	path  string
}

// NewResourceHandler returns a handler for one resource kind. path is the
// URL path segment (e.g. "organizations"), not a resource.Kind value.
func NewResourceHandler[S, St any](s store.Store[S, St], path string) *ResourceHandler[S, St] {
	return &ResourceHandler[S, St]{store: s, path: path}
}

// Register wires this handler's 5 routes onto mux using Go 1.22+
// pattern-based net/http.ServeMux routing -- no router library.
func (h *ResourceHandler[S, St]) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/"+h.path, h.Create)
	mux.HandleFunc("GET /v1/"+h.path, h.List)
	mux.HandleFunc("GET /v1/"+h.path+"/{id}", h.Get)
	mux.HandleFunc("PUT /v1/"+h.path+"/{id}", h.Update)
	mux.HandleFunc("DELETE /v1/"+h.path+"/{id}", h.Delete)
}

func (h *ResourceHandler[S, St]) Create(w http.ResponseWriter, r *http.Request) {
	var res resource.Resource[S, St]
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "malformed request body", err.Error())
		return
	}
	created, err := h.store.Create(r.Context(), res)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *ResourceHandler[S, St]) Get(w http.ResponseWriter, r *http.Request) {
	got, err := h.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// List supports ?ownerId=<id> and ?label.<key>=<value> (repeated for
// multiple labels), matching exactly what core/store.ListOptions already
// supports -- nothing more (specs/002-http-api/plan.md open question 4).
func (h *ResourceHandler[S, St]) List(w http.ResponseWriter, r *http.Request) {
	opts := store.ListOptions{OwnerID: r.URL.Query().Get("ownerId")}

	labels := make(map[string]string)
	const labelPrefix = "label."
	for key, values := range r.URL.Query() {
		if after, ok := strings.CutPrefix(key, labelPrefix); ok && len(values) > 0 {
			labels[after] = values[0]
		}
	}
	if len(labels) > 0 {
		opts.Labels = labels
	}

	list, err := h.store.List(r.Context(), opts)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *ResourceHandler[S, St]) Update(w http.ResponseWriter, r *http.Request) {
	var res resource.Resource[S, St]
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "malformed request body", err.Error())
		return
	}
	// The path's {id} is authoritative over whatever the body claims --
	// standard REST semantics for PUT /resource/{id}.
	res.Metadata.ID = r.PathValue("id")

	updated, err := h.store.Update(r.Context(), res)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *ResourceHandler[S, St]) Delete(w http.ResponseWriter, r *http.Request) {
	cascade := r.URL.Query().Get("cascade") == "true"
	if err := h.store.Delete(r.Context(), r.PathValue("id"), cascade); err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ResourceHandler[S, St]) writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	status, title := MapStoreError(err)
	h.writeError(w, r, status, title, err.Error())
}

func (h *ResourceHandler[S, St]) writeError(w http.ResponseWriter, r *http.Request, status int, title, detail string) {
	WriteProblem(w, status, title, detail, audit.RequestIDFrom(r.Context()))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
