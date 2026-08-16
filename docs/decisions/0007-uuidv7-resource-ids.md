# ADR 0007: UUIDv7 (google/uuid) for resource IDs

Status: Accepted
Date: 2026-08

## Context
Every Serendip resource (`docs/architecture/resource-model.md`) needs a
unique `Metadata.ID`. Spec 001 left the format an open question: ULID
(`github.com/oklog/ulid/v2`) vs. UUIDv7 (RFC 9562) vs. hand-rolling a
sortable ID from `crypto/rand`. Constitution rule 4: standard library
first, any new dependency needs an ADR stating what it buys us and its
transitive weight.

Current-state check (2026-08, via oss-researcher):
- RFC 9562 (UUIDv7) is finalized, not draft.
- `github.com/google/uuid` has stable, non-experimental `NewV7()` support —
  not a niche or experimental API.
- `github.com/oklog/ulid/v2` is still maintained but offers no advantage
  over UUIDv7 for our use: both are time-sortable-prefix IDs, and
  `Metadata.CreatedAt` already carries real creation time separately, so
  the sortable-prefix property is a nice-to-have either way, not decisive.
- Hand-rolling a sortable ID from `crypto/rand` + a timestamp prefix was
  considered and rejected: it reinvents a spec with no tooling/interop
  payoff over taking one well-audited dependency.

## Decision
Use `github.com/google/uuid`'s `uuid.NewV7()` for all `Metadata.ID` values,
wrapped behind `core/id.New() string` so the dependency is isolated to one
package (swappable later without touching call sites — same pattern as
every other capability/provider abstraction in this project).

## Consequences
- One new direct dependency: `google/uuid` (zero transitive dependencies,
  single well-audited package, extremely widely vendored across the Go
  ecosystem).
- IDs are 36-character UUID strings (not ULID's 26-character Crockford
  base32), slightly larger in storage/index size — negligible at this
  scale (spec 001's acceptance criterion targets 10k resources).
- `core/id` is the only package that imports `google/uuid` directly;
  everything else calls `id.New()`.
