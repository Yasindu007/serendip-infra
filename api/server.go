package api

import (
	"context"
	"database/sql"
	"log"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/registry"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// NewServer assembles the full HTTP API: one ResourceHandler per resource
// kind (14 instantiations below, mirroring core/store/sqlite.Store[S,St]'s
// per-kind pattern), the graph read endpoints (GraphHandler, T013 --
// wired in here even though tasks.md's T013 didn't list server.go as a
// file it touches, since otherwise GraphHandler would be dead code once
// assembled: nothing else in this package ever calls NewServeMux + graph
// wiring, so leaving it unwired would mean the graph endpoints exist but
// are unreachable through the actual running server -- same reasoning as
// T003's Interface-bundling call), the registry default-provider endpoint
// (RegistryHandler -- specs/002-http-api/spec.md Scope-IN item 2, closed
// as a T019 final-review gap fix; no task on paper ever built it and the
// drop went undisclosed until contract-guardian's holistic pass caught
// it), the middleware chain (request ID -> auth -> mux,
// specs/002-http-api/plan.md), and a GET /healthz that needs no token.
//
// apiKeys and authn are AuthMiddleware's two ways to resolve a caller: the
// static token-to-actor map, and the Identity capability's Authenticator
// (nil when no identity provider is wired) -- see its doc comment.
//
// secretReconciler is nil when SERENDIP_KUBECONFIG is unset
// (cmd/serendipd's wireKubernetes never ran) -- in that case the
// "secrets" capability has no real provider behind it yet, so
// /v1/secrets isn't registered at all (404, not a nil-pointer panic
// on first use). Every other resource kind's handler only ever needs
// a store.Store, built independently of any cluster -- Secret is the
// first kind whose API surface is genuinely unavailable without one,
// since SecretHandler's value routes have nothing underneath them to
// call (specs/009-secrets-capability/plan.md resolution 6).
//
// providers is the live capability registry whose readiness GET
// /v1/readiness reports (specs/014-reconciliation-hardening plan.md
// decision 4); nil reports no capabilities.
//
// console, when non-nil, adds the web console and its browser login
// (specs/016-ui-mvp, ADR 0013): the built files and /auth/* on the outer
// mux, the session cookie as a credential, and GET /v1/session. Nil keeps
// the API exactly as it was.
//
// Every /v1 route is authorized (specs/017-authorization): it's registered
// through an authorizing mux that refuses, at startup, any route without a
// rule in api/authz.go's route table, and decides each request against the
// role assignments. Each API key with no assignment yet is made Owner at
// "/", so existing keys keep their full access (plan.md decision 7).
func NewServer(db *sql.DB, bus event.Bus, apiKeys map[string]string, authn identity.Authenticator, secretReconciler *reconcile.SecretReconciler, providers *provider.Registry, console *Console, opts ...ServerOption) *http.Server {
	var o serverOptions
	for _, opt := range opts {
		opt(&o)
	}
	engine, stopPolicy := newPolicyEngine(db, bus)
	migrateAPIKeys(engine, db, bus, apiKeys)
	approvals := sqlite.NewStore[resource.ApprovalSpec, resource.ApprovalStatus](db, resource.KindApproval).SetBus(bus)
	apiMux := &authzMux{mux: http.NewServeMux(), a: newAuthorizer(engine, graph.NewSQLiteGraph(db), recordDeniedIn(db), approvals, resourceExistsIn(db))}
	registerAPI(apiMux, db, bus, engine, approvals, secretReconciler, providers, console, o)

	topMux := http.NewServeMux()
	topMux.HandleFunc("GET /healthz", healthzHandler)
	origin := ""
	if console != nil && console.Origin != nil {
		origin = console.Origin.String()
	}
	var metadataURL func(*http.Request) string
	if o.metadata != nil {
		topMux.HandleFunc("GET "+resourceMetadataPath, resourceMetadataHandler(*o.metadata, origin))
		metadataURL = resourceMetadataURL(origin)
	}
	// Everything under /v1/ requires a resolved actor; /healthz doesn't
	// go through AuthMiddleware at all -- it's mounted directly on
	// topMux, not apiMux, so there's no bypass logic to get wrong inside
	// a shared handler.
	v1 := apiHardening(authMiddleware(apiKeys, authn, console, sqlite.NewPrincipalRegistry(db), sponsorLookup(engine))(apiMux.mux))
	topMux.Handle("/v1/", v1)
	if o.mcp {
		// Without resource metadata a 401 names no document to read.
		topMux.Handle(MCPPath, mcpEndpoint(v1, apiKeys, authn, metadataURL))
	}
	if console != nil {
		// The console shell, its assets and /auth/* need no credential:
		// after the cross-site return from login the browser withholds the
		// Strict session cookie from the document request, so the shell
		// must load without it (spec 016 T001 spike finding 6).
		console.init(authn)
		console.register(topMux)
	} else {
		// Same reasoning as apiMux's catch-all, for paths outside /v1/ and
		// /healthz entirely (e.g. a bare "/" or a typo'd top-level path).
		topMux.HandleFunc("/", notFoundHandler)
	}

	srv := &http.Server{
		Handler:           RequestIDMiddleware(topMux),
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
		MaxHeaderBytes:    MaxHeaderBytes,
	}
	srv.RegisterOnShutdown(stopPolicy)
	return srv
}

// PolicyRebuildInterval is how often the policy index reloads, which picks
// up assignments written by another process (the break-glass command).
// Set it before NewServer; tests shorten it.
var PolicyRebuildInterval = 30 * time.Second

// newPolicyEngine returns the policy engine over db's role assignments and
// definitions, kept current by bus events and a periodic rebuild, and the
// function that stops both.
func newPolicyEngine(db *sql.DB, bus event.Bus) (*policy.Engine, func()) {
	assignments := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment).SetBus(bus)
	definitions := sqlite.NewStore[resource.RoleDefinitionSpec, resource.RoleDefinitionStatus](db, resource.KindRoleDefinition).SetBus(bus)
	engine := policy.NewEngine(assignments, definitions, graph.NewSQLiteGraph(db).Owners)
	stopWatch := engine.Watch(bus)
	ctx, cancel := context.WithCancel(context.Background())
	go engine.Run(ctx, PolicyRebuildInterval)
	return engine, func() { cancel(); stopWatch() }
}

