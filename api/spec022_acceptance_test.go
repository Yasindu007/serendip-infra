package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/api/catalog"
	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// This file proves specs/022-builds/spec.md's acceptance criteria 1, 3, 4,
// 6-8, 10 and 11 in process (T012): the whole API stack; the real Build,
// Component, Deployment, ProviderInstance and Approval reconcilers, run by
// hand after each change as the engine would; a fake build capability
// (builder022) and a fake installer (installer022) standing in for the
// workflow plane. The console's side (the Builds tab, the Packages page)
// is proven by ui/src's tests; the real cluster by T014; the numbers
// (criterion 2), the cheap resync (5) and the footprint (12) by T013 and
// the installer's own tests.

// builder022 is a build capability whose runs move only when the test
// says so. It remembers the credential each run was started with.
type builder022 struct {
	mu        sync.Mutex
	runs      map[string]*run022 // by Build ID
	head      string
	lines     chan reconcile.LogLine
	cancelled []string
}

type run022 struct {
	commit  string
	phase   resource.BuildPhase
	message string
	cred    map[string]any
}

func newBuilder022() *builder022 {
	return &builder022{runs: map[string]*run022{}, head: strings.Repeat("1", 40), lines: make(chan reconcile.LogLine)}
}

func (f *builder022) StartBuild(_ context.Context, _ resource.Application, _ resource.Component, b resource.Build, cred map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.runs[b.Metadata.ID]; ok {
		return nil
	}
	commit := b.Spec.Commit
	if commit == "" {
		commit = f.head
	}
	f.runs[b.Metadata.ID] = &run022{commit: commit, phase: resource.BuildQueued, cred: cred}
	return nil
}

func (f *builder022) run(id string) *run022 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs[id]
}

// set moves a run on, as the platform would.
func (f *builder022) set(t *testing.T, id string, phase resource.BuildPhase, message string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[id]
	if !ok {
		t.Fatalf("build %s has no run", id)
	}
	r.phase, r.message = phase, message
}

func image022(commit string) string { return "localhost:5000/shop/api:" + commit[:12] }

func (f *builder022) ObserveBuild(_ context.Context, b resource.Build) (reconcile.BuildObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[b.Metadata.ID]
	if !ok {
		return reconcile.BuildObservation{Gone: true}, nil
	}
	obs := reconcile.BuildObservation{Phase: r.phase, Message: r.message, Commit: r.commit,
		Steps: []resource.BuildStep{{Name: "checkout", Phase: resource.BuildSucceeded}, {Name: "build", Phase: r.phase}}}
	if r.phase == resource.BuildSucceeded {
		obs.Image = image022(r.commit)
	}
	return obs, nil
}

func (f *builder022) CancelBuild(_ context.Context, b resource.Build) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, b.Metadata.ID)
	if r, ok := f.runs[b.Metadata.ID]; ok {
		r.phase = resource.BuildCancelled
	}
	return nil
}

func (f *builder022) RemoveBuild(_ context.Context, b resource.Build) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.runs, b.Metadata.ID)
	return nil
}

// BuildLogs answers a run's recent lines, which say how it ended, or with
// Follow streams whatever is sent on lines until it's closed.
func (f *builder022) BuildLogs(ctx context.Context, b resource.Build, opts reconcile.LogOptions) (reconcile.LogStream, error) {
	if opts.Follow {
		return &followStream{ctx: ctx, lines: f.lines}, nil
	}
	lines := []reconcile.LogLine{{Time: time.Now(), Instance: "build", Line: "STEP 1/2: FROM alpine"}}
	if r := f.run(b.Metadata.ID); r != nil && r.phase == resource.BuildFailed {
		lines = append(lines, reconcile.LogLine{Time: time.Now(), Instance: "build", Line: "error: make: exit status 2"})
	}
	return &sliceStream{lines: lines}, nil
}

func (f *builder022) BuildEvents(context.Context, resource.Build, time.Time) ([]reconcile.RuntimeEvent, error) {
	return []reconcile.RuntimeEvent{{Type: "Normal", Reason: "Pulled", ObjectKind: "Pod", ObjectName: "build"}}, nil
}

func (f *builder022) HeadCommit(context.Context, resource.ComponentSource, map[string]any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.head, nil
}

// platform022 is the application platform, remembering the image each
// Component and each Environment runs.
type platform022 struct {
	*fakePlatform020
	mu       sync.Mutex
	comps    map[string]string // image by Component ID
	envImage map[string]string // image by Environment ID
}

func (p *platform022) EnsureApplication(context.Context, resource.Application) (bool, string, string, error) {
	return true, reconcile.ReasonDeployed, "", nil
}
func (p *platform022) DeleteApplication(context.Context, resource.Application) error { return nil }
func (p *platform022) EnsureComponent(_ context.Context, _ resource.Application, c resource.Component) (reconcile.ComponentObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.comps[c.Metadata.ID] = c.Spec.Image
	return reconcile.ComponentObservation{Ready: true, Reason: reconcile.ReasonDeployed}, nil
}
func (p *platform022) DeleteComponent(context.Context, resource.Component) error { return nil }

