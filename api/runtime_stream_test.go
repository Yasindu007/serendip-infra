package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// chanRuntime streams whatever is sent on lines, and records when a
// stream is closed.
type chanRuntime struct {
	lines chan reconcile.LogLine
	// err, when set, is what a stream ends with once lines is closed.
	err    error
	mu     sync.Mutex
	closed int
}

func (c *chanRuntime) RuntimeObjects(context.Context, resource.Deployment, resource.Environment) ([]reconcile.RuntimeObject, error) {
	return nil, nil
}

func (c *chanRuntime) Events(context.Context, resource.Deployment, resource.Environment, time.Time) ([]reconcile.RuntimeEvent, error) {
	return nil, nil
}

func (c *chanRuntime) Logs(ctx context.Context, _ resource.Deployment, _ resource.Environment, _ reconcile.LogOptions) (reconcile.LogStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	return &chanStream{rt: c, ctx: ctx, cancel: cancel}, nil
}

func (c *chanRuntime) closedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

type chanStream struct {
	rt     *chanRuntime
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func (s *chanStream) Next() (reconcile.LogLine, error) {
	select {
	case l, ok := <-s.rt.lines:
		if !ok {
			if s.rt.err != nil {
				return reconcile.LogLine{}, s.rt.err
			}
			return reconcile.LogLine{}, io.EOF
		}
		return l, nil
	case <-s.ctx.Done():
		return reconcile.LogLine{}, io.EOF
	}
}

func (s *chanStream) Close() error {
	s.once.Do(func() {
		s.cancel()
		s.rt.mu.Lock()
		s.rt.closed++
		s.rt.mu.Unlock()
	})
	return nil
}

// streamServer serves h with the caller named by the X-Caller header, as
// the authorizer would, and returns a Deployment's logs URL.
func streamServer(t *testing.T, rt reconcile.RuntimeProvider, configure func(h *RuntimeHandler)) (*httptest.Server, string) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	envs := sqlite.NewStore[resource.EnvironmentSpec, resource.EnvironmentStatus](db, resource.KindEnvironment)
	deps := sqlite.NewStore[resource.DeploymentSpec, resource.DeploymentStatus](db, resource.KindDeployment)
	env, err := envs.Create(ctx, resource.Environment{Metadata: resource.Metadata{Name: "dev"}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := deps.Create(ctx, resource.Deployment{Metadata: resource.Metadata{Name: "web", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindEnvironment, ID: env.Metadata.ID}}},
		Spec: resource.DeploymentSpec{Component: "c1"}})
	if err != nil {
		t.Fatal(err)
	}
	h := NewRuntimeHandler(deps, envs, rt)
	if configure != nil {
		configure(h)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(withCaller(r.Context(), policy.Principal{ID: r.Header.Get("X-Caller")})))
	}))
	t.Cleanup(srv.Close)
	return srv, srv.URL + "/v1/deployments/" + dep.Metadata.ID + "/logs?follow=true"
}

func follow(t *testing.T, ctx context.Context, url, caller string) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("X-Caller", caller)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestFollow_StreamsNDJSONAndEndsOnClientClose(t *testing.T) {
	rt := &chanRuntime{lines: make(chan reconcile.LogLine)}
	_, url := streamServer(t, rt, nil)
	ctx, cancel := context.WithCancel(context.Background())
	resp := follow(t, ctx, url, "alice")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("answer: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	lines := bufio.NewScanner(resp.Body)
	for _, text := range []string{"one", "two"} {
		rt.lines <- reconcile.LogLine{Instance: "web-1", Line: text}
		if !lines.Scan() {
			t.Fatalf("no line for %s: %v", text, lines.Err())
		}
		var l reconcile.LogLine
		if err := json.Unmarshal(lines.Bytes(), &l); err != nil || l.Line != text || l.Instance != "web-1" {
			t.Fatalf("line = %s, %v", lines.Bytes(), err)
		}
	}
	cancel()
	resp.Body.Close()
	waitFor(t, func() bool { return rt.closedCount() == 1 }, "the platform stream closing after the client left")
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// endOf reads a stream to its end and returns its end reason.
func endOf(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var last map[string]any
	lines := bufio.NewScanner(resp.Body)
	for lines.Scan() {
		last = nil
		json.Unmarshal(lines.Bytes(), &last)
	}
	reason, _ := last["end"].(string)
	return reason
}

func TestFollow_EndsWhenIdleOrTooLong(t *testing.T) {
	rt := &chanRuntime{lines: make(chan reconcile.LogLine)}
	_, url := streamServer(t, rt, func(h *RuntimeHandler) { h.idle = 50 * time.Millisecond })
	if got := endOf(t, follow(t, context.Background(), url, "alice")); got != "idle" {
		t.Errorf("end = %q, want idle", got)
	}

	busy := &chanRuntime{lines: make(chan reconcile.LogLine)}
	_, url = streamServer(t, busy, func(h *RuntimeHandler) { h.max = 150 * time.Millisecond })
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case busy.lines <- reconcile.LogLine{Line: "tick"}:
				time.Sleep(10 * time.Millisecond)
			case <-stop:
				return
			}
		}
	}()
	if got := endOf(t, follow(t, context.Background(), url, "alice")); got != "maxDuration" {
		t.Errorf("end = %q, want maxDuration", got)
	}

	done := &chanRuntime{lines: make(chan reconcile.LogLine)}
	close(done.lines)
	_, url = streamServer(t, done, nil)
	if got := endOf(t, follow(t, context.Background(), url, "alice")); got != "ended" {
		t.Errorf("end = %q, want ended", got)
	}

	// The platform failing part-way says so: a partial stream never
	// looks complete.
	broken := &chanRuntime{lines: make(chan reconcile.LogLine), err: io.ErrUnexpectedEOF}
	close(broken.lines)
	_, url = streamServer(t, broken, nil)
	if got := endOf(t, follow(t, context.Background(), url, "alice")); got != "error" {
		t.Errorf("end = %q, want error", got)
	}
}

