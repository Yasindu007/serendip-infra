package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/graph"
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
	// secretEnv checks the Secrets its secret variables name, and
	// deployments are checked for clashes with them (spec 026).
	secretEnv   secretEnvChecker
	deployments store.Store[resource.DeploymentSpec, resource.DeploymentStatus]
	// view is what admission reads (storedView); a template's create
	// admits through a pendingView instead (spec 027).
	view workloadView
	// dependents lists what depends on a resource (the graph): a
	// Component's Deployments.
	dependents func(ctx context.Context, ref graph.ResourceRef) ([]graph.ResourceRef, error)
	// secretEnvChanged, when set, is told a Component's secret variables
	// changed, so its Deployments deliver them without waiting for resync.
	secretEnvChanged func(ctx context.Context, componentID string)
	// mu serialises Component writes, so a port can't be removed while a
	// dependency on it is being added. It's shared with DeploymentHandler,
	// so a Component's secret variables and its Deployments' overrides are
	// checked against each other without a race (spec 026).
	mu *sync.Mutex
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

// admit runs every check a Component write passes, reading v: its
// image, dependencies, source and secret variables. On update, current is
// c as stored. It returns the context with the checks' policy evidence.
func (h *ComponentHandler) admit(ctx context.Context, v workloadView, c resource.Component, current *resource.Component) (context.Context, error) {
	if err := resource.ValidateComponentImage(c.Spec); err != nil {
		return ctx, err
	}
	ctx, err := h.checkDependencies(ctx, v, c, current)
	if err != nil {
		return ctx, err
	}
	if ctx, err = h.source.check(ctx, v, c, current); err != nil {
		return ctx, err
	}
	return h.checkSecretEnv(ctx, v, c, current)
}

