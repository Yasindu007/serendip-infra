# Contributing

Thanks for helping. Serendip is built spec by spec, and every change
answers to [`docs/constitution.md`](docs/constitution.md): read it first.

## How work is tracked

Everything lives on the [roadmap board](https://github.com/orgs/Serendip-Infra/projects/2).

| Type | What it is | How it's made |
|---|---|---|
| **Epic** | One spec (`specs/NNN-name/`). | The *Epic* form, once the spec is approved. |
| **Task** | One `T###` from the spec's `tasks.md`. A sub-issue of its epic. | The *Task* form, or from `tasks.md`. |
| **Sub-task** | A piece of a task that needs its own tracking. A sub-issue of the task. | The *Task* form. |
| **Bug** | Something broken that isn't fixed on the spot. | The *Bug report* form. |
| **Feature** | A request or idea, before it's a spec. | The *Feature request* form. |

- **Planned specs** sit in the **v1 GA** milestone.
- **Labels:** a type (`epic`, `task`, `bug`, `enhancement`,
  `documentation`), a priority (`P0`–`P3`), an area (`area: core`,
  `area: api`, ...), and `needs-triage` or `blocked` when they apply.
- **The board** moves items itself: added → Backlog, closed → Done.

## How a change is made

1. **A spec first:** no implementation without `specs/NNN-name/spec.md`,
   then `plan.md` (approved by a maintainer) and `tasks.md`.
2. **Task by task:** each task has a `Verify:` line, and isn't done until
   it has run.
3. **Reviews before committing:** `contract-guardian` for anything under
   `core/` or `providers/`, and a Go review for non-trivial Go.
4. **Commits** are [conventional commits](https://www.conventionalcommits.org/):
   `feat(core): ...`, `fix(providers): ...`, `docs: ...`.
5. **One PR per spec,** from a `spec-NNN` branch, with `Closes #<epic>`
   in its body. It's merged with a merge commit.

## Rules worth knowing up front

- **Standard library first.** A new dependency needs an ADR in
  `docs/decisions/`.
- **`core/` imports no providers** and names no products
  (`.claude/scripts/check-imports.sh` checks it; CI runs it).
- **Honesty:** a capability that isn't there is shown as unavailable,
  never faked. Accepted risks go in `docs/risk-register.md`.

## Before opening a PR

```sh
gofmt -l .                       # prints nothing
go vet ./... && go build ./...
bash .claude/scripts/check-imports.sh
go test ./...
cd ui    # corepack reads ui/package.json's pinned pnpm from here
corepack pnpm install --frozen-lockfile
corepack pnpm typecheck && corepack pnpm test && corepack pnpm build
```

CI runs the same on every PR.

## Conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).
