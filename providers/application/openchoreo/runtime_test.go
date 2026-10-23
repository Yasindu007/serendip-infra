package openchoreo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

const runtimeNS = "dp-org-app-env-1234"

// dataPlane fakes the reads the runtime view makes: a binding, its
// rendered release, pods, events and pod logs.
type dataPlane struct {
	mu       sync.Mutex
	rendered bool
	pods     []string // names; "crash-…" pods are crash-looping
	events   string
	logs     map[string]string // pod -> body
	follow   chan string       // lines for following streams
	queries  []string
}

func (dp *dataPlane) handler(t *testing.T, binding string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dp.mu.Lock()
		dp.queries = append(dp.queries, r.URL.RawQuery)
		dp.mu.Unlock()
		path := r.URL.Path
		switch {
		case path == namespacedPath("releasebindings")+"/"+binding:
			fmt.Fprintf(w, `{"metadata":{"name":%q},"spec":{"environment":"env-x","owner":{"componentName":"cmp-x","projectName":"app-x"}}}`, binding)
		case path == namespacedPath("renderedreleases")+"/"+binding && dp.rendered:
			fmt.Fprintf(w, `{"metadata":{"name":%q},"status":{"resources":[
				{"kind":"Deployment","name":"cmp-x-dep","namespace":%q,"healthStatus":"Degraded","status":{"conditions":[{"type":"Available","status":"False","message":"Deployment does not have minimum availability."}]}},
				{"kind":"Service","name":"cmp-x","namespace":%q,"healthStatus":"Healthy"}]}}`, binding, runtimeNS, runtimeNS)
		case path == "/api/v1/namespaces/"+runtimeNS+"/pods":
			if r.URL.Query().Get("labelSelector") != "openchoreo.dev/component=cmp-x,openchoreo.dev/environment=env-x" {
				t.Errorf("pod selector = %q", r.URL.Query().Get("labelSelector"))
			}
			var items []string
			for i, name := range dp.pods {
				status := `{"phase":"Running","containerStatuses":[{"name":"main","ready":true}]}`
				if strings.HasPrefix(name, "crash") {
					status = `{"phase":"Running","containerStatuses":[{"name":"main","ready":false,"restartCount":4,"state":{"waiting":{"reason":"CrashLoopBackOff","message":"back-off 40s"}}}]}`
				}
				items = append(items, fmt.Sprintf(`{"metadata":{"name":%q,"creationTimestamp":"2026-10-23T10:0%d:00Z","labels":{"pod-template-hash":"abc"}},"spec":{"containers":[{"name":"main"}]},"status":%s}`, name, i, status))
			}
			fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(items, ","))
		case path == "/api/v1/namespaces/"+runtimeNS+"/events":
			// Honour the field selector, as the API server does.
			name := strings.TrimPrefix(r.URL.Query().Get("fieldSelector"), "involvedObject.name=")
			var all struct {
				Items []map[string]any `json:"items"`
			}
			json.Unmarshal([]byte(dp.events), &all)
			var keep []map[string]any
			for _, e := range all.Items {
				if e["involvedObject"].(map[string]any)["name"] == name {
					keep = append(keep, e)
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"items": keep})
		case strings.HasSuffix(path, "/log"):
			pod := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/namespaces/"+runtimeNS+"/pods/"), "/log")
			body, ok := dp.logs[pod]
			switch {
			case !ok:
				http.Error(w, `previous terminated container "main" not found`, http.StatusBadRequest)
				return
			case body == "FORBIDDEN":
				http.Error(w, "kubelet says no", http.StatusForbidden)
				return
			case strings.HasSuffix(body, "BREAK"):
				// Cut the connection mid-answer.
				w.Header().Set("Content-Length", "1000")
				w.Write([]byte(strings.TrimSuffix(body, "BREAK")))
				return
			}
			w.Write([]byte(body))
			if r.URL.Query().Get("follow") != "true" {
				return
			}
			w.(http.Flusher).Flush()
			for {
				select {
				case line := <-dp.follow:
					w.Write([]byte(line + "\n"))
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}
}

func newRuntimeProvider(t *testing.T, dp *dataPlane) (*Provider, resource.Deployment, resource.Environment) {
	t.Helper()
	d := resource.Deployment{Metadata: resource.Metadata{ID: "dep-1"}, Spec: resource.DeploymentSpec{Component: "comp-1"}}
	env := resource.Environment{Metadata: resource.Metadata{ID: "env-1"}}
	client := newTestKubeClient(t, dp.handler(t, deploymentBindingName("comp-1", "env-1")))
	return NewProvider(client, testGateway), d, env
}

func TestRuntimeObjects(t *testing.T) {
	dp := &dataPlane{rendered: true, pods: []string{"cmp-x-dep-abc-1", "crash-2"}}
	p, d, env := newRuntimeProvider(t, dp)
	got, err := p.RuntimeObjects(context.Background(), d, env)
	if err != nil {
		t.Fatal(err)
	}
	want := []reconcile.RuntimeObject{
		{Kind: "Deployment", Name: "cmp-x-dep", Health: "Degraded", Message: "Deployment does not have minimum availability."},
		{Kind: "Service", Name: "cmp-x", Health: "Healthy"},
		{Kind: "Pod", Name: "cmp-x-dep-abc-1", Health: "Healthy"},
		{Kind: "Pod", Name: "crash-2", Health: "Degraded", Message: "CrashLoopBackOff: back-off 40s; restarts: 4"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("objects =\n%+v\nwant\n%+v", got, want)
	}

	// Before the platform renders anything, and before any deploy: empty.
	dp.rendered = false
	if got, err := p.RuntimeObjects(context.Background(), d, env); err != nil || len(got) != 0 {
		t.Errorf("not rendered: %v, %v", got, err)
	}
	d.Spec.Component = "never-deployed"
	if got, err := p.RuntimeObjects(context.Background(), d, env); err != nil || len(got) != 0 {
		t.Errorf("not deployed: %v, %v", got, err)
	}
}

func TestPodHealth(t *testing.T) {
	for _, tt := range []struct{ status, health string }{
		{`{"phase":"Pending"}`, "Progressing"},
		{`{"phase":"Running","containerStatuses":[{"ready":false,"state":{"waiting":{"reason":"ContainerCreating"}}}]}`, "Progressing"},
		{`{"phase":"Running","containerStatuses":[{"ready":false,"state":{"waiting":{"reason":"ImagePullBackOff"}}}]}`, "Degraded"},
		{`{"phase":"Failed"}`, "Degraded"},
		{`not json`, "Unknown"},
	} {
		if got, _ := podHealth(podObject(tt.status)); got != tt.health {
			t.Errorf("%s: health %s, want %s", tt.status, got, tt.health)
		}
	}
}

func TestEvents_OnlyTheDeploymentsOwnNewestFirst(t *testing.T) {
	dp := &dataPlane{rendered: true, pods: []string{"cmp-x-dep-abc-1"}, events: `{"items":[
		{"type":"Normal","reason":"Scheduled","message":"assigned","involvedObject":{"kind":"Pod","name":"cmp-x-dep-abc-1"},"eventTime":"2026-10-23T10:00:00.000000Z"},
		{"type":"Normal","reason":"SuccessfulCreate","involvedObject":{"kind":"ReplicaSet","name":"cmp-x-dep-abc"},"lastTimestamp":"2026-10-23T09:59:00Z"},
		{"type":"Warning","reason":"Sibling","involvedObject":{"kind":"Pod","name":"cmp-x-dep-worker-1"},"lastTimestamp":"2026-10-23T10:07:00Z"},
		{"type":"Warning","reason":"BackOff","message":"restarting","count":3,"involvedObject":{"kind":"Pod","name":"cmp-x-dep-abc-1"},"lastTimestamp":"2026-10-23T10:05:00Z"},
		{"type":"Normal","reason":"ScalingReplicaSet","involvedObject":{"kind":"Deployment","name":"cmp-x-dep"},"lastTimestamp":"2026-10-23T09:00:00Z"},
		{"type":"Warning","reason":"BackOff","involvedObject":{"kind":"Pod","name":"cmp-other-dep-1"},"lastTimestamp":"2026-10-23T10:06:00Z"},
		{"type":"Normal","reason":"Old","involvedObject":{"kind":"Service","name":"cmp-x"},"lastTimestamp":"2026-10-22T10:00:00Z"}]}`}
	p, d, env := newRuntimeProvider(t, dp)
	got, err := p.Events(context.Background(), d, env, time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, e := range got {
		reasons = append(reasons, e.Reason)
	}
	if !slices.Equal(reasons, []string{"BackOff", "Scheduled", "SuccessfulCreate", "ScalingReplicaSet"}) {
		t.Errorf("events = %+v", got)
	}
	if got[0].Count != 3 || got[0].ObjectName != "cmp-x-dep-abc-1" || got[0].Type != "Warning" {
		t.Errorf("first event = %+v", got[0])
	}
}

func drain(t *testing.T, s reconcile.LogStream) []reconcile.LogLine {
	t.Helper()
	var out []reconcile.LogLine
	for {
		l, err := s.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, l)
	}
}

func TestLogs_RecentLinesFromEveryInstance(t *testing.T) {
	long := strings.Repeat("y", maxLogLine+500)
	dp := &dataPlane{rendered: true, pods: []string{"cmp-x-dep-abc-1", "cmp-x-dep-abc-2"}, logs: map[string]string{
		"cmp-x-dep-abc-1": "2026-10-23T10:00:00.5Z hello\n2026-10-23T10:00:01Z " + long + "\n",
		"cmp-x-dep-abc-2": "2026-10-23T10:00:02Z from two\nno timestamp",
	}}
	p, d, env := newRuntimeProvider(t, dp)
	s, err := p.Logs(context.Background(), d, env, reconcile.LogOptions{TailLines: 5, Since: 90 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lines := drain(t, s)
	if len(lines) != 4 {
		t.Fatalf("lines = %+v", lines)
	}
	byText := map[string]reconcile.LogLine{}
	for _, l := range lines {
		byText[l.Line[:min(len(l.Line), 10)]] = l
	}
	if l := byText["hello"]; l.Instance != "cmp-x-dep-abc-1" || !l.Time.Equal(time.Date(2026, 10, 23, 10, 0, 0, 5e8, time.UTC)) {
		t.Errorf("hello = %+v", l)
	}
	if l := byText["yyyyyyyyyy"]; len(l.Line) != maxLogLine-len("2026-10-23T10:00:01Z ") {
		t.Errorf("a long line kept %d bytes", len(l.Line))
	}
	if l := byText["no timesta"]; !l.Time.IsZero() || l.Instance != "cmp-x-dep-abc-2" {
		t.Errorf("untimed = %+v", l)
	}
	q := strings.Join(dp.queries, " ")
	for _, want := range []string{"tailLines=5", "sinceSeconds=90", "timestamps=true", "limitBytes="} {
		if !strings.Contains(q, want) {
			t.Errorf("log queries %q lack %s", q, want)
		}
	}
}

func TestLogs_PreviousSkipsInstancesWithoutOne(t *testing.T) {
	dp := &dataPlane{rendered: true, pods: []string{"a", "b"}, logs: map[string]string{"b": "2026-10-23T10:00:00Z before the crash\n"}}
	p, d, env := newRuntimeProvider(t, dp)
	s, err := p.Logs(context.Background(), d, env, reconcile.LogOptions{Previous: true})
	if err != nil {
		t.Fatal(err)
	}
	if lines := drain(t, s); len(lines) != 1 || lines[0].Instance != "b" {
		t.Errorf("lines = %+v", lines)
	}
	// No instance has one: nothing to read, which isn't a failure.
	dp.logs = map[string]string{}
	s, err = p.Logs(context.Background(), d, env, reconcile.LogOptions{Previous: true})
	if err != nil {
		t.Fatal(err)
	}
	if lines := drain(t, s); len(lines) != 0 {
		t.Errorf("lines = %+v", lines)
	}
}

// A partial answer never looks complete: an instance that can't be read,
// or breaks off, ends the stream with its error after the others' lines.
func TestLogs_FailuresSurfaceAfterTheLines(t *testing.T) {
	for name, logs := range map[string]map[string]string{
		"refused":    {"a": "2026-10-23T10:00:00Z fine\n", "b": "FORBIDDEN"},
		"broken off": {"a": "2026-10-23T10:00:00Z fine\n", "b": "2026-10-23T10:00:01Z cut\nBREAK"},
	} {
		t.Run(name, func(t *testing.T) {
			dp := &dataPlane{rendered: true, pods: []string{"a", "b"}, logs: logs}
			p, d, env := newRuntimeProvider(t, dp)
			s, err := p.Logs(context.Background(), d, env, reconcile.LogOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var got []string
			for {
				l, err := s.Next()
				if err != nil {
					if errors.Is(err, io.EOF) {
						t.Fatalf("ended cleanly after %v", got)
					}
					if !strings.Contains(err.Error(), "logs of b") {
						t.Errorf("err = %v", err)
					}
					break
				}
				got = append(got, l.Line)
			}
			if !slices.Contains(got, "fine") {
				t.Errorf("lines before the failure = %v", got)
			}
		})
	}
	// Every instance refused: the error comes back at once.
	dp := &dataPlane{rendered: true, pods: []string{"a"}, logs: map[string]string{"a": "FORBIDDEN"}}
	p, d, env := newRuntimeProvider(t, dp)
	if _, err := p.Logs(context.Background(), d, env, reconcile.LogOptions{}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v", err)
	}
}

func TestLogs_FollowUntilClosed(t *testing.T) {
	dp := &dataPlane{rendered: true, pods: []string{"cmp-x-dep-abc-1"}, logs: map[string]string{"cmp-x-dep-abc-1": "2026-10-23T10:00:00Z start\n"}, follow: make(chan string)}
	p, d, env := newRuntimeProvider(t, dp)
	s, err := p.Logs(context.Background(), d, env, reconcile.LogOptions{Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	if l, err := s.Next(); err != nil || l.Line != "start" {
		t.Fatalf("first = %+v, %v", l, err)
	}
	dp.follow <- "2026-10-23T10:00:05Z later"
	if l, err := s.Next(); err != nil || l.Line != "later" {
		t.Fatalf("followed = %+v, %v", l, err)
	}
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close didn't end the stream")
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("Next after Close = %v", err)
	}
	if !strings.Contains(strings.Join(dp.queries, " "), "follow=true") {
		t.Error("the stream wasn't asked to follow")
	}
}

func TestLogs_NotDeployedIsEmpty(t *testing.T) {
	p, d, env := newRuntimeProvider(t, &dataPlane{})
	d.Spec.Component = "never-deployed"
	s, err := p.Logs(context.Background(), d, env, reconcile.LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if lines := drain(t, s); len(lines) != 0 {
		t.Errorf("lines = %+v", lines)
	}
}

func podObject(status string) kubernetes.Object {
	return kubernetes.Object{Status: json.RawMessage(status)}
}
