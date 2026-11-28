package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// txStores is one batch's kinds on a shared database and bus, the shape
// a template create writes (specs/027 plan decision 8).
type txStores struct {
	db    *sql.DB
	atom  *sqlite.DB
	bus   *recordingBus
	apps  *sqlite.Store[resource.ApplicationSpec, resource.ApplicationStatus]
	comps *sqlite.Store[resource.ComponentSpec, resource.ComponentStatus]
	deps  *sqlite.Store[resource.DeploymentSpec, resource.DeploymentStatus]
	vers  *sqlite.Store[resource.TemplateVersionSpec, resource.TemplateVersionStatus]
}

func newTxStores(tb testing.TB) txStores {
	tb.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, tb.TempDir()+"/tx.db")
	if err != nil {
		tb.Fatalf("Open() = %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		tb.Fatalf("Migrate() = %v", err)
	}
	bus := &recordingBus{}
	return txStores{
		db:    db,
		atom:  sqlite.NewDB(db),
		bus:   bus,
		apps:  sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, resource.KindApplication).SetBus(bus),
		comps: sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](db, resource.KindComponent).SetBus(bus),
		deps:  sqlite.NewStore[resource.DeploymentSpec, resource.DeploymentStatus](db, resource.KindDeployment).SetBus(bus),
		vers: sqlite.NewStore[resource.TemplateVersionSpec, resource.TemplateVersionStatus](db, resource.KindTemplateVersion).
			SetBus(bus).SetUniqueKey(resource.TemplateVersionKey),
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// assertEmpty checks that nothing a rolled-back batch wrote is left.
func assertEmpty(t *testing.T, s txStores) {
	t.Helper()
	for _, table := range []string{"resources", "edges", "audit_log"} {
		if n := countRows(t, s.db, table); n != 0 {
			t.Errorf("%s has %d rows after rollback, want 0", table, n)
		}
	}
	if ev := s.bus.snapshot(); len(ev) != 0 {
		t.Errorf("published %d events after rollback, want 0: %v", len(ev), ev)
	}
}

func app(name string) resource.Application {
	return resource.Application{Metadata: resource.Metadata{Name: name}}
}

func comp(name, appID string) resource.Component {
	return resource.Component{
		Metadata: resource.Metadata{Name: name,
			OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: appID}}},
		Spec: resource.ComponentSpec{Image: "nginx:1.27"},
	}
}

// A failing third create leaves no row, edge, audit record or event.
func TestAtomically_FailingCreateRollsBackAll(t *testing.T) {
	ctx := context.Background()
	s := newTxStores(t)
	err := s.atom.Atomically(ctx, func(tx store.Tx) error {
		a, err := s.apps.CreateIn(ctx, tx, app("web"))
		if err != nil {
			return err
		}
		if _, err := s.comps.CreateIn(ctx, tx, comp("api", a.Metadata.ID)); err != nil {
			return err
		}
		_, err = s.comps.CreateIn(ctx, tx, comp("", a.Metadata.ID)) // no name: invalid
		return err
	})
	if !errors.Is(err, resource.ErrValidation) {
		t.Fatalf("Atomically() = %v, want ErrValidation", err)
	}
	assertEmpty(t, s)
}

// A duplicate unique key inside the batch rolls back all of it.
func TestAtomically_DuplicateKeyRollsBackAll(t *testing.T) {
	ctx := context.Background()
	s := newTxStores(t)
	ver := func() resource.TemplateVersion {
		return resource.TemplateVersion{Metadata: resource.Metadata{Name: "v1"},
			Spec: resource.TemplateVersionSpec{Template: "tpl", Version: "1.0.0"}}
	}
	err := s.atom.Atomically(ctx, func(tx store.Tx) error {
		if _, err := s.apps.CreateIn(ctx, tx, app("web")); err != nil {
			return err
		}
		if _, err := s.vers.CreateIn(ctx, tx, ver()); err != nil {
			return err
		}
		_, err := s.vers.CreateIn(ctx, tx, ver())
		return err
	})
	if !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("Atomically() = %v, want ErrDuplicate", err)
	}
	assertEmpty(t, s)
}

