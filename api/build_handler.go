package api

import (
	"cmp"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// buildCapability is the capability the builds package provides.
const buildCapability = "build"

// cancelRetries bounds re-reading a Build whose cancel lost a write race
// with the build reconciler's status update.
const cancelRetries = 3

// BuildHandler serves a source Component's builds (specs/022-builds plan
// decision 11). Triggering creates a Build and cancelling sets its
// spec.cancel, both through the store, so both are audited and the build
// reconciler does the work. Runs, logs and events are read; a Build has no
// delete of its own, since it goes with its Component.
type BuildHandler struct {
	builds store.Store[resource.BuildSpec, resource.BuildStatus]
	comps  store.Store[resource.ComponentSpec, resource.ComponentStatus]
	// provider is nil when no builds package is wired; its logs and
	// events then answer 503. Runs are read from the store either way.
	provider reconcile.BuildProvider
	// ready says why the build capability can't take a build now.
	ready func() error
	// streams and recent are the Deployment logs' caps, shared, so build
	// logs don't add a second allowance.
	streams, recent *streamLimiter
	idle, max       time.Duration
	// stored reads a finished run's logs once its pods are gone
	// (specs/023-observability plan decision 10); nil without the
	// observability package wired.
	stored reconcile.ObservabilityProvider
	// storedReady says why stored logs can't be read now.
	storedReady func() error
}

func (h *BuildHandler) Register(mux Mux) {
	mux.HandleFunc("POST /v1/components/{id}/builds", h.Trigger)
	mux.HandleFunc("GET /v1/components/{id}/builds", h.List)
	mux.HandleFunc("GET /v1/builds/{id}", h.Get)
	mux.HandleFunc("POST /v1/builds/{id}/cancel", h.Cancel)
	mux.HandleFunc("GET /v1/builds/{id}/events", h.Events)
	mux.HandleFunc("GET /v1/builds/{id}/logs", h.Logs)
}

func (h *BuildHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var rf *deployRefusal
	if errors.As(err, &rf) {
		WriteProblem(w, rf.status, rf.title, rf.detail, audit.RequestIDFrom(r.Context()))
		return
	}
	writeStoreErr(w, r, err)
}

// buildReadiness says why a build can't be asked for now: 422 naming the
// package while the build capability isn't ready. The package's name comes
// from the catalogue.
func buildReadiness(providers *provider.Registry, packages reconcile.Catalogue) func() error {
	var names []string
	for _, p := range packages {
		if p.Capability == buildCapability {
			names = append(names, p.Provider)
		}
	}
	slices.Sort(names)
	name := "the builds package"
	if len(names) > 0 {
		name += " (" + strings.Join(names, " or ") + ")"
	}
	return func() error {
		var rd provider.Readiness
		if providers != nil {
			rd = providers.Readiness()[buildCapability]
		}
		if rd.Ready {
			return nil
		}
		detail := "building from source needs " + name + " installed and ready"
		if rd.Reason != "" {
			detail += "; it's " + rd.Reason
		} else {
			detail += "; install it by creating a ProviderInstance naming it (GET /v1/packages)"
		}
		return deployRefuse(http.StatusUnprocessableEntity, "build capability not ready", "%s", detail)
	}
}

// Trigger creates a Build of the Component, at the body's optional
// commit, else the branch's head when it starts.
func (h *BuildHandler) Trigger(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Commit string `json:"commit"`
	}
	if !decodeOptionalBody(w, r, &body) {
		return
	}
	comp, err := h.comps.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if comp.Spec.Source == nil {
		h.fail(w, r, deployRefuse(http.StatusConflict, "conflict", "Component %s runs an image, not source; only a Component built from source has builds", comp.Metadata.Name))
		return
	}
	if err := h.ready(); err != nil {
		h.fail(w, r, err)
		return
	}
	b := resource.Build{
		Metadata: resource.Metadata{Kind: resource.KindBuild,
			OwnerRefs: []resource.OwnerRef{{Kind: resource.KindComponent, ID: comp.Metadata.ID}}},
		Spec: resource.BuildSpec{Commit: body.Commit},
	}
	if err := resource.ValidateBuild(b.Metadata, b.Spec); err != nil {
		h.fail(w, r, err)
		return
	}
	// Named by its commit, or when it was asked for, so lists tell builds
	// apart; names aren't unique, IDs are.
	b.Metadata.Name = "build-" + time.Now().UTC().Format("20060102-150405")
	if body.Commit != "" {
		b.Metadata.Name = "build-" + body.Commit[:12]
	}
	created, err := h.builds.Create(r.Context(), b)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// List answers the Component's builds, newest first.
func (h *BuildHandler) List(w http.ResponseWriter, r *http.Request) {
	comp, err := h.comps.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	all, err := h.builds.List(r.Context(), store.ListOptions{OwnerID: comp.Metadata.ID})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	slices.SortFunc(all, func(a, b resource.Build) int {
		return cmp.Or(b.Metadata.CreatedAt.Compare(a.Metadata.CreatedAt), cmp.Compare(b.Metadata.ID, a.Metadata.ID))
	})
	writeJSON(w, http.StatusOK, nonNil(all))
}

func (h *BuildHandler) Get(w http.ResponseWriter, r *http.Request) {
	b, err := h.builds.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// Cancel asks for the Build to stop: 200 with the Build, whether or not it
// was already asked; 409 once it has finished.
func (h *BuildHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	for range cancelRetries {
		b, err := h.builds.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if b.Status.Phase.Done() {
			h.fail(w, r, deployRefuse(http.StatusConflict, "conflict", "build %s has already finished (%s)", b.Metadata.Name, b.Status.Phase))
			return
		}
		if b.Spec.Cancel {
			writeJSON(w, http.StatusOK, b)
			return
		}
		b.Spec.Cancel = true
		updated, err := h.builds.Update(r.Context(), b)
		if errors.Is(err, store.ErrConflict) {
			continue // the reconciler wrote its status meanwhile
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
		return
	}
	// Not a 409: nothing is wrong with the request, the build was just
	// busy.
	w.Header().Set("Retry-After", "1")
	WriteProblem(w, http.StatusServiceUnavailable, "build busy", "the build kept changing while it was being cancelled; retry", audit.RequestIDFrom(r.Context()))
}

// run reads the Build and checks the platform still has its run, answering
// the error itself when it can't: 503 without the builds package, 409
// before the run has started, 410 once it has expired. A run's logs and
// events are kept for an hour after it ends (plan decision 5); the Build
// keeps its outcome.
func (h *BuildHandler) run(w http.ResponseWriter, r *http.Request) (resource.Build, bool) {
	b, gone, ok := h.runOrGone(w, r)
	if gone {
		writeExpired(w, r, b)
		return resource.Build{}, false
	}
	return b, ok
}

// runOrGone is run, except that an expired run is returned with gone set,
// unanswered, so its stored logs can be read instead.
func (h *BuildHandler) runOrGone(w http.ResponseWriter, r *http.Request) (resource.Build, bool, bool) {
	reqID := audit.RequestIDFrom(r.Context())
	if h.provider == nil {
		WriteProblem(w, http.StatusServiceUnavailable, "service unavailable", "the builds package isn't installed", reqID)
		return resource.Build{}, false, false
	}
	b, err := h.builds.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return resource.Build{}, false, false
	}
	if !b.Status.Started {
		if b.Status.Phase.Done() {
			WriteProblem(w, http.StatusGone, "gone", "build "+b.Metadata.Name+" never ran, so it has no logs or events", reqID)
		} else {
			WriteProblem(w, http.StatusConflict, "conflict", "build "+b.Metadata.Name+" hasn't started yet; retry once it's Running", reqID)
		}
		return resource.Build{}, false, false
	}
	// BuildLogs' error for an expired run has no sentinel (a provider's
	// own), so the observation says whether the run is still there.
	obs, err := h.provider.ObserveBuild(r.Context(), b)
	if err != nil {
		h.buildReadFailed(w, r, err)
		return resource.Build{}, false, false
	}
	if obs.Gone {
		return b, true, false
	}
	return b, false, true
}

func writeExpired(w http.ResponseWriter, r *http.Request, b resource.Build) {
	WriteProblem(w, http.StatusGone, "logs expired", "build "+b.Metadata.Name+"'s logs and events expired: they're kept for an hour after a build ends", audit.RequestIDFrom(r.Context()))
}

// readFailed answers a failed read of b's logs or events. The run may
// have expired since run() looked, so it's looked at again: gone is 410,
// as it would have been a moment earlier, not a 502.
func (h *BuildHandler) readFailed(b resource.Build) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if obs, oerr := h.provider.ObserveBuild(r.Context(), b); oerr == nil && obs.Gone {
			writeExpired(w, r, b)
			return
		}
		h.buildReadFailed(w, r, err)
	}
}

