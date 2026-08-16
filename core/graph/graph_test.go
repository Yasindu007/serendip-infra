package graph_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() = %v", err)
	}
	return db
}

func ref(kind resource.Kind, id string) graph.ResourceRef {
	return graph.ResourceRef{Kind: kind, ID: id}
}

// SQLiteGraph is the public Graph interface implementation --
// core/store/sqlite calls the package-level Children/Dependents/
// BlastRadius/SyncEdges functions directly against its own *sql.Tx (the
// other tests in this file), but SQLiteGraph{db} is what a future
// consumer outside a store transaction (e.g. an HTTP handler in spec 002,
// or the AI-ops blast-radius queries ai-operations.md describes) will
// actually construct and call. Confirm it delegates correctly, not just
// that the underlying free functions work.
func TestSQLiteGraph_DelegatesToPackageFunctions(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	g := graph.NewSQLiteGraph(db)

	org := ref(resource.KindOrganization, "org-1")
	project := ref(resource.KindProject, "project-1")
	cluster := ref(resource.KindCluster, "cluster-1")
	backend := ref(resource.KindComponent, "backend")
	frontend := ref(resource.KindComponent, "frontend")

	if err := graph.SyncEdges(ctx, db, project,
		[]resource.OwnerRef{{Kind: org.Kind, ID: org.ID}}, nil); err != nil {
		t.Fatalf("SyncEdges(project) = %v", err)
	}
	if err := graph.SyncEdges(ctx, db, backend, nil,
		[]resource.Relationship{
			{Type: resource.EdgeRunsOn, TargetKind: cluster.Kind, TargetID: cluster.ID},
		}); err != nil {
		t.Fatalf("SyncEdges(backend) = %v", err)
	}
	if err := graph.SyncEdges(ctx, db, frontend, nil,
		[]resource.Relationship{
			{Type: resource.EdgeDependsOn, TargetKind: backend.Kind, TargetID: backend.ID},
		}); err != nil {
		t.Fatalf("SyncEdges(frontend) = %v", err)
	}

	t.Run("Children", func(t *testing.T) {
		got, err := g.Children(ctx, org)
		if err != nil {
			t.Fatalf("Children(org) = %v", err)
		}
		if len(got) != 1 || got[0] != project {
			t.Errorf("Children(org) = %+v, want [%+v]", got, project)
		}
	})

	t.Run("Dependents", func(t *testing.T) {
		got, err := g.Dependents(ctx, backend)
		if err != nil {
			t.Fatalf("Dependents(backend) = %v", err)
		}
		if len(got) != 1 || got[0] != frontend {
			t.Errorf("Dependents(backend) = %+v, want [%+v]", got, frontend)
		}
	})

	t.Run("BlastRadius", func(t *testing.T) {
		got, err := g.BlastRadius(ctx, cluster)
		if err != nil {
			t.Fatalf("BlastRadius(cluster) = %v", err)
		}
		found := map[graph.ResourceRef]bool{}
		for _, r := range got {
			found[r] = true
		}
		if !found[backend] || !found[frontend] {
			t.Errorf("BlastRadius(cluster) = %+v, want it to include backend and frontend", got)
		}
	})
}

func TestSyncEdges_OwnsAndRelationships(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	org := ref(resource.KindOrganization, "org-1")
	project := ref(resource.KindProject, "project-1")
	cluster := ref(resource.KindCluster, "cluster-1")

	// project is owned by org, and declares a runs-on relationship to cluster.
	err := graph.SyncEdges(ctx, db, project,
		[]resource.OwnerRef{{Kind: org.Kind, ID: org.ID}},
		[]resource.Relationship{{Type: resource.EdgeRunsOn, TargetKind: cluster.Kind, TargetID: cluster.ID}})
	if err != nil {
		t.Fatalf("SyncEdges() = %v", err)
	}

	children, err := graph.Children(ctx, db, org)
	if err != nil {
		t.Fatalf("Children(org) = %v", err)
	}
	if len(children) != 1 || children[0] != project {
		t.Errorf("Children(org) = %+v, want [%+v]", children, project)
	}

	// runs-on isn't an owns edge, so it must not show up as a child.
	clusterChildren, err := graph.Children(ctx, db, cluster)
	if err != nil {
		t.Fatalf("Children(cluster) = %v", err)
	}
	if len(clusterChildren) != 0 {
		t.Errorf("Children(cluster) = %+v, want empty (runs-on isn't owns)", clusterChildren)
	}
}

