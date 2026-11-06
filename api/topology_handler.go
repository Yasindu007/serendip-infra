package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// ComponentHandler serves Components (spec 020 T009). Get, List and
// Delete are the ordinary ones. Create and Update are its own, to keep
// declared dependencies coherent:
//   - each target is another Component of the same Application (one
//     platform project), with the named port, that the caller may read;
//   - the depends-on edges to Components are the server's, derived from
//     the dependencies;
//   - a Component's Application can't change once created;
//   - a port another Component depends on can't be removed, and a
//     Component others depend on can't be deleted.
type ComponentHandler struct {
	*ResourceHandler[resource.ComponentSpec, resource.ComponentStatus]
	comps store.Store[resource.ComponentSpec, resource.ComponentStatus]
	apps  store.Store[resource.ApplicationSpec, resource.ApplicationStatus]
	// recordDenied audits the handler's own refusals (auditRefusal).
	recordDenied dbForAudit
	// source checks a source Component's build capability and credential
	// (spec 022).
	source sourceChecker
	// mu serialises Component writes, so a port can't be removed while a
	// dependency on it is being added.
	mu sync.Mutex
}

func (h *ComponentHandler) Register(mux Mux) {
	mux.HandleFunc("POST /v1/components", h.Create)
	mux.HandleFunc("GET /v1/components", h.List)
	mux.HandleFunc("GET /v1/components/{id}", h.Get)
	mux.HandleFunc("PUT /v1/components/{id}", h.Update)
	mux.HandleFunc("DELETE /v1/components/{id}", h.Delete)
	mux.HandleFunc("GET /v1/components/{id}/dependencies", h.Dependencies)
	mux.HandleFunc("GET /v1/applications/{id}/topology", h.Topology)
}

func (h *ComponentHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var rf *deployRefusal
	if errors.As(err, &rf) {
		auditRefusal(r, rf, resource.KindComponent, h.recordDenied)
		h.writeError(w, r, rf.status, rf.title, rf.detail)
		return
	}
	h.writeStoreError(w, r, err)
}

// checkDependencies checks c's dependencies against the Components they
// name, and returns the context with the reads it checked in its policy
// evidence. On update, current is c as stored.
func (h *ComponentHandler) checkDependencies(ctx context.Context, c resource.Component, current *resource.Component) (context.Context, error) {
	if err := resource.ValidateComponentDependencies(c.Metadata.ID, c.Spec); err != nil {
		return ctx, err
	}
	appID, err := applicationOf(c)
	if err != nil && (current != nil || len(c.Spec.Dependencies) > 0) {
		return ctx, err
	}
	read := policy.Action{Name: "serendip/components/read"}
	for i, d := range c.Spec.Dependencies {
		var ok bool
		var err error
		ctx, ok, err = allowedNoted(ctx, read, resource.Scope{Kind: resource.KindComponent, ID: d.Component})
		if err != nil {
			return ctx, err
		}
		if !ok {
			return ctx, deployRefuse(http.StatusForbidden, "forbidden", "missing %s at Component/%s: depending on a Component needs reading it", read.Name, d.Component)
		}
		target, err := h.comps.Get(ctx, d.Component)
		if errors.Is(err, store.ErrNotFound) {
			return ctx, deployRefuse(http.StatusBadRequest, "validation failed", "dependencies[%d].component: Component %s not found", i, d.Component)
		}
		if err != nil {
			return ctx, err
		}
		if owner(target.Metadata, resource.KindApplication) != appID {
			return ctx, deployRefuse(http.StatusBadRequest, "validation failed", "dependencies[%d].component: a Component can depend only on Components of its own Application", i)
		}
		if !slices.ContainsFunc(target.Spec.Ports, func(p resource.ComponentPort) bool { return p.Name == d.Port }) {
			return ctx, deployRefuse(http.StatusBadRequest, "validation failed", "dependencies[%d].port: Component %s has no port %s", i, target.Metadata.Name, d.Port)
		}
	}
	if current == nil {
		return ctx, nil
	}
	// The Application stays: the same-Application rule holds for those
	// that call this Component too.
	if appID != owner(current.Metadata, resource.KindApplication) {
		return ctx, deployRefuse(http.StatusBadRequest, "validation failed", "a Component's Application can't change: create it in the other Application instead")
	}
	// Ports others call stay.
	callers, err := h.callers(ctx, *current)
	if err != nil {
		return ctx, err
	}
	for _, caller := range callers {
		for _, d := range caller.Spec.Dependencies {
			if d.Component == current.Metadata.ID && !slices.ContainsFunc(c.Spec.Ports, func(p resource.ComponentPort) bool { return p.Name == d.Port }) {
				return ctx, deployRefuse(http.StatusConflict, "conflict", "port %s is a dependency of Component %s; remove that dependency first", d.Port, caller.Metadata.Name)
			}
		}
	}
	return ctx, nil
}