// buildReadFailed is platformFailed in the builds package's words. Not
// ready is said as the capability's readiness has it: a package that
// isn't installed isn't "being set up" (specs/022-builds T014 U4).
func (h *BuildHandler) buildReadFailed(w http.ResponseWriter, r *http.Request, err error) {
	reqID := audit.RequestIDFrom(r.Context())
	if errors.Is(err, reconcile.ErrProviderNotReady) {
		detail := "the builds package is still being set up; retry shortly"
		var rf *deployRefusal
		if h.ready != nil && errors.As(h.ready(), &rf) {
			detail = "the build's logs and events can't be read now: " + rf.detail
		}
		WriteProblem(w, http.StatusServiceUnavailable, "service unavailable", detail, reqID)
		return
	}
	log.Printf("api: %s %s: build read failed (request %s): %v", r.Method, r.URL.Path, reqID, err)
	WriteProblem(w, http.StatusBadGateway, "bad gateway", "the build's run couldn't be read; retry later, quoting the request ID", reqID)
}

// Events answers {"events": [...]}, newest first; ?since=RFC 3339 time
// keeps only newer ones.
func (h *BuildHandler) Events(w http.ResponseWriter, r *http.Request) {
	var since time.Time
	if s := r.URL.Query().Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "validation failed", "since: an RFC 3339 time, e.g. 2026-10-23T10:00:00Z", audit.RequestIDFrom(r.Context()))
			return
		}
		since = t
	}
	b, ok := h.run(w, r)
	if !ok {
		return
	}
	events, err := h.provider.BuildEvents(r.Context(), b, since)
	if err != nil {
		h.readFailed(b)(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": nonNil(events)})
}

// Logs answers the run's recent lines, or with ?follow=true streams them
// as NDJSON until the run ends: the same options, caps and stream ends as
// a Deployment's logs.
func (h *BuildHandler) Logs(w http.ResponseWriter, r *http.Request) {
	opts, err := logOptions(r)
	if err != nil {
		WriteProblem(w, http.StatusBadRequest, "validation failed", err.Error(), audit.RequestIDFrom(r.Context()))
		return
	}
	b, gone, ok := h.runOrGone(w, r)
	if gone {
		h.storedLogs(w, r, b, opts)
		return
	}
	if !ok {
		return
	}
	open := func(ctx context.Context) (reconcile.LogStream, error) { return h.provider.BuildLogs(ctx, b, opts) }
	if opts.Follow {
		followLogs(w, r, h.streams, h.idle, h.max, open, h.readFailed(b))
		return
	}
	lines, truncated, ok := recentLogs(w, r, h.recent, open, h.readFailed(b))
	if !ok {
		return
	}
	// A finished run whose step pods are gone (deleted by hand, evicted,
	// or lost with a node) reads as no lines while the run itself is
	// still there; its stored lines are the answer then (spec 023 T013).
	if len(lines) == 0 && b.Status.Phase.Done() && h.storedUsable() {
		h.storedLogs(w, r, b, opts)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "truncated": truncated})
}

