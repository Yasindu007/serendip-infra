# Spec 003: Reconciliation engine

Status: Draft — human review required before plan.md

## Problem

Constitution rule 3: "All state changes flow: desired state → reconciliation
engine → provider → verification. No imperative side-channels." Spec 001
built the desired-state store; spec 002 built the API to write it. Nothing
reads it back and *does* anything — every resource's `Status.Conditions` is
inert, exactly as spec 002's Scope-OUT documented: "Status/Conditions stay
whatever they were at spec 001 (unpopulated)." `core/resource.HealthCheckContract`
and `ResourceRequirements` were both written in spec 001 with doc comments
that say, verbatim, "the reconciliation engine (spec 003)" — this spec is
what those comments were pointing at.

This spec builds the reconciliation *engine*: the generic, event-driven +
periodically-resynced loop that observes actual state, diffs it against
desired state, and writes verified Status/Conditions back — idempotently,
per constitution rule 3's "re-running a loop with no changes must be a
no-op."

## Why not a real provider yet

No `providers/` package exists (confirmed: the directory has only a
`CLAUDE.md`). `docs/architecture/provider-model.md`'s own open question —
"whether `ApplicationProvider`/`InfrastructureProvider` end up as the actual
`core/` interface names... resolve during the relevant spec's plan.md, not
by guessing here" — is deliberately NOT resolved by this spec. Guessing the
shape of a capability-specific provider interface (deploy/scale/delete an
Application, provision infrastructure, ...) before a real provider exists to
consume it would be speculative design, which constitution rule 10 (scope
discipline) rejects. That interface gets designed by whichever future spec
builds the first real provider (K3s, per the bootstrap-agent track, or
whichever capability turns out to go first).

What this spec builds instead is narrower and fully real, not a stub: a
health-check mechanism generic enough that any future provider can reuse it
unchanged, proven end-to-end against one concrete resource kind (Cluster)
using only the standard library (no vendor SDK, nothing to fake).

## Scope — IN

- `core/reconcile` package:
  - `Reconciler` interface: `Reconcile(ctx, graph.ResourceRef) error`,
    idempotent — reuses `core/graph.ResourceRef` rather than inventing a
    second kind-agnostic identifier type.
  - `Engine`: holds a per-`resource.Kind` registration (`Reconciler` +
    a lister function for periodic resync). Two trigger paths feed the
    same reconcile path: (a) `core/event.Bus` subscription — non-blocking
    channel send from the handler, since `event.InProcessBus`'s own doc
    comment requires handlers to be "fast and non-blocking" (they run
    synchronously on the publishing goroutine); (b) a periodic resync
    ticker that re-lists every registered kind, so drift that happens
    without a triggering mutation (a health check flipping from pass to
    fail) is still caught. Single goroutine, sequential processing —
    appropriate at edge-1 scale (dozens of resources, not thousands);
    concurrent/sharded reconciliation is a later-scale concern (Phase 4
    small-3+), not built speculatively now.
  - Each `Reconcile` call gets a bounded per-call `context.Context`
    timeout set by the `Engine` (not the `Reconciler`), so one hung
    health check can't stall the whole loop.
  - Reconciler-driven mutations get their own audit identity —
    `reconcile.InterfaceName` and a synthetic actor — set via
    `audit.WithInterface`/`audit.WithActor` on the context the `Engine`
    passes in, so `audit_log` can distinguish "the engine changed this"
    from a human/API-driven change or a genuinely unauthenticated one
    (`audit.ActorFrom`'s `"unknown"` default is meant to flag the
    latter, not automated system activity).
