# Tasks: Spec 001 — Resource Model + Registries

Ordered; do not skip ahead. `[P]` = parallel-safe with other `[P]` tasks in
the same block once their shared prerequisite is done. `[A]` = subagent-
eligible (tests/docs), still requires the human-review discipline in
CLAUDE.md workflow rules before commit. Run `contract-guardian` before
committing anything under `core/` (T019), per CLAUDE.md.

## T001 — Scaffold + ADR 0007

- **Do**: `go mod init github.com/serendip-infra/serendip-infra`. Create
  empty `core/{id,resource,store,registry,graph,event,audit}/` package dirs
  each with a `doc.go` stating the package's one-sentence purpose (mirrors
  `core/CLAUDE.md`'s "one package per concern"). Add `google/uuid` as a
  dependency (`go get github.com/google/uuid`). Write ADR 0007 justifying
  it per constitution rule 4 (what it buys us: stable RFC 9562 UUIDv7,
  single well-audited dependency vs. hand-rolling; transitive weight:
  effectively zero, no sub-dependencies).
- **Files**: `go.mod`, `go.sum`, `core/*/doc.go`,
  `docs/decisions/0007-uuidv7-resource-ids.md`.
- **Verify**: `go build ./...` succeeds (empty packages). ADR follows the
  format of ADRs 0001-0006 (Status/Date/Context/Decision/Consequences).

## T002 [P] — core/id

- **Do**: `id.New() string` wrapping `uuid.NewV7().String()`. Table-driven
  tests: generates valid UUIDv7 (version nibble check), no collisions
  across N generations, monotonic-ish ordering property documented (not
  guaranteed across processes, note that in a doc comment).
- **Files**: `core/id/id.go`, `core/id/id_test.go`.
- **Verify**: `go test ./core/id/...` green.

## T003 — core/resource envelope (blocks T004-T008)

- **Do**: `Metadata`, `OwnerRef`, `Relationship`, `EdgeType` (owns/runs-on/
  depends-on/provided-by — string enum shared with `core/graph`),
  `ConditionStatus`, `Condition`, `Status` (embeds `[]Condition`), and a
  `Validate(Metadata) error` covering: `Name` required, `Labels` keys/values
  match Kubernetes label syntax rules (`[a-z0-9A-Z][-a-z0-9A-Z_.]*`, ≤63
  chars — reuse this rule, don't invent a new one, since `resource-model.md`
  says follow Kubernetes conventions).
- **Files**: `core/resource/envelope.go`, `core/resource/condition.go`,
  `core/resource/validate.go`, `core/resource/envelope_test.go`.
- **Verify**: `go test ./core/resource/...` green; table-driven validation
  tests cover at least: empty name rejected, invalid label key rejected,
  valid envelope accepted.

## T004 [P] — Org tree resource types

- **Do**: `Organization`, `Team`, `Project`, `Environment` Spec/Status types
  per `docs/architecture/resource-model.md`'s field list (keep minimal —
  don't invent fields the docs don't name). `Environment.Status` must be
  able to express "single instance, no node-failure protection" per
  constitution rule 6 — reserve a field for it even if nothing sets it yet.
- **Files**: `core/resource/orgtree.go`, `core/resource/orgtree_test.go`.
- **Verify**: `go test ./core/resource/...` green.

## T005 [P] — Infra tree resource types