// storedUsable says the observability package can answer stored logs now.
func (h *BuildHandler) storedUsable() bool {
	return h.stored != nil && (h.storedReady == nil || h.storedReady() == nil)
}

// secretValueRead is what naming a credential Secret on a Component needs:
// the build reads the value as the system and sends it to the repository's
// host, so whoever points a build at a Secret must be allowed its value.
var secretValueRead = policy.Action{Name: "serendip/secrets/value/read", Data: true}

// sourceChecker checks a Component's source where the API knows more than
// its own validation: the build capability is ready, and the credential
// Secret exists and may be read by the caller.
type sourceChecker struct {
	ready func() error
}

// check checks c's source, on update against current as stored, and
// returns the context with the credential's check in its policy evidence.
// A source left exactly as it was isn't checked again, so a Component's
// other fields can be edited while the package is down.
func (s sourceChecker) check(ctx context.Context, v workloadView, c resource.Component, current *resource.Component) (context.Context, error) {
	src := c.Spec.Source
	if src == nil {
		return ctx, nil
	}
	if err := resource.ValidateComponentSource(c.Spec); err != nil {
		return ctx, err
	}
	var was *resource.ComponentSource
	if current != nil {
		was = current.Spec.Source
	}
	if was != nil && *was == *src {
		return ctx, nil
	}
	if s.ready != nil {
		if err := s.ready(); err != nil {
			return ctx, err
		}
	}
	// The credential is checked when it's newly named, and when the
	// repository it's sent to changes.
	if src.CredentialSecret == "" || (was != nil && was.CredentialSecret == src.CredentialSecret && was.Repository == src.Repository) {
		return ctx, nil
	}
	at := resource.Scope{Kind: resource.KindSecret, ID: src.CredentialSecret}
	ctx, ok, err := allowedNoted(ctx, secretValueRead, v.scope(at))
	if err != nil {
		return ctx, err
	}
	if !ok {
		return ctx, deployRefuse(http.StatusForbidden, "forbidden", "missing %s at Secret/%s: a build reads its credential's value, so naming it needs reading that value", secretValueRead.Name, src.CredentialSecret)
	}
	found, err := v.exists(ctx, resource.KindSecret, src.CredentialSecret)
	if err != nil {
		return ctx, err
	}
	if !found {
		return ctx, deployRefuse(http.StatusBadRequest, "validation failed", "source.credentialSecret: Secret %s not found", src.CredentialSecret)
	}
	return ctx, nil
}

