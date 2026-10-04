# Progress / session state

**Read this file first in every new session**, before touching code or
docs. It's the single source of truth for "what's actually done, what's in
flight, what's next" — overwritten each session, not appended to. Git
history has the detail if you need archaeology; this file is only ever the
current picture.

Companion docs: `docs/PRD.md` (why/what/roadmap, changes rarely),
`docs/constitution.md` (non-negotiable rules, changes almost never).

Last updated: 2026-10-04

## Current phase

**Spec 001 (Foundation) merged to Serendip-Infra/serendip-infra:main via
PR #24.** Specs 002-006 are committed locally only — decided explicitly
with the user to keep working locally and push everything together at
a later checkpoint, now that GitHub auth works again (see GitHub note
below) — not pushing yet is a choice, not a blocker.

**Spec 006 (Kubernetes access foundation + Provider interface pattern)
— COMPLETE. All 9 tasks committed locally**, holistic contract-guardian
review PASS (one non-blocking `go.mod`/`go mod tidy` bookkeeping gap
fixed same-session). Full picture:

- **Resolves a question left open since spec 001**:
  `docs/architecture/provider-model.md`'s "whether `ApplicationProvider`/
  `InfrastructureProvider` end up as the actual `core/` interface names"
  sat unresolved through 5 specs because no real provider existed to
  force the decision. This spec resolves the *mechanism* (not any
  specific capability's interface shape, still open — spec 007's job):
  `core/provider.Registry`, an in-memory, capability-agnostic map from
  capability name to live Go value, populated only by `cmd/serendipd`
  (the composition root) — the one place allowed to import both `core/`
  and a specific `providers/` package, preserving constitution rule 2
  (`core/` imports no `providers/` package, ever) since `core/provider`
  itself never does. `provider-model.md`'s "Open questions" section
  rewritten to reflect the actual resolution, not left stale.
- **`client-go` researched and rejected, with real evidence, not a
  hunch**: its transitive dependency weight is a documented,
  upstream-acknowledged problem (Kubernetes' own maintainers have an
  open issue about it, cited directly in ADR 0008). Built a hand-rolled,
  generic `providers/kubernetes` REST client instead (~400 LOC):
  kubeconfig parsing (confirmed field-for-field against K3s's own
  source via oss-researcher, not assumed — client-cert auth, YAML lists
  not maps, `preferences: {}` always present) + a minimal
  `Object`/`ObjectMeta` envelope with raw-JSON spec/status, so this
  package has zero hardcoded knowledge of any specific Kubernetes
  object kind.
- **One new dependency, justified and measured, not estimated**:
  `sigs.k8s.io/yaml` (ADR 0008) — chosen because its JSON-round-trip
  design means the *same* struct + `json:` tags work for both the
  kubeconfig (YAML) and Kubernetes API objects (JSON) this package also
  needs. Real transitive weight verified by building a throwaway module
  against it and running `go list -deps`: exactly one additional module
  actually linked into the build (`go.yaml.in/yaml/v2`).
