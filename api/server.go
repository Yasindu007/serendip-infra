package api

import (
	"database/sql"
	"net/http"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// NewServer assembles the full HTTP API: one ResourceHandler per resource
// kind (14 instantiations below, mirroring core/store/sqlite.Store[S,St]'s
// per-kind pattern), the middleware chain (request ID -> actor -> mux,
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
	mountResource[resource.CapabilitySpec, resource.CapabilityStatus](apiMux, db, bus, resource.KindCapability, "capabilities")
	mountResource[resource.ProviderSpec, resource.ProviderStatus](apiMux, db, bus, resource.KindProvider, "providers")
	mountResource[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](apiMux, db, bus, resource.KindProviderInstance, "provider-instances")
	mountResource[resource.IncidentSpec, resource.IncidentStatus](apiMux, db, bus, resource.KindIncident, "incidents")
	mountResource[resource.AlertSpec, resource.AlertStatus](apiMux, db, bus, resource.KindAlert, "alerts")

	topMux := http.NewServeMux()
	topMux.HandleFunc("GET /healthz", healthzHandler)
	// Everything under /v1/ requires a resolved actor; /healthz doesn't
	// go through ActorMiddleware at all -- it's mounted directly on
	// topMux, not apiMux, so there's no bypass logic to get wrong inside
	// a shared handler.
	topMux.Handle("/v1/", ActorMiddleware(apiKeys)(apiMux))

	return &http.Server{
		Handler: RequestIDMiddleware(topMux),
	}
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
