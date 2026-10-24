package graph_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
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

func TestAncestors(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	owns := func(child, owner graph.ResourceRef) {
		t.Helper()
		if err := graph.SyncEdges(ctx, db, child, []resource.OwnerRef{{Kind: owner.Kind, ID: owner.ID}}, nil); err != nil {
			t.Fatalf("SyncEdges(%v) = %v", child, err)
		}
	}
	org := ref(resource.KindOrganization, "org")
	project := ref(resource.KindProject, "proj")
	env := ref(resource.KindEnvironment, "prod")
	app := ref(resource.KindApplication, "app")
	comp := ref(resource.KindComponent, "web")
	cluster := ref(resource.KindCluster, "c1")
	owns(project, org)
	owns(env, project)
	owns(app, env)
	owns(comp, app)
	// A non-owns edge must not count as inheritance.
	if err := graph.SyncEdges(ctx, db, comp, []resource.OwnerRef{{Kind: app.Kind, ID: app.ID}},
		[]resource.Relationship{{Type: resource.EdgeRunsOn, TargetKind: cluster.Kind, TargetID: cluster.ID}}); err != nil {
		t.Fatal(err)
	}

	t.Run("five levels, nearest first", func(t *testing.T) {
		got, err := graph.NewSQLiteGraph(db).Ancestors(ctx, comp)
		if err != nil {
			t.Fatal(err)
		}
		want := []graph.ResourceRef{app, env, project, org}
		if len(got) != len(want) {
			t.Fatalf("Ancestors(comp) = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("Ancestors(comp)[%d] = %v, want %v", i, got[i], want[i])
			}
		}
	})
	t.Run("unowned resource has none", func(t *testing.T) {
		got, err := graph.Ancestors(ctx, db, org)
		if err != nil || len(got) != 0 {
			t.Errorf("Ancestors(org) = %v, %v; want none", got, err)
		}
	})
	t.Run("several owners are all listed before grandparents", func(t *testing.T) {
		shared := ref(resource.KindComponent, "shared")
		other := ref(resource.KindApplication, "other")
		owns(other, env)
		if err := graph.SyncEdges(ctx, db, shared, []resource.OwnerRef{{Kind: app.Kind, ID: app.ID}, {Kind: other.Kind, ID: other.ID}}, nil); err != nil {
			t.Fatal(err)
		}
		got, err := graph.Ancestors(ctx, db, shared)
		if err != nil || len(got) < 2 {
			t.Fatalf("Ancestors(shared) = %v, %v", got, err)
		}
		first := map[graph.ResourceRef]bool{got[0]: true, got[1]: true}
		if !first[app] || !first[other] {
			t.Errorf("Ancestors(shared) = %v, want both direct owners first", got)
		}
		if got[2] != env || len(got) != 5 {
			t.Errorf("Ancestors(shared) = %v, want env once, then project and org", got)
		}
	})
	t.Run("a cycle ends the walk", func(t *testing.T) {
		a := ref(resource.KindProject, "loop-a")
		b := ref(resource.KindProject, "loop-b")
		owns(a, b)
		owns(b, a)
		got, err := graph.Ancestors(ctx, db, a)
		if err != nil || len(got) != 1 || got[0] != b {
			t.Errorf("Ancestors(loop-a) = %v, %v; want [loop-b] and no infinite loop", got, err)
		}
	})
}

func TestOwnersAndDescendants(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	ref := func(kind resource.Kind, id string) graph.ResourceRef { return graph.ResourceRef{Kind: kind, ID: id} }
	own := func(child graph.ResourceRef, owners ...graph.ResourceRef) {
		t.Helper()
		var refs []resource.OwnerRef
		for _, o := range owners {
			refs = append(refs, resource.OwnerRef{Kind: o.Kind, ID: o.ID})
		}
		if err := graph.SyncEdges(ctx, db, child, refs, nil); err != nil {
			t.Fatal(err)
		}
	}
	proj, env, app := ref(resource.KindProject, "p"), ref(resource.KindEnvironment, "e"), ref(resource.KindApplication, "a")
	web, vol := ref(resource.KindComponent, "w"), ref(resource.KindVolume, "v")
	own(env, proj)
	own(app, env)
	own(web, app)
	own(vol, app, proj)

	owners, err := graph.Owners(ctx, db, vol)
	if err != nil || len(owners) != 2 || !slices.Contains(owners, app) || !slices.Contains(owners, proj) {
		t.Errorf("Owners(vol) = %v, %v; want the application and the project", owners, err)
	}
	below, err := graph.Descendants(ctx, db, proj)
	if err != nil || len(below) != 4 {
		t.Fatalf("Descendants(proj) = %v, %v; want env, app, web and vol once each", below, err)
	}
	for _, want := range []graph.ResourceRef{env, app, web, vol} {
		if !slices.Contains(below, want) {
			t.Errorf("Descendants(proj) lacks %v", want)
		}
	}
	if leaf, err := graph.Descendants(ctx, db, web); err != nil || len(leaf) != 0 {
		t.Errorf("Descendants(web) = %v, %v; want none", leaf, err)
	}
}

// A Deployment (spec 020) is owned by its Environment and depends on its
// Component, through the envelope's generic edges: it's among the
// Component's dependents and blast radius, and among the Environment's
// children (which a cascade delete covers).
func TestDeploymentEdges(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	env := ref(resource.KindEnvironment, "staging")
	comp := ref(resource.KindComponent, "web")
	dep := ref(resource.KindDeployment, "d1")
	if err := graph.SyncEdges(ctx, db, dep, []resource.OwnerRef{{Kind: env.Kind, ID: env.ID}},
		resource.DeploymentRelationships(resource.DeploymentSpec{Component: comp.ID})); err != nil {
		t.Fatal(err)
	}
	if got, _ := graph.Dependents(ctx, db, comp); len(got) != 1 || got[0] != dep {
		t.Errorf("Dependents(component) = %v", got)
	}
	if got, _ := graph.Children(ctx, db, env); len(got) != 1 || got[0] != dep {
		t.Errorf("Children(environment) = %v", got)
	}
	if got, _ := graph.BlastRadius(ctx, db, comp); !slices.Contains(got, dep) {
		t.Errorf("BlastRadius(component) = %v, want the Deployment in it", got)
	}
}

// A Build (spec 022) is owned by its Component: roles on the Component's
// Application or Project reach it (Ancestors), and a cascade delete of the
// Component removes it (Descendants).
func TestBuildOwnedByComponent(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	proj, app, comp := ref(resource.KindProject, "p"), ref(resource.KindApplication, "a"), ref(resource.KindComponent, "web")
	build := ref(resource.KindBuild, "b1")
	for _, e := range [][2]graph.ResourceRef{{app, proj}, {comp, app}, {build, comp}} {
		if err := graph.SyncEdges(ctx, db, e[0], []resource.OwnerRef{{Kind: e[1].Kind, ID: e[1].ID}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := graph.Ancestors(ctx, db, build); err != nil || !slices.Equal(got, []graph.ResourceRef{comp, app, proj}) {
		t.Errorf("Ancestors(build) = %v, %v; want the Component, Application and Project", got, err)
	}
	if got, err := graph.Descendants(ctx, db, comp); err != nil || !slices.Equal(got, []graph.ResourceRef{build}) {
		t.Errorf("Descendants(component) = %v, %v; want the Build", got, err)
	}
}
