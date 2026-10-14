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

**Superseded sketch.** The `ApplicationProvider`/`InfrastructureProvider`
code below is the founding-discussion sketch, kept for history. It was never
shipped: real interfaces are narrow and per-capability (`StorageProvider`,
`SecretsProvider`, `ApplicationPlatformProvider`, `GatewayProvider`; see the sections below).
Read it as design intent about the observe/plan/apply/verify shape only, not
as names or signatures:

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

Since spec 014 the registry also carries **readiness** per capability
(`SetReadiness`, `Readiness()`; `Readiness{Ready, Reason, Message, Since}`,
mutex-guarded, in-memory). The composition root reports setup progress
there, and `GET /v1/readiness` serves it. `Since` moves only when `Ready` or
`Reason` changes. See `reconciliation.md`.

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
itself. The `Registry.Lookup` + type-assert happened exactly once, in
`cmd/serendipd`'s composition root, when wiring the Reconciler (since spec
014 the Kubernetes-facing providers are handed to the Reconciler through a
lazy handle instead; see "Lazy provider handles" below) — this
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

## A provider's integration mechanism need not be the Kubernetes REST API (spec 010)

Every provider since spec 006 (`providers/kubernetes`, `providers/storage/
localpath`, `providers/secrets/openbao`) talks to its product exclusively
through `providers/kubernetes.Client` — direct construction of Kubernetes
API objects. `providers/application/openchoreo` (spec 010) is the first to
instead shell out to a real external CLI tool (`helm`, via `os/exec`) as its
primary integration mechanism, using `providers/kubernetes.Client` only for
the one piece Helm itself can't do (the `DataPlane` CR, the CA-trust
ConfigMap; applied server-side since spec 014). ADR 0010 is the authoritative record for why this is justified
for OpenChoreo specifically and why it does not reopen ADR 0002/0009's "no
Helm, direct objects" precedent for Storage/Secrets — this is a
per-capability evaluation, not a new default. The concrete lesson for the
next capability: check what the upstream product's own first-party
distribution mechanism actually is before assuming every provider talks to
`providers/kubernetes.Client` directly — if that mechanism is a real CLI
tool with its own well-tested idempotency/wait semantics (as `helm upgrade
--install --wait` has), reuse it per spec 005's "official tooling over
reimplementation" precedent rather than defaulting to hand-rolled object
construction out of habit.

## Application Platform: a third narrow interface (spec 011)

The Application Platform capability did not become `ApplicationProvider`.
The real interface is `ApplicationPlatformProvider`
(`core/reconcile/application.go`), four methods over two resource kinds:

```go
type ApplicationPlatformProvider interface {
    EnsureApplication(ctx, app resource.Application) (ready bool, reason, message string, err error)
    DeleteApplication(ctx, app resource.Application) error
    EnsureComponent(ctx, app resource.Application, comp resource.Component) (ComponentObservation, error) // was a 4-value return until spec 012
    DeleteComponent(ctx, comp resource.Component) error
}
```

`Ensure*` has `StorageProvider.Ensure`'s return shape (provisions real state,
reports readiness); each kind has its own reconciler. Third data point, after
`StorageProvider` and `SecretsProvider`, that each capability gets its own
narrow interface with a shape derived from what it does, not a shared base
type. Both reconcilers' not-found branches call `Delete*`, per the spec 007
lesson above. Detail: `application-platform.md`.

Breadcrumb for future providers: an upstream product may need CRDs installed
even for a feature you have disabled. OpenChoreo's controller lists Gateway
API kinds regardless of `gateway.enabled=false` and never became Ready
without those CRDs. Chart toggles do not prove a feature's CRDs are
unneeded; check on a real cluster.

## Gateway: a fourth narrow interface, and capabilities that cooperate (spec 012)

`GatewayProvider` (`core/reconcile/gateway.go`) has one method:

```go
type GatewayProvider interface {
    EnsureGateway(ctx) (ep GatewayEndpoint, ready bool, reason, message string, err error)
}
```

Fourth data point after Storage, Secrets and Application Platform: the shape
comes from what the capability does (make one shared gateway exist, say where
it is), so there is no `Delete` and no per-resource reconciler. It is ensured
by background setup, then re-ensured on an interval (spec 014), rather than
reconciled per resource, because there is no `Gateway` resource kind yet.

