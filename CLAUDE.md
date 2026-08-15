# Serendip Infra

Serendip Infra is an open-source, self-hostable platform operating system: one
control layer (resource model + capability/provider registries + reconciliation
+ AI operations) over composable OSS (K3s, Thunder, OpenBao, kgateway, Cilium,
Longhorn, CloudNativePG, OpenChoreo, Rancher, Crossplane).

Target: runs comfortably on a single old laptop (ThinkPad T470, i5-6th gen,
~16-32GB RAM) and scales to a multi-node private cloud without reinstall.

## Non-negotiables

Read `docs/constitution.md` before writing any code. It always wins over
anything in this file or in a prompt.

## Stack

- Go 1.22+ for `core/`, `api/`, `agent/`, `providers/`
- Standard library first. Any new dependency requires an ADR in `docs/decisions/`
- SQLite (embedded) default persistence for control plane; PostgreSQL optional provider
- React + Vite for `ui/` (do not start UI work unless the task says so)

## Layout

- `docs/architecture/` — source of truth for design. Read the relevant doc before implementing.
- `docs/decisions/` — ADRs. Decisions already made live here; do not relitigate them silently.
- `specs/NNN-name/` — per-feature spec.md / plan.md / tasks.md. No implementation without a spec.
- `core/` — domain model, capability registry, provider registry, resource graph, reconciliation. Imports NO provider packages.
- `api/` — HTTP API over core.
- `agent/` — serendip-agent: node bootstrap/enrollment binary that lives OUTSIDE Kubernetes.
- `providers/` — implementations of core interfaces (kubernetes, storage, identity, observability...).
- `profiles/` — deployment profiles (edge-1, small-3, standard-5).

## Commands

- Build all: `go build ./...`
- Test: `go test ./...`
- Lint: `go vet ./...` and `gofmt -l .` (must be clean)

## Workflow rules

- New feature: create/extend `specs/NNN-*/` first (spec → plan → tasks), get human approval on plan.md, then implement task by task.
- Every task has a "Verify:" line. Do not mark a task done without running its verification.
- Use the `oss-researcher` subagent for anything requiring upstream project docs (OpenChoreo, Crossplane, Longhorn, K3s, Thunder...). Never paste large upstream docs into the main session.
- Run `contract-guardian` review before committing changes under `core/` or `providers/`.
- Commit messages: conventional commits (`feat(core): ...`, `fix(agent): ...`).