func (p *platform022) Deploy(ctx context.Context, app resource.Application, c resource.Component, env resource.Environment, d resource.Deployment) (reconcile.DeploymentObservation, error) {
	p.mu.Lock()
	p.envImage[env.Metadata.ID] = c.Spec.Image
	p.mu.Unlock()
	return p.fakePlatform020.Deploy(ctx, app, c, env, d)
}

func (p *platform022) imageIn(env string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.envImage[env]
}

// creds022 is the Secrets capability's read of a value.
type creds022 map[string]map[string]any

func (c creds022) ReadValue(_ context.Context, id string) (map[string]any, error) {
	v, ok := c[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return v, nil
}

// installer022 installs the builds package; installed says whether its
// workloads are on the cluster.
type installer022 struct {
	mu                   sync.Mutex
	ready, installed     bool
	installs, uninstalls int
}

func (i *installer022) Install(context.Context, resource.ProviderInstance) (reconcile.PackageState, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.installs++
	i.installed = true
	return reconcile.PackageState{Ready: i.ready, Message: "starting the workflow plane"}, nil
}

func (i *installer022) Uninstall(context.Context, string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.uninstalls++
	i.installed = false
	return nil
}

func (i *installer022) setReady() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.ready = true
}

func (i *installer022) state() (installed bool, uninstalls int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.installed, i.uninstalls
}

const removal022 = "Build history stays; run logs, the registry and built images go. Running workloads keep their images until they're redeployed."

// world022 is the deploy world with the builds package in its catalogue,
// the fake build capability and platform, and every reconciler spec 022
// touches.
type world022 struct {
	*deployWorld
	registry  *provider.Registry
	builder   *builder022
	platform  *platform022
	installer *installer022
	creds     creds022
	cat       reconcile.Catalogue

	comps     *sqlite.Store[resource.ComponentSpec, resource.ComponentStatus]
	builds    *sqlite.Store[resource.BuildSpec, resource.BuildStatus]
	deps      *sqlite.Store[resource.DeploymentSpec, resource.DeploymentStatus]
	instances *sqlite.Store[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus]

	buildRecon    *reconcile.BuildReconciler
	compRecon     *reconcile.ComponentReconciler
	depRecon      *reconcile.DeploymentReconciler
	instRecon     *reconcile.ProviderInstanceReconciler
	approvalRecon *reconcile.ApprovalReconciler

	// bodies holds every response body, for criterion 7's leak check.
	bodies [][]byte
}

func newWorld022(t *testing.T) *world022 {
	t.Helper()
	reg := provider.NewRegistry()
	b := newBuilder022()
	p := &platform022{fakePlatform020: newFakePlatform020(), comps: map[string]string{}, envImage: map[string]string{}}
	inst := &installer022{}
	cat := reconcile.Catalogue{
		buildPackage: {Provider: buildPackage, Capability: "build", Description: "Build Components from source",
			Installer: inst, Removal: removal022,
			Requirements: &provider.Requirements{IdleMemoryMiB: 46, IdleCPUMillicores: 5, PeakMemoryMiB: 900, PeakCPUMillicores: 1800,
				MeasuredOn: "edge-1 simulated T470", Results: "profiles/edge-1/bench/results/builds"}},
		"observer": {Provider: "observer", Capability: "observability", Installer: nopInstaller{}}, // in this build, not measured
	}
	d := newDeployWorldWith(t, reg, api.WithPackages(cat), api.WithBuilds(b), api.WithRuntime(p))
	w := &world022{deployWorld: d, registry: reg, builder: b, platform: p, installer: inst, creds: creds022{}, cat: cat,
		comps:     sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](d.db, resource.KindComponent),
		builds:    sqlite.NewStore[resource.BuildSpec, resource.BuildStatus](d.db, resource.KindBuild),
		deps:      sqlite.NewStore[resource.DeploymentSpec, resource.DeploymentStatus](d.db, resource.KindDeployment),
		instances: sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](d.db, resource.KindProviderInstance).SetUniqueKey(resource.ProviderInstanceKey),
	}
	apps := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](d.db, resource.KindApplication)
	envs := sqlite.NewStore[resource.EnvironmentSpec, resource.EnvironmentStatus](d.db, resource.KindEnvironment)
	w.buildRecon = reconcile.NewBuildReconciler(w.builds, w.comps, apps, b, w.creds, nil)
	w.compRecon = reconcile.NewComponentReconciler(w.comps, apps, p).WithBuilds(w.builds, b, w.creds)
	w.depRecon = reconcile.NewDeploymentReconciler(w.deps, envs, w.comps, apps, p)
	report := func(capability string, ready bool, reason, message string) {
		reg.SetReadiness(capability, provider.Readiness{Ready: ready, Reason: reason, Message: message})
	}
	w.instRecon = reconcile.NewProviderInstanceReconciler(w.instances, cat, report)
	if err := w.instRecon.ReportStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	approvals := sqlite.NewStore[resource.ApprovalSpec, resource.ApprovalStatus](d.db, resource.KindApproval)
	del := func(ctx context.Context, kind resource.Kind, id string, cascade bool) error {
		return sqlite.NewStore[json.RawMessage, json.RawMessage](d.db, kind).Delete(ctx, id, cascade)
	}
	exists := func(ctx context.Context, kind resource.Kind, id string) (bool, error) {
		return sqlite.ResourceExists(ctx, d.db, kind, id)
	}
	w.approvalRecon = reconcile.NewApprovalReconciler(approvals, del, map[resource.Kind]reconcile.CleanupFunc{
		resource.KindProviderInstance: w.instRecon.UninstallCleanup(),
		resource.KindBuild:            reconcile.BuildCleanup(b),
	}, exists, time.Now)

	// The Application is Ready, as its reconciler would report.
	app, err := apps.Get(context.Background(), d.app)
	if err != nil {
		t.Fatal(err)
	}
	app.Status.Conditions = []resource.Condition{{Type: "Ready", Status: resource.ConditionTrue, Reason: reconcile.ReasonDeployed, LastTransitionTime: time.Now()}}
	if _, err := apps.Update(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	return w
}