- **Do**: `Site`, `Cluster`, `Node` Spec/Status. `Node.Status` carries
  hardware facts (CPU, RAM, disk type/size, network, virtualization
  support) per `bootstrap-agent.md` — types only, nothing populates them
  yet (spec 004's job).
- **Files**: `core/resource/infra.go`, `core/resource/infra_test.go`.
- **Verify**: `go test ./core/resource/...` green.

## T006 [P] — Application/Component resource types

- **Do**: `Application`, `Component` Spec/Status. `Component.Spec` includes
  a way to declare `runs-on`/`depends-on` relationships (feeds
  `Metadata.Relationships`, not a separate mechanism).
- **Files**: `core/resource/app.go`, `core/resource/app_test.go`.
- **Verify**: `go test ./core/resource/...` green.

## T007 [P] — Capability/Provider/ProviderInstance resource types

- **Do**: `Capability` (name, required, provider slots, default provider —
  `capability-model.md`'s spec format), `Provider` (descriptor: name,
  capabilities provided, resource requirements, health-check contract —
  data only, per `provider-model.md` Registration section), `ProviderInstance`
  (binds a Provider to a capability + Environment).
- **Files**: `core/resource/capability.go`, `core/resource/capability_test.go`.
- **Verify**: `go test ./core/resource/...` green.

## T008 [P] — Incident/Alert resource types

- **Do**: `Incident`, `Alert` Spec/Status — types only, no lifecycle logic
  (spec explicitly excludes this; don't build `operations.md`'s state
  machine here).
- **Files**: `core/resource/ops.go`, `core/resource/ops_test.go`.
- **Verify**: `go test ./core/resource/...` green.

## T009 — core/store interface

- **Do**: `Store[S, St any]` generic interface, `ListOptions`, sentinel
  errors `ErrNotFound`/`ErrConflict`. No implementation yet.
- **Files**: `core/store/store.go`.
- **Verify**: `go build ./core/store/...` succeeds.

## T010 — SQLite schema + migrations + connection

- **Do**: `resources` table (id, kind, name, labels JSON, owner_refs JSON,
  relationships JSON, spec JSON, status JSON, resource_version,
  created_at, updated_at), `edges` table (from_kind, from_id, to_kind,
  to_id, edge_type, created_at), `audit_log` table (per `core/audit`
  Record shape), `schema_migrations` table. Embed migration SQL via
  `embed.FS`; apply-in-order-once logic. Connection setup: `PRAGMA
  journal_mode=WAL`, `PRAGMA busy_timeout=5000`, `SetMaxOpenConns(1)`.
- **Files**: `core/store/sqlite/migrations/0001_init.sql`,
  `core/store/sqlite/migrate.go`, `core/store/sqlite/conn.go`.
- **Verify**: `go test ./core/store/sqlite/...` — migration applies cleanly
  to a temp file DB, re-running is a no-op (idempotent).

## T011 — SQLite CRUD + optimistic concurrency

- **Do**: Implement `Store[S, St]` against the `resources` table.
  `Update`/`Delete` check `resource_version` matches the caller's, return
  `ErrConflict` on mismatch, increment on success. `List` filters by kind/
  owner/exact-label-match (spec's label-selector open question, resolved
  in plan.md as exact-match only).
- **Files**: `core/store/sqlite/store.go`, `core/store/sqlite/store_test.go`.
- **Verify**: `go test ./core/store/...` — covers acceptance criterion 1
  (validation, optimistic-concurrency conflict case explicitly tested).

## T012 — Audit integration

- **Do**: `audit.Record`, `WithActor`/`ActorFrom` context helpers. Wire
  into the *same transaction* as T011's Create/Update/Delete — not a
  wrapper the caller could forget. `Interface` field literal `"core"` for
  now. `PolicyDecision *string` stays nil (reserved, spec 003+ territory).
- **Files**: `core/audit/audit.go`, `core/audit/context.go`,
  `core/audit/audit_test.go` (plus edits to `core/store/sqlite/store.go`).
- **Verify**: `go test ./core/...` — a test asserts every Create/Update/
  Delete produces exactly one audit record with correct before/after
  snapshot (acceptance criterion 1's audit-emission clause).

## T013 — Graph edges + cascade delete + blast radius

- **Do**: `graph.Graph` interface + SQLite-backed implementation over the
  `edges` table. Edge sync derives `owns` from `Metadata.OwnerRefs` and
  other types from `Metadata.Relationships`, diffed and applied inside
  T011's mutation transaction. `Delete` with `cascade=false` and existing
  `owns`-children returns an error; `cascade=true` deletes children
  post-order in the same transaction. `BlastRadius` does a reverse-edge
  BFS.
- **Files**: `core/graph/graph.go`, `core/graph/edges.go`,
  `core/graph/graph_test.go` (plus edits to `core/store/sqlite/store.go`).
- **Verify**: `go test ./core/...` — covers acceptance criterion 2 exactly
  (Application under Project under Organization produces correct edges;
  deleting a Project with children rejected unless cascade requested).

## T014 [P] — Event bus

- **Do**: In-process `Bus` (`Subscribe`/`Publish`), `Event` type. Published
  post-commit by the SQLite store (after T011-T013's transaction commits).
- **Files**: `core/event/bus.go`, `core/event/bus_test.go` (plus a small
  post-commit publish call added to `core/store/sqlite/store.go`).
- **Verify**: `go test ./core/event/...` — multiple subscribers each
  receive one event per mutation; unsubscribe stops delivery.

## T015 — Capability/Provider registries

- **Do**: `CapabilityRegistry`/`ProviderRegistry` thin wrappers over
  `Store[CapabilitySpec, CapabilityStatus]` / `Store[ProviderSpec,
  ProviderStatus]`. `Default`/`SetDefault` convenience methods — `SetDefault`
  is an ordinary `Update` call, nothing special.
- **Files**: `core/registry/capability.go`, `core/registry/provider.go`,
  `core/registry/registry_test.go`.
- **Verify**: `go test ./core/registry/...` — a test registers two
  providers for one capability and switches the default, asserting no
  code outside registry *data* was touched (acceptance criterion 3,
  literally).

## T016 [A] — Benchmark (dev-machine, directional)

- **Do**: Combined smoke benchmark: create 10k resources across kinds
  (with realistic owner/relationship edges) in the SQLite store, measure
  RSS. Write results to `profiles/edge-1/bench/001-resource-model.md`,
  explicitly labeled "measured on dev machine (RTX 4060 / Ryzen 7 / 32GB),
  NOT T470 reference hardware — see docs/PRD.md Hardware section;
  revalidate in Phase 3b before treating as a shipped constitution-rule-4
  claim."
- **Files**: `core/store/sqlite/bench_test.go` (or a small `cmd/` smoke
  tool if a benchmark harness is cleaner than a Go test),
  `profiles/edge-1/bench/001-resource-model.md`.
- **Verify**: benchmark runs to completion; the actual RSS number is
  recorded (whatever it is — under 50MB satisfies acceptance criterion 5
  as a dev-machine directional result; over 50MB gets recorded honestly,
  not hidden, and flagged for follow-up).

## T017 — Import boundary + naming audit

- **Do**: Run the import-boundary check. Grep `core/` for any OSS product
  name (Longhorn, Thunder, OpenBao, OpenChoreo, Rancher, Crossplane,
  Cilium, kgateway, CloudNativePG, OpenObserve, SigNoz — constitution rule
  1's list) in identifiers/comments; fix any that slipped in during
  T001-T016.
- **Files**: none expected (verification-only task; fixes if needed touch
  whatever file has the violation).
- **Verify**: `bash .claude/scripts/check-imports.sh` passes (acceptance
  criterion 4).

## T018 [A] — Docs sync

- **Do**: Update `docs/architecture/resource-model.md`'s "Open questions"
  section — ID format, Conditions, label/selector are resolved (link ADR
  0007 for ID format; note exact-match-only label filtering with the
  deferral rationale). Update `docs/PROGRESS.md`: spec 001 complete, next
  is spec 002 (HTTP API).
- **Files**: `docs/architecture/resource-model.md`, `docs/PROGRESS.md`.
- **Verify**: no stale "TODO"/"undecided" language remains for the three
  questions this spec resolved.

## T019 — Final acceptance pass + contract-guardian

- **Do**: `go test ./core/... -v` full run; re-check all 5 acceptance
  criteria in `spec.md` explicitly against test output; run
  `contract-guardian` on the full `core/` diff before any commit (CLAUDE.md
  workflow rule — mandatory for `core/` changes).
- **Files**: none (review/gate task).
- **Verify**: `go test ./core/...` green, `go vet ./...` clean, `gofmt -l .`
  empty, `check-imports.sh` clean, contract-guardian reports no
  constitution violations, all 5 acceptance criteria individually
  confirmed against actual test output (not assumed).
