# Plan: Spec 003 — Reconciliation engine

Status: Draft — awaiting human approval before implementation.

## Open-question resolutions

1. **Resync interval: 30s default, constructor parameter (not hardcoded).**
   At edge-1 scale (dozens of resources, not thousands) a full re-list +
   diff of one kind every 30s is negligible against the < 5% idle-CPU
   budget (constitution 4) — each pass is a handful of SQLite reads plus,
   only for resources whose computed Condition changed, one `Update`.
   Configurable rather than a package constant so a future profile
   (`profiles/small-3`, `standard-5`) can tune it without a code change,
   mirroring how `SERENDIP_ADDR`/`SERENDIP_DB_PATH` are already
   env-configurable in `cmd/serendipd`.
2. **Per-reconcile timeout: 10s default, same treatment.** A TCP dial
   with no response should fail fast, not hold the single reconcile
   goroutine hostage; 10s is generous for a health check while still
   bounding worst-case loop stall to one timeout period.

## Approach

**`core/reconcile.Engine`: one dispatcher, two trigger paths converging
on the same reconcile call.**

```go
type Reconciler interface {
    Reconcile(ctx context.Context, ref graph.ResourceRef) error
}

type registration struct {
    reconciler Reconciler
    list       func(ctx context.Context) ([]graph.ResourceRef, error)
}

type Engine struct {
    bus          event.Bus
    resyncEvery  time.Duration
    reconcileTimeout time.Duration
    regs         map[resource.Kind]registration
}

func NewEngine(bus event.Bus, resyncEvery, reconcileTimeout time.Duration) *Engine
func (e *Engine) Register(kind resource.Kind, r Reconciler, list func(ctx context.Context) ([]graph.ResourceRef, error))
func (e *Engine) Run(ctx context.Context) error // blocks until ctx.Done()
```

`Run` subscribes to `bus` once (`Subscribe("", handler)` — every kind,
filtered internally against `regs`), starts a `time.Ticker` at
`resyncEvery`, and processes both event-triggered and resync-triggered
refs through one internal `workCh chan graph.ResourceRef` drained by a
single `for { select }` loop — deliberately one goroutine, not a
worker pool: at edge-1 scale sequential processing is simpler and safer
(no per-resource locking needed) than concurrent processing, and
`event.InProcessBus`'s own contract ("handlers run on the publishing
goroutine... must be fast and non-blocking") means the *event handler*
itself must never block — it does a non-blocking `select { case
workCh <- ref: default: }` and returns immediately. A dropped send
(channel full) is safe: the next resync tick re-lists and re-enqueues
everything anyway, so nothing is silently lost forever, only delayed —
exactly the "eventually consistent, level-triggered" property
constitution rule 3 asks for.

Each dequeued ref gets `context.WithTimeout(ctx, e.reconcileTimeout)`
before calling the registered `Reconciler.Reconcile`, and the context
passed in carries `audit.WithInterface(ctx, InterfaceName)` and
`audit.WithActor(ctx, ActorName)` — set once per call in the Engine,
not duplicated in every `Reconciler` implementation.

```go
// core/reconcile/reconcile.go (or audit.go)
const InterfaceName = "reconcile"
const ActorName = "system:reconciler"
```

**`HealthChecker`: one implemented `Kind`, stdlib only.**

```go
type HealthChecker interface {
    // Check returns nil if healthy, a non-nil error (with a human-
    // readable message suitable for Condition.Message) otherwise.
    // An empty/unrecognized contract.Kind returns ErrNotConfigured,
    // which callers must treat as "unknown," not "unhealthy."
    Check(ctx context.Context, contract resource.HealthCheckContract) error
}

var ErrNotConfigured = errors.New("reconcile: no health check configured")

type TCPHealthChecker struct{} // Config["address"], e.g. "127.0.0.1:6443"
func (TCPHealthChecker) Check(ctx context.Context, contract resource.HealthCheckContract) error
```

