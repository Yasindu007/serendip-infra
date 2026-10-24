package api_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// This file proves specs/020-openchoreo-console-features/spec.md's
// acceptance criteria 1-7, 10 and 11 in process (T013): the whole API
// stack, the real Deployment and ProviderInstance reconcilers, and a fake
// application platform (fakePlatform020) standing in for OpenChoreo. The
// console's side of each criterion (the screens, the greyed-out sections,
// every "Same request through the API" panel) is proven by
// ui/src/screens/platform.test.tsx; the real cluster by T015.

// fakePlatform020 is an application platform that remembers what it was
// asked to run: per Environment, the release and the overrides. It's every
// platform interface spec 020 added.
type fakePlatform020 struct {
	mu sync.Mutex
	// latest is the Component's newest release number.
	latest  int
	running map[string]resource.Deployment // by Environment ID
	lines   chan reconcile.LogLine
}

func newFakePlatform020() *fakePlatform020 {
	return &fakePlatform020{latest: 1, running: map[string]resource.Deployment{}, lines: make(chan reconcile.LogLine)}
}

func (f *fakePlatform020) release(n int) string { return fmt.Sprintf("web-r%d", n) }

func (f *fakePlatform020) newRelease() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latest++
}

func (f *fakePlatform020) EnsureEnvironment(context.Context, resource.Environment) error { return nil }
func (f *fakePlatform020) DeleteEnvironment(context.Context, resource.Environment) error { return nil }
func (f *fakePlatform020) EnsurePipeline(context.Context, resource.Project) error        { return nil }
func (f *fakePlatform020) DeletePipeline(context.Context, string) error                  { return nil }
func (f *fakePlatform020) Releases(context.Context, resource.Component) ([]reconcile.Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []reconcile.Release
	for n := f.latest; n >= 1; n-- {
		out = append(out, reconcile.Release{Name: f.release(n)})
	}
	return out, nil
}
func (f *fakePlatform020) CreateRelease(context.Context, resource.Component) (reconcile.Release, error) {
	f.newRelease()
	f.mu.Lock()
	defer f.mu.Unlock()
	return reconcile.Release{Name: f.release(f.latest)}, nil
}

// Deploy runs d's release, or the latest when it names none, with d's own
// overrides, the way the platform would.
func (f *fakePlatform020) Deploy(_ context.Context, _ resource.Application, _ resource.Component, env resource.Environment, d resource.Deployment) (reconcile.DeploymentObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d.Spec.Release == "" {
		d.Spec.Release = f.release(f.latest)
	}
	f.running[env.Metadata.ID] = d
	return reconcile.DeploymentObservation{Release: d.Spec.Release, Ready: true, Reason: reconcile.ReasonDeployed}, nil
}

func (f *fakePlatform020) Undeploy(_ context.Context, d resource.Deployment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for env, r := range f.running {
		if r.Metadata.ID == d.Metadata.ID {
			delete(f.running, env)
		}
	}
	return nil
}

func (f *fakePlatform020) runningIn(env string) resource.Deployment {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[env]
}

// RuntimeObjects reports the workload as it runs, with its replica count
// and autoscale setting, so a configuration change is visible on it.
func (f *fakePlatform020) RuntimeObjects(_ context.Context, _ resource.Deployment, env resource.Environment) ([]reconcile.RuntimeObject, error) {
	r := f.runningIn(env.Metadata.ID)
	msg := fmt.Sprintf("release %s, replicas %v, autoscale max %v", r.Spec.Release, r.Spec.Overrides.Config["replicas"], r.Spec.Overrides.Traits["autoscale"]["maxReplicas"])
	return []reconcile.RuntimeObject{{Kind: "Deployment", Name: "web-" + env.Metadata.Name, Health: "Healthy", Message: msg}}, nil
}

func (f *fakePlatform020) Events(context.Context, resource.Deployment, resource.Environment, time.Time) ([]reconcile.RuntimeEvent, error) {
	return []reconcile.RuntimeEvent{{Type: "Normal", Reason: "Started", ObjectKind: "Pod", ObjectName: "web-1", Message: "Started container"}}, nil
}

// Logs answers recent lines, or with Follow streams whatever is sent on
// lines until it's closed.
func (f *fakePlatform020) Logs(ctx context.Context, _ resource.Deployment, _ resource.Environment, opts reconcile.LogOptions) (reconcile.LogStream, error) {
	if !opts.Follow {
		return &sliceStream{lines: []reconcile.LogLine{{Time: time.Now(), Instance: "web-1", Line: "listening on :8080"}}}, nil
	}
	return &followStream{ctx: ctx, lines: f.lines}, nil
}

