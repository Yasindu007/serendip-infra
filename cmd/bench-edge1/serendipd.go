package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const apiKey = "bench"

// daemon runs serendipd as a child process with its own database and log.
type daemon struct {
	bin  string
	env  []string
	log  string
	addr string

	cmd *exec.Cmd
	// done is closed when the process exits; exitErr is valid after that.
	// A closed channel (not a one-shot send) so both the start-up wait and
	// a later stop can observe the exit without deadlocking.
	done    chan struct{}
	exitErr error
}

// start launches serendipd and waits for its "listening on" line, which
// it only logs after every provider (and so every install) is ready.
func (d *daemon) start(ctx context.Context, timeout time.Duration) (time.Duration, error) {
	logFile, err := os.Create(d.log)
	if err != nil {
		return 0, err
	}
	d.cmd = exec.Command(d.bin)
	d.cmd.Env = append(os.Environ(), d.env...)
	d.cmd.Stdout, d.cmd.Stderr = logFile, logFile
	began := time.Now()
	if err := d.cmd.Start(); err != nil {
		logFile.Close()
		return 0, fmt.Errorf("start %s: %w", d.bin, err)
	}
	d.done = make(chan struct{})
	cmd, done := d.cmd, d.done
	go func() {
		d.exitErr = cmd.Wait()
		logFile.Close()
		close(done)
	}()
	if err := waitForLogLine(ctx, d.log, "listening on", timeout, done, func() error { return d.exitErr }); err != nil {
		return 0, err
	}
	return time.Since(began), nil
}

func (d *daemon) stop() {
	if d.cmd == nil || d.cmd.Process == nil {
		return
	}
	_ = d.cmd.Process.Kill()
	<-d.done
	d.cmd = nil
}

func (d *daemon) pid() int { return d.cmd.Process.Pid }

// waitForLogLine polls path until it contains want, the process exits, or
// timeout passes. An early exit returns the log's tail, which holds
// serendipd's fatal error.
func waitForLogLine(ctx context.Context, path, want string, timeout time.Duration, done <-chan struct{}, exitErr func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		data, _ := os.ReadFile(path)
		if bytes.Contains(data, []byte(want)) {
			return nil
		}
		select {
		case <-done:
			data, _ = os.ReadFile(path)
			return fmt.Errorf("process exited before %q (%v); log tail:\n%s", want, exitErr(), tail(data, 1500))
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no %q after %s; log tail:\n%s", want, timeout, tail(data, 1500))
		}
	}
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

// api is a minimal client for serendipd's HTTP API.
type api struct {
	base string
	http *http.Client
}

func (a api) do(ctx context.Context, method, path string, body any) (map[string]any, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: %d %.300s", method, path, resp.StatusCode, raw)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s %s: decode: %w", method, path, err)
	}
	return out, nil
}

func idOf(obj map[string]any) string {
	meta, _ := obj["metadata"].(map[string]any)
	id, _ := meta["id"].(string)
	return id
}

// conditionReason returns the reason of obj's condition typ, or "".
func conditionReason(obj map[string]any, typ string) string {
	st, _ := obj["status"].(map[string]any)
	conds, _ := st["conditions"].([]any)
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m["type"] == typ {
			r, _ := m["reason"].(string)
			return r
		}
	}
	return ""
}

func (a api) waitReason(ctx context.Context, path, typ, reason string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		obj, err := a.do(ctx, http.MethodGet, path, nil)
		if err == nil {
			last = conditionReason(obj, typ)
			if last == reason {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: %s=%q after %s, want %q", path, typ, last, timeout, reason)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// deployWorkload creates one Application and n exposed Components and
// returns the Application's ID and each Component's create→Serving time.
func (a api) deployWorkload(ctx context.Context, n int, image string) (string, []Duration, error) {
	app, err := a.do(ctx, http.MethodPost, "/v1/applications", map[string]any{"metadata": map[string]any{"name": "bench"}})
	if err != nil {
		return "", nil, err
	}
	appID := idOf(app)
	if appID == "" {
		return "", nil, fmt.Errorf("create application: response carried no metadata.id")
	}
	if err := a.waitReason(ctx, "/v1/applications/"+appID, "Ready", "Deployed", 5*time.Minute); err != nil {
		return appID, nil, err
	}
	type pending struct {
		id      string
		created time.Time
	}
	var comps []pending
	for i := 0; i < n; i++ {
		comp, err := a.do(ctx, http.MethodPost, "/v1/components", map[string]any{
			"metadata": map[string]any{
				"name":      fmt.Sprintf("bench-%d", i+1),
				"ownerRefs": []any{map[string]any{"kind": "Application", "id": appID}},
			},
			"spec": map[string]any{"image": image, "ports": []any{map[string]any{"name": "http", "port": 80, "expose": true}}},
		})
		if err != nil {
			return appID, nil, err
		}
		id := idOf(comp)
		if id == "" {
			return appID, nil, fmt.Errorf("create component: response carried no metadata.id")
		}
		comps = append(comps, pending{id: id, created: time.Now()})
	}

	// Poll every Component in one loop and record when each was first seen
	// Serving, so one slow Component doesn't inflate the others' times.
	serving := make([]Duration, len(comps))
	done := make([]bool, len(comps))
	remaining := len(comps)
	deadline := time.Now().Add(10 * time.Minute)
	for remaining > 0 {
		for i, c := range comps {
			if done[i] {
				continue
			}
			obj, err := a.do(ctx, http.MethodGet, "/v1/components/"+c.id, nil)
			if err == nil && conditionReason(obj, "Reachable") == "Serving" {
				serving[i] = Duration(time.Since(c.created))
				done[i] = true
				remaining--
			}
		}
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			return appID, nil, fmt.Errorf("%d of %d components not Serving after 10m", remaining, len(comps))
		}
		select {
		case <-ctx.Done():
			return appID, nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return appID, serving, nil
}

func (a api) deleteApplication(ctx context.Context, appID string) error {
	_, err := a.do(ctx, http.MethodDelete, "/v1/applications/"+appID+"?cascade=true", nil)
	if err != nil && strings.Contains(err.Error(), " 404 ") {
		return nil
	}
	return err
}