// apiKeyMigration describes the Owner assignment an unscoped API key gets.
const apiKeyMigration = "migrated from unscoped API key (spec 017)"

// migrateAPIKeys makes each API key that has never had a role assignment
// Owner at "/", so keys from before authorization keep working (plan.md
// decision 7). A key that has, or ever had, an assignment is left alone, so
// an operator can narrow it, or revoke it entirely, and a restart doesn't
// undo that. Failures are logged: the key then has no access, which is the
// safe direction.
//
// A key no longer configured loses its assignments, unless one of them is
// the last Owner at "/": that one stays, with a warning, so removing a key
// can't lock everyone out.
func migrateAPIKeys(engine *policy.Engine, db *sql.DB, bus event.Bus, apiKeys map[string]string) {
	ctx := audit.WithInterface(audit.WithActor(context.Background(), "serendipd"), "bootstrap")
	assignments := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment).SetBus(bus)
	actors := slices.Compact(slices.Sorted(maps.Values(apiKeys)))
	unlock := engine.LockAssignments()
	defer unlock()
	removeStaleKeyAssignments(ctx, engine, assignments, actors)
	for _, actor := range actors {
		ref := resource.PrincipalRef{Type: resource.PrincipalAPIKey, ID: actor}
		has, err := engine.HasAssignment(ctx, ref)
		if err == nil && !has {
			has, err = sqlite.EverAssigned(ctx, db, ref.Type, ref.ID)
		}
		if err != nil {
			log.Printf("api: check API key %q's role assignments: %v", actor, err)
			continue
		}
		if has {
			continue
		}
		_, err = assignments.Create(ctx, resource.RoleAssignment{
			Metadata: resource.Metadata{Name: "apikey-" + actor + "-owner"},
			Spec: resource.RoleAssignmentSpec{Principal: ref, RoleDefinitionID: policy.OwnerRoleID, Scope: resource.RootScope,
				Description: apiKeyMigration},
		})
		if err != nil {
			log.Printf("api: make API key %q Owner at \"/\": %v", actor, err)
			continue
		}
		log.Printf("api: API key %q had no role assignment; it is now Owner at \"/\" (%s)", actor, apiKeyMigration)
	}
	// Belt and braces: the store's events have already marked the index
	// stale.
	engine.Invalidate()
}

// removeStaleKeyAssignments deletes the assignments of API keys that are no
// longer configured, keeping the last Owner at "/".
func removeStaleKeyAssignments(ctx context.Context, engine *policy.Engine, assignments store.Store[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus], configured []string) {
	all, err := engine.Assignments(ctx)
	if err != nil {
		log.Printf("api: list role assignments to clean up removed API keys: %v", err)
		return
	}
	for _, a := range all {
		p := a.Spec.Principal
		if p.Type != resource.PrincipalAPIKey || slices.Contains(configured, p.ID) {
			continue
		}
		last, err := engine.IsLastRootOwner(ctx, a.Metadata.ID)
		if err != nil {
			log.Printf("api: API key %q is no longer configured; keeping its assignment %s, since checking for the last Owner failed: %v", p.ID, a.Metadata.ID, err)
			continue
		}
		if last {
			log.Printf("api: WARNING: API key %q is no longer configured, but its assignment %s is the last Owner at \"/\"; keeping it", p.ID, a.Metadata.ID)
			continue
		}
		if err := assignments.Delete(store.WithApproval(ctx, configApproval(ctx, a.Metadata.ID, p.ID)), a.Metadata.ID, false); err != nil {
			log.Printf("api: remove assignment %s of removed API key %q: %v", a.Metadata.ID, p.ID, err)
			continue
		}
		log.Printf("api: API key %q is no longer configured; removed its assignment %s", p.ID, a.Metadata.ID)
	}
}