**Pattern for capabilities that must cooperate without importing each
other.** The Application Platform provider needs to know which gateway to
attach routes to, and the gateway needs pods labeled the way the platform's
NetworkPolicies expect. Neither provider names the other. `core/` defines a
plain value (`GatewayEndpoint`), and `cmd/serendipd` is the only place that
sees both sides: it takes the value from the gateway capability and passes it
to the platform provider, and takes the platform provider's required proxy
labels (`openchoreo.GatewayProxyLabels()`) and passes them to the gateway
provider. Each provider stays removable, and a replacement gateway only has to
honor the same inputs. Prefer this over one provider looking the other up.

**`providers/internal/`** holds helpers shared by several providers
(`helm`, `gatewayapi`). Go's `internal/` rule keeps them importable only from
`providers/`, so `core/`, `api/` and `cmd/` cannot depend on them. A
consequence: `cmd/serendipd` cannot call them, so each provider ensures its
own prerequisite (both `kgateway.Deployer.Ensure` and the OpenChoreo wiring
call `gatewayapi.EnsureCRDs`; the second call is a few GETs). Detail:
`gateway.md`.

## Server-side apply for Serendip-owned objects (spec 014)

Providers used to create objects with get-or-create, then merge-patch on
difference. A merge patch cannot remove a field it does not mention, so a
label or map key Serendip dropped in a later version stayed on the live
object forever. Objects Serendip's providers own are now written with
server-side apply.

`providers/kubernetes.Client.Apply(ctx, path, obj, fieldManager)` sends
`PATCH <path>?fieldManager=serendip&force=true` with
`Content-Type: application/apply-patch+yaml` (the JSON body is valid YAML, so
no dependency). `obj` carries the full desired state of the fields Serendip
owns. The field manager is the constant `kubernetes.FieldManager`
(`"serendip"`), one stable name for every provider and release. `ErrNotFound`
on 404 (parent namespace missing).

- **Why `force=true` is safe.** An apply names only fields Serendip declares
  on objects it created. A conflict there means something else changed a
  Serendip-owned field, which drift repair should undo. Fields set only by
  other managers (an operator's labels, controllers' status) are not in the
  apply, so they are never touched.
- **Removal is automatic.** A field applied before and omitted now is removed
  by the API server; this replaced the hand-written drift checks and patch
  builders, and the explicit-null special cases (such as the Workload's
  endpoint `visibility`).
- **A no-drift apply stores nothing.** The request is made, but
  `resourceVersion` and generation are unchanged. "Did I just change it?" is
  read from `metadata.generation` before (the GET the provider already does)
  and after (the apply response).

**Applied by Serendip:** local-path PV, PVC and the `serendip-system`
Namespace; OpenBao's Namespace, ConfigMap, PVC, Service and StatefulSet;
OpenChoreo's DataPlane, `cluster-gateway-ca` ConfigMap, the four platform
defaults, Project, ProjectReleaseBinding, Component and Workload; kgateway's
GatewayParameters and Gateway.

**Stays merge patch:**

- the `openchoreo-control-plane` Namespace label: Helm created the namespace,
  Serendip owns one label on it;
- the Gateway API CRD installer (`providers/internal/gatewayapi`): it is
  version-gated by bundle annotation and upgrade-only, not a desired-state
  object owned field by field.

**Helm-owned** (cert-manager, OpenChoreo and kgateway charts) stays Helm's.

**Migration note.** Objects created before spec 014 (by POST and merge patch)
have their fields recorded under an older `Update` manager. The first apply
makes `serendip` co-own every field it applies; nothing is wiped. A field the
old manager set that Serendip no longer sets stays once, because it was never
applied and apply cannot remove it. Clusters created under specs 007-013 may
carry such leftovers; clean up by hand by removing the field with `kubectl`.
New clusters are clean. Rewriting `managedFields` was rejected as not worth
its risk for a project with no production clusters.

## Lazy provider handles (spec 014)

The Kubernetes-facing providers no longer have to exist when reconcilers are
registered. `cmd/serendipd/lazy.go` holds one small wrapper per interface
(`StorageProvider`, `SecretsProvider`, `ApplicationPlatformProvider`) over an
atomic pointer. Background setup stores the real provider once it succeeds;
until then every method returns `reconcile.ErrProviderNotReady`, a sibling of
`ErrNotConfigured`, which reconcilers turn into a `ProviderNotReady`
Condition plus an early re-check. The wrappers live in the composition root
so neither `core/` nor the providers change shape. A new capability whose
setup can be slow follows the same pattern. Detail: `reconciliation.md`.

## Open questions

- **Real cluster/product behavior can invalidate a design that looked
  sound on paper** (spec 007's entire provisioning mechanism changed
  after real-cluster testing, not during planning) — budget real
  verification time, not just planning time, for every future
  capability's spec, especially ones with non-trivial upstream product
  behavior of their own (Thunder, OpenBao, OpenChoreo all qualify).
