package sqlite_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func instance(capability, env string) resource.ProviderInstance {
	return resource.ProviderInstance{Metadata: resource.Metadata{Name: "pkg"},
		Spec: resource.ProviderInstanceSpec{CapabilityID: capability, ProviderID: "p", EnvironmentID: env}}
}

// Creates racing on one key leave exactly one row (RP-033): the index
// decides, whatever any caller checked first.
func TestUniqueKey_ConcurrentCreates(t *testing.T) {
	ctx := context.Background()
	_, db := newTestStoreAndDB(t)
	s := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](db, resource.KindProviderInstance).
		SetUniqueKey(resource.ProviderInstanceKey)

	const n = 20
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.Create(ctx, instance("build", ""))
		}()
	}
	wg.Wait()
	created := 0
	for _, err := range errs {
		switch {
		case err == nil:
			created++
		case !errors.Is(err, store.ErrDuplicate):
			t.Errorf("Create() = %v, want nil or ErrDuplicate", err)
		}
	}
	all, err := s.List(ctx, store.ListOptions{})
	if created != 1 || err != nil || len(all) != 1 {
		t.Fatalf("created %d, listed %d (%v); want exactly one", created, len(all), err)
	}

	// Other keys, and an empty one, don't collide; Update keeps the key.
	if _, err := s.Create(ctx, instance("build", "env-1")); err != nil {
		t.Errorf("same capability, another Environment: %v", err)
	}
	for range 2 {
		if _, err := s.Create(ctx, instance("", "")); err != nil {
			t.Errorf("no key: %v", err)
		}
	}
	all[0].Spec.Config = map[string]string{"x": "y"}
	if _, err := s.Update(ctx, all[0]); err != nil {
		t.Errorf("Update() = %v", err)
	}
	if _, err := s.Create(ctx, instance("build", "")); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("after the Update, a duplicate: %v, want ErrDuplicate", err)
	}
	// A failed create writes no audit row.
	var audits int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE kind = 'ProviderInstance' AND before IS NULL`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 4 {
		t.Errorf("create audit rows = %d, want 4 (one per committed create)", audits)
	}
	// Kinds without a key are unaffected.
	if _, err := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](db, resource.KindProviderInstance).
		Create(ctx, instance("build", "")); err != nil {
		t.Errorf("a store without a key function: %v", err)
	}
}

// A store from before 0009 holding duplicates: only the oldest of each
// group is keyed, and each other one gets a note.
func TestMigration0009_KeysOnlyTheOldest(t *testing.T) {
	ctx := context.Background()
	_, db := newTestStoreAndDB(t)
	plain := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](db, resource.KindProviderInstance)
	var ids []string
	for _, i := range []resource.ProviderInstance{instance("build", ""), instance("build", ""), instance("observability", ""), instance("", "")} {
		r, err := plain.Create(ctx, i)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.Metadata.ID)
	}
	// Back to the schema 0009 meets.
	for _, q := range []string{
		`DROP INDEX idx_resources_unique_key`,
		`ALTER TABLE resources DROP COLUMN unique_key`,
		`DELETE FROM schema_migrations WHERE version = 9`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() = %v", err)
	}

	keys := map[string]string{}
	rows, err := db.Query(`SELECT id, COALESCE(unique_key, '') FROM resources WHERE kind = 'ProviderInstance'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			t.Fatal(err)
		}
		keys[id] = key
	}
	rows.Close()
	want := map[string]string{ids[0]: "build/", ids[1]: "", ids[2]: "observability/", ids[3]: ""}
	for id, k := range want {
		if keys[id] != k {
			t.Errorf("unique_key of %s = %q, want %q", id, keys[id], k)
		}
	}

	var notes []string
	nrows, err := db.Query(`SELECT note FROM migration_notes WHERE migration = 9`)
	if err != nil {
		t.Fatal(err)
	}
	for nrows.Next() {
		var n string
		if err := nrows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		notes = append(notes, n)
	}
	nrows.Close()
	if len(notes) != 1 || !strings.Contains(notes[0], ids[1]) || !strings.Contains(notes[0], ids[0]) {
		t.Errorf("notes = %q, want one naming %s as a duplicate of %s", notes, ids[1], ids[0])
	}

	// The key now holds: another build package is refused.
	keyed := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](db, resource.KindProviderInstance).
		SetUniqueKey(resource.ProviderInstanceKey)
	if _, err := keyed.Create(ctx, instance("build", "")); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("Create() after the migration = %v, want ErrDuplicate", err)
	}
}

// Update keeps a keyed row's key in step: changing onto a taken key is
// refused, the old key is freed, a store without a key function leaves
// keys alone, and an unkeyed duplicate's own writes still work.
func TestUniqueKey_Update(t *testing.T) {
	ctx := context.Background()
	_, db := newTestStoreAndDB(t)
	keyed := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](db, resource.KindProviderInstance).
		SetUniqueKey(resource.ProviderInstanceKey)
	plain := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](db, resource.KindProviderInstance)

	a, err := keyed.Create(ctx, instance("build", ""))
	if err != nil {
		t.Fatal(err)
	}
	b, err := keyed.Create(ctx, instance("observability", ""))
	if err != nil {
		t.Fatal(err)
	}
	b.Spec.CapabilityID = "build"
	if _, err := keyed.Update(ctx, b); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("updating onto a taken key = %v, want ErrDuplicate", err)
	}
	// A status write through a store without a key keeps a's key.
	a.Status.Applied = "x"
	if a, err = plain.Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := keyed.Create(ctx, instance("build", "")); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("after a keyless write, a duplicate = %v, want ErrDuplicate", err)
	}
	// Moving a to another capability frees "build/".
	a.Spec.CapabilityID = "storage"
	if _, err := keyed.Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := keyed.Create(ctx, instance("build", "")); err != nil {
		t.Errorf("the freed key: %v", err)
	}
	// An unkeyed duplicate (as migration 0009 leaves one) can still be
	// written, and stays unkeyed.
	dup, err := plain.Create(ctx, instance("build", ""))
	if err != nil {
		t.Fatal(err)
	}
	dup.Status.Applied = "y"
	if _, err := keyed.Update(ctx, dup); err != nil {
		t.Errorf("writing an unkeyed duplicate: %v", err)
	}
}
