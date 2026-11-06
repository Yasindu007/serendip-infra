package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/api/catalog"
)

func followClient(t *testing.T, h http.HandlerFunc) *apiClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &apiClient{server: srv.URL, hc: newHTTPClient(), clientName: "cli",
		bearer: func(context.Context) (string, error) { return "tok", nil }}
}

func logsOp(t *testing.T) catalog.Operation {
	t.Helper()
	return mustOp(t, "deployments_logs")
}

func TestFollowPrintsLinesUntilTheServerEnds(t *testing.T) {
	var query string
	c := followClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Write([]byte(`{"time":"2026-10-23T10:00:00Z","instance":"web-1","line":"hello"}` + "\n"))
		w.(http.Flusher).Flush()
		w.Write([]byte(`{"instance":"web-2","line":"no time"}` + "\n" + `{"end":"idle"}` + "\n"))
	})
	var out, errOut bytes.Buffer
	code := runOperation(context.Background(), c, logsOp(t), map[string]any{"id": "d1", "follow": true, "tailLines": 5}, globals{}, &out, &errOut)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(query, "follow=true") || !strings.Contains(query, "tailLines=5") {
		t.Errorf("query = %s", query)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], " web-1 hello") || lines[1] != "- web-2 no time" {
		t.Errorf("output = %q", out.String())
	}
	if !strings.Contains(errOut.String(), "run it again to keep following") {
		t.Errorf("stderr = %q", errOut.String())
	}

	// --json passes the stream through.
	out.Reset()
	runOperation(context.Background(), c, logsOp(t), map[string]any{"id": "d1", "follow": true}, globals{json: true}, &out, &errOut)
	if !strings.HasPrefix(out.String(), `{"time":"2026-10-23T10:00:00Z"`) {
		t.Errorf("json output = %q", out.String())
	}
}

// A stream that stops without its closing reason, or ends in error, is
// a failure: some lines may be missing.
func TestFollowReportsABrokenStream(t *testing.T) {
	for name, body := range map[string]string{
		"cut off":        `{"line":"a"}` + "\n",
		"platform error": `{"line":"a"}` + "\n" + `{"end":"error"}` + "\n",
		"not a log line": "<html>\n",
	} {
		t.Run(name, func(t *testing.T) {
			c := followClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) })
			var out, errOut bytes.Buffer
			if code := runOperation(context.Background(), c, logsOp(t), map[string]any{"id": "d1", "follow": true}, globals{}, &out, &errOut); code != exitError {
				t.Errorf("exit %d, stderr %q", code, errOut.String())
			}
		})
	}
}

func TestFollowRefused(t *testing.T) {
	c := followClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"title":"too many requests","status":429,"detail":"too many log streams open"}`))
	})
	var out, errOut bytes.Buffer
	if code := runOperation(context.Background(), c, logsOp(t), map[string]any{"id": "d1", "follow": true}, globals{}, &out, &errOut); code == exitOK || !strings.Contains(errOut.String(), "too many log streams") {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
}

// Ctrl-C stops following at once, and isn't an error. Nothing but the
// server or ctx ends a followed stream (no overall time limit).
func TestFollowStopsOnInterrupt(t *testing.T) {
	c := followClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"line":"first"}` + "\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut bytes.Buffer
	done := make(chan int)
	go func() {
		done <- runOperation(ctx, c, logsOp(t), map[string]any{"id": "d1", "follow": true}, globals{}, &out, &errOut)
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case code := <-done:
		if code != exitOK || !strings.Contains(out.String(), "first") {
			t.Errorf("exit %d, output %q, stderr %q", code, out.String(), errOut.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupting didn't stop following")
	}
}

// Without --follow, logs are the ordinary one-shot read; and MCP never
// sees follow.
func TestFollowIsCLIOnly(t *testing.T) {
	op := logsOp(t)
	if following(op, map[string]any{"id": "d1"}) || !following(op, map[string]any{"id": "d1", "follow": true}) {
		t.Error("following() misreads the arguments")
	}
	for _, op := range []catalog.Operation{op, mustOp(t, "builds_logs")} {
		mcpOp := op.ForMCP()
		if _, ok := mcpOp.InputSchema()["properties"].(map[string]any)["follow"]; ok {
			t.Errorf("the MCP tool %s offers follow", op.Name)
		}
		if _, err := mcpOp.Request(map[string]any{"id": "d1", "follow": true}); err == nil {
			t.Errorf("the MCP tool %s accepted follow", op.Name)
		}
	}
	args, err := parseArgs(op, []string{"d1", "--follow", "--tailLines", "5"}, strings.NewReader(""))
	if err != nil || !following(op, args) || args["id"] != "d1" {
		t.Errorf("serendip deployments logs d1 --follow: %v, %v", args, err)
	}
	if following(mustOp(t, "deployments_events"), map[string]any{"follow": true}) {
		t.Error("an operation without follow streams")
	}
}

// A workload's log line can't drive the terminal.
func TestSafe(t *testing.T) {
	for in, want := range map[string]string{
		"plain\ttext":          "plain\ttext",
		"\x1b]52;c;aGk=\a":     `\x1b]52;c;aGk=\x07`,
		"over\rwrite":          `over\x0dwrite`,
		"c1 \u009b2J":          `c1 \x9b2J`,
		"Stream ended: fake\n": `Stream ended: fake\x0a`,
	} {
		if got := safe(in); got != want {
			t.Errorf("safe(%q) = %q, want %q", in, got, want)
		}
	}
}

// failingWriter fails every write with err.
type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

// When the output can't be written, following stops: quietly for a closed
// pipe (| head), as an error otherwise (T016 review).
func TestFollowStopsWhenTheOutputFails(t *testing.T) {
	c := followClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"instance":"web-1","line":"a"}` + "\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // the CLI must stop on its own, not wait for the server
	})
	for _, tt := range []struct {
		err  error
		want int
	}{{syscall.EPIPE, exitOK}, {errors.New("disk full"), exitError}} {
		var errOut bytes.Buffer
		done := make(chan int, 1)
		go func() {
			done <- runOperation(context.Background(), c, logsOp(t), map[string]any{"id": "d1", "follow": true}, globals{}, failingWriter{tt.err}, &errOut)
		}()
		select {
		case code := <-done:
			if code != tt.want {
				t.Errorf("%v: exit %d, want %d (%s)", tt.err, code, tt.want, errOut.String())
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%v: still following after the output failed", tt.err)
		}
	}
}

// A followed build's logs end with the build (spec 022).
func TestFollowBuildLogsEndWithTheBuild(t *testing.T) {
	var path string
	c := followClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path + "?" + r.URL.RawQuery
		w.Write([]byte(`{"instance":"build","line":"STEP 1/3"}` + "\n" + `{"end":"ended"}` + "\n"))
	})
	var out, errOut bytes.Buffer
	if code := runOperation(context.Background(), c, mustOp(t, "builds_logs"), map[string]any{"id": "b1", "follow": true}, globals{}, &out, &errOut); code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if path != "/v1/builds/b1/logs?follow=true" || out.String() != "- build STEP 1/3\n" || !strings.Contains(errOut.String(), "the build ended") {
		t.Errorf("path %q, out %q, stderr %q", path, out.String(), errOut.String())
	}
}
