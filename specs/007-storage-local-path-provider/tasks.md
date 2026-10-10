# Tasks: Spec 007 — Storage provider (local-path)

Ordered; `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). Run `contract-guardian` before committing anything under
`core/` (CLAUDE.md workflow rule) — also applies to `providers/storage/
localpath` here given it performs real cluster-mutating writes, the same
elevated scrutiny spec 006 applied to `providers/kubernetes`.

## T001 — Volume resource kind

- **Do**: `KindVolume`, `VolumeSpec{SizeBytes int64}`,
  `VolumeStatus{Status}`, `Volume` type alias — `core/resource/storage.go`,
  following every existing kind's exact pattern (json tags, doc comments
  explaining the Volume-not-Storage naming deviation from
  `resource-model.md`'s literal wording).
- **Files**: `core/resource/storage.go`, `core/resource/storage_test.go`.
- **Verify**: `go test ./core/resource/...` — a table-driven test
  confirms `Validate(Volume{}.Metadata)` behaves like every other kind
  (Name required, etc. — no Volume-specific validation needed yet).

## T002 — StorageProvider interface

- **Do**: `StorageProvider` interface in `core/reconcile/storage.go` —
  capability-shaped, `core/resource.Volume`-only signature, no
  Kubernetes-specific types.
- **Files**: `core/reconcile/storage.go`.
- **Verify**: `go build ./...` succeeds. (No real test content yet —
  an interface has nothing to unit-test on its own; T003's
  `VolumeReconciler` tests exercise it via a fake implementation.)

## T003 — VolumeReconciler

- **Do**: `VolumeReconciler`, `NewVolumeReconciler`, `Reconcile`,
  `setCondition` — mirrors `ClusterReconciler`'s exact structure
  (idempotency check before `Update`, `store.ErrNotFound` on Get treated
  as "deleted since the event fired, not an error").
- **Files**: `core/reconcile/volume.go`, `core/reconcile/volume_test.go`.
- **Verify**: `go test ./core/reconcile/...` — table-driven against a
  fake `StorageProvider` (mirrors `cluster_test.go`'s pattern, no real
  cluster): `Ensure` returns bound=true → Condition True; bound=false →
  Condition False with the provider's reason; `Ensure` returns an error
  → Condition False, Reason "EnsureFailed". Plus the idempotency test
  (repeated `Reconcile` with an unchanged fake-provider result makes no
  additional `Update` call — real `sqlite.Store`, not a mock, same
  discipline as `cluster_test.go`).

## T004 — providers/storage/localpath: setup (namespace + StorageClass)

- **Do**: `NewProvider(ctx, client)` — idempotent GET-then-Create for
  the `serendip-system` namespace and the `serendip-local-path`
  StorageClass (`volumeBindingMode: Immediate`, provisioner
  `rancher.io/local-path`, no default-class annotation).
- **Files**: `providers/storage/localpath/provider.go`,
  `providers/storage/localpath/provider_test.go`, `doc.go`.
- **Verify**: `go test ./providers/storage/localpath/...` —
  `httptest.Server`-backed (mirrors spec 006's `client_test.go`
  pattern): namespace/StorageClass both absent → both created; either
  already present (404-then-...no, GET returns 200) → no duplicate
  Create attempted; a genuine unexpected error from either GET or
  Create propagates clearly.

## T005 — providers/storage/localpath: Ensure + Delete

- **Do**: `Ensure` (deterministic PVC name from `vol.Metadata.ID`,
  GET-then-Create-if-missing, report `bound`/`reason` from the real
  `status.phase`), `Delete` (remove the PVC, treat not-found as success
  not an error).
- **Files**: `providers/storage/localpath/provider.go` (extends T004).
- **Verify**: `go test ./providers/storage/localpath/...` —
  `httptest.Server`-backed: PVC absent → created, `bound=false,
  reason="Pending"` (a freshly-created PVC isn't instantly bound in
  real Kubernetes, matching the test server returning phase=Pending on
  the create response); PVC present with phase=Bound → `bound=true,
  reason="Bound"`; `Delete` on an existing PVC issues the DELETE call;
  `Delete` on a 404 returns nil, not an error.

## T006 — cmd/serendipd wiring (replaces spec 006's proof-of-concept)

- **Do**: Extend `wireKubernetes`: construct `localpath.NewProvider`,
  register it under `"storage"` in the `core/provider.Registry`,
  `Lookup` + type-assert once, construct `reconcile.NewVolumeReconciler`
  and register it with the engine (volumes `store.Store` + a lister
  mirroring `ClusterReconciler`'s own `clusters.List`-based one).
  **Delete** spec 006's `nodeLister`/`kubeNodeLister` types and their
  registration entirely — this spec's real capability replaces that
  proof-of-concept, doesn't sit alongside it.
- **Files**: `cmd/serendipd/main.go`.
- **Verify**: `go build ./...`; grep confirms `nodeLister`/
  `kubeNodeLister` no longer exist anywhere in the file.

## T007 — api/server.go: mount the Volume kind

- **Do**: One `mountResource[resource.VolumeSpec,
  resource.VolumeStatus](apiMux, db, bus, resource.KindVolume,
  "volumes")` line, following the existing 14-kind pattern exactly.
- **Files**: `api/server.go`.
- **Verify**: `go test ./api/...` — extend `server_test.go`'s
  `allKindPaths` to include `"volumes"`, confirming the existing
  `TestNewServer_AllKindsRespondToPostAndGet` smoke test covers it with
  zero new test code otherwise (proves spec.md's "the generic pattern
  needs nothing else" claim empirically, not just by assertion).

## T008 — Real-cluster verification (disposable WSL2 clone): acceptance
criteria 2, 3, 4

- **Do**: Same `wsl --export`/`--import` disposable-clone approach as
  specs 005/006 (never the persistent dev distro) — bootstrap a fresh
  K3s cluster (spec 005's proven mechanism), run `serendipd` with
  `SERENDIP_KUBECONFIG` set, create a `Volume` through the real HTTP
  API, confirm `kubectl get pvc -n serendip-system` shows it `Bound`
  and `GET /v1/volumes/{id}` shows the `Bound` Condition `True`, both
  within a bounded time. Reconcile again (e.g. trigger a resync wait or
  a no-op `PUT`) and confirm no spurious additional `Update`/audit row
  (criterion 3, through the real `Engine`, not just T003's unit-level
  proof). Delete the `Volume` through the API, confirm the PVC is
  really gone via `kubectl`. Tear down the disposable clone completely
  afterward, confirm zero trace in the persistent dev distro.
- **Files**: none (verification task; a scratch shell script is fine).
- **Verify**: all of the above observed directly against the real
  cluster, not assumed from unit tests alone.

## T009 — Final acceptance pass + contract-guardian

- **Do**: `go test ./core/resource/... ./core/reconcile/... ./providers/storage/... ./api/... -v`
  full run, re-check all 6 acceptance criteria against actual test
  output + T008's real-cluster results, `go vet ./...`, `gofmt -l .`,
  `.claude/scripts/check-imports.sh`. One holistic `contract-guardian`
  review across `core/resource/storage.go`, `core/reconcile/{storage,
  volume}.go`, all of `providers/storage/localpath`, and the
  `cmd/serendipd`/`api/server.go` diffs, explicitly re-reading spec.md's
  Scope-IN line by line against the actual code (specs 002-006's
  established lesson) — pay particular attention to: (a) whether
  `core/reconcile/storage.go`'s `StorageProvider` interface genuinely
  contains zero Kubernetes-specific types (acceptance criterion 5), (b)
  whether `Ensure`'s idempotent GET-then-Create for the namespace/
  StorageClass/PVC could ever create a duplicate under any real race
  condition worth worrying about at this scale, (c) whether spec 006's
  `nodeLister` proof-of-concept was actually fully deleted, not left as
  dead code alongside the real capability.
- **Files**: none (review/gate task).
- **Verify**: all green, contract-guardian PASS, all 6 acceptance
  criteria individually confirmed.

## T010 [A] — Docs sync

- **Do**: Update `docs/PROGRESS.md` (spec 007 complete; this is the
  first real end-to-end capability, worth noting plainly against
  `docs/PRD.md`'s POC scope item 3's "at least one real provider live"
  bar). Update `docs/architecture/provider-model.md` — this spec
  resolves the first concrete capability-specific interface shape
  (`StorageProvider`), record what that actually looked like as a
  breadcrumb for Identity/Secrets/Application-Platform/Gateway's own
  future interface designs. Update `docs/architecture/resource-model.md`
  to record the Volume-not-Storage naming decision where the doc
  currently says "Storage — a primary or backup storage request."
- **Files**: `docs/PROGRESS.md`, `docs/architecture/provider-model.md`,
  `docs/architecture/resource-model.md`.
- **Verify**: no stale language remains describing spec 007 as
  not-started.