**`ClusterReconciler`: the one concrete `Reconciler`.**

```go
type ClusterReconciler struct {
    clusters  store.Store[resource.ClusterSpec, resource.ClusterStatus]
    providers store.Store[resource.ProviderSpec, resource.ProviderStatus]
    checker   HealthChecker
}

func (r *ClusterReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
    cluster, err := r.clusters.Get(ctx, ref.ID)
    if errors.Is(err, store.ErrNotFound) { return nil } // deleted since the event fired
    if err != nil { return err }
    if cluster.Spec.ProviderID == "" {
        return r.setCondition(ctx, cluster, "Healthy", resource.ConditionUnknown, "NoProviderConfigured", "")
    }
    provider, err := r.providers.Get(ctx, cluster.Spec.ProviderID)
    if errors.Is(err, store.ErrNotFound) {
        return r.setCondition(ctx, cluster, "Healthy", resource.ConditionUnknown, "ProviderNotFound", "")
    }
    if err != nil { return err }

    checkErr := r.checker.Check(ctx, provider.Spec.HealthCheck)
    switch {
    case errors.Is(checkErr, ErrNotConfigured):
        return r.setCondition(ctx, cluster, "Healthy", resource.ConditionUnknown, "NoHealthCheckConfigured", "")
    case checkErr != nil:
        return r.setCondition(ctx, cluster, "Healthy", resource.ConditionFalse, "HealthCheckFailed", checkErr.Error())
    default:
        return r.setCondition(ctx, cluster, "Healthy", resource.ConditionTrue, "HealthCheckPassed", "")
    }
}
```

`setCondition` compares the computed `{Status, Reason}` against the
existing `"Healthy"` Condition on the just-fetched `cluster`; if
unchanged, returns `nil` without calling `Update` (idempotency,
acceptance criterion 3). If changed, upserts the Condition in place and
calls `r.clusters.Update(ctx, cluster)` — an ordinary `Update`, which
already audits inside the same transaction as the write (constitution
rule 8) via `core/store/sqlite`'s existing machinery; no new audit code
needed anywhere in `core/reconcile`.

On `store.ErrConflict` from `Update` (a concurrent human/API edit raced
this reconcile pass), `Reconcile` returns the error, the `Engine` logs
it and moves on — no retry loop inside `Reconcile` itself. The next
resync tick (or the next event, if the racing edit itself published
one) reconciles again from fresh state. This is the same
"level-triggered, eventually consistent" reasoning as the dropped-event
case above, applied to a write race instead of a missed trigger.

**Listing for resync**: `ClusterReconciler`'s registration passes
`func(ctx) ([]graph.ResourceRef, error)` built from
`clusters.List(ctx, store.ListOptions{})`, mapping each result to
`graph.ResourceRef{Kind: resource.KindCluster, ID: r.Metadata.ID}`.

**`cmd/serendipd` wiring**: after constructing `bus` and before
`srv.ListenAndServe`, construct the Cluster/Provider stores (sharing
the same `*sql.DB` and `bus` already used for `api.NewServer`),
register `ClusterReconciler` on a new `reconcile.Engine`, and run
`engine.Run(ctx)` in its own goroutine (same `ctx` from the existing
`signal.NotifyContext`, so Ctrl-C/SIGTERM stops both the HTTP server
and the engine together — T014's graceful-shutdown pattern extended,
not replaced). `engine.Run`'s returned error goes on the same
`errCh`-style selection `run()` already uses for the HTTP server, so a
crashed engine surfaces the same way a crashed listener does.

## Interfaces / types to create

