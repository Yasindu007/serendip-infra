# Plan: Spec 011 — Application Platform capability

Status: Draft — human approval required before implementation

Upstream facts below come from an oss-researcher pass against the
OpenChoreo **v1.3.1** tag (`api/v1alpha1`, `config/crd/bases`,
`internal/controller`, `samples/`) on 2026-10-11. Items marked
**(unconfirmed)** are not settled facts: T008's real-cluster run is the
place they get settled, and code gets corrected there if they're wrong
(the same "plan.md is a hypothesis" discipline specs 007/008/009/010 all
needed).

## Upstream object model (what OpenChoreo 1.3.1 actually needs)

Resolves spec.md's first open question.

- **There is no `Organization` CRD.** A plain Kubernetes Namespace
  labeled `openchoreo.dev/control-plane=true` is the org/tenant
  boundary. `Project`, `Component`, `Workload`, `Environment`, and
  `DeploymentPipeline` are all namespaced inside it.
  `docs/architecture/application-platform.md`'s CRD list (carried over
  from spec 010's planning) is wrong on this point and gets corrected
  in T010.
- **The control-plane chart seeds nothing.** No Environment,
  DeploymentPipeline, ClusterProjectType, or ClusterComponentType ships
  by default; they all come from `samples/getting-started/all.yaml`.
  This spec has to create its own.
- **Minimal chain to run one pre-built image:**
  1. Namespace (labeled)
  2. `ClusterProjectType`: its template creates the data-plane "cell"
     namespace
  3. `ClusterComponentType` with `workloadType: deployment`
  4. `Environment` with `dataPlaneRef` → `DataPlane/default` in the same
     namespace
  5. `DeploymentPipeline`
  6. `Project`, referencing the pipeline and project type
  7. `ProjectReleaseBinding`, one per Project × Environment. We must
     create it; no controller does.
  8. `Component` with `autoDeploy: true` and `componentType:
     deployment/<name>`
  9. `Workload`: image, env, endpoints

  With `autoDeploy`, the Component controller creates the
  `ComponentRelease` and the `ReleaseBinding` (`{component}-{env}`)
  itself, and the ReleaseBinding's `RenderedRelease` applies the real
  Deployment into the cell namespace. We never create releases.
- **Readiness signals.** `Component.status` `Ready` does **not** mean a
  Pod is running. The authoritative signal is the
  **`ReleaseBinding` `Ready` condition**: reason `Ready` when the
  workload is up, otherwise one of `ResourcesProgressing` /
  `ResourcesNotReady` / `ResourcesDegraded` / `ResourceApplyFailed` /
  `RenderingFailed` / `EnvironmentNotFound` / `DataPlaneNotFound`.
  `ProjectReleaseBinding` has its own `Ready` condition, which covers
  the cell namespace existing.
- **Deletion is finalizer-driven, not ownerReference-driven.**
  - Deleting a `Component` cleans up its Workload, releases, and live
    data-plane resources (`openchoreo.dev/component-cleanup`).
  - Deleting a `Project` cleans up its Components, ProjectReleaseBindings,
    and cell namespaces (`openchoreo.dev/project-cleanup`).
  - Deletes are asynchronous and hang if controller-manager or the
    data-plane agent is down.
- **ComponentType gotchas.**
  - `componentType` is **immutable** on a Component.
  - The sample `service` type validates `size(workload.endpoints) > 0`.
  - An endpoint with `external`/`internal` visibility needs gateway
    ingress, which spec 010 disabled. Only the default `project`
    visibility is usable here.

## Resolved design decisions

### 1. Hierarchy mapping: Serendip `Application` ↔ OpenChoreo `Project`, 1:1; fixed org namespace

Resolves spec.md's structural open question.

- Each Serendip `Application` gets exactly one OpenChoreo `Project`,
  plus that Project's one `ProjectReleaseBinding`.
- Each Serendip `Component` gets one OpenChoreo `Component` + `Workload`.
- Serendip's own `Organization`/`Team`/`Project` resource kinds are
  **not** mapped onto anything in this spec. All OpenChoreo objects live
  in one fixed org namespace (decision 2).
  - Mapping Serendip `Organization` onto an OpenChoreo namespace means
    one namespace per tenant. Each would need its own
    `DataPlane`/`Environment`/`DeploymentPipeline` set, which is real
    multi-tenant work this spec's Scope-OUT already defers.
