# Spec 007: Storage provider (local-path)

Status: Draft — human review required before plan.md

## Problem

Spec 006 built the foundation (a generic Kubernetes REST client, and
`core/provider.Registry` resolving how capability-specific Go
interfaces get wired without `core/` importing `providers/`) but
deliberately built no capability on top of it — no `StorageProvider`
interface, no storage-related resource kind, no real provider. This
spec is the first real capability: Storage, backed by
rancher.io/local-path (already running by default in any stock K3s
install, confirmed via spec 005's own bootstrapped clusters — this is
K3s's bundled "local-storage" component, the same provisioner, not a
separate install step).

This is also the first spec that resolves a capability-specific
Provider Go interface shape — `provider-model.md`'s remaining open
question, deliberately left unanswered until a real capability forced
the decision (specs 003 and 006 both explicitly deferred it for this
reason).

## Scope — IN

- **A new resource kind.** `resource-model.md`'s "Cross-cutting
  infrastructure resources" section names this "Storage," but this spec
  uses the name **`Volume`** instead (`KindVolume`, `VolumeSpec`,
  `VolumeStatus`) to avoid colliding with "Storage" already being used
  as the *capability* name throughout `capability-model.md`/`storage.md`
  — a resource kind and the capability it belongs to having the same
  name would be confusing in API paths, logs, and error messages. This
  is a deliberate naming deviation from `resource-model.md`'s literal
  wording, flagged explicitly for approval, not silently decided.
  Minimal shape for this spec: `VolumeSpec{SizeBytes int64}` (no
  tier/policy/replication/backup/encryption — `storage.md`'s full
  `storagePolicy` concept is explicitly future work, see Scope-OUT),
  `VolumeStatus{Status}` with one `Condition{Type: "Bound"}`
  (True/False/Unknown + Reason/Message), mirroring `ClusterReconciler`'s
  single-Condition precedent (spec 003).
- **`StorageProvider` Go interface**, defined in `core/reconcile`
  alongside `VolumeReconciler` (mirrors `HealthChecker`'s placement next
  to `ClusterReconciler`) — capability-shaped, using only
  `core/resource` types, no Kubernetes-specific concepts leaking into
  `core/`:
  ```go
  type StorageProvider interface {
      Ensure(ctx context.Context, vol resource.Volume) (bound bool, reason, message string, err error)
      Delete(ctx context.Context, vol resource.Volume) error
  }
  ```
- **`providers/storage/localpath`**: the real implementation, wrapping
  spec 006's `providers/kubernetes.Client`. Creates (idempotently) its
  own `StorageClass` with `volumeBindingMode: Immediate` — not K3s's
  bundled default `local-path` StorageClass, which uses
  `WaitForFirstConsumer` (deferring PV creation until a consuming Pod is
  scheduled). Serendip has no Application Platform provider yet to ever
  create such a Pod, so relying on the default would mean every Volume
  sits `Pending` forever — a real, concrete constraint discovered during
  this spec's planning, not a hypothetical. `Ensure` creates/gets a
  `PersistentVolumeClaim` (deterministic name derived from the Volume's
  own ID, in a fixed `serendip-system` namespace created if missing) and
  reports its real `status.phase`; `Delete` removes it.
- **`VolumeReconciler`** in `core/reconcile`: looks up `"storage"` in
  `core/provider.Registry`, type-asserts to `StorageProvider`, calls
  `Ensure`/writes the `Bound` Condition — mirrors `ClusterReconciler`'s
  idempotency pattern (only `Update` when the computed Condition
  actually differs from what's stored).
- `cmd/serendipd` wiring: when `SERENDIP_KUBECONFIG` is set (spec 006),
  construct the real `localpath.Provider`, register it under `"storage"`
  in the registry, register `VolumeReconciler` with the reconcile
  engine. Replaces spec 006's throwaway `nodeLister` proof-of-concept
  entirely — that code is deleted, not left alongside this.

## Scope — OUT (explicitly)

- **`storage.md`'s full `storagePolicy` concept** (performance tier,
  replication count, backup schedule/retention, encryption) — this spec
  is the simplest possible real Volume: one size, one (Immediate-bound,
  local-path-backed) implementation. Policies/tiers are a real future
  need, not invented speculatively now (constitution rule 10).