type listStream struct {
	lines []reconcile.LogLine
	err   error
}

func (s *listStream) Next() (reconcile.LogLine, error) {
	if len(s.lines) == 0 {
		if s.err != nil {
			return reconcile.LogLine{}, s.err
		}
		return reconcile.LogLine{}, io.EOF
	}
	l := s.lines[0]
	s.lines = s.lines[1:]
	return l, nil
}

func (s *listStream) Close() error { return nil }

func TestRecentLines(t *testing.T) {
	at := func(sec int) time.Time { return time.Date(2026, 10, 23, 10, 0, sec, 0, time.UTC) }
	// Two instances, interleaved, with an untimed continuation line.
	got, truncated, err := recentLines(&listStream{lines: []reconcile.LogLine{
		{Time: at(3), Instance: "b", Line: "b3"},
		{Time: at(1), Instance: "a", Line: "a1"},
		{Instance: "a", Line: "a1-cont"},
		{Time: at(2), Instance: "a", Line: "a2"},
	}})
	var order []string
	for _, l := range got {
		order = append(order, l.Line)
	}
	if err != nil || truncated || strings.Join(order, " ") != "a1 a1-cont a2 b3" {
		t.Errorf("order = %v, truncated %v, %v", order, truncated, err)
	}

	// More than the cap: the newest are kept, whichever instance won the race.
	var many []reconcile.LogLine
	for i := range maxRecentLines + 10 {
		many = append(many, reconcile.LogLine{Time: at(0).Add(time.Duration(maxRecentLines+10-i) * time.Millisecond), Instance: "x", Line: "l"})
	}
	got, truncated, _ = recentLines(&listStream{lines: many})
	if len(got) != maxRecentLines || !truncated || !got[len(got)-1].Time.Equal(at(0).Add(time.Duration(maxRecentLines+10)*time.Millisecond)) {
		t.Errorf("kept %d, truncated %v, newest %v", len(got), truncated, got[len(got)-1].Time)
	}

	// A byte budget, and a failure part-way.
	big := strings.Repeat("z", maxRecentBytes/2+1)
	if got, truncated, _ := recentLines(&listStream{lines: []reconcile.LogLine{{Line: big}, {Line: big}, {Line: big}}}); len(got) != 1 || !truncated {
		t.Errorf("over the byte budget: kept %d, truncated %v", len(got), truncated)
	}
	if _, _, err := recentLines(&listStream{lines: []reconcile.LogLine{{Line: "x"}}, err: io.ErrUnexpectedEOF}); err == nil {
		t.Error("a failure part-way was dropped")
	}
}

func TestFollow_Caps(t *testing.T) {
	rt := &chanRuntime{lines: make(chan reconcile.LogLine)}
	_, url := streamServer(t, rt, func(h *RuntimeHandler) { h.streams.total = 4 })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var open []*http.Response
	for range maxStreamsPerCaller {
		resp := follow(t, ctx, url, "alice")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %d: %d", len(open)+1, resp.StatusCode)
		}
		open = append(open, resp)
	}
	// alice is at her limit; bob isn't, until the server is.
	if resp := follow(t, ctx, url, "alice"); resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("alice's fourth: %d", resp.StatusCode)
	}
	bob := follow(t, ctx, url, "bob")
	if bob.StatusCode != http.StatusOK {
		t.Fatalf("bob's first: %d", bob.StatusCode)
	}
	if resp := follow(t, ctx, url, "carol"); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("past the server's limit: %d", resp.StatusCode)
	}
	// Closing one frees its place.
	open[0].Body.Close()
	waitFor(t, func() bool { return rt.closedCount() >= 1 }, "a stream closing")
	waitFor(t, func() bool {
		resp := follow(t, ctx, url, "alice")
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, "alice's freed place")
}
