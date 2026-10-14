# Application Platform

This document describes the Application Platform capability
(`capability-model.md:46`) as it exists today: spec 010's OpenChoreo
deployment foundation plus spec 011's real capability on top of it
(`Application`/`Component` resources, `ApplicationPlatformProvider`, two
reconcilers), and spec 012's external exposure through the Gateway
capability (`gateway.md`). ADR 0005 is the authoritative record for *why* OpenChoreo;
ADR 0010 for *why the foundation shells out to `helm`* (a per-capability
exception to ADR 0002/0009's direct-object-construction precedent).

Code: `providers/application/openchoreo/` (provider side),
`core/reconcile/application.go` (interface + reconcilers),
`core/resource/app.go` (resource kinds), `cmd/serendipd` `wireKubernetes`
and `setup.go` (wiring and background setup). Pinned versions: `providers/application/openchoreo/VERSIONS.md`.

## The capability (spec 011)

### Object chain

Everything OpenChoreo-side lives in one org namespace,
`openchoreo-control-plane`.

| Serendip | OpenChoreo | Created by |
|---|---|---|
| `Application` | `Project` + its one `ProjectReleaseBinding` (env `development`) | Serendip provider |
| `Component` | `Component` (`autoDeploy: true`, `componentType: deployment/serendip-service`) + `Workload` | Serendip provider |
| (none) | `ComponentRelease`, `ReleaseBinding` (`<component>-development`), `RenderedRelease` | OpenChoreo's own controllers |
| (none) | Deployment, Service, env ConfigMap | OpenChoreo, into the Project's cell namespace |

Mapping is 1:1 Application to Project. Each Project gets its own data-plane
"cell" namespace (OpenChoreo names it like
`dp-openchoreo-co-app-<hash>-development-<hash>` and truncates long names
itself, confirmed on a real cluster), so Applications are isolated from each
other, and deleting an Application is deleting one Project. Serendip's own
`Organization`/`Team`/`Project` kinds are not mapped to anything yet.

Provider calls (`Provider`, `provider.go`) only use
`providers/kubernetes.Client`: a server-side apply of each object under field
manager `serendip` (spec 014; `provider-model.md`), never helm. The API
server does the diff, so a no-drift reconcile stores nothing on either side,
and a field Serendip stops setting is removed.
`EnsureComponent` first GETs the Component: its owner is immutable, so if
it differs from the requested Application it returns `Unsupported` before
any apply. It then applies the Component CR, then the Workload. "Did I just
change the Workload?" is read from `metadata.generation` before and after the
apply; a change returns `Progressing` ("rolling out updated workload").
A port becomes a `TCP` endpoint, unless it sets `expose: true` (below).
Workload env is OpenChoreo's
`[{key,value}]` list, sorted by key; OpenChoreo delivers it via the type's
env-config ConfigMap and `envFrom`.

### Corrected upstream facts

Spec 010's earlier version of this doc listed an `Organization` CRD. That
was wrong. OpenChoreo 1.3.1 has **no Organization CRD**: a namespace labeled
`openchoreo.dev/control-plane=true` is the org boundary. Serendip reuses
`openchoreo-control-plane` as that namespace because `DataPlane/default`
already lives there and an Environment resolves its `dataPlaneRef` by name in
its own namespace. Cost: workload *definitions* (not the workloads, which run
in cell namespaces) share a namespace with the control plane's Deployments;
accepted at edge-1, revisit with multi-tenancy.

The control-plane chart seeds none of the Environment, DeploymentPipeline,
ClusterProjectType or ClusterComponentType objects; Serendip creates its own.

### Platform defaults

`EnsurePlatformDefaults` runs at platform setup and then on every foundations
re-ensure pass (`reconciliation.md`; default every 5m), so a default deleted
or edited by hand is restored. Each is server-side applied, except the
namespace label:

- Label on the org namespace (merge-patched only if missing; Helm created the
  namespace, Serendip owns one label).
- `ClusterProjectType/serendip-default`: its template creates the cell
  namespace. Copied from v1.3.1 `samples/getting-started/all.yaml`.
- `ClusterComponentType/serendip-service` (`workloadType: deployment`): a
  trimmed copy of the sample's `service` type that renders a Deployment, a
  ClusterIP Service, an env ConfigMap consumed via `envFrom`, and (spec 012)
  an `httproute-external` for each endpoint that is `HTTP` with `external`
  visibility. The route is the sample's, minus its CORS filter (outside the
  standard-channel Gateway API). Dropped: the sample's visibility validations,
  ExternalSecrets (external-secrets not installed; secret injection out of
  scope), file-mount config, command/args, and the workflow/trait
  allow-lists (nothing uses them). Kept: the "at least one endpoint"
  validation, which is why port-less Components are unsupported (below).
  **Update path:** the type is applied like every other default, so a
  Serendip release that changes it (spec 012 added the route) reaches existing
  clusters on the next setup or re-ensure pass, and a key removed from the
  desired spec is removed from the live type. Existing releases snapshot the
  type, so running Components are untouched until their next Workload change.
