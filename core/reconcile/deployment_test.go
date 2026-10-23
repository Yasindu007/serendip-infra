package reconcile_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// fakeDeployments records calls and answers Deploy with obs or err.
type fakeDeployments struct {
	mu         sync.Mutex
	obs        reconcile.DeploymentObservation
	err        error
	deployed   []string // Deployment IDs, in call order
	undeployed []resource.Deployment
	pipelines  []resource.Project
	envsGone   []string
	pipesGone  []string
}

func (f *fakeDeployments) EnsureEnvironment(context.Context, resource.Environment) error { return nil }
func (f *fakeDeployments) DeleteEnvironment(_ context.Context, env resource.Environment) error {
	f.envsGone = append(f.envsGone, env.Metadata.ID)
	return nil
}
func (f *fakeDeployments) EnsurePipeline(_ context.Context, p resource.Project) error {
	f.pipelines = append(f.pipelines, p)
	return f.err
}
func (f *fakeDeployments) DeletePipeline(_ context.Context, id string) error {
	f.pipesGone = append(f.pipesGone, id)
	return nil
}
func (f *fakeDeployments) Releases(context.Context, resource.Component) ([]reconcile.Release, error) {
	return nil, nil
}
func (f *fakeDeployments) CreateRelease(context.Context, resource.Component) (reconcile.Release, error) {
	return reconcile.Release{}, nil
}
func (f *fakeDeployments) Deploy(_ context.Context, _ resource.Application, _ resource.Component, _ resource.Environment, d resource.Deployment) (reconcile.DeploymentObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deployed = append(f.deployed, d.Metadata.ID)
	return f.obs, f.err
}
func (f *fakeDeployments) Undeploy(_ context.Context, d resource.Deployment) error {
	f.undeployed = append(f.undeployed, d)
	return nil
}

type deployStores struct {
	appStores
	deployments store.Store[resource.DeploymentSpec, resource.DeploymentStatus]
	envs        store.Store[resource.EnvironmentSpec, resource.EnvironmentStatus]
	projects    store.Store[resource.ProjectSpec, resource.ProjectStatus]
	proj, env   string
	comp        resource.Component
}

// newDeployStores builds a Project with an Environment, and an Application
// in the Project with a released Component.
func newDeployStores(t *testing.T) *deployStores {
	t.Helper()
	ctx := context.Background()
	s := &deployStores{appStores: newAppStores(t)}
	s.deployments = sqlite.NewStore[resource.DeploymentSpec, resource.DeploymentStatus](s.db, resource.KindDeployment)
	s.envs = sqlite.NewStore[resource.EnvironmentSpec, resource.EnvironmentStatus](s.db, resource.KindEnvironment)
	s.projects = sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](s.db, resource.KindProject)
	p, err := s.projects.Create(ctx, resource.Project{Metadata: resource.Metadata{Name: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	s.proj = p.Metadata.ID
	env, err := s.envs.Create(ctx, resource.Environment{Metadata: resource.Metadata{Name: "dev", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: s.proj}}}})
	if err != nil {
		t.Fatal(err)
	}
	s.env = env.Metadata.ID
	app, err := s.apps.Create(ctx, resource.Application{Metadata: resource.Metadata{Name: "shop", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: s.proj}}}})
	if err != nil {
		t.Fatal(err)
	}
	s.comp, err = s.components.Create(ctx, resource.Component{Metadata: resource.Metadata{Name: "web", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: app.Metadata.ID}}},
		Spec: resource.ComponentSpec{Image: "nginx", Ports: []resource.ComponentPort{{Name: "http", Port: 80}}}})
	if err != nil {
		t.Fatal(err)
	}
	s.released(t, true)
	return s
}

