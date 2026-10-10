# Tasks: Spec 009 — Secrets capability

Ordered; `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). Run `contract-guardian` before committing anything under
`core/` or `providers/` here — this spec touches both, plus `api/` for
the first time since spec 002.

## T001 — core/resource: Secret kind

- **Do**: `KindSecret`, `SecretSpec{Path string}`,
  `SecretStatus{Status; LastSyncedAt *time.Time}`, `type Secret =
  Resource[SecretSpec, SecretStatus]`.
- **Files**: `core/resource/secrets.go`, `core/resource/secrets_test.go`.
- **Verify**: `go test ./core/resource/...` — mirrors
  `storage_test.go`'s exact pattern: `KindSecret == "Secret"`, a valid
  envelope with `Path` set passes `Validate`, an empty `Name` fails
  `Validate` (same rule every kind follows), `Status.Conditions` round-
  trips. Additionally: confirm by inspection (a test asserting via
  `encoding/json` struct tags, or simply code review noted in the PR)
  that neither `SecretSpec` nor `SecretStatus` has any field capable of
  holding a secret's value — this is spec.md's security property,
  acceptance criterion 3's structural half.

## T002 — core/reconcile: SecretsProvider interface + SecretReconciler.Reconcile

- **Do**: `SecretsProvider` interface (`Exists`/`WriteValue`/
  `ReadValue`/`Delete`), `SecretReconciler` struct, `NewSecretReconciler`,
  `Reconcile` (existence-check via `Exists` → `"Reachable"` Condition,
  idempotent `setCondition` reusing `cluster.go`'s existing
  `findCondition`/`upsertCondition`). `Reconcile`'s `store.ErrNotFound`
  branch is a plain `return nil` — deliberately, per plan.md resolution
  4 (external cleanup already happened synchronously in `api/`'s
  `Delete`, by the time an async resync could ever observe the
  deletion).
- **Files**: `core/reconcile/secrets.go`, `core/reconcile/secrets_test.go`.
- **Verify**: `go test ./core/reconcile/...` — mirrors
  `volume_test.go`'s exact pattern against a `fakeSecretsProvider`: a
  value exists → `"Reachable"`/`True`/`"ValueExists"`; no value → 
  `"Reachable"`/`False`/`"NoValueWritten"`; `Exists` errors →
  `"Reachable"`/`Unknown` (or `False`, decide and document which — not
  `True`) with the error message in `Message`; a no-drift second
  `Reconcile` call makes zero additional `Update`/`ResourceVersion`
  bump (`TestSecretReconciler_NoDrift_IsIdempotent_NoAdditionalUpdate`,
  same shape as `TestVolumeReconciler_NoDrift...`); `Reconcile` against
  a nonexistent `Secret` ID returns `nil` and calls the provider zero
  times (confirming the deliberate divergence from `VolumeReconciler`
  is real, not accidental).

## T003 — core/reconcile: SecretReconciler synchronous value methods

- **Do**: `WriteValue(ctx, id, value) (resource.Secret, error)` (fetch
  → provider write → bump `Status.LastSyncedAt` + `"Reachable"`
  Condition to `True`/`"ValueExists"` → `store.Update`),
  `ReadValue(ctx, id) (map[string]any, error)` (fetch → provider read,
  no store write), `DeleteWithValue(ctx, id, cascade) error` (fetch →
  provider delete → `store.Delete`).
- **Files**: `core/reconcile/secrets.go` (extends T002),
  `core/reconcile/secrets_test.go` (extends T002).
- **Verify**: `go test ./core/reconcile/...` — against a real
  `sqlite.Store` (mirrors `volume_test.go`'s `newVolumeTestStore`
  helper, not a fake, since these methods' whole point is the
  fetch-then-write-back sequence) + a fake `SecretsProvider`:
  `WriteValue` calls the provider with the right `(mount-implicit)
  path`/value, persists `LastSyncedAt`, and the resulting `Update`'s
  audit row (query `audit_log` directly in-test) does **not** contain
  the plaintext value anywhere in its `Before`/`After` JSON — this is
  acceptance criterion 3's empirical half, proven at the unit level
  before the real-cluster pass proves it end-to-end; `ReadValue`
  returns the provider's value and makes zero `store.Update` calls;
  `DeleteWithValue` calls the provider's `Delete` before
  `store.Delete`, and propagates a provider-delete error without ever
  calling `store.Delete` (the record must survive if the external
  cleanup failed, so a retry has something to retry).

## T004 [P] — providers/secrets/openbao: Provider adapter

- **Do**: `Provider` struct wrapping `*Client`, `NewProvider`, and the
  four `SecretsProvider` methods — `Exists` calls `ReadSecret` and maps
  `ErrSecretNotFound` to `(false, nil)`; `ReadValue`/`WriteValue`/
  `Delete` call `ReadSecret`/`WriteSecret`/`DeleteSecret` directly,
  always with `DefaultKVMount`.
- **Files**: `providers/secrets/openbao/provider.go`,
  `providers/secrets/openbao/provider_test.go`.
- **Verify**: `go test ./providers/secrets/openbao/...` —
  `httptest.Server`-backed (mirrors `client_test.go`'s existing
  pattern, not a fake): `Exists` against a fixture 404 returns
  `(false, nil)`, not an error; `Exists` against a fixture 200 returns
  `(true, nil)`; `WriteValue`/`ReadValue`/`Delete` each hit the exact
  `/v1/secret/data/<path>` or `/v1/secret/metadata/<path>` route
  `client_test.go` already confirms, now driven through a
  `resource.Secret{Spec: resource.SecretSpec{Path: "..."}}` instead of
  raw strings — confirms the adapter's translation layer, not the wire
  protocol again.

## T005 [P] — api: SecretHandler

- **Do**: `SecretHandler` embedding
  `*ResourceHandler[resource.SecretSpec, resource.SecretStatus]`,
  `NewSecretHandler(store, reconciler)`, `Register` (5 CRUD routes +
  `POST`/`GET /v1/secrets/{id}/value`), an overridden `Delete` calling
  `reconciler.DeleteWithValue` instead of the embedded handler's
  store-only delete, `WriteValue`/`ReadValue` HTTP methods decoding/
  encoding a plain `{"data": {...}}`-shaped body (mirrors
  `providers/secrets/openbao`'s own KV v2 wire shape for consistency,
  even though this is a different layer) around
  `reconciler.WriteValue`/`ReadValue`.
- **Files**: `api/secret_handler.go`, `api/secret_handler_test.go`.
- **Verify**: `go test ./api/...` — against a real `sqlite.Store` +
  `core.NewSecretReconciler` + a fake `SecretsProvider` (same
  `httptest`-free style as T003, since this layer's job is routing/
  encoding, not wire protocol): `POST /v1/secrets` creates a `Secret`
  with just `Path` set, no value anywhere in the response body's
  `status`/`spec`; `POST .../value` then `GET .../value` round-trips
  byte-for-byte; `DELETE /v1/secrets/{id}` calls the fake provider's
  `Delete` (confirmed via a call-count field on the fake, same pattern
  `fakeStorageProvider` already established) before the record is
  gone from a subsequent `GET` (404); every response still goes through
  the existing RFC 7807 problem+json path on error (reuse, not
  reinvent, `writeStoreError`/`writeError`).

## T006 — cmd/serendipd + api/server.go wiring

- **Do**: `wireOpenBao`: change `registry.Register("secrets",
  baoClient)` to `registry.Register("secrets",
  openbao.NewProvider(baoClient))`. `wireKubernetes`: construct the
  `Secret` store, look up `"secrets"` from the registry, type-assert to
  `reconcile.SecretsProvider`, construct `NewSecretReconciler`, register
  it with the engine (mirrors the existing `VolumeReconciler` wiring
  block). `api/server.go`'s `NewServer`: new parameter for the
  constructed `*reconcile.SecretReconciler`, wire `SecretHandler`
  instead of a `mountResource[SecretSpec,...]` call.
- **Files**: `cmd/serendipd/main.go`, `api/server.go`.
- **Verify**: `go build ./...`; `serendipd` with `SERENDIP_KUBECONFIG`
  unset still starts successfully (the whole `Secret` wiring sequence
  is skipped, same gating as every other `wireKubernetes`-dependent
  piece — confirms this spec didn't make Kubernetes-facing setup
  load-bearing for a no-cluster startup, same check spec 008's T006
  already established).

## T007 — Real-cluster verification (kind): acceptance criteria 2-4

- **Do**: Against a real cluster (`kind`, this session's established
  preference over hand-rolled WSL2 clones — see `docs/PROGRESS.md`),
  with spec 008's OpenBao already deployed/bootstrapped and this
  spec's code running: `POST /v1/secrets` with `{"path":
  "t009-test/value"}`, confirm the response and a subsequent `GET`
  show no value anywhere; `POST /v1/secrets/{id}/value` with a test
  payload, `GET /v1/secrets/{id}/value`, confirm byte-for-byte equality;
  independently confirm (directly against OpenBao's own API, bypassing
  Serendip entirely, same discipline as spec 008's own T007) that the
  value actually lives at `secret/data/t009-test/value`; inspect the
  `audit_log` row the write produced directly (e.g. a throwaway query
  against the SQLite file) and confirm the plaintext value is not
  present in `Before`/`After`; `DELETE /v1/secrets/{id}`, confirm a
  subsequent direct OpenBao query shows the value gone too. Tear down
  the `kind` cluster completely afterward.
- **Files**: none (verification task; a scratch shell script is fine).
- **Verify**: all of the above observed directly against the real
  cluster, not assumed from unit tests alone — matches spec 008's T007
  discipline, now itself validated twice in a row as the right
  approach for this project on this host.

## T008 — Final acceptance pass + contract-guardian + go-reviewer

- **Do**: `go test ./core/resource/... ./core/reconcile/... ./providers/secrets/openbao/... ./api/... -v`
  full run, re-check all 6 acceptance criteria against actual test
  output + T007's real-cluster results, `go vet ./...`, `gofmt -l .`,
  `.claude/scripts/check-imports.sh`. One holistic `contract-guardian`
  review (constitution compliance, import direction, the
  `SecretHandler`-depends-on-`core/reconcile` pattern deviation
  flagged in plan.md's Risks — confirm it's documented, not silently
  generalized) and one `go-reviewer` pass (idiomatic error handling,
  especially around the non-atomic `DeleteWithValue` ordering plan.md's
  Risks already names), explicitly re-reading spec.md's Scope-IN line
  by line against the actual code (specs 002-008's established
  lesson).
- **Files**: none (review/gate task).
- **Verify**: all green, both reviews PASS, all 6 acceptance criteria
  individually confirmed.

## T009 [A] — Docs sync

- **Do**: Update `docs/architecture/secrets.md` — add the real
  capability (Secret kind, SecretsProvider, the value-read/write API
  shape, the audit-piggyback mechanism and why reads aren't audited
  yet) alongside spec 008's existing foundation content; don't
  duplicate, extend. Update `docs/PROGRESS.md` (spec 009 complete).
  Update `docs/architecture/provider-model.md` only if this spec's
  `SecretsProvider` shape or the `api/`-depends-on-`core/reconcile`
  pattern surfaces something worth recording as a breadcrumb for
  future capability interfaces — check before assuming a change is
  needed, same discipline spec 008's T009 already applied.
- **Files**: `docs/architecture/secrets.md`, `docs/PROGRESS.md`,
  possibly `docs/architecture/provider-model.md`.
- **Verify**: no stale language remains describing spec 009 as
  not-started; `docs/architecture/secrets.md` accurately describes only
  what actually shipped, not any aspirational shape (rotation, read
  auditing, policy) this spec deliberately left out.
