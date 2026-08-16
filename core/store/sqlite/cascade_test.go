package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// This is spec 001's acceptance criterion 2 (spec.md): "Creating an
// Application under a Project under an Organization produces correct
// graph edges; deleting the Project with children is rejected unless
// cascade requested." It spans three different resource kinds sharing
// one *sql.DB through three separately-typed Store[S, St] instances --
// exactly the scenario core/graph's kind-agnostic cascade delete exists
// for (a Store[ProjectSpec, ...] can't natively delete an
// ApplicationSpec-typed child).

func newCascadeTestDB(t *testing.T) *sql.DB {
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

func TestAcceptanceCriterion2_OrgProjectApplication(t *testing.T) {
	ctx := context.Background()
	db := newCascadeTestDB(t)

	orgs := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)
	projects := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](db, resource.KindProject)
	apps := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, resource.KindApplication)

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

	app, err := apps.Create(ctx, resource.Application{
		Metadata: resource.Metadata{
			Name:      "frontend",
			OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: project.Metadata.ID}},
		},
	})
	if err != nil {
		t.Fatalf("Create(app) = %v", err)
	}

	// Deleting the Project without cascade must be rejected -- it owns
	// the Application.
	err = projects.Delete(ctx, project.Metadata.ID, false)
	if !errors.Is(err, store.ErrHasChildren) {
		t.Fatalf("Delete(project, cascade=false) error = %v, want ErrHasChildren", err)
	}
	// The project and application must still exist -- rejected means rejected.
	if _, err := projects.Get(ctx, project.Metadata.ID); err != nil {
		t.Errorf("Get(project) after rejected delete = %v, want it to still exist", err)
	}
	if _, err := apps.Get(ctx, app.Metadata.ID); err != nil {
		t.Errorf("Get(app) after rejected delete = %v, want it to still exist", err)
	}

	// Deleting the Organization without cascade must also be rejected --
	// transitively, it owns the Project (though not directly).
	err = orgs.Delete(ctx, org.Metadata.ID, false)
	if !errors.Is(err, store.ErrHasChildren) {
		t.Fatalf("Delete(org, cascade=false) error = %v, want ErrHasChildren", err)
	}

	// With cascade=true, deleting the Organization removes the Project
	// and the Application too (multi-kind cascade, post-order).
	if err := orgs.Delete(ctx, org.Metadata.ID, true); err != nil {
		t.Fatalf("Delete(org, cascade=true) = %v", err)
	}
	if _, err := orgs.Get(ctx, org.Metadata.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get(org) after cascade delete = %v, want ErrNotFound", err)
	}
	if _, err := projects.Get(ctx, project.Metadata.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get(project) after cascade delete = %v, want ErrNotFound", err)
	}
	if _, err := apps.Get(ctx, app.Metadata.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get(app) after cascade delete = %v, want ErrNotFound", err)
	}

	// Every deletion in the cascade (org, project, app) must have its own
	// audit record, plus the 3 Create records from setup = 6 total.
	var auditCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log`).Scan(&auditCount); err != nil {
		t.Fatalf("count audit_log: %v", err)
	}
	if auditCount != 6 {
		t.Errorf("audit_log rows = %d, want 6 (3 creates + 3 cascade deletes)", auditCount)
	}

	// No dangling edges should remain after the cascade.
	var edgeCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM edges`).Scan(&edgeCount); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if edgeCount != 0 {
		t.Errorf("edges remaining after cascade delete = %d, want 0", edgeCount)
	}
}

func TestDelete_CascadeFalse_NoChildren_Succeeds(t *testing.T) {
	ctx := context.Background()
	db := newCascadeTestDB(t)
	orgs := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)

	org, err := orgs.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("Create(org) = %v", err)
	}

	// No children at all -- cascade=false must succeed, not be rejected.
	if err := orgs.Delete(ctx, org.Metadata.ID, false); err != nil {
		t.Fatalf("Delete(org with no children, cascade=false) = %v, want success", err)
	}
}