- **T008's holistic review's one real finding**: `go.mod` had
  `sigs.k8s.io/yaml` marked `// indirect` despite being a direct import
  — a `go mod tidy` bookkeeping gap, not a functional or constitutional
  issue (ADR 0008's own text already stated the correct intent). Fixed.
- **T007's real-cluster verification proved the whole chain end-to-end**,
  not just unit tests, on a disposable WSL2 clone (same `wsl --export`/
  `--import` approach as spec 005, never the persistent dev distro):
  `serendipd` started cleanly with `SERENDIP_KUBECONFIG` unset (a
  supported, non-fatal state); a real K3s cluster was bootstrapped via
  spec 005's own already-proven `serendip-agent bootstrap --confirm`;
  `serendipd` run with `SERENDIP_KUBECONFIG` pointed at the real
  `/etc/rancher/k3s/k3s.yaml` logged `"found 1 node(s): [yasinduds]"`,
  which exactly matched `kubectl get nodes --no-headers | wc -l`'s real
  output — not a mocked or assumed result. Clone fully destroyed
  afterward, zero trace confirmed in the persistent dev distro.
- **The registration pattern is proven with real (if throwaway) code,
  not just designed**: a deliberately trivial `nodeLister` capability +
  `kubeNodeLister` implementation, registered and looked up through
  `core/provider.Registry` exactly as a real capability would be,
  clearly marked for replacement by spec 007's real `StorageProvider`.

**Specs 002-005 — COMPLETE.** See their own "what actually got built"
sections below; condensed here now that spec 006 is current. Spec 005's
one notable lesson worth carrying forward: cross-referencing `docs/PRD.md`
against a spec's actual Scope-OUT is what caught the K3s-bootstrap
staleness bug spec 005 then fixed — re-reading adjacent docs, not just
the spec being worked on, is part of the same "check the actual
commitment, don't assume" discipline as the holistic-review lesson from
specs 002/003, now also the mechanism behind spec 006's
`provider-model.md` resolution above.

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

## Spec 005 — what actually got built

`agent/bootstrap` + `cmd/serendip-agent`'s `bootstrap` subcommand — real
K3s single-node install/uninstall, 9 tasks, all merged locally:

- Shells out to the *official* `get.k3s.io` install script and its
  auto-generated `k3s-uninstall.sh` (never reimplementing K3s install
  logic), both gated behind `--confirm`; `--dry-run` is completely
  side-effect-free (traced by hand: returns before any check/exec call).
  Version explicitly pinned (`v1.36.4+k3s1`).
- Cross-checking `docs/PRD.md` against spec 004 found a real staleness
  bug: PRD's POC item 2 attributed K3s bootstrapping to spec 004, which
  had explicitly deferred it — corrected.
- `wsl --install`'s distro-catalog fetch timed out on this dev machine
  (a real, confirmed constraint) — worked around with `wsl --export`/
  `--import`, a pure local clone of the existing WSL Ubuntu distro, now
  the established pattern reused by spec 006.
- T007's real-host verification ran the whole install→verify→uninstall
  cycle for real on a disposable WSL2 clone (never the persistent dev
  distro): K3s genuinely installed, node reported `Ready` in ~70s,
  uninstall genuinely removed it, clone fully destroyed afterward.
- T008's holistic review traced every code path in `runBootstrap` by
  hand (this spec's core safety property) and found one real gap:
  `Install`'s download had no bound except Ctrl-C. Fixed with its own
  separate timeout.
- K3s's own defaults left enabled (Traefik, ServiceLB, local-storage,
  Flannel) since Serendip's eventual replacements don't exist yet.
  Recorded breadcrumb: swapping Flannel for Cilium later likely means a
  fresh cluster, not a live migration.

## Spec 004 — what actually got built

`agent/{discover,identity,client}` + `cmd/serendip-agent` — hardware/
environment discovery, stable node identity, self-registration against
the existing `/v1/nodes` API, 10 tasks, all merged locally:

- `agent/discover`: reads `/proc/meminfo`, `/proc/cpuinfo`,
  `/proc/mounts`, `/sys/block/*`, `/sys/class/net/*` directly (zero
  non-stdlib deps). Verified for real in WSL2 — CPU count matched
  `nproc` exactly, memory matched `/proc/meminfo` exactly.
  Disk-partition-suffix stripping handles both traditional (`sda2`→`sda`)
  and NVMe/eMMC (`nvme0n1p2`→`nvme0n1`) naming.
- `agent/client`: its own wire structs, not an import of
  `core/resource` — confirmed directly against
  `core/store/sqlite/store.go` that `Create` accepts a client-supplied
  `Metadata.ID`. `Upsert` does GET-then-POST-or-PUT keyed by the
  locally-persisted identity.
- T009's holistic review found and fixed two gaps: a documented
  `SERENDIP_AGENT_STATE_DIR` env var that was never wired up, and a
  `Conditions`-wiping bug in `Upsert` (fixed by preserving them across
  updates, with a regression test).
- End-to-end proof in WSL2: `register` run twice against a real
  `serendipd` created a Node then updated the *same* Node
  (`resourceVersion` 1→2), `NodeStatus` fields matching standalone
  `discover` output exactly.

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

1. **Spec 006 is done — the Kubernetes access foundation + Provider
   registry pattern both work for real.** Next is **spec 007: the
   actual Storage/local-path provider** — user already confirmed
   Storage (local-path) as the first real capability/provider
   (2026-10-04) and the two-spec split (006=foundation, 007=the
   provider itself). Spec 007 needs its own planning pass: define the
   `StorageProvider` Go interface (deliberately left open by spec 006 —
   the first real capability interface decision in this project), a new
   `Volume` resource kind under `core/resource`, a `VolumeReconciler` in
   `core/reconcile`, and `providers/storage/localpath` wrapping spec
   006's `providers/kubernetes.Client` — read
   `specs/006-kubernetes-provider-foundation/plan.md`'s Risks section
   first (notes local-path-provisioner's `WaitForFirstConsumer` binding
   mode means a PVC won't bind until a consuming Pod exists, which
   doesn't exist yet in this project — spec 007 likely needs its own
   `Immediate`-binding-mode StorageClass, not K3s's bundled default, to
   get a real Bound result without an Application Platform provider
   existing first). After Storage: Identity/Thunder, Secrets/OpenBao,
   Application Platform/OpenChoreo, Gateway/kgateway, each likely its
   own spec, per `docs/PRD.md`'s POC scope (items 3-4).
2. **specs 002-006 are still unpushed by deliberate choice** (user
   confirmed: keep working locally, push everything together at a later
   checkpoint) — GitHub auth itself is no longer the blocker (see
   GitHub note above), so this is a scheduling decision to revisit, not
   a technical one to keep re-litigating.
3. GitHub tracking board (milestones/issues/sub-issues/project) was
   fully built for spec 001 and merged via PR #24 — extending it to
   cover specs 002-006 once they're pushed is still open. CodeRabbit:
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
