package reconcile_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

const (
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// fakeBuilds is a BuildProvider whose runs are a map of observations.
type fakeBuilds struct {
	runs     map[string]reconcile.BuildObservation // by Build ID
	startErr error
	notReady bool
	head     string
	headErr  error

	started, cancelled, removed []string
	startedSource               []resource.ComponentSource
	startedCred                 []map[string]any
	headCalls                   int
}

func newFakeBuilds() *fakeBuilds {
	return &fakeBuilds{runs: map[string]reconcile.BuildObservation{}}
}

func (f *fakeBuilds) StartBuild(_ context.Context, _ resource.Application, comp resource.Component, b resource.Build, cred map[string]any) error {
	if f.notReady {
		return reconcile.ErrProviderNotReady
	}
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, b.Metadata.ID)
	f.startedSource = append(f.startedSource, *comp.Spec.Source)
	f.startedCred = append(f.startedCred, cred)
	if _, ok := f.runs[b.Metadata.ID]; !ok {
		f.runs[b.Metadata.ID] = reconcile.BuildObservation{Phase: resource.BuildQueued}
	}
	return nil
}

func (f *fakeBuilds) ObserveBuild(_ context.Context, b resource.Build) (reconcile.BuildObservation, error) {
	if f.notReady {
		return reconcile.BuildObservation{}, reconcile.ErrProviderNotReady
	}
	obs, ok := f.runs[b.Metadata.ID]
	if !ok {
		return reconcile.BuildObservation{Gone: true}, nil
	}
	return obs, nil
}

func (f *fakeBuilds) CancelBuild(_ context.Context, b resource.Build) error {
	f.cancelled = append(f.cancelled, b.Metadata.ID)
	delete(f.runs, b.Metadata.ID)
	return nil
}

func (f *fakeBuilds) RemoveBuild(_ context.Context, b resource.Build) error {
	f.removed = append(f.removed, b.Metadata.ID)
	delete(f.runs, b.Metadata.ID)
	return nil
}

func (f *fakeBuilds) BuildLogs(context.Context, resource.Build, reconcile.LogOptions) (reconcile.LogStream, error) {
	return nil, errors.New("not used")
}

func (f *fakeBuilds) BuildEvents(context.Context, resource.Build, time.Time) ([]reconcile.RuntimeEvent, error) {
	return nil, nil
}

func (f *fakeBuilds) HeadCommit(context.Context, resource.ComponentSource, map[string]any) (string, error) {
	f.headCalls++
	return f.head, f.headErr
}

// fakeCreds reads credentials from a map; a missing ID is not found.
type fakeCreds map[string]map[string]any