func TestSyncEdges_Idempotent_ReplacesPreviousSet(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	org1 := ref(resource.KindOrganization, "org-1")
	org2 := ref(resource.KindOrganization, "org-2")
	project := ref(resource.KindProject, "project-1")

	if err := graph.SyncEdges(ctx, db, project, []resource.OwnerRef{{Kind: org1.Kind, ID: org1.ID}}, nil); err != nil {
		t.Fatalf("SyncEdges(owner=org1) = %v", err)
	}
	// Re-sync with a different owner -- org1's edge must be gone, only org2's remains.
	if err := graph.SyncEdges(ctx, db, project, []resource.OwnerRef{{Kind: org2.Kind, ID: org2.ID}}, nil); err != nil {
		t.Fatalf("SyncEdges(owner=org2) = %v", err)
	}

	org1Children, err := graph.Children(ctx, db, org1)
	if err != nil {
		t.Fatalf("Children(org1) = %v", err)
	}
	if len(org1Children) != 0 {
		t.Errorf("Children(org1) = %+v, want empty after re-sync dropped this owner", org1Children)
	}
	org2Children, err := graph.Children(ctx, db, org2)
	if err != nil {
		t.Fatalf("Children(org2) = %v", err)
	}
	if len(org2Children) != 1 || org2Children[0] != project {
		t.Errorf("Children(org2) = %+v, want [%+v]", org2Children, project)
	}
}

func TestDependents(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	database := ref(resource.KindComponent, "database")
	backend := ref(resource.KindComponent, "backend")

	if err := graph.SyncEdges(ctx, db, backend, nil,
		[]resource.Relationship{{Type: resource.EdgeDependsOn, TargetKind: database.Kind, TargetID: database.ID}}); err != nil {
		t.Fatalf("SyncEdges() = %v", err)
	}

	dependents, err := graph.Dependents(ctx, db, database)
	if err != nil {
		t.Fatalf("Dependents(database) = %v", err)
	}
	if len(dependents) != 1 || dependents[0] != backend {
		t.Errorf("Dependents(database) = %+v, want [%+v]", dependents, backend)
	}
}

func TestBlastRadius_TransitiveReverseTraversal(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	cluster := ref(resource.KindCluster, "cluster-1")
	backend := ref(resource.KindComponent, "backend")
	frontend := ref(resource.KindComponent, "frontend")

	// backend runs-on cluster; frontend depends-on backend.
	if err := graph.SyncEdges(ctx, db, backend, nil,
		[]resource.Relationship{{Type: resource.EdgeRunsOn, TargetKind: cluster.Kind, TargetID: cluster.ID}}); err != nil {
		t.Fatalf("SyncEdges(backend) = %v", err)
	}
	if err := graph.SyncEdges(ctx, db, frontend, nil,
		[]resource.Relationship{{Type: resource.EdgeDependsOn, TargetKind: backend.Kind, TargetID: backend.ID}}); err != nil {
		t.Fatalf("SyncEdges(frontend) = %v", err)
	}

	// If the cluster dies: backend is directly affected (runs-on it), and
	// frontend is transitively affected (depends-on backend).
	radius, err := graph.BlastRadius(ctx, db, cluster)
	if err != nil {
		t.Fatalf("BlastRadius(cluster) = %v", err)
	}
	got := map[graph.ResourceRef]bool{}
	for _, r := range radius {
		got[r] = true
	}
	if !got[backend] {
		t.Errorf("BlastRadius(cluster) = %+v, want it to include backend (runs-on cluster)", radius)
	}
	if !got[frontend] {
		t.Errorf("BlastRadius(cluster) = %+v, want it to include frontend (transitively, via backend)", radius)
	}
}

func TestDeleteEdgesFor_RemovesBothDirections(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	org := ref(resource.KindOrganization, "org-1")
	project := ref(resource.KindProject, "project-1")
	cluster := ref(resource.KindCluster, "cluster-1")

	if err := graph.SyncEdges(ctx, db, project,
		[]resource.OwnerRef{{Kind: org.Kind, ID: org.ID}},
		[]resource.Relationship{{Type: resource.EdgeRunsOn, TargetKind: cluster.Kind, TargetID: cluster.ID}}); err != nil {
		t.Fatalf("SyncEdges() = %v", err)
	}

	if err := graph.DeleteEdgesFor(ctx, db, project); err != nil {
		t.Fatalf("DeleteEdgesFor(project) = %v", err)
	}

	orgChildren, err := graph.Children(ctx, db, org)
	if err != nil {
		t.Fatalf("Children(org) = %v", err)
	}
	if len(orgChildren) != 0 {
		t.Errorf("Children(org) = %+v, want empty after DeleteEdgesFor(project) (project was the 'to' side)", orgChildren)
	}

	var edgeCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM edges WHERE from_kind = ? AND from_id = ?`,
		string(project.Kind), project.ID).Scan(&edgeCount); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if edgeCount != 0 {
		t.Errorf("edges from project = %d, want 0 after DeleteEdgesFor (project was also the 'from' side)", edgeCount)
	}
}