// sponsorLookup names an agent's sponsor for its audit records: the
// sponsor's subject, or "" when the agent has none (no assignment, so no
// access either). A failed lookup is an error: the request is refused
// rather than audited without its sponsor.
func sponsorLookup(engine *policy.Engine) func(ctx context.Context, agentID string) (string, error) {
	return func(ctx context.Context, agentID string) (string, error) {
		s, ok, err := engine.SponsorOf(ctx, agentID)
		if err != nil || !ok {
			return "", err
		}
		return s.ID, nil
	}
}

// ServerOption sets an optional part of NewServer.
type ServerOption func(*serverOptions)

type serverOptions struct {
	directory identity.Directory
	runtime   reconcile.RuntimeProvider
	schemas   reconcile.ConfigSchemaProvider
	metadata  *ResourceMetadata
	mcp       bool
}

// WithDirectory lets the authorization routes read the identity provider's
// directory live (specs/018-agent-manager-poc plan.md decision 5). Without
// it they answer from the principals seen at sign-in.
func WithDirectory(d identity.Directory) ServerOption {
	return func(o *serverOptions) { o.directory = d }
}

// WithConfigSchemas lets Deployments' configuration be checked against
// the platform's schemas, and serves them (spec 020). Without it,
// configuration is refused (503) and the schema route answers 503.
func WithConfigSchemas(s reconcile.ConfigSchemaProvider) ServerOption {
	return func(o *serverOptions) { o.schemas = s }
}

// WithRuntime lets the runtime routes read what Deployments run as, live
// from the application platform (spec 020). Without it they answer 503.
func WithRuntime(rt reconcile.RuntimeProvider) ServerOption {
	return func(o *serverOptions) { o.runtime = rt }
}