// packageReady makes the builds package read Ready, as an install would.
func (w *world022) packageReady() {
	w.registry.SetReadiness("build", provider.Readiness{Ready: true, Reason: reconcile.ReasonInstalled})
}

func (w *world022) call(t *testing.T, token, method, path string, body any, want int) []byte {
	t.Helper()
	got := w.do(token, method, path, body)
	w.bodies = append(w.bodies, got.Body.Bytes())
	if got.Code != want {
		t.Fatalf("%s %s %s: %d, want %d (%s)", people[token].Name, method, path, got.Code, want, strings.TrimSpace(got.Body.String()))
	}
	return got.Body.Bytes()
}

var reconcilerCtx = audit.WithActor(audit.WithInterface(context.Background(), reconcile.InterfaceName), reconcile.ActorName)

// reconcileOnce runs r for ref; asking to be requeued is no failure.
func reconcileOnce(t *testing.T, r reconcile.Reconciler, kind resource.Kind, id string) {
	t.Helper()
	err := r.Reconcile(reconcilerCtx, graph.ResourceRef{Kind: kind, ID: id})
	var requeue reconcile.RequeueAfter
	if err != nil && !errors.As(err, &requeue) {
		t.Fatalf("reconcile %s %s: %v", kind, id, err)
	}
}

// build reconciles Build id and reads it back through the API.
func (w *world022) build(t *testing.T, id string) resource.Build {
	t.Helper()
	reconcileOnce(t, w.buildRecon, resource.KindBuild, id)
	return decodeBuild(t, w.call(t, "tok-reader", http.MethodGet, "/v1/builds/"+id, nil, http.StatusOK))
}

// component reconciles Component id and reads it back through the API.
func (w *world022) component(t *testing.T, id string) resource.Component {
	t.Helper()
	reconcileOnce(t, w.compRecon, resource.KindComponent, id)
	var c resource.Component
	json.Unmarshal(w.call(t, "tok-reader", http.MethodGet, "/v1/components/"+id, nil, http.StatusOK), &c)
	return c
}

func (w *world022) deployment(t *testing.T, id string) resource.Deployment {
	t.Helper()
	reconcileOnce(t, w.depRecon, resource.KindDeployment, id)
	return decodeDeployment(t, w.call(t, "tok-reader", http.MethodGet, "/v1/deployments/"+id, nil, http.StatusOK))
}

// createSource creates a source Component through the API.
func (w *world022) createSource(t *testing.T, token string, src map[string]any) resource.Component {
	t.Helper()
	body := sourceComponent(w.app, src)
	body["metadata"].(map[string]any)["name"] = "api"
	var c resource.Component
	json.Unmarshal(w.call(t, token, http.MethodPost, "/v1/components", body, http.StatusCreated), &c)
	return c
}

func (w *world022) trigger(t *testing.T, comp, commit string) resource.Build {
	t.Helper()
	var body any
	if commit != "" {
		body = map[string]string{"commit": commit}
	}
	return decodeBuild(t, w.call(t, "tok-devops", http.MethodPost, "/v1/components/"+comp+"/builds", body, http.StatusCreated))
}

// succeed runs Build id from queued to succeeded, checking each phase.
func (w *world022) succeed(t *testing.T, id string) resource.Build {
	t.Helper()
	if got := w.build(t, id); got.Status.Phase != resource.BuildQueued || !got.Status.Started {
		t.Fatalf("started build: %+v", got.Status)
	}
	w.builder.set(t, id, resource.BuildRunning, "")
	if got := w.build(t, id); got.Status.Phase != resource.BuildRunning {
		t.Fatalf("running build: %+v", got.Status)
	}
	w.builder.set(t, id, resource.BuildSucceeded, "")
	got := w.build(t, id)
	if got.Status.Phase != resource.BuildSucceeded || got.Status.Image == "" {
		t.Fatalf("succeeded build: %+v", got.Status)
	}
	return got
}

