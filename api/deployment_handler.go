package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/jsonschema"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// DeploymentHandler serves Deployments (specs/020 plan decision 2): one
// Component running in the Environment that owns the Deployment. Get,
// List and Delete are the ordinary ones (a delete is an undeploy, through
// an Approval like every delete). Create and Update are its own, to keep
// what's deployed coherent:
//   - the Deployment is owned by exactly one Environment, and its
//     Component's Application belongs to the same Project (deploying stays
//     inside a subscription);
//   - the caller may read the Component it deploys, besides writing at the
//     Environment ("read the source, write the target");
//   - a Component runs at most once per Environment;
//   - the release follows the Project's pipeline (resource.AdmitRelease):
//     the platform doesn't enforce it, so Serendip does;
//   - the Component and the Environment are fixed once created, and the
//     depends-on edge to the Component is the server's.
//
// Promote moves the release a Deployment runs on to the next Environment.
type DeploymentHandler struct {
	*ResourceHandler[resource.DeploymentSpec, resource.DeploymentStatus]
	deployments store.Store[resource.DeploymentSpec, resource.DeploymentStatus]
	envs        store.Store[resource.EnvironmentSpec, resource.EnvironmentStatus]
	comps       store.Store[resource.ComponentSpec, resource.ComponentStatus]
	apps        store.Store[resource.ApplicationSpec, resource.ApplicationStatus]
	projects    store.Store[resource.ProjectSpec, resource.ProjectStatus]
	// schemas checks configuration overrides; nil when no application
	// platform is configured.
	schemas reconcile.ConfigSchemaProvider
	// recordDenied audits the handler's own refusals (auditRefusal).
	recordDenied dbForAudit
	// mu serialises Deployment writes, so two requests can't both pass
	// the one-per-Environment check and both create (requests run
	// concurrently; one control plane, so a mutex is enough).
	mu sync.Mutex
}

func (h *DeploymentHandler) Register(mux Mux) {
	mux.HandleFunc("POST /v1/deployments", h.Create)
	mux.HandleFunc("GET /v1/deployments", h.List)
	mux.HandleFunc("GET /v1/deployments/{id}", h.Get)
	mux.HandleFunc("PUT /v1/deployments/{id}", h.Update)
	mux.HandleFunc("DELETE /v1/deployments/{id}", h.Delete)
	mux.HandleFunc("POST /v1/deployments/{id}/promote", h.Promote)
}

// deployRefusal is a reason to refuse a Deployment, with its HTTP status.
type deployRefusal struct {
	status        int
	title, detail string
}

func (e *deployRefusal) Error() string { return e.detail }

func deployRefuse(status int, title, format string, args ...any) error {
	return &deployRefusal{status: status, title: title, detail: fmt.Sprintf(format, args...)}
}

// auditRefusal records a refused mutation the handler decided itself (a
// 403 from a check beyond the route's rule, such as promote's write at the
// target Environment), as the authorizer records its own: a refused
// mutation gets an audit row (constitution 8). Reads are only refused.
func auditRefusal(r *http.Request, rf *deployRefusal, kind resource.Kind, record dbForAudit) {
	if rf.status != http.StatusForbidden || record == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return
	}
	if err := record(r.Context(), kind, r.PathValue("id"), "deny: "+rf.detail); err != nil {
		log.Printf("api: audit a refused %s %s (request %s): %v", r.Method, r.URL.Path, audit.RequestIDFrom(r.Context()), err)
	}
}

func (h *DeploymentHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var rf *deployRefusal
	if errors.As(err, &rf) {
		auditRefusal(r, rf, resource.KindDeployment, h.recordDenied)
		h.writeError(w, r, rf.status, rf.title, rf.detail)
		return
	}
	h.writeStoreError(w, r, err)
}

