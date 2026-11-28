package api

import (
	"context"
	"encoding/json"
	"errors"
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
	// validate, when set, checks a spec on create and update; id is ""
	// on create. Its error answers 400 (wrap resource.ErrValidation).
	validate func(ctx context.Context, id string, spec S) error
	// checkMeta, when set, checks metadata on create and update, before
	// validate. Its error answers 400 (wrap resource.ErrValidation).
	checkMeta func(m resource.Metadata) error
	// prepare, when set, checks the whole resource on create and update,
	// after checkMeta and validate, and may set what's derived from its
	// spec (its depends-on relationships). Its error answers 400 (wrap
	// resource.ErrValidation).
	prepare func(r *resource.Resource[S, St]) error
	// authorize, when set, makes checks beyond the route's rule that need
	// the body (spec 024: naming a channel needs read on it), after
	// prepare. current is the stored resource on update, nil on create.
	// The returned context carries the checks' policy evidence. A
	// *deployRefusal answers with its status; a 403 is audited.
	authorize    func(ctx context.Context, res resource.Resource[S, St], current *resource.Resource[S, St]) (context.Context, error)
	kind         resource.Kind
	recordDenied dbForAudit
	// serverStatus makes status the server's: empty on create, and the
	// stored one kept on update, whatever the body says (spec 024: a
	// rule's silence is changed only through its own action).
	serverStatus bool
	// serverFields, when set, guards the spec fields only the server sets
	// (spec 027: createdFrom), on create (current nil) and on update
	// against the stored resource: it may refuse (wrap
	// resource.ErrValidation for a 400) or copy the stored values in.
	serverFields func(res, current *resource.Resource[S, St]) error
	// view, when set, is what a response shows of a resource (spec 024:
	// a channel's Secrets as secretSet).
	view func(ctx context.Context, r resource.Resource[S, St]) (any, error)
}

// WithServerStatus makes the handler ignore a body's status: empty on
// create, the stored one on update.
func (h *ResourceHandler[S, St]) WithServerStatus() *ResourceHandler[S, St] {
	h.serverStatus = true
	return h
}

// WithServerFields makes the handler guard server-set spec fields with f
// on each create and update.
func (h *ResourceHandler[S, St]) WithServerFields(f func(res, current *resource.Resource[S, St]) error) *ResourceHandler[S, St] {
	h.serverFields = f
	return h
}

// WithView makes the handler answer with v's rendering of each resource.
func (h *ResourceHandler[S, St]) WithView(v func(ctx context.Context, r resource.Resource[S, St]) (any, error)) *ResourceHandler[S, St] {
	h.view = v
	return h
}

// render is r as a response shows it.
func (h *ResourceHandler[S, St]) render(ctx context.Context, r resource.Resource[S, St]) (any, error) {
	if h.view == nil {
		return r, nil
	}
	return h.view(ctx, r)
}

// respond writes r, rendered, with status.
func (h *ResourceHandler[S, St]) respond(w http.ResponseWriter, r *http.Request, status int, res resource.Resource[S, St]) {
	v, err := h.render(r.Context(), res)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, status, v)
}

// WithAuthorize makes the handler run a's checks on each create and
// update, auditing a refusal under kind through record.
func (h *ResourceHandler[S, St]) WithAuthorize(kind resource.Kind, record dbForAudit,
	a func(ctx context.Context, res resource.Resource[S, St], current *resource.Resource[S, St]) (context.Context, error)) *ResourceHandler[S, St] {
	h.authorize, h.kind, h.recordDenied = a, kind, record
	return h
}

// authorized runs the authorize hook, if any, answering a refusal itself.
func (h *ResourceHandler[S, St]) authorized(w http.ResponseWriter, r *http.Request, res resource.Resource[S, St], update bool) (context.Context, bool) {
	ctx := r.Context()
	if h.authorize == nil {
		return ctx, true
	}
	var current *resource.Resource[S, St]
	if update {
		got, err := h.store.Get(ctx, res.Metadata.ID)
		if err != nil {
			h.writeStoreError(w, r, err)
			return ctx, false
		}
		current = &got
	}
	return h.authorizeWith(w, r, res, current)
}

// authorizeWith runs the authorize hook against current (nil on create).
func (h *ResourceHandler[S, St]) authorizeWith(w http.ResponseWriter, r *http.Request, res resource.Resource[S, St], current *resource.Resource[S, St]) (context.Context, bool) {
	ctx := r.Context()
	if h.authorize == nil {
		return ctx, true
	}
	ctx, err := h.authorize(ctx, res, current)
	if err == nil {
		return ctx, true
	}
	var rf *deployRefusal
	if errors.As(err, &rf) {
		auditRefusal(r, rf, h.kind, h.recordDenied)
		h.writeError(w, r, rf.status, rf.title, rf.detail)
		return ctx, false
	}
	h.writeStoreError(w, r, err)
	return ctx, false
}

// WithPrepare makes the handler check and complete each resource with p
// before storing it.
func (h *ResourceHandler[S, St]) WithPrepare(p func(r *resource.Resource[S, St]) error) *ResourceHandler[S, St] {
	h.prepare = p
	return h
}

// WithMetadataCheck makes the handler check metadata with c before storing.
func (h *ResourceHandler[S, St]) WithMetadataCheck(c func(m resource.Metadata) error) *ResourceHandler[S, St] {
	h.checkMeta = c
	return h
}

