package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func newTestStore(t *testing.T) *sqlite.Store[resource.OrganizationSpec, resource.OrganizationStatus] {
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
	return sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)
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
