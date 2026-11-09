package openchoreo

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// fakeHelm records each helm invocation. onRun, when set, runs as the
// chart would: the tests use it to create the chart's Deployments.
type fakeHelm struct {
	calls [][]string
	err   error
	onRun func(args []string)
	// respond, when set, answers each call instead of err.
	respond func(args []string) ([]byte, error)
}

func (f *fakeHelm) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if f.respond != nil {
		out, err := f.respond(args)
		if err == nil && f.onRun != nil {
			f.onRun(args)
		}
		return out, err
	}
	if f.onRun != nil {
		f.onRun(args)
	}
	return nil, f.err
}

// fakeAPI is a minimal in-memory Kubernetes API keyed by item path. An
// apply stores the object (keeping any status already there), a DELETE
// removes it (or, for a path in finalizing, only marks it), and a GET of
// a collection lists the objects directly under it. calls records
// "METHOD path" with APPLY for server-side apply.
type fakeAPI struct {
	mu         sync.Mutex
	objects    map[string]kubernetes.Object
	calls      []string
	finalizing map[string]bool
	// queries is the raw query of the last request per "METHOD path".
	queries map[string]string
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{objects: map[string]kubernetes.Object{}, finalizing: map[string]bool{}, queries: map[string]string{}}
}

func (f *fakeAPI) put(path string, obj kubernetes.Object) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[path] = obj
}

func (f *fakeAPI) has(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[path]
	return ok
}

func (f *fakeAPI) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeAPI) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func (f *fakeAPI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.RawQuery != "" {
			f.queries[r.Method+" "+r.URL.Path] = r.URL.RawQuery
		}
		method := r.Method
		if method == http.MethodPatch && r.Header.Get("Content-Type") == "application/apply-patch+yaml" {
			method = "APPLY"
		}
		path := r.URL.Path
		f.calls = append(f.calls, method+" "+path)
		switch method {
		case "APPLY":
			var in kubernetes.Object
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Errorf("decode apply %s: %v", path, err)
			}
			if old, ok := f.objects[path]; ok {
				in.Status = old.Status
			}
			f.objects[path] = in
			json.NewEncoder(w).Encode(in)
		case http.MethodGet:
			if obj, ok := f.objects[path]; ok {
				json.NewEncoder(w).Encode(obj)
				return
			}
			items := []kubernetes.Object{}
			listed := false
			for p, obj := range f.objects {
				if rest, ok := strings.CutPrefix(p, path+"/"); ok && !strings.Contains(rest, "/") {
					items = append(items, obj)
					listed = true
				}
			}
			if !listed && !strings.HasSuffix(path, "/pods") && path != workflowRunsPath {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"items": items})
		case http.MethodDelete:
			if r.URL.RawQuery != "" { // a delete by label: the collection
				w.WriteHeader(http.StatusOK)
				return
			}
			if _, ok := f.objects[path]; !ok {
				// A collection's delete removes what's directly under it.
				found := false
				for p := range f.objects {
					if rest, ok := strings.CutPrefix(p, path+"/"); ok && !strings.Contains(rest, "/") {
						found = true
						if !f.finalizing[p] {
							delete(f.objects, p)
						}
					}
				}
				if !found {
					http.NotFound(w, r)
				}
				return
			}
			if !f.finalizing[path] {
				delete(f.objects, path)
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodPost: // a create into the collection
			var in kubernetes.Object
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Errorf("decode create %s: %v", path, err)
			}
			item := path + "/" + in.Metadata.Name
			if _, ok := f.objects[item]; ok {
				w.WriteHeader(http.StatusConflict)
				return
			}
			f.objects[item] = in
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(in)
		default:
			t.Errorf("unexpected %s %s", r.Method, path)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func newTestKubeClient(t *testing.T, handler http.HandlerFunc) *kubernetes.Client {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	client, err := kubernetes.NewClient(kubernetes.Config{Host: srv.URL, CAData: caPEM})
	if err != nil {
		t.Fatalf("NewClient() = %v", err)
	}
	return client
}