// resource.Validate rejects a Relationship of type EdgeOwns, but doesn't
// (and can't, cheaply) reject a cycle formed via OwnerRefs across two
// separate mutations (A owns B, then a later Update makes B own A too).
// cascadeDeleteChild's visited-set guard exists for exactly this case --
// confirm it turns a malformed cycle into an error, not a stack overflow.
func TestDelete_Cascade_CycleDetected(t *testing.T) {
	ctx := context.Background()
	db := newCascadeTestDB(t)
	orgs := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)

	a, err := orgs.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "a"}})
	if err != nil {
		t.Fatalf("Create(a) = %v", err)
	}
	b, err := orgs.Create(ctx, resource.Organization{
		Metadata: resource.Metadata{
			Name:      "b",
			OwnerRefs: []resource.OwnerRef{{Kind: resource.KindOrganization, ID: a.Metadata.ID}},
		},
	})
	if err != nil {
		t.Fatalf("Create(b, owned by a) = %v", err)
	}

	// Close the cycle: a now also owned by b.
	a.Metadata.OwnerRefs = []resource.OwnerRef{{Kind: resource.KindOrganization, ID: b.Metadata.ID}}
	if _, err := orgs.Update(ctx, a); err != nil {
		t.Fatalf("Update(a, owned by b) = %v", err)
	}

	err = orgs.Delete(ctx, a.Metadata.ID, true)
	if err == nil {
		t.Fatal("Delete(a, cascade=true) on a cyclic owns-graph succeeded, want a cycle-detected error")
	}
}

// TestCascadeDelete_AuditSnapshotShape_MatchesOrdinaryDelete proves a
// cascade-deleted child's audit "before" snapshot has the same shape and
// key casing (lowerCamelCase, nested under "metadata") as an ordinary
// (non-cascaded) delete's -- contract-guardian flagged this as a real gap
// after T005-T010 added json tags to core/resource: rawSnapshotJSON built
// its own ad hoc struct with hardcoded capitalized tags, independent of
// resource.Metadata, so cascade-deleted children's audit records would
// have looked different from every other resource's.
func TestCascadeDelete_AuditSnapshotShape_MatchesOrdinaryDelete(t *testing.T) {
	ctx := context.Background()
	db := newCascadeTestDB(t)

	orgs := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)
	projects := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](db, resource.KindProject)

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

	// org has a child (project), so deleting it cascades -- project's
	// audit "before" snapshot goes through cascadeDeleteChild/
	// rawSnapshotJSON, not the ordinary typed Delete() path.
	if err := orgs.Delete(ctx, org.Metadata.ID, true); err != nil {
		t.Fatalf("Delete(org, cascade=true) = %v", err)
	}

	var beforeJSON string
	if err := db.QueryRowContext(ctx,
		`SELECT before FROM audit_log WHERE resource_id = ? AND after IS NULL`, project.Metadata.ID,
	).Scan(&beforeJSON); err != nil {
		t.Fatalf("query cascade child's audit before snapshot: %v", err)
	}

	for _, wantKey := range []string{
		`"metadata":{`, `"id":"` + project.Metadata.ID + `"`, `"name":"web"`,
		`"kind":"Project"`, `"resourceVersion":1`, `"spec":`, `"status":`,
		// project has a real OwnerRef (org) but no Labels/Relationships --
		// present vs. omitted keys must both be correct, not just casing.
		`"ownerRefs":[{`,
	} {
		if !strings.Contains(beforeJSON, wantKey) {
			t.Errorf("cascade child audit snapshot = %s, want it to contain %s", beforeJSON, wantKey)
		}
	}
	for _, wantAbsent := range []string{
		`"Name":`, `"ID":`, `"ResourceVersion":`, `"Metadata":`, // old ad hoc capitalized shape
		// project.Metadata.Labels/Relationships were never set (nil), so
		// omitempty must actually drop these keys -- the bug this test
		// exists to catch: wrapping raw DB column text ("null") in
		// json.RawMessage defeats omitempty (RawMessage("null") has
		// len()==4, never counts as empty), even though the real
		// resource.Metadata's nil map/slice correctly omits under the
		// same tag. Fixed by unmarshaling into typed map/slice fields
		// instead of RawMessage before marshaling the snapshot.
		`"labels":`, `"relationships":`,
	} {
		if strings.Contains(beforeJSON, wantAbsent) {
			t.Errorf("cascade child audit snapshot = %s, want it to NOT contain %s", beforeJSON, wantAbsent)
		}
	}
}
