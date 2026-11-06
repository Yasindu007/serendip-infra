package openchoreo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/internal/gitremote"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

const (
	testBuildID = "11111111-2222-3333-4444-555555555555"
	testCompID  = "cmp-id-1"
	testCommit  = "19a03d1880f84504fffa4d573a20e3b488f7a655"
	testImage   = "localhost:5000/serendip/cmp-x:19a03d1880f8"
)

var (
	testRun      = runName(testBuildID)
	testRunPath  = workflowRunsPath + "/" + testRun
	testWorkflow = argoWorkflowPath(testRun)
)

func testBuild() resource.Build {
	var b resource.Build
	b.Metadata.ID = testBuildID
	return b
}

func testComponent() resource.Component {
	var c resource.Component
	c.Metadata.ID = testCompID
	c.Spec.Source = &resource.ComponentSource{Repository: "https://github.com/org/repo.git", Path: "svc"}
	return c
}

// buildsEnv is a Builds over the installer tests' fake API, plus pod
// logs (served from logs, keyed by pod name; a pod in waiting answers
// 400 until it's taken out) and events (keyed by object name).
type buildsEnv struct {
	api *fakeAPI
	b   *Builds

	mu      sync.Mutex
	logs    map[string]string
	waiting map[string]bool
	events  map[string][]event
	logReqs []string
}

func newBuildsEnv(t *testing.T) *buildsEnv {
	t.Helper()
	e := &buildsEnv{api: newFakeAPI(), logs: map[string]string{}, waiting: map[string]bool{}, events: map[string][]event{}}
	api := e.api.handler(t)
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/log"):
			pod := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/"+RunNamespace+"/pods/"), "/log")
			e.logReqs = append(e.logReqs, pod+"?"+r.URL.RawQuery)
			body, ok := e.logs[pod]
			waiting := e.waiting[pod]
			e.mu.Unlock()
			switch {
			case waiting:
				http.Error(w, `container "main" is waiting to start`, http.StatusBadRequest)
			case !ok:
				http.NotFound(w, r)
			case body == "!500":
				http.Error(w, "boom", http.StatusInternalServerError)
			default:
				io.WriteString(w, body)
			}
			return
		case strings.HasSuffix(r.URL.Path, "/events"):
			name := strings.TrimPrefix(r.URL.Query().Get("fieldSelector"), "involvedObject.name=")
			items := e.events[r.URL.Path+"|"+name]
			e.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"items": items})
			return
		}
		e.mu.Unlock()
		api(w, r)
	})
	e.b = NewBuilds(client)
	e.b.pollEvery = 5 * time.Millisecond
	return e
}

func (e *buildsEnv) setWaiting(pod string, v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.waiting[pod] = v
}

func podPath(name string) string { return "/api/v1/namespaces/" + RunNamespace + "/pods/" + name }

func (e *buildsEnv) addPod(name, created string) {
	e.api.put(podPath(name), kubernetes.Object{Kind: "Pod", Metadata: kubernetes.ObjectMeta{Name: name, CreationTimestamp: created}})
}

func TestRunName_ValidAndStable(t *testing.T) {
	long := strings.Repeat("a:b.", 32) // 128 characters, with ":" which Kubernetes names refuse
	for _, id := range []string{testBuildID, long} {
		n := runName(id)
		if n != runName(id) || len(n) != len("build-")+16 || strings.ContainsAny(n, ":.") {
			t.Errorf("runName(%q) = %q", id, n)
		}
	}
	if runName("a") == runName("b") {
		t.Error("two IDs share a run")
	}
}