- `Environment/development` pointing at `DataPlane/default`.
- `DeploymentPipeline/serendip`, single environment. A pipeline with an empty
  `targetEnvironmentRefs` was confirmed accepted on a real cluster
  (`Available=True`).

The cluster-scoped types are `serendip-` prefixed so they cannot collide with
an operator who also applied OpenChoreo's own getting-started sample.

### Names

Cluster object names derive from the Serendip ID alone:
`app-<h>` (Project), `cmp-<h>` (Component, Workload), with `<h>` the first 10
hex chars of `sha256(ID)`. Two reasons. The reconciler's delete branch only
has the ID (and a cascade delete of an Application emits one delete event per
child Component). And the raw 36-char UUID plus the org namespace would blow
past Kubernetes' 63-char namespace limit in OpenChoreo's cell-namespace name.
`serendip.dev/id` and `serendip.dev/name` labels are for `kubectl` debugging
only; nothing reads them back.

### Ready conditions

Each kind has one `Ready` Condition with provider-neutral reasons (no product
vocabulary in `core/`; upstream detail goes in `Message`):

| Reason | Set by | Meaning |
|---|---|---|
| `Deployed` | provider | Upstream `Ready=True` (workload running / cell namespace ready) |
| `Progressing` | provider | Created, not ready yet |
| `DeployFailed` | provider | Upstream reports a failure that won't converge on its own |
| `Unsupported` | provider | Valid spec this provider can't run (no ports; moving a Component to another Application) |
| `InvalidSpec` | reconciler | Empty image, bad/duplicate port name, port outside 1-65535, no or multiple Application owner refs |
| `WaitingForApplication` | reconciler | Owning Application missing or not `Ready=True`; provider not called |
| `EnsureFailed` | reconciler | Provider returned an error (API call failed) |
| `ProviderNotReady` | reconciler | The platform's background setup has not finished (spec 014); provider not called, resource re-checked early |

Upstream mapping (`mapReady`): a Component reads the `ReleaseBinding` `Ready`
condition (a missing ReleaseBinding is `Progressing`, since autoDeploy has not
created it yet); an Application reads the `ProjectReleaseBinding` `Ready`
condition. `Ready=True` is `Deployed`. A reason in `failureReasons`
(`ResourceApplyFailed`, `RenderingFailed`, `ResourcesDegraded`,
`NamespaceDegraded`, `EnvironmentNotFound`, `DataPlaneNotFound`,
`DataPlaneNotConfigured`, `ProjectNotFound`, `ComponentNotFound`,
`InvalidReleaseConfiguration`, `ReleaseOwnershipConflict`,
`ReleaseUpdateFailed`, `JobFailed`) is `DeployFailed`. Anything else not-ready,
including `ResourcesProgressing`/`ResourcesNotReady`, is `Progressing`. Three
honesty rules keep an update from looking `Deployed` before it is:

- A `Ready` condition whose `observedGeneration` is older than the object's
  `metadata.generation` is `Progressing`, because it describes a stale spec.
- Right after an apply changes the Workload's generation, `EnsureComponent`
  returns `Progressing` without reading the ReleaseBinding.
