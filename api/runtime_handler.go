package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// Log stream limits (specs/020 plan decision 8): following logs holds a
// connection and a platform stream open, so they're capped per caller and
// per server, and end when idle or after a maximum duration. Reconnecting
// is the client's job.
const (
	maxStreamsPerCaller = 3
	maxStreamsPerServer = 20
	streamIdle          = 5 * time.Minute
	streamMax           = 10 * time.Minute
	// maxTailLines caps the recent lines asked of each instance.
	maxTailLines = 1000
	// maxRecentLines caps a non-following answer across all instances,
	// keeping the newest; maxRecentBytes caps what's read to choose them.
	maxRecentLines = 5000
	maxRecentBytes = 4 << 20
	// recentTimeout bounds a non-following read.
	recentTimeout = 30 * time.Second
)

// RuntimeHandler serves what a Deployment runs as, read live from the
// platform: its objects, their events and its logs (spec 020). Reads only,
// so nothing is stored or audited. Objects and events need
// serendip/components/runtime/read on the Deployment, logs need
// serendip/components/logs/read (the route rules).
type RuntimeHandler struct {
	deployments store.Store[resource.DeploymentSpec, resource.DeploymentStatus]
	envs        store.Store[resource.EnvironmentSpec, resource.EnvironmentStatus]
	// runtime is nil when no application platform is configured.
	runtime reconcile.RuntimeProvider
	streams streamLimiter
	// idle and max are streamIdle and streamMax; tests shorten them.
	idle, max time.Duration
}

func NewRuntimeHandler(deployments store.Store[resource.DeploymentSpec, resource.DeploymentStatus], envs store.Store[resource.EnvironmentSpec, resource.EnvironmentStatus], runtime reconcile.RuntimeProvider) *RuntimeHandler {
	return &RuntimeHandler{deployments: deployments, envs: envs, runtime: runtime, idle: streamIdle, max: streamMax,
		streams: streamLimiter{perCaller: maxStreamsPerCaller, total: maxStreamsPerServer, open: map[string]int{}}}
}

func (h *RuntimeHandler) Register(mux Mux) {
	mux.HandleFunc("GET /v1/deployments/{id}/runtime", h.Objects)
	mux.HandleFunc("GET /v1/deployments/{id}/events", h.Events)
	mux.HandleFunc("GET /v1/deployments/{id}/logs", h.Logs)
}

// streamLimiter counts open log streams per caller and in all.
type streamLimiter struct {
	mu        sync.Mutex
	perCaller int
	total     int
	open      map[string]int
	all       int
}

func (l *streamLimiter) acquire(caller string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.all >= l.total || l.open[caller] >= l.perCaller {
		return false
	}
	l.open[caller]++
	l.all++
	return true
}

func (l *streamLimiter) release(caller string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all--
	if l.open[caller]--; l.open[caller] <= 0 {
		delete(l.open, caller)
	}
}

// target reads the Deployment and its Environment, answering the error
// itself when it can't.
func (h *RuntimeHandler) target(w http.ResponseWriter, r *http.Request) (resource.Deployment, resource.Environment, bool) {
	reqID := audit.RequestIDFrom(r.Context())
	if h.runtime == nil {
		WriteProblem(w, http.StatusServiceUnavailable, "service unavailable", "no application platform is configured", reqID)
		return resource.Deployment{}, resource.Environment{}, false
	}
	d, err := h.deployments.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, r, err)
		return resource.Deployment{}, resource.Environment{}, false
	}
	env, err := h.envs.Get(r.Context(), owner(d.Metadata, resource.KindEnvironment))
	if err != nil {
		writeStoreErr(w, r, err)
		return resource.Deployment{}, resource.Environment{}, false
	}
	return d, env, true
}

// platformFailed answers a failed platform read: 503 while the platform
// is being set up, else 502. Never the error's text, which can name
// platform internals; it's logged for the operator.
func platformFailed(w http.ResponseWriter, r *http.Request, err error) {
	reqID := audit.RequestIDFrom(r.Context())
	if errors.Is(err, reconcile.ErrProviderNotReady) {
		WriteProblem(w, http.StatusServiceUnavailable, "service unavailable", "the application platform is still being set up; retry shortly", reqID)
		return
	}
	log.Printf("api: %s %s: platform read failed (request %s): %v", r.Method, r.URL.Path, reqID, err)
	WriteProblem(w, http.StatusBadGateway, "bad gateway", "the application platform couldn't be read; retry later, quoting the request ID", reqID)
}

// Objects answers {"objects": [...]}: the rendered objects with their
// health, then each running instance.
func (h *RuntimeHandler) Objects(w http.ResponseWriter, r *http.Request) {
	d, env, ok := h.target(w, r)
	if !ok {
		return
	}
	objects, err := h.runtime.RuntimeObjects(r.Context(), d, env)
	if err != nil {
		platformFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"objects": nonNil(objects)})
}