type followStream struct {
	ctx   context.Context
	lines chan reconcile.LogLine
}

func (s *followStream) Next() (reconcile.LogLine, error) {
	select {
	case l, ok := <-s.lines:
		if !ok {
			return reconcile.LogLine{}, io.EOF
		}
		return l, nil
	case <-s.ctx.Done():
		return reconcile.LogLine{}, s.ctx.Err()
	}
}

func (s *followStream) Close() error { return nil }

// The schemas the platform publishes: replicas per environment, and an
// autoscale trait.
func (f *fakePlatform020) ConfigSchemas(context.Context, resource.Component) (reconcile.ConfigSchemas, error) {
	return reconcile.ConfigSchemas{
		Environment: json.RawMessage(`{"type":"object","properties":{"replicas":{"type":"integer","minimum":1}}}`),
		Traits: map[string]json.RawMessage{
			"autoscale": json.RawMessage(`{"type":"object","properties":{"maxReplicas":{"type":"integer","minimum":1,"maximum":5}}}`),
		},
	}, nil
}

func (f *fakePlatform020) Platform(context.Context) (reconcile.PlatformView, error) {
	return reconcile.PlatformView{
		Runtimes:       []reconcile.PlatformRuntime{{Name: "default", Ready: true}},
		ComponentTypes: []reconcile.PlatformComponent{{Name: "deployment/serendip-service", AllowedTraits: []string{"autoscale"}}},
		Traits:         []reconcile.PlatformComponent{{Name: "autoscale"}},
	}, nil
}

// world020 is the deploy world on the fake platform, with the Deployment
// reconciler run by hand after each change, as the engine would.
type world020 struct {
	*deployWorld
	platform *fakePlatform020
	recon    *reconcile.DeploymentReconciler
	deps     *sqlite.Store[resource.DeploymentSpec, resource.DeploymentStatus]
}

func newWorld020(t *testing.T, registry *provider.Registry, opts ...api.ServerOption) *world020 {
	t.Helper()
	p := newFakePlatform020()
	opts = append([]api.ServerOption{api.WithRuntime(p), api.WithConfigSchemas(p), api.WithPlatformView(p)}, opts...)
	d := newDeployWorldWith(t, registry, opts...)
	w := &world020{deployWorld: d, platform: p, deps: sqlite.NewStore[resource.DeploymentSpec, resource.DeploymentStatus](d.db, resource.KindDeployment)}
	w.recon = reconcile.NewDeploymentReconciler(w.deps,
		sqlite.NewStore[resource.EnvironmentSpec, resource.EnvironmentStatus](d.db, resource.KindEnvironment),
		sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](d.db, resource.KindComponent),
		sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](d.db, resource.KindApplication), p)
	// The Component has a release, as its own reconciler would report.
	comps := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](d.db, resource.KindComponent)
	c, err := comps.Get(context.Background(), d.web)
	if err != nil {
		t.Fatal(err)
	}
	c.Status.Conditions = []resource.Condition{{Type: "Ready", Status: resource.ConditionTrue, Reason: reconcile.ReasonDeployed, LastTransitionTime: time.Now()}}
	if _, err := comps.Update(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return w
}

// reconcile runs the Deployment reconciler for id as the engine does.
func (w *world020) reconcile(t *testing.T, id string) resource.Deployment {
	t.Helper()
	ctx := audit.WithActor(audit.WithInterface(context.Background(), reconcile.InterfaceName), reconcile.ActorName)
	if err := w.recon.Reconcile(ctx, graph.ResourceRef{Kind: resource.KindDeployment, ID: id}); err != nil {
		t.Fatalf("reconcile %s: %v", id, err)
	}
	dep, err := w.deps.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return dep
}

// call sends a request and fails unless it's answered want.
func (w *world020) call(t *testing.T, token, method, path string, body any, want int) []byte {
	t.Helper()
	got := w.do(token, method, path, body)
	if got.Code != want {
		t.Fatalf("%s %s %s: %d, want %d (%s)", people[token].Name, method, path, got.Code, want, strings.TrimSpace(got.Body.String()))
	}
	return got.Body.Bytes()
}

