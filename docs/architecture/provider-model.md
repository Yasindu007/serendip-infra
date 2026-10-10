# Provider model

A capability (`capability-model.md`) is an interface. A provider is a
concrete implementation of that interface for a specific product. This
document describes the contract shape, registration, lifecycle, and
replacement mechanics. Enforcement rules (import direction, removability)
are constitution rule 2 and `core/CLAUDE.md` / `providers/CLAUDE.md`; this
doc explains the *design intent* behind those rules.

## Where interfaces live

- Interfaces are defined in `core/` — one Go interface per capability.
- Implementations live in `providers/<capability>/<product>/` (e.g.
  `providers/identity/thunder/`, `providers/storage/longhorn/`).
- `core/` imports no provider package, ever. Providers import `core/`.
  Deleting a provider directory must not break `go build ./core/... ./api/...`
  (constitution 2).

## Illustrative interface shape

The exact Go signatures are not yet finalized (see `specs/001-resource-model`,
which scopes only the Provider Registry descriptor, not live interfaces).
The shape below is the design intent carried from the founding discussion and
should be treated as a draft to refine during implementation, not as
already-shipped code:

```go
// core/provider/application.go
type ApplicationProvider interface {
    CreateApplication(ctx context.Context, app Application) error
    DeployApplication(ctx context.Context, ref ApplicationRef, spec DeploySpec) error
    ScaleApplication(ctx context.Context, ref ApplicationRef, spec ScaleSpec) error
    DeleteApplication(ctx context.Context, ref ApplicationRef) error
    GetStatus(ctx context.Context, ref ApplicationRef) (Status, error)
}

// core/provider/infrastructure.go
type InfrastructureProvider interface {
    Provision(ctx context.Context, spec InfraSpec) (Handle, error)
    Update(ctx context.Context, handle Handle, spec InfraSpec) error
    Delete(ctx context.Context, handle Handle) error
    GetStatus(ctx context.Context, handle Handle) (Status, error)
}
```

Every capability gets one such interface (`StorageProvider`,
`ObservabilityProvider`, `ClusterProvider`, `IdentityProvider`, ...). All
follow the same observe/plan/apply/verify shape so the reconciliation engine
(constitution 3) can drive any of them identically.

## Registration

Providers register a **descriptor**, not just code, with the Provider
Registry:

- **name** — unique provider identifier (e.g. `longhorn`, `thunder`).
- **capabilities provided** — which capability interface(s) it implements.
- **resource requirements** — RAM/CPU footprint on reference hardware
  (T470-class), used by the topology planner and installer.
- **health check contract** — how the reconciliation loop verifies the
  provider is alive.
- **upgrade behavior** — how version upgrades are performed/verified.

Business logic never instantiates a provider directly; it asks the registry
for "the current provider for capability X" and gets back whatever is
configured as default or explicitly selected for that Environment/Org
(constitution 2).

## Lifecycle

1. **Install** — provider is deployed as part of a profile or added later via
   the marketplace/capability picker.
2. **Health** — reconciliation loop polls the provider's health check
   contract; unhealthy providers surface as Conditions on affected resources.
3. **Upgrade** — driven by the profile's pinned version; providers declare
   their own upgrade procedure (constitution 9: benchmarked before profile
   defaults change).
4. **Removal** — a provider can be deleted without breaking `core/`/`api/`
   builds. If it was the active default for a required capability, the
   registry must refuse removal (or force a replacement selection) rather
   than leave the capability unsatisfied.

## Replacement

Switching a capability's active provider (e.g. local-path → Longhorn, or
Thunder → an existing Keycloak) is a registry configuration change, not a
code change. Spec 001's acceptance criteria state this explicitly:
"registering two providers for one capability and switching the default
requires no changes outside registry data."

## Bring your own capability

The `external` provider is not a special case in the code — it is a provider
like any other, whose implementation is "call out to a system the user
already runs" instead of "manage a bundled deployment." This is what
constitution rule 1's "external escape hatch" requires for every capability.

## Registration mechanism (resolved — spec 006)

The "Registration" section above describes the *data* a provider
declares (name, capabilities, resource requirements, health check,
upgrade behavior) — that's `core/registry.{Capability,Provider}Registry`,
persisted via the ordinary resource store, since spec 001. What was
still unresolved through 5 specs was different: how does a
capability-specific **Go interface implementation** — real, executable
code, not a data descriptor — get wired up without `core/` ever
importing a `providers/` package (constitution rule 2)?

