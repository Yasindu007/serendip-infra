# Tasks: Spec 003 — Reconciliation engine

Ordered; `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). Run `contract-guardian` before committing anything under
`core/` (CLAUDE.md workflow rule).

## T001 — core/reconcile scaffold + CLAUDE.md

- **Do**: Create `core/reconcile/` package. `core/reconcile/CLAUDE.md`
  documenting package conventions (mirrors `api/CLAUDE.md`'s role):
  reconcilers are idempotent, single-goroutine sequential dispatch at
  this scale, no vendor names (constitution rule 1 applies here same as
  everywhere in `core/`), reuses `graph.ResourceRef` not a new ID type.
- **Files**: `core/reconcile/CLAUDE.md`, `core/reconcile/doc.go`.
- **Verify**: `go build ./...` succeeds.

## T002 — HealthChecker + TCPHealthChecker

- **Do**: `HealthChecker` interface, `ErrNotConfigured` sentinel,
  `TCPHealthChecker` implementing the `"tcp"` `HealthCheckContract.Kind`
  via `net.Dialer.DialContext(ctx, "tcp", contract.Config["address"])`.
  Any other/empty `Kind` returns `ErrNotConfigured`.
- **Files**: `core/reconcile/health.go`, `core/reconcile/health_test.go`.
- **Verify**: `go test ./core/reconcile/...` — table-driven: reachable
  address → nil; unreachable address → non-nil, non-`ErrNotConfigured`
  error; empty `Kind` → `ErrNotConfigured`; unrecognized `Kind` (e.g.
  `"http"`) → `ErrNotConfigured`. Reachable/unreachable cases use a real
  `net.Listen("tcp", "127.0.0.1:0")` started/stopped in-test, not a
  fake/mock — this is the one piece of this spec with real I/O to prove.

## T003 — reconcile.Engine: registration + event-triggered dispatch

- **Do**: `Reconciler` interface, `Engine` struct with `NewEngine`,
  `Register`, and `Run(ctx) error`. `Run` subscribes to the given
  `event.Bus` (all kinds), non-blocking-enqueues matching-kind refs onto
  an internal channel, and processes them sequentially via the
  registered `Reconciler.Reconcile`, each call wrapped in
  `context.WithTimeout(ctx, reconcileTimeout)` and
  `audit.WithInterface`/`audit.WithActor` (constants from T004). No
  resync ticker yet — that's T005, kept separate so this task's tests
  can prove event-triggering in isolation first.
- **Files**: `core/reconcile/engine.go`, `core/reconcile/engine_test.go`.
- **Verify**: `go test ./core/reconcile/...` — a registered fake
  `Reconciler` (test-local, records calls) receives exactly one call
  per matching-kind event published on the bus; a non-matching-kind
  event triggers no call; `Run` returns cleanly when `ctx` is canceled.

## T004 — Reconciler audit identity constants

- **Do**: `InterfaceName = "reconcile"`, `ActorName = "system:reconciler"`
  constants, used by T003's `Engine` (already wired if done in the same
  commit as T003 — split into its own task only so the constants and
  their rationale are independently reviewable/greppable, matching how
  spec 002 T003 bundled `Interface`-setting into one commit rather than
  scattering it).
- **Files**: `core/reconcile/audit.go`.
- **Verify**: `go test ./core/reconcile/...` — a test asserts a
  `Reconcile` call's context (as observed by a fake `Reconciler`) carries
  both values via `audit.InterfaceFrom`/`audit.ActorFrom`.

## T005 — Periodic resync

- **Do**: Extend `Engine.Run` with a `time.Ticker` at `resyncEvery`; on
  each tick, call every registered kind's `list` function and enqueue
  all returned refs the same way an event does (same channel, same
  non-blocking-send-with-drop-on-full semantics as T003, so a full
  channel during a resync burst degrades the same way an event burst
  does — one code path, not two).
- **Files**: `core/reconcile/engine.go` (extends T003), `engine_test.go`.
- **Verify**: `go test ./core/reconcile/...` — a registered fake
  `Reconciler`+`list` pair gets called by the ticker alone (no event
  published) within one resync interval in a test using a short
  interval (e.g. 20ms), not the 30s production default.

## T006 — ClusterReconciler

- **Do**: `ClusterReconciler` per plan.md's `Reconcile` logic: resolve
  `Cluster.Spec.ProviderID` → `Provider`, run `HealthChecker.Check`
  against `Provider.Spec.HealthCheck`, upsert a `"Healthy"` Condition,
  `Update` only if the computed `{Status, Reason}` differs from what's
  already stored (idempotency). Handles: no `ProviderID` set, `Provider`
  not found, `ErrNotConfigured` from the health check, pass, fail —
  each maps to a distinct `Reason` per plan.md.
- **Files**: `core/reconcile/cluster.go`, `core/reconcile/cluster_test.go`.
- **Verify**: `go test ./core/reconcile/...` — table-driven over all 5
  cases above, using real `sqlite.Store` instances (`core/store/sqlite`,
  not a mock) so `Update`'s real audit/optimistic-concurrency behavior
  is exercised, not assumed. A repeated `Reconcile` call with no drift
  makes zero additional `Update` calls (spy on the store, or assert
  `ResourceVersion` is unchanged after the second call) — this is
  acceptance criterion 3's core mechanism, prove it here at the unit
  level before T008 proves it through the full `Engine`.

## T007 [P] — cmd/serendipd wiring

- **Do**: In `run()`, after constructing `bus` and the stores
  `api.NewServer` already needs, construct the Cluster/Provider stores
  reconcile needs (or share `api.NewServer`'s if a shared-store refactor
  is cheap — decide during implementation, not here), build a
  `reconcile.Engine`, `Register` the `ClusterReconciler` with a lister
  built from `clusters.List`, and run `engine.Run(ctx)` in its own
  goroutine using the same `ctx` the HTTP server already shuts down on.
  Engine errors go on the same selection `run()` uses for the HTTP
  listener's error channel.
- **Files**: `cmd/serendipd/main.go`.
- **Verify**: `go run ./cmd/serendipd` starts with both the HTTP server
  and the engine active; a real SIGINT stops both cleanly (extend
  T014's WSL-based shutdown test rather than writing a new one from
  scratch) — no hung process, no goroutine leak (`go build
  -race`-compiled binary, or `go test -race` on an equivalent
  in-process test, whichever is cheaper to set up here).

## T008 — Integration test: acceptance criteria 2-4

- **Do**: One test file proving spec.md's criteria directly through the
  real `Engine` (not `ClusterReconciler` called directly, which T006
  already covers at the unit level): create a `Provider` with a `"tcp"`
  `HealthCheck` pointed at a real in-test listener, create a `Cluster`
  referencing it, publish/trigger via the actual store+bus path (a real
  `Create` call, which already publishes an event per spec 001), assert
  the Condition goes `True` after one pass (criterion 2); reconcile
  again with no change and assert no additional audit row/version bump
  (criterion 3, this time through the full `Engine` not just
  `ClusterReconciler` directly); stop the listener, advance past one
  resync interval with no triggering event, and assert the Condition
  flips to `False` via resync alone (criterion 4).
- **Files**: `core/reconcile/acceptance_test.go`.
- **Verify**: `go test ./core/reconcile/... -v` — three named tests, one
  per criterion, mirroring specs 001/002's
  `TestAcceptanceCriterionN_...` naming convention.

## T009 [A] — Audit identity test: acceptance criterion 5

- **Do**: Assert every `audit_log` row written by a `ClusterReconciler`-
  driven `Update` (through the real `Engine`, real `sqlite.Store`) has
  `Interface = reconcile.InterfaceName` and `Actor = reconcile.ActorName`
  — not `"core"`, not `"http"`, not `"unknown"`.
- **Files**: `core/reconcile/acceptance_test.go` (extends T008) or a
  separate `audit_test.go` if that reads more cleanly once T008 exists.
- **Verify**: `go test ./core/reconcile/...` green.

## T010 — Final acceptance pass + contract-guardian

- **Do**: `go test ./core/... ./api/... ./cmd/...` (or equivalent) full
  run, re-check all 6 acceptance criteria against actual test output,
  `bash .claude/scripts/check-imports.sh` (confirms `core/reconcile`
  imports no `providers/` package — it shouldn't need to, but verify
  rather than assume, per spec 002 T019's lesson that "should be true"
  and "is verified true" are different claims), one holistic
  `contract-guardian` review across all of `core/reconcile` and the
  `cmd/serendipd` diff.
- **Files**: none (review/gate task).
- **Verify**: all green, `check-imports.sh` clean, contract-guardian
  PASS, all 6 acceptance criteria individually confirmed. Re-read
  spec.md's Scope-IN line by line against what actually got built
  before declaring done — the exact check that was skipped, then
  caught late, in spec 002.

## T011 [A] — Docs sync

- **Do**: Update `docs/PROGRESS.md` (spec 003 complete, next phase per
  `docs/PRD.md`'s roadmap). Update `docs/architecture/provider-model.md`
  if implementation revealed anything about the eventual
  capability-specific Provider interface question worth recording as a
  breadcrumb for whichever spec resolves it next (without resolving it
  here — just note what T002-T006 learned, if anything, about the
  shape a real provider interface will need).
- **Files**: `docs/PROGRESS.md`, possibly
  `docs/architecture/provider-model.md`.
- **Verify**: no stale language remains describing spec 003 as
  not-started.