func (s *deployStores) released(t *testing.T, ok bool) {
	t.Helper()
	c, err := s.components.Get(context.Background(), s.comp.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	status := resource.ConditionFalse
	if ok {
		status = resource.ConditionTrue
	}
	c.Status.Conditions = []resource.Condition{{Type: "Ready", Status: status, Reason: "Released"}}
	if s.comp, err = s.components.Update(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

func (s *deployStores) deployment(t *testing.T, env string) resource.Deployment {
	t.Helper()
	d, err := s.deployments.Create(context.Background(), resource.Deployment{
		Metadata: resource.Metadata{Name: "web", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindEnvironment, ID: env}}},
		Spec:     resource.DeploymentSpec{Component: s.comp.Metadata.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDeploymentReconciler_RunsAndRecordsTheRelease(t *testing.T) {
	s := newDeployStores(t)
	ctx := context.Background()
	fake := &fakeDeployments{obs: reconcile.DeploymentObservation{Release: "r1", Ready: true, Reason: reconcile.ReasonDeployed,
		Endpoints: []resource.ComponentEndpointStatus{{Name: "http", URL: "http://x"}}}}
	r := reconcile.NewDeploymentReconciler(s.deployments, s.envs, s.components, s.apps, fake)
	d := s.deployment(t, s.env)
	ref := graph.ResourceRef{Kind: resource.KindDeployment, ID: d.Metadata.ID}

	checkRequeue(t, r.Reconcile(ctx, ref), false)
	got, _ := s.deployments.Get(ctx, d.Metadata.ID)
	if got.Status.Release != "r1" || len(got.Status.History) != 0 || len(got.Status.Endpoints) != 1 || readyCondition(t, got.Status.Conditions).Status != resource.ConditionTrue {
		t.Errorf("status = %+v", got.Status)
	}
	// Nothing changed: nothing written.
	version := got.Metadata.ResourceVersion
	checkRequeue(t, r.Reconcile(ctx, ref), false)
	if again, _ := s.deployments.Get(ctx, d.Metadata.ID); again.Metadata.ResourceVersion != version {
		t.Error("a no-change reconcile wrote the Deployment")
	}
	// A new release moves the old one into history.
	fake.obs.Release = "r2"
	checkRequeue(t, r.Reconcile(ctx, ref), false)
	got, _ = s.deployments.Get(ctx, d.Metadata.ID)
	if got.Status.Release != "r2" || !slices.Equal(got.Status.History, []string{"r1"}) {
		t.Errorf("after a new release: %+v", got.Status)
	}
	// Not running: no endpoints, the release kept.
	fake.obs = reconcile.DeploymentObservation{Release: "r3", Reason: reconcile.ReasonProgressing}
	checkRequeue(t, r.Reconcile(ctx, ref), true)
	got, _ = s.deployments.Get(ctx, d.Metadata.ID)
	if got.Status.Release != "r2" || len(got.Status.Endpoints) != 0 {
		t.Errorf("while progressing: %+v", got.Status)
	}
}

func TestDeploymentReconciler_RefusesBeforeDeploying(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		arrange    func(t *testing.T, s *deployStores) resource.Deployment
		wantReason string
		requeue    bool
	}{
		{name: "not owned by an Environment", wantReason: reconcile.ReasonInvalidSpec, arrange: func(t *testing.T, s *deployStores) resource.Deployment {
			d, err := s.deployments.Create(ctx, resource.Deployment{Metadata: resource.Metadata{Name: "x"}, Spec: resource.DeploymentSpec{Component: s.comp.Metadata.ID}})
			if err != nil {
				t.Fatal(err)
			}
			return d
		}},
		{name: "Component not released yet", wantReason: reconcile.ReasonWaitingForComponent, requeue: true, arrange: func(t *testing.T, s *deployStores) resource.Deployment {
			s.released(t, false)
			return s.deployment(t, s.env)
		}},
		{name: "Environment in another Project", wantReason: reconcile.ReasonUnsupported, arrange: func(t *testing.T, s *deployStores) resource.Deployment {
			other, _ := s.projects.Create(ctx, resource.Project{Metadata: resource.Metadata{Name: "q"}})
			env, err := s.envs.Create(ctx, resource.Environment{Metadata: resource.Metadata{Name: "far", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: other.Metadata.ID}}}})
			if err != nil {
				t.Fatal(err)
			}
			return s.deployment(t, env.Metadata.ID)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newDeployStores(t)
			fake := &fakeDeployments{}
			r := reconcile.NewDeploymentReconciler(s.deployments, s.envs, s.components, s.apps, fake)
			d := tt.arrange(t, s)
			checkRequeue(t, r.Reconcile(ctx, graph.ResourceRef{Kind: resource.KindDeployment, ID: d.Metadata.ID}), tt.requeue)
			got, _ := s.deployments.Get(ctx, d.Metadata.ID)
			if c := readyCondition(t, got.Status.Conditions); c.Status != resource.ConditionFalse || c.Reason != tt.wantReason {
				t.Errorf("Ready = %+v, want %s", c, tt.wantReason)
			}
			if len(fake.deployed) != 0 {
				t.Error("the provider was asked to deploy")
			}
		})
	}
}

// While its Component isn't ready, a Deployment says why in the
// Component's own words: after a restart that's usually only that the
// platform can't be read yet (T015 finding F2).
func TestDeploymentReconciler_SaysWhyItWaitsForTheComponent(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		conditions []resource.Condition
		want       string
	}{
		{nil, "it has no release yet"},
		{[]resource.Condition{{Type: "Ready", Status: resource.ConditionFalse, Reason: reconcile.ReasonWaitingForApplication}}, "it isn't ready (WaitingForApplication)"},
	} {
		s := newDeployStores(t)
		c, err := s.components.Get(ctx, s.comp.Metadata.ID)
		if err != nil {
			t.Fatal(err)
		}
		c.Status.Conditions = tt.conditions
		if _, err := s.components.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
		r := reconcile.NewDeploymentReconciler(s.deployments, s.envs, s.components, s.apps, &fakeDeployments{})
		d := s.deployment(t, s.env)
		checkRequeue(t, r.Reconcile(ctx, graph.ResourceRef{Kind: resource.KindDeployment, ID: d.Metadata.ID}), true)
		got, _ := s.deployments.Get(ctx, d.Metadata.ID)
		if c := readyCondition(t, got.Status.Conditions); c.Reason != reconcile.ReasonWaitingForComponent || !strings.HasSuffix(c.Message, tt.want) {
			t.Errorf("Ready = %+v, want a message ending %q", c, tt.want)
		}
	}
}

// A message Serendip writes from its own state is rewritten when that
// state changes, though the reason stays the same (T016 review).
func TestDeploymentReconciler_RefreshesItsOwnMessages(t *testing.T) {
	ctx := context.Background()
	s := newDeployStores(t)
	fake := &fakeDeployments{obs: reconcile.DeploymentObservation{Reason: reconcile.ReasonWaitingForComponent, Message: "waiting for: Component a"}}
	r := reconcile.NewDeploymentReconciler(s.deployments, s.envs, s.components, s.apps, fake)
	d := s.deployment(t, s.env)
	ref := graph.ResourceRef{Kind: resource.KindDeployment, ID: d.Metadata.ID}
	checkRequeue(t, r.Reconcile(ctx, ref), true)
	fake.obs.Message = "waiting for: Component b"
	checkRequeue(t, r.Reconcile(ctx, ref), true)
	got, _ := s.deployments.Get(ctx, d.Metadata.ID)
	if c := readyCondition(t, got.Status.Conditions); c.Message != "waiting for: Component b" {
		t.Errorf("message = %q, want it refreshed", c.Message)
	}
	// The platform's own wording churning under Progressing isn't written.
	fake.obs = reconcile.DeploymentObservation{Reason: reconcile.ReasonProgressing, Message: "one"}
	checkRequeue(t, r.Reconcile(ctx, ref), true)
	before, _ := s.deployments.Get(ctx, d.Metadata.ID)
	fake.obs.Message = "two"
	checkRequeue(t, r.Reconcile(ctx, ref), true)
	after, _ := s.deployments.Get(ctx, d.Metadata.ID)
	if after.Metadata.ResourceVersion != before.Metadata.ResourceVersion {
		t.Error("Progressing message churn was written")
	}
}

func TestDeploymentReconciler_ProviderNotReadyAndGone(t *testing.T) {
	s := newDeployStores(t)
	ctx := context.Background()
	fake := &fakeDeployments{err: reconcile.ErrProviderNotReady}
	r := reconcile.NewDeploymentReconciler(s.deployments, s.envs, s.components, s.apps, fake)
	d := s.deployment(t, s.env)
	checkRequeue(t, r.Reconcile(ctx, graph.ResourceRef{Kind: resource.KindDeployment, ID: d.Metadata.ID}), true)
	got, _ := s.deployments.Get(ctx, d.Metadata.ID)
	if c := readyCondition(t, got.Status.Conditions); c.Reason != reconcile.ReasonProviderNotReady {
		t.Errorf("Ready = %+v", c)
	}
	// A Deployment that's gone is the approval clean-up's business.
	fake.deployed = nil
	if err := r.Reconcile(ctx, graph.ResourceRef{Kind: resource.KindDeployment, ID: "gone"}); err != nil || len(fake.deployed) != 0 {
		t.Errorf("a gone Deployment: %v, deployed %v", err, fake.deployed)
	}
}

func TestProjectReconciler(t *testing.T) {
	s := newDeployStores(t)
	ctx := context.Background()
	fake := &fakeDeployments{}
	r := reconcile.NewProjectReconciler(s.projects, fake)
	ref := graph.ResourceRef{Kind: resource.KindProject, ID: s.proj}
	if err := r.Reconcile(ctx, ref); err != nil {
		t.Fatal(err)
	}
	p, _ := s.projects.Get(ctx, s.proj)
	if len(fake.pipelines) != 1 || readyCondition(t, p.Status.Conditions).Status != resource.ConditionTrue {
		t.Errorf("pipelines %v, status %+v", fake.pipelines, p.Status)
	}
	version := p.Metadata.ResourceVersion
	if err := r.Reconcile(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.projects.Get(ctx, s.proj); again.Metadata.ResourceVersion != version {
		t.Error("a no-change reconcile wrote the Project")
	}
	// An invalid pipeline (written past the API) isn't sent.
	p, _ = s.projects.Get(ctx, s.proj)
	p.Spec.Pipeline = resource.Pipeline{Paths: []resource.PromotionPath{{From: "a", To: []string{"a"}}}}
	if _, err := s.projects.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	fake.pipelines = nil
	if err := r.Reconcile(ctx, ref); err != nil {
		t.Fatal(err)
	}
	p, _ = s.projects.Get(ctx, s.proj)
	if len(fake.pipelines) != 0 || readyCondition(t, p.Status.Conditions).Reason != reconcile.ReasonInvalidSpec {
		t.Errorf("invalid pipeline: sent %v, status %+v", fake.pipelines, p.Status)
	}
	// A platform error is retried.
	p.Spec.Pipeline = resource.Pipeline{}
	s.projects.Update(ctx, p)
	fake.err = errors.New("boom")
	if err := r.Reconcile(ctx, ref); err == nil {
		t.Error("a platform error wasn't returned")
	}
}

func TestCleanups(t *testing.T) {
	ctx := context.Background()
	fake := &fakeDeployments{}
	spec, _ := json.Marshal(resource.DeploymentSpec{Component: "c1"})
	if err := reconcile.UndeployCleanup(fake)(ctx, resource.ApprovalRef{Kind: resource.KindDeployment, ID: "d1", LastSpec: spec}); err != nil {
		t.Fatal(err)
	}
	// The ID is all it needs: a damaged last spec doesn't strand it.
	if err := reconcile.UndeployCleanup(fake)(ctx, resource.ApprovalRef{ID: "d2", LastSpec: []byte("{oops")}); err != nil {
		t.Fatal(err)
	}
	if len(fake.undeployed) != 2 || fake.undeployed[0].Metadata.ID != "d1" || fake.undeployed[1].Metadata.ID != "d2" {
		t.Errorf("undeployed %+v", fake.undeployed)
	}
	reconcile.EnvironmentCleanup(fake)(ctx, resource.ApprovalRef{ID: "e1"})
	reconcile.PipelineCleanup(fake)(ctx, resource.ApprovalRef{ID: "p1"})
	if !slices.Equal(fake.envsGone, []string{"e1"}) || !slices.Equal(fake.pipesGone, []string{"p1"}) {
		t.Errorf("environments %v, pipelines %v", fake.envsGone, fake.pipesGone)
	}
}
