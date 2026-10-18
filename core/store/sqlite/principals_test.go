package sqlite_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func newPrincipalRegistry(t *testing.T) *sqlite.PrincipalRegistry {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "principals.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	return sqlite.NewPrincipalRegistry(db)
}

func TestPrincipalRegistry(t *testing.T) {
	ctx := context.Background()
	r := newPrincipalRegistry(t)
	alice := sqlite.KnownPrincipal{Type: resource.PrincipalPerson, ID: "sub-alice", DisplayName: "alice", Groups: []string{"platform", "eng"}}
	if err := r.Seen(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if err := r.Seen(ctx, sqlite.KnownPrincipal{Type: resource.PrincipalAPIKey, ID: "ci"}); err != nil {
		t.Fatal(err)
	}

	people, err := r.List(ctx, resource.PrincipalPerson)
	if err != nil || len(people) != 1 {
		t.Fatalf("people = %+v, %v", people, err)
	}
	if p := people[0]; p.ID != "sub-alice" || p.DisplayName != "alice" || !slices.Equal(p.Groups, []string{"eng", "platform"}) || p.FirstSeen.IsZero() {
		t.Errorf("person = %+v", p)
	}
	groups, err := r.List(ctx, resource.PrincipalGroup)
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups = %+v, %v; want each of alice's groups recorded", groups, err)
	}
	all, err := r.List(ctx, "")
	if err != nil || len(all) != 4 {
		t.Fatalf("all = %d, %v; want 4", len(all), err)
	}

	// A changed group list is written even within the minute.
	alice.Groups = []string{"eng"}
	if err := r.Seen(ctx, alice); err != nil {
		t.Fatal(err)
	}
	people, _ = r.List(ctx, resource.PrincipalPerson)
	if !slices.Equal(people[0].Groups, []string{"eng"}) {
		t.Errorf("groups after change = %v", people[0].Groups)
	}

	if err := r.Forget(ctx, resource.PrincipalAPIKey, "ci"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := r.List(ctx, resource.PrincipalAPIKey); len(keys) != 0 {
		t.Errorf("forgotten key still listed: %+v", keys)
	}
}
