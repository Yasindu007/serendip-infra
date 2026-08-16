# Tasks: Spec 002 — HTTP API over core

Ordered; `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). Run `contract-guardian` before committing anything under
`core/` or `api/` (CLAUDE.md workflow rule).

## T001 — api/ + cmd/serendipd scaffold

- **Do**: Confirm `api/doc.go` matches current scope. Create
  `cmd/serendipd/` directory. No real code yet beyond package decls.
- **Files**: `cmd/serendipd/main.go` (stub `func main() {}`).
- **Verify**: `go build ./...` succeeds.

## T002 — RFC 7807 problem+json + error mapping

- **Do**: `Problem` struct, `WriteProblem`, `MapStoreError` (switches on
  `errors.Is(err, store.ErrNotFound/ErrConflict/ErrHasChildren)`, 400
  fallback for validation errors, 500 default).
- **Files**: `api/problem.go`, `api/problem_test.go`.
- **Verify**: `go test ./api/...` — table-driven tests cover all 5 status
  mappings (404/409/409/400/500).

## T003 — Request ID: middleware + audit schema extension

- **Do**: `RequestIDMiddleware` (generates via `id.New()`, sets
  `X-Request-Id` response header, injects into context). Add
  `audit.Record.RequestID string`. New migration
  `0002_add_request_id.sql` (`ALTER TABLE audit_log ADD COLUMN
  request_id TEXT`). Update `core/store/sqlite/store.go`'s `writeAudit`
  to read the request ID from context and persist it.
- **Files**: `api/middleware.go`, `api/middleware_test.go`,
  `core/audit/audit.go`, `core/store/sqlite/migrations/0002_add_request_id.sql`,
  edits to `core/store/sqlite/store.go`.
- **Verify**: `go test ./core/... ./api/...` — a test asserts the same
  request ID appears in the response header and the resulting audit row.
- **Scope grew during implementation** (contract-guardian-reviewed,
  judged reasonable, noted here for anyone reading this file later): also
  added `audit.WithInterface`/`InterfaceFrom` and replaced the hardcoded
  `Interface: "core"` literal at all 4 `audit.Record{}` construction
  sites in `core/store/sqlite/store.go`. This wasn't explicitly T003's
  job on paper, but shares the exact same context-carrying mechanism and
  call sites as `RequestID`, and no other task in this file was ever
  scoped to implement acceptance criterion 2's "`Interface` is a real
  value, not `\"core\"`" requirement (T004 is actor-only, T016 only
  tests it). Left unassigned, it would have been silently missing until
  someone noticed. Also fixed a spec-001-era test
  (`TestMigrate_AppliesSchemaAndIsIdempotent`) that hardcoded "exactly 1
  migration row," which broke once this task's migration `0002` made
  that assumption stale.

## T004 — Actor extraction middleware

- **Do**: `ActorMiddleware(apiKeys map[string]string)` — reads
  `Authorization: Bearer <token>`, looks up actor, calls
  `audit.WithActor`. Missing/unknown token → 401 problem+json,
  short-circuits before the handler chain reaches `core/store`.
- **Files**: `api/middleware.go` (extends T003's file),
  `api/middleware_test.go`.
- **Verify**: `go test ./api/...` — valid token passes through with
  correct actor in context; missing/invalid token returns 401 and a test
  proves no `core/store` call was attempted (e.g. via a spy store).

## T005 — JSON tags: envelope + condition

- **Do**: Add `json:"camelCase"` tags to `Metadata`, `OwnerRef`,
  `Relationship`, `Condition`, `Status` in `core/resource/{envelope,
  condition}.go`. No field additions/removals, no behavior change.
- **Files**: `core/resource/envelope.go`, `core/resource/condition.go`.
- **Verify**: `go test ./core/...` still green (existing tests must not
  break from the tagging change — case-insensitive fallback should make
  this a non-event, confirm it actually is).

## T006 [P] — JSON tags: org tree

- **Do**: Tags on `OrganizationSpec/Status`, `TeamSpec/Status`,
  `ProjectSpec/Status`, `EnvironmentSpec/Status` in `orgtree.go`.
- **Files**: `core/resource/orgtree.go`.
- **Verify**: `go test ./core/...` green.

## T007 [P] — JSON tags: infra tree

- **Do**: Tags on `SiteSpec/Status`, `ClusterSpec/Status`,
  `NodeSpec/Status` in `infra.go`.
- **Files**: `core/resource/infra.go`.
- **Verify**: `go test ./core/...` green.

## T008 [P] — JSON tags: app

- **Do**: Tags on `ApplicationSpec/Status`, `ComponentSpec/Status`.
- **Files**: `core/resource/app.go`.
- **Verify**: `go test ./core/...` green.

## T009 [P] — JSON tags: capability

- **Do**: Tags on `CapabilitySpec/Status`, `ProviderSpec/Status`,
  `ProviderInstanceSpec/Status`, `ResourceRequirements`,
  `HealthCheckContract`.
- **Files**: `core/resource/capability.go`.
- **Verify**: `go test ./core/...` green.

## T010 [P] — JSON tags: ops

- **Do**: Tags on `AlertSpec/Status`, `IncidentSpec/Status`.
- **Files**: `core/resource/ops.go`.
- **Verify**: `go test ./core/...` green.

## T011 — Generic ResourceHandler[S, St]

- **Do**: `NewResourceHandler[S, St]`, `Register(mux)` wiring
  `POST/GET/PATCH-as-PUT/DELETE` per Go 1.22 `ServeMux` patterns. `Create`
  decodes body → `store.Create` → 201 + body. `Get` → 200 or mapped
  error. `List` → parses `?ownerId=`/`?label.*=` into
  `store.ListOptions` → 200 array. `Update` decodes body (must include
  `Metadata.ID`/`ResourceVersion`) → `store.Update` → 200 or mapped
  error. `Delete` reads `?cascade=true` → `store.Delete` → 204 or mapped
  error (409 on `ErrHasChildren`).
- **Files**: `api/handler.go`, `api/handler_test.go`.
- **Verify**: `go test ./api/...` — CRUD cycle proven end-to-end on one
  representative kind (Organization) against a real temp-file SQLite
  store, plus each error-mapping case (404/409/400) exercised through
  the actual HTTP handler, not just `MapStoreError` in isolation (T002
  already covers that unit).

## T012 — Mount all 14 kinds + server assembly

- **Do**: `NewServer(db, bus, apiKeys)` constructs one `Store[S,St]` +
  `ResourceHandler` per kind (14 explicit instantiations), applies the
  middleware chain (request ID → actor → mux), adds a `GET /healthz`
  endpoint (200, no auth required).
- **Files**: `api/server.go`, `api/server_test.go`.
- **Verify**: `go test ./api/...` — a test hits every one of the 14
  kinds' `POST` + `GET` routes once each (smoke, not full CRUD per kind
  — T011 already proved the pattern deeply on one kind) and confirms
  `/healthz` responds without a token.