var (
	caSecretPath   = "/api/v1/namespaces/openchoreo-control-plane/secrets/cluster-gateway-ca"
	caConfigPath   = "/api/v1/namespaces/openchoreo-workflow-plane/configmaps/cluster-gateway-ca"
	argoPath       = "/apis/apps/v1/namespaces/openchoreo-workflow-plane/deployments/argo-workflow-controller"
	agentPath      = "/apis/apps/v1/namespaces/openchoreo-workflow-plane/deployments/cluster-agent-workflowplane"
	registryPath   = "/apis/apps/v1/namespaces/serendip-builds/deployments/serendip-registry"
	registryPVC    = "/api/v1/namespaces/serendip-builds/persistentvolumeclaims/serendip-registry"
	registrySvc    = "/api/v1/namespaces/serendip-builds/services/registry"
	planePath      = "/apis/openchoreo.dev/v1alpha1/clusterworkflowplanes/default"
	workflowPath   = "/apis/openchoreo.dev/v1alpha1/clusterworkflows/serendip-dockerfile"
	readyStatus    = json.RawMessage(`{"readyReplicas":1}`)
	connectedPlane = json.RawMessage(`{"agentConnection":{"connected":true,"connectedAgents":1}}`)
)

// testEnv is an installer over a fake API holding the control plane's CA,
// with a fake helm that creates the chart's two ready Deployments.
type testEnv struct {
	api  *fakeAPI
	helm *fakeHelm
	inst *Installer
	now  time.Time
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	e := &testEnv{api: newFakeAPI(), now: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)}
	e.api.put(caSecretPath, kubernetes.Object{APIVersion: "v1", Kind: "Secret",
		Data: json.RawMessage(`{"ca.crt":"LS0tLUNBLS0tLQ=="}`)}) // "----CA----"
	e.helm = &fakeHelm{onRun: func(args []string) {
		if args[0] == "upgrade" {
			e.api.put(argoPath, kubernetes.Object{Kind: "Deployment", Status: readyStatus})
			e.api.put(agentPath, kubernetes.Object{Kind: "Deployment", Status: readyStatus})
		}
	}}
	e.inst = newInstaller(e.helm.run, newTestKubeClient(t, e.api.handler(t)))
	e.inst.now = func() time.Time { return e.now }
	return e
}

// healthy makes every quick check pass.
func (e *testEnv) healthy() {
	e.api.put(argoPath, kubernetes.Object{Kind: "Deployment", Status: readyStatus})
	e.api.put(agentPath, kubernetes.Object{Kind: "Deployment", Status: readyStatus})
	e.api.put(registryPath, kubernetes.Object{Kind: "Deployment", Status: readyStatus})
	e.api.put(planePath, kubernetes.Object{Kind: "ClusterWorkflowPlane", Status: connectedPlane})
	e.api.put(workflowPath, kubernetes.Object{Kind: "ClusterWorkflow"})
}

func instance(applied string) resource.ProviderInstance {
	var inst resource.ProviderInstance
	inst.Metadata.ID = "pi-1"
	inst.Status.Applied = applied
	return inst
}

