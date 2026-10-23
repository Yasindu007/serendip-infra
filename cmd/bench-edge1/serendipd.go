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

// readyLine is what serendipd logs once every capability's background
// setup has succeeded (spec 014). "listening on" now comes first, before
// any install, so it no longer marks a finished cold start.
const readyLine = "serendipd: all capabilities ready"

// start launches serendipd and waits for readyLine.
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
	if err := waitForLogLine(ctx, d.log, readyLine, timeout, done, func() error { return d.exitErr }); err != nil {
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

// workload is what deployWorkload created, for the polling phase and the
// clean-up.
type workload struct {
	project, env, app string
	deployments       []string
}

// deployWorkload deploys n exposed Components the way spec 020 does: a
// Project with one Environment, an Application in the Project, its
// Components, and a Deployment of each into the Environment. It returns
// what it created and each Component's create→Serving time, Serving
// being its Deployment's first published endpoint.
func (a api) deployWorkload(ctx context.Context, n int, image string) (workload, []Duration, error) {
	var w workload
	create := func(path string, body map[string]any) (string, error) {
		obj, err := a.do(ctx, http.MethodPost, path, body)
		if err != nil {
			return "", err
		}
		id := idOf(obj)
		if id == "" {
			return "", fmt.Errorf("POST %s: response carried no metadata.id", path)
		}
		return id, nil
	}
	owned := func(name, kind, id string) map[string]any {
		return map[string]any{"name": name, "ownerRefs": []any{map[string]any{"kind": kind, "id": id}}}
	}
	var err error
	if w.project, err = create("/v1/projects", map[string]any{"metadata": map[string]any{"name": "bench"}}); err != nil {
		return w, nil, err
	}
	if w.env, err = create("/v1/environments", map[string]any{"metadata": owned("bench", "Project", w.project)}); err != nil {
		return w, nil, err
	}
	if w.app, err = create("/v1/applications", map[string]any{"metadata": owned("bench", "Project", w.project)}); err != nil {
		return w, nil, err
	}
	if err := a.waitReason(ctx, "/v1/applications/"+w.app, "Ready", "Deployed", 5*time.Minute); err != nil {
		return w, nil, err
	}
	type pending struct {
		comp, dep string
		created   time.Time
	}
	var comps []pending
	for i := 0; i < n; i++ {
		id, err := create("/v1/components", map[string]any{
			"metadata": owned(fmt.Sprintf("bench-%d", i+1), "Application", w.app),
			"spec":     map[string]any{"image": image, "ports": []any{map[string]any{"name": "http", "port": 80, "expose": true}}},
		})
		if err != nil {
			return w, nil, err
		}
		comps = append(comps, pending{comp: id, created: time.Now()})
	}

	// Poll every Component in one loop: deploy each once it's released,
	// and record when its Deployment first publishes an endpoint, so one
	// slow Component doesn't inflate the others' times.
	serving := make([]Duration, len(comps))
	remaining := len(comps)
	deadline := time.Now().Add(10 * time.Minute)
	for remaining > 0 {
		for i := range comps {
			c := &comps[i]
			switch {
			case serving[i] > 0:
			case c.dep == "":
				obj, err := a.do(ctx, http.MethodGet, "/v1/components/"+c.comp, nil)
				if err != nil || !conditionTrue(obj, "Ready") {
					continue
				}
				if c.dep, err = create("/v1/deployments", map[string]any{
					"metadata": owned(fmt.Sprintf("bench-%d", i+1), "Environment", w.env),
					"spec":     map[string]any{"component": c.comp},
				}); err != nil {
					return w, nil, err
				}
				w.deployments = append(w.deployments, c.dep)
			default:
				obj, err := a.do(ctx, http.MethodGet, "/v1/deployments/"+c.dep, nil)
				if err == nil && hasEndpoint(obj) {
					serving[i] = Duration(time.Since(c.created))
					remaining--
				}
			}
		}
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			return w, nil, fmt.Errorf("%d of %d components not Serving after 10m", remaining, len(comps))
		}
		select {
		case <-ctx.Done():
			return w, nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return w, serving, nil
}

func conditionTrue(obj map[string]any, typ string) bool {
	st, _ := obj["status"].(map[string]any)
	conds, _ := st["conditions"].([]any)
	for _, c := range conds {
		if m, _ := c.(map[string]any); m["type"] == typ {
			return m["status"] == "True"
		}
	}
	return false
}

func hasEndpoint(obj map[string]any) bool {
	st, _ := obj["status"].(map[string]any)
	eps, _ := st["endpoints"].([]any)
	return len(eps) > 0
}

// componentIDs lists the Components owned by appID. List endpoints answer
// a JSON array, so this doesn't go through do.
func (a api) componentIDs(ctx context.Context, appID string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+"/v1/components?ownerId="+appID, nil)
	if err != nil {
		return nil, fmt.Errorf("list components: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list components: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list components: %s", resp.Status)
	}
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode components: %w", err)
	}
	var ids []string
	for _, c := range list {
		if id := idOf(c); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// deleteWorkload removes what deployWorkload created, Deployments first
// (they depend on the Components), then the Application with its
// Components, the Environment and the Project. A 404 is already gone.
func (a api) deleteWorkload(ctx context.Context, w workload) error {
	paths := []string{}
	for _, d := range w.deployments {
		paths = append(paths, "/v1/deployments/"+d)
	}
	for _, p := range []struct{ path, id string }{{"/v1/applications/", w.app}, {"/v1/environments/", w.env}, {"/v1/projects/", w.project}} {
		if p.id != "" {
			paths = append(paths, p.path+p.id+"?cascade=true")
		}
	}
	for _, p := range paths {
		if _, err := a.do(ctx, http.MethodDelete, p, nil); err != nil && !strings.Contains(err.Error(), " 404 ") {
			return err
		}
	}
	return nil
}
