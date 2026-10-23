package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// fakeRuntime answers every Deployment with the same objects, events and
// logs; err, when set, fails every read.
type fakeRuntime struct {
	err   error
	lines []reconcile.LogLine
	got   reconcile.LogOptions
}

func (f *fakeRuntime) RuntimeObjects(context.Context, resource.Deployment, resource.Environment) ([]reconcile.RuntimeObject, error) {
	return []reconcile.RuntimeObject{{Kind: "Pod", Name: "web-1", Health: "Healthy"}}, f.err
}

func (f *fakeRuntime) Events(_ context.Context, _ resource.Deployment, _ resource.Environment, since time.Time) ([]reconcile.RuntimeEvent, error) {
	if !since.IsZero() {
		return nil, f.err
	}
	return []reconcile.RuntimeEvent{{Reason: "Started", ObjectKind: "Pod", ObjectName: "web-1"}}, f.err
}

func (f *fakeRuntime) Logs(_ context.Context, _ resource.Deployment, _ resource.Environment, opts reconcile.LogOptions) (reconcile.LogStream, error) {
	f.got = opts
	if f.err != nil {
		return nil, f.err
	}
	return &sliceStream{lines: f.lines}, nil
}

type sliceStream struct{ lines []reconcile.LogLine }

func (s *sliceStream) Next() (reconcile.LogLine, error) {
	if len(s.lines) == 0 {
		return reconcile.LogLine{}, io.EOF
	}
	l := s.lines[0]
	s.lines = s.lines[1:]
	return l, nil
}

func (s *sliceStream) Close() error { return nil }

func (d *deployWorld) deployed(t *testing.T) string {
	t.Helper()
	got := d.do("tok-owner", http.MethodPost, "/v1/deployments", d.deployment(d.dev, ""))
	if got.Code != http.StatusCreated {
		t.Fatalf("deploy: %d %s", got.Code, got.Body)
	}
	return decodeDeployment(t, got.Body.Bytes()).Metadata.ID
}

func TestRuntime_ReadsAndPermissions(t *testing.T) {
	at := func(sec int) time.Time { return time.Date(2026, 10, 23, 10, 0, sec, 0, time.UTC) }
	rt := &fakeRuntime{lines: []reconcile.LogLine{{Time: at(2), Instance: "b", Line: "two"}, {Time: at(1), Instance: "a", Line: "one"}}}
	d := newDeployWorld(t, api.WithRuntime(rt))
	id := d.deployed(t)

	got := d.do("tok-reader", http.MethodGet, "/v1/deployments/"+id+"/runtime", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"name":"web-1"`) {
		t.Errorf("runtime: %d %s", got.Code, got.Body)
	}
	got = d.do("tok-reader", http.MethodGet, "/v1/deployments/"+id+"/events", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"reason":"Started"`) {
		t.Errorf("events: %d %s", got.Code, got.Body)
	}
	if got := d.do("tok-reader", http.MethodGet, "/v1/deployments/"+id+"/events?since=yesterday", nil); got.Code != http.StatusBadRequest {
		t.Errorf("a bad since: %d", got.Code)
	}

	// Logs are their own permission: a Reader is refused, a Log Reader isn't.
	got = d.do("tok-reader", http.MethodGet, "/v1/deployments/"+id+"/logs", nil)
	if got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "serendip/components/logs/read") {
		t.Errorf("Reader reads logs: %d %s", got.Code, got.Body)
	}
	got = d.do("tok-logreader", http.MethodGet, "/v1/deployments/"+id+"/logs?tailLines=20&sinceSeconds=60", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("Log Reader reads logs: %d %s", got.Code, got.Body)
	}
	var out struct {
		Lines []reconcile.LogLine `json:"lines"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &out); err != nil || len(out.Lines) != 2 || out.Lines[0].Line != "one" {
		t.Errorf("lines, oldest first = %+v, %v", out.Lines, err)
	}
	if rt.got.TailLines != 20 || rt.got.Since != time.Minute || rt.got.Follow {
		t.Errorf("options passed on = %+v", rt.got)
	}
	for _, q := range []string{"tailLines=0", "tailLines=1001", "sinceSeconds=x", "follow=maybe", "previous=true&follow=true"} {
		if got := d.do("tok-logreader", http.MethodGet, "/v1/deployments/"+id+"/logs?"+q, nil); got.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, got.Code)
		}
	}
	// No read of another Project's Deployments.
	if got := d.do("tok-logreader", http.MethodGet, "/v1/deployments/missing/logs", nil); got.Code != http.StatusNotFound && got.Code != http.StatusForbidden {
		t.Errorf("a missing Deployment: %d", got.Code)
	}
}

func TestRuntime_PlatformFailures(t *testing.T) {
	rt := &fakeRuntime{err: reconcile.ErrProviderNotReady}
	d := newDeployWorld(t, api.WithRuntime(rt))
	id := d.deployed(t)
	if got := d.do("tok-owner", http.MethodGet, "/v1/deployments/"+id+"/runtime", nil); got.Code != http.StatusServiceUnavailable {
		t.Errorf("not ready: %d", got.Code)
	}
	rt.err = io.ErrUnexpectedEOF
	got := d.do("tok-owner", http.MethodGet, "/v1/deployments/"+id+"/logs", nil)
	if got.Code != http.StatusBadGateway || strings.Contains(got.Body.String(), "unexpected EOF") {
		t.Errorf("a platform error: %d %s (no internal text)", got.Code, got.Body)
	}

	// No platform configured at all.
	bare := newDeployWorld(t)
	bareID := bare.deployed(t)
	if got := bare.do("tok-owner", http.MethodGet, "/v1/deployments/"+bareID+"/runtime", nil); got.Code != http.StatusServiceUnavailable {
		t.Errorf("no platform: %d", got.Code)
	}
}
