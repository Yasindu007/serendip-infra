package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// fakeBuilds is a build capability whose runs all exist, until gone says
// otherwise.
type fakeBuilds struct {
	mu   sync.Mutex
	gone map[string]bool
	// expireOnRead makes the next logs or events read find the run just
	// removed, as the platform's clean-up would between two calls.
	expireOnRead bool
	// failRead fails the next read with the run still there.
	failRead bool
}

// read fails the read and expires the run when expireOnRead is set.
func (f *fakeBuilds) read(b resource.Build) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failRead {
		f.failRead = false
		return errors.New("connection refused")
	}
	if !f.expireOnRead {
		return nil
	}
	f.expireOnRead, f.gone[b.Metadata.ID] = false, true
	return errors.New("workflowruns.openchoreo.dev \"build-1\" not found")
}

func (f *fakeBuilds) StartBuild(context.Context, resource.Application, resource.Component, resource.Build, map[string]any) error {
	return nil
}
func (f *fakeBuilds) ObserveBuild(_ context.Context, b resource.Build) (reconcile.BuildObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return reconcile.BuildObservation{Phase: b.Status.Phase, Gone: f.gone[b.Metadata.ID]}, nil
}
func (f *fakeBuilds) CancelBuild(context.Context, resource.Build) error { return nil }
func (f *fakeBuilds) RemoveBuild(context.Context, resource.Build) error { return nil }
func (f *fakeBuilds) BuildLogs(_ context.Context, b resource.Build, opts reconcile.LogOptions) (reconcile.LogStream, error) {
	if err := f.read(b); err != nil {
		return nil, err
	}
	return &sliceStream{lines: []reconcile.LogLine{{Time: time.Now(), Instance: "build", Line: "STEP 1/3: FROM alpine"}}}, nil
}
func (f *fakeBuilds) BuildEvents(_ context.Context, b resource.Build, _ time.Time) ([]reconcile.RuntimeEvent, error) {
	if err := f.read(b); err != nil {
		return nil, err
	}
	return []reconcile.RuntimeEvent{{Type: "Normal", Reason: "Pulled", ObjectKind: "Pod", ObjectName: "build-1"}}, nil
}
func (f *fakeBuilds) HeadCommit(context.Context, resource.ComponentSource, map[string]any) (string, error) {
	return strings.Repeat("a", 40), nil
}

const buildPackage = "test-builder"

// buildWorld is the deploy world with a source Component, a Secret beside
// it, the builds package's readiness and a fake build capability.
type buildWorld struct {
	*deployWorld
	registry *provider.Registry
	builds   *fakeBuilds
	store    *sqlite.Store[resource.BuildSpec, resource.BuildStatus]
	src      string // the source Component
	secret   string
}

