# Plan: Spec 009 — Secrets capability

Status: Draft — human review required before implementation.

## Open-question resolutions

1. **`SecretSpec` holds one field: `Path string`** — a user-chosen,
   human-meaningful path (e.g. `"myapp/db-password"`), not derived from
   `Metadata.ID`. Deliberately unlike `Volume`'s ID-derived PVC name
   (spec 007): nobody wants to browse secrets by opaque UUID path, and
   real secrets managers universally use human-chosen paths. `core/
   resource` never says "mount" (OpenBao/Vault-specific jargon,
   constitution rule 1) — the KV v2 mount is a single deployment-wide
   constant (`openbao.DefaultKVMount`, spec 008), not a per-Secret
   choice, so it has no business appearing in `core/` at all.
2. **`SecretsProvider` interface — four methods**, defined in
   `core/reconcile` alongside `SecretReconciler` (mirrors
   `StorageProvider`/`VolumeReconciler`'s placement exactly):
   ```go
   type SecretsProvider interface {
       Exists(ctx context.Context, sec resource.Secret) (bool, error)
       WriteValue(ctx context.Context, sec resource.Secret, value map[string]any) error
       ReadValue(ctx context.Context, sec resource.Secret) (map[string]any, error)
       Delete(ctx context.Context, sec resource.Secret) error
   }
   ```
   `Exists` (not `Ensure`) deliberately — `VolumeProvider`'s `Ensure`
   *provisions* a PV/PVC that didn't exist; there is nothing
   analogous to provision here (OpenBao's KV v2 mount already exists,
   set up once by spec 008's `Bootstrap`). All the reconcile loop can
   honestly do is observe whether a value currently exists at this
   Secret's path and report that — constitution rule 6, "honest
   capability reporting," not silently implying a repair action that
   doesn't exist. `ReadValue`/`WriteValue`/`Delete` are called
   synchronously by API-layer orchestration (resolution 4 below), not
   from `Reconcile` — a real, deliberate difference from
   `StorageProvider`, explained there.
3. **Implemented by `providers/secrets/openbao.Provider`** (mirrors
   `providers/storage/localpath.Provider`'s naming exactly) — a thin
   adapter wrapping spec 008's existing `*openbao.Client`, translating
   `sec.Spec.Path` + the constant `DefaultKVMount` into
   `ReadSecret`/`WriteSecret`/`DeleteSecret`/`ReadSecret`-for-existence
   calls. Zero new wire-protocol code. `cmd/serendipd`'s `wireOpenBao`
   changes its existing `registry.Register("secrets", baoClient)` call
   to `registry.Register("secrets", openbao.NewProvider(baoClient))` —
   registering the capability-shaped adapter directly, matching
   `wireKubernetes`'s existing pattern of registering
   `localpath.NewProvider(...)` rather than a lower-level client
   needing further unwrapping elsewhere. Spec 008's "reachable for a
   direct smoke-test call only" framing for the bare `*Client`
   registration is superseded by this real capability, not left
   alongside it (same "replace, don't leave a parallel path" precedent
   spec 007 set for spec 006's `nodeLister`).
4. **Value read/write/delete orchestration lives in `core/reconcile`,
   as synchronous methods on `SecretReconciler` beyond its `Reconcile`
   entry point — not inline in `api/` handlers** (`api/CLAUDE.md`: "no
   business logic" in handlers) **and not inside the async reconcile
   loop** (reasons below). Three new exported methods:
   ```go
   func (r *SecretReconciler) WriteValue(ctx context.Context, id string, value map[string]any) (resource.Secret, error)
   func (r *SecretReconciler) ReadValue(ctx context.Context, id string) (map[string]any, error)
   func (r *SecretReconciler) DeleteWithValue(ctx context.Context, id string, cascade bool) error
   ```
   Each fetches the current `Secret` from the store first (to read
   `Path` and, for writes/deletes, the current `ResourceVersion`),
   calls the provider, then — for `WriteValue`/`DeleteWithValue`
   only — writes back through the store (resolution 5). The new
   `api/SecretHandler` (resolution 6) calls these directly; they are
   plain synchronous functions, not reconcile-loop concerns, so living
   on the same type as `Reconcile` is a convenience (one less type),
   not a conflation — clearly doc-commented as "synchronous value
   operations, called directly by `api/`, not invoked by the reconcile
   engine."
   **Why `DeleteWithValue` exists outside `Reconcile` at all — a real
   divergence from `VolumeReconciler`'s pattern, not an oversight**:
   `VolumeReconciler.Reconcile`'s `store.ErrNotFound` branch can call
   `provider.Delete` with nothing but `ref.ID` because the PVC name is
   *derived* from the ID alone (resolution 1 above re-explains why
   `Secret`'s `Path` can't be). Once a `Secret` record is gone from the
   store, `Path` is gone with it — `Reconcile`'s `ErrNotFound` branch
   would have no way to know what to delete from OpenBao. So the
   external cleanup has to happen *before* the store record is deleted,
   not after — exactly the shape `api/SecretHandler.Delete` provides by
   calling `DeleteWithValue` (fetch Path → provider.Delete → store.Delete)
   instead of the generic `ResourceHandler[S,St].Delete`.
   `SecretReconciler.Reconcile`'s own `ErrNotFound` branch is
   consequently a plain no-op (`return nil`) — correct here, unlike
   `VolumeReconciler`, because there is genuinely nothing left to clean
   up by the time an async resync could ever observe the deletion.
5. **Value writes are audited by piggybacking a `Status` update
   through the existing, unmodified `core/audit` mechanism — no
   `core/audit` changes at all.** `WriteValue` (resolution 4), after a
   successful provider write, sets `Status.LastSyncedAt` (a new
   `*time.Time` field, `SecretStatus`) and refreshes the `"Reachable"`
   Condition to `True`/`"ValueExists"`, then calls `store.Update`. That
   `Update` call writes exactly one audit row through the exact
   mechanism every other mutation already uses — `Before`/`After` are
   whole-`Secret` JSON snapshots, safe because the value never lives in
   `Spec` or `Status` (spec.md's structural constraint, confirmed
   directly against `core/store/sqlite/store.go`). This fully resolves
   spec.md's open question "does `core/audit.Record` need a real
   extension" — **no**.
   **Value reads are explicitly NOT audited in this spec** — the
   obvious way to audit a read (piggyback a `Status` bump the same way)
   would mean every `GET .../value` call bumps `ResourceVersion`,
   which breaks ordinary optimistic-concurrency expectations for any
   caller doing read-then-`PUT` on the same `Secret` (a spurious
   `ErrConflict` on a `Secret`'s own metadata PUT, caused by an
   unrelated value read in between — surprising and wrong). A
   correct read-audit trail needs a genuinely separate, non-
   `ResourceVersion`-affecting append-only write path in `core/audit`
   that nothing in this codebase has today and no concrete consumer
   (e.g. a compliance report) is asking for yet — building it
   speculatively violates constitution rule 10. Recorded as explicit
   future work (Explicitly NOT done, below), not silently dropped.
6. **New `api/SecretHandler`, not the generic `ResourceHandler[S,St]`
   used unchanged.** `Create`/`Get`/`List`/`Update` delegate to an
   embedded `*ResourceHandler[resource.SecretSpec, resource.SecretStatus]`
   (reuse, not reimplementation — those four really are plain
   Spec/Status CRUD, nothing provider-specific). `Delete` is
   overridden to call `SecretReconciler.DeleteWithValue` instead of the
   embedded handler's store-only delete (resolution 4). Two new routes,
   registered alongside the other five:
   ```
   POST /v1/secrets/{id}/value   → SecretReconciler.WriteValue
   GET  /v1/secrets/{id}/value   → SecretReconciler.ReadValue
   ```
   `api/secret_handler.go` is a new, small, Secret-specific file — not
   a change to the generic `ResourceHandler[S,St]` (which stays exactly
   as every other 14 kinds use it).
7. **Resync interval: reuse `cmd/serendipd`'s existing `resyncEvery`/
   `reconcileTimeout` constants (30s/10s)**, same as `Volume`/`Cluster`
   — no concrete reason surfaced during planning to tune separately.

## Approach

1. `core/resource/secrets.go` (new): `KindSecret`, `SecretSpec{Path
   string}`, `SecretStatus{Status; LastSyncedAt *time.Time}`, `type
   Secret = Resource[SecretSpec, SecretStatus]` — mirrors
   `core/resource/storage.go`'s shape exactly.
2. `core/reconcile/secrets.go` (new): the `SecretsProvider` interface
   (resolution 2), `SecretReconciler` struct + `NewSecretReconciler`,
   `Reconcile` (existence-check → `"Reachable"` Condition, idempotent
   `setCondition` reusing `cluster.go`'s existing `findCondition`/
   `upsertCondition` helpers — no new helpers needed), and the three
   synchronous methods from resolution 4.
3. `providers/secrets/openbao/provider.go` (new): `Provider` struct
   wrapping `*Client`, `NewProvider`, and the four `SecretsProvider`
   methods (resolution 3) — `Exists` calls `ReadSecret` and maps
   `ErrSecretNotFound` to `(false, nil)`, any other error passes
   through; `ReadValue`/`WriteValue`/`Delete` call `ReadSecret`/
   `WriteSecret`/`DeleteSecret` directly, always with
   `DefaultKVMount`.
4. `api/secret_handler.go` (new): `SecretHandler` (resolution 6),
   embedding `*ResourceHandler[resource.SecretSpec,
   resource.SecretStatus]` for 4 of 5 CRUD routes, overriding `Delete`,
   adding the two value routes. Needs a reference to the constructed
   `*reconcile.SecretReconciler` (for `WriteValue`/`ReadValue`/
   `DeleteWithValue`) alongside the `store.Store[...]` the embedded
   handler already needs — both get threaded through from
   `cmd/serendipd`'s composition root, same place every other wiring
   decision already happens.
5. `cmd/serendipd/main.go`: in `wireOpenBao` (spec 008), change the
   registered value per resolution 3; in `wireKubernetes`, construct
   `secretStore := sqlite.NewStore[resource.SecretSpec,
   resource.SecretStatus](db, resource.KindSecret).SetBus(bus)`,
   look up `"secrets"` from the registry and type-assert to
   `reconcile.SecretsProvider`, construct `reconcile.
   NewSecretReconciler(secretStore, secretsProvider)`, register it
   with the engine (`resource.KindSecret`, same `List` closure pattern
   `VolumeReconciler`'s wiring already uses) — all mirroring the
   existing `VolumeReconciler` wiring block line-for-line where the
   shapes match. `api/server.go`'s `NewServer` gains the new
   `SecretHandler` wiring instead of (not in addition to) a
   `mountResource[resource.SecretSpec, resource.SecretStatus]` call —
   `NewServer`'s signature needs the constructed `*SecretReconciler`
   passed in, since it's built in `cmd/serendipd`, not inside `api/`
   (an API-layer first: every other handler only ever needed a
   `store.Store`, never a `core/reconcile` type — document this as a
   one-off, not a new general pattern, in the handler's own doc
   comment).

## Interfaces / types to create

```go
// core/resource/secrets.go
const KindSecret Kind = "Secret"

type SecretSpec struct {
    Path string `json:"path"`
}

type SecretStatus struct {
    Status
    LastSyncedAt *time.Time `json:"lastSyncedAt,omitempty"`
}

type Secret = Resource[SecretSpec, SecretStatus]
```

```go
// core/reconcile/secrets.go
type SecretsProvider interface {
    Exists(ctx context.Context, sec resource.Secret) (bool, error)
    WriteValue(ctx context.Context, sec resource.Secret, value map[string]any) error
    ReadValue(ctx context.Context, sec resource.Secret) (map[string]any, error)
    Delete(ctx context.Context, sec resource.Secret) error
}

type SecretReconciler struct { /* store, provider */ }
func NewSecretReconciler(secrets store.Store[resource.SecretSpec, resource.SecretStatus], provider SecretsProvider) *SecretReconciler
func (r *SecretReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error
func (r *SecretReconciler) WriteValue(ctx context.Context, id string, value map[string]any) (resource.Secret, error)
func (r *SecretReconciler) ReadValue(ctx context.Context, id string) (map[string]any, error)
func (r *SecretReconciler) DeleteWithValue(ctx context.Context, id string, cascade bool) error
```

```go
// providers/secrets/openbao/provider.go
type Provider struct { /* client *Client */ }
func NewProvider(client *Client) *Provider
// satisfies core/reconcile.SecretsProvider
```

```go
// api/secret_handler.go
type SecretHandler struct { /* embeds *ResourceHandler[...], holds *reconcile.SecretReconciler */ }
func NewSecretHandler(store store.Store[resource.SecretSpec, resource.SecretStatus], reconciler *reconcile.SecretReconciler) *SecretHandler
func (h *SecretHandler) Register(mux *http.ServeMux)
```

## Files touched

- `core/resource/secrets.go` (new), `core/resource/secrets_test.go` (new)
- `core/reconcile/secrets.go` (new), `core/reconcile/secrets_test.go` (new)
- `providers/secrets/openbao/provider.go` (new),
  `providers/secrets/openbao/provider_test.go` (new)
- `api/secret_handler.go` (new), `api/secret_handler_test.go` (new)
- `cmd/serendipd/main.go` (edit: `wireOpenBao`'s registered value,
  `wireKubernetes`'s new `SecretReconciler` wiring, `NewServer`'s new
  parameter)
- `api/server.go` (edit: thread the new `*SecretReconciler` through,
  swap `mountResource[SecretSpec,...]` for `SecretHandler`)
- `docs/architecture/secrets.md` (edit: document the real capability
  now that it exists, same split as spec 008's own doc describing only
  what shipped)
- `docs/architecture/provider-model.md` (edit, only if this spec's
  `SecretsProvider` shape surfaces a breadcrumb worth recording the way
  spec 007's `StorageProvider`/`Delete`-must-be-called lesson was —
  confirm before assuming a change is needed)

## Risks

- **`api/SecretHandler` needing a `core/reconcile` type is a new
  pattern** — every prior handler only ever touched `core/store`
  directly (`api/CLAUDE.md`'s documented status quo: "Calling
  `core/store.Store[S,St]` directly from a handler *is* the core call
  ... there's no separate domain-logic layer between them yet"). This
  spec's `SecretHandler` is the first to depend on a `core/reconcile`
  type instead. Mitigated by keeping the dependency narrow (three
  specific methods, not the whole reconcile `Engine`) and documenting
  it explicitly as a one-off rather than silently generalizing
  "handlers can depend on reconcilers" as a new rule.
- **The `DeleteWithValue`-before-`store.Delete` ordering is not
  atomic** — if the provider delete succeeds but the subsequent
  `store.Delete` fails (e.g. a `graph.ErrHasChildren` from an
  un-cascaded delete with children, unlikely for a leaf kind like
  `Secret` but not structurally impossible), OpenBao's value is gone
  while the Serendip-side record remains, now pointing at nothing.
  Accepted as a real, narrow gap for this spec rather than engineering
  a two-phase-commit across two completely different systems
  (constitution rule 10 scope discipline) — `Secret` has no children
  today (nothing `owns` a `Secret` yet), so `ErrHasChildren` can't
  actually occur in practice; worth revisiting if that ever changes.
- **Resource-version races between `WriteValue`'s read-then-write
  pair** — a concurrent edit to the same `Secret`'s metadata between
  `WriteValue`'s internal `Get` and its internal `Update` surfaces as
  `ErrConflict`, same as any other optimistic-concurrency race
  elsewhere in this codebase. Not a new risk class, just a new call
  site for an existing, already-accepted one.
- **Real-cluster behavior may again invalidate part of this plan**
  (`provider-model.md`'s own standing breadcrumb, true again for specs
  007 and 008 in a row) — budget real verification time against a
  `kind` cluster (this session's established preference over WSL2),
  not just unit tests, before calling T-whichever-task "done."

## Explicitly NOT done in this spec

- **Auditing value reads** — resolution 5 explains why in detail;
  needs a genuine `core/audit` extension (a non-`ResourceVersion`-
  affecting append-only write path) this spec deliberately doesn't
  build speculatively.
- **A policy engine gating secret access** — `core/policy` doesn't
  exist; `ActorMiddleware`'s existing (intentionally weak) actor
  resolution is the only access control, same as every other kind
  today.
- **Rotation, versioning, leasing, expiry, PKI, dynamic database
  credentials, Kubernetes auth method / workload identity** — all
  already out per spec.md's Scope-OUT, restated here for completeness.
- **Any `Application`/`Component` wiring to inject a Secret's value
  into a running workload** — needs the Application Platform provider
  (OpenChoreo), which doesn't exist yet.
- **UI/CLI/MCP** — API only, same precedent as every prior spec.