- The ReleaseBinding's `Ready` is only trusted once the `ComponentRelease` it
  points at (`spec.releaseName`) has a `spec.workload` snapshot equal to the
  desired workload. Otherwise the result is `Progressing`.

The third rule is what actually closes the gap: the second rule alone wasn't
enough on a real cluster. The reconciler's own status write publishes an event
that re-runs the reconcile within milliseconds of the apply. OpenChoreo takes
about 80 ms to cut a new release and retarget the binding, so the re-run saw
the old release still `Ready` and briefly reported `Deployed`. Each Component
reconcile therefore makes one extra GET, for the bound ComponentRelease.

Reconcilers write only when `{Status, Reason}` changes (`Message` excluded,
since upstream text churns), so an unchanged Component produces no `Update`,
audit row, or `ResourceVersion` bump.

Convergence is not left to the 30s resync (spec 014). A Component that is
`Progressing`, `WaitingForApplication`, `ProviderNotReady`, or Ready with
`Reachable=Pending` (and an Application that is `Progressing` or
`ProviderNotReady`) makes its reconciler return `RequeueAfter`, after the
status write, and the engine re-checks it on its own 2s-doubling backoff.
Settled and terminal reasons ride the normal resync. See `reconciliation.md`.

Both reconcilers' not-found branches call `Delete*` with an ID-only resource.
Delete issues a DELETE on the Project/Component (404 is success) and returns;
OpenChoreo's finalizers do the teardown asynchronously.

### External exposure (spec 012)

`ComponentPort.Expose` (default false) maps to a Workload endpoint
`{type: HTTP, visibility: [external]}`; other ports stay `TCP` as before.
`EnsureDataPlane` (in `dataplane.go`) applies `DataPlane/default` including
`spec.gateway.ingress.external`, the `GatewayEndpoint` that `cmd/serendipd`
hands over from the Gateway capability. `EnsureComponent` returns a
`ComponentObservation`; when a port is exposed and the workload is Deployed it
also finds the rendered HTTPRoutes (by `openchoreo.dev/component` and
`endpoint-visibility=external` labels, in the cell namespace from
`ProjectReleaseBinding.status.namespace`), checks each route's parent status
for the shared Gateway, and reads the URL OpenChoreo reports. Reasons, URL
publication rules, the NetworkPolicy/proxy-label finding and the
`externalURLs` finding are in `gateway.md`; OpenChoreo 1.3.1 reports
`ReleaseBinding.status.endpoints[].externalURLs.http {scheme,host,port,path}`
rather than `invokeURL`, so the provider assembles the URL from those parts.
A Component with nothing exposed makes no extra API calls and has no
`Reachable` Condition.

### Gateway API CRDs prerequisite (real-cluster finding)

OpenChoreo's RenderedRelease controller lists Gateway and HTTPRoute objects in
the data plane on every reconcile, and fails every reconcile with "not found"
when those CRDs are missing, **even with `gateway.enabled=false` on both
charts**. Result: no release ever reported status and nothing became Ready.

`EnsureGatewayAPICRDs` is a one-line delegate to
`providers/internal/gatewayapi.EnsureCRDs`, shared with the kgateway provider.
It runs as part of the gateway's setup (the kgateway `Deployer`) and the
gateway foundations re-ensure, both of which come before the platform's.
Spec 011 pinned v1.3.0 and never touched existing CRDs; that is **superseded**:
the pin is now **v1.6.1** (forced by kgateway 2.4.x, ADR 0011) with
**upgrade-only** semantics. Older CRDs are upgraded in place by bundle-version
annotation, equal or newer are left alone, and upstream's `safe-upgrades`
admission policy is installed too. Details in `gateway.md`.

**Known limitation:** a fresh install (or an upgrade) needs network access to
github.com; air-gapped installs need the manifest embedded or mirrored later.

### Observed rollout semantics

During an image update the old Pod keeps serving. Serendip applies the
Workload once and reports `Progressing` until the ReleaseBinding is bound to a
release cut from the new spec and is `Ready`, then `Deployed`. In the spec 011
verification run, `Progressing` held from the update until the new Pod was
serving, about 29s (one resync); since spec 014 it is re-checked early, so
status trails the upstream change by seconds.