func TestInstall_FirstInstallRunsHelmOnceAndAppliesEverything(t *testing.T) {
	e := newTestEnv(t)
	state, err := e.inst.Install(context.Background(), instance(""))
	if err != nil {
		t.Fatalf("Install() = %v", err)
	}
	if len(e.helm.calls) != 1 {
		t.Fatalf("helm calls = %v, want 1", e.helm.calls)
	}
	want := []string{"upgrade", "--install", "openchoreo-workflow-plane", workflowPlaneChart, "--version", "1.3.1",
		"--namespace", "openchoreo-workflow-plane", "--create-namespace",
		"--set", "argo-workflows.crds.full=false", "--set", "argo-workflows.crds.keep=true",
		"--wait", "--timeout", "10m"}
	if !slices.Equal(e.helm.calls[0], want) {
		t.Errorf("helm args = %v\nwant %v", e.helm.calls[0], want)
	}
	// The registry isn't ready and the agent not connected yet, so it
	// settles; what was applied is reported so the next poll only checks.
	if state.Ready || state.Applied != Fingerprint() || state.Message != "waiting for the build registry to be ready" {
		t.Errorf("state = %+v", state)
	}
	for _, p := range []string{caConfigPath, planePath, registryPVC, registryPath, registrySvc, workflowPath} {
		if !e.api.has(p) {
			t.Errorf("%s wasn't applied", p)
		}
	}
	// The CA trust copy happens before the chart, and the workflow last.
	order := e.api.calls
	idx := func(call string) int { return slices.Index(order, call) }
	if idx("APPLY "+caConfigPath) < 0 || idx("APPLY "+planePath) > idx("APPLY "+registryPath) || idx("APPLY "+registryPath) > idx("APPLY "+workflowPath) {
		t.Errorf("apply order = %v", order)
	}
	var cm map[string]string
	json.Unmarshal(e.api.objects[caConfigPath].Data, &cm)
	if cm["ca.crt"] != "----CA----" {
		t.Errorf("CA copy = %v", cm)
	}
	plane := string(e.api.objects[planePath].Spec)
	if !strings.Contains(plane, `"planeID":"default"`) || !strings.Contains(plane, `"name":"cluster-agent-tls","namespace":"openchoreo-workflow-plane"`) {
		t.Errorf("ClusterWorkflowPlane spec = %s", plane)
	}
}

func TestInstall_MatchingFingerprintAndHealthyRunsNoHelm(t *testing.T) {
	e := newTestEnv(t)
	e.healthy()
	for range 10 {
		state, err := e.inst.Install(context.Background(), instance(Fingerprint()))
		if err != nil {
			t.Fatalf("Install() = %v", err)
		}
		if !state.Ready || state.Applied != Fingerprint() || state.Message != readyMessage {
			t.Fatalf("state = %+v", state)
		}
	}
	if len(e.helm.calls) != 0 {
		t.Errorf("helm calls = %v, want none", e.helm.calls)
	}
	if n := e.api.count("APPLY"); n != 0 {
		t.Errorf("applies = %d, want none", n)
	}
	if n := e.api.count("GET"); n != 50 {
		t.Errorf("GETs = %d, want 5 per resync", n)
	}
}

// A fault is repaired straight away, even right after an install, unless
// it may only be settling; Helm runs only for the chart's own objects.
func TestInstall_Repairs(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(e *testEnv) resource.ProviderInstance
		wantHelm int
		restored string
	}{
		{"missing registry Deployment", func(e *testEnv) resource.ProviderInstance {
			delete(e.api.objects, registryPath)
			return instance(Fingerprint())
		}, 0, registryPath},
		{"missing workflow", func(e *testEnv) resource.ProviderInstance {
			delete(e.api.objects, workflowPath)
			return instance(Fingerprint())
		}, 0, workflowPath},
		{"plane not registered", func(e *testEnv) resource.ProviderInstance {
			delete(e.api.objects, planePath)
			return instance(Fingerprint())
		}, 0, planePath},
		{"missing workflow controller", func(e *testEnv) resource.ProviderInstance {
			delete(e.api.objects, argoPath)
			return instance(Fingerprint())
		}, 1, argoPath},
		{"agent disconnected after a restart", func(e *testEnv) resource.ProviderInstance {
			e.api.put(planePath, kubernetes.Object{Status: json.RawMessage(`{"agentConnection":{"connected":false}}`)})
			return instance(Fingerprint())
		}, 1, ""},
		{"changed fingerprint", func(e *testEnv) resource.ProviderInstance {
			return instance("sha256:old")
		}, 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.healthy()
			// Even right after an install, a missing object isn't waited out.
			e.inst.lastInstall = e.now
			if tt.name == "agent disconnected after a restart" {
				e.inst.lastInstall = time.Time{}
			}
			state, err := e.inst.Install(context.Background(), tt.setup(e))
			if err != nil {
				t.Fatalf("Install() = %v", err)
			}
			if len(e.helm.calls) != tt.wantHelm {
				t.Errorf("helm calls = %d, want %d", len(e.helm.calls), tt.wantHelm)
			}
			if tt.restored != "" && !e.api.has(tt.restored) {
				t.Errorf("%s wasn't restored", tt.restored)
			}
			if state.Applied != Fingerprint() {
				t.Errorf("state = %+v", state)
			}
		})
	}
}