// applicationOf is a Component's one Application; more than one, or
// none, is refused.
func applicationOf(c resource.Component) (string, error) {
	var apps []string
	for _, o := range c.Metadata.OwnerRefs {
		if o.Kind == resource.KindApplication {
			apps = append(apps, o.ID)
		}
	}
	if len(apps) != 1 {
		return "", deployRefuse(http.StatusBadRequest, "validation failed", "a Component is owned by exactly one Application")
	}
	return apps[0], nil
}

// callers lists the Components of c's Application that depend on c.
func (h *ComponentHandler) callers(ctx context.Context, c resource.Component) ([]resource.Component, error) {
	appID := owner(c.Metadata, resource.KindApplication)
	if appID == "" {
		return nil, nil
	}
	siblings, err := h.comps.List(ctx, store.ListOptions{OwnerID: appID})
	if err != nil {
		return nil, err
	}
	var out []resource.Component
	for _, s := range siblings {
		if s.Metadata.ID != c.Metadata.ID && slices.ContainsFunc(s.Spec.Dependencies, func(d resource.ComponentDependency) bool { return d.Component == c.Metadata.ID }) {
			out = append(out, s)
		}
	}
	return out, nil
}

// Delete refuses while other Components depend on this one; otherwise
// it's the ordinary delete, through an Approval.
func (h *ComponentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, err := h.comps.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	callers, err := h.callers(r.Context(), c)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if len(callers) > 0 {
		h.fail(w, r, deployRefuse(http.StatusConflict, "conflict", "Component %s depends on this one; remove that dependency first", callers[0].Metadata.Name))
		return
	}
	h.ResourceHandler.Delete(w, r)
}