func environmentOf(d resource.Deployment) (string, error) {
	if len(d.Metadata.OwnerRefs) != 1 || d.Metadata.OwnerRefs[0].Kind != resource.KindEnvironment {
		return "", deployRefuse(http.StatusBadRequest, "validation failed", "a Deployment is owned by exactly one Environment: the one it runs in")
	}
	return d.Metadata.OwnerRefs[0].ID, nil
}

// allowedNoted is allowedAt for a handler's own extra check: an allowed
// check joins the request's policy evidence, so the mutation's audit row
// names every decision behind it, not only the route's.
func allowedNoted(ctx context.Context, act policy.Action, at resource.Scope) (context.Context, bool, error) {
	ok, err := allowedAt(ctx, act, at)
	if !ok || err != nil {
		return ctx, ok, err
	}
	note := fmt.Sprintf("allow %s at %s/%s", act.Name, at.Kind, at.ID)
	if prev := audit.PolicyDecisionFrom(ctx); prev != nil && *prev != "" {
		note = *prev + "; " + note
	}
	return audit.WithPolicyDecision(ctx, note), true, nil
}

func owner(m resource.Metadata, kind resource.Kind) string {
	for _, o := range m.OwnerRefs {
		if o.Kind == kind {
			return o.ID
		}
	}
	return ""
}

// schemaCheckTimeout bounds reading the platform's schemas for a write.
const schemaCheckTimeout = 10 * time.Second

// checkConfig checks d's configuration against the platform's schemas. It
// runs before the handler's lock, since it reads the platform: a slow
// platform mustn't hold up every other Deployment write (T016 review).
// Unchanged configuration isn't checked again, so promoting and changing
// releases work while the platform is away (RP-032). current is the
// Deployment as read before the lock (nil on create); if it changes
// meanwhile, the store's version check refuses the write.
func (h *DeploymentHandler) checkConfig(ctx context.Context, d resource.Deployment, current *resource.Deployment) error {
	same, err := overridesEqual(current, d.Spec.Overrides)
	if err != nil || same {
		return err
	}
	comp, err := h.comps.Get(ctx, d.Spec.Component)
	if errors.Is(err, store.ErrNotFound) {
		return deployRefuse(http.StatusBadRequest, "validation failed", "Component %s not found", d.Spec.Component)
	} else if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, schemaCheckTimeout)
	defer cancel()
	return h.checkOverrides(ctx, comp, d.Spec.Overrides)
}

// admit checks d may run in envID. current is the Deployment as stored
// (nil on create). Keeping the release it already has is always allowed:
// editing overrides further along the pipeline mustn't depend on what
// earlier Environments run now. The context it returns carries the
// Component read check in its policy evidence.
func (h *DeploymentHandler) admit(ctx context.Context, d resource.Deployment, envID string, current *resource.Deployment) (context.Context, error) {
	if err := resource.ValidateDeploymentSpec(d.Spec); err != nil {
		return ctx, err
	}
	compRead := policy.Action{Name: "serendip/components/read"}
	ctx, ok, err := allowedNoted(ctx, compRead, resource.Scope{Kind: resource.KindComponent, ID: d.Spec.Component})
	if err != nil {
		return ctx, err
	}
	if !ok {
		return ctx, deployRefuse(http.StatusForbidden, "forbidden", "missing %s at Component/%s: deploying a Component needs reading it", compRead.Name, d.Spec.Component)
	}
	return ctx, h.coherent(ctx, d, envID, current)
}