func TestInstall_WaitsOutSettlingThenRepairsWithBackOff(t *testing.T) {
	e := newTestEnv(t)
	first, err := e.inst.Install(context.Background(), instance(""))
	if err != nil {
		t.Fatal(err)
	}
	applied := instance(first.Applied)
	// The registry comes up, but the agent's connection isn't seen yet.
	e.api.put(registryPath, kubernetes.Object{Status: readyStatus})
	e.now = e.now.Add(time.Minute)
	state, err := e.inst.Install(context.Background(), applied)
	if err != nil {
		t.Fatal(err)
	}
	if state.Ready || state.Message != "waiting for the workflow plane's agent to connect" || len(e.helm.calls) != 1 {
		t.Errorf("settling: state = %+v, helm calls = %d", state, len(e.helm.calls))
	}
	// Past the settle window, it's repaired with Helm, and the next
	// window is twice as long.
	e.now = e.now.Add(agentSettle)
	if _, err := e.inst.Install(context.Background(), applied); err != nil {
		t.Fatal(err)
	}
	if len(e.helm.calls) != 2 {
		t.Fatalf("after settling: helm calls = %d, want 2", len(e.helm.calls))
	}
	e.now = e.now.Add(agentSettle + time.Minute)
	if _, err := e.inst.Install(context.Background(), applied); err != nil {
		t.Fatal(err)
	}
	if len(e.helm.calls) != 2 {
		t.Errorf("inside the doubled window: helm calls = %d, want 2", len(e.helm.calls))
	}
	// Connected: ready without Helm, and the back-off resets.
	e.api.put(planePath, kubernetes.Object{Status: connectedPlane})
	state, err = e.inst.Install(context.Background(), applied)
	if err != nil || !state.Ready || len(e.helm.calls) != 2 || e.inst.repairs != 0 {
		t.Errorf("connected: state = %+v, err = %v, helm calls = %d, repairs = %d", state, err, len(e.helm.calls), e.inst.repairs)
	}
}

// A lasting fault keeps waiting the capped window, however many repairs.
func TestInstall_SettleWindowStaysCapped(t *testing.T) {
	e := newTestEnv(t)
	e.healthy()
	e.api.put(planePath, kubernetes.Object{Status: json.RawMessage(`{}`)})
	e.inst.lastInstall = e.now
	for range 100 {
		e.now = e.now.Add(maxSettle) // each poll is past the window
		if _, err := e.inst.Install(context.Background(), instance(Fingerprint())); err != nil {
			t.Fatal(err)
		}
	}
	if e.inst.repairs > 4 {
		t.Errorf("repairs = %d, want it to stop at the cap", e.inst.repairs)
	}
	helm := len(e.helm.calls)
	e.now = e.now.Add(maxSettle - time.Minute)
	if _, err := e.inst.Install(context.Background(), instance(Fingerprint())); err != nil {
		t.Fatal(err)
	}
	if len(e.helm.calls) != helm {
		t.Error("repaired inside the capped window")
	}
}

func TestInstall_WithoutTheControlPlaneWaits(t *testing.T) {
	e := newTestEnv(t)
	delete(e.api.objects, caSecretPath)
	_, err := e.inst.Install(context.Background(), instance(""))
	if !errors.Is(err, reconcile.ErrProviderNotReady) {
		t.Errorf("Install() = %v, want ErrProviderNotReady", err)
	}
	if len(e.helm.calls) != 0 {
		t.Errorf("helm ran without the control plane")
	}
}

func TestInstall_HelmFailureIsAnError(t *testing.T) {
	e := newTestEnv(t)
	e.helm.err = errors.New("boom")
	if _, err := e.inst.Install(context.Background(), instance("")); err == nil {
		t.Fatal("Install() = nil, want the helm error")
	}
	if e.api.has(planePath) {
		t.Error("ClusterWorkflowPlane applied after the chart failed")
	}
}