```go
// core/reconcile/engine.go
type Reconciler interface { Reconcile(ctx context.Context, ref graph.ResourceRef) error }
type Engine struct { /* ... */ }
func NewEngine(bus event.Bus, resyncEvery, reconcileTimeout time.Duration) *Engine
func (e *Engine) Register(kind resource.Kind, r Reconciler, list func(ctx context.Context) ([]graph.ResourceRef, error))
func (e *Engine) Run(ctx context.Context) error

// core/reconcile/audit.go
const InterfaceName = "reconcile"
const ActorName = "system:reconciler"

// core/reconcile/health.go
type HealthChecker interface { Check(ctx context.Context, contract resource.HealthCheckContract) error }
var ErrNotConfigured = errors.New("reconcile: no health check configured")
type TCPHealthChecker struct{}
func (TCPHealthChecker) Check(ctx context.Context, contract resource.HealthCheckContract) error

// core/reconcile/cluster.go
type ClusterReconciler struct { /* ... */ }
func NewClusterReconciler(clusters store.Store[resource.ClusterSpec, resource.ClusterStatus], providers store.Store[resource.ProviderSpec, resource.ProviderStatus], checker HealthChecker) *ClusterReconciler
func (r *ClusterReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error

// cmd/serendipd/main.go (extended, not replaced)
func run() error // now also starts the Engine
```

## Files touched

```
core/reconcile/engine.go, engine_test.go       (new)
core/reconcile/health.go, health_test.go       (new)
core/reconcile/cluster.go, cluster_test.go     (new)
core/reconcile/audit.go                        (new — InterfaceName/ActorName consts)
core/reconcile/CLAUDE.md                       (new — package conventions, mirrors api/CLAUDE.md)
cmd/serendipd/main.go                          (extended: construct + run the Engine)
docs/PROGRESS.md                               (updated on completion)
```

No file under `api/` is touched (spec.md Scope-OUT).

## Risks

- **Single-goroutine sequential reconciliation is a throughput ceiling.**
  Explicitly accepted for edge-1 scale (constitution 4's lightweight
  bar); revisit only once a real multi-kind, higher-resource-count
  deployment (Phase 4, small-3+) makes it a measured problem, not a
  theoretical one — matches how spec 002 deferred pagination for the
  same "don't build for a load that doesn't exist yet" reason.
- **Dropped events on a full `workCh` rely on the next resync tick to
  self-heal.** Correct by design (see Approach), but means worst-case
  detection latency for a missed event is `resyncEvery`, not
  near-instant — acceptable since 30s is still far faster than a human
  would notice via `GET /v1/clusters/{id}` anyway, and this is a health
  *check*, not a hard real-time control loop.
- **`ClusterReconciler` calling `store.Store.Update` directly (not
  through `api/`) means its audit rows bypass `api/middleware.go`'s
  request-ID plumbing entirely** — `audit.Record.RequestID` will be
  empty for every reconciler-driven row, which is correct (there is no
  HTTP request to attach), but worth stating plainly so nobody reads an
  empty `RequestID` on a reconcile-originated audit row as a bug later.
- **`TCPHealthChecker` is the only real capability this spec ships**,
  and nothing in the current `Provider`/`Cluster` test data actually
  has a populated `HealthCheck` contract outside this spec's own tests
  — acceptable (matches `ProviderSpec.HealthCheck`'s spec-001 doc
  comment: "stored as data only... nothing executes it yet," now
  finally executed, still with nothing real to check against until a
  real provider registers a real contract).

## Explicitly NOT done in this spec

- The full capability-specific Provider Go interface(s) from
  `provider-model.md` (deferred to the spec that builds the first real
  provider).
- Real provider implementations (Thunder, OpenBao, K3s, OpenChoreo,
  local-path, ...).
- Reconcilers for any kind besides Cluster.
- The apply/provision side of reconciliation — observe-and-verify
  (health check) only.
- Any `api/` change — reconciliation is fully automatic/internal.
- A status-only update subresource — shared full-resource `Update` with
  optimistic-concurrency protection continues from specs 001/002.
- Policy engine / approval gating — nothing destructive happens in this
  spec's one Reconciler.
- Concurrent/sharded reconciliation — single goroutine, sequential.
