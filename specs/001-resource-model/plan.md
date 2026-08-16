# Plan: Spec 001 — Resource Model + Registries (Foundation v0.1)

Status: Draft — awaiting human approval before implementation.

## Open-question resolutions

The spec left three questions for plan review. Resolved here (research via
oss-researcher, 2026-08, summarized — not upstream docs pasted verbatim):

1. **ID format: UUIDv7**, via `github.com/google/uuid` (`uuid.NewV7()`).
   RFC 9562 is finalized; `google/uuid`'s v7 support is stable, not
   experimental. Rejected ULID (`oklog/ulid/v2`) — same
   time-sortable-prefix property, but a second dependency to justify when
   one suffices; the sortable-prefix benefit is a nice-to-have anyway since
   `Metadata.CreatedAt` already carries real time. Rejected hand-rolling a
   sortable ID from `crypto/rand` — reinvents a spec with no tooling/interop
   payoff over taking one well-audited dependency. **New dependency → ADR
   0007, written as task T001.**
2. **Conditions: one shared status package** (`core/resource` — `Condition`,
   `ConditionStatus`), used by every resource kind's `Status`, per
   `resource-model.md`'s existing lean and to avoid 14 near-identical types.
3. **Label/selector semantics: exact-match only in this spec.** `Store.List`
   filters by kind, owner, and exact label equality. Full Kubernetes
   set-based selector grammar (`in`, `notin`, `exists`) is deferred to
   whichever future spec first needs it (likely the topology planner) —
   building it now with no consumer violates constitution rule 10 (scope
   discipline). Explicitly listed under "Not done" below.

   `resource.Validate` (T003) implements the full Kubernetes label-key
   grammar, not just the single name/value regex T003's task text named:
   `prefix/name` keys are supported, with the DNS-subdomain prefix
   validated separately from the ≤63-char name segment (values use the
   name-segment regex, empty values allowed). This is real Kubernetes
   label-key semantics, already covered by `envelope_test.go`, not a
   speculative addition — documented here because it's more than T003's
   task text literally asked for, per contract-guardian review.

SQLite driver decisions (modernc.org/sqlite, already locked by ADR 0003, not
reopened): `PRAGMA journal_mode=WAL` + `PRAGMA busy_timeout` on connect;
`SetMaxOpenConns(1)` — serializes all writes through one connection. This is
a deliberate simplicity-over-throughput tradeoff appropriate for a
single-process control plane at this scale (see Risks). Hand-rolled
migrations via `embed.FS` + a `schema_migrations` table — no migration
library dependency; no driver-specific objection to this pattern.

## Approach

Everything is a `Resource[S, St]`: one generic envelope
(`Metadata`/`Spec`/`Status`) instantiated per kind, backed by **one shared
SQLite table** (`resources`, columns: id, kind, name, labels JSON,
owner_refs JSON, relationships JSON, spec JSON, status JSON,
resource_version, created_at, updated_at) rather than one table per kind.
Rationale: 14 resource kinds sharing identical envelope mechanics; a shared
table keeps the graph, audit, and event layers kind-agnostic (they operate
on rows, not per-kind SQL), and keeps 14 kinds from becoming 14 hand-rolled
stores. Trade-off accepted and flagged in Risks: SQL-level querying on
spec/status fields is limited (JSON blob), fine for this spec's scope
(CRUD + label/owner filtering), not fine forever.