// coherent checks d's Environment, Component, Project and pipeline.
func (h *DeploymentHandler) coherent(ctx context.Context, d resource.Deployment, envID string, current *resource.Deployment) error {
	var selfID string
	var history []string
	if current != nil {
		// What it runs now counts as its own, beside what it ran before.
		selfID, history = current.Metadata.ID, append(slices.Clone(current.Status.History), current.Status.Release)
	}
	env, err := h.envs.Get(ctx, envID)
	if errors.Is(err, store.ErrNotFound) {
		return deployRefuse(http.StatusBadRequest, "validation failed", "Environment %s not found", envID)
	} else if err != nil {
		return err
	}
	comp, err := h.comps.Get(ctx, d.Spec.Component)
	if errors.Is(err, store.ErrNotFound) {
		return deployRefuse(http.StatusBadRequest, "validation failed", "Component %s not found", d.Spec.Component)
	} else if err != nil {
		return err
	}
	app, err := h.apps.Get(ctx, owner(comp.Metadata, resource.KindApplication))
	if err != nil {
		return deployRefuse(http.StatusBadRequest, "validation failed", "the Component's Application can't be read: %v", err)
	}
	projectID := owner(env.Metadata, resource.KindProject)
	if projectID == "" || owner(app.Metadata, resource.KindProject) != projectID {
		return deployRefuse(http.StatusBadRequest, "validation failed", "the Component's Application and the Environment must belong to the same Project")
	}
	project, err := h.projects.Get(ctx, projectID)
	if err != nil {
		return err
	}
	all, err := h.deployments.List(ctx, store.ListOptions{})
	if err != nil {
		return err
	}
	running := map[string]string{}
	for _, other := range all {
		if other.Spec.Component != d.Spec.Component || other.Metadata.ID == selfID {
			continue
		}
		otherEnv := owner(other.Metadata, resource.KindEnvironment)
		if otherEnv == envID {
			return deployRefuse(http.StatusConflict, "conflict", "Component %s already runs in this Environment, as Deployment %s; change that one", d.Spec.Component, other.Metadata.ID)
		}
		running[otherEnv] = cmpOr(other.Status.Release, other.Spec.Release)
	}
	if current != nil && d.Spec.Release == current.Spec.Release {
		return nil
	}
	return resource.AdmitRelease(project.Spec.Pipeline, envID, d.Spec.Release, running, history)
}

// overridesEqual reports whether next configures what current (nil on
// create) already does.
func overridesEqual(current *resource.Deployment, next resource.DeploymentOverrides) (bool, error) {
	if current == nil {
		return false, nil
	}
	a := current.Spec.Overrides
	ja, err := json.Marshal(struct{ C, T any }{a.Config, a.Traits})
	if err != nil {
		return false, fmt.Errorf("encode the stored configuration: %w", err)
	}
	jb, err := json.Marshal(struct{ C, T any }{next.Config, next.Traits})
	if err != nil {
		return false, fmt.Errorf("encode the configuration: %w", err)
	}
	return string(ja) == string(jb), nil
}