// Stored build logs (specs/023-observability plan decision 10).
const (
	// storedBuildPages bounds the pages read for one answer.
	storedBuildPages = 20
	// storedBuildSlack is how long before the Build was created its run's
	// lines are looked for, for clock skew between the store and Serendip.
	storedBuildSlack = time.Minute
)

// storedLogs answers an expired run's logs from the observability
// package's store, in the shapes Logs uses: {"lines", "truncated",
// "stored": true}, oldest first, or with ?follow=true the same lines as
// NDJSON, then {"end": "ended"} (a finished build has nothing to follow).
// Without the package ready, it's 410 as before.
func (h *BuildHandler) storedLogs(w http.ResponseWriter, r *http.Request, b resource.Build, opts reconcile.LogOptions) {
	if !h.storedUsable() {
		writeExpired(w, r, b)
		return
	}
	read := func(ctx context.Context) ([]reconcile.LogLine, bool, error) {
		ctx, cancel := context.WithTimeout(ctx, obsQueryTimeout)
		defer cancel()
		return h.readStored(ctx, b, opts)
	}
	failed := func(w http.ResponseWriter, r *http.Request, err error) {
		reqID := audit.RequestIDFrom(r.Context())
		switch {
		case r.Context().Err() != nil:
			return // the client went away
		case errors.Is(err, reconcile.ErrProviderNotReady):
			writeExpired(w, r, b)
			return
		case errors.Is(err, context.DeadlineExceeded):
			WriteProblem(w, http.StatusGatewayTimeout, "gateway timeout", "reading the build's stored logs took longer than "+obsQueryTimeout.String()+"; try tailLines or sinceSeconds", reqID)
			return
		}
		log.Printf("api: %s %s: stored build logs read failed (request %s): %v", r.Method, r.URL.Path, reqID, err)
		WriteProblem(w, http.StatusBadGateway, "bad gateway", "the build's stored logs couldn't be read; retry later, quoting the request ID", reqID)
	}
	if opts.Follow {
		open := func(ctx context.Context) (reconcile.LogStream, error) {
			lines, _, err := read(ctx)
			if err != nil {
				return nil, err
			}
			return &linesStream{lines: lines}, nil
		}
		followLogs(w, r, h.streams, h.idle, h.max, open, failed)
		return
	}
	reqID := audit.RequestIDFrom(r.Context())
	caller, ok := callerFrom(r.Context())
	if !ok {
		WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", reqID)
		return
	}
	if !h.recent.acquire(caller.ID) {
		w.Header().Set("Retry-After", "5")
		WriteProblem(w, http.StatusTooManyRequests, "too many requests",
			"too many log reads at once: at most "+strconv.Itoa(h.recent.perCaller)+" per caller and "+strconv.Itoa(h.recent.total)+" in all; retry shortly", reqID)
		return
	}
	defer h.recent.release(caller.ID)
	lines, truncated, err := read(r.Context())
	if err != nil {
		failed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "truncated": truncated, "stored": true})
}

