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

**Phase 0 — Setup & architecture freeze: complete.** User reviewed
`docs/architecture/*.md` and ADRs 0004-0006 and approved committing. Next
phase is Foundation (spec 001 planning) — see "Not yet done / next up".

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

## Not yet done / next up

1. Phase 0 batch committed (architecture docs, ADRs 0004-0006, PRD,
   PROGRESS, .gitignore) — see git log for the commit.
2. **Next: `/plan-spec 001-resource-model`** — review plan.md and tasks.md
   carefully, this is the highest-leverage review of the project (per
   `GETTING-STARTED.md`). Not started yet.
3. Then implement spec 001 task by task, `/ship` per commit, `/clear`
   between tasks.

## Standing instructions this session established (not yet in CLAUDE.md structurally — verify they've been folded in; if this section still exists next session, they may not have been)

- Setup wizard: every required capability shows a recommended default
  (clearly labeled) plus alternatives — never hardcode the default as the
  only path. See `docs/PRD.md` "Setup wizard philosophy".
- Keep `docs/PROGRESS.md` current: update it at natural milestones (end of
  a spec, end of a phase, before a long gap) so a fresh session — whether
  from manual `/clear`, auto-compaction, or a new terminal — can resume
  with zero back-and-forth. This file, not conversation memory, is the
  durable handoff mechanism (see CLAUDE.md "Session continuity").
