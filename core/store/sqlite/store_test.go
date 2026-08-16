package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func newTestStore(t *testing.T) *sqlite.Store[resource.OrganizationSpec, resource.OrganizationStatus] {
	t.Helper()
	db, _ := newTestStoreAndDB(t)
	return db
}

func newTestStoreAndDB(t *testing.T) (*sqlite.Store[resource.OrganizationSpec, resource.OrganizationStatus], *sql.DB) {
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
	s := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)
	return s, db
}

// countAuditRecords returns how many audit_log rows exist for resourceID.
func countAuditRecords(t *testing.T, db *sql.DB, resourceID string) int {
	t.Helper()
	var count int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM audit_log WHERE resource_id = ?`, resourceID).Scan(&count)
	if err != nil {
		t.Fatalf("count audit_log rows for %s: %v", resourceID, err)
	}
	return count
}

// lastAuditRecord returns the most recent audit_log row for resourceID.
func lastAuditRecord(t *testing.T, db *sql.DB, resourceID string) audit.Record {
	t.Helper()
	var (
		rec           audit.Record
		before, after sql.NullString
		atStr         string
	)
	err := db.QueryRowContext(context.Background(), `
		SELECT id, actor, interface, kind, resource_id, before, after, at
		FROM audit_log WHERE resource_id = ? ORDER BY at DESC, rowid DESC LIMIT 1`, resourceID,
	).Scan(&rec.ID, &rec.Actor, &rec.Interface, &rec.Kind, &rec.ResourceID, &before, &after, &atStr)
	if err != nil {
		t.Fatalf("query last audit_log row for %s: %v", resourceID, err)
	}
	if before.Valid {
		rec.Before = []byte(before.String)
	}
	if after.Valid {
		rec.After = []byte(after.String)
	}
	return rec
}

func TestStore_CreateGet_Roundtrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	created, err := s.Create(ctx, resource.Organization{
		Metadata: resource.Metadata{
			Name:   "acme",
			Labels: map[string]string{"tier": "prod"},
		},
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if created.Metadata.ID == "" {
		t.Error("Create() did not assign an ID")
	}
	if created.Metadata.Kind != resource.KindOrganization {
		t.Errorf("Kind = %q, want %q", created.Metadata.Kind, resource.KindOrganization)
	}
	if created.Metadata.ResourceVersion != 1 {
		t.Errorf("ResourceVersion = %d, want 1", created.Metadata.ResourceVersion)
	}
	if created.Metadata.CreatedAt.IsZero() || created.Metadata.UpdatedAt.IsZero() {
		t.Error("CreatedAt/UpdatedAt should be set by Create()")
	}

	got, err := s.Get(ctx, created.Metadata.ID)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if got.Metadata.Name != "acme" {
		t.Errorf("Get().Metadata.Name = %q, want %q", got.Metadata.Name, "acme")
	}
	if got.Metadata.Labels["tier"] != "prod" {
		t.Errorf("Get().Metadata.Labels[tier] = %q, want %q", got.Metadata.Labels["tier"], "prod")
	}
	if !got.Metadata.CreatedAt.Equal(created.Metadata.CreatedAt) {
		t.Errorf("Get().CreatedAt = %v, want %v", got.Metadata.CreatedAt, created.Metadata.CreatedAt)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.Get(ctx, "does-not-exist")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestStore_Create_InvalidMetadataRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: ""}})
	if err == nil {
		t.Fatal("Create() with empty Name should fail Validate()")
	}
}

func TestStore_List_FiltersByOwnerAndLabels(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	mustCreate := func(name, ownerID string, labels map[string]string) {
		t.Helper()
		var ownerRefs []resource.OwnerRef
		if ownerID != "" {
			ownerRefs = []resource.OwnerRef{{Kind: resource.KindOrganization, ID: ownerID}}
		}
		if _, err := s.Create(ctx, resource.Organization{
			Metadata: resource.Metadata{Name: name, Labels: labels, OwnerRefs: ownerRefs},
		}); err != nil {
			t.Fatalf("Create(%s) = %v", name, err)
		}
	}

	mustCreate("a", "owner-1", map[string]string{"env": "prod"})
	mustCreate("b", "owner-1", map[string]string{"env": "staging"})
	mustCreate("c", "owner-2", map[string]string{"env": "prod"})

	all, err := s.List(ctx, store.ListOptions{})
	if err != nil {
		t.Fatalf("List(all) = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List(all) len = %d, want 3", len(all))
	}

	byOwner, err := s.List(ctx, store.ListOptions{OwnerID: "owner-1"})
	if err != nil {
		t.Fatalf("List(owner-1) = %v", err)
	}
	if len(byOwner) != 2 {
		t.Errorf("List(owner-1) len = %d, want 2", len(byOwner))
	}

	byLabel, err := s.List(ctx, store.ListOptions{Labels: map[string]string{"env": "prod"}})
	if err != nil {
		t.Fatalf("List(env=prod) = %v", err)
	}
	if len(byLabel) != 2 {
		t.Errorf("List(env=prod) len = %d, want 2", len(byLabel))
	}

	combined, err := s.List(ctx, store.ListOptions{OwnerID: "owner-1", Labels: map[string]string{"env": "prod"}})
	if err != nil {
		t.Fatalf("List(owner-1, env=prod) = %v", err)
	}
	if len(combined) != 1 || combined[0].Metadata.Name != "a" {
		t.Errorf("List(owner-1, env=prod) = %+v, want exactly [a]", combined)
	}
}

func TestStore_Update_Success(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	created, err := s.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	created.Metadata.Name = "acme-renamed"
	updated, err := s.Update(ctx, created)
	if err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if updated.Metadata.Name != "acme-renamed" {
		t.Errorf("Name = %q, want %q", updated.Metadata.Name, "acme-renamed")
	}
	if updated.Metadata.ResourceVersion != created.Metadata.ResourceVersion+1 {
		t.Errorf("ResourceVersion = %d, want %d", updated.Metadata.ResourceVersion, created.Metadata.ResourceVersion+1)
	}
	if !updated.Metadata.CreatedAt.Equal(created.Metadata.CreatedAt) {
		t.Errorf("CreatedAt changed on Update(): got %v, want %v", updated.Metadata.CreatedAt, created.Metadata.CreatedAt)
	}
	if !updated.Metadata.UpdatedAt.After(created.Metadata.UpdatedAt) && !updated.Metadata.UpdatedAt.Equal(created.Metadata.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want >= %v", updated.Metadata.UpdatedAt, created.Metadata.UpdatedAt)
	}
}

func TestStore_Update_VersionConflict(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	created, err := s.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	// Simulate two readers: both start from the same version, one wins.
	staleView := created
	firstWriter := created

	firstWriter.Metadata.Name = "acme-v2"
	if _, err := s.Update(ctx, firstWriter); err != nil {
		t.Fatalf("first Update() = %v, want success", err)
	}

	staleView.Metadata.Name = "acme-conflict"
	_, err = s.Update(ctx, staleView)
	if !errors.Is(err, store.ErrConflict) {
		t.Errorf("second Update() error = %v, want ErrConflict", err)
	}
}

func TestStore_Update_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.Update(ctx, resource.Organization{
		Metadata: resource.Metadata{ID: "does-not-exist", Name: "x", ResourceVersion: 1},
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Update() on missing resource error = %v, want ErrNotFound", err)
	}
}

func TestStore_Delete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	created, err := s.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if err := s.Delete(ctx, created.Metadata.ID, false); err != nil {
		t.Fatalf("Delete() = %v", err)
	}

	_, err = s.Get(ctx, created.Metadata.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get() after Delete() error = %v, want ErrNotFound", err)
	}
}

func TestStore_Delete_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	err := s.Delete(ctx, "does-not-exist", false)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}

// Acceptance criterion 1 (spec.md): "table-driven tests cover ... audit
// emission on every mutation." These three tests are that coverage.

func TestStore_Create_WritesExactlyOneAuditRecord(t *testing.T) {
	ctx := audit.WithActor(context.Background(), "alice")
	s, db := newTestStoreAndDB(t)

	created, err := s.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if got := countAuditRecords(t, db, created.Metadata.ID); got != 1 {
		t.Fatalf("audit records for %s = %d, want 1", created.Metadata.ID, got)
	}
	rec := lastAuditRecord(t, db, created.Metadata.ID)
	if rec.Actor != "alice" {
		t.Errorf("Actor = %q, want %q", rec.Actor, "alice")
	}
	if rec.Interface != "core" {
		t.Errorf("Interface = %q, want %q", rec.Interface, "core")
	}
	if rec.Before != nil {
		t.Errorf("Before = %q, want nil (nothing existed before Create)", rec.Before)
	}
	if rec.After == nil {
		t.Fatal("After = nil, want the created resource's JSON snapshot")
	}
	if !bytesContain(rec.After, `"name":"acme"`) {
		t.Errorf("After = %s, want it to contain the created resource's Name", rec.After)
	}
}

func TestStore_Update_WritesExactlyOneAuditRecordWithBeforeAndAfter(t *testing.T) {
	ctx := audit.WithActor(context.Background(), "bob")
	s, db := newTestStoreAndDB(t)

	created, err := s.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if got := countAuditRecords(t, db, created.Metadata.ID); got != 1 {
		t.Fatalf("audit records after Create = %d, want 1", got)
	}

	created.Metadata.Name = "acme-renamed"
	if _, err := s.Update(ctx, created); err != nil {
		t.Fatalf("Update() = %v", err)
	}

	if got := countAuditRecords(t, db, created.Metadata.ID); got != 2 {
		t.Fatalf("audit records after Create+Update = %d, want 2", got)
	}
	rec := lastAuditRecord(t, db, created.Metadata.ID)
	if rec.Actor != "bob" {
		t.Errorf("Actor = %q, want %q", rec.Actor, "bob")
	}
	if !bytesContain(rec.Before, `"name":"acme"`) || bytesContain(rec.Before, `"name":"acme-renamed"`) {
		t.Errorf("Before = %s, want the pre-update Name (acme, not acme-renamed)", rec.Before)
	}
	if !bytesContain(rec.After, `"name":"acme-renamed"`) {
		t.Errorf("After = %s, want the post-update Name (acme-renamed)", rec.After)
	}
}

func TestStore_Delete_WritesExactlyOneAuditRecordWithBeforeOnly(t *testing.T) {
	ctx := audit.WithActor(context.Background(), "carol")
	s, db := newTestStoreAndDB(t)

	created, err := s.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if err := s.Delete(ctx, created.Metadata.ID, false); err != nil {
		t.Fatalf("Delete() = %v", err)
	}

	if got := countAuditRecords(t, db, created.Metadata.ID); got != 2 {
		t.Fatalf("audit records after Create+Delete = %d, want 2", got)
	}
	rec := lastAuditRecord(t, db, created.Metadata.ID)
	if rec.Actor != "carol" {
		t.Errorf("Actor = %q, want %q", rec.Actor, "carol")
	}
	if rec.Before == nil {
		t.Error("Before = nil, want the deleted resource's JSON snapshot")
	}
	if rec.After != nil {
		t.Errorf("After = %q, want nil (nothing exists after Delete)", rec.After)
	}
}

func TestActorFrom_DefaultsToUnknownInAuditRecord(t *testing.T) {
	ctx := context.Background() // no WithActor call
	s, db := newTestStoreAndDB(t)

	created, err := s.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	rec := lastAuditRecord(t, db, created.Metadata.ID)
	if rec.Actor != "unknown" {
		t.Errorf("Actor = %q, want %q (no actor set on context)", rec.Actor, "unknown")
	}
}

func bytesContain(b []byte, substr string) bool {
	return len(b) > 0 && strings.Contains(string(b), substr)
}
