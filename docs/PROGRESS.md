# Progress / session state

**Read this file first in every new session**, before touching code or
docs. It's the single source of truth for "what's actually done, what's in
flight, what's next" — overwritten each session, not appended to. Git
history has the detail if you need archaeology; this file is only ever the
current picture.

Companion docs: `docs/PRD.md` (why/what/roadmap, changes rarely),
`docs/constitution.md` (non-negotiable rules, changes almost never).

Last updated: 2026-08-26

## Current phase

**Spec 001 (Foundation) merged to Serendip-Infra/serendip-infra:main via
PR #24.** Specs 002 and 003 are committed locally only — nothing pushed
anywhere yet (see GitHub note below).

**Spec 004 (Bootstrap agent — discovery/identity/self-registration) —
PLANNING DONE, NOT YET APPROVED.** `specs/004-bootstrap-agent/{spec.md,
plan.md,tasks.md}` exist on disk but are **uncommitted** (untracked —
deliberately not committed yet, since committing implied "accepted" for
specs 002/003 and this one hasn't gotten explicit user approval the way
those did). Read `specs/004-bootstrap-agent/plan.md` before doing
anything else with this spec — don't re-plan from scratch.

Scope (decided, presented, not yet confirmed): NOT the full 8-item
bootstrap-agent.md responsibility list — only hardware/environment
discovery, stable node identity, and self-registration against the
*already-existing* `/v1/nodes` API. K3s install, full negotiated
enrollment (needs a topology planner), and daemon-mode health reporting
are explicitly deferred (need root/real-host mutation or infra that
doesn't exist yet). Zero new `api/`/`core/` code — purely additive
under `agent/` + `cmd/serendip-agent/`, `core/id` is the only `core/`
import. 10 tasks planned (T001-T010).

**Spec 003 (Reconciliation engine) — COMPLETE. All 11 tasks committed
locally**, holistic contract-guardian review PASS (one round-trip: a
vendor-name-in-a-comment finding, fixed, re-reviewed clean). Full
picture:

- **Why this spec looks narrower than "reconciliation engine" might
  suggest**: no `providers/` package exists yet, so building the full
  capability-specific Provider Go interface `provider-model.md` sketches
  (`ApplicationProvider`, `InfrastructureProvider`, ...) would have been
  guessing at a shape with no real consumer — that doc's own open
  question explicitly defers it to "the relevant spec's plan.md," and
  this spec deliberately didn't claim to be that spec. What it built
  instead is fully real, not a stub: `core/reconcile`, an event-driven +
  30s-periodic-resync `Engine` (single goroutine, sequential — edge-1
  scale doesn't need more), proven end-to-end with one concrete
  `Reconciler` (`ClusterReconciler`) driving a stdlib-only, genuinely
  reusable `TCPHealthChecker` against `resource.HealthCheckContract` —
  the exact mechanism that field's spec-001 doc comment named as
  "the reconciliation engine (spec 003)."
- **Idempotency (constitution rule 3) is enforced in code, not just
  documented**: `ClusterReconciler.setCondition` compares the computed
  `{Status, Reason}` against what's already stored before calling
  `Update` — a no-drift reconcile makes zero store calls, zero audit
  rows, zero `ResourceVersion` bumps. Proven directly (not just "no
  error") in both `cluster_test.go` (unit level) and
  `acceptance_test.go`'s criterion-3 test (through the real `Engine`).
- **A real shutdown race was designed around, not just avoided by luck**:
  `cmd/serendipd`'s engine goroutine reacts to the same `ctx` the HTTP
  server's shutdown branch does. If the engine's completion channel
  became ready merely from `ctx` cancellation (the same instant
  `ctx.Done()` itself becomes ready), Go's `select` could randomly pick
  that case over the shutdown branch and skip `srv.Shutdown()` entirely.
  Fix: the engine's error channel only ever receives a value for a
  genuine crash (`context.Canceled` is filtered out before sending), so
  it can never race `ctx.Done()`'s readiness — mirrors why the
  pre-existing HTTP `errCh` was already race-free (it's only ready
  *downstream* of `Shutdown()` being called, never in direct parallel
  reaction to `ctx`). A separate `engineDone` channel is waited on before
  the deferred `db.Close()` runs, so no in-flight reconcile can be
  torn down mid-write.
- **T010's holistic review caught one real (if trivial) constitution
  violation**: `TCPHealthChecker`'s doc comment named K3s and OpenBao as
  illustrative future consumers — inside `core/reconcile`, where rule 1's
  "no OSS product name in `core/` types, function names, or
  comments-as-behavior" applies unqualified (unlike spec 002's Thunder
  mention in `api/middleware.go`, which is outside `core/` and was
  correctly ruled acceptable). Fixed to vendor-neutral language,
  re-reviewed: PASS.
- Verified end-to-end via the real compiled binary in WSL, not just unit
  tests: a `Cluster` created through the live HTTP API got a real
  `Healthy: True` Condition within ~100ms (event-triggered), and a real
  SIGINT still shuts down cleanly (~200ms) with both the HTTP server and
  the engine active.

**Spec 002 (HTTP API) — COMPLETE.** See "Spec 002 — what actually got
built" below for detail; condensed here now that spec 003 is current.

**GitHub note (updated 2026-08-26)**: user believes GitHub may have
lifted the `serendipinfra` suspension/flag. Status as of this check:
`gh auth status` now reports the **`serendipinfra` token itself as
invalid** ("Failed to log in... The token in keyring is invalid"),
which is a *different* symptom than the earlier flag (which let the
token authenticate but suppressed list-views/GraphQL). This could mean
the account state changed (support resolved it and something
invalidated the old token, e.g. a forced re-auth) or could be an
unrelated token expiry — **not yet confirmed which**. No re-auth
attempted; nothing GitHub-related was touched this session beyond the
read-only `gh auth status`/rate-limit checks already covered in earlier
entries. `Yasindu007` remains logged in, valid, and **not** made
active — standing instruction not to use it stands unless the user says
otherwise. **Before doing anything else with GitHub: confirm with the
user directly whether `serendipinfra` is actually unsuspended (e.g. by
having them check github.com in a browser or re-auth the CLI
themselves), rather than assuming from this token-invalid symptom
alone.**

## Spec 002 — what actually got built

`api/{problem,middleware,handler,graph_handler,registry_handler,server}.go`
+ `cmd/serendipd` — the HTTP API over `core/`, 19 tasks, all merged
locally:

- RFC 7807 error mapping, request-ID middleware, actor-extraction
  middleware (static `SERENDIP_API_KEYS` — explicitly weak/temporary,
  `ActorMiddleware`'s doc comment names it as the swap-in point for
  `providers/identity` later), generic `ResourceHandler[S,St]` (14
  instantiations in `server.go`), `GraphHandler`, `RegistryHandler`
  (`GET/PUT /v1/capabilities/{name}/default`), two-mux composition
  (`/healthz` bypasses auth, `/v1/` doesn't, both have a problem+json
  404 catch-all).
- `cmd/serendipd/main.go`: real entrypoint, graceful shutdown on
  SIGINT/SIGTERM, verified via the real compiled binary in WSL (Go
  wasn't installed there at the time — installed to `~/go-sdk`,
  user-space, no sudo — reused by spec 003).
- **T019's holistic review caught one real, undisclosed scope gap**:
  spec.md had committed to registry get/set-default-provider endpoints
  that no task ever built — fixed same-session (`RegistryHandler`,
  `CapabilityRegistry.SetDefault` wrapping `resource.ErrValidation` for
  a proper 400). **This is the lesson spec 003's T010 explicitly applied
  again**: a task list can look complete while quietly dropping a
  spec.md commitment no task ever claimed — the final holistic review
  earns its place by re-reading spec.md's Scope-IN against the actual
  code, not just re-running diffs already reviewed task-by-task.
- `core/resource` gained `json:"lowerCamelCase"` tags throughout — this
  surfaced and fixed a real bug (`core/store/sqlite`'s cascade-delete
  audit-snapshot builder defeated `omitempty` by wrapping raw DB text in
  `json.RawMessage`). `core/resource.ErrValidation` and
  `core/audit`'s `WithRequestID`/`WithInterface` are also new here.

All 5 acceptance criteria satisfied and tested; every task got an
independent `contract-guardian` review before commit (git log has
per-task detail if needed).

## Spec 001 — what actually got built

`core/{id,resource,store,store/sqlite,graph,audit,event,registry}` —
the full resource model foundation:

- **`core/id`**: `id.New()` — UUIDv7 (ADR 0007), one-package dependency isolation.
- **`core/resource`**: `Metadata`/`Condition`/`Status`/generic `Resource[S,St]`
  envelope, `Validate()` (Kubernetes-style label grammar), all 14 resource
  kinds (Organization, Team, Project, Environment, Site, Cluster, Node,
  Application, Component, Capability, Provider, ProviderInstance, Alert,
  Incident) as `resource.<Kind> = Resource[<Kind>Spec, <Kind>Status]` aliases.
- **`core/store` + `core/store/sqlite`**: generic `Store[S,St]` interface,
  SQLite implementation — one shared `resources` table (JSON-blob columns,
  not per-kind tables), embedded migrations, WAL + `SetMaxOpenConns(1)`.
  Full CRUD with optimistic concurrency (`ErrConflict`/`ErrNotFound`).
- **`core/audit`**: every Create/Update/Delete writes one `audit_log` row
  **inside the same transaction** as the data change — verified with a
  live mutation test (temporarily broke the write, confirmed the test
  catches it, reverted).
- **`core/graph`**: edges table, `SyncEdges`/`Children`/`Dependents`/
  `BlastRadius`. Cross-kind cascade delete (`store.ErrHasChildren`,
  cascade=true recursively deletes post-order) — the one genuinely tricky
  design: a `Store[ProjectSpec,...]` deleting a Project must also delete
  Application-kind children it has no Go type for, so cascade operates on
  kind-agnostic `graph.ResourceRef` + raw-JSON audit snapshots, not the
  typed `S,St`. Has a cycle guard (malformed `OwnerRefs` cycles reject
  with an error, not a stack overflow).
- **`core/event`**: in-process `Bus`, published strictly after
  `tx.Commit()` succeeds (never before — an event for a rolled-back
  mutation would be wrong). Optional per-`Store` via `SetBus`, nil-safe.
- **`core/registry`**: `CapabilityRegistry`/`ProviderRegistry` — thin
  wrappers; `SetDefault` is an ordinary `store.Update`, which is exactly
  what makes acceptance criterion 3 true by construction.
- **`cmd/bench-resource-model`**: 10,011 resources, ~20-23MB real process
  memory on the dev machine (directional — see `docs/PRD.md` Hardware
  section, revalidate on real T470 in Phase 3b).

All 5 acceptance criteria (spec.md) are satisfied and tested. Every task
T001-T016 got an independent `contract-guardian` review before commit —
PASS on all, several real findings caught and fixed along the way (see
git log on `core/` commits for detail per-task if needed; not repeated
here since it's git-archaeology, not current state).

`go.mod`'s `go` directive is `1.25.0`, not `1.22` — `modernc.org/sqlite`
v1.56.0 forced this via Go's toolchain mechanism when it was added in
T010. Still satisfies "Go 1.22+" as a floor (ADR 0003 addendum).

## Tooling set up this session (unrelated to spec 001 — separate track)

- **CodeRabbit**: `.coderabbit.yaml` + GitHub App instructions in
  `GETTING-STARTED.md` (browser-only install, not yet confirmed done by
  user). Auto-review gated to **external contributors only**
  (`.github/workflows/label-external-contributions.yml` labels PRs by
  `author_association`; org members' PRs aren't auto-reviewed since
  internal review covers those already). Local advisory review via
  `.githooks/post-commit` — **blocked on the user running two WSL2
  commands with their sudo password** (exact commands in
  `GETTING-STARTED.md`'s "Local review" section).
- **GitHub tracking board** (Jira-style: Milestone=Phase, Issue=Spec,
  native sub-issues=Task, Project board) — designed, approved, **still
  blocked** on GitHub token permissions (Issues write + Projects, tested
  empirically twice, still 403/GraphQL-permission-denied as of last
  check). User was told to check three specific things (Issues
  Read-vs-Read&write checkbox, Projects org-level permission, a possible
  pending-approval step) — verify with `gh api
  repos/Serendip-Infra/serendip-infra/milestones -f title=test -f
  state=open` before assuming fixed. Once unblocked: 8 milestones
  (Phase 0-6 per `docs/PRD.md`), one Epic issue per spec with
  `--parent`-linked sub-issues T001-T019 under spec 001 (mark done ones
  closed), Project v2 board (Backlog/In Progress/Review/Done).
- **Git remote / SSH identity separation**: fixed by user, confirmed via
  `git remote -v` (aliased hosts in use). No further action.
- **Memory saved**: `architecture_change_guardrails` (feedback-type) —
  don't add a new abstraction when one exists; match existing
  error-handling/observability conventions; **multi-tenant isolation is
  not yet an enforced constitution rule**, flagged for a possible ADR
  once spec 002 (HTTP API/auth) makes it concrete. Also
  `propose_shortcuts_before_switching` (feedback-type, added during spec
  003 planning) — a shortcut that reshapes which spec does what or pulls
  a dependency forward (e.g. integrating a real provider like Thunder
  earlier than the roadmap sequence) needs explicit confirmation before
  switching approach, unlike routine implementation-detail calls.

## Next up

1. **Get explicit approval on `specs/004-bootstrap-agent/plan.md`** (not
   yet given), then implement T001-T010 task-by-task, same rigor as
   specs 002/003 (contract-guardian at the end given it's a new
   top-level component; real verification in WSL2 for anything
   Linux-specific, per precedent).
2. GitHub: PAT permissions got fixed and the tracking board (milestones/
   issues/sub-issues/project) got fully built and spec 001 merged via
   PR #24 — but then `serendipinfra` got flagged (blocks OAuth app
   authorization, and apparently Issues/PR list-view visibility too).
   User emailed GitHub Support directly (SMS verification doesn't support
   Sri Lankan numbers, blocking the normal contact path) and is waiting to
   hear back — expect a few days. **Nothing from spec 002 has been pushed
   anywhere** (all local commits only) — don't assume otherwise. WSL sudo
   step for CodeRabbit local review turned out to be avoidable entirely
   (a Python-based `unzip` shim, no `sudo` needed) — CLI is installed,
   only `coderabbit auth login` (browser) remains, blocked by the same
   account flag. **Do not use `serendipinfra` OR `Yasindu007` (user's
   personal account) for GitHub automation right now** — user explicitly
   asked not to touch the personal account either; leave GitHub alone
   entirely until they say otherwise.

## Standing instructions (fold into CLAUDE.md properly if this section
keeps persisting across many sessions unchanged — don't let it live here
forever)

- Setup wizard: every required capability shows a recommended default
  (clearly labeled) plus alternatives — never hardcode the default as the
  only path. See `docs/PRD.md` "Setup wizard philosophy".
- Run `contract-guardian` before every commit touching `core/` or
  `providers/` — established as a hard habit this session, not optional.
- Keep `docs/PROGRESS.md` current at natural checkpoints — this file, not
  conversation memory, is the durable handoff mechanism (CLAUDE.md
  "Session continuity"). Rewrite stale sections, don't just append.
