package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// oldTree is a store as spec 019/021 left it: Applications under an
// Environment. It's written through the stores, which don't constrain
// owner kinds, on a fully migrated database; the test then re-runs 0008 as
// such a database would meet it.
type oldTree struct {
	db                                  *sql.DB
	org, proj, env, app, web, api       string
	orphanEnv, orphanApp                string // an Environment with no Project
	projApp                             string // already under the Project
	envAssignment, projAssignment, root string
}

func newOldTree(t *testing.T) *oldTree {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "m8.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	mk := func(kind resource.Kind, name string, owner *resource.OwnerRef) string {
		t.Helper()
		meta := resource.Metadata{Name: name}
		if owner != nil {
			meta.OwnerRefs = []resource.OwnerRef{*owner}
		}
		var id string
		var err error
		switch kind {
		case resource.KindOrganization:
			r, e := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, kind).Create(ctx, resource.Organization{Metadata: meta})
			id, err = r.Metadata.ID, e
		case resource.KindProject:
			r, e := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](db, kind).Create(ctx, resource.Project{Metadata: meta})
			id, err = r.Metadata.ID, e
		case resource.KindEnvironment:
			r, e := sqlite.NewStore[resource.EnvironmentSpec, resource.EnvironmentStatus](db, kind).Create(ctx, resource.Environment{Metadata: meta})
			id, err = r.Metadata.ID, e
		case resource.KindApplication:
			r, e := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, kind).Create(ctx, resource.Application{Metadata: meta})
			id, err = r.Metadata.ID, e
		case resource.KindComponent:
			r, e := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](db, kind).Create(ctx,
				resource.Component{Metadata: meta, Spec: resource.ComponentSpec{Image: "nginx"}})
			id, err = r.Metadata.ID, e
		}
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return id
	}
	o := &oldTree{db: db}
	o.org = mk(resource.KindOrganization, "org", nil)
	o.proj = mk(resource.KindProject, "proj", &resource.OwnerRef{Kind: resource.KindOrganization, ID: o.org})
	o.env = mk(resource.KindEnvironment, "dev", &resource.OwnerRef{Kind: resource.KindProject, ID: o.proj})
	o.app = mk(resource.KindApplication, "shop", &resource.OwnerRef{Kind: resource.KindEnvironment, ID: o.env})
	o.web = mk(resource.KindComponent, "web", &resource.OwnerRef{Kind: resource.KindApplication, ID: o.app})
	o.api = mk(resource.KindComponent, "api", &resource.OwnerRef{Kind: resource.KindApplication, ID: o.app})
	o.orphanEnv = mk(resource.KindEnvironment, "loose", &resource.OwnerRef{Kind: resource.KindOrganization, ID: o.org})
	o.orphanApp = mk(resource.KindApplication, "stray", &resource.OwnerRef{Kind: resource.KindEnvironment, ID: o.orphanEnv})
	o.projApp = mk(resource.KindApplication, "already", &resource.OwnerRef{Kind: resource.KindProject, ID: o.proj})

	assign := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment)
	grant := func(who string, at resource.Scope) string {
		t.Helper()
		a, err := assign.Create(ctx, resource.RoleAssignment{Metadata: resource.Metadata{Name: "ra"}, Spec: resource.RoleAssignmentSpec{
			Principal: resource.PrincipalRef{Type: resource.PrincipalPerson, ID: who}, RoleDefinitionID: "Contributor", Scope: at}})
		if err != nil {
			t.Fatal(err)
		}
		return a.Metadata.ID
	}
	o.envAssignment = grant("sub-dev-team", resource.Scope{Kind: resource.KindEnvironment, ID: o.env})
	o.projAssignment = grant("sub-lead", resource.Scope{Kind: resource.KindProject, ID: o.proj})
	o.root = grant("sub-owner", resource.RootScope)
	return o
}