func (w *world022) deployTo(t *testing.T, comp, env string) resource.Deployment {
	t.Helper()
	body := map[string]any{"metadata": map[string]any{"name": "api", "ownerRefs": []any{map[string]any{"kind": "Environment", "id": env}}},
		"spec": map[string]any{"component": comp}}
	return decodeDeployment(t, w.call(t, "tok-devops", http.MethodPost, "/v1/deployments", body, http.StatusCreated))
}

func readiness022(t *testing.T, w *world022) provider.Readiness {
	t.Helper()
	var out map[string]provider.Readiness
	json.Unmarshal(w.call(t, "tok-owner", http.MethodGet, "/v1/readiness", nil, http.StatusOK), &out)
	return out["build"]
}

// TestSpec022Criterion1_CostBeforeInstall: with the package not installed,
// GET /v1/packages states its measured idle and in-build cost and where the
// numbers came from; an unmeasured package reads null, never a number.
// The CLI and the MCP tool are the same catalogue operation; the console's
// Packages page is ui/src's test.
func TestSpec022Criterion1_CostBeforeInstall(t *testing.T) {
	w := newWorld022(t)
	var list struct {
		Packages []struct {
			Provider       string
			Available      bool
			Installable    bool
			NotInstallable string
			Requirements   *provider.Requirements
			Removal        string
		}
	}
	body := w.call(t, "tok-owner", http.MethodGet, "/v1/packages", nil, http.StatusOK)
	if err := json.Unmarshal(body, &list); err != nil || len(list.Packages) != 2 {
		t.Fatalf("packages: %s (%v)", body, err)
	}
	for _, p := range list.Packages {
		switch p.Provider {
		case buildPackage:
			r := p.Requirements
			if !p.Available || !p.Installable || r == nil || r.IdleMemoryMiB != 46 || r.IdleCPUMillicores != 5 ||
				r.PeakMemoryMiB != 900 || r.PeakCPUMillicores != 1800 || r.MeasuredOn == "" || r.Results == "" {
				t.Errorf("the builds package: %+v (requirements %+v)", p, r)
			}
		case "observer":
			if p.Requirements != nil || p.Installable || !strings.Contains(p.NotInstallable, "measured") {
				t.Errorf("the unmeasured package: %+v", p)
			}
		}
	}
	if !strings.Contains(string(body), `"requirements":null`) {
		t.Errorf("an unmeasured package's cost must read null: %s", body)
	}
	if r := readiness022(t, w); r.Ready || r.Reason != reconcile.ReasonNotInstalled {
		t.Errorf("builds before install: %+v", r)
	}
	op := operation(t, "GET /v1/packages")
	if len(op.CLI) == 0 || !op.MCP {
		t.Errorf("packages list isn't a CLI command and an MCP tool: %+v", op)
	}
}