func newBuildWorld(t *testing.T) *buildWorld {
	t.Helper()
	reg := provider.NewRegistry()
	reg.SetReadiness("build", provider.Readiness{Ready: true, Reason: "Ready"})
	fb := &fakeBuilds{gone: map[string]bool{}}
	d := newDeployWorldWith(t, reg, api.WithPackages(testCatalogue()), api.WithBuilds(fb))
	w := &buildWorld{deployWorld: d, registry: reg, builds: fb, store: sqlite.NewStore[resource.BuildSpec, resource.BuildStatus](d.db, resource.KindBuild)}
	ctx := context.Background()
	comp, err := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](d.db, resource.KindComponent).Create(ctx, resource.Component{
		Metadata: resource.Metadata{Name: "api", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: d.app}}},
		Spec:     resource.ComponentSpec{Source: &resource.ComponentSource{Repository: "https://example.com/shop/api"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.src = comp.Metadata.ID
	sec, err := sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](d.db, resource.KindSecret).Create(ctx, resource.Secret{
		Metadata: resource.Metadata{Name: "git", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: d.app}}},
		Spec:     resource.SecretSpec{Path: "shop/git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.secret = sec.Metadata.ID
	return w
}

func (w *buildWorld) call(t *testing.T, token, method, path string, body any, want int) []byte {
	t.Helper()
	got := w.do(token, method, path, body)
	if got.Code != want {
		t.Fatalf("%s %s %s: %d, want %d (%s)", people[token].Name, method, path, got.Code, want, strings.TrimSpace(got.Body.String()))
	}
	return got.Body.Bytes()
}

func decodeBuild(t *testing.T, body []byte) resource.Build {
	t.Helper()
	var b resource.Build
	if err := json.Unmarshal(body, &b); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return b
}

// assignRole gives a person a role through the API, so the policy index
// picks it up at once.
func (w *buildWorld) assignRole(t *testing.T, subject, role string, at resource.Scope) {
	t.Helper()
	w.call(t, "tok-owner", http.MethodPost, authzPath+"roleAssignments", assignment(resource.PrincipalPerson, subject, role, at), http.StatusCreated)
}

// setStatus writes a Build's status as the build reconciler would.
func (w *buildWorld) setStatus(t *testing.T, id string, phase resource.BuildPhase, started bool) {
	t.Helper()
	b, err := w.store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	b.Status.Phase, b.Status.Started = phase, started
	if _, err := w.store.Update(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

// TestBuildRoutes covers the build routes' authorization (plan decision
// 11): a Reader sees runs but not their logs; Log Reader reads logs; only a
// writer triggers and cancels, and both are audited as the caller.
func TestBuildRoutes(t *testing.T) {
	w := newBuildWorld(t)
	base := "/v1/components/" + w.src + "/builds"

	commit := strings.Repeat("b", 40)
	b := decodeBuild(t, w.call(t, "tok-devops", http.MethodPost, base, map[string]string{"commit": commit}, http.StatusCreated))
	if b.Spec.Commit != commit || b.Metadata.Name != "build-"+commit[:12] || resource.BuildComponent(b.Metadata) != w.src {
		t.Errorf("triggered build: %+v", b)
	}
	head := decodeBuild(t, w.call(t, "tok-devops", http.MethodPost, base, nil, http.StatusCreated))
	if head.Spec.Commit != "" || !strings.HasPrefix(head.Metadata.Name, "build-20") {
		t.Errorf("build of the head: %+v", head)
	}

	// Reader: runs yes, logs no, no trigger or cancel.
	var list []resource.Build
	json.Unmarshal(w.call(t, "tok-reader", http.MethodGet, base, nil, http.StatusOK), &list)
	if len(list) != 2 || list[0].Metadata.ID != head.Metadata.ID {
		t.Errorf("runs, newest first: %+v", list)
	}
	w.call(t, "tok-reader", http.MethodGet, "/v1/builds/"+b.Metadata.ID, nil, http.StatusOK)
	w.setStatus(t, b.Metadata.ID, resource.BuildRunning, true)
	w.call(t, "tok-reader", http.MethodGet, "/v1/builds/"+b.Metadata.ID+"/events", nil, http.StatusOK)
	if body := w.call(t, "tok-reader", http.MethodGet, "/v1/builds/"+b.Metadata.ID+"/logs", nil, http.StatusForbidden); !strings.Contains(string(body), policy.ActionComponentLogsRead) {
		t.Errorf("a Reader's logs refusal: %s", body)
	}
	w.call(t, "tok-reader", http.MethodPost, base, nil, http.StatusForbidden)
	w.call(t, "tok-reader", http.MethodPost, "/v1/builds/"+b.Metadata.ID+"/cancel", nil, http.StatusForbidden)

	// Log Reader reads them.
	if body := w.call(t, "tok-logreader", http.MethodGet, "/v1/builds/"+b.Metadata.ID+"/logs", nil, http.StatusOK); !strings.Contains(string(body), "FROM alpine") {
		t.Errorf("logs: %s", body)
	}

	// Someone with no role on the Project sees none of it.
	w.call(t, "tok-nobody", http.MethodGet, base, nil, http.StatusForbidden)
	w.call(t, "tok-nobody", http.MethodGet, "/v1/builds/"+b.Metadata.ID, nil, http.StatusForbidden)

	// Cancel: asked once, idempotent, refused once finished.
	got := decodeBuild(t, w.call(t, "tok-devops", http.MethodPost, "/v1/builds/"+b.Metadata.ID+"/cancel", nil, http.StatusOK))
	if !got.Spec.Cancel {
		t.Errorf("cancelled build: %+v", got.Spec)
	}
	w.call(t, "tok-devops", http.MethodPost, "/v1/builds/"+b.Metadata.ID+"/cancel", nil, http.StatusOK)
	w.setStatus(t, b.Metadata.ID, resource.BuildCancelled, true)
	w.call(t, "tok-devops", http.MethodPost, "/v1/builds/"+b.Metadata.ID+"/cancel", nil, http.StatusConflict)

	// Audit: the trigger and the cancel, each as the person, with the
	// decision that allowed it.
	var n int
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE resource_id = ? AND actor = 'person:devops' AND policy_decision LIKE 'allow: Contributor%'`, b.Metadata.ID).Scan(&n); err != nil || n != 2 {
		t.Errorf("audit rows for the trigger and the cancel: %d (%v)", n, err)
	}
	// A refused trigger is audited too.
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE actor = 'person:reader' AND policy_decision LIKE 'deny:%'`).Scan(&n); err != nil || n != 2 {
		t.Errorf("audit rows for the refused trigger and cancel: %d (%v)", n, err)
	}
}

func TestBuildTriggerRefusals(t *testing.T) {
	w := newBuildWorld(t)
	base := "/v1/components/" + w.src + "/builds"

	if body := w.call(t, "tok-devops", http.MethodPost, "/v1/components/"+w.web+"/builds", nil, http.StatusConflict); !strings.Contains(string(body), "runs an image") {
		t.Errorf("an image Component's build: %s", body)
	}
	w.call(t, "tok-devops", http.MethodPost, base, map[string]string{"commit": "abc123"}, http.StatusBadRequest)
	w.call(t, "tok-devops", http.MethodPost, base, map[string]string{"commit": strings.Repeat("c", 40), "image": "x"}, http.StatusCreated)
	w.call(t, "tok-owner", http.MethodPost, "/v1/components/missing/builds", nil, http.StatusNotFound)

	w.registry.SetReadiness("build", provider.Readiness{Ready: false, Reason: "Installing"})
	if body := w.call(t, "tok-devops", http.MethodPost, base, nil, http.StatusUnprocessableEntity); !strings.Contains(string(body), buildPackage) || !strings.Contains(string(body), "Installing") {
		t.Errorf("a build while the package installs: %s", body)
	}
}

// TestBuildLogsExpired: a run's logs and events go an hour after it ends,
// and a queued build has none yet.
func TestBuildLogsExpired(t *testing.T) {
	w := newBuildWorld(t)
	b := decodeBuild(t, w.call(t, "tok-devops", http.MethodPost, "/v1/components/"+w.src+"/builds", nil, http.StatusCreated))
	logs := "/v1/builds/" + b.Metadata.ID + "/logs"

	w.call(t, "tok-logreader", http.MethodGet, logs, nil, http.StatusConflict)
	w.call(t, "tok-logreader", http.MethodGet, logs+"?follow=true", nil, http.StatusConflict)

	w.setStatus(t, b.Metadata.ID, resource.BuildSucceeded, true)
	w.call(t, "tok-logreader", http.MethodGet, logs, nil, http.StatusOK)

	// Following, over a real connection, ends with the run.
	srv := httptest.NewServer(w.h)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+logs+"?follow=true", nil)
	req.Header.Set("Authorization", "Bearer tok-logreader")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	streamed, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(streamed), "FROM alpine") || !strings.HasSuffix(string(streamed), `{"end":"ended"}`+"\n") {
		t.Errorf("followed build logs: %d %q", res.StatusCode, streamed)
	}

	// The run expires between the check and the read: still 410, not 502,
	// for logs, followed logs and events.
	for _, path := range []string{logs, logs + "?follow=true", "/v1/builds/" + b.Metadata.ID + "/events"} {
		w.builds.mu.Lock()
		w.builds.expireOnRead, w.builds.gone[b.Metadata.ID] = true, false
		w.builds.mu.Unlock()
		if path == logs+"?follow=true" {
			req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
			req.Header.Set("Authorization", "Bearer tok-logreader")
			res, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != http.StatusGone {
				t.Errorf("follow, expiring mid-request: %d", res.StatusCode)
			}
			continue
		}
		w.call(t, "tok-logreader", http.MethodGet, path, nil, http.StatusGone)
	}
	// A read failing for another reason is still 502.
	w.builds.mu.Lock()
	w.builds.gone[b.Metadata.ID], w.builds.failRead = false, true
	w.builds.mu.Unlock()
	w.call(t, "tok-logreader", http.MethodGet, logs, nil, http.StatusBadGateway)

	w.builds.mu.Lock()
	w.builds.gone[b.Metadata.ID] = true
	w.builds.mu.Unlock()
	if body := w.call(t, "tok-logreader", http.MethodGet, logs, nil, http.StatusGone); !strings.Contains(string(body), "expired") {
		t.Errorf("expired logs: %s", body)
	}
	w.call(t, "tok-logreader", http.MethodGet, "/v1/builds/"+b.Metadata.ID+"/events", nil, http.StatusGone)
	// The record keeps its outcome.
	if got := decodeBuild(t, w.call(t, "tok-reader", http.MethodGet, "/v1/builds/"+b.Metadata.ID, nil, http.StatusOK)); got.Status.Phase != resource.BuildSucceeded {
		t.Errorf("expired build's record: %+v", got.Status)
	}
	w.call(t, "tok-logreader", http.MethodGet, logs+"?tailLines=0", nil, http.StatusBadRequest)
}

