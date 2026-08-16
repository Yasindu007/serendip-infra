# Progress / session state

**Read this file first in every new session**, before touching code or
docs. It's the single source of truth for "what's actually done, what's in
flight, what's next" — overwritten each session, not appended to. Git
history has the detail if you need archaeology; this file is only ever the
current picture.

Companion docs: `docs/PRD.md` (why/what/roadmap, changes rarely),
`docs/constitution.md` (non-negotiable rules, changes almost never).

Last updated: 2026-08-16

## Current phase

**Spec 001 (Foundation) merged to Serendip-Infra/serendip-infra:main via
PR #24.** All 19 tasks + epic closed and linked (locally only — see
GitHub note below, nothing from spec 002 has been pushed anywhere yet).

**Spec 002 (HTTP API) in progress: T001-T010 committed locally
(through 695a0d3), T011 (generic `ResourceHandler[S,St]`) next.**
`api/{problem.go, middleware.go}` exist and are tested: RFC 7807 error
mapping, request-ID middleware, actor-extraction middleware (the real
authn gate — got a deliberately security-focused contract-guardian pass,
no bypass found). `core/resource` now has `json:"lowerCamelCase"` tags
throughout (T005-T010) — this surfaced and fixed a real bug along the
way: `core/store/sqlite`'s cascade-delete audit-snapshot builder
(`rawSnapshotJSON`) wrapped raw DB text in `json.RawMessage`, which
silently defeats `omitempty` (an empty map/slice is stored as the literal
string `"null"`, and `RawMessage("null")` has `len()==4`, never counting
as empty) — contract-guardian caught this on a re-review (BLOCK, then
PASS after the fix). Worth remembering: `core/resource.ErrValidation` is
new too (T002) — `resource.Validate()` didn't have a sentinel error
before spec 002 needed one to map validation failures to 400 without
string-matching. `core/audit` gained `WithRequestID`/`RequestIDFrom` and
`WithInterface`/`InterfaceFrom` (T003) — the latter wasn't explicitly
scoped to any task on paper but shared T003's exact call sites and no
other task claimed it, so it got bundled in (contract-guardian agreed
this was the right call).

Plan.md decisions worth remembering without re-reading it: generic
`ResourceHandler[S,St]` (one type, 14 instantiations, mirrors
`core/store/sqlite.Store[S,St]`), static API-key actor extraction
(`SERENDIP_API_KEYS` env var — explicitly weak/temporary until a real
identity provider exists), JSON tags directly on `core/resource` types
(no separate DTOs), flat unscoped routes (no multi-tenant path nesting —
`OwnerRefs` is plural so it doesn't map to single-parent nesting anyway),
`/v1/` prefix from day one.

**Process note this stretch**: user asked me to make spec-level decisions
myself and present them for approval rather than asking open questions
for routine/reversible calls (saved to memory as `decide-then-confirm`) —
applying that from T001 onward in spec 002, most visibly in the
Interface-bundling call above and the rawSnapshotJSON fix.

**GitHub note**: `serendipinfra` account is flagged by GitHub (blocks
OAuth app authorization — hit this trying `coderabbit auth login` —
and apparently also Issues/PR list-view visibility, though individual
resources remain fully intact via direct API access). Support contact is
blocked by SMS-verification not supporting Sri Lankan numbers; user has
emailed GitHub Support directly (2026-08-16) and is waiting to hear back
— expect this to take a few days. **Do not use `serendipinfra` for any
GitHub automation until resolved.** User's personal account
(`Yasindu007`) is confirmed unaffected but the user does not want it used
for this project's automation either — leave GitHub tooling alone
entirely until the flag clears.

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
  once spec 002 (HTTP API/auth) makes it concrete.

## Next up

1. **T011** (generic `ResourceHandler[S,St]`) — the actual HTTP CRUD
   layer, then T012 (mount all 14 kinds + server assembly), T013 (graph
   read endpoints — finally gives `graph.SQLiteGraph` a real consumer),
   T014 (`cmd/serendipd` wiring), T015-T019 (tests, import audit, docs
   sync, final pass) per `specs/002-http-api/tasks.md`.
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