// Events answers {"events": [...]}, newest first; ?since=RFC 3339 time
// keeps only newer ones.
func (h *RuntimeHandler) Events(w http.ResponseWriter, r *http.Request) {
	var since time.Time
	if s := r.URL.Query().Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "validation failed", "since: an RFC 3339 time, e.g. 2026-10-23T10:00:00Z", audit.RequestIDFrom(r.Context()))
			return
		}
		since = t
	}
	d, env, ok := h.target(w, r)
	if !ok {
		return
	}
	events, err := h.runtime.Events(r.Context(), d, env, since)
	if err != nil {
		platformFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": nonNil(events)})
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// logOptions reads ?tailLines, ?sinceSeconds, ?previous and ?follow.
func logOptions(r *http.Request) (reconcile.LogOptions, error) {
	q := r.URL.Query()
	var opts reconcile.LogOptions
	if s := q.Get("tailLines"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxTailLines {
			return opts, errors.New("tailLines: a whole number from 1 to " + strconv.Itoa(maxTailLines))
		}
		opts.TailLines = n
	}
	if s := q.Get("sinceSeconds"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return opts, errors.New("sinceSeconds: a whole number of seconds, at least 1")
		}
		opts.Since = time.Duration(n) * time.Second
	}
	for name, dst := range map[string]*bool{"previous": &opts.Previous, "follow": &opts.Follow} {
		if s := q.Get(name); s != "" {
			b, err := strconv.ParseBool(s)
			if err != nil {
				return opts, errors.New(name + ": true or false")
			}
			*dst = b
		}
	}
	if opts.Previous && opts.Follow {
		return opts, errors.New("previous and follow: a previous instance has stopped, so there's nothing to follow")
	}
	return opts, nil
}

// Logs answers recent lines as {"lines": [...], "truncated": bool}, the
// newest 5000 oldest first, or with ?follow=true streams NDJSON: one line
// object per line as it's written, then {"end": reason} when the stream
// ends on its own (idle, maxDuration, ended, or error when the platform
// failed part-way, so a partial stream never looks complete).
func (h *RuntimeHandler) Logs(w http.ResponseWriter, r *http.Request) {
	reqID := audit.RequestIDFrom(r.Context())
	opts, err := logOptions(r)
	if err != nil {
		WriteProblem(w, http.StatusBadRequest, "validation failed", err.Error(), reqID)
		return
	}
	d, env, ok := h.target(w, r)
	if !ok {
		return
	}
	if opts.Follow {
		h.follow(w, r, d, env, opts)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), recentTimeout)
	defer cancel()
	stream, err := h.runtime.Logs(ctx, d, env, opts)
	if err != nil {
		platformFailed(w, r, err)
		return
	}
	defer stream.Close()
	lines, truncated, err := recentLines(stream)
	if err != nil {
		platformFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "truncated": truncated})
}

// recentLines reads a stream of recent lines (within maxRecentBytes),
// orders them by time and keeps the newest maxRecentLines. Instances'
// lines arrive interleaved; an untimed line takes the time of the line
// before it from the same instance, so the order is total.
func recentLines(stream reconcile.LogStream) ([]reconcile.LogLine, bool, error) {
	lines := []reconcile.LogLine{}
	last := map[string]time.Time{}
	size, truncated := 0, false
	for {
		l, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, err
		}
		if size += len(l.Line); size > maxRecentBytes {
			truncated = true
			break
		}
		if l.Time.IsZero() {
			l.Time = last[l.Instance]
		}
		last[l.Instance] = l.Time
		lines = append(lines, l)
	}
	slices.SortStableFunc(lines, func(a, b reconcile.LogLine) int { return a.Time.Compare(b.Time) })
	if len(lines) > maxRecentLines {
		lines, truncated = lines[len(lines)-maxRecentLines:], true
	}
	return lines, truncated, nil
}

func (h *RuntimeHandler) follow(w http.ResponseWriter, r *http.Request, d resource.Deployment, env resource.Environment, opts reconcile.LogOptions) {
	reqID := audit.RequestIDFrom(r.Context())
	caller, ok := callerFrom(r.Context())
	if !ok {
		WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", reqID)
		return
	}
	// The server's write timeout is for ordinary answers; a stream gets
	// its own, just past its maximum. A connection that can't take one
	// can't stream.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Now().Add(h.max + 30*time.Second)); err != nil {
		log.Printf("api: %s %s: can't stream here (request %s): %v", r.Method, r.URL.Path, reqID, err)
		WriteProblem(w, http.StatusInternalServerError, "internal error", "this connection can't stream; read recent lines without follow", reqID)
		return
	}
	if !h.streams.acquire(caller.ID) {
		w.Header().Set("Retry-After", "30")
		WriteProblem(w, http.StatusTooManyRequests, "too many requests",
			"too many log streams open: at most "+strconv.Itoa(h.streams.perCaller)+" per caller and "+strconv.Itoa(h.streams.total)+" in all; close one and retry", reqID)
		return
	}
	defer h.streams.release(caller.ID)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stream, err := h.runtime.Logs(ctx, d, env, opts)
	if err != nil {
		platformFailed(w, r, err)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_ = rc.Flush()

	lines := make(chan reconcile.LogLine)
	ended := make(chan error, 1)
	go func() {
		for {
			l, err := stream.Next()
			if err != nil {
				ended <- err
				return
			}
			select {
			case lines <- l:
			case <-ctx.Done():
				return
			}
		}
	}()

	enc := json.NewEncoder(w)
	idle := time.NewTimer(h.idle)
	defer idle.Stop()
	deadline := time.NewTimer(h.max)
	defer deadline.Stop()
	end := func(reason string) {
		_ = enc.Encode(map[string]string{"end": reason})
		_ = rc.Flush()
	}
	for {
		select {
		case l := <-lines:
			if err := enc.Encode(l); err != nil {
				return // the client went away
			}
			if err := rc.Flush(); err != nil {
				return
			}
			idle.Reset(h.idle)
		case err := <-ended:
			if !errors.Is(err, io.EOF) {
				log.Printf("api: %s %s: log stream failed (request %s): %v", r.Method, r.URL.Path, reqID, err)
				end("error")
				return
			}
			end("ended")
			return
		case <-idle.C:
			end("idle")
			return
		case <-deadline.C:
			end("maxDuration")
			return
		case <-r.Context().Done():
			return
		}
	}
}