// registerAPI registers every /v1 route on mux. NewServer passes its
// authorizing mux; the fail-closed test passes a recorder to enumerate them.
func registerAPI(apiMux Mux, db *sql.DB, bus event.Bus, engine *policy.Engine, approvals store.Store[resource.ApprovalSpec, resource.ApprovalStatus], secretReconciler *reconcile.SecretReconciler, providers *provider.Registry, console *Console, opts serverOptions) {
	// 14 explicit instantiations, deliberately not abstracted further
	// (specs/002-http-api/plan.md Risks: "each line is trivial and
	// independently readable" -- a loop over a slice of type-erased
	// descriptors would need reflection or code generation to recover
	// the per-kind generic type parameters, which is worse).
	mountResource[resource.OrganizationSpec, resource.OrganizationStatus](apiMux, db, bus, resource.KindOrganization, "organizations")
	mountResource[resource.TeamSpec, resource.TeamStatus](apiMux, db, bus, resource.KindTeam, "teams")
	envStore := sqlite.NewStore[resource.EnvironmentSpec, resource.EnvironmentStatus](db, resource.KindEnvironment).SetBus(bus)
	projectStore := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](db, resource.KindProject).SetBus(bus)
	// A Project's pipeline names only its own Environments (spec 020).
	NewResourceHandler[resource.ProjectSpec, resource.ProjectStatus](projectStore, "projects").WithValidate(projectValidator(envStore)).Register(apiMux)
	NewResourceHandler[resource.EnvironmentSpec, resource.EnvironmentStatus](envStore, "environments").Register(apiMux)
	mountResource[resource.SiteSpec, resource.SiteStatus](apiMux, db, bus, resource.KindSite, "sites")
	mountResource[resource.ClusterSpec, resource.ClusterStatus](apiMux, db, bus, resource.KindCluster, "clusters")
	mountResource[resource.NodeSpec, resource.NodeStatus](apiMux, db, bus, resource.KindNode, "nodes")
	mountResource[resource.ApplicationSpec, resource.ApplicationStatus](apiMux, db, bus, resource.KindApplication, "applications")
	compStore := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](db, resource.KindComponent).SetBus(bus)
	NewResourceHandler[resource.ComponentSpec, resource.ComponentStatus](compStore, "components").Register(apiMux)
	// Deployments (spec 020): their own Create, Update and Promote.
	deployStore := sqlite.NewStore[resource.DeploymentSpec, resource.DeploymentStatus](db, resource.KindDeployment).SetBus(bus)
	(&DeploymentHandler{
		ResourceHandler: NewResourceHandler[resource.DeploymentSpec, resource.DeploymentStatus](deployStore, "deployments"),
		deployments:     deployStore, envs: envStore, comps: compStore,
		apps:     sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, resource.KindApplication).SetBus(bus),
		projects: projectStore,
		schemas:  opts.schemas,
	}).Register(apiMux)
	(&SchemaHandler{comps: compStore, schemas: opts.schemas}).Register(apiMux)
	NewRuntimeHandler(deployStore, envStore, opts.runtime).Register(apiMux)
	// Capability's store is shared with RegistryHandler below (T019 gap
	// fix -- specs/002-http-api/spec.md Scope-IN item 2, "get/set a
	// capability's default provider", was never wired up) rather than
	// going through mountResource's private per-call store construction.
	capStore := sqlite.NewStore[resource.CapabilitySpec, resource.CapabilityStatus](db, resource.KindCapability).SetBus(bus)
	NewResourceHandler[resource.CapabilitySpec, resource.CapabilityStatus](capStore, "capabilities").Register(apiMux)
	mountResource[resource.ProviderSpec, resource.ProviderStatus](apiMux, db, bus, resource.KindProvider, "providers")
	mountResource[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](apiMux, db, bus, resource.KindProviderInstance, "provider-instances")
	mountResource[resource.IncidentSpec, resource.IncidentStatus](apiMux, db, bus, resource.KindIncident, "incidents")
	mountResource[resource.AlertSpec, resource.AlertStatus](apiMux, db, bus, resource.KindAlert, "alerts")
	mountResource[resource.VolumeSpec, resource.VolumeStatus](apiMux, db, bus, resource.KindVolume, "volumes")
	// Not mountResource -- SecretHandler isn't the generic
	// ResourceHandler[S,St] (its Delete and value routes need
	// secretReconciler, not just a store.Store), and is skipped
	// entirely when secretReconciler is nil (see this func's own doc
	// comment).
	if secretReconciler != nil {
		secretStore := sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](db, resource.KindSecret).SetBus(bus)
		NewSecretHandler(secretStore, secretReconciler).Register(apiMux)
	}

	NewGraphHandler(graph.NewSQLiteGraph(db)).Register(apiMux)
	(&ApprovalHandler{
		approvals: approvals,
		query: func(ctx context.Context, q sqlite.ApprovalQuery) ([]resource.Approval, error) {
			return sqlite.ListApprovals(ctx, db, q)
		},
		tree: graph.NewSQLiteGraph(db),
		head: func(ctx context.Context, kind resource.Kind, id string) (string, int64, error) {
			return sqlite.ResourceHead(ctx, db, kind, id)
		},
		now: time.Now,
	}).Register(apiMux)
	NewRegistryHandler(registry.NewCapabilityRegistry(capStore)).Register(apiMux)
	NewReadinessHandler(providers).Register(apiMux)
	(&AuthorizationHandler{
		engine:       engine,
		assignments:  sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment).SetBus(bus),
		definitions:  sqlite.NewStore[resource.RoleDefinitionSpec, resource.RoleDefinitionStatus](db, resource.KindRoleDefinition).SetBus(bus),
		principals:   sqlite.NewPrincipalRegistry(db),
		directory:    opts.directory,
		recordDenied: recordDeniedIn(db),
		exists:       resourceExistsIn(db),
	}).Register(apiMux)
	apiMux.HandleFunc("GET /v1/session", console.sessionHandler)
	// Catch-all for anything under /v1/ that doesn't match a registered
	// kind/graph/registry route -- without this, an unmatched /v1/ path
	// falls through to Go's default plain-text 404, violating acceptance
	// criterion 4's literal "every error response is valid ... problem+json"
	// (T019 final-review gap fix, contract-guardian non-blocking finding).
	apiMux.HandleFunc("/", notFoundHandler)
}

func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	WriteProblem(w, http.StatusNotFound, "not found", "no route matches "+r.Method+" "+r.URL.Path, audit.RequestIDFrom(r.Context()))
}

// mountResource constructs a Store[S, St] for kind (wired to bus) and
// registers its 5 REST routes onto mux at the given path segment.
func mountResource[S, St any](mux Mux, db *sql.DB, bus event.Bus, kind resource.Kind, path string) {
	s := sqlite.NewStore[S, St](db, kind).SetBus(bus)
	NewResourceHandler[S, St](s, path).Register(mux)
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// resourceExistsIn says whether a resource of any kind exists in db.
func resourceExistsIn(db *sql.DB) func(ctx context.Context, kind resource.Kind, id string) (bool, error) {
	return func(ctx context.Context, kind resource.Kind, id string) (bool, error) {
		return sqlite.ResourceExists(ctx, db, kind, id)
	}
}