func (w *world020) promote(t *testing.T, token, from, to string, want int) resource.Deployment {
	t.Helper()
	return decodeDeployment(t, w.call(t, token, http.MethodPost, "/v1/deployments/"+from+"/promote", map[string]string{"to": to}, want))
}

// edit reads Deployment id, changes it, and PUTs it back.
func (w *world020) edit(t *testing.T, token, id string, change func(*resource.Deployment), want int) []byte {
	t.Helper()
	dep := decodeDeployment(t, w.call(t, token, http.MethodGet, "/v1/deployments/"+id, nil, http.StatusOK))
	change(&dep)
	return w.call(t, token, http.MethodPut, "/v1/deployments/"+id, dep, want)
}

func ready(d resource.Deployment) bool {
	for _, c := range d.Status.Conditions {
		if c.Type == "Ready" {
			return c.Status == resource.ConditionTrue
		}
	}
	return false
}

// TestSpec020Criterion1_RuntimeView: a person opens a running Component and
// sees its resource tree, recent events and pod logs, including following
// new lines live.
func TestSpec020Criterion1_RuntimeView(t *testing.T) {
	w := newWorld020(t, nil)
	dep := decodeDeployment(t, w.call(t, "tok-devops", http.MethodPost, "/v1/deployments", w.deployment(w.dev, ""), http.StatusCreated))
	w.reconcile(t, dep.Metadata.ID)
	base := "/v1/deployments/" + dep.Metadata.ID

	var objects struct{ Objects []reconcile.RuntimeObject }
	json.Unmarshal(w.call(t, "tok-logreader", http.MethodGet, base+"/runtime", nil, http.StatusOK), &objects)
	if len(objects.Objects) != 1 || objects.Objects[0].Health != "Healthy" || !strings.Contains(objects.Objects[0].Message, "web-r1") {
		t.Errorf("runtime tree: %+v", objects.Objects)
	}
	if body := w.call(t, "tok-logreader", http.MethodGet, base+"/events", nil, http.StatusOK); !strings.Contains(string(body), `"reason":"Started"`) {
		t.Errorf("events: %s", body)
	}
	if body := w.call(t, "tok-logreader", http.MethodGet, base+"/logs", nil, http.StatusOK); !strings.Contains(string(body), "listening on :8080") {
		t.Errorf("recent logs: %s", body)
	}

	// Following, over a real connection: lines arrive as they're written.
	srv := httptest.NewServer(w.h)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+base+"/logs?follow=true", nil)
	req.Header.Set("Authorization", "Bearer tok-logreader")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("follow: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	read := bufio.NewScanner(res.Body)
	for _, line := range []string{"GET /healthz 200", "GET /cart 200"} {
		w.platform.lines <- reconcile.LogLine{Time: time.Now(), Instance: "web-1", Line: line}
		if !read.Scan() || !strings.Contains(read.Text(), line) {
			t.Fatalf("followed line: %q, want %q (%v)", read.Text(), line, read.Err())
		}
	}
	close(w.platform.lines)
	if !read.Scan() || read.Text() != `{"end":"ended"}` {
		t.Errorf("end of stream: %q", read.Text())
	}
}