// TestSpec022Criterion3_Install: a Contributor's install is refused; the
// Owner's goes from not installed to installing to ready, and is audited.
func TestSpec022Criterion3_Install(t *testing.T) {
	w := newWorld022(t)
	if body := w.call(t, "tok-devops", http.MethodPost, "/v1/provider-instances", install(buildPackage, "build"), http.StatusForbidden); !strings.Contains(string(body), "serendip/provider-instances/write") {
		t.Errorf("the Contributor's refusal: %s", body)
	}
	var inst resource.ProviderInstance
	json.Unmarshal(w.call(t, "tok-owner", http.MethodPost, "/v1/provider-instances", install(buildPackage, "build"), http.StatusCreated), &inst)

	reconcileOnce(t, w.instRecon, resource.KindProviderInstance, inst.Metadata.ID)
	if r := readiness022(t, w); r.Ready || r.Reason != reconcile.ReasonInstalling {
		t.Errorf("while installing: %+v", r)
	}
	// A source Component can't be created yet, and says why.
	src := sourceComponent(w.app, map[string]any{"repository": "https://example.com/shop/api"})
	if body := w.call(t, "tok-devops", http.MethodPost, "/v1/components", src, http.StatusUnprocessableEntity); !strings.Contains(string(body), buildPackage) {
		t.Errorf("a source Component while installing: %s", body)
	}
	w.installer.setReady()
	reconcileOnce(t, w.instRecon, resource.KindProviderInstance, inst.Metadata.ID)
	if r := readiness022(t, w); !r.Ready {
		t.Errorf("once installed: %+v", r)
	}
	w.call(t, "tok-devops", http.MethodPost, "/v1/components", src, http.StatusCreated)

	var actor, decision string
	if err := w.db.QueryRow(`SELECT actor, COALESCE(policy_decision, '') FROM audit_log WHERE resource_id = ? AND interface = ?`,
		inst.Metadata.ID, api.InterfaceName).Scan(&actor, &decision); err != nil {
		t.Fatal(err)
	}
	if actor != "person:owner" || !strings.Contains(decision, "allow: Owner") {
		t.Errorf("the install audited as %q, %q", actor, decision)
	}
	var refused int
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE kind = 'ProviderInstance' AND actor = 'person:devops' AND policy_decision LIKE 'deny:%'`).Scan(&refused); err != nil || refused != 1 {
		t.Errorf("the refused install's audit rows: %d (%v)", refused, err)
	}
}

// TestSpec022Criterion4_Uniqueness: concurrent installs leave one
// ProviderInstance and the rest get 409; and the store refuses a second
// one by itself, without the API's check.
func TestSpec022Criterion4_Uniqueness(t *testing.T) {
	w := newWorld022(t)
	const n = 10
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = w.do("tok-owner", http.MethodPost, "/v1/provider-instances", install(buildPackage, "build")).Code
		}()
	}
	wg.Wait()
	count := map[int]int{}
	for _, c := range codes {
		count[c]++
	}
	if count[http.StatusCreated] != 1 || count[http.StatusConflict] != n-1 {
		t.Errorf("status counts %v, want one 201 and %d 409s", count, n-1)
	}

	// Past the API: the store's unique key decides.
	_, err := w.instances.Create(context.Background(), resource.ProviderInstance{Metadata: resource.Metadata{Name: "again"},
		Spec: resource.ProviderInstanceSpec{CapabilityID: "build", ProviderID: buildPackage}})
	if !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("a second instance written past the API: %v, want ErrDuplicate", err)
	}
	all, err := w.instances.List(context.Background(), store.ListOptions{})
	if err != nil || len(all) != 1 {
		t.Errorf("instances: %d (%v), want 1", len(all), err)
	}
}

// TestSpec022Criterion6_PublicRepository: a person creates a Component
// from a public repository, triggers a build, watches it go from queued
// to succeeded, follows its logs live, sees the Component served through
// its Deployment in the first Environment, and promotes it.
func TestSpec022Criterion6_PublicRepository(t *testing.T) {
	w := newWorld022(t)
	w.packageReady()
	comp := w.createSource(t, "tok-devops", map[string]any{"repository": "https://example.com/shop/api"})
	commit := strings.Repeat("2", 40)
	b := w.trigger(t, comp.Metadata.ID, commit)
	if b.Status.Phase != "" && b.Status.Phase != resource.BuildQueued {
		t.Errorf("a new build: %+v", b.Status)
	}
	// Until it succeeds the Component waits for it, and no auto-build is
	// added beside the person's.
	if c := w.component(t, comp.Metadata.ID); c.Status.Image != "" || !strings.Contains(fmt.Sprint(c.Status.Conditions), reconcile.ReasonWaitingForBuild) {
		t.Errorf("the Component before its build: %+v", c.Status)
	}
	var list []resource.Build
	json.Unmarshal(w.call(t, "tok-reader", http.MethodGet, "/v1/components/"+comp.Metadata.ID+"/builds", nil, http.StatusOK), &list)
	if len(list) != 1 {
		t.Errorf("builds: %d, want the one triggered", len(list))
	}

	if got := w.build(t, b.Metadata.ID); got.Status.Phase != resource.BuildQueued || !got.Status.Started {
		t.Fatalf("started build: %+v", got.Status)
	}
	w.builder.set(t, b.Metadata.ID, resource.BuildRunning, "")
	if got := w.build(t, b.Metadata.ID); got.Status.Phase != resource.BuildRunning || len(got.Status.Steps) != 2 {
		t.Fatalf("running build: %+v", got.Status)
	}

	// Following the running build's logs, over a real connection.
	srv := httptest.NewServer(w.h)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/builds/"+b.Metadata.ID+"/logs?follow=true", nil)
	req.Header.Set("Authorization", "Bearer tok-logreader")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("follow: %d", res.StatusCode)
	}
	read := bufio.NewScanner(res.Body)
	for _, line := range []string{"STEP 1/2: FROM alpine", "STEP 2/2: RUN make", "pushed " + image022(commit)} {
		w.builder.lines <- reconcile.LogLine{Time: time.Now(), Instance: "build", Line: line}
		if !read.Scan() || !strings.Contains(read.Text(), line) {
			t.Fatalf("followed line %q, want %q (%v)", read.Text(), line, read.Err())
		}
	}
	close(w.builder.lines)
	if !read.Scan() || read.Text() != `{"end":"ended"}` {
		t.Errorf("end of the stream: %q", read.Text())
	}

	w.builder.set(t, b.Metadata.ID, resource.BuildSucceeded, "")
	got := w.build(t, b.Metadata.ID)
	if got.Status.Phase != resource.BuildSucceeded || got.Status.Commit != commit || got.Status.Image != image022(commit) {
		t.Fatalf("succeeded build: %+v", got.Status)
	}
	c := w.component(t, comp.Metadata.ID)
	if c.Status.Image != image022(commit) || !ready022(c.Status.Conditions) {
		t.Fatalf("the built Component: %+v", c.Status)
	}

	// Served through its Deployment in the first Environment, then promoted.
	dev := w.deployTo(t, comp.Metadata.ID, w.dev)
	if d := w.deployment(t, dev.Metadata.ID); !ready(d) || w.platform.imageIn(w.dev) != image022(commit) {
		t.Fatalf("development: %+v, running %q", d.Status, w.platform.imageIn(w.dev))
	}
	staging := decodeDeployment(t, w.call(t, "tok-devops", http.MethodPost, "/v1/deployments/"+dev.Metadata.ID+"/promote", map[string]string{"to": w.staging}, http.StatusCreated))
	if d := w.deployment(t, staging.Metadata.ID); !ready(d) || w.platform.imageIn(w.staging) != image022(commit) {
		t.Errorf("staging: %+v, running %q", d.Status, w.platform.imageIn(w.staging))
	}
}

func ready022(conds []resource.Condition) bool {
	for _, c := range conds {
		if c.Type == "Ready" {
			return c.Status == resource.ConditionTrue
		}
	}
	return false
}

// syncBuffer collects what the log package writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestSpec022Criterion7_PrivateRepository: the same with a private
// repository whose credential is a Serendip Secret. The build is given the
// credential; it never appears in an API response, a line Serendip logs,
// or an audit record.
func TestSpec022Criterion7_PrivateRepository(t *testing.T) {
	logs := &syncBuffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	w := newWorld022(t)
	w.packageReady()
	const token = "ghp_s3cr3tV4lue9f2e8c1"
	sec, err := sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](w.db, resource.KindSecret).Create(context.Background(), resource.Secret{
		Metadata: resource.Metadata{Name: "git", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: w.app}}},
		Spec:     resource.SecretSpec{Path: "shop/git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.creds[sec.Metadata.ID] = map[string]any{"username": "git", "password": token}
	w.call(t, "tok-owner", http.MethodPost, authzPath+"roleAssignments",
		assignment(resource.PrincipalPerson, "sub-devops", policy.SecretsUserRoleID, resource.Scope{Kind: resource.KindApplication, ID: w.app}), http.StatusCreated)

	comp := w.createSource(t, "tok-devops", map[string]any{"repository": "https://example.com/shop/private", "credentialSecret": sec.Metadata.ID, "autoBuild": true})
	// The Component's own first pass starts the first build (and reads the
	// head with the credential, for auto-build).
	w.component(t, comp.Metadata.ID)
	var list []resource.Build
	json.Unmarshal(w.call(t, "tok-reader", http.MethodGet, "/v1/components/"+comp.Metadata.ID+"/builds", nil, http.StatusOK), &list)
	if len(list) != 1 {
		t.Fatalf("the first build: %d builds", len(list))
	}
	b := w.succeed(t, list[0].Metadata.ID)
	if r := w.builder.run(b.Metadata.ID); r == nil || r.cred["password"] != token {
		t.Fatalf("the build wasn't given the credential: %+v", r)
	}
	c := w.component(t, comp.Metadata.ID)
	if c.Status.Image != b.Status.Image {
		t.Fatalf("the built Component: %+v", c.Status)
	}
	dev := w.deployTo(t, comp.Metadata.ID, w.dev)
	w.deployment(t, dev.Metadata.ID)

	// Everything a person reads about it.
	base := "/v1/builds/" + b.Metadata.ID
	w.call(t, "tok-logreader", http.MethodGet, base+"/logs", nil, http.StatusOK)
	w.call(t, "tok-logreader", http.MethodGet, base+"/events", nil, http.StatusOK)
	w.call(t, "tok-devops", http.MethodGet, "/v1/components/"+comp.Metadata.ID, nil, http.StatusOK)
	w.call(t, "tok-devops", http.MethodGet, "/v1/components?ownerId="+w.app, nil, http.StatusOK)
	w.call(t, "tok-devops", http.MethodGet, "/v1/secrets/"+sec.Metadata.ID, nil, http.StatusOK)
	for _, body := range w.bodies {
		if bytes.Contains(body, []byte(token)) {
			t.Errorf("an API response carries the credential: %s", body)
		}
	}
	if strings.Contains(logs.String(), token) {
		t.Errorf("Serendip's log carries the credential: %s", logs.String())
	}
	rows, err := w.db.Query(`SELECT * FROM audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	n := 0
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		n++
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b) // a []byte prints as numbers
			}
		}
		if strings.Contains(fmt.Sprint(vals...), token) {
			t.Errorf("an audit record carries the credential: %v", vals)
		}
	}
	if err := rows.Err(); err != nil || n == 0 {
		t.Errorf("audit rows: %d (%v)", n, err)
	}
}