- `HealthChecker`: interprets `resource.HealthCheckContract` (spec 001,
  `Kind`/`Config` fields, explicitly "interpreted by the reconciliation
  engine, not here"). Exactly one `Kind` implemented: `"tcp"` (dials
  `Config["address"]` via `net.Dialer.DialContext`, stdlib only,
  provider-agnostic — reusable unchanged by a future K3s/OpenBao/any
  provider's real health check once one exists). An empty/unrecognized
  `Kind` means "no health check configured" (most `Provider` descriptors
  don't have one yet, per `ProviderSpec.HealthCheck`'s own spec-001 doc
  comment), not an error.
- `ClusterReconciler`: the one concrete `Reconciler` this spec ships.
  Reads a `Cluster`, resolves its `Spec.ProviderID` to a `Provider`
  record, runs `HealthChecker.Check` against that Provider's
  `HealthCheck` contract, writes a `"Healthy"` `Condition`
  (`True`/`False`/`Unknown`, with `Reason`) back via `core/store`'s
  ordinary `Update` — no bespoke audit code needed, `Update` already
  audits every mutation (constitution rule 8) regardless of caller.
  Idempotent by construction: `Update` is only called when the computed
  Condition actually differs from what's already stored.
- `cmd/serendipd` wiring: starts the `Engine` as a background goroutine
  alongside the HTTP server, sharing the existing
  `signal.NotifyContext`-derived context for graceful shutdown (same
  pattern T014 already established).

## Scope — OUT (explicitly)

- The full capability-specific Provider Go interface(s)
  (`ApplicationProvider`, `InfrastructureProvider`, ...) from
  `provider-model.md`'s illustrative sketch — deferred to whichever spec
  builds the first real provider implementation, per that doc's own open
  question. This spec's `HealthChecker` is deliberately the only
  provider-facing surface it defines.
- Real provider implementations (Thunder, OpenBao, K3s, OpenChoreo,
  local-path, ...) — later specs/phases, per `docs/PRD.md`'s POC scope
  item 3 ("at least one real provider live") being listed separately
  from "reconciliation engine — spec 003."
- Reconcilers for any kind other than Cluster (Application, Component,
  Database, Storage, ProviderInstance, ...) — Cluster proves the pattern
  end-to-end (mirrors spec 002 T011's "prove the pattern deeply on one
  kind" precedent); other kinds get their own Reconciler once a real
  provider exists for them to drive.
- The *apply/provision* side of reconciliation (actually creating
  infrastructure) — this spec is observe-and-verify only (health
  checking), since there is nothing yet to provision. "Apply" arrives
  with the first real provider.
- HTTP API changes — reconciliation is triggered automatically by
  events/resync, not a new endpoint. The existing `GET /v1/clusters/{id}`
  already exposes whatever `Conditions` the reconciler writes; no `api/`
  file is touched by this spec.
- A separate status-only update subresource (mirroring Kubernetes'
  `/status`). The reconciler and any human `PUT` both go through the
  same full-resource `Update`, protected only by optimistic concurrency
  (`ResourceVersion`) — a known simplification carried forward from
  specs 001/002, not a new gap this spec introduces. Revisit only if
  the shared-Update race proves painful in practice, not speculatively.
- Policy engine / approval gating for reconciler actions — this spec's
  only "action" is a read-only health check, nothing destructive or
  infrastructure-mutating; constitution rule 5's approval-object
  requirement has nothing to gate yet (unaffected, not newly resolved).
- `ProviderRegistry`/`CapabilityRegistry` changes — `ClusterReconciler`
  looks up a `Provider` by ID via the existing
  `store.Store[ProviderSpec,ProviderStatus].Get`, not through
  `registry.ProviderRegistry` (which is name-keyed, a different lookup
  need — reusing an existing mechanism rather than adding a redundant
  `ByID` wrapper).

## Acceptance criteria

1. `go test ./core/reconcile/... -v` green: table-driven tests cover
   `HealthChecker`'s `"tcp"` kind (reachable and unreachable target) and
   an unrecognized `Kind` (treated as "not configured," not an error).
2. A `Cluster` with a `Provider` whose `HealthCheck` targets a real,
   reachable TCP listener (started in-test) gets a `"Healthy"` Condition
   with `Status: True` after one reconcile pass — proven through the
   real `Engine` (event-triggered), not by calling `ClusterReconciler`
   directly.
3. Idempotency, proven directly: reconciling the same unchanged Cluster
   twice in a row produces exactly one `audit_log` row and one
   `ResourceVersion` bump, not two — the second pass is a verified no-op.
4. Resync-without-event, proven directly: after the first reconcile
   marks a Cluster healthy, stop the backing TCP listener (no store
   mutation happens — nothing triggers a bus event), advance past one
   resync interval, and confirm the Engine's periodic resync alone
   flips the Condition to `Status: False` — proving detection doesn't
   depend on a triggering mutation.
5. Every reconciler-driven `audit_log` row shows `Interface` as
   `reconcile.InterfaceName` (not `"core"` or `"http"`) and a real,
   consistent synthetic `Actor` (not `"unknown"`).
6. `cmd/serendipd` builds and runs with the `Engine` active in the
   background (confirmed via a running-process test, not just
   `go build`); `go vet ./...`, `gofmt -l .`,
   `check-imports.sh`-equivalent boundary checks all clean; `core/`
   still imports no `providers/` package (`core/reconcile` doesn't
   either, since no `providers/` package exists to import).

## Open questions (resolve during plan review)

- **Resync interval value**: a concrete default (candidate: 30s) needs
  picking and justifying against the < 5% idle-CPU budget (constitution
  4) at edge-1 scale — not a hard open design question, just a number
  to pin down with reasoning in plan.md rather than asserting here.
- **Per-reconcile timeout value**: same treatment (candidate: 10s).
