# Plan: Spec 002 — HTTP API over core

Status: Draft — awaiting human approval before implementation.

## Open-question resolutions

1. **Actor extraction: static API-key-to-actor map, env-configured.**
   `SERENDIP_API_KEYS` env var, format `token1:alice,token2:bob`. Middleware
   reads `Authorization: Bearer <token>`, looks up the actor name, injects
   it via `audit.WithActor(ctx, actor)` — the exact context mechanism
   spec 001 already built for this. Missing/unknown token → 401, before
   any `core/store` call. No real OIDC/Thunder integration (no identity
   provider is implemented yet — that's a future `providers/identity`
   spec); the swap-in point is this one middleware, nothing else changes
   when it happens. Zero new dependencies.
2. **JSON tags: added directly to `core/resource` types, no separate DTOs.**
   14 kinds' worth of hand-written translation structs is real boilerplate
   for no current benefit — nobody has asked for API/domain divergence
   yet (constitution rule 10, scope discipline). Safe to add now: Go's
   `encoding/json` falls back to case-insensitive field-name matching
   when no tag matches, so this doesn't break anything against
   already-marshaled data (moot anyway — no real persisted data exists
   yet, dev/test only). Revisit with real DTOs only when domain and API
   shapes actually need to diverge.
3. **Multi-tenant isolation: flat routes, no path nesting, enforcement
   still deferred.** `Metadata.OwnerRefs` is a `[]OwnerRef` — a resource
   can have multiple owners, which doesn't map cleanly onto single-parent
   path nesting (`/organizations/{id}/projects/{id}/...`) in the first
   place. Every kind gets a flat top-level route
   (`/v1/organizations`, `/v1/projects`, ...); `core/store.ListOptions`'s
   existing owner/label filtering is exposed as query params
   (`?ownerId=`, `?label.key=value`). No access-scoping is enforced —
   stated plainly, not implied otherwise (constitution rule 6's honesty
   principle applied to API security posture, not just capability
   reporting). Real enforcement waits for a policy engine to exist.
4. **Pagination/filtering: exactly what `core/store.List` already
   supports, nothing more.** `?ownerId=`, `?label.<key>=<value>` (repeated
   for multiple labels). No cursor/offset pagination — spec 001 never
   built it either, and adding it speculatively violates scope discipline.
5. **API versioning: `/v1/` prefix from day one.** Unlike most upfront
   scope additions, this one is cheap now and expensive to retrofit later
   (every route changes) — worth the small upfront cost given spec 001's
   constitution rule 7 (UI/CLI/MCP will all depend on this surface later).

## Approach

**Generic CRUD handler, instantiated per kind** — mirrors spec 001's
`core/store/sqlite.Store[S, St]` pattern exactly:

```go
type ResourceHandler[S, St any] struct {
    store store.Store[S, St]
    path  string // e.g. "organizations", for logging/routing only
}
func (h *ResourceHandler[S, St]) Create(w http.ResponseWriter, r *http.Request)
func (h *ResourceHandler[S, St]) Get(w http.ResponseWriter, r *http.Request)
func (h *ResourceHandler[S, St]) List(w http.ResponseWriter, r *http.Request)
func (h *ResourceHandler[S, St]) Update(w http.ResponseWriter, r *http.Request)
func (h *ResourceHandler[S, St]) Delete(w http.ResponseWriter, r *http.Request)
```

One handler type, 14 one-line instantiations in `api/server.go`'s route
registration (Go 1.22+ `net/http.ServeMux` pattern routing:
`"POST /v1/organizations"`, `"GET /v1/organizations/{id}"`, ...). No
router library, no reflection, no code generation — generics do the work
stdlib's older `ServeMux` couldn't, which is exactly why `api/CLAUDE.md`
already commits to "net/http + stdlib routing (Go 1.22 patterns)."

**Middleware chain** (applied in `api/server.go`, outermost first):
request ID generation (`id.New()`, reused from `core/id`) → actor
extraction (401 short-circuit) → the matched `ResourceHandler` method.
Every response gets `X-Request-Id`; every audit record gets the same ID
(requires a new `RequestID` column — see Files touched).

**Error mapping** (`api/problem.go`): a `MapStoreError(error) (status int,
title string)` function switches on `errors.Is` against
`store.ErrNotFound` (404), `store.ErrConflict` (409),
`store.ErrHasChildren` (409), falls back to 400 for `resource.Validate`
failures (identified structurally, not by sentinel — validation errors
are plain `fmt.Errorf`, not a typed error in spec 001), 500 otherwise.
All responses are RFC 7807 (`application/problem+json`).