// TestBuildLogsWithoutPackage: runs are records, so they read without the
// builds package; logs and events need it.
func TestBuildLogsWithoutPackage(t *testing.T) {
	d := newDeployWorld(t)
	builds := sqlite.NewStore[resource.BuildSpec, resource.BuildStatus](d.db, resource.KindBuild)
	b, err := builds.Create(context.Background(), resource.Build{Metadata: resource.Metadata{Name: "old",
		OwnerRefs: []resource.OwnerRef{{Kind: resource.KindComponent, ID: d.web}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.do("tok-reader", http.MethodGet, "/v1/builds/"+b.Metadata.ID, nil); got.Code != http.StatusOK {
		t.Errorf("a build's record without the package: %d %s", got.Code, got.Body)
	}
	if got := d.do("tok-logreader", http.MethodGet, "/v1/builds/"+b.Metadata.ID+"/logs", nil); got.Code != http.StatusServiceUnavailable {
		t.Errorf("logs without the package: %d %s", got.Code, got.Body)
	}
	if got := d.do("tok-devops", http.MethodPost, "/v1/components/"+d.web+"/builds", nil); got.Code != http.StatusConflict {
		t.Errorf("trigger on an image Component: %d %s", got.Code, got.Body)
	}
}

func sourceComponent(app string, src map[string]any) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": "from-git", "ownerRefs": []any{map[string]any{"kind": "Application", "id": app}}},
		"spec":     map[string]any{"source": src},
	}
}

// TestComponentSource: a source Component needs the builds package ready
// (422, naming it), and naming a credential needs reading its value.
func TestComponentSource(t *testing.T) {
	w := newBuildWorld(t)
	repo := "https://example.com/shop/web"

	w.registry.SetReadiness("build", provider.Readiness{Ready: false, Reason: "NotInstalled"})
	if body := w.call(t, "tok-devops", http.MethodPost, "/v1/components", sourceComponent(w.app, map[string]any{"repository": repo}), http.StatusUnprocessableEntity); !strings.Contains(string(body), buildPackage) {
		t.Errorf("a source Component without the package: %s", body)
	}
	// An image Component doesn't need it.
	img := sourceComponent(w.app, nil)
	img["spec"] = map[string]any{"image": "nginx"}
	w.call(t, "tok-devops", http.MethodPost, "/v1/components", img, http.StatusCreated)

	w.registry.SetReadiness("build", provider.Readiness{Ready: true, Reason: "Ready"})
	w.call(t, "tok-devops", http.MethodPost, "/v1/components", sourceComponent(w.app, map[string]any{"repository": "http://example.com/x"}), http.StatusBadRequest)
	c := w.call(t, "tok-devops", http.MethodPost, "/v1/components", sourceComponent(w.app, map[string]any{"repository": repo}), http.StatusCreated)
	var comp resource.Component
	json.Unmarshal(c, &comp)

	// Editing anything but the source works while the package is down.
	w.registry.SetReadiness("build", provider.Readiness{Ready: false, Reason: "Installing"})
	comp.Spec.Ports = []resource.ComponentPort{{Name: "http", Port: 8080}}
	c = w.call(t, "tok-devops", http.MethodPut, "/v1/components/"+comp.Metadata.ID, comp, http.StatusOK)
	json.Unmarshal(c, &comp)
	comp.Spec.Source.Branch = "release"
	w.call(t, "tok-devops", http.MethodPut, "/v1/components/"+comp.Metadata.ID, comp, http.StatusUnprocessableEntity)
	w.registry.SetReadiness("build", provider.Readiness{Ready: true, Reason: "Ready"})

	// A Contributor can't point a build at a Secret whose value they can't
	// read; a Secrets User can.
	comp.Spec.Source.CredentialSecret = w.secret
	if body := w.call(t, "tok-devops", http.MethodPut, "/v1/components/"+comp.Metadata.ID, comp, http.StatusForbidden); !strings.Contains(string(body), "serendip/secrets/value/read") {
		t.Errorf("credential without its value: %s", body)
	}
	w.assignRole(t, "sub-devops", policy.SecretsUserRoleID, resource.Scope{Kind: resource.KindApplication, ID: w.app})
	c = w.call(t, "tok-devops", http.MethodPut, "/v1/components/"+comp.Metadata.ID, comp, http.StatusOK)
	json.Unmarshal(c, &comp)
	var decision string
	if err := w.db.QueryRow(`SELECT policy_decision FROM audit_log WHERE resource_id = ? ORDER BY at DESC LIMIT 1`, comp.Metadata.ID).Scan(&decision); err != nil || !strings.Contains(decision, "serendip/secrets/value/read") {
		t.Errorf("the credential check's evidence: %q (%v)", decision, err)
	}

	// Moving the credential to another repository is checked again.
	w.assignRole(t, "sub-reader", policy.ContributorRoleID, resource.Scope{Kind: resource.KindProject, ID: w.proj})
	comp.Spec.Source.Repository = "https://attacker.example/x"
	w.call(t, "tok-reader", http.MethodPut, "/v1/components/"+comp.Metadata.ID, comp, http.StatusForbidden)
	// Leaving it as it is isn't.
	comp.Spec.Source.Repository = repo
	comp.Spec.Source.AutoBuild = true
	w.call(t, "tok-reader", http.MethodPut, "/v1/components/"+comp.Metadata.ID, comp, http.StatusOK)

	// A Secret that doesn't exist, named by someone allowed values
	// everywhere.
	w.assignRole(t, "sub-owner", policy.SecretsUserRoleID, resource.RootScope)
	if body := w.call(t, "tok-owner", http.MethodPost, "/v1/components", sourceComponent(w.app, map[string]any{"repository": repo, "credentialSecret": "no-such-secret"}), http.StatusBadRequest); !strings.Contains(string(body), "not found") {
		t.Errorf("a missing credential Secret: %s", body)
	}
}

// TestPackagesStateCost: GET /v1/packages states each package's measured
// cost and what removing it loses, and says why an unmeasured one can't be
// installed (plan decisions 4 and 12).
func TestPackagesStateCost(t *testing.T) {
	cat := testCatalogue()
	p := cat["test-builder"]
	p.Removal = "Build history stays; run logs and built images go."
	cat["test-builder"] = p
	cat["later"] = reconcile.Package{Provider: "later", Capability: "observability", Installer: nopInstaller{}}
	d := newDeployWorld(t, api.WithPackages(cat))
	var list struct {
		Packages []struct {
			Provider       string
			Requirements   *provider.Requirements
			Removal        string
			Installable    bool
			NotInstallable string
		}
	}
	body := d.do("tok-owner", http.MethodGet, "/v1/packages", nil)
	if err := json.Unmarshal(body.Body.Bytes(), &list); err != nil || body.Code != http.StatusOK {
		t.Fatalf("packages: %d %s", body.Code, body.Body)
	}
	for _, pk := range list.Packages {
		switch pk.Provider {
		case "test-builder":
			if pk.Requirements == nil || pk.Requirements.PeakCPUMillicores != 1500 || pk.Removal == "" || !pk.Installable {
				t.Errorf("measured package: %+v", pk)
			}
		case "later":
			if pk.Requirements != nil || pk.Installable || !strings.Contains(pk.NotInstallable, "measured") {
				t.Errorf("unmeasured package: %+v", pk)
			}
		}
	}
	if !strings.Contains(body.Body.String(), `"requirements":null`) {
		t.Errorf("an unmeasured package's requirements should read null: %s", body.Body)
	}
}