## Deployment foundation (spec 010)

### Deployment model

Three Helm releases installed in order by `Deployer.Ensure` via the real
`helm` CLI (ADR 0010), each with `--wait` and `--create-namespace`:

1. **cert-manager** (`oci://quay.io/jetstack/charts/cert-manager`, `1.21.2`,
   `--set crds.enabled=true`). A real prerequisite: neither OpenChoreo chart
   bundles a cert-manager controller, only objects that assume one.
2. **OpenChoreo control plane** (`oci://ghcr.io/openchoreo/helm-charts/
   openchoreo-control-plane`, `1.3.1`, ns `openchoreo-control-plane`).
3. **OpenChoreo data plane** (`.../openchoreo-data-plane`, `1.3.1`, ns
   `openchoreo-data-plane`).

Control and data plane are co-located on one cluster (an officially supported
topology). Readiness is Helm's own `--wait`; `helm upgrade --install` is
idempotent so there is no separate GET-then-install. `NewDeployer` fails fast
if `helm` is not on `$PATH`. The Deployments verified ready are
`cluster-gateway`, `controller-manager`, `event-forwarder`, `openchoreo-api`
and the data plane's `cluster-agent-dataplane` (bare names, no release prefix).

### Disabled-by-default feature set

Each disabled flag turns off a dependency deferred rather than removed:

- `security.enabled=false` (control plane). The default hard-fails validation
  on the placeholder issuer `http://thunder.openchoreo.invalid`. Thunder is
  not a prerequisite; real auth is a future Identity spec.
- `backstage.enabled=false` (control plane). Needs credentials and real
  hostnames; the developer-portal UI is deferred (no consumer).
- `gateway.enabled=false` (both charts) plus `gateway.tls.enabled=false` (data
  plane). The data-plane chart's hostname check is gated on `gateway.tls.enabled`
  alone, found on a real cluster. The charts' own gateway stays off because
  Serendip creates the shared Gateway itself (`gateway.md`). (OpenChoreo's
  internal `clusterGateway` is separate and always on.)

### `DataPlane` CR and CA-trust bootstrapping

`EnsureDataPlane` applies `DataPlane/default` in `openchoreo-control-plane`
as one object: `spec.clusterAgent.clientCA.secretKeyRef` pointing at
`cluster-agent-tls` / `ca.crt` in `openchoreo-data-plane`, plus the gateway
wiring (spec 012). It is built directly with `providers/kubernetes.Client`
rather than by shelling out to OpenChoreo's `add-default-dataplane.sh`. (Until
spec 014 this was two calls, a create and a gateway merge patch.)

The control-plane chart promises a "CA extractor hook job" to populate the
data plane's trust ConfigMap; no such job exists (the only Job is the unrelated
authz bootstrap), and the ConfigMap it ships holds a permanent placeholder. The
real CA sits in a `cluster-gateway-ca` Secret the chart never reads.
`Deployer.ensureDataPlaneTrustsControlPlane`, run between the two chart
installs, copies it from that Secret into the data plane's
`cluster-gateway-ca` ConfigMap (applied on every setup, so a rotated CA is
carried over). Confirmed: the agent logged "connected to
control plane" and `DataPlane.status.agentConnection` showed
`connected: true, connectedAgents: 1`.

### `CheckReady`

A second confirmation after Helm's `--wait`: GETs each expected Deployment and
requires `readyReplicas >= 1`, retrying 3 times 2s apart (a `go-reviewer`
finding: its error propagates to `os.Exit(1)`, so one informer-cache blip must
not kill the daemon).

### Startup wiring

