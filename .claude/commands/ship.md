---
description: Pre-commit gate — reviews, checks, then commit
---
Before committing the current work:
1. Run bash .claude/scripts/check-imports.sh
2. Run go build ./... && go vet ./... && go test ./...
3. Use the contract-guardian subagent on the diff if core/ or providers/ changed.
4. Use the go-reviewer subagent on the diff for non-trivial Go changes.
5. Fix blockers, then commit with a conventional-commit message and update the
   tasks.md progress table.
6. Use the docs-keeper subagent if any design decision changed during implementation.
