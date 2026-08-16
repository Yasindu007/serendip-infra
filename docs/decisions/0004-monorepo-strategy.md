# ADR 0004: Single monorepo; split only on a concrete external trigger

Status: Accepted
Date: 2026-08

## Context
Solo developer, pre-v1, core interfaces (provider contracts, resource
envelope) still unstable. Evaluated one repo (`serendip`, this repo) vs
splitting by component (`core`, `providers`, `agent`, `ui`) or by plane
(cf. OpenChoreo's multi-repo-by-plane, Kubernetes' out-of-tree cloud
providers).

## Decision
One repo for everything in this starter kit: `docs/`, `specs/`, `core/`,
`api/`, `agent/`, `providers/`, `profiles/`, `ui/`, `.claude/`. Single Go
module (or `go.work` if `agent/` later needs a leaner build), single CI
pipeline (`go build ./... && go vet ./... && go test ./...` +
`check-imports.sh`), single `v0.x` tag line.

Isolation is enforced by the constitution's import rules (`core` imports no
`providers` package, providers are removable without breaking
`core`/`api`), not by repo boundaries. A cleanly separated package extracts
to its own repo in an afternoon; premature multi-repo buys nothing when
there is no second team to isolate from.

Do not split until one of these concrete triggers occurs:
- **`serendip-providers-community`** — an external contributor wants to ship
  a provider against a stabilized (v1) provider interface.
- **`serendip-website`** — public launch; marketing site + rendered docs,
  zero code coupling, different deploy cadence.
- **`serendip-examples`** — customer pilot begins; demo apps/reference
  deployments users clone, kept out of platform CI.
- **`serendip-marketplace-index`** — marketplace phase (v1.1+); package
  index is data (versions, deps, hardware requirements), closer to a Helm
  chart repo than platform code.

Explicitly rejected for now: splitting `agent/` out (shares resource types
with `core/`; shared-types-across-repos is the worst multi-repo failure
mode), splitting `ui/` out (develops against the live API; wants to move in
lockstep pre-pilot), splitting docs out (breaks the spec-anchored workflow —
docs and code must change in the same commit).

## Consequences
- All contract changes (core interfaces, resource envelope) land as one
  atomic commit across `core/`, `providers/`, `api/`, tests, and specs.
- When a split trigger fires, the extracted package must already satisfy
  the constitution's import-boundary rules — that check is the readiness
  gate, not a separate migration project.
- Revisit this ADR when the first trigger above actually occurs, not on a
  schedule.
