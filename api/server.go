package api

import (
	"database/sql"
	"net/http"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/registry"
	"github.com/serendip-infra/serendip-infra/core/resource"
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
// it), the middleware chain (request ID -> actor -> mux,
// specs/002-http-api/plan.md), and a GET /healthz that needs no token.
//
// apiKeys is the static token-to-actor map for ActorMiddleware -- see its
// doc comment for why this is intentionally temporary.
func NewServer(db *sql.DB, bus event.Bus, apiKeys map[string]string) *http.Server {
	apiMux := http.NewServeMux()

	// 14 explicit instantiations, deliberately not abstracted further
	// (specs/002-http-api/plan.md Risks: "each line is trivial and
	// independently readable" -- a loop over a slice of type-erased
	// descriptors would need reflection or code generation to recover
	// the per-kind generic type parameters, which is worse).
	mountResource[resource.OrganizationSpec, resource.OrganizationStatus](apiMux, db, bus, resource.KindOrganization, "organizations")
	mountResource[resource.TeamSpec, resource.TeamStatus](apiMux, db, bus, resource.KindTeam, "teams")
	mountResource[resource.ProjectSpec, resource.ProjectStatus](apiMux, db, bus, resource.KindProject, "projects")
	mountResource[resource.EnvironmentSpec, resource.EnvironmentStatus](apiMux, db, bus, resource.KindEnvironment, "environments")
	mountResource[resource.SiteSpec, resource.SiteStatus](apiMux, db, bus, resource.KindSite, "sites")
	mountResource[resource.ClusterSpec, resource.ClusterStatus](apiMux, db, bus, resource.KindCluster, "clusters")
	mountResource[resource.NodeSpec, resource.NodeStatus](apiMux, db, bus, resource.KindNode, "nodes")
	mountResource[resource.ApplicationSpec, resource.ApplicationStatus](apiMux, db, bus, resource.KindApplication, "applications")
	mountResource[resource.ComponentSpec, resource.ComponentStatus](apiMux, db, bus, resource.KindComponent, "components")
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

	NewGraphHandler(graph.NewSQLiteGraph(db)).Register(apiMux)
	NewRegistryHandler(registry.NewCapabilityRegistry(capStore)).Register(apiMux)
	// Catch-all for anything under /v1/ that doesn't match a registered
	// kind/graph/registry route -- without this, an unmatched /v1/ path
	// falls through to Go's default plain-text 404, violating acceptance
	// criterion 4's literal "every error response is valid ... problem+json"
	// (T019 final-review gap fix, contract-guardian non-blocking finding).
	apiMux.HandleFunc("/", notFoundHandler)

	topMux := http.NewServeMux()
	topMux.HandleFunc("GET /healthz", healthzHandler)
	// Everything under /v1/ requires a resolved actor; /healthz doesn't
	// go through ActorMiddleware at all -- it's mounted directly on
	// topMux, not apiMux, so there's no bypass logic to get wrong inside
	// a shared handler.
	topMux.Handle("/v1/", ActorMiddleware(apiKeys)(apiMux))
	// Same reasoning as apiMux's catch-all, for paths outside /v1/ and
	// /healthz entirely (e.g. a bare "/" or a typo'd top-level path).
	topMux.HandleFunc("/", notFoundHandler)

	return &http.Server{
		Handler: RequestIDMiddleware(topMux),
	}
}

func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	WriteProblem(w, http.StatusNotFound, "not found", "no route matches "+r.Method+" "+r.URL.Path, audit.RequestIDFrom(r.Context()))
}

// mountResource constructs a Store[S, St] for kind (wired to bus) and
// registers its 5 REST routes onto mux at the given path segment.
func mountResource[S, St any](mux *http.ServeMux, db *sql.DB, bus event.Bus, kind resource.Kind, path string) {
	s := sqlite.NewStore[S, St](db, kind).SetBus(bus)
	NewResourceHandler[S, St](s, path).Register(mux)
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