// TestSpec020Criterion2_Promotion: development, staging and production in a
// pipeline; deploy, promote twice, a production-only override, a new
// release promoted without carrying overrides, rollback, and every step
// audited.
func TestSpec020Criterion2_Promotion(t *testing.T) {
	w := newWorld020(t, nil)
	const person = "tok-devops"

	dev := decodeDeployment(t, w.call(t, person, http.MethodPost, "/v1/deployments", w.deployment(w.dev, ""), http.StatusCreated))
	if got := w.reconcile(t, dev.Metadata.ID); got.Status.Release != "web-r1" || !ready(got) {
		t.Fatalf("development: %+v", got.Status)
	}
	staging := w.promote(t, person, dev.Metadata.ID, w.staging, http.StatusCreated)
	w.reconcile(t, staging.Metadata.ID)
	prod := w.promote(t, person, staging.Metadata.ID, w.prod, http.StatusCreated)
	w.reconcile(t, prod.Metadata.ID)

	// Production gets its own replicas and variable; development doesn't.
	w.edit(t, person, prod.Metadata.ID, func(d *resource.Deployment) {
		d.Spec.Overrides = resource.DeploymentOverrides{Env: map[string]string{"MODE": "production"}, Config: map[string]any{"replicas": 3}}
	}, http.StatusOK)
	w.reconcile(t, prod.Metadata.ID)
	if r := w.platform.runningIn(w.prod); r.Spec.Release != "web-r1" || r.Spec.Overrides.Env["MODE"] != "production" || fmt.Sprint(r.Spec.Overrides.Config["replicas"]) != "3" {
		t.Errorf("production runs %+v", r.Spec)
	}
	if r := w.platform.runningIn(w.dev); len(r.Spec.Overrides.Env) != 0 || len(r.Spec.Overrides.Config) != 0 {
		t.Errorf("development has production's overrides: %+v", r.Spec.Overrides)
	}

	// A new release enters at development and moves along; each
	// Environment keeps its own overrides.
	w.platform.newRelease()
	if got := w.reconcile(t, dev.Metadata.ID); got.Status.Release != "web-r2" {
		t.Fatalf("development after a new release: %+v", got.Status)
	}
	w.promote(t, person, dev.Metadata.ID, w.staging, http.StatusOK)
	w.reconcile(t, staging.Metadata.ID)
	w.promote(t, person, staging.Metadata.ID, w.prod, http.StatusOK)
	w.reconcile(t, prod.Metadata.ID)
	if r := w.platform.runningIn(w.staging); r.Spec.Release != "web-r2" || len(r.Spec.Overrides.Env) != 0 {
		t.Errorf("staging runs %+v", r.Spec)
	}
	if r := w.platform.runningIn(w.prod); r.Spec.Release != "web-r2" || r.Spec.Overrides.Env["MODE"] != "production" || fmt.Sprint(r.Spec.Overrides.Config["replicas"]) != "3" {
		t.Errorf("production after promotion runs %+v", r.Spec)
	}
	// Skipping staging isn't allowed.
	w.platform.newRelease()
	w.reconcile(t, dev.Metadata.ID)
	if body := w.call(t, person, http.MethodPost, "/v1/deployments/"+dev.Metadata.ID+"/promote", map[string]string{"to": w.prod}, http.StatusBadRequest); !strings.Contains(string(body), "pipeline") {
		t.Errorf("skipping staging: %s", body)
	}

	// Each Environment shows the release it runs and its state.
	var list []resource.Deployment
	json.Unmarshal(w.call(t, person, http.MethodGet, "/v1/deployments", nil, http.StatusOK), &list)
	want := map[string]string{w.dev: "web-r3", w.staging: "web-r2", w.prod: "web-r2"}
	for _, d := range list {
		env := d.Metadata.OwnerRefs[0].ID
		if d.Status.Release != want[env] || !ready(d) {
			t.Errorf("%s shows %q (ready %v), want %q", env, d.Status.Release, ready(d), want[env])
		}
	}

	// Rollback returns production to the earlier release, overrides kept.
	got := decodeDeployment(t, w.call(t, person, http.MethodGet, "/v1/deployments/"+prod.Metadata.ID, nil, http.StatusOK))
	if len(got.Status.History) == 0 || got.Status.History[0] != "web-r1" {
		t.Fatalf("production's history: %v", got.Status.History)
	}
	w.edit(t, person, prod.Metadata.ID, func(d *resource.Deployment) { d.Spec.Release = "web-r1" }, http.StatusOK)
	if got := w.reconcile(t, prod.Metadata.ID); got.Status.Release != "web-r1" || got.Status.History[0] != "web-r2" {
		t.Errorf("after rollback: %+v", got.Status)
	}
	if r := w.platform.runningIn(w.prod); r.Spec.Release != "web-r1" || r.Spec.Overrides.Env["MODE"] != "production" {
		t.Errorf("production after rollback runs %+v", r.Spec)
	}

	// Every step is audited, by the person, with the decision behind it:
	// deploy, two promotions creating, the override, two promotions
	// updating, the rollback.
	rows := deploymentAudit(t, w.db)
	if len(rows) != 7 {
		t.Errorf("audited %d changes by people, want 7: %v", len(rows), rows)
	}
	// A route's decision names the role and scope that allowed it; a
	// promotion also names its check at the target Environment.
	promotions := 0
	for _, r := range rows {
		if r.actor != "person:devops" || !strings.HasPrefix(r.decision, "allow: Contributor") {
			t.Errorf("audit row %+v", r)
		}
		if strings.Contains(r.decision, "allow serendip/deployments/write at Environment/") {
			promotions++
		}
	}
	if promotions != 4 {
		t.Errorf("%d audit rows name the promotion's check, want 4", promotions)
	}
}