func TestInstall_RegistryPortTakenSaysWhoHasIt(t *testing.T) {
	e := newTestEnv(t)
	other := `{"hostNetwork":true,"containers":[{"ports":[{"containerPort":5000}]}]}`
	e.api.put("/api/v1/pods/other", kubernetes.Object{
		Metadata: kubernetes.ObjectMeta{Name: "other", Namespace: "default"},
		Spec:     json.RawMessage(other), Status: json.RawMessage(`{"phase":"Running"}`),
	})
	state, err := e.inst.Install(context.Background(), instance(""))
	if err != nil {
		t.Fatalf("Install() = %v", err)
	}
	if state.Ready || state.Applied != "" || !strings.Contains(state.Message, "pod default/other is using it") {
		t.Errorf("state = %+v", state)
	}
	if e.api.has(registryPath) {
		t.Error("registry applied over a taken port")
	}
	// The port is checked before anything else, so polling a taken port
	// costs no Helm run.
	if _, err := e.inst.Install(context.Background(), instance("")); err != nil {
		t.Fatal(err)
	}
	if len(e.helm.calls) != 0 {
		t.Errorf("helm calls = %d, want none while the port is taken", len(e.helm.calls))
	}
}

func TestRegistryPortHolder_IgnoresTheRegistryAndFinishedPods(t *testing.T) {
	e := newTestEnv(t)
	hostNet := json.RawMessage(`{"hostNetwork":true,"containers":[{"ports":[{"containerPort":5000}]}]}`)
	e.api.put("/api/v1/pods/registry", kubernetes.Object{
		Metadata: kubernetes.ObjectMeta{Name: "registry", Namespace: BuildsNamespace, Labels: registryLabels()},
		Spec:     hostNet, Status: json.RawMessage(`{"phase":"Running"}`),
	})
	e.api.put("/api/v1/pods/done", kubernetes.Object{
		Metadata: kubernetes.ObjectMeta{Name: "done", Namespace: "default"},
		Spec:     hostNet, Status: json.RawMessage(`{"phase":"Succeeded"}`),
	})
	e.api.put("/api/v1/pods/elsewhere", kubernetes.Object{
		Metadata: kubernetes.ObjectMeta{Name: "elsewhere", Namespace: "default"},
		Spec:     json.RawMessage(`{"containers":[{"ports":[{"containerPort":5000}]}]}`),
		Status:   json.RawMessage(`{"phase":"Running"}`),
	})
	if got, err := registryPortHolder(context.Background(), e.inst.client); err != nil || got != "" {
		t.Errorf("holder = %q, %v; want none", got, err)
	}
	e.api.put("/api/v1/pods/hostport", kubernetes.Object{
		Metadata: kubernetes.ObjectMeta{Name: "hostport", Namespace: "default"},
		Spec:     json.RawMessage(`{"containers":[{"ports":[{"containerPort":80,"hostPort":5000}]}]}`),
		Status:   json.RawMessage(`{"phase":"Pending"}`),
	})
	if got, _ := registryPortHolder(context.Background(), e.inst.client); got != "default/hostport" {
		t.Errorf("holder = %q, want default/hostport", got)
	}
}