`wireKubernetes` (skipped entirely when `SERENDIP_KUBECONFIG` is unset)
registers the Application and Component reconcilers with the engine at
startup, against a lazy `ApplicationPlatformProvider` handle that returns
`ErrProviderNotReady` until setup finishes (spec 014; `reconciliation.md`).
The platform's setup step runs in the background once the gateway's has
succeeded, with the gateway's `GatewayEndpoint`: `Deployer.Ensure`, then
`EnsureDataPlane` and `EnsurePlatformDefaults`, then `CheckReady`. On
success it registers `openchoreo.NewProvider(client, gateway)` under
`"application-platform"` and hands it to the lazy handle; on failure it is
retried (5s doubling to 2m) and reported by `GET /v1/readiness`. The
DataPlane and the defaults are re-ensured afterwards on the foundations
interval.

## Not done / deferred

- **Port-less (worker) Components.** The `serendip-service` type requires an
  endpoint, and `componentType` is immutable upstream, so supporting both would
  need delete-and-recreate. Reported as `Unsupported`.
- **Non-external visibility** (`internal`/`namespace`), TLS, custom domains:
  see `gateway.md`. Only `external` HTTP exposure exists.
- ~~Retrying a failed DELETE after the Serendip record is gone~~: fixed after
  spec 012. The reconcile engine now remembers refs whose `Reconcile`
  failed and retries them on every resync until they succeed, so a teardown
  that fails while the cluster is unreachable completes once it's back.
  Verified on a real cluster with the node stopped during the delete.
- **Mapping Serendip Organization/Project onto OpenChoreo namespaces**
  (multi-tenancy; one fixed org namespace today).
- **Secret injection** into Components (spec 009 `Secret`s are not wired in).
- **Multi-environment promotion** (one `development` Environment, a
  single-environment pipeline).
- ~~Live reconciliation of platform defaults~~ and ~~status lag of up to
  ~30s~~: fixed in spec 014 (foundations re-ensure loop; early re-checks
  while converging). See `reconciliation.md`.
- **Watching upstream objects** instead of re-checking them: status still
  trails OpenChoreo by up to one early re-check step (about 2s at the
  observed rollout times). Out of scope for edge-1 (spec 014).
- **Build Plane, Observability Plane, Thunder** (ADR 0005 and prior scope).
  kgateway is deployed by the Gateway capability (`gateway.md`), not here.

## Real-cluster verification (spec 011, kind, Kubernetes 1.34.0)

- Application `Ready` about 30s after create; Component `Progressing` then
  `Deployed` in about 30s.
- Real nginx Pod 1/1 Running, env var delivered, HTTP served.
- Idempotency held across 2 resyncs: Component `ResourceVersion`, audit rows
  and Workload generation unchanged.
- Image update patched the Workload once and rolled out.
- Component delete removed the Pod, Service, ConfigMap and all OpenChoreo
  objects in about 7s; Application cascade delete removed the Project and
  cell namespace in about 20s.
- Footprint: see `profiles/edge-1/bench/002-edge1-footprint.md` (spec 013,
  simulated T470, two runs). OpenChoreo measured about 55–58 MiB for the
  control plane and about 7 MiB for the data plane, and the whole edge-1
  stack about 1.3 GiB. This is still **not** ADR 0005's reference-hardware
  gate.

## Real-cluster verification (spec 014, kind, Kubernetes 1.34.0)

- The spec 011/012 flow (deploy, expose, curl 200, image update, un-expose,
  re-expose, delete, cascade) passed after the server-side apply conversion;
  un-exposing left no `visibility` field on the Workload.
- Component create to `Deployed`/`Serving` with a cached image: 14.2s in 4 of
  4 runs, against OpenChoreo's own ReleaseBinding `Ready` at 11-13s (was a
  flat 30s in spec 013).
- Idle over 75s: generation and `serendip` apply time unchanged on all 20
  owned objects, no Component write, no audit rows.
- A force-deleted `Environment` was recreated in 12s by the foundations loop
  (`SERENDIP_REENSURE_INTERVAL=30s`). A plain delete never completes:
  OpenChoreo blocks it while the DeploymentPipeline references it.
- A label applied under manager `serendip` was removed on the next re-ensure;
  a label set by another manager survived.

## Open questions

- ADR 0005's T470 footprint benchmark gate is still unmet (above).
- CNCF Sandbox governance / WSO2 commit concentration: standing watch item
  per ADR 0005.
- Air-gapped Gateway API CRD source (embed vs mirror).