// checkOverrides validates configuration against the schemas the
// Component's type publishes (spec 020 T008). Environment variables need
// no schema. A schema that can't be read or checked refuses rather than
// letting unchecked configuration through.
func (h *DeploymentHandler) checkOverrides(ctx context.Context, comp resource.Component, o resource.DeploymentOverrides) error {
	if len(o.Config) == 0 && len(o.Traits) == 0 {
		return nil
	}
	if h.schemas == nil {
		return deployRefuse(http.StatusServiceUnavailable, "service unavailable", "configuration can't be checked: no application platform is configured")
	}
	schemas, err := h.schemas.ConfigSchemas(ctx, comp)
	if errors.Is(err, reconcile.ErrProviderNotReady) {
		return deployRefuse(http.StatusServiceUnavailable, "service unavailable", "configuration can't be checked until the application platform is ready; retry shortly")
	}
	if err != nil {
		return schemaFailure(ctx, "read configuration schemas", err)
	}
	var problems []string
	check := func(prefix string, raw json.RawMessage, value map[string]any) error {
		schema, err := jsonschema.Parse(raw)
		if err != nil {
			return schemaFailure(ctx, "the platform's schema for "+prefix, err)
		}
		v, err := toJSONValue(value)
		if err != nil {
			return fmt.Errorf("encode %s: %w", prefix, err)
		}
		for _, p := range schema.Validate(v) {
			problems = append(problems, prefix+"."+p.String())
		}
		return nil
	}
	if len(o.Config) > 0 {
		if len(schemas.Environment) == 0 {
			problems = append(problems, "overrides.config: this Component's type takes no configuration")
		} else if err := check("overrides.config", schemas.Environment, o.Config); err != nil {
			return err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(o.Traits)) {
		raw, ok := schemas.Traits[name]
		if !ok {
			problems = append(problems, "overrides.traits."+name+": the Component has no trait instance "+name)
			continue
		}
		if err := check("overrides.traits."+name, raw, o.Traits[name]); err != nil {
			return err
		}
	}
	if len(problems) > 0 {
		return deployRefuse(http.StatusBadRequest, "validation failed", "%s", strings.Join(problems, "; "))
	}
	return nil
}

// schemaFailure refuses configuration whose schema couldn't be read or
// checked: 502 with a fixed message, the cause logged for the operator.
func schemaFailure(ctx context.Context, what string, err error) error {
	log.Printf("api: %s (request %s): %v", what, audit.RequestIDFrom(ctx), err)
	return deployRefuse(http.StatusBadGateway, "bad gateway", "the configuration couldn't be checked against the platform's schema; retry later, quoting the request ID")
}

// toJSONValue gives v the shape encoding/json decodes into (numbers as
// float64), which is what the validator reads.
func toJSONValue(v map[string]any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (h *DeploymentHandler) Create(w http.ResponseWriter, r *http.Request) {
	var d resource.Deployment
	if !decodeBody(w, r, &d) {
		return
	}
	ctx := r.Context()
	if err := h.checkConfig(ctx, d, nil); err != nil {
		h.fail(w, r, err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	envID, err := environmentOf(d)
	if err == nil {
		ctx, err = h.admit(ctx, d, envID, nil)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d.Metadata.ID = ""
	d.Metadata.Relationships = resource.DeploymentRelationships(d.Spec)
	d.Status = resource.DeploymentStatus{}
	created, err := h.deployments.Create(ctx, d)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *DeploymentHandler) Update(w http.ResponseWriter, r *http.Request) {
	var d resource.Deployment
	if !decodeBody(w, r, &d) {
		return
	}
	ctx := r.Context()
	d.Metadata.ID = r.PathValue("id")
	before, err := h.deployments.Get(ctx, d.Metadata.ID)
	if err == nil && d.Spec.Component == before.Spec.Component {
		err = h.checkConfig(ctx, d, &before)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	current, err := h.deployments.Get(ctx, d.Metadata.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	envID, err := environmentOf(d)
	switch {
	case err != nil:
	case envID != owner(current.Metadata, resource.KindEnvironment):
		err = deployRefuse(http.StatusBadRequest, "validation failed", "a Deployment's Environment can't change: deploy to the other Environment instead")
	case d.Spec.Component != current.Spec.Component:
		err = deployRefuse(http.StatusBadRequest, "validation failed", "a Deployment's Component can't change: create another Deployment")
	default:
		ctx, err = h.admit(ctx, d, envID, &current)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d.Metadata.Relationships = resource.DeploymentRelationships(d.Spec)
	d.Status = current.Status
	updated, err := h.deployments.Update(ctx, d)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// promoteBody names the Environment to promote to.
type promoteBody struct {
	To string `json:"to"`
}

// Promote runs the release this Deployment runs now in another Environment,
// along the Project's pipeline. The target keeps its own overrides; only
// the release moves. It needs reading this Deployment (the route's rule)
// and writing deployments at the target Environment.
func (h *DeploymentHandler) Promote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body promoteBody
	if !decodeBody(w, r, &body) {
		return
	}
	// Locked before reading the source, so the release promoted is the one
	// it runs now, not one a concurrent write is replacing.
	h.mu.Lock()
	defer h.mu.Unlock()
	src, err := h.deployments.Get(ctx, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	release := cmpOr(src.Status.Release, src.Spec.Release)
	if release == "" {
		h.fail(w, r, deployRefuse(http.StatusConflict, "conflict", "Deployment %s runs no release yet; promote once it does", src.Metadata.ID))
		return
	}
	if body.To == "" {
		h.fail(w, r, deployRefuse(http.StatusBadRequest, "validation failed", "to: the Environment to promote to is required"))
		return
	}
	write := policy.Action{Name: "serendip/deployments/write"}
	ctx, ok, err := allowedNoted(ctx, write, resource.Scope{Kind: resource.KindEnvironment, ID: body.To})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !ok {
		h.fail(w, r, deployRefuse(http.StatusForbidden, "forbidden", "missing %s at Environment/%s", write.Name, body.To))
		return
	}

	all, err := h.deployments.List(ctx, store.ListOptions{OwnerID: body.To})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var target *resource.Deployment
	for i := range all {
		if all[i].Spec.Component == src.Spec.Component {
			target = &all[i]
		}
	}
	if target == nil {
		d := resource.Deployment{
			Metadata: resource.Metadata{Name: src.Metadata.Name, OwnerRefs: []resource.OwnerRef{{Kind: resource.KindEnvironment, ID: body.To}}},
			Spec:     resource.DeploymentSpec{Component: src.Spec.Component, Release: release},
		}
		ctx, err := h.admit(ctx, d, body.To, nil)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		d.Metadata.Relationships = resource.DeploymentRelationships(d.Spec)
		created, err := h.deployments.Create(ctx, d)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
		return
	}
	if target.Spec.Release == release {
		// Already there: nothing to write (the operation is idempotent).
		writeJSON(w, http.StatusOK, target)
		return
	}
	current := *target
	target.Spec.Release = release
	ctx, err = h.admit(ctx, *target, body.To, &current)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	updated, err := h.deployments.Update(ctx, *target)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// projectValidator checks a Project's pipeline names only its own
// Environments (the platform's pipeline is per Project, plan 020 decision
// 2).
func projectValidator(envs store.Store[resource.EnvironmentSpec, resource.EnvironmentStatus]) func(ctx context.Context, id string, spec resource.ProjectSpec) error {
	return func(ctx context.Context, id string, spec resource.ProjectSpec) error {
		if err := resource.ValidatePipeline(spec.Pipeline); err != nil {
			return err
		}
		names := spec.Pipeline.Environments()
		if len(names) == 0 {
			return nil
		}
		if id == "" {
			return fmt.Errorf("%w: pipeline: create the Project, then its Environments, then set its pipeline", resource.ErrValidation)
		}
		for _, envID := range names {
			env, err := envs.Get(ctx, envID)
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("%w: pipeline: Environment %s not found", resource.ErrValidation, envID)
			}
			if err != nil {
				return err
			}
			if owner(env.Metadata, resource.KindProject) != id {
				return fmt.Errorf("%w: pipeline: Environment %s belongs to another Project", resource.ErrValidation, envID)
			}
		}
		return nil
	}
}

// SchemaHandler serves GET /v1/components/{id}/schemas: what can be
// configured on a Component, as JSON Schemas read from the platform
// (spec 020 T008), for forms. Read only; needs reading the Component.
type SchemaHandler struct {
	comps   store.Store[resource.ComponentSpec, resource.ComponentStatus]
	schemas reconcile.ConfigSchemaProvider
}

func (h *SchemaHandler) Register(mux Mux) {
	mux.HandleFunc("GET /v1/components/{id}/schemas", h.Get)
}

func (h *SchemaHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := audit.RequestIDFrom(r.Context())
	comp, err := h.comps.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	if h.schemas == nil {
		WriteProblem(w, http.StatusServiceUnavailable, "service unavailable", "no application platform is configured", reqID)
		return
	}
	schemas, err := h.schemas.ConfigSchemas(r.Context(), comp)
	if err != nil {
		platformFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, schemas)
}
