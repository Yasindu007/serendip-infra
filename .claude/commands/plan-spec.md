---
description: Write plan.md and tasks.md for an approved spec
---
For spec $ARGUMENTS (find it under specs/):
1. Enter plan mode thinking: read the spec, constitution, relevant
   architecture docs, and existing code. Use the oss-researcher subagent for
   any upstream knowledge needed — do not fetch upstream docs yourself.
2. Write plan.md: approach, interfaces/types to create, files touched, risks,
   what is explicitly NOT done.
3. Write tasks.md: ordered tasks T001..., each with Do / Files / Verify lines.
   Mark [P] for parallel-safe and [A] for subagent-eligible (tests, docs).
4. STOP for my approval before implementing.