func TestStartBuild_PublicCreatesOnlyTheRun(t *testing.T) {
	e := newBuildsEnv(t)
	b := testBuild()
	b.Spec.Commit = testCommit
	if err := e.b.StartBuild(context.Background(), resource.Application{}, testComponent(), b, nil); err != nil {
		t.Fatalf("StartBuild() = %v", err)
	}
	run, ok := e.api.objects[testRunPath]
	if !ok {
		t.Fatalf("no run; calls %v", e.api.calls)
	}
	if run.Metadata.Labels[BuildLabel] != testRun || run.Metadata.Annotations[buildIDAnnotation] != testBuildID {
		t.Errorf("run metadata = %+v", run.Metadata)
	}
	for _, l := range []string{"openchoreo.dev/component", "openchoreo.dev/project"} {
		if _, ok := run.Metadata.Labels[l]; ok {
			t.Errorf("run carries %s, which 1.3.1 refuses (T001 item 3)", l)
		}
	}
	var spec struct {
		Workflow struct {
			Kind, Name string
			Parameters struct {
				Repository struct{ URL, Branch, Commit, Path string }
				Dockerfile string
				Image      struct{ Repository string }
			}
		}
	}
	if err := json.Unmarshal(run.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	w := spec.Workflow
	if w.Kind != "ClusterWorkflow" || w.Name != WorkflowName {
		t.Errorf("workflow = %s/%s", w.Kind, w.Name)
	}
	repo := w.Parameters.Repository
	if repo.URL != "https://github.com/org/repo.git" || repo.Branch != "main" || repo.Commit != testCommit || repo.Path != "svc" ||
		w.Parameters.Dockerfile != "Dockerfile" || w.Parameters.Image.Repository != imageRepository(testCompID) {
		t.Errorf("parameters = %+v", w.Parameters)
	}
	if !strings.HasPrefix(w.Parameters.Image.Repository, "serendip/cmp-") {
		t.Errorf("image repository = %q", w.Parameters.Image.Repository)
	}
	if e.api.has(gitSecretPath(testRun)) || e.api.count("APPLY /api/v1/namespaces") != 0 {
		t.Errorf("a public build made a credential copy: %v", e.api.calls)
	}
}

func TestStartBuild_PrivateCopiesTheCredentialFirst(t *testing.T) {
	tests := []struct {
		name     string
		cred     map[string]any
		wantUser string
		wantPass string
	}{
		{"username and password", map[string]any{"username": "u", "password": "p"}, "u", "p"},
		{"a token alone", map[string]any{"token": "t"}, "git", "t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newBuildsEnv(t)
			if err := e.b.StartBuild(context.Background(), resource.Application{}, testComponent(), testBuild(), tt.cred); err != nil {
				t.Fatalf("StartBuild() = %v", err)
			}
			secret, ok := e.api.objects[gitSecretPath(testRun)]
			if !ok {
				t.Fatalf("no credential copy; calls %v", e.api.calls)
			}
			var data map[string][]byte
			json.Unmarshal(secret.Data, &data)
			if string(data["username"]) != tt.wantUser || string(data["password"]) != tt.wantPass {
				t.Errorf("credential copy = %q", data)
			}
			if secret.Metadata.Labels[BuildLabel] != testRun {
				t.Errorf("credential copy labels = %v", secret.Metadata.Labels)
			}
			calls := strings.Join(e.api.calls, "\n")
			ns := strings.Index(calls, "APPLY /api/v1/namespaces/"+RunNamespace+"\n")
			sec := strings.Index(calls, "APPLY "+gitSecretPath(testRun))
			run := strings.Index(calls, "POST "+workflowRunsPath)
			if ns < 0 || ns > sec || sec > run {
				t.Errorf("order: namespace, then the copy, then the run; got\n%s", calls)
			}
		})
	}
}

func TestStartBuild_Refusals(t *testing.T) {
	e := newBuildsEnv(t)
	if err := e.b.StartBuild(context.Background(), resource.Application{}, testComponent(), testBuild(), map[string]any{"username": "u"}); err == nil {
		t.Error("a credential without a password started a build")
	}
	var imageOnly resource.Component
	imageOnly.Spec.Image = "nginx"
	if err := e.b.StartBuild(context.Background(), resource.Application{}, imageOnly, testBuild(), nil); err == nil {
		t.Error("a component without a source started a build")
	}
	if e.api.count("POST") != 0 {
		t.Errorf("calls = %v", e.api.calls)
	}
}