// TestSpec022Criterion8_FailureAndCancel: a failed build shows failed with
// its logs and the running release is unchanged; a running build
// cancelled reads cancelled.
func TestSpec022Criterion8_FailureAndCancel(t *testing.T) {
	w := newWorld022(t)
	w.packageReady()
	comp := w.createSource(t, "tok-devops", map[string]any{"repository": "https://example.com/shop/api"})
	first := strings.Repeat("3", 40)
	good := w.succeed(t, w.trigger(t, comp.Metadata.ID, first).Metadata.ID)
	w.component(t, comp.Metadata.ID)
	dev := w.deployTo(t, comp.Metadata.ID, w.dev)
	w.deployment(t, dev.Metadata.ID)
	if w.platform.imageIn(w.dev) != good.Status.Image {
		t.Fatalf("development runs %q", w.platform.imageIn(w.dev))
	}

	// A failing build.
	bad := w.trigger(t, comp.Metadata.ID, strings.Repeat("4", 40))
	w.build(t, bad.Metadata.ID)
	w.builder.set(t, bad.Metadata.ID, resource.BuildRunning, "")
	w.build(t, bad.Metadata.ID)
	w.builder.set(t, bad.Metadata.ID, resource.BuildFailed, "step build failed: exit status 2")
	got := w.build(t, bad.Metadata.ID)
	if got.Status.Phase != resource.BuildFailed || got.Status.Image != "" || !strings.Contains(fmt.Sprint(got.Status.Conditions), "exit status 2") {
		t.Errorf("the failed build: %+v", got.Status)
	}
	if body := w.call(t, "tok-logreader", http.MethodGet, "/v1/builds/"+bad.Metadata.ID+"/logs", nil, http.StatusOK); !strings.Contains(string(body), "make: exit status 2") {
		t.Errorf("the failed build's logs: %s", body)
	}
	if c := w.component(t, comp.Metadata.ID); c.Status.Image != good.Status.Image || !ready022(c.Status.Conditions) {
		t.Errorf("the Component after a failed build: %+v", c.Status)
	}
	w.deployment(t, dev.Metadata.ID)
	if w.platform.imageIn(w.dev) != good.Status.Image {
		t.Errorf("development runs %q after a failed build, want %q", w.platform.imageIn(w.dev), good.Status.Image)
	}

	// A running build, cancelled.
	run := w.trigger(t, comp.Metadata.ID, strings.Repeat("5", 40))
	w.build(t, run.Metadata.ID)
	w.builder.set(t, run.Metadata.ID, resource.BuildRunning, "")
	w.build(t, run.Metadata.ID)
	w.call(t, "tok-devops", http.MethodPost, "/v1/builds/"+run.Metadata.ID+"/cancel", nil, http.StatusOK)
	if got := w.build(t, run.Metadata.ID); got.Status.Phase != resource.BuildCancelled {
		t.Errorf("the cancelled build: %+v", got.Status)
	}
	w.builder.mu.Lock()
	cancelled := fmt.Sprint(w.builder.cancelled)
	w.builder.mu.Unlock()
	if !strings.Contains(cancelled, run.Metadata.ID) {
		t.Errorf("the run wasn't stopped: %s", cancelled)
	}
	if c := w.component(t, comp.Metadata.ID); c.Status.Image != good.Status.Image {
		t.Errorf("the Component after a cancel: %+v", c.Status)
	}
}