- Why 1:1 Application→Project rather than one hidden default Project:
  - Each OpenChoreo Project gets its own data-plane cell namespace, so
    Applications are isolated from each other for free.
  - Deleting an Application maps onto deleting one Project, whose own
    finalizer tears everything down.
  - A single hidden Project would put every Application's workloads in
    one namespace and make Application deletion a manual multi-object
    sweep.

### 2. Org namespace: reuse `openchoreo-control-plane`

- Spec 010's `DataPlane/default` already lives there, and an
  `Environment`'s `dataPlaneRef` resolves `DataPlane` by name *in its own
  namespace*.
- Using a separate namespace would need a second `DataPlane` CR pointing
  at the same cluster-agent (behavior unknown, risky) or switching spec
  010 to a `ClusterDataPlane` (re-opens a completed, verified spec).
- `EnsurePlatformDefaults` (decision 5) adds the
  `openchoreo.dev/control-plane=true` label to this namespace if it's
  missing. Whether the chart already sets it, and whether it's enforced,
  is **(unconfirmed)**; T008 checks both.
- Cost: user-workload *definitions* (not the workloads themselves, which
  run in per-Project cell namespaces) share a namespace with the control
  plane's own Deployments. Acceptable at edge-1. Revisit with
  multi-tenancy.

### 3. Real cluster object names are derived from the Serendip ID alone

- `Volume` already set the rule: the reconciler's delete branch only has
  the deleted resource's ID. Cascade-deleting an Application publishes
  one delete event per child Component (`core/store/sqlite/store.go`),
  so Component deletion must also work from the ID alone.
- Names: `app-<h>` for Projects, `cmp-<h>` for Components/Workloads,
  where `<h>` = first 10 hex chars of `sha256(Metadata.ID)`.
- Not the raw UUID. OpenChoreo builds the cell namespace name as
  `dp-{orgNs}-{project}-{env}-{hash}`; with a 36-char UUID plus the
  24-char org namespace, that blows well past Kubernetes' 63-char
  namespace-name limit. Whether OpenChoreo truncates on its own is
  **(unconfirmed)**, so this sidesteps the question.
- Collision risk at 40 bits is negligible at edge-1 scale (dozens of
  resources).
- Human-readable context goes in labels: `serendip.dev/id` (full ID) and
  `serendip.dev/name`. They're for `kubectl` debugging only; nothing
  reads them back.

### 4. `ComponentSpec` / `ApplicationSpec` shape

```go
// core/resource/app.go
type ApplicationSpec struct{} // unchanged: pure grouping, needs no fields

type ComponentSpec struct {
    Image string            `json:"image"`
    Ports []ComponentPort   `json:"ports,omitempty"`
    Env   map[string]string `json:"env,omitempty"`
}

type ComponentPort struct {
    Name string `json:"name"` // DNS-label; becomes the endpoint key
    Port int32  `json:"port"`
}
```

- **No protocol field.** The provider maps every port to an endpoint of
  type `TCP`, the honest generic choice that works for any listener.
  HTTP-aware routing only matters once ingress exists (Scope-OUT).
- **Env is plain `map[string]string`.** No shape anticipating `Secret`
  injection (constitution rule 10). Adding a `valueFrom`-style field
  later is additive, not breaking.
- **Zero ports is valid at the `core/resource` level** but unsupported
  by this spec's provider: `EnsureComponent` returns not-ready with
  reason `Unsupported`.
  - OpenChoreo's port-less `worker` ComponentType exists, but
    `componentType` is immutable, so supporting both would need
    delete-and-recreate whenever ports go 0↔N.
  - Deferred, not silently dropped. Recorded in application-platform.md.
- **No new API-layer validation.** `core/resource.Validate` checks only
  `Metadata` today; no kind has spec-level validation. An empty `Image`
  or a missing/non-`Application` owner shows up as an honest `Ready=False`
  Condition with reason `InvalidSpec` instead. That means zero `api/`
  changes, consistent with every capability except Secrets.

### 5. Platform defaults are ensured once at startup, not reconciled

`openchoreo.EnsurePlatformDefaults(ctx, client)` runs in
`wireOpenChoreo` right after `EnsureDefaultDataPlane`, which is the same
"foundation object, ensure at startup" pattern. It idempotently
GET-then-Creates:

- the namespace label (decision 2);
- `ClusterProjectType/serendip-default`;
- `ClusterComponentType/serendip-service` (`workloadType: deployment`);
- `Environment/development` (`dataPlaneRef: DataPlane/default`,
  `isProduction: false`);
- `DeploymentPipeline/serendip`, a single-environment pipeline. Whether
  `targetEnvironmentRefs: []` is accepted is **(unconfirmed)**. Fallback:
  a self-referencing path, or a second, never-bound `staging`
  Environment, whichever T008 shows works.

Details:

- The cluster-scoped types are `serendip-`-prefixed so they can't
  collide with a user who also applies OpenChoreo's own getting-started
  sample (`default`/`service`).
- Exact `spec` bodies for the two cluster-scoped types are copied from
  v1.3.1's `samples/getting-started/all.yaml`, fetched verbatim via
  oss-researcher in T004. The researcher only saw part of that file, so
  they aren't guessed here.

### 6. `ApplicationPlatformProvider` interface: four methods, two reconcilers

```go
// core/reconcile/application.go
type ApplicationPlatformProvider interface {
    EnsureApplication(ctx context.Context, app resource.Application) (ready bool, reason, message string, err error)
    DeleteApplication(ctx context.Context, app resource.Application) error
    EnsureComponent(ctx context.Context, app resource.Application, comp resource.Component) (ready bool, reason, message string, err error)
    DeleteComponent(ctx context.Context, comp resource.Component) error
}
```

- **The return shape mirrors `StorageProvider.Ensure` exactly.** Both
  capabilities provision real external state and report readiness. This
  is a real `Ensure`/`Delete` capability, not `SecretsProvider`'s
  `Exists` shape.
- **Two reconcilers** (`ApplicationReconciler`, `ComponentReconciler`),
  not one:
  - The engine dispatches per `resource.Kind`.
  - Application is not pure grouping on the provider side. It owns a
    real Project + ProjectReleaseBinding.
- **`EnsureComponent` takes the owning `Application`.** The Component CR
  needs its Project's name. `ComponentReconciler` resolves it from the
  Component's single `OwnerRef` of kind `Application`, using the
  Application store.