func TestStartBuild_ExistingRunIsLeftAlone(t *testing.T) {
	e := newBuildsEnv(t)
	e.api.put(testRunPath, kubernetes.Object{Kind: "WorkflowRun", Metadata: kubernetes.ObjectMeta{Name: testRun},
		Status: json.RawMessage(`{"conditions":[{"type":"WorkflowRunning","status":"True"}]}`)})
	e.api.reset()
	if err := e.b.StartBuild(context.Background(), resource.Application{}, testComponent(), testBuild(), map[string]any{"password": "new"}); err != nil {
		t.Fatalf("StartBuild() = %v", err)
	}
	if string(e.api.objects[testRunPath].Status) == "" {
		t.Error("the existing run was replaced")
	}
	if e.api.has(gitSecretPath(testRun)) || len(e.api.calls) != 1 {
		t.Errorf("a running build's credential copy was touched: %v", e.api.calls)
	}
}

func TestCancelAndRemove_DeleteRunAndCopyIdempotently(t *testing.T) {
	for _, op := range []string{"cancel", "remove"} {
		t.Run(op, func(t *testing.T) {
			e := newBuildsEnv(t)
			e.api.put(testRunPath, kubernetes.Object{Kind: "WorkflowRun"})
			e.api.put(gitSecretPath(testRun), kubernetes.Object{Kind: "Secret"})
			do := e.b.RemoveBuild
			if op == "cancel" {
				do = e.b.CancelBuild
			}
			for range 2 {
				if err := do(context.Background(), testBuild()); err != nil {
					t.Fatalf("%s = %v", op, err)
				}
			}
			if e.api.has(testRunPath) || e.api.has(gitSecretPath(testRun)) {
				t.Errorf("left behind; calls %v", e.api.calls)
			}
		})
	}
}

// The status samples are T001's observations (plan "Spike findings"
// items 3, 5 and 6) in the shapes OpenChoreo 1.3.1 and Argo 4.0.8 write.
const (
	condPending   = `{"type":"WorkflowCompleted","status":"False","reason":"WorkflowPending","message":"Workflow has not completed yet"}`
	condRunning   = `{"type":"WorkflowRunning","status":"True","reason":"WorkflowRunning","message":"Argo Workflow is running"}`
	condRunDone   = `{"type":"WorkflowRunning","status":"False","reason":"WorkflowRunning","message":"Argo Workflow running has completed"}`
	condSucceeded = `{"type":"WorkflowSucceeded","status":"True","reason":"WorkflowSucceeded","message":"Workflow completed successfully"},` +
		`{"type":"WorkflowCompleted","status":"True","reason":"WorkflowSucceeded","message":"Workflow has completed successfully"}`
	condFailed = `{"type":"WorkflowFailed","status":"True","reason":"WorkflowFailed","message":"Workflow execution failed"},` +
		`{"type":"WorkflowCompleted","status":"True","reason":"WorkflowFailed","message":"Workflow has completed with failure"}`
	condNoPlane = `{"type":"WorkflowCompleted","status":"False","reason":"WorkflowPlaneNotFound","message":"ClusterWorkflowPlane default not found"}`
)

func runJSON(conds, tasks string) string {
	return fmt.Sprintf(`{"conditions":[%s],"tasks":[%s],"startedAt":"2026-10-24T10:00:05Z",`+
		`"runReference":{"apiVersion":"argoproj.io/v1alpha1","kind":"Workflow","name":%q,"namespace":%q}}`, conds, tasks, testRun, RunNamespace)
}

func task(name, phase, msg string) string {
	return fmt.Sprintf(`{"name":%q,"phase":%q,"startedAt":"2026-10-24T10:00:10Z","message":%q}`, name, phase, msg)
}

