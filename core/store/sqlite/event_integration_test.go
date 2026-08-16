package sqlite_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// recordingBus collects every published event, safe for concurrent use
// (Publish on event.InProcessBus already runs handlers synchronously on
// the caller's goroutine, but a test double should still be safe if that
// ever changes).
type recordingBus struct {
	mu     sync.Mutex
	events []event.Event
}

func (b *recordingBus) Subscribe(resource.Kind, func(event.Event)) func() { return func() {} }

func (b *recordingBus) Publish(e event.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}

func (b *recordingBus) snapshot() []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]event.Event, len(b.events))
	copy(out, b.events)
	return out
}

var _ event.Bus = (*recordingBus)(nil)

func TestStore_PublishesEventPerMutation(t *testing.T) {
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

	bus := &recordingBus{}
	s := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization).SetBus(bus)

	created, err := s.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	created.Metadata.Name = "acme-renamed"
	if _, err := s.Update(ctx, created); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if err := s.Delete(ctx, created.Metadata.ID, false); err != nil {
		t.Fatalf("Delete() = %v", err)
	}

	got := bus.snapshot()
	if len(got) != 3 {
		t.Fatalf("published events = %d, want 3 (create, update, delete)", len(got))
	}
	wantTypes := []event.EventType{event.EventCreated, event.EventUpdated, event.EventDeleted}
	for i, want := range wantTypes {
		if got[i].Type != want {
			t.Errorf("event[%d].Type = %q, want %q", i, got[i].Type, want)
		}
		if got[i].Kind != resource.KindOrganization {
			t.Errorf("event[%d].Kind = %q, want %q", i, got[i].Kind, resource.KindOrganization)
		}
		if got[i].ID != created.Metadata.ID {
			t.Errorf("event[%d].ID = %q, want %q", i, got[i].ID, created.Metadata.ID)
		}
	}
}

func TestStore_NoBusAttached_NoPanic(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t) // no SetBus call

	created, err := s.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if err := s.Delete(ctx, created.Metadata.ID, false); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
}

func TestStore_CascadeDelete_PublishesEventPerCascadedResource(t *testing.T) {
	ctx := context.Background()
	db := newCascadeTestDB(t)
	bus := &recordingBus{}

	orgs := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization).SetBus(bus)
	projects := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](db, resource.KindProject).SetBus(bus)
	apps := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, resource.KindApplication).SetBus(bus)

	org, err := orgs.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("Create(org) = %v", err)
	}
	project, err := projects.Create(ctx, resource.Project{
		Metadata: resource.Metadata{Name: "web", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindOrganization, ID: org.Metadata.ID}}},
	})
	if err != nil {
		t.Fatalf("Create(project) = %v", err)
	}
	app, err := apps.Create(ctx, resource.Application{
		Metadata: resource.Metadata{Name: "frontend", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: project.Metadata.ID}}},
	})
	if err != nil {
		t.Fatalf("Create(app) = %v", err)
	}

	if err := orgs.Delete(ctx, org.Metadata.ID, true); err != nil {
		t.Fatalf("Delete(org, cascade=true) = %v", err)
	}

	got := bus.snapshot()
	// 3 creates + 3 cascade-triggered deletes (org, project, app).
	if len(got) != 6 {
		t.Fatalf("published events = %d, want 6", len(got))
	}

	deletedIDs := map[string]resource.Kind{}
	for _, e := range got[3:] {
		if e.Type != event.EventDeleted {
			t.Errorf("event %+v after the 3 creates, want Type=deleted", e)
		}
		deletedIDs[e.ID] = e.Kind
	}
	want := map[string]resource.Kind{
		org.Metadata.ID:     resource.KindOrganization,
		project.Metadata.ID: resource.KindProject,
		app.Metadata.ID:     resource.KindApplication,
	}
	for id, kind := range want {
		if deletedIDs[id] != kind {
			t.Errorf("no delete event for %s %s (got kind %q)", kind, id, deletedIDs[id])
		}
	}
}