// A panic in fn rolls back too, and the panic carries on.
func TestAtomically_PanicRollsBack(t *testing.T) {
	ctx := context.Background()
	s := newTxStores(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic was swallowed")
			}
		}()
		_ = s.atom.Atomically(ctx, func(tx store.Tx) error {
			if _, err := s.apps.CreateIn(ctx, tx, app("web")); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	assertEmpty(t, s)
}

// A committed batch publishes one event per resource, after commit, in
// creation order; its rows, edges and audit records are all there.
func TestAtomically_CommitPublishesAfterInOrder(t *testing.T) {
	ctx := context.Background()
	s := newTxStores(t)
	var want []event.Event
	err := s.atom.Atomically(ctx, func(tx store.Tx) error {
		a, err := s.apps.CreateIn(ctx, tx, app("web"))
		if err != nil {
			return err
		}
		want = append(want, event.Event{Type: event.EventCreated, Kind: resource.KindApplication, ID: a.Metadata.ID})
		for _, name := range []string{"api", "worker"} {
			c, err := s.comps.CreateIn(ctx, tx, comp(name, a.Metadata.ID))
			if err != nil {
				return err
			}
			want = append(want, event.Event{Type: event.EventCreated, Kind: resource.KindComponent, ID: c.Metadata.ID})
		}
		if n := len(s.bus.snapshot()); n != 0 {
			t.Errorf("%d events published before commit, want 0", n)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Atomically() = %v", err)
	}
	got := s.bus.snapshot()
	if len(got) != len(want) {
		t.Fatalf("published %d events, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Type != want[i].Type || got[i].Kind != want[i].Kind || got[i].ID != want[i].ID {
			t.Errorf("event %d = %s %s %s, want %s %s %s", i,
				got[i].Type, got[i].Kind, got[i].ID, want[i].Type, want[i].Kind, want[i].ID)
		}
	}
	if n := countRows(t, s.db, "resources"); n != 3 {
		t.Errorf("resources = %d, want 3", n)
	}
	if n := countRows(t, s.db, "edges"); n != 2 {
		t.Errorf("edges = %d, want 2 (owns)", n)
	}
	if n := countRows(t, s.db, "audit_log"); n != 3 {
		t.Errorf("audit_log = %d, want 3", n)
	}
}

// fakeTx is a store.Tx some other implementation opened.
type fakeTx struct{}

func (fakeTx) StoreTx() {}

// CreateIn fails closed on a Tx it didn't open, one on another
// database, or one that has finished.
func TestCreateIn_RefusesForeignOrFinishedTx(t *testing.T) {
	ctx := context.Background()
	s := newTxStores(t)
	other := newTxStores(t)

	if _, err := s.apps.CreateIn(ctx, fakeTx{}, app("web")); !errors.Is(err, sqlite.ErrForeignTx) {
		t.Errorf("CreateIn(fake) = %v, want ErrForeignTx", err)
	}
	var kept store.Tx
	err := other.atom.Atomically(ctx, func(tx store.Tx) error {
		kept = tx
		_, err := s.apps.CreateIn(ctx, tx, app("web"))
		return err
	})
	if !errors.Is(err, sqlite.ErrForeignTx) {
		t.Errorf("CreateIn(other db) = %v, want ErrForeignTx", err)
	}
	if _, err := other.apps.CreateIn(ctx, kept, app("web")); !errors.Is(err, sqlite.ErrForeignTx) {
		t.Errorf("CreateIn(finished) = %v, want ErrForeignTx", err)
	}
	assertEmpty(t, s)
	assertEmpty(t, other)
}

// BenchmarkAtomically_20Components measures RP-108: the write lock held
// by the largest batch a template may create (an Application, 20
// Components and 20 Deployments).
func BenchmarkAtomically_20Components(b *testing.B) {
	ctx := context.Background()
	s := newTxStores(b)
	env := resource.OwnerRef{Kind: resource.KindEnvironment, ID: "env-1"}
	for i := 0; b.Loop(); i++ {
		err := s.atom.Atomically(ctx, func(tx store.Tx) error {
			a, err := s.apps.CreateIn(ctx, tx, app(fmt.Sprintf("web-%d", i)))
			if err != nil {
				return err
			}
			for j := range 20 {
				c, err := s.comps.CreateIn(ctx, tx, comp(fmt.Sprintf("c%d", j), a.Metadata.ID))
				if err != nil {
					return err
				}
				if _, err := s.deps.CreateIn(ctx, tx, resource.Deployment{
					Metadata: resource.Metadata{Name: c.Metadata.Name, OwnerRefs: []resource.OwnerRef{env}},
					Spec:     resource.DeploymentSpec{Component: c.Metadata.ID},
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			b.Fatalf("Atomically() = %v", err)
		}
	}
}
