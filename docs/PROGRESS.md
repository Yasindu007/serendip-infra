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

**Phase 0 complete. Phase 1 (Foundation, spec 001) in progress: T001-T012
committed (through d244a4c). T013 (graph edges + cascade delete + blast
radius) next.** core/store/sqlite now has real CRUD + optimistic
concurrency (T011) + transactional audit (T012) — every mutation writes
exactly one audit_log row atomically with the data change, verified by a
live mutation test (temporarily broke the audit write, confirmed the test
catches it, reverted). T013-T019 remain: graph, event bus, registry,
benchmark, import/naming audit, docs sync, final acceptance pass.

## Done

- Starter kit committed: constitution, root + per-package `CLAUDE.md`,
  5 subagent defs, 3 slash commands, hooks (kubectl context guard,
  post-edit build/vet), ADRs 0001-0003 (K3s, storage progression, Go+SQLite).
- `docs/raw/{nexus.md,other.md}` — founding conversation exports — copied
  in locally, **gitignored, never committed** (user's explicit instruction).
- All 10 `docs/architecture/*.md` extracted from the raw source via
  docs-keeper agent. Name fully migrated Nexus→Serendip.
- ADR 0004 (monorepo strategy) written.
- ADR 0005 (OpenChoreo as default Application Platform) and ADR 0006
  (Thunder as default Identity) written, grounded in a live oss-researcher
  check (2026-08) rather than just the year-old source conversation.
  Key facts baked into those ADRs, worth remembering without re-reading them:
  - OpenChoreo: 1.0 GA, CNCF Sandbox, actively developed. Real risk: control
    plane wants ~4-8GB RAM alone — tight on a shared T470, **unverified**,
    gated on a Phase 3b benchmark before it's an `edge-1` default.
  - Thunder: renamed **ThunderID**, repo moved to `github.com/asgardeo/thunder`,
    hit **v1.0.0 on 2026-08-15** — zero field track record at time of
    decision. ADR 0006 defines explicit fallback triggers to Keycloak.
- `docs/architecture/*.md` cross-references updated to point at ADR 0005/0006
  instead of flagging them as open gaps.
- `docs/PRD.md` written — product scope, POC definition, default-provider
  table, setup-wizard UX philosophy (recommended default + alternatives,
  never a forced single choice).
- `.gitignore` added (`docs/raw/` excluded, plus standard Go/Node/editor
  entries).
- **Hardware clarification locked in** (see `docs/PRD.md` Hardware
  section): dev/POC machine is RTX 4060 / Ryzen 7 / 32GB RAM, available
  now. Reference/target hardware stays ThinkPad T470-class per constitution
  rule 4 — unchanged, just not purchased yet. POC runs on the dev machine;
  T470 purchase is gated on POC success (roadmap Phase 3b in `PRD.md`).
  Every resource-footprint claim (ADR 0005 OpenChoreo RAM, ADR 0006 Thunder
  "lighter than Keycloak") stays unverified until actually benchmarked on a
  T470 — the dev machine's headroom is not evidence.

## Done (spec 001 implementation)

- `specs/001-resource-model/plan.md` + `tasks.md` (T001-T019) drafted, user-approved.
- **T001** (165f61b): `go.mod` (`github.com/serendip-infra/serendip-infra`,
  `go 1.22`), `core/{id,resource,store,registry,graph,event,audit}/` scaffold,
  `google/uuid` dep, ADR 0007.
- **T002** (a2eb3a9): `core/id.New()` (UUIDv7 wrapper), tests pass.
- **T003** (de8ac80): `core/resource` envelope — `Metadata`, `Kind`,
  `EdgeType`+4 constants, `OwnerRef`, `Relationship`, `Condition`/`Status`,
  generic `Resource[S, St]`, `Validate()`. contract-guardian PASS both
  times, flagged 2 plan/code drifts (Kind type instead of bare string,
  fuller label-prefix grammar) — both fixed by updating `plan.md` to match,
  not by changing the code (the code was the better call in both cases).
- **T004-T008** (fc3dc6e): all 14 per-kind resource types (org tree,
  infra tree, app, capability, ops), each a `resource.<Kind> =
  Resource[<Kind>Spec, <Kind>Status]` type alias. contract-guardian PASS;
  fixed one advisory (vendor names in test fixtures → vendor-neutral),
  left two as reasoned/intentional (ProviderInstanceSpec.Config,
  AlertSpec fields — slightly beyond doc-named scope but not speculative).
  Key things NOT yet populated by anything (types only, expected): Node
  hardware facts (spec 004's job), Incident correlation (operations.md,
  later spec), Environment.Availability (reconciliation's job, spec 003).
- **contract-guardian is run before every `core/` commit this session** —
  established pattern, keep doing it, not just when convenient.

## Tooling set up this session (unrelated to spec 001, don't confuse with it)

- **GitHub tracking board (Jira-style: Milestone=Phase, Issue=Spec/Epic,
  native sub-issues=Tasks, Project board)**: designed and approved by user,
  **still blocked as of this update**, tested empirically twice. User
  re-ran `gh auth login` as the `serendipinfra` org account (a NEW
  fine-grained PAT, `github_pat_11CLUSRSI0...`) believing they'd granted
  full permissions. Retested: reading Issues now works (was fully blocked
  before), but writing (milestone/label creation) still 403s, and
  `gh project create` still fails with the same GraphQL permission error.
  Likely cause (told user, unresolved): Issues permission is set to
  Read-only not Read-and-write, Projects org-level permission wasn't
  touched, or a fine-grained-PAT org-resource pending-approval step exists
  that hasn't been approved. **Once actually fixed (verify with `gh api
  repos/Serendip-Infra/serendip-infra/milestones -f title=test -f
  state=open` — should NOT 403), resume by building**: 8 milestones
  (Phase 0-6, see `docs/PRD.md` roadmap), one Epic issue per spec (001-004)
  with `--parent`-linked sub-issues T001-T019 under spec 001 (mark
  T001-T008 closed since done), a Project v2 board (Backlog/In
  Progress/Review/Done).
- **Git remote / SSH identity mismatch: fixed by user.** `git remote -v`
  now shows `origin` → `git@github.com-personal:...` and `upstream` →
  `git@github.com-org:...`, matching their SSH config aliases. Confirmed,
  no further action needed.
- **CodeRabbit** (1ac6992, c57eb19): `.coderabbit.yaml` (path-scoped review
  instructions tied to constitution rules) + GitHub App install
  instructions in `GETTING-STARTED.md` (browser-only, needs org-owner
  approval, not scriptable — not yet confirmed done by user). Auto-review
  gated to **external contributors only**: `auto_review.labels:
  [external-contribution]` allowlist +
  `.github/workflows/label-external-contributions.yml` (labels PRs based
  on `author_association`; org members' PRs stay unlabeled since internal
  review already covers those; `@coderabbitai review` comment still works
  manually anytime). Local advisory review via `.githooks/post-commit`
  (background, non-blocking, `core.hooksPath` set locally) — **blocked on
  two manual WSL2 steps** needing the user's `sudo` password:
  `apt-get install unzip`, then `sh /tmp/coderabbit-install.sh` (already
  downloaded to WSL `/tmp`, content-vetted, sha256 `29dadcd3...`), then
  `coderabbit auth login`. Exact commands are in `GETTING-STARTED.md`'s
  "Local review" section.
- **Memory saved**: `architecture_change_guardrails` (feedback-type) —
  user pasted a generic AI-guardrail prompt (had stale "NEXUS" naming),
  most of it duplicates constitution.md rules 1-3/7-8. Non-redundant parts
  saved: don't add a new abstraction when one exists, match existing
  error-handling/observability conventions, and a flagged gap —
  **multi-tenant isolation is not yet an enforced constitution rule**,
  worth an ADR once spec 002 (HTTP API/auth) makes it concrete.

## Next up

1. **T004-T008** (per-kind resource types: org tree, infra tree, app,
   capability, ops — parallel-safe against each other, all depend on T003
   which is done). Keep fields minimal per `resource-model.md`, don't
   invent business fields — see `tasks.md` for exact scope per task.
2. Then T009-T019 in order (store, audit, graph, event bus, registry,
   benchmark, docs sync, final acceptance pass) per `tasks.md`.
3. Three things blocked on the user, not on implementation — check if
   resolved before assuming still blocked: GitHub PAT permissions (board
   setup), WSL sudo steps (CodeRabbit local review), remote URL fix
   (SSH identity separation).

## Standing instructions (confirm still true if this section persists across
many sessions — fold into CLAUDE.md properly if so, don't let it live here
forever)

- Setup wizard: every required capability shows a recommended default
  (clearly labeled) plus alternatives — never hardcode the default as the
  only path. See `docs/PRD.md` "Setup wizard philosophy".
- Run `contract-guardian` before every commit touching `core/` or
  `providers/` — not optional, established as a hard habit this session.
- Keep `docs/PROGRESS.md` current at natural checkpoints (end of a task
  batch, end of a phase, before a long gap) — this file, not conversation
  memory, is the durable handoff mechanism (see CLAUDE.md "Session
  continuity"). Rewrite stale sections, don't just append.