// TestSpec022Criterion10_Removal: what's lost is stated before the
// request; an agent's removal waits for a person's approval; once
// approved the package's workloads are uninstalled and the builds
// capability reads not installed, so the console greys its sections out.
func TestSpec022Criterion10_Removal(t *testing.T) {
	withAgentTokens(t)
	w := newWorld022(t)
	s := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](w.db, resource.KindRoleAssignment)
	if _, err := s.Create(context.Background(), resource.RoleAssignment{Metadata: resource.Metadata{Name: "agent-grant"}, Spec: resource.RoleAssignmentSpec{
		Principal:        resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "am-dev/agent-1", Sponsor: &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "sub-owner"}},
		RoleDefinitionID: policy.OwnerRoleID, Scope: resource.RootScope}}); err != nil {
		t.Fatal(err)
	}

	var inst resource.ProviderInstance
	json.Unmarshal(w.call(t, "tok-owner", http.MethodPost, "/v1/provider-instances", install(buildPackage, "build"), http.StatusCreated), &inst)
	w.installer.setReady()
	reconcileOnce(t, w.instRecon, resource.KindProviderInstance, inst.Metadata.ID)
	if r := readiness022(t, w); !r.Ready {
		t.Fatalf("installed: %+v", r)
	}

	// What's lost, stated before any request.
	if body := w.call(t, "tok-owner", http.MethodGet, "/v1/packages", nil, http.StatusOK); !strings.Contains(string(body), removal022) {
		t.Errorf("the removal sentence: %s", body)
	}

	// The agent can't remove it outright; its request waits.
	if body := w.call(t, "tok-agent", http.MethodDelete, "/v1/provider-instances/"+inst.Metadata.ID, nil, http.StatusForbidden); !strings.Contains(string(body), "needs-approval") {
		t.Errorf("the agent's delete: %s", body)
	}
	req := w.requestApproval("tok-agent", deleteRequest(resource.Scope{Kind: resource.KindProviderInstance, ID: inst.Metadata.ID}, false, "unused"), http.StatusCreated)
	for range 3 {
		reconcileOnce(t, w.approvalRecon, resource.KindApproval, req.Metadata.ID)
	}
	if _, err := w.instances.Get(context.Background(), inst.Metadata.ID); err != nil {
		t.Fatalf("a pending request removed the package: %v", err)
	}
	if installed, _ := w.installer.state(); !installed || !readiness022(t, w).Ready {
		t.Fatal("a pending request uninstalled the package")
	}

	// A person approves; the approval runs it.
	w.call(t, "tok-owner", http.MethodPost, "/v1/approvals/"+req.Metadata.ID+"/approve", nil, http.StatusOK)
	approvals := sqlite.NewStore[resource.ApprovalSpec, resource.ApprovalStatus](w.db, resource.KindApproval)
	var a resource.Approval
	for range 5 {
		reconcileOnce(t, w.approvalRecon, resource.KindApproval, req.Metadata.ID)
		var err error
		if a, err = approvals.Get(context.Background(), req.Metadata.ID); err != nil {
			t.Fatal(err)
		}
		if a.Status.Settled {
			break
		}
	}
	if a.Status.Phase != resource.ApprovalExecuted || !a.Status.Settled {
		t.Fatalf("the approval: %+v", a.Status)
	}
	if _, err := w.instances.Get(context.Background(), inst.Metadata.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the ProviderInstance after removal: %v", err)
	}
	if installed, uninstalls := w.installer.state(); installed || uninstalls != 1 {
		t.Errorf("the package's workloads after removal: installed %v, uninstalls %d", installed, uninstalls)
	}
	if r := readiness022(t, w); r.Ready || r.Reason != reconcile.ReasonNotInstalled {
		t.Errorf("builds after removal: %+v", r)
	}
	// A source Component now says it needs the package.
	src := sourceComponent(w.app, map[string]any{"repository": "https://example.com/shop/api"})
	if body := w.call(t, "tok-devops", http.MethodPost, "/v1/components", src, http.StatusUnprocessableEntity); !strings.Contains(string(body), buildPackage) {
		t.Errorf("a source Component after removal: %s", body)
	}
}