func (c fakeCreds) ReadValue(_ context.Context, id string) (map[string]any, error) {
	v, ok := c[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return v, nil
}

// recordingPlatform is an ApplicationPlatformProvider that records the
// Component it was asked to run.
type recordingPlatform struct {
	fakeAppPlatform
	ran []resource.ComponentSpec
}

func (p *recordingPlatform) EnsureComponent(ctx context.Context, app resource.Application, comp resource.Component) (reconcile.ComponentObservation, error) {
	p.ran = append(p.ran, comp.Spec)
	return reconcile.ComponentObservation{Ready: true, Reason: reconcile.ReasonDeployed}, nil
}

type buildFixture struct {
	t        *testing.T
	ctx      context.Context
	s        appStores
	builds   store.Store[resource.BuildSpec, resource.BuildStatus]
	provider *fakeBuilds
	platform *recordingPlatform
	creds    fakeCreds
	br       *reconcile.BuildReconciler
	cr       *reconcile.ComponentReconciler
	now      time.Time
	enqueued []graph.ResourceRef
	app      resource.Application
	comp     resource.Component
}

func newBuildFixture(t *testing.T, src resource.ComponentSource) *buildFixture {
	t.Helper()
	f := &buildFixture{t: t, ctx: context.Background(), s: newAppStores(t), provider: newFakeBuilds(),
		platform: &recordingPlatform{}, creds: fakeCreds{}, now: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)}
	f.builds = sqlite.NewStore[resource.BuildSpec, resource.BuildStatus](f.s.db, resource.KindBuild)
	clock := func() time.Time { return f.now }
	f.br = reconcile.NewBuildReconciler(f.builds, f.s.components, f.s.apps, f.provider, f.creds,
		func(ref graph.ResourceRef) { f.enqueued = append(f.enqueued, ref) })
	reconcile.SetBuildClock(f.br, clock)
	f.cr = reconcile.NewComponentReconciler(f.s.components, f.s.apps, f.platform).WithBuilds(f.builds, f.provider, f.creds)
	reconcile.SetComponentClock(f.cr, clock)

	var err error
	f.app, err = f.s.apps.Create(f.ctx, resource.Application{Metadata: resource.Metadata{Name: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	f.app.Status.Conditions = []resource.Condition{{Type: "Ready", Status: resource.ConditionTrue, Reason: reconcile.ReasonDeployed}}
	if f.app, err = f.s.apps.Update(f.ctx, f.app); err != nil {
		t.Fatal(err)
	}
	f.comp, err = f.s.components.Create(f.ctx, resource.Component{
		Metadata: resource.Metadata{Name: "web", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: f.app.Metadata.ID}}},
		Spec:     resource.ComponentSpec{Source: &src},
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func publicSource() resource.ComponentSource {
	return resource.ComponentSource{Repository: "https://github.com/org/web.git"}
}

func (f *buildFixture) reconcileComp() error {
	return f.cr.Reconcile(f.ctx, graph.ResourceRef{Kind: resource.KindComponent, ID: f.comp.Metadata.ID})
}

func (f *buildFixture) mustReconcileComp() {
	f.t.Helper()
	if err := f.reconcileComp(); err != nil && !isRequeue(err) {
		f.t.Fatalf("component reconcile: %v", err)
	}
}

func (f *buildFixture) reconcileBuild(id string) error {
	return f.br.Reconcile(f.ctx, graph.ResourceRef{Kind: resource.KindBuild, ID: id})
}

func (f *buildFixture) mustReconcileBuild(id string) {
	f.t.Helper()
	if err := f.reconcileBuild(id); err != nil && !isRequeue(err) {
		f.t.Fatalf("build reconcile %s: %v", id, err)
	}
}

func isRequeue(err error) bool {
	var rq reconcile.RequeueAfter
	return errors.As(err, &rq)
}

func (f *buildFixture) listBuilds() []resource.Build {
	f.t.Helper()
	bs, err := f.builds.List(f.ctx, store.ListOptions{OwnerID: f.comp.Metadata.ID})
	if err != nil {
		f.t.Fatal(err)
	}
	return bs
}

func (f *buildFixture) build(id string) resource.Build {
	f.t.Helper()
	b, err := f.builds.Get(f.ctx, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *buildFixture) component() resource.Component {
	f.t.Helper()
	c, err := f.s.components.Get(f.ctx, f.comp.Metadata.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

func (f *buildFixture) trigger(commit string) resource.Build {
	f.t.Helper()
	b, err := f.builds.Create(f.ctx, resource.Build{
		Metadata: resource.Metadata{Name: "manual", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindComponent, ID: f.comp.Metadata.ID}}},
		Spec:     resource.BuildSpec{Commit: commit},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *buildFixture) succeed(id, commit, image string) {
	f.provider.runs[id] = reconcile.BuildObservation{Phase: resource.BuildSucceeded, Commit: commit, Image: image,
		StartedAt: f.now.Add(-time.Minute), FinishedAt: f.now}
}

func readyOf(conds []resource.Condition, typ string) resource.Condition {
	for _, c := range conds {
		if c.Type == typ {
			return c
		}
	}
	return resource.Condition{}
}

// firstBuild creates the Component's first build through its reconciler
// and returns it.
func (f *buildFixture) firstBuild() resource.Build {
	f.t.Helper()
	f.mustReconcileComp()
	bs := f.listBuilds()
	if len(bs) != 1 {
		f.t.Fatalf("after creating a source Component: %d builds, want 1", len(bs))
	}
	return bs[0]
}

func TestComponentReconciler_FirstBuildIsAutomatic(t *testing.T) {
	f := newBuildFixture(t, publicSource())
	b := f.firstBuild()
	if b.Spec.Commit != "" {
		t.Errorf("first build commit = %q, want the branch head", b.Spec.Commit)
	}
	var actor string
	if err := f.s.db.QueryRow(`SELECT actor FROM audit_log WHERE resource_id = ? AND before IS NULL`, b.Metadata.ID).Scan(&actor); err != nil || actor != reconcile.AutoBuildActor {
		t.Errorf("first build created by %q (%v), want %s", actor, err, reconcile.AutoBuildActor)
	}
	c := readyOf(f.component().Status.Conditions, "Ready")
	if c.Status != resource.ConditionFalse || c.Reason != reconcile.ReasonWaitingForBuild || !strings.Contains(c.Message, b.Metadata.ID) {
		t.Errorf("Ready = %+v, want WaitingForBuild naming %s", c, b.Metadata.ID)
	}
	if len(f.platform.ran) != 0 {
		t.Errorf("the platform was asked to run a Component with no image: %+v", f.platform.ran)
	}

	// Idempotent: a second pass creates nothing and writes nothing.
	version := f.component().Metadata.ResourceVersion
	f.mustReconcileComp()
	if n := len(f.listBuilds()); n != 1 {
		t.Errorf("a second pass made %d builds, want 1", n)
	}
	if v := f.component().Metadata.ResourceVersion; v != version {
		t.Errorf("a second pass wrote the Component (v%d -> v%d)", version, v)
	}
}

func TestComponentReconciler_SourceChangeStartsABuild(t *testing.T) {
	f := newBuildFixture(t, publicSource())
	b := f.firstBuild()
	f.mustReconcileBuild(b.Metadata.ID)
	f.succeed(b.Metadata.ID, commitA, "localhost:5000/web@sha256:1")
	f.mustReconcileBuild(b.Metadata.ID)

	// The credential and auto-build don't change what's built.
	comp := f.component()
	comp.Spec.Source.CredentialSecret = "cred"
	if _, err := f.s.components.Update(f.ctx, comp); err != nil {
		t.Fatal(err)
	}
	f.mustReconcileComp()
	if n := len(f.listBuilds()); n != 1 {
		t.Fatalf("changing the credential made %d builds, want 1", n)
	}

	comp = f.component()
	comp.Spec.Source.Branch = "release"
	if _, err := f.s.components.Update(f.ctx, comp); err != nil {
		t.Fatal(err)
	}
	f.mustReconcileComp()
	if n := len(f.listBuilds()); n != 2 {
		t.Fatalf("changing the branch made %d builds in all, want 2", n)
	}
	// Until the new source builds, the Component waits for it.
	if c := readyOf(f.component().Status.Conditions, "Ready"); c.Reason != reconcile.ReasonWaitingForBuild {
		t.Errorf("after a source change, Ready = %+v, want WaitingForBuild", c)
	}
}

func TestBuildReconciler_Lifecycle(t *testing.T) {
	const image = "localhost:5000/serendip/cmp-x@sha256:1"
	tests := []struct {
		name string
		// run sets up the run after it started, before the next pass.
		run       func(f *buildFixture, id string)
		cancel    bool
		wantPhase resource.BuildPhase
		wantImage string
	}{
		{
			name:      "success gives the Component a new image",
			run:       func(f *buildFixture, id string) { f.succeed(id, commitA, image) },
			wantPhase: resource.BuildSucceeded, wantImage: image,
		},
		{
			name: "failure changes nothing",
			run: func(f *buildFixture, id string) {
				f.provider.runs[id] = reconcile.BuildObservation{Phase: resource.BuildFailed, Message: "build-push: exit status 1", FinishedAt: f.now}
			},
			wantPhase: resource.BuildFailed,
		},
		{
			name: "cancel stops the run",
			run: func(f *buildFixture, id string) {
				f.provider.runs[id] = reconcile.BuildObservation{Phase: resource.BuildRunning}
			},
			cancel:    true,
			wantPhase: resource.BuildCancelled,
		},
		{
			name:      "a run that vanished is a failure",
			run:       func(f *buildFixture, id string) { delete(f.provider.runs, id) },
			wantPhase: resource.BuildFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newBuildFixture(t, publicSource())
			// A running image from an earlier, successful build.
			first := f.firstBuild()
			f.mustReconcileBuild(first.Metadata.ID)
			f.succeed(first.Metadata.ID, commitA, "old-image")
			f.mustReconcileBuild(first.Metadata.ID)
			f.mustReconcileComp()
			if img := f.component().Status.Image; img != "old-image" {
				t.Fatalf("after the first build, image = %q", img)
			}

			b := f.trigger(commitB)
			f.mustReconcileBuild(b.Metadata.ID)
			if got := f.build(b.Metadata.ID); !got.Status.Started || got.Status.Phase != resource.BuildQueued || got.Status.Source == nil {
				t.Fatalf("after start: %+v", got.Status)
			}
			tt.run(f, b.Metadata.ID)
			if tt.cancel {
				got := f.build(b.Metadata.ID)
				got.Spec.Cancel = true
				if _, err := f.builds.Update(f.ctx, got); err != nil {
					t.Fatal(err)
				}
			}
			f.enqueued = nil
			f.mustReconcileBuild(b.Metadata.ID)
			got := f.build(b.Metadata.ID)
			if got.Status.Phase != tt.wantPhase || got.Status.FinishedAt.IsZero() {
				t.Fatalf("phase = %s (finished %v), want %s", got.Status.Phase, got.Status.FinishedAt, tt.wantPhase)
			}
			if tt.cancel && (len(f.provider.cancelled) != 1 || f.provider.cancelled[0] != b.Metadata.ID) {
				t.Errorf("cancelled runs %v, want %s", f.provider.cancelled, b.Metadata.ID)
			}
			if len(f.enqueued) == 0 || f.enqueued[0] != (graph.ResourceRef{Kind: resource.KindComponent, ID: f.comp.Metadata.ID}) {
				t.Errorf("a finished build re-queued %v, want its Component first", f.enqueued)
			}

			f.mustReconcileComp()
			want := tt.wantImage
			if want == "" {
				want = "old-image"
			}
			comp := f.component()
			if comp.Status.Image != want {
				t.Errorf("Component image = %q, want %q", comp.Status.Image, want)
			}
			if ran := f.platform.ran[len(f.platform.ran)-1]; ran.Image != want || ran.Source != nil {
				t.Errorf("the platform ran %+v, want image %q and no source", ran, want)
			}

			// A finished build is left alone: no provider call, no write.
			version, starts := got.Metadata.ResourceVersion, len(f.provider.started)
			if err := f.reconcileBuild(b.Metadata.ID); err != nil {
				t.Fatal(err)
			}
			if f.build(b.Metadata.ID).Metadata.ResourceVersion != version || len(f.provider.started) != starts {
				t.Error("a finished build was written or started again")
			}
		})
	}
}

func TestBuildReconciler_QueuedBehindARunningBuild(t *testing.T) {
	f := newBuildFixture(t, publicSource())
	first := f.firstBuild()
	f.mustReconcileBuild(first.Metadata.ID)
	f.provider.runs[first.Metadata.ID] = reconcile.BuildObservation{Phase: resource.BuildRunning}
	f.mustReconcileBuild(first.Metadata.ID)

	second := f.trigger(commitB)
	if err := f.reconcileBuild(second.Metadata.ID); err != nil {
		t.Fatalf("queued build: %v", err)
	}
	got := f.build(second.Metadata.ID)
	c := readyOf(got.Status.Conditions, "Ready")
	if got.Status.Started || got.Status.Phase != resource.BuildQueued || c.Reason != reconcile.ReasonWaitingForTurn || !strings.Contains(c.Message, first.Metadata.ID) {
		t.Fatalf("second build = %+v, %+v; want Queued behind %s", got.Status, c, first.Metadata.ID)
	}
	if len(f.provider.started) != 1 {
		t.Fatalf("started %v, want only the first", f.provider.started)
	}
	// Waiting is written once.
	version := got.Metadata.ResourceVersion
	if err := f.reconcileBuild(second.Metadata.ID); err != nil {
		t.Fatal(err)
	}
	if f.build(second.Metadata.ID).Metadata.ResourceVersion != version {
		t.Error("a waiting build was rewritten with nothing changed")
	}

	// The first finishing re-queues the second, which then starts.
	f.succeed(first.Metadata.ID, commitA, "img-1")
	f.enqueued = nil
	f.mustReconcileBuild(first.Metadata.ID)
	if !containsRef(f.enqueued, graph.ResourceRef{Kind: resource.KindBuild, ID: second.Metadata.ID}) {
		t.Errorf("finishing re-queued %v, want the queued build too", f.enqueued)
	}
	f.mustReconcileBuild(second.Metadata.ID)
	if !f.build(second.Metadata.ID).Status.Started {
		t.Error("the queued build didn't start once the first finished")
	}
}

func containsRef(refs []graph.ResourceRef, want graph.ResourceRef) bool {
	for _, r := range refs {
		if r == want {
			return true
		}
	}
	return false
}

func TestBuildReconciler_ProviderNotReadyWaits(t *testing.T) {
	f := newBuildFixture(t, publicSource())
	f.provider.notReady = true
	b := f.firstBuild()
	err := f.reconcileBuild(b.Metadata.ID)
	if !isRequeue(err) {
		t.Fatalf("Reconcile() = %v, want a requeue", err)
	}
	got := f.build(b.Metadata.ID)
	if c := readyOf(got.Status.Conditions, "Ready"); got.Status.Phase != resource.BuildQueued || got.Status.Started || c.Reason != reconcile.ReasonProviderNotReady {
		t.Errorf("build = %+v, %+v; want Queued, ProviderNotReady", got.Status, c)
	}
	f.mustReconcileComp()
	if c := readyOf(f.component().Status.Conditions, "Ready"); c.Reason != reconcile.ReasonWaitingForBuild {
		t.Errorf("Component Ready = %+v, want WaitingForBuild", c)
	}

	f.provider.notReady = false
	f.mustReconcileBuild(b.Metadata.ID)
	if !f.build(b.Metadata.ID).Status.Started {
		t.Error("the build didn't start once the provider was ready")
	}
}

func TestBuildReconciler_Credentials(t *testing.T) {
	src := publicSource()
	src.CredentialSecret = "git-cred"

	t.Run("read and handed to the run", func(t *testing.T) {
		f := newBuildFixture(t, src)
		f.creds["git-cred"] = map[string]any{"username": "u", "password": "p"}
		b := f.firstBuild()
		f.mustReconcileBuild(b.Metadata.ID)
		if len(f.provider.startedCred) != 1 || f.provider.startedCred[0]["password"] != "p" {
			t.Errorf("credential given %v", f.provider.startedCred)
		}
	})
	t.Run("a missing Secret fails the build", func(t *testing.T) {
		f := newBuildFixture(t, src)
		b := f.firstBuild()
		f.mustReconcileBuild(b.Metadata.ID)
		got := f.build(b.Metadata.ID)
		if c := readyOf(got.Status.Conditions, "Ready"); got.Status.Phase != resource.BuildFailed || !strings.Contains(c.Message, "git-cred") {
			t.Errorf("build = %s, %+v; want Failed naming the Secret", got.Status.Phase, c)
		}
		if len(f.provider.started) != 0 {
			t.Error("started without its credential")
		}
		// A failed build of the current source doesn't start another.
		f.mustReconcileComp()
		if n := len(f.listBuilds()); n != 1 {
			t.Errorf("%d builds after a failure, want 1", n)
		}
	})
	t.Run("a failed start removes the credential copy", func(t *testing.T) {
		f := newBuildFixture(t, src)
		f.creds["git-cred"] = map[string]any{"token": "t"}
		f.provider.startErr = errors.New("apiserver unavailable")
		b := f.firstBuild()
		err := f.reconcileBuild(b.Metadata.ID)
		if err == nil || isRequeue(err) {
			t.Fatalf("Reconcile() = %v, want the start error, to retry", err)
		}
		if len(f.provider.removed) != 1 || f.provider.removed[0] != b.Metadata.ID {
			t.Errorf("removed %v, want %s's run and copy", f.provider.removed, b.Metadata.ID)
		}
		got := f.build(b.Metadata.ID)
		if c := readyOf(got.Status.Conditions, "Ready"); got.Status.Started || c.Reason != reconcile.ReasonStartFailed || strings.Contains(c.Message, "apiserver") {
			t.Errorf("build = %+v, %+v; want not started, StartFailed, without the provider's text", got.Status, c)
		}
		f.provider.startErr = nil
		f.mustReconcileBuild(b.Metadata.ID)
		if !f.build(b.Metadata.ID).Status.Started {
			t.Error("the retry didn't start the build")
		}
	})
}

func TestBuildReconciler_BuildsItsOwnSource(t *testing.T) {
	f := newBuildFixture(t, publicSource())
	b := f.firstBuild()
	// Seen once while waiting: its source is recorded then.
	f.provider.notReady = true
	_ = f.reconcileBuild(b.Metadata.ID)
	comp := f.component()
	comp.Spec.Source.Branch = "other"
	if _, err := f.s.components.Update(f.ctx, comp); err != nil {
		t.Fatal(err)
	}
	f.provider.notReady = false
	f.mustReconcileBuild(b.Metadata.ID)
	if len(f.provider.startedSource) != 1 || f.provider.startedSource[0].Branch != "" {
		t.Errorf("started %+v, want the source as it was triggered (default branch)", f.provider.startedSource)
	}
}

func TestBuildReconciler_CancelBeforeStart(t *testing.T) {
	f := newBuildFixture(t, publicSource())
	b := f.firstBuild()
	b.Spec.Cancel = true
	if _, err := f.builds.Update(f.ctx, b); err != nil {
		t.Fatal(err)
	}
	f.mustReconcileBuild(b.Metadata.ID)
	if got := f.build(b.Metadata.ID); got.Status.Phase != resource.BuildCancelled {
		t.Errorf("phase = %s, want Cancelled", got.Status.Phase)
	}
	// The cancel is sent anyway (a no-op for a run that doesn't exist); no
	// run is started.
	if len(f.provider.started) != 0 || len(f.provider.cancelled) != 1 {
		t.Errorf("unstarted build: started %v, cancelled %v; want none started, one idempotent cancel", f.provider.started, f.provider.cancelled)
	}
	f.mustReconcileComp()
	if c := readyOf(f.component().Status.Conditions, "Ready"); !strings.Contains(c.Message, "cancelled") {
		t.Errorf("Component Ready = %+v, want it to say the build was cancelled", c)
	}
}

// conflictOnce is a Build store whose next Update loses to a concurrent
// edit, which before is made first.
type conflictOnce struct {
	store.Store[resource.BuildSpec, resource.BuildStatus]
	before func()
	armed  bool
}

func (c *conflictOnce) Update(ctx context.Context, b resource.Build) (resource.Build, error) {
	if c.armed {
		c.armed = false
		c.before()
		return resource.Build{}, store.ErrConflict
	}
	return c.Store.Update(ctx, b)
}

// A cancel that lands between a run's start and its status write still
// stops the run, and the dropped write re-queues nothing.
func TestBuildReconciler_CancelRacingAStart(t *testing.T) {
	f := newBuildFixture(t, publicSource())
	b := f.firstBuild()
	racing := &conflictOnce{Store: f.builds, armed: true, before: func() {
		got := f.build(b.Metadata.ID)
		got.Spec.Cancel = true
		if _, err := f.builds.Update(f.ctx, got); err != nil {
			t.Fatal(err)
		}
	}}
	f.enqueued = nil
	br := reconcile.NewBuildReconciler(racing, f.s.components, f.s.apps, f.provider, f.creds,
		func(ref graph.ResourceRef) { f.enqueued = append(f.enqueued, ref) })
	ref := graph.ResourceRef{Kind: resource.KindBuild, ID: b.Metadata.ID}
	if err := br.Reconcile(f.ctx, ref); !isRequeue(err) {
		t.Fatalf("a dropped write: Reconcile() = %v, want a requeue", err)
	}
	if len(f.provider.started) != 1 || f.build(b.Metadata.ID).Status.Started {
		t.Fatalf("setup: started %v; want the run started and Started not stored", f.provider.started)
	}
	if err := br.Reconcile(f.ctx, ref); err != nil {
		t.Fatal(err)
	}
	if got := f.build(b.Metadata.ID); got.Status.Phase != resource.BuildCancelled || len(f.provider.cancelled) != 1 {
		t.Errorf("phase %s, cancelled %v; want the started run cancelled", got.Status.Phase, f.provider.cancelled)
	}
	if _, running := f.provider.runs[b.Metadata.ID]; running {
		t.Error("the run kept going after the cancel")
	}
	if len(f.enqueued) != 1 {
		t.Errorf("re-queued %v; want only the Component, once, after the stored cancel", f.enqueued)
	}
}

// A commit built for one Dockerfile is built again for another.
func TestComponentReconciler_SameCommitNewSource(t *testing.T) {
	src := publicSource()
	src.AutoBuild = true
	f := newBuildFixture(t, src)
	b := f.firstBuild()
	f.mustReconcileBuild(b.Metadata.ID)
	f.succeed(b.Metadata.ID, commitA, "img-a")
	f.mustReconcileBuild(b.Metadata.ID)

	comp := f.component()
	comp.Spec.Source.Dockerfile = "Dockerfile.prod"
	if _, err := f.s.components.Update(f.ctx, comp); err != nil {
		t.Fatal(err)
	}
	f.mustReconcileComp() // the new source's first build
	bs := f.listBuilds()
	if len(bs) != 2 || bs[1].Metadata.Name != bs[0].Metadata.Name {
		t.Fatalf("builds %+v; want a second first build, under the same name", bs)
	}
	f.mustReconcileBuild(bs[1].Metadata.ID)
	f.succeed(bs[1].Metadata.ID, commitB, "img-b")
	f.mustReconcileBuild(bs[1].Metadata.ID)
	// commitA was built only for the old Dockerfile; a later head of A
	// (a revert) builds again.
	f.provider.head = commitA
	f.now = f.now.Add(10 * time.Minute)
	f.mustReconcileComp()
	if n := len(f.listBuilds()); n != 3 {
		t.Errorf("%d builds; want commitA built for the new Dockerfile too", n)
	}
}

func TestComponentReconciler_AutoBuild(t *testing.T) {
	auto := publicSource()
	auto.AutoBuild = true
	tests := []struct {
		name       string
		src        resource.ComponentSource
		headErr    error
		wantReason string
		wantText   string
		wantPolls  int
	}{
		{name: "SSH URL", src: resource.ComponentSource{Repository: "git@github.com:org/web.git", AutoBuild: true},
			wantReason: reconcile.ReasonSSHUnsupported, wantText: "can't see new commits for SSH URLs; use HTTPS"},
		{name: "needs a credential", src: auto, headErr: fmt.Errorf("%w: 401", reconcile.ErrSourceAuth),
			wantReason: reconcile.ReasonNeedsCredential, wantText: "needs a credential", wantPolls: 1},
		{name: "no branch", src: auto, headErr: fmt.Errorf("%w", reconcile.ErrSourceNoBranch),
			wantReason: reconcile.ReasonNoSuchBranch, wantText: "no branch main", wantPolls: 1},
		{name: "unreachable", src: auto, headErr: errors.New("dial tcp: i/o timeout"),
			wantReason: reconcile.ReasonUnreachable, wantText: "couldn't reach the repository since 2026-11-06T10:00:00Z", wantPolls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newBuildFixture(t, tt.src)
			f.provider.headErr = tt.headErr
			b := f.firstBuild()
			f.mustReconcileBuild(b.Metadata.ID)
			f.succeed(b.Metadata.ID, commitA, "img")
			f.mustReconcileBuild(b.Metadata.ID)
			f.mustReconcileComp()
			c := readyOf(f.component().Status.Conditions, "AutoBuild")
			if c.Status != resource.ConditionFalse || c.Reason != tt.wantReason || !strings.Contains(c.Message, tt.wantText) {
				t.Errorf("AutoBuild = %+v, want %s saying %q", c, tt.wantReason, tt.wantText)
			}
			if f.provider.headCalls != tt.wantPolls {
				t.Errorf("head read %d times, want %d", f.provider.headCalls, tt.wantPolls)
			}
			if strings.Contains(c.Message, "i/o timeout") {
				t.Error("the network error's text reached the status")
			}
			// Still failing later: the date stays the first failure's.
			f.now = f.now.Add(10 * time.Minute)
			version := f.component().Metadata.ResourceVersion
			f.mustReconcileComp()
			if v := f.component().Metadata.ResourceVersion; v != version {
				t.Errorf("an unchanged failure rewrote the Component (v%d -> v%d)", version, v)
			}
		})
	}
}

func TestComponentReconciler_AutoBuildNewCommit(t *testing.T) {
	src := publicSource()
	src.AutoBuild = true
	f := newBuildFixture(t, src)
	b := f.firstBuild()
	// While the first build runs, the head isn't read.
	f.mustReconcileBuild(b.Metadata.ID)
	f.mustReconcileComp()
	if f.provider.headCalls != 0 {
		t.Fatalf("the head was read %d times while a build ran", f.provider.headCalls)
	}
	f.succeed(b.Metadata.ID, commitA, "img-a")
	f.mustReconcileBuild(b.Metadata.ID)

	// The same head as built: no build.
	f.provider.head = commitA
	f.mustReconcileComp()
	if n := len(f.listBuilds()); n != 1 || f.provider.headCalls != 1 {
		t.Fatalf("the built head made %d builds (head read %d times), want 1 build", n, f.provider.headCalls)
	}
	if c := readyOf(f.component().Status.Conditions, "AutoBuild"); c.Status != resource.ConditionTrue || c.Reason != reconcile.ReasonWatching {
		t.Errorf("AutoBuild = %+v, want Watching", c)
	}

	// A new head inside 5 minutes isn't seen yet.
	f.provider.head = commitB
	f.now = f.now.Add(4 * time.Minute)
	f.mustReconcileComp()
	if f.provider.headCalls != 1 {
		t.Fatalf("the head was read again within 5 minutes")
	}

	// After 5 minutes: one build of it, attributed to auto-build.
	f.now = f.now.Add(time.Minute)
	f.mustReconcileComp()
	bs := f.listBuilds()
	if len(bs) != 2 || bs[1].Spec.Commit != commitB {
		t.Fatalf("builds after a new head: %+v, want one more, at %s", bs, commitB)
	}
	var actor string
	if err := f.s.db.QueryRow(`SELECT actor FROM audit_log WHERE resource_id = ? AND before IS NULL`, bs[1].Metadata.ID).Scan(&actor); err != nil || actor != reconcile.AutoBuildActor {
		t.Errorf("created by %q (%v)", actor, err)
	}
	// The old image keeps running while the new one builds.
	if img := f.component().Status.Image; img != "img-a" {
		t.Errorf("image while the new commit builds = %q, want img-a", img)
	}

	// The same head again, later: no second build.
	f.now = f.now.Add(10 * time.Minute)
	f.mustReconcileBuild(bs[1].Metadata.ID)
	f.succeed(bs[1].Metadata.ID, commitB, "img-b")
	f.mustReconcileBuild(bs[1].Metadata.ID)
	f.mustReconcileComp()
	if n := len(f.listBuilds()); n != 2 {
		t.Errorf("the same head made another build (%d in all)", n)
	}
	if img := f.component().Status.Image; img != "img-b" {
		t.Errorf("image = %q, want img-b", img)
	}
}

func TestComponentReconciler_SourceWithoutBuilds(t *testing.T) {
	s := newAppStores(t)
	ctx := context.Background()
	src := publicSource()
	comp, err := s.components.Create(ctx, resource.Component{
		Metadata: resource.Metadata{Name: "web", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: "app"}}},
		Spec:     resource.ComponentSpec{Source: &src},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := reconcile.NewComponentReconciler(s.components, s.apps, &fakeAppPlatform{})
	if err := r.Reconcile(ctx, graph.ResourceRef{Kind: resource.KindComponent, ID: comp.Metadata.ID}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.components.Get(ctx, comp.Metadata.ID)
	if c := readyOf(got.Status.Conditions, "Ready"); c.Reason != reconcile.ReasonUnsupported {
		t.Errorf("Ready = %+v, want Unsupported", c)
	}
}

// Deleting a Component removes its Builds' runs, through the approval's
// clean-up for each Build in the blast radius.
func TestBuildCleanup_DeletingAComponentRemovesItsRuns(t *testing.T) {
	f := newBuildFixture(t, publicSource())
	b1 := f.firstBuild()
	f.mustReconcileBuild(b1.Metadata.ID)
	b2 := f.trigger(commitB)

	approvals := sqlite.NewStore[resource.ApprovalSpec, resource.ApprovalStatus](f.s.db, resource.KindApproval)
	comp := f.component()
	a, err := approvals.Create(f.ctx, resource.Approval{
		Metadata: resource.Metadata{Name: "delete web"},
		Spec: resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: resource.ApprovalVerbDelete,
			Target: resource.Scope{Kind: resource.KindComponent, ID: comp.Metadata.ID}, Cascade: true}, Reason: "retire"},
		Status: resource.ApprovalStatus{
			Phase:         resource.ApprovalApproved,
			RequestedBy:   resource.ApprovalActor{Actor: "person:ann", Principal: &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "ann"}},
			TargetVersion: comp.Metadata.ResourceVersion,
			BlastRadius: []resource.ApprovalRef{
				{Kind: resource.KindComponent, ID: comp.Metadata.ID, Name: "web"},
				{Kind: resource.KindBuild, ID: b1.Metadata.ID, Name: b1.Metadata.Name},
				{Kind: resource.KindBuild, ID: b2.Metadata.ID, Name: b2.Metadata.Name},
			},
			Decision: &resource.ApprovalDecision{By: &resource.ApprovalActor{Actor: "person:bob", Principal: &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "bob"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	del := func(ctx context.Context, kind resource.Kind, id string, cascade bool) error {
		return sqlite.NewStore[json.RawMessage, json.RawMessage](f.s.db, kind).Delete(ctx, id, cascade)
	}
	exists := func(ctx context.Context, kind resource.Kind, id string) (bool, error) {
		return sqlite.ResourceExists(ctx, f.s.db, kind, id)
	}
	ar := reconcile.NewApprovalReconciler(approvals, del,
		map[resource.Kind]reconcile.CleanupFunc{resource.KindBuild: reconcile.BuildCleanup(f.provider)},
		exists, func() time.Time { return f.now })
	ref := graph.ResourceRef{Kind: resource.KindApproval, ID: a.Metadata.ID}
	for range 2 {
		if err := ar.Reconcile(f.ctx, ref); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{b1.Metadata.ID, b2.Metadata.ID} {
		if ok, _ := sqlite.ResourceExists(f.ctx, f.s.db, resource.KindBuild, id); ok {
			t.Errorf("Build %s survived its Component's delete", id)
		}
	}
	if len(f.provider.removed) != 2 {
		t.Errorf("removed runs %v, want both builds'", f.provider.removed)
	}
	// The build reconciler has nothing to do for a removed Build.
	if err := f.reconcileBuild(b1.Metadata.ID); err != nil {
		t.Errorf("reconciling a removed Build: %v", err)
	}
}

func TestDeploymentReconciler_ReleasesTheBuiltImage(t *testing.T) {
	s := newDeployStores(t)
	ctx := context.Background()
	src := publicSource()
	c := s.comp
	c.Spec.Image, c.Spec.Source = "", &src
	c.Status.Image = "localhost:5000/serendip/cmp-x@sha256:1"
	var err error
	if s.comp, err = s.components.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	fake := &fakeDeployments{obs: reconcile.DeploymentObservation{Release: "r1", Ready: true, Reason: reconcile.ReasonDeployed}}
	r := reconcile.NewDeploymentReconciler(s.deployments, s.envs, s.components, s.apps, fake)
	d := s.deployment(t, s.env)
	if err := r.Reconcile(ctx, graph.ResourceRef{Kind: resource.KindDeployment, ID: d.Metadata.ID}); err != nil {
		t.Fatal(err)
	}
	if len(fake.ran) != 1 || fake.ran[0].Image != c.Status.Image || fake.ran[0].Source != nil {
		t.Fatalf("released %+v, want the built image and no source", fake.ran)
	}

	// Without a built image, nothing is released.
	c, _ = s.components.Get(ctx, s.comp.Metadata.ID)
	c.Status.Image = ""
	if _, err := s.components.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := r.Reconcile(ctx, graph.ResourceRef{Kind: resource.KindDeployment, ID: d.Metadata.ID}); err != nil && !isRequeue(err) {
		t.Fatal(err)
	}
	got, _ := s.deployments.Get(ctx, d.Metadata.ID)
	if cond := readyOf(got.Status.Conditions, "Ready"); len(fake.ran) != 1 || cond.Reason != reconcile.ReasonWaitingForComponent {
		t.Errorf("without an image: released %d times, Ready %+v", len(fake.ran), cond)
	}
}