func (h *ComponentHandler) Create(w http.ResponseWriter, r *http.Request) {
	var c resource.Component
	if !decodeBody(w, r, &c) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	c.Metadata.ID = ""
	ctx, err := h.checkDependencies(r.Context(), c, nil)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if ctx, err = h.source.check(ctx, c, nil); err != nil {
		h.fail(w, r, err)
		return
	}
	c.Metadata.Relationships = resource.ComponentRelationships(c.Metadata.Relationships, c.Spec)
	created, err := h.comps.Create(ctx, c)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *ComponentHandler) Update(w http.ResponseWriter, r *http.Request) {
	var c resource.Component
	if !decodeBody(w, r, &c) {
		return
	}
	c.Metadata.ID = r.PathValue("id")
	h.mu.Lock()
	defer h.mu.Unlock()
	current, err := h.comps.Get(r.Context(), c.Metadata.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ctx, err := h.checkDependencies(r.Context(), c, &current)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if ctx, err = h.source.check(ctx, c, &current); err != nil {
		h.fail(w, r, err)
		return
	}
	c.Metadata.Relationships = resource.ComponentRelationships(c.Metadata.Relationships, c.Spec)
	updated, err := h.comps.Update(ctx, c)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// componentLink is one declared call between two Components.
type componentLink struct {
	From string `json:"from"`
	To   string `json:"to"`
	Port string `json:"port"`
	Env  string `json:"env"`
}

// readableComponents keeps the Components the caller may read.
func readableComponents(ctx context.Context, comps []resource.Component) ([]resource.Component, error) {
	read := policy.Action{Name: "serendip/components/read"}
	out := comps[:0:0]
	for _, c := range comps {
		ok, err := allowedAt(ctx, read, resource.Scope{Kind: resource.KindComponent, ID: c.Metadata.ID})
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// Dependencies answers {"calls": [...], "calledBy": [...]}: the
// Component's declared dependencies, and the Components of its
// Application that declare one on it (those the caller may read).
func (h *ComponentHandler) Dependencies(w http.ResponseWriter, r *http.Request) {
	c, err := h.comps.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	callers, err := h.callers(r.Context(), c)
	if err == nil {
		callers, err = readableComponents(r.Context(), callers)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	read := policy.Action{Name: "serendip/components/read"}
	calls, calledBy := []componentLink{}, []componentLink{}
	for _, d := range c.Spec.Dependencies {
		ok, err := allowedAt(r.Context(), read, resource.Scope{Kind: resource.KindComponent, ID: d.Component})
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if ok {
			calls = append(calls, componentLink{From: c.Metadata.ID, To: d.Component, Port: d.Port, Env: d.Env})
		}
	}
	for _, s := range callers {
		for _, d := range s.Spec.Dependencies {
			if d.Component == c.Metadata.ID {
				calledBy = append(calledBy, componentLink{From: s.Metadata.ID, To: c.Metadata.ID, Port: d.Port, Env: d.Env})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"calls": calls, "calledBy": calledBy})
}

// topologyNode is one Component in an Application's diagram.
type topologyNode struct {
	ID    string                   `json:"id"`
	Name  string                   `json:"name"`
	Ports []resource.ComponentPort `json:"ports"`
}

// Topology answers an Application's declared topology: {"components":
// [...], "dependencies": [...]}, the Components the caller may read and
// the calls between them. It's what's declared, not live traffic.
func (h *ComponentHandler) Topology(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	if _, err := h.apps.Get(r.Context(), appID); err != nil {
		h.fail(w, r, err)
		return
	}
	all, err := h.comps.List(r.Context(), store.ListOptions{OwnerID: appID})
	var comps []resource.Component
	for _, c := range all {
		if owner(c.Metadata, resource.KindApplication) == appID {
			comps = append(comps, c)
		}
	}
	if err == nil {
		comps, err = readableComponents(r.Context(), comps)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	nodes, links := []topologyNode{}, []componentLink{}
	shown := map[string]bool{}
	for _, c := range comps {
		shown[c.Metadata.ID] = true
		nodes = append(nodes, topologyNode{ID: c.Metadata.ID, Name: c.Metadata.Name, Ports: nonNil(c.Spec.Ports)})
	}
	for _, c := range comps {
		for _, d := range c.Spec.Dependencies {
			if shown[d.Component] {
				links = append(links, componentLink{From: c.Metadata.ID, To: d.Component, Port: d.Port, Env: d.Env})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"components": nodes, "dependencies": links})
}

// PlatformHandler serves GET /v1/platform: the platform described
// read-only (spec 020 T009): where workloads run, and the component types
// and traits installed. Environments and pipelines have their own routes.
type PlatformHandler struct {
	platform reconcile.PlatformViewProvider
}

func (h *PlatformHandler) Register(mux Mux) {
	mux.HandleFunc("GET /v1/platform", h.Get)
}

func (h *PlatformHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.platform == nil {
		WriteProblem(w, http.StatusServiceUnavailable, "service unavailable", "no application platform is configured", audit.RequestIDFrom(r.Context()))
		return
	}
	view, err := h.platform.Platform(r.Context())
	if err != nil {
		platformFailed(w, r, err)
		return
	}
	view.Runtimes, view.ComponentTypes, view.Traits = nonNil(view.Runtimes), nonNil(view.ComponentTypes), nonNil(view.Traits)
	writeJSON(w, http.StatusOK, view)
}
