# Getting started (day 1 runbook)

1. git init; copy this kit in; commit as chore(repo): scaffold.
2. Drop the two founding conversation exports into docs/raw/ (create it).
3. Open Claude Code at repo root. First session:
   "Use the docs-keeper subagent approach: extract docs/architecture/*.md
    from docs/raw/ per docs/architecture/README.md. Decisions only,
    exploration dropped, open questions at the bottom of each file."
   Review each file yourself. This is your architecture freeze.
4. /clear. Then: "/plan-spec 001-resource-model" -> review plan.md and
   tasks.md carefully (this is the highest-leverage review of the project).
5. Implement task by task. /ship before every commit. /clear between tasks.
6. Only after 001 merges: /new-spec 002-http-api, then 003-reconciler,
   then 004-bootstrap-agent.

Rules of thumb:
- You review plans and contracts; Claude writes code and tests.
- Never accept "done" without the Verify line output.
- Anything upstream (K3s flags, Longhorn CRDs) -> oss-researcher, never
  pasted into the main session.
