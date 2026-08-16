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

## Open questions

- Whether `ApplicationProvider` / `InfrastructureProvider` end up as the
  actual `core/` interface names, or get split further per capability (e.g.
  separate `ClusterProvider` vs. `ComputeProvider`), is undecided — resolve
  during the relevant spec's plan.md, not by guessing here.

**Breadcrumb from spec 003** (`core/reconcile`, the reconciliation
engine): still doesn't resolve the question above — no real provider
exists yet, so nothing forced a decision. What it did establish, real
and load-bearing for whoever answers the question next:

- `HealthCheckContract.Kind` now has one concrete, implemented value:
  `"tcp"` (dials `Config["address"]` via stdlib `net.Dialer`). An
  empty/unrecognized `Kind` means "not configured," not an error — that
  convention is now precedent, not just a comment.
- The generic dispatch shape that turned out to work:
  `Reconciler.Reconcile(ctx, graph.ResourceRef) error`, one interface per
  *kind* being reconciled, not per capability. Whether the eventual
  capability-specific Provider interface(s) plug in underneath a
  `Reconciler` implementation (most likely, given how `ClusterReconciler`
  is structured — it calls a `HealthChecker`, a capability-shaped
  interface, from inside a kind-shaped `Reconciler`) or need to reshape
  this dispatch layer is worth checking against whatever the first real
  provider actually needs, not assumed either way.
- The observe/verify side (health checking) is now real and working;
  the apply/provision side (actually creating infrastructure) is still
  completely unbuilt — that boundary is exactly where the first real
  provider spec starts, not somewhere upstream of it.