func operation(t *testing.T, route string) catalog.Operation {
	t.Helper()
	for _, op := range catalog.Operations() {
		if op.Method+" "+op.Path == route {
			return op
		}
	}
	t.Fatalf("%s: no catalogue operation", route)
	return catalog.Operation{}
}

// TestSpec022Criterion11_AuthorizationAndParity: every route spec 022
// added has a rule (NewServer panics on one without, and
// TestEveryRouteHasARule lists them), a Reader sees runs but not their
// logs, and every console action is a CLI command and an MCP tool. That
// an agent's removal waits for a person is criterion 10's test.
func TestSpec022Criterion11_AuthorizationAndParity(t *testing.T) {
	w := newWorld022(t)
	w.packageReady()
	comp := w.createSource(t, "tok-devops", map[string]any{"repository": "https://example.com/shop/api"})
	b := w.trigger(t, comp.Metadata.ID, "")
	w.build(t, b.Metadata.ID)
	w.builder.set(t, b.Metadata.ID, resource.BuildRunning, "")
	w.build(t, b.Metadata.ID)

	w.call(t, "tok-reader", http.MethodGet, "/v1/components/"+comp.Metadata.ID+"/builds", nil, http.StatusOK)
	w.call(t, "tok-reader", http.MethodGet, "/v1/builds/"+b.Metadata.ID, nil, http.StatusOK)
	w.call(t, "tok-reader", http.MethodGet, "/v1/builds/"+b.Metadata.ID+"/events", nil, http.StatusOK)
	if body := w.call(t, "tok-reader", http.MethodGet, "/v1/builds/"+b.Metadata.ID+"/logs", nil, http.StatusForbidden); !strings.Contains(string(body), policy.ActionComponentLogsRead) {
		t.Errorf("the Reader's logs refusal: %s", body)
	}
	w.call(t, "tok-reader", http.MethodPost, "/v1/components/"+comp.Metadata.ID+"/builds", nil, http.StatusForbidden)
	w.call(t, "tok-reader", http.MethodPost, "/v1/builds/"+b.Metadata.ID+"/cancel", nil, http.StatusForbidden)
	w.call(t, "tok-logreader", http.MethodGet, "/v1/builds/"+b.Metadata.ID+"/logs", nil, http.StatusOK)

	// The console's actions: the Builds tab, the Component form's source
	// fields (the Component routes), and the Packages page.
	excluded := catalog.Excluded()
	for _, r := range []string{
		"POST /v1/components/{id}/builds", "GET /v1/components/{id}/builds", "GET /v1/builds/{id}",
		"POST /v1/builds/{id}/cancel", "GET /v1/builds/{id}/events", "GET /v1/builds/{id}/logs",
		"POST /v1/components", "PUT /v1/components/{id}",
		"GET /v1/packages", "POST /v1/provider-instances", "DELETE /v1/provider-instances/{id}",
	} {
		op := operation(t, r)
		if len(op.CLI) == 0 && excluded[r].CLI == "" {
			t.Errorf("%s: no CLI command and no reason", r)
		}
		if !op.MCP && excluded[r].MCP == "" {
			t.Errorf("%s: no MCP tool and no reason", r)
		}
	}
	for _, p := range operation(t, "GET /v1/builds/{id}/logs").Params {
		if p.Name == "follow" && p.CLIOnly == "" {
			t.Error("following build logs is offered to MCP")
		}
	}
}