func node(template, phase, msg, output, value string) string {
	out := ""
	if output != "" {
		out = fmt.Sprintf(`,"outputs":{"parameters":[{"name":%q,"value":%q}]}`, output, value)
	}
	return fmt.Sprintf(`%q:{"id":"%[1]s","name":"%s[0].%s","displayName":%q,"type":"Pod","templateName":%q,"phase":%q,"message":%q,`+
		`"startedAt":"2026-10-24T10:00:01Z","finishedAt":"2026-10-24T10:01:00Z"%s}`,
		testRun+"-"+template, testRun, template, template, template, phase, msg, out)
}

func argoJSON(phase, msg string, nodes ...string) string {
	steps := fmt.Sprintf(`%q:{"id":%[1]q,"name":%[1]q,"type":"Steps","templateName":"build","phase":%q}`, testRun, phase)
	finished := ""
	if phase == "Succeeded" || phase == "Failed" || phase == "Error" {
		finished = `,"finishedAt":"2026-10-24T10:02:00Z"`
	}
	return fmt.Sprintf(`{"phase":%q,"message":%q,"startedAt":"2026-10-24T10:00:00Z"%s,"nodes":{%s}}`,
		phase, msg, finished, strings.Join(append([]string{steps}, nodes...), ","))
}

func TestObserveBuild_StatusToPhase(t *testing.T) {
	commitOut := func(phase string) string { return node(CheckoutStep, phase, "", CommitOutput, testCommit) }
	tests := []struct {
		name      string
		run       string // "" for none
		argo      string // "" for none
		wantPhase resource.BuildPhase
		wantMsg   string
		wantSteps string // name:phase, comma-separated
		commit    bool
		image     bool
	}{
		{name: "just created, no status", run: "{}", wantPhase: resource.BuildQueued},
		{name: "pending", run: runJSON(condPending, ""), wantPhase: resource.BuildQueued},
		{name: "the plane can't be found", run: runJSON(condNoPlane, ""), wantPhase: resource.BuildQueued,
			wantMsg: "WorkflowPlaneNotFound: ClusterWorkflowPlane default not found"},
		{name: "running before the Workflow is read", run: runJSON(condPending+","+condRunning, task(CheckoutStep, "Running", "")),
			wantPhase: resource.BuildRunning, wantSteps: "checkout:Running"},
		{name: "running; the run's tasks lag the Workflow's nodes",
			run:       runJSON(condPending+","+condRunning, task(CheckoutStep, "Pending", "")),
			argo:      argoJSON("Running", "", commitOut("Succeeded"), node(BuildPushStep, "Running", "", "", "")),
			wantPhase: resource.BuildRunning, wantSteps: "checkout:Succeeded,build-push:Running", commit: true},
		{name: "the Workflow is pending", run: runJSON(condPending, ""), argo: argoJSON("Pending", ""),
			wantPhase: resource.BuildQueued},
		{name: "succeeded",
			run:       runJSON(condRunDone+","+condSucceeded, task(CheckoutStep, "Succeeded", "")+","+task(BuildPushStep, "Succeeded", "")),
			argo:      argoJSON("Succeeded", "", commitOut("Succeeded"), node(BuildPushStep, "Succeeded", "", ImageOutput, testImage)),
			wantPhase: resource.BuildSucceeded, wantSteps: "checkout:Succeeded,build-push:Succeeded", commit: true, image: true},
		{name: "the Workflow succeeded before the run says so",
			run:       runJSON(condPending+","+condRunning, task(CheckoutStep, "Succeeded", "")),
			argo:      argoJSON("Succeeded", "", commitOut("Succeeded"), node(BuildPushStep, "Succeeded", "", ImageOutput, testImage)),
			wantPhase: resource.BuildSucceeded, wantSteps: "checkout:Succeeded,build-push:Succeeded", commit: true, image: true},
		{name: "the build step failed",
			run:       runJSON(condRunDone+","+condFailed, task(CheckoutStep, "Succeeded", "")+","+task(BuildPushStep, "Failed", "Error (exit code 1)")),
			argo:      argoJSON("Failed", "child 'x' failed", commitOut("Succeeded"), node(BuildPushStep, "Failed", "Error (exit code 1)", "", "")),
			wantPhase: resource.BuildFailed, wantMsg: "build-push: Error (exit code 1)",
			wantSteps: "checkout:Succeeded,build-push:Failed", commit: true},
		{name: "the checkout pod couldn't be created",
			run: runJSON(condRunDone+","+condFailed, task(CheckoutStep, "Error",
				`Pod "x" is invalid: spec.volumes[1].secret.secretName: Required value`)),
			wantPhase: resource.BuildFailed, wantMsg: `checkout: Pod "x" is invalid: spec.volumes[1].secret.secretName: Required value`,
			wantSteps: "checkout:Failed"},
		{name: "the Workflow errored",
			run:       runJSON(condPending+","+condRunning, ""),
			argo:      argoJSON("Error", "pod deleted", node(CheckoutStep, "Error", "", "", "")),
			wantPhase: resource.BuildFailed, wantMsg: "pod deleted", wantSteps: "checkout:Failed"},
		{name: "failed with no message anywhere", run: runJSON(condFailed, ""),
			wantPhase: resource.BuildFailed, wantMsg: "Workflow execution failed"},
		{name: "succeeded without an image",
			run:       runJSON(condSucceeded, ""),
			argo:      argoJSON("Succeeded", "", commitOut("Succeeded"), node(BuildPushStep, "Succeeded", "", "", "")),
			wantPhase: resource.BuildFailed, wantMsg: "the build finished without reporting its image",
			wantSteps: "checkout:Succeeded,build-push:Succeeded", commit: true},
		{name: "a step that never ran",
			run:       runJSON(condFailed, ""),
			argo:      argoJSON("Failed", "", node(CheckoutStep, "Failed", "Error (exit code 128)", "", ""), node(BuildPushStep, "Omitted", "", "", "")),
			wantPhase: resource.BuildFailed, wantMsg: "checkout: Error (exit code 128)", wantSteps: "checkout:Failed,build-push:Cancelled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newBuildsEnv(t)
			e.api.put(testRunPath, kubernetes.Object{Kind: "WorkflowRun", Status: json.RawMessage(tt.run)})
			if tt.argo != "" {
				e.api.put(testWorkflow, kubernetes.Object{Kind: "Workflow", Status: json.RawMessage(tt.argo)})
			}
			obs, err := e.b.ObserveBuild(context.Background(), testBuild())
			if err != nil {
				t.Fatalf("ObserveBuild() = %v", err)
			}
			if obs.Phase != tt.wantPhase || obs.Message != tt.wantMsg || obs.Gone {
				t.Errorf("phase, message, gone = %s, %q, %v; want %s, %q", obs.Phase, obs.Message, obs.Gone, tt.wantPhase, tt.wantMsg)
			}
			var steps []string
			for _, s := range obs.Steps {
				steps = append(steps, s.Name+":"+string(s.Phase))
			}
			if got := strings.Join(steps, ","); got != tt.wantSteps {
				t.Errorf("steps = %s, want %s", got, tt.wantSteps)
			}
			if (obs.Commit == testCommit) != tt.commit || (obs.Image == testImage) != tt.image {
				t.Errorf("commit, image = %q, %q", obs.Commit, obs.Image)
			}
			if tt.argo != "" && !obs.StartedAt.Equal(time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)) {
				t.Errorf("startedAt = %v, want the Workflow's", obs.StartedAt)
			}
			if obs.Phase.Done() == obs.FinishedAt.IsZero() && tt.argo != "" {
				t.Errorf("finishedAt = %v for %s", obs.FinishedAt, obs.Phase)
			}
		})
	}
}