func TestUninstall_RemovesInOrderAndIsIdempotent(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.inst.Install(context.Background(), instance("")); err != nil {
		t.Fatal(err)
	}
	e.api.reset()
	for i := range 2 {
		if err := e.inst.Uninstall(context.Background(), "pi-1"); err != nil {
			t.Fatalf("Uninstall() #%d = %v", i+1, err)
		}
	}
	for _, p := range []string{workflowPath, planePath, registryPath, registryPVC, registrySvc, caConfigPath} {
		if e.api.has(p) {
			t.Errorf("%s left behind", p)
		}
	}
	calls := e.api.calls
	idx := func(call string) int { return slices.Index(calls, call) }
	if !(idx("DELETE "+workflowPath) < idx("GET "+workflowRunsPath) &&
		idx("GET "+workflowRunsPath) < idx("DELETE "+planePath) &&
		idx("DELETE "+planePath) < idx("DELETE "+registrySvc) &&
		idx("DELETE "+registrySvc) < idx("DELETE "+registryPath) &&
		idx("DELETE "+registryPath) < idx("DELETE "+registryPVC)) {
		t.Errorf("delete order = %v", calls)
	}
	uninstall := []string{"uninstall", "openchoreo-workflow-plane", "--namespace", "openchoreo-workflow-plane",
		"--ignore-not-found", "--wait", "--timeout", "5m"}
	if len(e.helm.calls) != 3 || !slices.Equal(e.helm.calls[1], uninstall) || !slices.Equal(e.helm.calls[2], uninstall) {
		t.Errorf("helm calls = %v", e.helm.calls)
	}
	if e.api.queries["DELETE /api/v1/namespaces/workflows-serendip-builds/secrets"] != "labelSelector=serendip.dev%2Fbuild" {
		t.Errorf("credential copies weren't deleted by label: %v", e.api.queries)
	}
}

func TestUninstall_HelmFailureStopsBeforeTheRegistry(t *testing.T) {
	e := newTestEnv(t)
	e.healthy()
	e.api.put(registryPVC, kubernetes.Object{Kind: "PersistentVolumeClaim"})
	e.helm.err = errors.New("boom")
	if err := e.inst.Uninstall(context.Background(), "pi-1"); err == nil {
		t.Fatal("Uninstall() = nil, want the helm error")
	}
	if !e.api.has(registryPath) || !e.api.has(registryPVC) {
		t.Error("the registry was removed after the chart failed to uninstall")
	}
}

func TestUninstall_NothingInstalledSucceeds(t *testing.T) {
	e := newTestEnv(t)
	if err := e.inst.Uninstall(context.Background(), "pi-1"); err != nil {
		t.Errorf("Uninstall() = %v", err)
	}
}

func TestUninstall_WaitsForRunsBeforeRemovingThePlane(t *testing.T) {
	e := newTestEnv(t)
	e.healthy()
	run := workflowRunsPath + "/b-1"
	e.api.put(run, kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: "b-1", Namespace: BuildsNamespace}})
	e.api.finalizing[run] = true

	done := workflowRunsPath + "/b-2" // no finalizer: gone at once
	e.api.put(done, kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: "b-2", Namespace: BuildsNamespace}})

	err := e.inst.Uninstall(context.Background(), "pi-1")
	if !errors.Is(err, ErrRunsRemaining) || !strings.Contains(err.Error(), "(1 left)") {
		t.Fatalf("Uninstall() = %v, want ErrRunsRemaining with 1 left", err)
	}
	if !e.api.has(planePath) || len(e.helm.calls) != 0 {
		t.Error("the plane was removed while a run was still finalizing")
	}
	// The run's finalizer finishes; the retry completes.
	delete(e.api.objects, run)
	if err := e.inst.Uninstall(context.Background(), "pi-1"); err != nil {
		t.Fatalf("retry Uninstall() = %v", err)
	}
	if e.api.has(planePath) || len(e.helm.calls) != 1 {
		t.Errorf("after retry: plane left = %v, helm calls = %d", e.api.has(planePath), len(e.helm.calls))
	}
}

func TestFingerprint_StableAndCoversWhatIsApplied(t *testing.T) {
	a, b := Fingerprint(), Fingerprint()
	if a != b || !strings.HasPrefix(a, "sha256:") || len(a) != len("sha256:")+64 {
		t.Errorf("Fingerprint() = %q, %q", a, b)
	}
}