- **Both delete branches call the provider.** Each reconciler's
  `store.ErrNotFound` branch calls `Delete*` with a minimal resource
  carrying only the ID (the `VolumeReconciler` precedent, plus
  `provider-model.md`'s breadcrumb).
- **Delete is fire-and-forget at the Serendip level.** It issues the
  DELETE, treats 404 as success, and returns. OpenChoreo's finalizers do
  the actual teardown asynchronously. T008 verifies the Pod really goes
  away.

### 7. Conditions: one `Ready` Condition per kind, provider-neutral reasons

Every reason string in `core/` is fixed and product-neutral (acceptance
criterion 6). The provider translates OpenChoreo's reasons into this set
and carries the upstream reason/message text in `Message`:

| Reason | Set by | Meaning |
|---|---|---|
| `Deployed` | provider | Workload running (ReleaseBinding `Ready`=True) / Project's cell namespace ready |
| `Progressing` | provider | Objects created, upstream not ready yet (incl. ReleaseBinding not yet created by autoDeploy) |
| `DeployFailed` | provider | Upstream reports a terminal-looking failure (`ResourceApplyFailed`, `RenderingFailed`, `*NotFound`, `ResourcesDegraded`) |
| `Unsupported` | provider | Valid spec this provider can't run (zero ports) |
| `InvalidSpec` | reconciler | Empty image, no/multiple Application owner refs |
| `WaitingForApplication` | reconciler | Owning Application missing, or its `Ready` isn't True yet |
| `EnsureFailed` | reconciler | Provider returned an error (API call failed) |

- **Same idempotency guard as every reconciler so far:** write only
  when `{Status, Reason}` changes. `Message` is deliberately excluded
  from the comparison, as in Volume/Secret, because upstream messages
  can churn without meaningful change.
- **No `CheckReady`-style retry loop** (resolves spec.md's question).
  That retry existed because spec 010's check ran once at startup and
  killed the daemon on failure. A per-resource reconcile already retries
  for free on every 30s resync, and a transient error just becomes an
  `EnsureFailed`/`Progressing` Condition for one tick.

### 8. Updates and idempotency inside the provider

- **New `Patch` method.** `providers/kubernetes.Client` gains
  `Patch(ctx, path string, patch any) (Object, error)`, sending
  `application/merge-patch+json`. Two places need it:
  - labeling the existing namespace;
  - updating a Workload when the image/env/ports change, after which
    autoDeploy rolls out a new release.
- Merge patch rather than PUT: PUT round-trips through this package's
  minimal `Object` envelope, which drops any metadata field it doesn't
  model (finalizers, annotations, owner refs). Merge patch only touches
  what we send. It's the smallest generic addition; spec 008's T001
  extended this client the same way.
- **`EnsureComponent` flow:**
  1. GET Component CR; create it if absent. `componentType` is fixed
     (`deployment/serendip-service`), so it is never patched.
  2. GET Workload; create it if absent. If present, unmarshal only the
     fields we own (`container.image`, `container.env`, `endpoints`),
     compare with the desired values, and PATCH only on a real
     difference. This makes a no-drift reconcile zero writes on the
     cluster side too, not just in the Serendip store.
  3. GET ReleaseBinding `cmp-<h>-development`. A 404 means
     `Progressing` (autoDeploy hasn't created it yet). Otherwise, map
     its `Ready` condition (decision 7).
- **`EnsureApplication`:** the same GET-or-create for Project and
  ProjectReleaseBinding, then map the ProjectReleaseBinding's `Ready`.

### 9. Wiring

- `wireOpenChoreo` (`cmd/serendipd/main.go`) gains the `db`, `bus`,
  `engine` parameters, the same shape `wireOpenBao` already has. It:
  1. runs `EnsurePlatformDefaults`;
  2. registers `openchoreo.NewProvider(client)` under
     `"application-platform"`, **replacing** `Marker{}` (deleted
     outright, per the "replace, don't leave a parallel path" precedent
     specs 007/009 set);
  3. does the single Lookup + type-assert to
     `reconcile.ApplicationPlatformProvider`;
  4. builds bus-attached Application/Component stores;
  5. registers both reconcilers with list funcs.
- Resync stays the existing 30s/10s (resolves the last open question).
  - OpenChoreo-side progress (a Pod becoming ready) isn't a Serendip
    event, so status catches up on the next resync. Expect
    "Component created → `Deployed`" to take up to ~30s past the real
    Pod-ready moment.
  - The engine has no per-resource requeue today. Adding one is
    engine-level scope this spec doesn't need.
- A 10s `reconcileTimeout` comfortably covers the ≤4 sequential API
  calls per Ensure.

## Files touched

- `providers/kubernetes/client.go`, `client_test.go`: `Patch`
- `core/resource/app.go`, `app_test.go`: `ComponentSpec` fields,
  `ComponentPort`
- `core/reconcile/application.go` (new): interface, `ApplicationReconciler`,
  `ComponentReconciler`, reason constants
- `core/reconcile/application_test.go` (new)
- `providers/application/openchoreo/names.go` (new), `names_test.go`:
  ID→name derivation
- `providers/application/openchoreo/defaults.go` (new), `defaults_test.go`:
  `EnsurePlatformDefaults`
- `providers/application/openchoreo/provider.go` (new), `provider_test.go`:
  `Provider`, `NewProvider`, status mapping
- `providers/application/openchoreo/deploy.go`: delete `Marker`
- `cmd/serendipd/main.go`: `wireOpenChoreo` extended
- Docs (T010): `docs/architecture/application-platform.md`,
  `docs/architecture/provider-model.md` (resolve the
  `ApplicationProvider` open question), `docs/architecture/resource-model.md`
  (Component now has real fields), new
  `providers/application/openchoreo/VERSIONS.md` (`providers/CLAUDE.md`
  requires it; no provider has one yet; just this one is added, not
  backfilled), `docs/PROGRESS.md`
- **No `api/` change.** The generic handlers for `/v1/applications` and
  `/v1/components` are already mounted (`api/server.go:58-59`) and pick
  up the new `ComponentSpec` fields through JSON automatically.
- **No new Go dependency.** `crypto/sha256` and `encoding/hex` are
  stdlib.

## Risks

1. **Unconfirmed upstream details** (all settled in T008, code fixed
   there if wrong):
   - namespace-label enforcement;
   - whether a single-environment `DeploymentPipeline` is accepted;
   - the exact ClusterProjectType/ClusterComponentType bodies (fetched
     in T004);
   - the Workload `container.env` wire shape;
   - whether the data-plane Deployment carries an
     `openchoreo.dev/component` label to find it by.
2. **Asynchronous, finalizer-driven deletes can hang** if
   controller-manager or the agent is down. Serendip's record is
   already gone at that point, and the engine has no retry for a deleted
   ID: a failed DELETE call (not a slow finalizer) orphans the CR. Same
   pre-existing limitation as `VolumeReconciler`. Recorded in
   application-platform.md, not fixed here (engine-level).
3. **Resource footprint.** A real workload Pod plus OpenChoreo's
   rendering pipeline on top of spec 010's install. T008 records one
   dev-machine data point; this is still not ADR 0005's T470 gate.
4. **Org-namespace sharing with the control plane** (decision 2). An
   accepted edge-1 trade-off, flagged for the eventual multi-tenancy
   spec.
5. **Image pull from inside `kind`.** The proof uses a small public
   image (`nginx:1.27-alpine`, port 80). A slow pull just stretches
   `Progressing`; it isn't a failure.

## Explicitly NOT done

Everything in spec.md's Scope-OUT, plus these plan-level deferrals:

- Port-less (`worker`) Components (decision 4).
- Any ingress-visible endpoint visibility (`external`/`internal`).
- Mapping Serendip `Organization`/`Project` onto OpenChoreo namespaces
  (decision 1).
- Spec-level validation in `api/` (decision 4).
- Reconciling the platform defaults on resync. They're ensured once at
  startup like `DataPlane/default`; a user deleting them by hand is
  repaired on the next `serendipd` restart, not live.
- A per-resource requeue in the engine (decision 9).
- Retrying a failed DELETE for an already-deleted Serendip record
  (risk 2).

## Corrections from real-cluster verification (T008)

The plan above was a hypothesis; the real run (kind, Kubernetes 1.34.0)
changed or settled the following. Where this section and the sections
above differ, this section wins.

- **Gateway API CRDs are a prerequisite (new, not in the plan).**
  OpenChoreo's RenderedRelease controller lists Gateway/HTTPRoute in the
  data plane and fails every reconcile without those CRDs, even with
  `gateway.enabled=false`; nothing ever became Ready. Added
  `EnsureGatewayAPICRDs`: Gateway API v1.3.0 standard-channel CRDs only (no
  controller, no Gateway objects), downloaded from the pinned GitHub
  release URL with sha256 verification, skipped if all 5 CRDs already exist,
  never upgrading existing CRDs. Wired after `EnsureDefaultDataPlane`.
  Known limitation: a fresh install needs network access to github.com.
  This does not make ingress in scope.
- **Unknowns settled (risk 1):**
  - The `openchoreo.dev/control-plane=true` label set on the reused
    namespace works.
  - A single-environment `DeploymentPipeline` (empty
    `targetEnvironmentRefs`) is accepted (`Available=True`), so no fallback
    was needed.
  - Workload env is `[{key,value}]`, delivered to the Pod through the
    component type's env-config ConfigMap via `envFrom`.
  - Data-plane objects carry an `openchoreo.dev/component` label.
  - OpenChoreo truncates long cell-namespace names itself (the hashed names
    are still kept: the delete branch needs ID-derived names).
- **Review-driven changes:**
  - `mapReady` treats a `Ready` condition with `observedGeneration` older
    than the object's `metadata.generation` as `Progressing`.
  - `EnsureComponent` returns `Progressing` right after patching a
    Workload, instead of reading the ReleaseBinding that still describes the
    previous release.
  - A real-cluster rerun showed that the rule above isn't sufficient on its
    own. The reconciler's own status write re-triggered a reconcile within
    milliseconds, before OpenChoreo retargeted the binding (about 80 ms), so
    the old release briefly read as `Deployed`. Fix: `bindingReleasesDesired`
    only trusts the binding's `Ready` once the bound `ComponentRelease`'s
    `spec.workload` snapshot equals the desired workload. Re-verified:
    `Progressing` held through the rollout, then `Deployed`.
  - Port validation (DNS-label name, 1-65535, no duplicates) moved into
    `ComponentReconciler` and reports `InvalidSpec`.
  - Moving a Component to a different Application reports `Unsupported`
    (upstream `owner` is immutable).
  - The CRD download is skipped when all Gateway API CRDs already exist.