func TestObserveBuild_GoneRun(t *testing.T) {
	e := newBuildsEnv(t)
	obs, err := e.b.ObserveBuild(context.Background(), testBuild())
	if err != nil || !obs.Gone {
		t.Fatalf("ObserveBuild() = %+v, %v; want Gone", obs, err)
	}
}

func TestPodStep(t *testing.T) {
	tests := map[string]int{
		testRun + "-checkout-1234567890":   0,
		testRun + "-build-push-2345678901": 1,
		testRun:                            -1,
		testRun + "-checkout":              -1,
		testRun + "x-checkout-1":           -1,
		"other-checkout-1":                 -1,
	}
	for name, want := range tests {
		if got := podStep(testRun, kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: name}}); got != want {
			t.Errorf("podStep(%s) = %d, want %d", name, got, want)
		}
	}
}

const argoNoise = `2026-10-24T10:00:01.000000000Z time="2026-10-24T10:00:01.000Z" level=info msg="capturing logs" argo=true` + "\n"

func readAll(t *testing.T, s reconcile.LogStream) ([]reconcile.LogLine, error) {
	t.Helper()
	var out []reconcile.LogLine
	for {
		l, err := s.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return out, err
		}
		out = append(out, l)
	}
}

func TestBuildLogs_RecentInStepOrderWithoutArgoLines(t *testing.T) {
	e := newBuildsEnv(t)
	e.api.put(testRunPath, kubernetes.Object{Kind: "WorkflowRun"})
	checkout, build := testRun+"-checkout-1", testRun+"-build-push-2"
	// Listed alphabetically, build-push comes first (T001 item 4).
	e.addPod(build, "2026-10-24T10:00:30Z")
	e.addPod(checkout, "2026-10-24T10:00:00Z")
	e.logs[checkout] = argoNoise + "2026-10-24T10:00:02Z checked out " + testCommit + "\n"
	e.logs[build] = "2026-10-24T10:00:31Z STEP 1/2: FROM alpine\n" + argoNoise + "2026-10-24T10:00:40Z pushed x\n"
	s, err := e.b.BuildLogs(context.Background(), testBuild(), reconcile.LogOptions{})
	if err != nil {
		t.Fatalf("BuildLogs() = %v", err)
	}
	defer s.Close()
	lines, err := readAll(t, s)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range lines {
		got = append(got, l.Instance+"|"+l.Line)
	}
	want := []string{"checkout|checked out " + testCommit, "build-push|STEP 1/2: FROM alpine", "build-push|pushed x"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if lines[0].Time.IsZero() {
		t.Error("timestamp not parsed")
	}
	for _, q := range e.logReqs {
		if !strings.Contains(q, "container=main") || !strings.Contains(q, "limitBytes=") || !strings.Contains(q, "tailLines=100") {
			t.Errorf("log request %s", q)
		}
	}
}

func TestBuildLogs_FollowWaitsForEachStepAndEndsWithTheRun(t *testing.T) {
	e := newBuildsEnv(t)
	e.api.put(testRunPath, kubernetes.Object{Kind: "WorkflowRun", Status: json.RawMessage(runJSON(condRunning, ""))})
	checkout, build := testRun+"-checkout-1", testRun+"-build-push-2"
	e.addPod(checkout, "2026-10-24T10:00:00Z")
	e.logs[checkout] = "2026-10-24T10:00:02Z checked out\n"
	s, err := e.b.BuildLogs(context.Background(), testBuild(), reconcile.LogOptions{Follow: true})
	if err != nil {
		t.Fatalf("BuildLogs() = %v", err)
	}
	defer s.Close()
	if l, err := s.Next(); err != nil || l.Line != "checked out" {
		t.Fatalf("Next() = %+v, %v", l, err)
	}
	// The build step's pod appears, waits to start, then runs.
	e.mu.Lock()
	e.waiting[build] = true
	e.logs[build] = "2026-10-24T10:00:31Z built\n"
	e.mu.Unlock()
	e.addPod(build, "2026-10-24T10:00:30Z")
	time.Sleep(30 * time.Millisecond)
	e.setWaiting(build, false)
	if l, err := s.Next(); err != nil || l.Line != "built" || l.Instance != BuildPushStep {
		t.Fatalf("Next() = %+v, %v", l, err)
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after the last step: %v, want io.EOF", err)
	}
	for _, q := range e.logReqs {
		if !strings.Contains(q, "follow=true") {
			t.Errorf("log request %s", q)
		}
	}
}

func TestBuildLogs_FollowEndsWhenTheRunFailsBeforeAStep(t *testing.T) {
	e := newBuildsEnv(t)
	e.api.put(testRunPath, kubernetes.Object{Kind: "WorkflowRun", Status: json.RawMessage(runJSON(condFailed, task(CheckoutStep, "Error", "pod invalid")))})
	s, err := e.b.BuildLogs(context.Background(), testBuild(), reconcile.LogOptions{Follow: true})
	if err != nil {
		t.Fatalf("BuildLogs() = %v", err)
	}
	defer s.Close()
	done := make(chan error, 1)
	go func() { _, err := readAll(t, s); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follow didn't end with a finished run")
	}
}

func TestBuildLogs_CloseEndsAWaitingFollow(t *testing.T) {
	e := newBuildsEnv(t)
	e.api.put(testRunPath, kubernetes.Object{Kind: "WorkflowRun", Status: json.RawMessage(runJSON(condRunning, ""))})
	s, err := e.b.BuildLogs(context.Background(), testBuild(), reconcile.LogOptions{Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close didn't return")
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("Next after Close = %v, want io.EOF", err)
	}
}

func TestBuildLogs_GoneRunHasExpired(t *testing.T) {
	e := newBuildsEnv(t)
	if _, err := e.b.BuildLogs(context.Background(), testBuild(), reconcile.LogOptions{}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("BuildLogs() = %v, want expired", err)
	}
}

func TestBuildLogs_RemovedPodIsNoFailure(t *testing.T) {
	e := newBuildsEnv(t)
	e.api.put(testRunPath, kubernetes.Object{Kind: "WorkflowRun"})
	e.addPod(testRun+"-checkout-1", "2026-10-24T10:00:00Z") // no logs: answers 404
	s, err := e.b.BuildLogs(context.Background(), testBuild(), reconcile.LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if lines, err := readAll(t, s); err != nil || len(lines) != 0 {
		t.Fatalf("lines, err = %v, %v", lines, err)
	}
}

func TestBuildLogs_UnreadableStepEndsWithItsError(t *testing.T) {
	e := newBuildsEnv(t)
	e.api.put(testRunPath, kubernetes.Object{Kind: "WorkflowRun"})
	checkout, build := testRun+"-checkout-1", testRun+"-build-push-2"
	e.addPod(checkout, "2026-10-24T10:00:00Z")
	e.addPod(build, "2026-10-24T10:00:30Z")
	e.logs[checkout] = "2026-10-24T10:00:02Z checked out\n"
	e.logs[build] = "!500"
	s, err := e.b.BuildLogs(context.Background(), testBuild(), reconcile.LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lines, err := readAll(t, s)
	if len(lines) != 1 || err == nil || !strings.Contains(err.Error(), "logs of build-push") {
		t.Fatalf("lines, err = %v, %v", lines, err)
	}
}

func TestBuildEvents_RunWorkflowAndPodsNewestFirst(t *testing.T) {
	e := newBuildsEnv(t)
	checkout := testRun + "-checkout-1"
	e.addPod(checkout, "2026-10-24T10:00:00Z")
	ev := func(kind, name, reason, at string) event {
		var x event
		x.Type, x.Reason, x.LastTimestamp, x.Count = "Normal", reason, at, 1
		x.InvolvedObject.Kind, x.InvolvedObject.Name = kind, name
		return x
	}
	e.events["/api/v1/namespaces/"+BuildsNamespace+"/events|"+testRun] = []event{ev("WorkflowRun", testRun, "Created", "2026-10-24T10:00:00Z")}
	e.events["/api/v1/namespaces/"+RunNamespace+"/events|"+testRun] = []event{ev("Workflow", testRun, "WorkflowRunning", "2026-10-24T10:00:01Z")}
	e.events["/api/v1/namespaces/"+RunNamespace+"/events|"+checkout] = []event{
		ev("Pod", checkout, "Pulled", "2026-10-24T10:00:05Z"),
		ev("Pod", checkout+"x", "Other", "2026-10-24T10:00:06Z"), // a different object
		ev("Pod", checkout, "Old", "2026-10-24T09:00:00Z"),
	}
	got, err := e.b.BuildEvents(context.Background(), testBuild(), time.Date(2026, 10, 24, 9, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildEvents() = %v", err)
	}
	var reasons []string
	for _, x := range got {
		reasons = append(reasons, x.Reason)
	}
	if strings.Join(reasons, ",") != "Pulled,WorkflowRunning,Created" {
		t.Errorf("events = %v", reasons)
	}
}

func TestHeadCommit_UsesTheCredential(t *testing.T) {
	body, err := os.ReadFile("../../internal/gitremote/testdata/private-basic.pkt")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "git" || p != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		w.Write(body)
	}))
	defer srv.Close()
	e := newBuildsEnv(t)
	e.b.http = srv.Client()
	src := resource.ComponentSource{Repository: srv.URL + "/r.git"}
	got, err := e.b.HeadCommit(context.Background(), src, map[string]any{"token": "tok"})
	if err != nil || got != "b4fe321b6c900c7193ccf3bb3c09e129163fd026" {
		t.Fatalf("HeadCommit() = %q, %v", got, err)
	}
	if _, err := e.b.HeadCommit(context.Background(), src, nil); !errors.Is(err, reconcile.ErrSourceAuth) || !strings.Contains(err.Error(), "credential") {
		t.Errorf("without a credential: %v", err)
	}
	ssh := resource.ComponentSource{Repository: "git@github.com:org/r.git"}
	if _, err := e.b.HeadCommit(context.Background(), ssh, nil); !errors.Is(err, reconcile.ErrSourceSSH) || !strings.Contains(err.Error(), "SSH") {
		t.Errorf("SSH: %v", err)
	}
}

func TestBuildLogs_FollowEndsWhenAStepNeverStarts(t *testing.T) {
	e := newBuildsEnv(t)
	e.api.put(testRunPath, kubernetes.Object{Kind: "WorkflowRun", Status: json.RawMessage(runJSON(condRunning, ""))})
	checkout := testRun + "-checkout-1"
	e.addPod(checkout, "2026-10-24T10:00:00Z")
	e.setWaiting(checkout, true) // an image that never pulls
	s, err := e.b.BuildLogs(context.Background(), testBuild(), reconcile.LogOptions{Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	done := make(chan error, 1)
	go func() { _, err := readAll(t, s); done <- err }()
	time.Sleep(20 * time.Millisecond)
	e.api.put(testRunPath, kubernetes.Object{Kind: "WorkflowRun", Status: json.RawMessage(runJSON(condFailed, task(CheckoutStep, "Failed", "ErrImagePull")))})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follow kept waiting on a step of a finished run")
	}
}

func TestHeadError_MapsToCoreErrors(t *testing.T) {
	for _, tt := range []struct {
		in, want error
	}{
		{gitremote.ErrSSH, reconcile.ErrSourceSSH},
		{gitremote.ErrAuth, reconcile.ErrSourceAuth},
		{gitremote.ErrNotFound, reconcile.ErrSourceNotFound},
		{gitremote.ErrNoBranch, reconcile.ErrSourceNoBranch},
	} {
		got := headError(fmt.Errorf("gitremote: %w", tt.in))
		if !errors.Is(got, tt.want) || !errors.Is(got, tt.in) {
			t.Errorf("headError(%v) = %v; want it to wrap %v and the original", tt.in, got, tt.want)
		}
	}
	if headError(nil) != nil {
		t.Error("headError(nil) isn't nil")
	}
	plain := errors.New("dial tcp: timeout")
	if got := headError(plain); got != plain {
		t.Errorf("headError(plain) = %v; want it unchanged", got)
	}
}