## T013 — Graph read endpoints

- **Do**: `GraphHandler` wrapping `graph.SQLiteGraph`. Routes:
  `GET /v1/graph/{kind}/{id}/children`, `.../dependents`,
  `.../blast-radius`. Kind-agnostic per `graph.ResourceRef`.
- **Files**: `api/graph_handler.go`, `api/graph_handler_test.go`.
- **Verify**: `go test ./api/...` — test creates a small owns/depends-on
  graph via the store, hits all 3 endpoints, confirms correct results
  (mirrors T013 spec 001's graph tests, now through HTTP).

## T014 — cmd/serendipd wiring

- **Do**: Real `main()`: open store (path from env/flag), migrate,
  construct everything T012 needs, read `SERENDIP_API_KEYS`,
  `http.ListenAndServe` (port from env/flag, default e.g. `:8080`).
  Graceful shutdown on SIGINT/SIGTERM.
- **Files**: `cmd/serendipd/main.go` (replaces T001's stub).
- **Verify**: `go run ./cmd/serendipd` starts, `curl localhost:8080/healthz`
  returns 200, Ctrl-C shuts down cleanly (no hung process).

## T015 [A] — Handler unit tests (breadth)

- **Do**: Fill any gaps T011-T013's own tests didn't cover: malformed
  JSON body → 400, wrong `Content-Type` handling, `List` with combined
  owner+label filters, `Update` with stale `ResourceVersion` → 409
  through the actual HTTP path (not just the store-level test from
  spec 001).
- **Files**: `api/handler_test.go` (extended).
- **Verify**: `go test ./api/...` green, no gap between this and spec
  001's acceptance criterion 1 language ("table-driven tests cover...").

## T016 [A] — Integration test: acceptance criteria 2-4

- **Do**: One test file proving spec.md's criteria directly: create via
  API → get via API → delete via API → verify unretrievable; audit log
  shows real `Interface` (not `"core"`) and real `Actor` (not
  `"unknown"`) for each; a rejected (no-actor) mutation produces zero
  audit rows and zero events; response request ID matches the audit
  row's `RequestID`.
- **Files**: `api/acceptance_test.go`.
- **Verify**: `go test ./api/...` — each acceptance criterion has a
  named test asserting it directly, mirroring spec 001's
  `TestAcceptanceCriterion2_...` pattern.

## T017 — Import boundary + naming audit

- **Do**: Confirm `api/` imports no `providers/` package directly
  (none exist yet, but the rule matters going forward) and no vendor
  product names leaked into `api/` identifiers/comments.
- **Files**: none expected.
- **Verify**: `bash .claude/scripts/check-imports.sh` passes (currently
  scoped to `core/` — confirm whether it should be extended to `api/`
  as part of this task, or left as a manual grep for now).

## T018 [A] — Docs sync

- **Do**: Update `docs/PROGRESS.md` (spec 002 complete, next is spec
  003). Update `api/CLAUDE.md` if implementation revealed anything the
  original starter-kit guidance didn't anticipate. Note the static
  API-key auth's swap-in point clearly for whoever builds
  `providers/identity` later.
- **Files**: `docs/PROGRESS.md`, possibly `api/CLAUDE.md`.
- **Verify**: no stale language remains describing spec 002 as
  not-started.

## T019 — Final acceptance pass + contract-guardian

- **Do**: `go test ./core/... ./api/... -v` full run, re-check all 5
  acceptance criteria against actual test output, holistic
  `contract-guardian` review across `core/` (the audit/migration
  changes) and all of `api/`.
- **Files**: none (review/gate task).
- **Verify**: all green, `check-imports.sh` clean, contract-guardian
  PASS, all 5 acceptance criteria individually confirmed.
