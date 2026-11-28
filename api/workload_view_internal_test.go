package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/id"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// overlayWorld is a stored Organization > Project with a Secret in the
// Project, and a ComponentHandler whose admission reads the stores.
type overlayWorld struct {
	org, project, secret string
	stored               storedView
	comps                *ComponentHandler
	deploys              *DeploymentHandler
	env                  string
}

func newOverlayWorld(t *testing.T) *overlayWorld {
	t.Helper()
	ctx := t.Context()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "overlay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	org, err := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization).
		Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "org"}})
	if err != nil {
		t.Fatal(err)
	}
	projects := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](db, resource.KindProject)
	p, err := projects.Create(ctx, resource.Project{Metadata: resource.Metadata{Name: "p",
		OwnerRefs: []resource.OwnerRef{{Kind: resource.KindOrganization, ID: org.Metadata.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	envs := sqlite.NewStore[resource.EnvironmentSpec, resource.EnvironmentStatus](db, resource.KindEnvironment)
	env, err := envs.Create(ctx, resource.Environment{Metadata: resource.Metadata{Name: "dev",
		OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: p.Metadata.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	sec, err := sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](db, resource.KindSecret).
		Create(ctx, resource.Secret{Metadata: resource.Metadata{Name: "db",
			OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: p.Metadata.ID}}}, Spec: resource.SecretSpec{Path: "db"}})
	if err != nil {
		t.Fatal(err)
	}
	comps := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](db, resource.KindComponent)
	apps := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, resource.KindApplication)
	deploys := sqlite.NewStore[resource.DeploymentSpec, resource.DeploymentStatus](db, resource.KindDeployment)
	stored := storedView{comps: comps, apps: apps, deploys: deploys,
		existsIn: resourceExistsIn(db), ancestorsOf: graph.NewSQLiteGraph(db).Ancestors}
	return &overlayWorld{
		org: org.Metadata.ID, project: p.Metadata.ID, secret: sec.Metadata.ID, env: env.Metadata.ID,
		stored:  stored,
		comps:   &ComponentHandler{comps: comps, apps: apps, view: stored},
		deploys: &DeploymentHandler{deployments: deploys, envs: envs, comps: comps, apps: apps, projects: projects, view: stored},
	}
}

// allowOnlyAt is a decider granting everything at the one scope given,
// and nothing anywhere else: a role assigned at that Project, say, without
// the inheritance the engine adds.
func allowOnlyAt(ctx context.Context, at resource.Scope, asked *[]resource.Scope) context.Context {
	return context.WithValue(ctx, deciderKey{}, decider(func(_ context.Context, _ policy.Action, s resource.Scope) (bool, error) {
		*asked = append(*asked, s)
		return s == at, nil
	}))
}

// pendingShop is a pending Application "shop" in the Project, with a
// pending Component "api" (port http) in it.
func (w *overlayWorld) pendingShop() (*pendingView, resource.Application, resource.Component) {
	v := newPendingView(w.stored)
	app := resource.Application{Metadata: resource.Metadata{ID: id.New(), Name: "shop",
		OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: w.project}}}}
	api := resource.Component{Metadata: resource.Metadata{ID: id.New(), Name: "api",
		OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: app.Metadata.ID}}},
		Spec: resource.ComponentSpec{Image: "api:1", Ports: []resource.ComponentPort{{Name: "http", Port: 8080}}}}
	v.addApplication(app)
	v.addComponent(api)
	return v, app, api
}

// A Component may depend on a Component of the same batch, decided at
// the Project the pending ones will inherit from; the stores alone don't
// know it.
func TestOverlayDependencyOnPendingComponent(t *testing.T) {
	w := newOverlayWorld(t)
	v, app, api := w.pendingShop()
	web := resource.Component{Metadata: resource.Metadata{ID: id.New(), Name: "web",
		OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: app.Metadata.ID}}},
		Spec: resource.ComponentSpec{Image: "web:1",
			Dependencies: []resource.ComponentDependency{{Component: api.Metadata.ID, Port: "http", Env: "API_URL"}}}}
	projectScope := resource.Scope{Kind: resource.KindProject, ID: w.project}

	var asked []resource.Scope
	ctx := allowOnlyAt(t.Context(), projectScope, &asked)
	if _, err := w.comps.admit(ctx, v, web, nil); err != nil {
		t.Fatalf("a dependency on a pending Component: %v", err)
	}
	if !slices.Equal(asked, []resource.Scope{projectScope}) {
		t.Errorf("decided at %v, want only the Project", asked)
	}

	// The stores alone: the read is asked at the Component itself.
	asked = nil
	_, err := w.comps.admit(ctx, w.stored, web, nil)
	var rf *deployRefusal
	if !errors.As(err, &rf) || rf.status != http.StatusForbidden {
		t.Errorf("through the stores: %v, want 403", err)
	}

	// A missing port on the pending target is still refused.
	web.Spec.Dependencies[0].Port = "grpc"
	if _, err := w.comps.admit(ctx, v, web, nil); !errors.As(err, &rf) || rf.status != http.StatusBadRequest || !strings.Contains(rf.detail, "no port grpc") {
		t.Errorf("a missing port: %v", err)
	}

	// Without a role at the Project, the pending target can't be read.
	web.Spec.Dependencies[0].Port = "http"
	ctx = allowOnlyAt(t.Context(), resource.Scope{Kind: resource.KindProject, ID: "elsewhere"}, &asked)
	if _, err := w.comps.admit(ctx, v, web, nil); !errors.As(err, &rf) || rf.status != http.StatusForbidden {
		t.Errorf("no role at the Project: %v, want 403", err)
	}
}