// rerun0008 applies 0008 again, as a database from before spec 020 meets it.
func (o *oldTree) rerun0008(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := o.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = 8`); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(ctx, o.db); err != nil {
		t.Fatalf("Migrate() = %v", err)
	}
}

func (o *oldTree) notes(t *testing.T) []string {
	t.Helper()
	rows, err := o.db.Query(`SELECT note FROM migration_notes WHERE migration = 8 ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// covered maps each role assignment to the resources its scope reaches:
// the scope itself and everything under it.
func covered(t *testing.T, db *sql.DB) map[string][]graph.ResourceRef {
	t.Helper()
	ctx := context.Background()
	g := graph.NewSQLiteGraph(db)
	rows, err := db.Query(`SELECT id, json_extract(spec, '$.scope.kind'), json_extract(spec, '$.scope.id') FROM resources WHERE kind = 'RoleAssignment'`)
	if err != nil {
		t.Fatal(err)
	}
	type a struct{ id, kind, scope string }
	var as []a
	for rows.Next() {
		var x a
		var kind, scope sql.NullString
		if err := rows.Scan(&x.id, &kind, &scope); err != nil {
			t.Fatal(err)
		}
		x.kind, x.scope = kind.String, scope.String
		as = append(as, x)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	out := map[string][]graph.ResourceRef{}
	for _, x := range as {
		if x.kind == "" {
			out[x.id] = []graph.ResourceRef{{Kind: "/", ID: "/"}} // root reaches everything
			continue
		}
		at := graph.ResourceRef{Kind: resource.Kind(x.kind), ID: x.scope}
		under, err := g.Descendants(ctx, at)
		if err != nil {
			t.Fatal(err)
		}
		out[x.id] = append([]graph.ResourceRef{at}, under...)
	}
	return out
}

func TestMigration0008_MovesApplicationsAndKeepsThemRunning(t *testing.T) {
	o := newOldTree(t)
	ctx := context.Background()
	before := covered(t, o.db)
	o.rerun0008(t)

	apps := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](o.db, resource.KindApplication)
	shop, err := apps.Get(ctx, o.app)
	if err != nil {
		t.Fatal(err)
	}
	if want := []resource.OwnerRef{{Kind: resource.KindProject, ID: o.proj}}; !slices.Equal(shop.Metadata.OwnerRefs, want) {
		t.Errorf("shop's owners = %v, want its Environment's Project", shop.Metadata.OwnerRefs)
	}
	if stray, _ := apps.Get(ctx, o.orphanApp); stray.Metadata.OwnerRefs[0].Kind != resource.KindEnvironment {
		t.Errorf("an Application under a Project-less Environment moved: %v", stray.Metadata.OwnerRefs)
	}
	if already, _ := apps.Get(ctx, o.projApp); already.Metadata.ResourceVersion != 1 {
		t.Errorf("an Application already under its Project was rewritten (version %d)", already.Metadata.ResourceVersion)
	}

	g := graph.NewSQLiteGraph(o.db)
	envRef := graph.ResourceRef{Kind: resource.KindEnvironment, ID: o.env}
	projRef := graph.ResourceRef{Kind: resource.KindProject, ID: o.proj}
	if owners, _ := g.Owners(ctx, graph.ResourceRef{Kind: resource.KindApplication, ID: o.app}); !slices.Equal(owners, []graph.ResourceRef{projRef}) {
		t.Errorf("graph owners of shop = %v", owners)
	}

	// Each Component runs on as a Deployment in the Environment.
	deps := sqlite.NewStore[resource.DeploymentSpec, resource.DeploymentStatus](o.db, resource.KindDeployment)
	children, _ := g.Children(ctx, envRef)
	if len(children) != 2 {
		t.Fatalf("Environment children = %v, want two Deployments", children)
	}
	for _, comp := range []string{o.web, o.api} {
		d, err := deps.Get(ctx, "dpl-"+comp)
		if err != nil {
			t.Fatalf("Deployment for %s: %v", comp, err)
		}
		if d.Spec.Component != comp || d.Spec.Release != "" || !slices.Equal(d.Metadata.OwnerRefs, []resource.OwnerRef{{Kind: resource.KindEnvironment, ID: o.env}}) {
			t.Errorf("Deployment %s = %+v", d.Metadata.ID, d)
		}
		if err := resource.ValidateDeploymentSpec(d.Spec); err != nil {
			t.Errorf("Deployment %s doesn't validate: %v", d.Metadata.ID, err)
		}
		if err := resource.Validate(d.Metadata); err != nil {
			t.Errorf("Deployment %s metadata doesn't validate: %v", d.Metadata.ID, err)
		}
		dependents, _ := g.Dependents(ctx, graph.ResourceRef{Kind: resource.KindComponent, ID: comp})
		if !slices.Contains(dependents, graph.ResourceRef{Kind: resource.KindDeployment, ID: "dpl-" + comp}) {
			t.Errorf("Component %s's dependents = %v", comp, dependents)
		}
		// Stored times keep the store's fixed width, so they sort as text.
		var created string
		o.db.QueryRow(`SELECT created_at FROM resources WHERE id = ?`, "dpl-"+comp).Scan(&created)
		if len(created) != len("2006-01-02T15:04:05.000000000Z") {
			t.Errorf("created_at %q isn't the store's fixed-width time", created)
		}
	}

	// The plan's risk test: every resource an assignment reached before
	// is still reached, or the assignment is in the notes.
	after := covered(t, o.db)
	notes := strings.Join(o.notes(t), "\n")
	for id, was := range before {
		for _, r := range was {
			if r.Kind == "/" || slices.Contains(after[id], r) {
				continue
			}
			if !strings.Contains(notes, "role assignment "+id) {
				t.Errorf("assignment %s lost %v, and the notes don't say so", id, r)
			}
		}
	}
	if !strings.Contains(notes, "role assignment "+o.envAssignment) {
		t.Errorf("the Environment-scope assignment isn't listed:\n%s", notes)
	}
	if strings.Contains(notes, o.projAssignment) || strings.Contains(notes, o.root) {
		t.Errorf("unaffected assignments are listed:\n%s", notes)
	}
	if !strings.Contains(notes, "Application "+o.orphanApp+" stays under Environment "+o.orphanEnv) {
		t.Errorf("the unmoved Application isn't listed:\n%s", notes)
	}
	// The Project-scope assignment now reaches the moved Application
	// directly, and still reaches its Components.
	for _, r := range []graph.ResourceRef{{Kind: resource.KindApplication, ID: o.app}, {Kind: resource.KindComponent, ID: o.web}} {
		if !slices.Contains(after[o.projAssignment], r) {
			t.Errorf("the Project-scope assignment no longer reaches %v", r)
		}
	}
}

func TestMigration0008_IsIdempotent(t *testing.T) {
	o := newOldTree(t)
	o.rerun0008(t)
	snapshot := func() string {
		rows, err := o.db.Query(`SELECT id, owner_refs, resource_version FROM resources ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var b strings.Builder
		for rows.Next() {
			var id, owners string
			var v int
			if err := rows.Scan(&id, &owners, &v); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s %s %d;", id, owners, v)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		var edges int
		o.db.QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&edges)
		fmt.Fprintf(&b, "edges %d", edges)
		return b.String()
	}
	first := snapshot()
	o.rerun0008(t)
	if second := snapshot(); second != first {
		t.Error("re-running 0008 changed the store again")
	}
	var deployments int
	o.db.QueryRow(`SELECT COUNT(*) FROM resources WHERE kind = 'Deployment'`).Scan(&deployments)
	if deployments != 2 {
		t.Errorf("%d Deployments after two runs, want 2", deployments)
	}
}

func TestMigration0008_EmptyStore(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var notes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM migration_notes`).Scan(&notes); err != nil || notes != 0 {
		t.Errorf("notes on an empty store: %d, %v", notes, err)
	}
}
