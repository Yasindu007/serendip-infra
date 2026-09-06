# Progress / session state

**Read this file first in every new session**, before touching code or
docs. It's the single source of truth for "what's actually done, what's in
flight, what's next" — overwritten each session, not appended to. Git
history has the detail if you need archaeology; this file is only ever the
current picture.

Companion docs: `docs/PRD.md` (why/what/roadmap, changes rarely),
`docs/constitution.md` (non-negotiable rules, changes almost never).

Last updated: 2026-09-06

## Current phase

**Spec 001 (Foundation) merged to Serendip-Infra/serendip-infra:main via
PR #24.** Specs 002, 003, and 004 are committed locally only — decided
explicitly with the user to keep working locally and push everything
together at a later checkpoint, now that GitHub auth works again (see
GitHub note below) — not pushing yet is a choice, not a blocker.

**Spec 004 (Bootstrap agent — discovery/identity/self-registration) —
COMPLETE. All 10 tasks committed locally**, holistic contract-guardian
review PASS (two non-blocking findings fixed same-session, re-reviewed
clean). Full picture:

- **Scope, approved before implementation**: NOT the full 8-item
  `bootstrap-agent.md` responsibility list — only hardware/environment
  discovery, stable node identity, and self-registration against the
  *already-existing* `/v1/nodes` API (spec 002). K3s install, full
  negotiated enrollment (needs a topology planner), and daemon-mode
  health reporting are explicitly deferred — same "don't build against
  infra that doesn't exist yet" reasoning specs 002/003 used for real
  providers and the apply-side of reconciliation. Zero new `api/`/`core/`
  code — purely additive under `agent/` + `cmd/serendip-agent/`,
  `core/id` is the only `core/` import (`.claude/scripts/check-imports.sh`
  extended and verified to actually catch a violation, not just pass).
- **Researched two WSO2 datacenter repos before starting** (user's
  request) for any reusable hardware-discovery tricks —
  `wso2/open-cloud-datacenter`'s `dc-agent` turned out to have no
  `/proc`/`/sys` discovery code at all (connection-lifecycle-only,
  pre-discovery-stage); the other repo was inaccessible. One idea noted
  but deliberately not acted on: `dc-agent`'s node identity is
  server-issued (a minted token with region/zone baked in), not
  self-generated like this spec's locally-persisted UUID — worth
  reconsidering *if* a future full-enrollment spec needs to resist a
  spoofing/compromised agent, not relevant to this spec's trusted,
  one-shot, human-invoked scope.
- **Real Linux facts, not guessed values**: `agent/discover` reads
  `/proc/meminfo`, `/proc/cpuinfo`, `/proc/mounts`, and
  `/sys/block/*`/`/sys/class/net/*` directly (zero non-stdlib deps).
  Parse functions are tested against committed fixture files (portable,
  no real host needed in CI); the real I/O layer was verified for real
  in WSL2 — CPU count matched `nproc` exactly, memory matched
  `/proc/meminfo` exactly. Disk-partition-suffix stripping handles both
  traditional (`sda2`→`sda`) and NVMe/eMMC (`nvme0n1p2`→`nvme0n1`,
  `mmcblk0p1`→`mmcblk0`) naming — the latter needed its own regex since
  a plain trailing-digit strip would wrongly mangle `nvme0n1`.
- **`agent/client` defines its own wire structs, doesn't import
  `core/resource`** — confirmed against `core/store/sqlite/store.go`
  directly (not assumed) that `CreatedAt` is server-preserved,
  `UpdatedAt`/`Kind` are server-set regardless of client input, and
  `Create` accepts a client-supplied `Metadata.ID`. `Upsert` does
  GET-then-POST-or-PUT keyed by the locally-persisted identity, with one
  retry on a 409 (a one-shot CLI doesn't need an unbounded retry loop).
- **T009's holistic review (same discipline as specs 002/003's
  T019/T010) found two real, worth-fixing gaps, no blocking ones**: (1)
  `plan.md` had named a `SERENDIP_AGENT_STATE_DIR` env var as the
  `--state-dir` override mechanism, but T006 never actually wired it up
  — fixed with the same flag-then-env-then-fallback precedence
  `cmd/serendipd` already uses. (2) `NodeStatusWire` had no `Conditions`
  field, so `Upsert` would have silently wiped any `Conditions` a future
  Node reconciler sets — fixed by preserving them across both the
  initial and retry-after-409 update paths, with a regression test.
  Binary size measured honestly (not assumed): 8.6MB unstripped / 6.0MB
  stripped in WSL2, both well under `agent/CLAUDE.md`'s 30MB target.
- **End-to-end proof, not just unit tests**: built real binaries in
  WSL2, ran `discover` (values verified against `nproc`/`/proc/meminfo`
  directly), started a real `serendipd`, ran `register` twice — first
  created a Node (`resourceVersion=1`), second updated the *same* Node
  (`resourceVersion=2`, not a duplicate), with `NodeStatus` fields
  matching standalone `discover` output exactly.

