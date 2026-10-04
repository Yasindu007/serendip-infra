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

## Open questions

- Whether `ApplicationProvider` / `InfrastructureProvider` (or a
  capability-specific interface like `StorageProvider`) end up as the
  actual `core/` interface names, or get split further per capability,
  is still undecided — spec 006 resolved *how* such interfaces get
  registered/wired (above), not *what* any specific one looks like.
  Resolve each capability's interface shape during that capability's
  own spec's plan.md (spec 007: Storage), not by guessing here.

**Breadcrumb from spec 003** (`core/reconcile`, the reconciliation
engine), still relevant: the generic dispatch shape that turned out to
work is `Reconciler.Reconcile(ctx, graph.ResourceRef) error`, one
interface per *kind* being reconciled, not per capability. Given spec
006's resolution above, the likely shape for spec 007 is: a
`VolumeReconciler` (kind-shaped, lives in `core/reconcile`) that calls
`core/provider.Registry.Lookup("storage")`, type-asserts to a
`StorageProvider` interface (capability-shaped, defined somewhere
`core/reconcile` can reference without importing `providers/` —
likely alongside the Reconciler itself, mirroring how
`ClusterReconciler` already calls a capability-shaped `HealthChecker`
from inside a kind-shaped `Reconciler`), and the real implementation
registered in `cmd/serendipd` is a `providers/storage/localpath` type
wrapping spec 006's `providers/kubernetes.Client`. Worth checking
against what spec 007 actually needs once written, not assumed to be
exactly right in advance.
