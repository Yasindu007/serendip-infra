package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// This mirrors spec 001's T013 graph tests (core/graph/graph_test.go),
// now proving the same traversal is reachable through HTTP, the first
// real consumer of graph.SQLiteGraph outside a core/store/sqlite
// transaction.
func TestGraphHandler_ChildrenDependentsBlastRadius(t *testing.T) {
	db := newTestDB(t)
	orgs := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)
	projects := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](db, resource.KindProject)
	clusters := sqlite.NewStore[resource.ClusterSpec, resource.ClusterStatus](db, resource.KindCluster)
	components := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](db, resource.KindComponent)

	ctx := t.Context()
	org, err := orgs.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("Create(org) = %v", err)
	}
	project, err := projects.Create(ctx, resource.Project{
		Metadata: resource.Metadata{
			Name:      "web",
			OwnerRefs: []resource.OwnerRef{{Kind: resource.KindOrganization, ID: org.Metadata.ID}},
		},
	})
	if err != nil {
		t.Fatalf("Create(project) = %v", err)
	}
	cluster, err := clusters.Create(ctx, resource.Cluster{Metadata: resource.Metadata{Name: "cluster-1"}})
	if err != nil {
		t.Fatalf("Create(cluster) = %v", err)
	}
	backend, err := components.Create(ctx, resource.Component{
		Metadata: resource.Metadata{
			Name: "backend",
			Relationships: []resource.Relationship{
				{Type: resource.EdgeRunsOn, TargetKind: resource.KindCluster, TargetID: cluster.Metadata.ID},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create(backend) = %v", err)
	}
	frontend, err := components.Create(ctx, resource.Component{
		Metadata: resource.Metadata{
			Name: "frontend",
			Relationships: []resource.Relationship{
				{Type: resource.EdgeDependsOn, TargetKind: resource.KindComponent, TargetID: backend.Metadata.ID},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create(frontend) = %v", err)
	}

	mux := http.NewServeMux()
	api.NewGraphHandler(graph.NewSQLiteGraph(db)).Register(mux)

	t.Run("children", func(t *testing.T) {
		got := doGraphRequest(t, mux, string(resource.KindOrganization), org.Metadata.ID, "children")
		assertContainsRef(t, got, resource.KindProject, project.Metadata.ID)
	})

	t.Run("dependents", func(t *testing.T) {
		got := doGraphRequest(t, mux, string(resource.KindComponent), backend.Metadata.ID, "dependents")
		assertContainsRef(t, got, resource.KindComponent, frontend.Metadata.ID)
	})

	t.Run("blast-radius", func(t *testing.T) {
		// If the cluster dies: backend (runs-on it) and frontend
		// (transitively, via depends-on backend) are both affected.
		got := doGraphRequest(t, mux, string(resource.KindCluster), cluster.Metadata.ID, "blast-radius")
		assertContainsRef(t, got, resource.KindComponent, backend.Metadata.ID)
		assertContainsRef(t, got, resource.KindComponent, frontend.Metadata.ID)
	})
}

func TestGraphHandler_NoEdges_ReturnsEmptyArrayNotError(t *testing.T) {
	db := newTestDB(t)
	mux := http.NewServeMux()
	api.NewGraphHandler(graph.NewSQLiteGraph(db)).Register(mux)

	got := doGraphRequest(t, mux, string(resource.KindOrganization), "does-not-exist", "children")
	if len(got) != 0 {
		t.Errorf("children of a nonexistent/edgeless ref = %+v, want empty array", got)
	}
}

func doGraphRequest(t *testing.T, mux *http.ServeMux, kind, id, edge string) []graph.ResourceRef {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/graph/"+kind+"/"+id+"/"+edge, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/graph/%s/%s/%s status = %d, want %d, body: %s", kind, id, edge, w.Code, http.StatusOK, w.Body)
	}
	var got []graph.ResourceRef
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return got
}

func assertContainsRef(t *testing.T, refs []graph.ResourceRef, kind resource.Kind, id string) {
	t.Helper()
	for _, r := range refs {
		if r.Kind == kind && r.ID == id {
			return
		}
	}
	t.Errorf("refs = %+v, want it to contain {Kind:%s ID:%s}", refs, kind, id)
}