**Audit and graph-edge sync happen inside the same SQL transaction as every
mutation** — not as an optional decorator someone could forget to wrap a
call with. `Create`/`Update`/`Delete` on the SQLite store: (1) write/modify
the `resources` row with optimistic-concurrency check on
`resource_version`, (2) diff `OwnerRefs`/`Relationships` against the
previous row and sync the `edges` table (owns edges derived from
`OwnerRefs`, other edge types from an explicit `Relationships` field on
`Metadata`), (3) write one `audit_log` row (actor from `context.Context` via
`audit.WithActor`/`audit.ActorFrom`, before/after JSON snapshot, interface
literal `"core"` for now — spec 002 will pass the real interface), (4)
commit, (5) publish a `Created`/`Updated`/`Deleted` event to the in-process
bus post-commit. This satisfies constitution rule 8 ("audit is not optional
per component; it is provided by core") by construction rather than by
convention.

Capability/Provider/ProviderInstance are resource kinds like any other,
stored the same way — `core/registry` is a thin typed convenience layer
(`Default(name)`, `SetDefault(name, provider)`) over the generic store, not
a separate storage mechanism. This is what makes acceptance criterion 3
("switching the default requires no changes outside registry data") true
by construction: `SetDefault` is an ordinary `Update` call.

## Interfaces / types to create

```go
// core/id
func New() string // uuid.NewV7().String()

// core/resource
type Kind string // e.g. "Organization", "Application" — constants per kind file (T004-T008)
type Metadata struct {
    ID, Name        string
    Kind             Kind
    Labels           map[string]string
    OwnerRefs        []OwnerRef
    Relationships    []Relationship   // drives non-owns edges (runs-on, depends-on, provided-by)
    CreatedAt, UpdatedAt time.Time
    ResourceVersion  int64
}
type OwnerRef struct{ Kind Kind; ID string }
type Relationship struct{ Type EdgeType; TargetKind Kind; TargetID string }
type ConditionStatus string // True | False | Unknown
type Condition struct{ Type string; Status ConditionStatus; Reason, Message string; LastTransitionTime time.Time }
type Status struct{ Conditions []Condition } // embedded by every kind-specific Status

// core/store
type Store[S, St any] interface {
    Get(ctx context.Context, id string) (Resource[S, St], error)
    List(ctx context.Context, opts ListOptions) ([]Resource[S, St], error)
    Create(ctx context.Context, r Resource[S, St]) (Resource[S, St], error)
    Update(ctx context.Context, r Resource[S, St]) (Resource[S, St], error) // resourceVersion checked
    Delete(ctx context.Context, id string, cascade bool) error
}
type ListOptions struct{ Kind string; OwnerID string; Labels map[string]string }
var ErrNotFound, ErrConflict error // sentinel errors

// core/graph
type EdgeType string // owns | runs-on | depends-on | provided-by
type Graph interface {
    Children(ctx context.Context, ref ResourceRef) ([]ResourceRef, error)
    Dependents(ctx context.Context, ref ResourceRef) ([]ResourceRef, error)
    BlastRadius(ctx context.Context, ref ResourceRef) ([]ResourceRef, error)
}

// core/event
type EventType string // created | updated | deleted
type Event struct{ Type EventType; Kind, ID string; Actor string; At time.Time }
type Bus interface {
    Subscribe(kind string, handler func(Event)) (unsubscribe func())
    Publish(Event)
}

// core/audit
type Record struct{ Actor, Interface, Kind, ID string; Before, After json.RawMessage; At time.Time; PolicyDecision *string }
type Recorder interface{ List(ctx context.Context, opts ListOptions) ([]Record, error) }
func WithActor(ctx context.Context, actor string) context.Context
func ActorFrom(ctx context.Context) string

// core/registry
type CapabilityRegistry struct{ /* wraps store.Store[CapabilitySpec, CapabilityStatus] */ }
func (r *CapabilityRegistry) Default(ctx context.Context, name string) (string, error)
func (r *CapabilityRegistry) SetDefault(ctx context.Context, name, provider string) error
type ProviderRegistry struct{ /* wraps store.Store[ProviderSpec, ProviderStatus] */ }
```

Per-kind `Spec`/`Status` structs (Organization, Team, Project, Environment,
Site, Cluster, Node, Application, Component, Capability, Provider,
ProviderInstance, Incident, Alert) — field lists per `resource-model.md`;
kept minimal (fields the spec/docs actually name), not speculative. Exact
fields are implementation detail decided during T004-T008, not pinned here.

## Files touched

```
go.mod, go.sum                              (new — module github.com/serendip-infra/serendip-infra)
docs/decisions/0007-uuidv7-resource-ids.md  (new — ADR for the google/uuid dependency)
core/id/id.go, id_test.go
core/resource/envelope.go, condition.go, validate.go, envelope_test.go
core/resource/orgtree.go        (Organization, Team, Project, Environment)
core/resource/infra.go          (Site, Cluster, Node)
core/resource/app.go            (Application, Component)
core/resource/capability.go     (Capability, Provider, ProviderInstance)
core/resource/ops.go            (Incident, Alert)
core/resource/*_test.go         (one per file above)
core/store/store.go             (interface, ListOptions, sentinel errors)
core/store/sqlite/migrate.go, migrations/0001_init.sql (embedded)
core/store/sqlite/store.go, store_test.go
core/store/sqlite/conn.go       (WAL/busy_timeout/MaxOpenConns setup)
core/audit/audit.go, context.go, audit_test.go
core/graph/graph.go, edges.go, graph_test.go
core/event/bus.go, bus_test.go
core/registry/capability.go, provider.go, registry_test.go
profiles/edge-1/bench/001-resource-model.md   (benchmark results, dev-machine-labeled)
docs/architecture/resource-model.md           (Open questions section resolved)
docs/PROGRESS.md                              (updated on completion)
```

## Risks

- **Shared-table JSON-blob design limits SQL-level querying** on spec/status
  fields (no `WHERE spec->>'foo'` style queries in 001). Acceptable now
  (List only filters kind/owner/labels); if a future spec needs
  field-level queries at scale, this may need JSON1 extension queries or a
  schema change. Flagged, not solved here.
- **`SetMaxOpenConns(1)` serializes all writes.** Correct and simple for a
  single-process control plane at this scale; reconciliation engine (spec
  003) concurrency design must account for this serialization rather than
  assume parallel writers.
- **Edge derivation from `OwnerRefs` + a generic `Relationships` field is a
  design bet** — implicit rather than an explicit graph-mutation API. If
  this proves hard to reason about once providers exist, spec 002/003 may
  need to revisit. Reversible: it's an internal mechanism, not an external
  contract.
- **10k-resource / <50MB RSS benchmark runs on the dev machine (RTX 4060 /
  Ryzen 7 / 32GB), not the T470 reference hardware** (`docs/PRD.md`
  Hardware section). Record the number, label it clearly as directional,
  do not report it as a verified constitution-rule-4 claim — that only
  happens once re-measured on real T470 hardware in Phase 3b.
- **Module path** chosen as `github.com/serendip-infra/serendip-infra`
  (lowercase, matching the `upstream` git remote org/repo, Go convention).
  Confirm before T001 — renaming later means touching every import.

## Explicitly NOT done in this spec

Per spec.md's Scope OUT, plus scope trims made during this plan:
- HTTP API (spec 002), reconciliation engine (spec 003), bootstrap agent
  (spec 004), any real provider implementation, UI/CLI/MCP, Kubernetes
  anything.
- RBAC enforcement — `Metadata`/`audit.Record` reserve the shape (actor,
  policy-decision field) but nothing enforces access control yet.
- Full Kubernetes-style label-selector grammar (set-based `in`/`notin`/
  `exists`) — exact-match label filtering only; revisit when a real
  consumer (topology planner) needs it.
- Provider health-check *execution* — `Provider`'s descriptor stores a
  health-check contract as data; actually calling it is the reconciliation
  engine's job (spec 003).
- Policy-gated mutations / approval flow — `audit.Record.PolicyDecision`
  is a reserved nil-able field, not wired to anything yet.
- Multi-process/multi-writer store scaling.