// readStored reads b's stored lines, newest first page by page, up to
// ?tailLines (else maxRecentLines) and maxRecentBytes in all, and returns
// them oldest first.
// truncated says older lines were left.
func (h *BuildHandler) readStored(ctx context.Context, b resource.Build, opts reconcile.LogOptions) ([]reconcile.LogLine, bool, error) {
	want := maxRecentLines
	if opts.TailLines > 0 {
		want = opts.TailLines
	}
	now := time.Now()
	// A Build always has its creation time; without one, never scan
	// further back than the store keeps.
	q := reconcile.LogQuery{From: later(b.Metadata.CreatedAt.Add(-storedBuildSlack), now.Add(-defaultRetention))}
	if opts.Since > 0 {
		q.From = later(q.From, now.Add(-opts.Since))
	}
	var lines []reconcile.LogLine
	truncated, size := false, 0
	for pages := 0; ; pages++ {
		if pages == storedBuildPages {
			truncated = true
			break
		}
		q.Limit = min(reconcile.MaxLogQueryLimit, want-len(lines))
		page, err := h.stored.BuildLogs(ctx, b, q)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || page.Truncated
		full := false
		for _, l := range page.Lines {
			if size += len(l.Line); size > maxRecentBytes {
				full = true
				break
			}
			lines = append(lines, reconcile.LogLine{Time: l.Time, Instance: cmp.Or(l.Instance, "build"), Line: l.Line})
		}
		if full {
			truncated = true
			break
		}
		if page.Next == "" {
			break
		}
		// Limit stays at least 1: the loop stops once want is reached.
		if len(lines) >= want {
			truncated = true
			break
		}
		q.Before = page.Next
	}
	// Only for a provider that sends more than Limit.
	if len(lines) > want {
		lines, truncated = lines[:want], true
	}
	slices.Reverse(lines)
	return nonNil(lines), truncated, nil
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// linesStream yields lines, then io.EOF.
type linesStream struct {
	lines []reconcile.LogLine
}

func (s *linesStream) Next() (reconcile.LogLine, error) {
	if len(s.lines) == 0 {
		return reconcile.LogLine{}, io.EOF
	}
	l := s.lines[0]
	s.lines = s.lines[1:]
	return l, nil
}

func (s *linesStream) Close() error { return nil }