**Graph endpoints** are thin wrappers directly over `graph.SQLiteGraph`
(built in spec 001's T013, never had a real consumer until now — the
`TestSQLiteGraph_DelegatesToPackageFunctions` test from T019 proved it
works, this is what actually uses it).

**`cmd/serendipd`**: opens the SQLite store (`sqlite.Open` +
`sqlite.Migrate`), constructs one `Store[S,St]` per kind (all `SetBus`
to a shared `event.NewInProcessBus()`), constructs `CapabilityRegistry`/
`ProviderRegistry`, wires `api.NewServer(...)`, starts `http.ListenAndServe`.

## Interfaces / types to create

```go
// api/problem.go
type Problem struct {
    Type, Title, Detail, Instance string
    Status int
}
func WriteProblem(w http.ResponseWriter, status int, title, detail, requestID string)
func MapStoreError(err error) (status int, title string)

// api/middleware.go
func RequestIDMiddleware(next http.Handler) http.Handler
func ActorMiddleware(apiKeys map[string]string) func(http.Handler) http.Handler
func RequestIDFromContext(ctx context.Context) string

// api/handler.go
type ResourceHandler[S, St any] struct { store store.Store[S, St]; path string }
func NewResourceHandler[S, St any](s store.Store[S, St], path string) *ResourceHandler[S, St]
func (h *ResourceHandler[S, St]) Register(mux *http.ServeMux)  // registers all 5 routes

// api/graph_handler.go
type GraphHandler struct { graph graph.Graph }
func (h *GraphHandler) Children/Dependents/BlastRadius(w http.ResponseWriter, r *http.Request)

// api/server.go
func NewServer(db *sql.DB, bus event.Bus, apiKeys map[string]string) *http.Server

// core/audit (extended, not replaced)
type Record struct {
    // ...existing fields...
    RequestID string // new
}

// cmd/serendipd/main.go
func main()
```

Per-kind JSON tags: every field in `core/resource/{envelope,condition,
orgtree,infra,app,capability,ops}.go` gets a `json:"camelCase"` tag.
Exact tag names are implementation detail, not pinned here — follow
standard Go JSON convention (camelCase, matching field intent).

## Files touched

```
core/audit/audit.go                     (add RequestID field)
core/store/sqlite/migrations/0002_add_request_id.sql  (new, additive)
core/store/sqlite/store.go              (writeAudit passes RequestID through)
core/resource/*.go                      (json tags added, no field/behavior changes)
api/doc.go                              (already exists)
api/problem.go, problem_test.go
api/middleware.go, middleware_test.go
api/handler.go, handler_test.go
api/graph_handler.go, graph_handler_test.go
api/server.go, server_test.go           (integration test, httptest + real sqlite)
cmd/serendipd/main.go
docs/PROGRESS.md                        (updated on completion)
```

## Risks

- **`RequestID` migration touches spec-001's audit schema.** Additive
  only (new nullable column, new migration file) — `Migrate()`'s
  idempotent apply-in-order design (T010) handles this cleanly, no
  existing migration is edited. Confirmed safe by the same mechanism
  that made T010's idempotency test pass.
- **Static API-key auth is intentionally weak** — acceptable for this
  spec's scope (no identity provider exists to integrate with yet) but
  must not be mistaken for real authentication. `docs/PROGRESS.md` and
  this plan should make the swap-in point obvious so nobody load-bears on
  the static-key mechanism past whenever `providers/identity` exists.
- **Flat routes + no enforcement means the API is honestly, not just
  currently, unscoped.** Constitution rule 6 concern: nothing in this
  spec must claim or imply tenant isolation that doesn't exist. API docs/
  responses should not use language like "your organization's..." that
  implies a scoping guarantee that isn't real yet.
- **14 generic-handler instantiations is a lot of near-identical wiring**
  in `server.go` even with the generic type doing the real work — flagged
  as acceptable repetition (each line is trivial and independently
  readable) rather than something worth abstracting further at this
  scale.

## Explicitly NOT done in this spec

- Reconciliation engine (spec 003) — mutations create desired state only.
- Real provider implementations (Thunder, OpenBao, ...).
- Real OIDC/Thunder-based authentication — static API keys only.
- Policy engine / RBAC enforcement.
- UI, CLI, MCP.
- Multi-tenant access-scoping enforcement (routes are flat and unscoped
  by design this spec, not merely unfinished).
- Pagination beyond owner/label filtering.
- Rate limiting, request size limits, CORS, TLS termination (assumed to
  sit in front of this, e.g. a future gateway/reverse proxy — not this
  spec's concern per `providers/` boundary).