// checkDependencies checks c's dependencies against the Components they
// name, and returns the context with the reads it checked in its policy
// evidence. On update, current is c as stored.
func (h *ComponentHandler) checkDependencies(ctx context.Context, v workloadView, c resource.Component, current *resource.Component) (context.Context, error) {
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
		ctx, ok, err = allowedNoted(ctx, read, v.scope(resource.Scope{Kind: resource.KindComponent, ID: d.Component}))
		if err != nil {
			return ctx, err
		}
		if !ok {
			return ctx, deployRefuse(http.StatusForbidden, "forbidden", "missing %s at Component/%s: depending on a Component needs reading it", read.Name, d.Component)
		}
		target, err := v.component(ctx, d.Component)
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

// checkSecretEnv checks c's secret variables (spec 026): the list and its
// clashes within the Component (400), the Secrets newly named
// (secretEnvChecker), and, on update, that no Deployment's overrides now
// clash with the Component's variables (409 naming it).
func (h *ComponentHandler) checkSecretEnv(ctx context.Context, v workloadView, c resource.Component, current *resource.Component) (context.Context, error) {
	if err := resource.ValidateComponentSecretEnv(c.Spec); err != nil {
		return ctx, err
	}
	var was []resource.ComponentSecretEnv
	if current != nil {
		was = current.Spec.SecretEnv
	}
	ctx, err := h.secretEnv.check(ctx, v, "secretEnv", c.Spec.SecretEnv, was, func(ctx context.Context) (string, error) { return projectOf(ctx, v, c) })
	if err != nil || current == nil || h.deployments == nil || h.dependents == nil {
		return ctx, err
	}
	same := maps.Equal(current.Spec.Env, c.Spec.Env) && slices.Equal(current.Spec.SecretEnv, c.Spec.SecretEnv) &&
		slices.Equal(current.Spec.Dependencies, c.Spec.Dependencies)
	if same {
		return ctx, nil
	}
	users, err := h.dependents(ctx, graph.ResourceRef{Kind: resource.KindComponent, ID: c.Metadata.ID})
	if err != nil {
		return ctx, err
	}
	for _, u := range users {
		if u.Kind != resource.KindDeployment {
			continue
		}
		d, err := h.deployments.Get(ctx, u.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return ctx, err
		}
		if err := resource.ValidateDeploymentSecretEnv(d.Spec.Overrides, c.Spec); err != nil {
			// Named only to a caller who may read it.
			name := "of this Component"
			if ok, aerr := allowedAt(ctx, action(resource.KindDeployment, "read"), resource.Scope{Kind: resource.KindDeployment, ID: d.Metadata.ID}); aerr == nil && ok {
				name = d.Metadata.Name
			}
			return ctx, deployRefuse(http.StatusConflict, "conflict", "Deployment %s: %v; change its overrides first", name, err)
		}
	}
	return ctx, nil
}

// projectOf is the Project c's Application belongs to, read from v.
func projectOf(ctx context.Context, v workloadView, c resource.Component) (string, error) {
	appID, err := applicationOf(c)
	if err != nil {
		return "", err
	}
	app, err := v.application(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		return "", deployRefuse(http.StatusBadRequest, "validation failed", "Application %s not found", appID)
	}
	if err != nil {
		return "", err
	}
	project := owner(app.Metadata, resource.KindProject)
	if project == "" {
		return "", deployRefuse(http.StatusBadRequest, "validation failed", "the Component's Application belongs to no Project")
	}
	return project, nil
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

// errCreatedFromSet refuses a client's createdFrom on create: provenance is
// the server's (specs/027-templates-and-scaffolder plan decision 9).
var errCreatedFromSet = fmt.Errorf("%w: createdFrom: only the server sets it, when a template creates the resource; leave it out", resource.ErrValidation)

// keepCreatedFrom is the createdFrom an update stores: the stored one
// when the body leaves it out or repeats it, and a 400 when it sets or
// changes one.
func keepCreatedFrom(next, stored *resource.TemplateOrigin) (*resource.TemplateOrigin, error) {
	if next == nil {
		return stored, nil
	}
	if stored == nil {
		return nil, errCreatedFromSet
	}
	a, err := json.Marshal(next)
	if err != nil {
		return nil, fmt.Errorf("encode createdFrom: %w", err)
	}
	b, err := json.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("encode the stored createdFrom: %w", err)
	}
	if string(a) != string(b) {
		return nil, fmt.Errorf("%w: createdFrom: it can't change; leave it out to keep it", resource.ErrValidation)
	}
	return stored, nil
}

// applicationCreatedFrom guards an Application's createdFrom
// (ResourceHandler.WithServerFields).
func applicationCreatedFrom(res, current *resource.Application) error {
	if current == nil {
		if res.Spec.CreatedFrom != nil {
			return errCreatedFromSet
		}
		return nil
	}
	var err error
	res.Spec.CreatedFrom, err = keepCreatedFrom(res.Spec.CreatedFrom, current.Spec.CreatedFrom)
	return err
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
	if !decodeResourceBody(w, r, &c) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	c.Metadata.ID = ""
	if c.Spec.CreatedFrom != nil {
		h.fail(w, r, errCreatedFromSet)
		return
	}
	ctx, err := h.admit(r.Context(), h.view, c, nil)
	if err != nil {
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
	if !decodeResourceBody(w, r, &c) {
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
	if c.Spec.CreatedFrom, err = keepCreatedFrom(c.Spec.CreatedFrom, current.Spec.CreatedFrom); err != nil {
		h.fail(w, r, err)
		return
	}
	ctx, err := h.admit(r.Context(), h.view, c, &current)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	c.Metadata.Relationships = resource.ComponentRelationships(c.Metadata.Relationships, c.Spec)
	updated, err := h.comps.Update(ctx, c)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// A reorder counts as a change: harmless, one extra pass.
	if h.secretEnvChanged != nil && !slices.Equal(current.Spec.SecretEnv, c.Spec.SecretEnv) {
		// The write is done: the wake-up mustn't end with the request.
		h.secretEnvChanged(context.WithoutCancel(ctx), c.Metadata.ID)
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
