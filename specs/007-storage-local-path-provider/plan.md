# Plan: Spec 007 — Storage provider (local-path)

Status: Implemented. **Correction, 2026-10-10**: the Approach/
Interfaces sections below describe the plan as originally approved
(a `StorageClass` with `volumeBindingMode: Immediate` driving
`rancher.io/local-path-provisioner`'s dynamic provisioning). Real-
cluster testing (T008) found this genuinely doesn't work — confirmed
against the provisioner's own source: in its default per-node hostPath
mode it hard-requires a target node, supplied only via the
`volume.kubernetes.io/selected-node` annotation the Kubernetes
scheduler sets as part of `WaitForFirstConsumer`'s delayed-binding
flow, which `Immediate` mode never populates. What actually shipped,
confirmed with the user before implementing since it changes this
spec's core mechanism: **static `PersistentVolume`/
`PersistentVolumeClaim` binding** — `providers/storage/localpath`
creates a hostPath-backed PV directly and a PVC that references it by
name via `spec.volumeName`, bypassing the provisioner's dynamic-
provisioning controller (and any `StorageClass`) entirely. See
`providers/storage/localpath/provider.go`'s doc comment for the full
reasoning and `docs/architecture/provider-model.md`'s breadcrumb for
what this means for future capability-provider designs. The
`StorageProvider` interface shape, `VolumeReconciler`, resource kind,
and every other section below are unaffected — only the provisioning
*mechanism* changed, not the capability-level contract.

A second, smaller correction: `VolumeReconciler.Reconcile`'s original
design (below) didn't call `provider.Delete` when a `Volume` was
deleted via the API — `core/reconcile/volume.go`'s `store.ErrNotFound`
branch now does, since (unlike `ClusterReconciler`) this is the first
Reconciler whose provider actually provisions external state that
needs explicit cleanup.

## Open-question resolutions

1. **PVC namespace: `serendip-system`, created eagerly at provider
   construction, not lazily.** A dedicated namespace (not `default`)
   keeps Serendip-managed objects visibly separate from anything else a
   user might put in the cluster — matches the general principle of not
   silently commingling control-plane-managed resources with
   user-managed ones. Created eagerly (in `localpath.NewProvider`,
   alongside the `StorageClass`) so setup failures surface at
   `cmd/serendipd` startup, not on the first `Volume` creation — same
   "fail fast at startup" reasoning as spec 006's `wireKubernetes`
   already uses for kubeconfig loading.
2. **PVC naming: deterministic, derived from `Volume.Metadata.ID`, not
   separately tracked in `VolumeStatus`.** `vol-<id>` (Kubernetes object
   names must be DNS-1123 subdomains; Serendip's UUIDv7 IDs are already
   lowercase-hex-and-hyphens, so no sanitization needed beyond the
   prefix). `VolumeReconciler` always knows exactly which PVC name to
   look for without needing to persist it — one less piece of state to
   keep in sync. `kubectl describe` convenience for the backing object
   name is a reasonable future `VolumeStatus` field if actually needed,
   not invented speculatively now.
3. **Resync interval: reuse the existing `resyncEvery`/`reconcileTimeout`
   constants (30s/10s, spec 003) — no new tuning.** Nothing about
   storage reconciliation needs a different cadence than Cluster health
   checking did; introducing a second set of tunables with no concrete
   reason would be speculative.

## Approach

**Resource kind**: `core/resource/storage.go` (new file).

```go
const KindVolume Kind = "Volume"

type VolumeSpec struct {
    SizeBytes int64 `json:"sizeBytes"`
}

type VolumeStatus struct {
    Status // embeds Conditions, per every other kind's existing pattern
}

type Volume = Resource[VolumeSpec, VolumeStatus]
```

One Condition, `Type: "Bound"`, mirrors `ClusterReconciler`'s single-
Condition precedent (spec 003) — `Status: True` once the backing PVC's
`status.phase == "Bound"`, `False` if provisioning failed or the PVC
exists but isn't bound yet, `Unknown` if no storage provider is
configured at all.

**`StorageProvider` interface**: `core/reconcile/storage.go` (new file,
alongside the new `VolumeReconciler` — mirrors `health.go`/`cluster.go`'s
existing co-location in this package).

```go
// StorageProvider is the capability-shaped interface a VolumeReconciler
// drives (core/reconcile/CLAUDE.md: capability-shaped interfaces live
// alongside their kind-shaped Reconciler, not in a separate package).
// Uses only core/resource types -- no Kubernetes-specific concept
// (PVC, StorageClass, ...) appears here; that knowledge is entirely
// inside whichever providers/ package implements this interface.
type StorageProvider interface {
    // Ensure makes vol's desired storage exist (creating it if
    // necessary) and reports whether it's currently bound/ready.
    // reason/message are suitable for the Volume's "Bound" Condition
    // (Reason/Message fields) -- same error-as-detail shape
    // HealthChecker already established (spec 003).
    Ensure(ctx context.Context, vol resource.Volume) (bound bool, reason, message string, err error)
    // Delete removes vol's backing storage. A no-op, not an error, if
    // it doesn't exist (already deleted, or never successfully
    // provisioned).
    Delete(ctx context.Context, vol resource.Volume) error
}
```

**`VolumeReconciler`**: `core/reconcile/volume.go` (new file). Takes a
concrete `StorageProvider` via its constructor — **not** a
`core/provider.Registry` handle inside `Reconcile` itself. This mirrors
`ClusterReconciler` exactly (`NewClusterReconciler(clusters, providers,
checker)` takes a concrete `HealthChecker`, no registry lookup inside
`Reconcile`): the `Registry.Lookup` + type-assert happens once, in
`cmd/serendipd`'s composition root, when wiring the Reconciler — not on
every reconcile pass. Keeps `core/reconcile` free of any import of
`core/provider` at all, and matches the one existing precedent this
package already has.

```go
type VolumeReconciler struct {
    volumes  store.Store[resource.VolumeSpec, resource.VolumeStatus]
    provider StorageProvider
}

func NewVolumeReconciler(volumes store.Store[resource.VolumeSpec, resource.VolumeStatus], provider StorageProvider) *VolumeReconciler

func (r *VolumeReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
    vol, err := r.volumes.Get(ctx, ref.ID)
    if errors.Is(err, store.ErrNotFound) { return nil } // deleted since the event/resync fired
    if err != nil { return err }

    bound, reason, message, err := r.provider.Ensure(ctx, vol)
    if err != nil {
        return r.setCondition(ctx, vol, resource.ConditionFalse, "EnsureFailed", err.Error())
    }
    status := resource.ConditionFalse
    if bound {
        status = resource.ConditionTrue
    }
    return r.setCondition(ctx, vol, status, reason, message)
}
```

`setCondition` reuses exactly `ClusterReconciler.setCondition`'s
idempotency check (compare computed `{Status, Reason}` against what's
stored; skip `Update` if unchanged) — acceptance criterion 3's
mechanism, proven the same way spec 003 proved it.

**`providers/storage/localpath`**: the real implementation.

```go
const (
    namespace        = "serendip-system"
    storageClassName = "serendip-local-path"
    provisionerName  = "rancher.io/local-path" // the real upstream provisioner name -- not reimplemented, just referenced (constitution rule 10)
)

type Provider struct { client *kubernetes.Client }

// NewProvider ensures the serendip-system namespace and the
// Immediate-binding-mode serendip-local-path StorageClass both exist
// (idempotent get-then-create, mirroring agent/client.Upsert's existing
// GET-then-POST-or-PUT pattern -- not a new idiom), failing fast at
// cmd/serendipd startup rather than on the first Volume creation.
func NewProvider(ctx context.Context, client *kubernetes.Client) (*Provider, error)

func (p *Provider) Ensure(ctx context.Context, vol resource.Volume) (bound bool, reason, message string, err error)
func (p *Provider) Delete(ctx context.Context, vol resource.Volume) error

var _ reconcile.StorageProvider = (*Provider)(nil)
```

`Ensure`'s logic: GET the PVC at a deterministic path
(`/api/v1/namespaces/serendip-system/persistentvolumeclaims/vol-<id>`).
404 → `Create` it (referencing `storageClassName`, `vol.Spec.SizeBytes`
as the PVC's requested storage). Any phase → report `bound =
(phase == "Bound")`, with `phase` itself as `reason` (`"Pending"`,
`"Bound"`, `"Lost"` are the real, standard Kubernetes PVC phase values)
so an operator reading the Volume's Condition sees the real upstream
state, not a Serendip-invented label.

The `StorageClass` is explicitly **not** marked as the cluster's default
(`storageclass.kubernetes.io/is-default-class` annotation omitted) —
Serendip's own PVCs always reference it by name explicitly, so
default-ness buys nothing and risks an ambiguous-default warning
alongside K3s's own bundled `local-path` StorageClass, which typically
is marked default.

**`cmd/serendipd` wiring**: `wireKubernetes` (spec 006) is extended, not
replaced. After building the `kubernetes.Client`, construct
`localpath.NewProvider(ctx, client)`, `Register("storage", ...)` in the
`core/provider.Registry` (kept for consistency/future introspection even
though this spec's actual wiring doesn't route through a runtime
`Lookup` on the hot path — see above), then immediately `Lookup` +
type-assert once and pass the concrete `StorageProvider` into
`reconcile.NewVolumeReconciler(...)`, registered with the engine the
same way `ClusterReconciler` already is. Spec 006's `nodeLister`/
`kubeNodeLister` proof-of-concept and its registration are deleted
entirely — this spec's real capability replaces it, not sits alongside
it (spec.md Scope-IN says this explicitly).

## Interfaces / types to create

```go
// core/resource/storage.go
const KindVolume Kind = "Volume"
type VolumeSpec struct{ SizeBytes int64 }
type VolumeStatus struct{ Status }
type Volume = Resource[VolumeSpec, VolumeStatus]

// core/reconcile/storage.go
type StorageProvider interface {
    Ensure(ctx context.Context, vol resource.Volume) (bound bool, reason, message string, err error)
    Delete(ctx context.Context, vol resource.Volume) error
}

// core/reconcile/volume.go
type VolumeReconciler struct{ /* ... */ }
func NewVolumeReconciler(volumes store.Store[resource.VolumeSpec, resource.VolumeStatus], provider StorageProvider) *VolumeReconciler
func (r *VolumeReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error

// providers/storage/localpath/provider.go
type Provider struct{ /* ... */ }
func NewProvider(ctx context.Context, client *kubernetes.Client) (*Provider, error)
func (p *Provider) Ensure(ctx context.Context, vol resource.Volume) (bound bool, reason, message string, err error)
func (p *Provider) Delete(ctx context.Context, vol resource.Volume) error

// cmd/serendipd/main.go (extended)
func wireKubernetes(ctx context.Context, kubeconfigPath string, registry *provider.Registry, engine *reconcile.Engine, volumes store.Store[resource.VolumeSpec, resource.VolumeStatus]) error
```

## Files touched

```
core/resource/storage.go, storage_test.go
core/reconcile/storage.go                      (StorageProvider interface)
core/reconcile/volume.go, volume_test.go
providers/storage/localpath/provider.go, provider_test.go, doc.go
cmd/serendipd/main.go                          (extends wireKubernetes; deletes nodeLister/kubeNodeLister entirely)
api/server.go                                  (one new mountResource[VolumeSpec,VolumeStatus] line -- confirm the generic pattern needs nothing else)
docs/architecture/provider-model.md            (record the first resolved capability interface, update the spec-003/006 breadcrumb)
docs/architecture/resource-model.md            (note the Volume-not-Storage naming deviation and why)
docs/PROGRESS.md                               (updated on completion)
```

## Risks

- **This is the first spec where a Reconciler actually *writes* to
  external infrastructure**, not just observes it (`ClusterReconciler`
  only ever read a health-check result; `VolumeReconciler`/`Ensure`
  creates real Kubernetes objects). Constitution rule 3's
  observe-diff-apply-verify loop is followed (`Ensure` is itself
  idempotent — GET-then-maybe-Create — and `VolumeReconciler`'s own
  `Update`-skip-if-unchanged is a second, independent idempotency
  layer), but this is a qualitatively bigger step than anything specs
  001-006 did, worth naming plainly rather than treating as routine.
- **The `serendip-local-path` StorageClass and `serendip-system`
  namespace are cluster-wide, shared mutable state created outside any
  single Volume's lifecycle.** If a human manually deletes either
  out-of-band, every subsequent `Ensure` call fails until
  `cmd/serendipd` restarts (which re-runs `NewProvider`'s setup). Not
  handled by self-healing within a single reconcile pass — acceptable
  for this spec's scope (a restart is a reasonable recovery path at this
  stage), flagged for whoever eventually builds longer-running
  self-healing for cluster-wide provider setup state.
- **No retry/backoff on `Ensure` failures beyond the Engine's own
  periodic resync** (30s) — same trade-off `ClusterReconciler` already
  accepted, not a new limitation.
- **`Volume`→Environment ownership isn't wired in this spec** (no
  `OwnerRef` requirement enforced) — `Metadata.OwnerRefs` already
  supports it generically from spec 001, but nothing requires a `Volume`
  to declare one yet. Acceptable: proving provisioning in isolation
  (Scope-OUT) doesn't need the full org-tree hierarchy wired through.

## Explicitly NOT done in this spec

- `storage.md`'s full `storagePolicy` concept (tiers, replication,
  backup, retention, encryption).
- Longhorn or any multi-node/replicated storage.
- Backup storage.
- Any Application/Component wiring that actually mounts a Volume into a
  workload.
- Capacity reporting / usable-after-replication math.
- Enforced `Volume`→Environment ownership.