// A release left locked by a Helm run that never finished (a process
// killed mid-install, specs/022-builds T013) is recovered, then installed
// again: a pending first install is uninstalled, a pending upgrade rolled
// back.
func TestInstall_RecoversAReleaseLeftPending(t *testing.T) {
	for _, tc := range []struct{ status, recovery string }{
		{"pending-install", "uninstall"},
		{"pending-upgrade", "rollback"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			e := newTestEnv(t)
			locked := true
			e.helm.respond = func(args []string) ([]byte, error) {
				switch args[0] {
				case "upgrade":
					if locked {
						return nil, errors.New("helm upgrade: exit status 1: Error: UPGRADE FAILED: " + helmBusy)
					}
				case "status":
					return []byte(`WARNING: noise
{"name":"openchoreo-workflow-plane","info":{"status":"` + tc.status + `"}}`), nil
				case "uninstall", "rollback":
					locked = false
				}
				return nil, nil
			}
			if _, err := e.inst.Install(context.Background(), instance("")); err != nil {
				t.Fatalf("Install() = %v", err)
			}
			var got []string
			for _, c := range e.helm.calls {
				got = append(got, c[0])
			}
			if want := []string{"upgrade", "status", tc.recovery, "upgrade"}; !slices.Equal(got, want) {
				t.Errorf("helm calls = %v, want %v", got, want)
			}
		})
	}

	// Anything else stays an error, with no recovery attempted.
	e := newTestEnv(t)
	e.helm.err = errors.New("helm upgrade: exit status 1: Error: chart not found")
	if _, err := e.inst.Install(context.Background(), instance("")); err == nil || len(e.helm.calls) != 1 {
		t.Errorf("Install() = %v after %d helm calls, want the error after 1", err, len(e.helm.calls))
	}
}

// Removal leaves no finished build behind (T014 F4): the run namespace's
// Argo Workflows, step pods and workspace volumes go, before the plane
// does, and so does the agent's TLS Secret, which cert-manager leaves
// (F1).
func TestUninstall_RemovesFinishedBuildsAndTheAgentKey(t *testing.T) {
	e := newTestEnv(t)
	e.healthy()
	wf := argoWorkflowPath("build-1")
	pod := "/api/v1/namespaces/" + RunNamespace + "/pods/build-1-checkout-1"
	pvc := "/api/v1/namespaces/" + RunNamespace + "/persistentvolumeclaims/build-1-workspace"
	tls := "/api/v1/namespaces/" + PlaneNamespace + "/secrets/" + agentTLSSecretName
	for _, p := range []string{wf, pod, pvc, tls} {
		e.api.put(p, kubernetes.Object{})
	}
	if err := e.inst.Uninstall(context.Background(), "pi-1"); err != nil {
		t.Fatalf("Uninstall() = %v", err)
	}
	for _, p := range []string{wf, pod, pvc, tls} {
		if e.api.has(p) {
			t.Errorf("%s left behind", p)
		}
	}
	calls := e.api.calls
	wfAt, planeAt := slices.Index(calls, "DELETE "+argoWorkflowsPath), slices.Index(calls, "DELETE "+planePath)
	if wfAt < 0 || planeAt < 0 || wfAt > planeAt {
		t.Errorf("the workflows weren't removed before the plane: %v", calls)
	}
}

// A Workflow still finalizing holds the removal, as a run does, so the
// controller that finalizes it isn't removed first.
func TestUninstall_WaitsForWorkflowsBeforeRemovingThePlane(t *testing.T) {
	e := newTestEnv(t)
	e.healthy()
	wf := argoWorkflowPath("build-1")
	e.api.put(wf, kubernetes.Object{})
	e.api.finalizing[wf] = true
	err := e.inst.Uninstall(context.Background(), "pi-1")
	if !errors.Is(err, ErrWorkflowsRemaining) || !errors.Is(err, ErrRunsRemaining) || !strings.Contains(err.Error(), "(1 left)") {
		t.Fatalf("Uninstall() = %v, want ErrWorkflowsRemaining", err)
	}
	if !e.api.has(planePath) || len(e.helm.calls) != 0 {
		t.Error("the plane was removed while a workflow was still finalizing")
	}
	// Its pods and volumes are left to the controller finalizing it.
	if n := e.api.count("DELETE /api/v1/namespaces/" + RunNamespace + "/pods"); n != 0 {
		t.Errorf("pods were deleted while a workflow was finalizing (%d deletes)", n)
	}
}