Spec 006 answers this: **`core/provider.Registry`**, a small,
capability-agnostic, in-memory map from a capability name to a live Go
value (`Register(capability string, impl any)`,
`Lookup(capability string) (any, bool)`). The dependency-inversion trick
is where the import happens: `cmd/serendipd` (the composition root) is
the one place that imports *both* `core/provider` and a specific
`providers/` package (e.g. `providers/kubernetes`), constructs the real
implementation, and calls `Register`. `core/` itself — including
`core/provider` — never imports anything under `providers/`. Whatever
capability-specific interface a Reconciler expects (a future
`StorageProvider`, say) is defined wherever that capability's own code
lives, type-asserted out of `Registry.Lookup`'s `any` result — `core/
provider` doesn't know or care what any specific capability's interface
looks like.

Proven, not just designed: spec 006 registered a deliberately trivial
`nodeLister` capability (list Kubernetes Nodes) through this exact
mechanism, end-to-end against a real K3s cluster (`serendipd` logged
the real node count, matching `kubectl` directly) — the registry
pattern itself is now load-bearing, tested infrastructure, not an
untested idea.

## First capability-specific interface — resolved (spec 007)

Spec 007 is the first real answer to "what does a capability-specific
interface actually look like." `StorageProvider`
(`core/reconcile/storage.go`):

```go
type StorageProvider interface {
    Ensure(ctx context.Context, vol resource.Volume) (bound bool, reason, message string, err error)
    Delete(ctx context.Context, vol resource.Volume) error
}
```

Confirms the breadcrumb below's prediction almost exactly: a
`VolumeReconciler` (kind-shaped, lives in `core/reconcile`) holds a
concrete `StorageProvider` (capability-shaped, defined alongside the
Reconciler in the same package, mirroring `ClusterReconciler`/
`HealthChecker`'s existing precedent) injected via its constructor —
**not** looked up from `core/provider.Registry` inside `Reconcile`
itself. The `Registry.Lookup` + type-assert happens exactly once, in
`cmd/serendipd`'s composition root, when wiring the Reconciler — this
turned out to be the right call: it keeps `core/reconcile` free of any
import of `core/provider` at all, and matches `ClusterReconciler`'s own
`HealthChecker` injection exactly, rather than introducing a second,
inconsistent way to obtain a capability implementation.

**A real gap this interface shape surfaced, worth remembering for every
future capability**: `Delete` is a required method, not optional —
`ClusterReconciler`'s `HealthChecker` never needed one (a passive
observer has nothing to tear down), but `StorageProvider` does, because
it actually provisions real external state. The first implementation
(`VolumeReconciler`) initially missed wiring `Delete` into the
reconcile loop at all (the `store.ErrNotFound` branch, hit when the
Serendip-side resource is deleted, just returned `nil` — copied
verbatim from `ClusterReconciler`, where that's correct). Real-cluster
testing caught this: deleting a `Volume` via the API left the real
`PersistentVolumeClaim` behind. **Any future capability interface that
includes a `Delete`/teardown method must have its Reconciler's
`ErrNotFound` branch actually call it** — don't copy
`ClusterReconciler`'s no-op pattern by default; check whether this
capability's provider has state to clean up first.

## A capability's interface shape need not mirror `StorageProvider`'s (spec 009)

`SecretsProvider` (`core/reconcile/secrets.go`) confirms each
capability gets its own narrow interface, not a shared base shape —
and shows that shape can genuinely differ from `StorageProvider`'s
"Ensure provisions, Delete tears down" pattern:

```go
type SecretsProvider interface {
    Exists(ctx context.Context, sec resource.Secret) (bool, error)
    WriteValue(ctx context.Context, sec resource.Secret, value map[string]any) error
    ReadValue(ctx context.Context, sec resource.Secret) (map[string]any, error)
    Delete(ctx context.Context, sec resource.Secret) error
}
```

`Exists`, not `Ensure` — this capability has nothing to provision
(OpenBao's KV v2 mount already exists once spec 008's `Bootstrap` has
run); all a reconcile loop can honestly do is observe whether a value
exists and report that (constitution rule 6). Don't assume every
capability interface needs an `Ensure`-shaped method just because
`StorageProvider` has one — derive the shape from what the capability
actually does.

`Secret` is also the first case where an `api/` handler depends on a
`core/reconcile` type (`*SecretReconciler`) instead of only
`core/store` — `api/CLAUDE.md`'s prior status quo. This is specific to
a value's lifecycle (read/write/delete) being unable to go through the
generic whole-resource CRUD pattern at all, not a new general rule;
documented in full in `docs/architecture/secrets.md`. Most future
capabilities should still only ever need `core/store` from `api/` —
check for this same structural wall before reaching for
`core/reconcile` from a handler.

## Open questions

- Whether `ApplicationProvider` / `InfrastructureProvider` end up as
  the actual `core/` interface names for Identity/Application-
  Platform/Gateway, or get split further per capability, is still
  undecided — `StorageProvider` and `SecretsProvider` above are two
  data points (narrow, capability-specific interfaces with genuinely
  different method shapes, not a shared base type), confirming each
  capability likely gets its own narrow interface rather than a shared
  `InfrastructureProvider` all capabilities implement. Resolve during
  each capability's own spec's plan.md, not by guessing here.
- **Real cluster/product behavior can invalidate a design that looked
  sound on paper** (spec 007's entire provisioning mechanism changed
  after real-cluster testing, not during planning) — budget real
  verification time, not just planning time, for every future
  capability's spec, especially ones with non-trivial upstream product
  behavior of their own (Thunder, OpenBao, OpenChoreo all qualify).
