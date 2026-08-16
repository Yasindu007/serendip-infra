# Spec 002: HTTP API over core

Status: Draft — human review required before plan.md

## Problem

Spec 001's `core/{store,registry,graph,audit,event}` is only reachable from
within the same Go process — nothing outside it (no UI, no CLI, no future
MCP surface, no external tooling) can create/read/update/delete a
resource, query the capability/provider registries, or traverse the graph.
Constitution rule 7 (interface parity: UI = CLI = API = MCP, all consuming
one API) has nothing to point at yet. This spec builds that one API.

`api/CLAUDE.md` (already in the repo) sets hard constraints this spec
inherits, not reopens: stdlib `net/http` + Go 1.22+ pattern routing (no
router library without an ADR), thin handlers (decode → authz → core call
→ encode, no business logic, no direct store/provider access bypassing
core), RFC 7807 problem+json errors, a request ID on every response.

## Scope — IN

- HTTP handlers over `core/store` for every spec-001 resource kind:
  standard CRUD (create/get/list/update/delete) per kind.
- HTTP handlers over `core/registry`: register/list capabilities and
  providers, get/set a capability's default provider.
- HTTP handlers over `core/graph` (read-only): children, dependents,
  blast-radius for a given resource ref.
- Actor extraction for every request, replacing spec 001's `"core"`
  literal in `audit.Record.Interface` with a real value, and giving
  `audit.Record.Actor` a real caller identity instead of `"unknown"`.
  Mechanism is minimal/pluggable — no real OIDC/Thunder integration (no
  identity provider is implemented yet, that's a future `providers/`
  spec) — see Open Questions.
- Error mapping to RFC 7807 problem+json: `store.ErrNotFound` → 404,
  `store.ErrConflict` → 409, `store.ErrHasChildren` → 409,
  `resource.Validate` failures → 400, missing/unresolvable actor → 401.
- Request ID generation and propagation (response header + audit record).
- `cmd/serendipd` — a runnable binary that starts the HTTP server.

## Scope — OUT (explicitly)

- Reconciliation engine (spec 003) — a mutation through this API creates
  desired state only; nothing "does the work" of converging it yet.
  Status/Conditions stay whatever they were at spec 001 (unpopulated).
- Real provider implementations (Thunder, OpenBao, ...) — later specs.
- Real OIDC/Thunder-based human authentication — actor extraction is
  deliberately minimal for this spec (see Open Questions); swapping in
  real identity later must not require reshaping the API.
- Policy engine / RBAC enforcement — `audit.Record.PolicyDecision` stays
  reserved/nil; "authz" in `api/CLAUDE.md`'s handler flow means "actor
  resolved," not "policy-engine-checked" for this spec.
- UI, CLI, MCP — future consumers of this API; not built now.
- Full multi-tenant isolation enforcement — flagged as an open gap in
  `docs/PROGRESS.md`; whether this spec must start enforcing it or defer
  further is an explicit open question below, not assumed either way.

## Acceptance criteria

1. `go test ./api/...` green; table-driven tests cover the shared
   error-mapping logic (404/409/400/401) and prove the CRUD pattern
   correctly on at least one resource kind end-to-end, not necessarily
   duplicated 14 times.
2. A resource created via the API is retrievable via the API with the
   same data; deleting it makes it unretrievable. `audit_log` (via
   `core/audit`) shows `Interface` as a real API-identifying value (not
   `"core"`) and `Actor` as the real resolved caller for every mutation
   made through the API.
3. Every mutating request without a resolvable actor is rejected with 401
   before any `core/store` call happens — no audit record, no data
   change, no event published for a rejected request.
4. Every error response is valid RFC 7807 problem+json and carries a
   request ID; the same request ID is attached to the resulting
   `audit_log` row when the request did reach a mutation.
5. `cmd/serendipd` builds and runs; a health endpoint responds; `go vet
   ./...`, `gofmt -l .`, `check-imports.sh`-equivalent boundary checks
   (no vendor names, `api/` imports no `providers/` package directly)
   all clean.

## Open questions (resolve during plan review)

- **Actor extraction mechanism**: what does "resolvable actor" mean
  concretely for this spec, given no identity provider exists yet?
  Candidates: a static configured API key/bearer token mapped to an
  actor name (simplest, unblocks everything else, real OIDC swaps in
  later behind the same extraction point); a dev-only trusted header
  (weaker, fine for local iteration, wrong default for anything else);
  something else. Needs a decision, not an assumption.
- **JSON serialization approach**: add `json:` struct tags directly to
  the spec-001 `core/resource` types (currently relying on Go's default
  field-name marshaling per spec 001's plan.md), or define separate
  API-layer request/response DTOs that translate to/from `core/resource`
  types? Trade-off: tags-on-core is less code but couples `core/`'s
  marshaling shape to API concerns; DTOs keep `core/` API-agnostic
  (matches constitution rule 1's capability-first framing more cleanly)
  at the cost of translation boilerplate across 14 kinds.
- **Multi-tenant isolation**: does this spec need to start enforcing
  Organization/Project-scoped access (e.g. path-based tenancy,
  rejecting cross-org access), or does the API ship flat/unscoped for
  now with enforcement deferred to whenever a policy engine exists?
  This has been flagged since spec 001 as a real gap, not yet an
  enforced constitution rule — this spec is the first place it becomes
  concrete enough to require an actual decision.
- **Pagination/filtering surface**: `core/store.List` already supports
  kind/owner/exact-label filtering. Does every LIST endpoint expose all
  of that as query parameters now, or a subset, with the rest deferred?
- **API versioning**: `/v1/`-prefixed routes from day one, or deferred
  until there's a reason to need it?