**Spec 003 (Reconciliation engine) and spec 002 (HTTP API) — COMPLETE.**
See their own "what actually got built" sections below; condensed here
now that spec 004 is current. Spec 003's one notable lesson, still worth
carrying forward: its holistic review caught a vendor-name-in-a-comment
constitution violation inside `core/reconcile` (K3s/OpenBao mentioned
where rule 1 applies unqualified) — fixed, re-reviewed clean.

**GitHub note (updated 2026-09-06, auth issue resolved)**: `serendipinfra`
GitHub auth is now working. The PAT-generation flow had a real,
unexplained problem (manually-created tokens repeatedly came back
"invalid" from GitHub itself, tried via masked interactive prompt, a
file piped via `--with-token`, and a `$env:GH_TOKEN` env var — all three
independently rejected the same copied token) — resolved by using `gh
auth login`'s **browser device-flow** option instead of pasting a PAT,
which worked immediately. If a PAT is ever needed again and manual
paste keeps failing, try device-flow login first rather than
re-debugging the paste path. Current token scopes:
`admin:public_key`, `gist`, `read:org`, `repo` — **missing `project` and
`workflow`**; add with `gh auth refresh -h github.com -s
project,workflow` only when Projects v2 board work or a
`.github/workflows/*.yml` push is actually needed, not preemptively.
`Yasindu007` remains a separate logged-in account, correctly inactive —
standing instruction not to use it for this project stands.

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

## Spec 003 — what actually got built

`core/reconcile/{engine,health,cluster,audit}.go` + `cmd/serendipd`
wiring — the reconciliation engine, 11 tasks, all merged locally:

- `Engine`: event-driven (subscribes to `core/event.Bus`, non-blocking
  enqueue per that bus's own "handlers must be fast" contract) +
  30s-periodic-resync dispatch to per-`resource.Kind` `Reconciler`s.
  Single goroutine, sequential — sufficient at edge-1 scale. Each
  `Reconcile` call gets a bounded timeout and reconciler-specific audit
  identity (`Interface="reconcile"`, `Actor="system:reconciler"`) set on
  its context.
- `HealthChecker`/`TCPHealthChecker`: one implemented
  `HealthCheckContract.Kind` (`"tcp"`), stdlib-only, reusable unchanged
  by any future real provider.
- `ClusterReconciler`: the one concrete `Reconciler` — resolves a
  Cluster's Provider, runs its health check, writes a `"Healthy"`
  Condition, idempotently (`Update` only called when the computed
  `{Status, Reason}` actually differs from what's stored — proven
  directly via `ResourceVersion`/audit-row-count assertions, not just
  "no error").
- `cmd/serendipd/main.go`: engine runs alongside the HTTP server, sharing
  the shutdown-signal `ctx` — a real shutdown race was designed around
  (the engine's error channel only fires on genuine crashes, never on
  plain `ctx` cancellation, so it can't race Go's `select` against the
  HTTP shutdown branch).
- **T010's holistic review caught one real (if trivial) constitution
  violation**: `TCPHealthChecker`'s doc comment named K3s/OpenBao inside
  `core/reconcile`, where rule 1 applies unqualified (unlike spec 002's
  Thunder mention in `api/middleware.go`, correctly outside `core/`).
  Fixed, re-reviewed: PASS.

All 6 acceptance criteria satisfied and tested, including two proven
end-to-end via the real compiled binary in WSL (not just `go test`): a
Cluster created through the live API got marked `Healthy: True` within
~100ms, and a real SIGINT still shuts down cleanly with both the HTTP
server and engine active.

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

1. **Spec 004 is done — Phase 2 (Bootstrap agent) is complete.** Per
   `docs/PRD.md`'s roadmap, Phase 3 is POC validation on the dev machine
   (not real T470 hardware yet — that's 3b). Check `docs/PRD.md`'s POC
   scope section for what's actually next in sequence before assuming;
   the POC needs at least one real provider live per required capability
   (Identity/Thunder, Secrets/OpenBao, Application Platform/OpenChoreo,
   Storage/local-path, Gateway/kgateway) plus a demo app deployed
   end-to-end — that's a bigger jump than specs 001-004's pattern of one
   self-contained slice each, so it may need breaking into its own
   sub-specs. Decide and present that breakdown, don't just start coding.
2. **specs 002/003/004 are still unpushed by deliberate choice** (user
   confirmed: keep working locally, push everything together at a later
   checkpoint) — GitHub auth itself is no longer the blocker (see
   GitHub note above), so this is a scheduling decision to revisit, not
   a technical one to keep re-litigating.
3. GitHub tracking board (milestones/issues/sub-issues/project) was
   fully built for spec 001 and merged via PR #24 — extending it to
   cover specs 002-004 once they're pushed is still open. CodeRabbit:
   CLI installed, only `coderabbit auth login` (browser) remains,
   previously blocked by the account flag — retry now that auth works,
   not yet re-attempted this session.

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