// A pending Application's ancestry comes from the batch's owner refs, then
// the graph: so a pending Component's Secret in the Project is reachable,
// and one in another Project isn't.
func TestOverlayAncestryOfPendingApplication(t *testing.T) {
	w := newOverlayWorld(t)
	v, app, api := w.pendingShop()

	got, err := v.ancestors(t.Context(), graph.ResourceRef{Kind: resource.KindComponent, ID: api.Metadata.ID})
	if err != nil {
		t.Fatal(err)
	}
	want := []graph.ResourceRef{
		{Kind: resource.KindApplication, ID: app.Metadata.ID},
		{Kind: resource.KindProject, ID: w.project},
		{Kind: resource.KindOrganization, ID: w.org},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ancestors = %v, want %v", got, want)
	}
	if s := v.scope(resource.Scope{Kind: resource.KindComponent, ID: api.Metadata.ID}); s != (resource.Scope{Kind: resource.KindProject, ID: w.project}) {
		t.Errorf("scope = %v, want the Project", s)
	}
	for _, kind := range []resource.Kind{resource.KindApplication, resource.KindComponent} {
		ref := app.Metadata.ID
		if kind == resource.KindComponent {
			ref = api.Metadata.ID
		}
		if ok, err := v.exists(t.Context(), kind, ref); err != nil || !ok {
			t.Errorf("pending %s exists = %v, %v", kind, ok, err)
		}
		if ok, err := w.stored.exists(t.Context(), kind, ref); err != nil || ok {
			t.Errorf("pending %s in the stores = %v, %v", kind, ok, err)
		}
	}

	// The secret variable's Project is the pending Application's.
	api.Spec.SecretEnv = []resource.ComponentSecretEnv{{Env: "DB", Secret: w.secret, Key: "password"}}
	var asked []resource.Scope
	ctx := allowOnlyAt(t.Context(), resource.Scope{Kind: resource.KindSecret, ID: w.secret}, &asked)
	if _, err := w.comps.checkSecretEnv(ctx, v, api, nil); err != nil {
		t.Errorf("a Secret in the pending Application's Project: %v", err)
	}
	// Through the stores, the Application isn't there.
	var rf *deployRefusal
	if _, err := w.comps.checkSecretEnv(ctx, w.stored, api, nil); !errors.As(err, &rf) || !strings.Contains(rf.detail, "not found") {
		t.Errorf("through the stores: %v, want the Application not found", err)
	}
	// A pending Application in no Project is refused.
	v.addApplication(resource.Application{Metadata: resource.Metadata{ID: app.Metadata.ID, Name: "shop"}})
	if _, err := w.comps.checkSecretEnv(ctx, v, api, nil); !errors.As(err, &rf) || !strings.Contains(rf.detail, "no Project") {
		t.Errorf("an Application in no Project: %v", err)
	}
}

// A pending Deployment counts against the one-per-Environment rule, and
// a Deployment of a pending Component is admitted at the Project.
func TestOverlayPendingDeployments(t *testing.T) {
	w := newOverlayWorld(t)
	v, _, api := w.pendingShop()
	d := resource.Deployment{Metadata: resource.Metadata{ID: id.New(), Name: "api-dev",
		OwnerRefs: []resource.OwnerRef{{Kind: resource.KindEnvironment, ID: w.env}}},
		Spec: resource.DeploymentSpec{Component: api.Metadata.ID}}
	var asked []resource.Scope
	ctx := allowOnlyAt(t.Context(), resource.Scope{Kind: resource.KindProject, ID: w.project}, &asked)
	if _, err := w.deploys.admit(ctx, v, d, w.env, nil); err != nil {
		t.Fatalf("a Deployment of a pending Component: %v", err)
	}
	v.addDeployment(d)
	twice := d
	twice.Metadata.ID = id.New()
	var rf *deployRefusal
	if _, err := w.deploys.admit(ctx, v, twice, w.env, nil); !errors.As(err, &rf) || rf.status != http.StatusConflict {
		t.Errorf("a second pending Deployment in the Environment: %v, want 409", err)
	}
}
