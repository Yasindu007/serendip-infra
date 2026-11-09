# core/reconcile — reconciliation engine

- Reconcilers are idempotent and context-aware (core/CLAUDE.md's hard
  rule, spec 003's whole point): re-running `Reconcile` with no drift must
  not call `store.Update` — check computed vs. stored state before
  writing, don't write unconditionally.
- Single goroutine, sequential dispatch at this scale (edge-1: dozens of
  resources, not thousands). Do not add worker pools/concurrent
  reconciliation speculatively — that's a later-scale concern (Phase 4
  small-3+), revisit only once it's a measured problem.
  - The one measured exception: `packagejob.go` runs a package's
    install/removal in a background goroutine (at most one per package),
    because Helm outlives the 10 s reconcile (spec 022 T013, RP-045).
    Dispatch itself stays sequential; the goroutine only runs the
    installer.
- Reuse `core/graph.ResourceRef` (Kind, ID) as the kind-agnostic resource
  identifier — do not invent a second one.
- No vendor product names in identifiers/comments-as-behavior (constitution
  rule 1 applies here same as everywhere else in `core/`) — this package
  drives providers generically (`HealthCheckContract`), it never mentions
  a specific provider.
- `event.InProcessBus` handlers "run on the publishing goroutine... must
  be fast and non-blocking" (its own doc comment) — the Engine's bus
  subscription must only enqueue (non-blocking channel send), never call
  `Reconcile` directly from the event handler.
- Reconciler-driven mutations get their own audit identity
  (`InterfaceName`/`ActorName` in audit.go) set by the Engine on the
  context passed into `Reconcile` — individual `Reconciler`
  implementations should not set these themselves.
  - The one deliberate exception: builds Serendip starts itself (a
    source Component's first build, and auto-build) are created as
    `system:auto-build` (`AutoBuildActor`), through `audit.WithActor` on
    that one Create (spec 022 T007).
- No `core/` package imports `providers/` (constitution rule 2) — this
  package is no exception, even though nothing in `providers/` exists yet
  to import.