- **Longhorn, or any multi-node/replicated storage** — ADR 0002's
  progression table scopes Longhorn to 3+ nodes; this spec's K3s
  cluster (spec 005) is single-node.
- **Backup storage** (`storage.md`'s explicitly separate capability,
  never conflated with primary) — not this spec.
- **Any `Application`/`Component` wiring** that would actually *use* a
  Volume (mounting it into a workload) — that needs the Application
  Platform provider (OpenChoreo, a later spec), which doesn't exist.
  This spec proves Volume provisioning in isolation, the same way spec
  003 proved Cluster health-checking in isolation before any real
  provider existed.
- **Any `api/` change beyond what's needed to create/read a `Volume`
  through the existing generic `ResourceHandler[S,St]` pattern** — spec
  002's `mountResource` already handles any new kind generically;
  confirm this holds rather than assume, but no new `api/` code is
  expected.
- **Capacity reporting, usable-after-replication math** — meaningless
  for an unreplicated local-path volume; relevant once Longhorn exists.

## Acceptance criteria

1. `go test ./core/reconcile/... ./providers/storage/...` green:
   `VolumeReconciler`'s logic (unit-level, mirroring
   `cluster_test.go`'s pattern) and `localpath.Provider`'s PVC
   create/get/delete request construction (`httptest.Server`-backed,
   mirroring spec 006's `client_test.go` pattern) are both
   table-driven-tested without a real cluster.
2. Against a real, freshly-bootstrapped K3s cluster (disposable WSL2
   clone, spec 005/006's established pattern): creating a `Volume`
   through the real HTTP API results in a real `PersistentVolumeClaim`
   with `status.phase: Bound` within a bounded time, confirmed both via
   `kubectl get pvc` directly and via the `Volume`'s own `Bound`
   Condition reaching `True` through `GET /v1/volumes/{id}`.
3. Idempotency, proven directly (mirrors spec 003's acceptance criterion
   3): reconciling the same unchanged, already-`Bound` `Volume` again
   produces no additional `Update` call/audit row/`ResourceVersion`
   bump.
4. Deleting a `Volume` through the API removes the real
   `PersistentVolumeClaim` (confirmed via `kubectl get pvc` returning
   not-found).
5. The `StorageProvider` interface and `VolumeReconciler` are proven
   reusable in principle: the interface itself contains no
   Kubernetes-specific types (only `core/resource.Volume` and plain Go
   types) — confirmed by inspection, not just by the one implementation
   this spec ships.
6. `go vet ./...`, `gofmt -l .`, `.claude/scripts/check-imports.sh`
   clean; `core/` (including `core/reconcile`) still imports nothing
   under `providers/`.

## Open questions (resolve during plan review)

- **PVC namespace**: `serendip-system`, created if missing, as stated
  above — confirm this is the right default rather than `default`, and
  whether the StorageClass/namespace creation happens at provider
  construction time or lazily on first `Ensure` call.
- **PVC naming scheme**: a deterministic name derived from
  `Volume.Metadata.ID` (e.g. `vol-<id>`) avoids needing to separately
  track the K8s object name in `VolumeStatus` — confirm this is
  sufficient, or whether `VolumeStatus` should carry the backing PVC
  name/namespace explicitly for operator visibility (`kubectl describe`
  convenience) even though it's derivable.
- **Resync interval for Volumes**: reuse `cmd/serendipd`'s existing
  30s/10s `resyncEvery`/`reconcileTimeout` constants (spec 003), or does
  storage reconciliation warrant its own tuning? Default to reusing
  unless a concrete reason not to surfaces in plan.md.