// WithValidate makes the handler check specs with v before storing them.
func (h *ResourceHandler[S, St]) WithValidate(v func(ctx context.Context, id string, spec S) error) *ResourceHandler[S, St] {
	h.validate = v
	return h
}

// NewResourceHandler returns a handler for one resource kind. path is the
// URL path segment (e.g. "organizations"), not a resource.Kind value.
func NewResourceHandler[S, St any](s store.Store[S, St], path string) *ResourceHandler[S, St] {
	return &ResourceHandler[S, St]{store: s, path: path}
}

// Register wires this handler's 5 routes onto mux using Go 1.22+
// pattern-based net/http.ServeMux routing -- no router library.
func (h *ResourceHandler[S, St]) Register(mux Mux) {
	mux.HandleFunc("POST /v1/"+h.path, h.Create)
	mux.HandleFunc("GET /v1/"+h.path, h.List)
	mux.HandleFunc("GET /v1/"+h.path+"/{id}", h.Get)
	mux.HandleFunc("PUT /v1/"+h.path+"/{id}", h.Update)
	mux.HandleFunc("DELETE /v1/"+h.path+"/{id}", h.Delete)
}

func (h *ResourceHandler[S, St]) Create(w http.ResponseWriter, r *http.Request) {
	var res resource.Resource[S, St]
	if !decodeResourceBody(w, r, &res) {
		return
	}
	if h.serverStatus {
		var zero St
		res.Status = zero
	}
	if h.serverFields != nil {
		if err := h.serverFields(&res, nil); err != nil {
			h.writeStoreError(w, r, err)
			return
		}
	}
	if h.checkMeta != nil {
		if err := h.checkMeta(res.Metadata); err != nil {
			h.writeStoreError(w, r, err)
			return
		}
	}
	if h.validate != nil {
		if err := h.validate(r.Context(), "", res.Spec); err != nil {
			h.writeStoreError(w, r, err)
			return
		}
	}
	if h.prepare != nil {
		if err := h.prepare(&res); err != nil {
			h.writeStoreError(w, r, err)
			return
		}
	}
	ctx, ok := h.authorized(w, r, res, false)
	if !ok {
		return
	}
	created, err := h.store.Create(ctx, res)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	h.respond(w, r, http.StatusCreated, created)
}

func (h *ResourceHandler[S, St]) Get(w http.ResponseWriter, r *http.Request) {
	got, err := h.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	h.respond(w, r, http.StatusOK, got)
}

// List supports ?ownerId=<id> and ?label.<key>=<value> (repeated for
// multiple labels), matching exactly what core/store.ListOptions already
// supports -- nothing more (specs/002-http-api/plan.md open question 4).
func (h *ResourceHandler[S, St]) List(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.List(r.Context(), listOptions(r))
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	// Only what the caller may read (specs/017-authorization decision 4).
	if list, err = filterReadable(r.Context(), list); err != nil {
		h.writeError(w, r, http.StatusInternalServerError, "authorization check failed", "the list couldn't be filtered; retry later")
		return
	}
	if h.view == nil {
		writeJSON(w, http.StatusOK, list)
		return
	}
	out := make([]any, 0, len(list))
	for _, it := range list {
		v, err := h.view(r.Context(), it)
		if err != nil {
			writeStoreErr(w, r, err)
			return
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// listOptions is a list's ?ownerId= and ?label.<key>=<value> filters.
func listOptions(r *http.Request) store.ListOptions {
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
	return opts
}

func (h *ResourceHandler[S, St]) Update(w http.ResponseWriter, r *http.Request) {
	var res resource.Resource[S, St]
	if !decodeResourceBody(w, r, &res) {
		return
	}
	// The path's {id} is authoritative over whatever the body claims --
	// standard REST semantics for PUT /resource/{id}.
	res.Metadata.ID = r.PathValue("id")
	if h.checkMeta != nil {
		if err := h.checkMeta(res.Metadata); err != nil {
			h.writeStoreError(w, r, err)
			return
		}
	}
	if h.validate != nil {
		if err := h.validate(r.Context(), res.Metadata.ID, res.Spec); err != nil {
			h.writeStoreError(w, r, err)
			return
		}
	}
	if h.prepare != nil {
		if err := h.prepare(&res); err != nil {
			h.writeStoreError(w, r, err)
			return
		}
	}
	var ctx context.Context
	var ok bool
	if h.serverStatus || h.serverFields != nil {
		current, err := h.store.Get(r.Context(), res.Metadata.ID)
		if err != nil {
			h.writeStoreError(w, r, err)
			return
		}
		if h.serverStatus {
			res.Status = current.Status
		}
		if h.serverFields != nil {
			if err := h.serverFields(&res, &current); err != nil {
				h.writeStoreError(w, r, err)
				return
			}
		}
		ctx, ok = h.authorizeWith(w, r, res, &current)
	} else {
		ctx, ok = h.authorized(w, r, res, true)
	}
	if !ok {
		return
	}
	updated, err := h.store.Update(ctx, res)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	h.respond(w, r, http.StatusOK, updated)
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
	if status == http.StatusInternalServerError {
		// Never internal error text in a 500 (api/CLAUDE.md); a
		// store.ErrNoApproval here is a wiring bug, logged for the operator.
		writeStoreErr(w, r, err)
		return
	}
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