type auditRow struct{ actor, decision string }

// deploymentAudit lists the Deployment changes made through the API, oldest first.
func deploymentAudit(t *testing.T, db *sql.DB) []auditRow {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT actor, COALESCE(policy_decision, '') FROM audit_log WHERE kind = 'Deployment' AND interface = ? ORDER BY at`, api.InterfaceName)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.actor, &r.decision); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// TestSpec020Criterion3_PolicyOnPromotion: a Reader can't promote; a role
// that allows it promotes at once; an agent's promotion takes the same
// policy path.
func TestSpec020Criterion3_PolicyOnPromotion(t *testing.T) {
	withAgentTokens(t)
	w := newWorld020(t, nil)
	assign := func(agent, role string) {
		s := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](w.db, resource.KindRoleAssignment)
		_, err := s.Create(context.Background(), resource.RoleAssignment{Metadata: resource.Metadata{Name: "agent-grant"}, Spec: resource.RoleAssignmentSpec{
			Principal:        resource.PrincipalRef{Type: resource.PrincipalAgent, ID: agent, Sponsor: &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "sub-owner"}},
			RoleDefinitionID: role, Scope: resource.Scope{Kind: resource.KindProject, ID: w.proj}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	assign("am-dev/agent-1", policy.ContributorRoleID)
	assign("am-dev/agent-2", policy.ReaderRoleID)

	dev := decodeDeployment(t, w.call(t, "tok-devops", http.MethodPost, "/v1/deployments", w.deployment(w.dev, ""), http.StatusCreated))
	w.reconcile(t, dev.Metadata.ID)

	body := w.call(t, "tok-reader", http.MethodPost, "/v1/deployments/"+dev.Metadata.ID+"/promote", map[string]string{"to": w.staging}, http.StatusForbidden)
	if !strings.Contains(string(body), "serendip/deployments/write") {
		t.Errorf("the Reader's refusal doesn't name the action: %s", body)
	}
	w.call(t, "tok-agent-bare", http.MethodPost, "/v1/deployments/"+dev.Metadata.ID+"/promote", map[string]string{"to": w.staging}, http.StatusForbidden)

	// The Contributor agent promotes at once, no approval: it's not a delete.
	staging := w.promote(t, "tok-agent", dev.Metadata.ID, w.staging, http.StatusCreated)
	if got := w.reconcile(t, staging.Metadata.ID); got.Status.Release != "web-r1" {
		t.Errorf("staging after the agent's promotion: %+v", got.Status)
	}
	var actor, sponsor, decision string
	err := w.db.QueryRowContext(context.Background(),
		`SELECT actor, COALESCE(actor_sponsor, ''), COALESCE(policy_decision, '') FROM audit_log WHERE resource_id = ? AND interface = ?`,
		staging.Metadata.ID, api.InterfaceName).Scan(&actor, &sponsor, &decision)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(actor, "agent:") || sponsor == "" || !strings.Contains(decision, "serendip/deployments/write") {
		t.Errorf("the agent's promotion audited as %q, sponsor %q, decision %q", actor, sponsor, decision)
	}
}

// TestSpec020Criterion4_Configuration: a trait's parameters are set against
// its schema, an invalid value is refused with the schema's message, and
// the change reaches the running workload.
func TestSpec020Criterion4_Configuration(t *testing.T) {
	w := newWorld020(t, nil)
	dev := decodeDeployment(t, w.call(t, "tok-devops", http.MethodPost, "/v1/deployments", w.deployment(w.dev, ""), http.StatusCreated))
	w.reconcile(t, dev.Metadata.ID)

	// The form's source: the schemas the platform publishes.
	var schemas struct {
		Traits map[string]struct {
			Properties map[string]map[string]any `json:"properties"`
		} `json:"traits"`
	}
	json.Unmarshal(w.call(t, "tok-devops", http.MethodGet, "/v1/components/"+w.web+"/schemas", nil, http.StatusOK), &schemas)
	if schemas.Traits["autoscale"].Properties["maxReplicas"]["maximum"] != float64(5) {
		t.Fatalf("schemas: %+v", schemas)
	}

	set := func(max int) func(*resource.Deployment) {
		return func(d *resource.Deployment) {
			d.Spec.Overrides.Traits = map[string]map[string]any{"autoscale": {"maxReplicas": max}}
		}
	}
	body := w.edit(t, "tok-devops", dev.Metadata.ID, set(9), http.StatusBadRequest)
	if !strings.Contains(string(body), "overrides.traits.autoscale.maxReplicas: must be at most 5") {
		t.Errorf("refusal: %s", body)
	}
	w.edit(t, "tok-devops", dev.Metadata.ID, set(4), http.StatusOK)
	w.reconcile(t, dev.Metadata.ID)

	var objects struct{ Objects []reconcile.RuntimeObject }
	json.Unmarshal(w.call(t, "tok-devops", http.MethodGet, "/v1/deployments/"+dev.Metadata.ID+"/runtime", nil, http.StatusOK), &objects)
	if len(objects.Objects) != 1 || !strings.Contains(objects.Objects[0].Message, "autoscale max 4") {
		t.Errorf("the running workload: %+v", objects.Objects)
	}
}

// TestSpec020Criterion5_Topology: the diagram's data has the Components and
// their declared dependencies, and each Component lists what it calls and
// what calls it.
func TestSpec020Criterion5_Topology(t *testing.T) {
	w := newWorld020(t, nil)
	apiComp := map[string]any{
		"metadata": map[string]any{"name": "cart", "ownerRefs": []any{map[string]any{"kind": "Application", "id": w.app}}},
		"spec":     map[string]any{"image": "cart:1", "ports": []any{map[string]any{"name": "http", "port": 8080}}},
	}
	var cart resource.Component
	json.Unmarshal(w.call(t, "tok-devops", http.MethodPost, "/v1/components", apiComp, http.StatusCreated), &cart)
	var web resource.Component
	json.Unmarshal(w.call(t, "tok-devops", http.MethodGet, "/v1/components/"+w.web, nil, http.StatusOK), &web)
	web.Spec.Dependencies = []resource.ComponentDependency{{Component: cart.Metadata.ID, Port: "http", Env: "CART_URL"}}
	w.call(t, "tok-devops", http.MethodPut, "/v1/components/"+w.web, web, http.StatusOK)

	var topo struct {
		Components   []struct{ ID, Name string }
		Dependencies []struct{ From, To, Port, Env string }
	}
	json.Unmarshal(w.call(t, "tok-reader", http.MethodGet, "/v1/applications/"+w.app+"/topology", nil, http.StatusOK), &topo)
	if len(topo.Components) != 2 || len(topo.Dependencies) != 1 || topo.Dependencies[0].From != w.web || topo.Dependencies[0].To != cart.Metadata.ID || topo.Dependencies[0].Env != "CART_URL" {
		t.Errorf("topology: %+v", topo)
	}
	var deps struct{ Calls, CalledBy []struct{ From, To string } }
	json.Unmarshal(w.call(t, "tok-reader", http.MethodGet, "/v1/components/"+w.web+"/dependencies", nil, http.StatusOK), &deps)
	if len(deps.Calls) != 1 || deps.Calls[0].To != cart.Metadata.ID || len(deps.CalledBy) != 0 {
		t.Errorf("web's dependencies: %+v", deps)
	}
	json.Unmarshal(w.call(t, "tok-reader", http.MethodGet, "/v1/components/"+cart.Metadata.ID+"/dependencies", nil, http.StatusOK), &deps)
	if len(deps.Calls) != 0 || len(deps.CalledBy) != 1 || deps.CalledBy[0].From != w.web {
		t.Errorf("cart's dependencies: %+v", deps)
	}
}

// TestSpec020Criterion6_PlatformView: data planes, environments, the
// pipeline, component types and traits, read-only.
func TestSpec020Criterion6_PlatformView(t *testing.T) {
	w := newWorld020(t, nil)
	var view reconcile.PlatformView
	json.Unmarshal(w.call(t, "tok-owner", http.MethodGet, "/v1/platform", nil, http.StatusOK), &view)
	if len(view.Runtimes) != 1 || !view.Runtimes[0].Ready || len(view.ComponentTypes) != 1 || view.ComponentTypes[0].AllowedTraits[0] != "autoscale" || len(view.Traits) != 1 {
		t.Errorf("platform: %+v", view)
	}
	var envs []resource.Environment
	json.Unmarshal(w.call(t, "tok-owner", http.MethodGet, "/v1/environments?ownerId="+w.proj, nil, http.StatusOK), &envs)
	if len(envs) != 3 {
		t.Errorf("environments: %d", len(envs))
	}
	var proj resource.Project
	json.Unmarshal(w.call(t, "tok-owner", http.MethodGet, "/v1/projects/"+w.proj, nil, http.StatusOK), &proj)
	if !proj.Spec.Pipeline.Allows(w.dev, w.staging) || !proj.Spec.Pipeline.Allows(w.staging, w.prod) || proj.Spec.Pipeline.First() != w.dev {
		t.Errorf("pipeline: %+v", proj.Spec.Pipeline)
	}
	// Nothing changes the platform through Serendip.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if got := w.do("tok-owner", method, "/v1/platform", map[string]any{}); got.Code < 400 {
			t.Errorf("%s /v1/platform: %d", method, got.Code)
		}
	}
}

// gatedInstaller installs a package that becomes ready only once ready is
// set, so "installing" can be observed.
type gatedInstaller struct {
	mu    sync.Mutex
	ready bool
}

func (g *gatedInstaller) Install(context.Context, resource.ProviderInstance) (reconcile.PackageState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return reconcile.PackageState{Ready: g.ready, Message: "starting"}, nil
}
func (g *gatedInstaller) Uninstall(context.Context, string) error { return nil }

// TestSpec020Criterion7_UnavailableHonestly: without the packages, their
// capabilities say so (the console greys out their sections from this,
// naming the package; ui/src/screens/platform.test.tsx), and nothing else
// errors. While a package installs it reads Installing, not unavailable.
func TestSpec020Criterion7_UnavailableHonestly(t *testing.T) {
	registry := provider.NewRegistry()
	builder := &gatedInstaller{}
	cat := reconcile.Catalogue{
		"test-builder": {Provider: "test-builder", Capability: "build", Installer: builder, Requirements: measured},
		"observer":     {Provider: "observer", Capability: "observability"}, // not in this build
	}
	w := newWorld020(t, registry, api.WithPackages(cat))
	instances := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](w.db, resource.KindProviderInstance)
	report := func(capability string, ready bool, reason, message string) {
		registry.SetReadiness(capability, provider.Readiness{Ready: ready, Reason: reason, Message: message})
	}
	recon := reconcile.NewProviderInstanceReconciler(instances, cat, report)
	if err := recon.ReportStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	readiness := func() map[string]provider.Readiness {
		var out map[string]provider.Readiness
		json.Unmarshal(w.call(t, "tok-owner", http.MethodGet, "/v1/readiness", nil, http.StatusOK), &out)
		return out
	}
	r := readiness()
	if r["build"].Ready || r["build"].Reason != reconcile.ReasonNotInstalled || r["observability"].Reason != reconcile.ReasonUnavailable {
		t.Fatalf("readiness without packages: %+v", r)
	}
	var list struct {
		Packages []struct{ Provider, Capability string }
	}
	json.Unmarshal(w.call(t, "tok-owner", http.MethodGet, "/v1/packages", nil, http.StatusOK), &list)
	if len(list.Packages) != 2 {
		t.Errorf("packages name what's missing: %+v", list)
	}

	// Without either package, deploying and the runtime view still work.
	dev := decodeDeployment(t, w.call(t, "tok-devops", http.MethodPost, "/v1/deployments", w.deployment(w.dev, ""), http.StatusCreated))
	w.reconcile(t, dev.Metadata.ID)
	w.call(t, "tok-logreader", http.MethodGet, "/v1/deployments/"+dev.Metadata.ID+"/runtime", nil, http.StatusOK)
	w.call(t, "tok-logreader", http.MethodGet, "/v1/deployments/"+dev.Metadata.ID+"/logs", nil, http.StatusOK)

	var inst resource.ProviderInstance
	json.Unmarshal(w.call(t, "tok-owner", http.MethodPost, "/v1/provider-instances", install("test-builder", "build"), http.StatusCreated), &inst)
	ref := graph.ResourceRef{Kind: resource.KindProviderInstance, ID: inst.Metadata.ID}
	ctx := audit.WithActor(audit.WithInterface(context.Background(), reconcile.InterfaceName), reconcile.ActorName)
	recon.Reconcile(ctx, ref) // not ready yet: asks to be requeued
	if r := readiness()["build"]; r.Ready || r.Reason != reconcile.ReasonInstalling {
		t.Errorf("while installing: %+v", r)
	}
	builder.mu.Lock()
	builder.ready = true
	builder.mu.Unlock()
	if err := recon.Reconcile(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if r := readiness()["build"]; !r.Ready {
		t.Errorf("once installed: %+v", r)
	}
}

// TestSpec020Criterion10_Authorization: logs need their own action, and
// packages need platform-level actions. (That every route has a rule is
// TestEveryRouteHasARule; each route's own refusals are in its handler's
// tests.)
func TestSpec020Criterion10_Authorization(t *testing.T) {
	w := newWorld020(t, nil, api.WithPackages(testCatalogue()))
	dev := decodeDeployment(t, w.call(t, "tok-devops", http.MethodPost, "/v1/deployments", w.deployment(w.dev, ""), http.StatusCreated))
	base := "/v1/deployments/" + dev.Metadata.ID

	// A Reader sees the runtime but not the logs; a Log Reader sees both.
	w.call(t, "tok-reader", http.MethodGet, base+"/runtime", nil, http.StatusOK)
	if body := w.call(t, "tok-reader", http.MethodGet, base+"/logs", nil, http.StatusForbidden); !strings.Contains(string(body), "serendip/components/logs/read") {
		t.Errorf("the Reader's log refusal: %s", body)
	}
	w.call(t, "tok-logreader", http.MethodGet, base+"/logs", nil, http.StatusOK)

	// Installing and removing a package are decided at "/": a Project's
	// Contributor can't, the platform's Owner can.
	if body := w.call(t, "tok-devops", http.MethodPost, "/v1/provider-instances", install("test-builder", "build"), http.StatusForbidden); !strings.Contains(string(body), "serendip/provider-instances/write") {
		t.Errorf("the Contributor's install refusal: %s", body)
	}
	var inst resource.ProviderInstance
	json.Unmarshal(w.call(t, "tok-owner", http.MethodPost, "/v1/provider-instances", install("test-builder", "build"), http.StatusCreated), &inst)
	if body := w.call(t, "tok-devops", http.MethodDelete, "/v1/provider-instances/"+inst.Metadata.ID, nil, http.StatusForbidden); !strings.Contains(string(body), "serendip/provider-instances/delete") {
		t.Errorf("the Contributor's removal refusal: %s", body)
	}
	got := w.do("tok-owner", http.MethodDelete, "/v1/provider-instances/"+inst.Metadata.ID, nil)
	if got.Code != http.StatusNoContent || got.Header().Get("Serendip-Approval-Id") == "" {
		t.Errorf("the Owner's removal: %d, approval %q", got.Code, got.Header().Get("Serendip-Approval-Id"))
	}
}

// TestSpec020Criterion11_InterfaceParity: every route spec 020 added is a
// CLI command, and an MCP tool unless it's left off with a reason. (The
// console's request panels are checked by its own tests.)
func TestSpec020Criterion11_InterfaceParity(t *testing.T) {
	routes := []string{
		"GET /v1/deployments", "POST /v1/deployments", "GET /v1/deployments/{id}", "PUT /v1/deployments/{id}", "DELETE /v1/deployments/{id}",
		"POST /v1/deployments/{id}/promote",
		"GET /v1/deployments/{id}/runtime", "GET /v1/deployments/{id}/events", "GET /v1/deployments/{id}/logs",
		"GET /v1/components/{id}/schemas", "GET /v1/components/{id}/dependencies", "GET /v1/applications/{id}/topology",
		"GET /v1/platform", "GET /v1/packages",
		"POST /v1/provider-instances", "DELETE /v1/provider-instances/{id}",
	}
	ops := map[string]catalog.Operation{}
	for _, op := range catalog.Operations() {
		ops[op.Method+" "+op.Path] = op
	}
	excluded := catalog.Excluded()
	for _, r := range routes {
		op, ok := ops[r]
		if !ok {
			t.Errorf("%s: no catalogue operation", r)
			continue
		}
		if len(op.CLI) == 0 && excluded[r].CLI == "" {
			t.Errorf("%s: no CLI command and no reason", r)
		}
		if !op.MCP && excluded[r].MCP == "" {
			t.Errorf("%s: no MCP tool and no reason", r)
		}
	}
	// Following logs is the CLI's alone, with its reason.
	logs := ops["GET /v1/deployments/{id}/logs"]
	for _, p := range logs.Params {
		if p.Name == "follow" && p.CLIOnly == "" {
			t.Error("follow is offered to MCP")
		}
	}
}
