# Plan: Spec 010 — OpenChoreo deployment foundation

Status: Implemented. **Three real corrections found during T006
real-cluster verification, same "plan.md is a hypothesis" discipline
specs 007/008/009 already proved necessary**:

1. **A Kubernetes CEL-compiler bug, not an OpenChoreo bug.** The
   control-plane chart's `ClusterAuthzRoleBinding` CRD failed to
   install on `kind`'s default node image (Kubernetes 1.31.0) with
   `compilation failed: ... undefined field 'namespace'`, even though
   the CRD's schema genuinely declares that field — confirmed
   byte-for-byte identical across OpenChoreo v1.1.8/v1.2.7/v1.3.1 via
   `helm pull`, and reproduced with plain `kubectl apply
   --dry-run=server`, bypassing Helm entirely. Decisively isolated by
   testing the *exact same* CRD file against a `kind` cluster on
   Kubernetes 1.34.0: it applied cleanly, no error. This is a
   Kubernetes-side CEL limitation on older minor versions, not an
   OpenChoreo schema defect — and irrelevant to this project's real
   target anyway, since K3s is already pinned to `v1.36.4+k3s1`
   (spec 005) — only `kind`'s own *default* node image (unrelated to
   any version this project runs) happened to land on the affected
   range. Fix: real-cluster verification (T006) now explicitly uses
   `kind --image kindest/node:v1.34.0` or newer, not `kind`'s default.
   No code change was needed or made for this one.
2. **The data-plane chart needs `gateway.tls.enabled=false` set
   explicitly, not just `gateway.enabled=false`.** The control-plane
   chart's placeholder-domain validation is gated on "`gateway.enabled`
   AND `gateway.tls.enabled`" together (confirmed in its
   `_helpers.tpl`), but the data-plane chart's equivalent
   `validateHostnames` check is gated on `gateway.tls.enabled` alone,
   independent of `gateway.enabled` — so the control-plane-chart
   pattern alone left the data-plane chart's default
   `gateway.tls.hostname` (`"*.openchoreoapis.invalid"`) still tripping
   its own check. Fixed in `Deployer.Ensure`'s data-plane `helm`
   invocation.
3. **A genuine, confirmed upstream gap: no CA-extractor mechanism
   exists anywhere in the chart**, despite the chart's own comment
   promising one. `templates/cluster-gateway/
   ca-configmap-placeholder.yaml` creates a `cluster-gateway-ca`
   ConfigMap containing a literal, permanent placeholder string ("will
   be replaced by actual CA certificate... The CA extractor hook job
   will populate it"), but grepping every template in the chart for
   `kind: Job` finds exactly one Job — the unrelated authz bootstrap
   job — and `controller-manager`'s own logs show zero CA-sync
   activity. The real, cert-manager-issued CA sits the whole time in a
   `cluster-gateway-ca` *Secret* (type `kubernetes.io/tls`) the chart
   never reads back out. Separately, and independent of that chart
   bug: this ConfigMap is namespace-scoped and the control-plane chart
   only ever creates it in its own namespace — a genuine multi-cluster
   install would copy it across clusters by hand, but single-cluster
   co-location doesn't make a same-cluster, different-namespace
   ConfigMap visible for free either. Fixed with a new
   `Deployer.ensureDataPlaneTrustsControlPlane` method, inserted
   between the control-plane and data-plane `helm` calls: ensures the
   data-plane namespace exists, reads the real CA directly out of the
   control plane's `cluster-gateway-ca` *Secret* (base64-decoded by
   `encoding/json`'s native `[]byte` handling), and creates the
   data-plane's own `cluster-gateway-ca` *ConfigMap* with that real
   value — bypassing the chart's broken placeholder mechanism entirely
   rather than waiting on something that doesn't exist. Confirmed
   working end-to-end: the data plane's `cluster-agent` logged "server
   CA certificate loaded successfully" and "connected to control
   plane", and the `DataPlane` CR's own `status.agentConnection` showed
   `connected: true, connectedAgents: 1`.

Also corrected, found in the same T006 pass: the real control-plane
Deployment names have **no Helm-release-name prefix at all** (bare
`cluster-gateway`, `controller-manager`, `event-forwarder`,
`openchoreo-api`, not `openchoreo-control-plane-cluster-gateway` etc.
as originally guessed) — `readiness.go`'s `expectedDeployments` is
fixed to the real, confirmed names.

**Scope correction vs. spec.md, confirmed by pulling and inspecting the
real v1.3.1 chart artifacts directly (`helm pull ... --untar`), not by
reading docs** — this plan.md resolves several things spec.md's own
oss-researcher pass got wrong or left open, the same "verify against
the real artifact, not docs" discipline specs 007/008 already proved
necessary twice:

1. **cert-manager is NOT bundled by the control-plane chart.** Neither
   chart's `templates/` directory contains a single cert-manager
   controller/webhook/CRD template — only `Certificate`/`Issuer`
   *objects* (`cert-manager.io/v1`) that assume a real cert-manager
   controller is already running. **This spec now deploys cert-manager
   itself**, via its own official OCI chart
   (`oci://quay.io/jetstack/charts/cert-manager`, confirmed reachable
   and pinned to `1.21.2`, the current latest — confirmed via `helm
   pull`), `--set crds.enabled=true` (the current non-deprecated flag;
   `installCRDs` still works but is marked deprecated in this chart
   version). Per the user's explicit direction: this is a POC on a
   32GB/good-CPU dev machine — don't under-scope a genuinely required
   piece to save footprint that doesn't matter here. The T470
   footprint question stays open exactly where ADR 0005 already left
   it (a real benchmark gate, not something this spec resolves).
2. **Thunder is not a hard pre-req, confirmed via the chart's own
   validation logic, not inference.** `templates/validate.yaml`
   (control-plane chart) hard-fails `helm template`/`install` if
   `security.enabled: true` (the default) and `security.oidc.issuer`
   still contains the placeholder domain `.invalid` (the default value
   is literally `http://thunder.openchoreo.invalid`). Setting
   `security.enabled=false` passes validation cleanly and is a
   supported, first-class toggle (not a hack) — same "intentionally
   weak, temporary, documented" pattern this codebase already uses for
   `SERENDIP_API_KEYS` (`api/middleware.go`). Real Thunder-backed auth
   is a future Identity spec's job, not this one's.
3. **kgateway is not a hard pre-req either, confirmed the same way.**
   `gateway.enabled: true` + `gateway.tls.enabled: true` +
   `gateway.tls.hostname: "*.openchoreo.invalid"` (control plane) /
   `"*.openchoreoapis.invalid"` (data plane) are *also* checked by the
   same placeholder-domain validation and *also* default to a
   `.invalid` hostname. This `gateway` block creates Gateway API
   `Gateway` objects referencing `gatewayClassName: "kgateway"` for
   **application traffic routing** — a data-plane/runtime concern with
   nothing to route to yet (no Component exists). Setting
   `gateway.enabled=false` on both charts passes validation and defers
   kgateway entirely to whichever future spec actually deploys a
   workload. (OpenChoreo's own internal `clusterGateway` — the
   control-plane↔data-plane agent connection — is a separate,
   always-on, self-contained component with cluster-internal DNS names
   that never hit the placeholder check; it is not kgateway and this
   spec does not touch its defaults.)
4. **`backstage.enabled: true` by default and additionally requires a
   credentials Secret** (`backend-secret`, `client-secret`,
   `jenkins-api-key` keys) and non-placeholder hostnames/baseUrl —
   none of which this spec can meaningfully provide (Backstage is a
   developer-portal UI with nothing to show until `Project`/
   `Component` CRs exist, a later spec's job). `backstage.enabled=false`
   — deferred for the same "no consumer yet" reason as the Build
   Plane, not a resource-footprint decision.
5. **The control-plane chart has six Deployments**, not the two
   `spec.md` assumed (`controller-manager`, `api-server`): `backstage`,
   `cluster-gateway`, `controller-manager`, `event-forwarder`,
   `openchoreo-api`, `portal-assistant`. With `backstage`/`gateway`
   disabled per points 3-4, the Deployments actually running this
   spec needs to verify ready are: `cluster-gateway`,
   `controller-manager`, `event-forwarder`, `openchoreo-api`, plus an
   `authz` bootstrap `Job` (Helm hook, runs once, completes, isn't a
   Deployment at all — `templates/authz/bootstrap-job.yaml`). The
   data-plane chart's only real Deployment (with `gateway` disabled)
   is `cluster-agent`.
6. **Readiness is Helm's own job, not a hand-rolled poll loop.** `helm
   upgrade --install ... --wait --timeout <N>` blocks until every
   Deployment/Job/StatefulSet the chart creates reports ready/complete,
   using Helm's own well-tested logic — a real simplification versus
   spec 008's hand-rolled `StatefulSet.status.readyReplicas` polling
   loop (`providers/secrets/openbao/deploy.go`'s `waitStatefulSetReady`),
   which existed specifically because that spec built Kubernetes
   objects directly with no Helm release to `--wait` on. This spec's
   Go code does not need an equivalent poll loop for the Helm-installed
   resources; it only needs to shell out with `--wait` and surface a
   clear error on timeout/failure.
7. **The `DataPlane` CR's real shape**, pulled directly from the
   installed CRD (`crds/openchoreo.dev_dataplanes.yaml`, `v1alpha1`):
   `spec.clusterAgent.clientCA.secretKeyRef` is the only genuinely
   required nested field beyond the top-level `clusterAgent` object
   itself. Tracing the data-plane chart's own cert flow
   (`clusterAgent.tls.generateCerts: true` by default →  a self-signed
   `Issuer` → a `Certificate` into Secret `cluster-agent-tls` in the
   `openchoreo-data-plane` namespace, containing `ca.crt`/`tls.crt`/
   `tls.key`) gives the exact reference this spec's `DataPlane` CR
   needs:
   ```json
   {
     "apiVersion": "openchoreo.dev/v1alpha1",
     "kind": "DataPlane",
     "metadata": {"name": "default", "namespace": "openchoreo-control-plane"},
     "spec": {
       "clusterAgent": {
         "clientCA": {
           "secretKeyRef": {
             "name": "cluster-agent-tls",
             "namespace": "openchoreo-data-plane",
             "key": "ca.crt"
           }
         }
       }
     }
   }
   ```
   This is constructed directly via the existing
   `providers/kubernetes.Client` (spec 006), **not** by shelling out to
   the project's own `add-default-dataplane.sh` (spec.md's original
   assumption) — once the CRD schema and the cert Secret's real
   name/namespace/key were traced directly from the chart artifacts,
   hand-construction turned out to be exactly as reliable as the
   script and avoids adding a second external-script dependency beyond
   `helm` itself. **The CR's own namespace (`openchoreo-control-plane`
   assumed above) is now confirmed correct against a real cluster**
   (T006): the control plane's `controller-manager` accepted it,
   updated `status.conditions` to `Created: True`, and the resulting
   `DataPlane` successfully tracked the data plane's cluster-agent
   connecting (`status.agentConnection.connected: true`).

## Open-question resolutions

1. **Version pin: OpenChoreo 1.3.1, cert-manager 1.21.2.** Both
   confirmed as real, currently-pullable OCI chart versions (not
   assumed from a docs page) via `helm pull`. 1.3.1 is OpenChoreo's
   newest release line and already includes both the CVE-2026-73843
   fix and the under-provisioned-default-limits fixes spec.md flagged
   as a watch item — no reason found to prefer the GitHub-"Latest"-
   tagged 1.1.8 line instead.
2. **CRD surface**: the real list is the 36 files under
   `openchoreo-control-plane/crds/` (confirmed by directly listing the
   pulled chart, not guessed) — `DataPlane`, `ClusterDataPlane`,
   `Project`, `Component`, `Environment`, `BuildPlane`/
   `ClusterBuildPlane`/`ClusterWorkflowPlane`, `ObservabilityPlane`/
   `ClusterObservabilityPlane`, `ComponentType`/`ClusterComponentType`,
   `Trait`/`ClusterTrait`, `Workflow`/`ClusterWorkflow`/`WorkflowRun`,
   `ProjectType`/`ClusterProjectType`, `ResourceType`/
   `ClusterResourceType`, `Resource`, `AuthzRole`/`ClusterAuthzRole`/
   `AuthzRoleBinding`/`ClusterAuthzRoleBinding`,
   `ObservabilityAlertsNotificationChannel`, `ProjectRelease`/
   `ProjectReleaseBinding`, `ResourceRelease`/`ResourceReleaseBinding`,
   `RenderedRelease`, `DeploymentPipeline` is **not** among them —
   spec.md's uncertainty about it is resolved: it does not exist as a
   standalone CRD kind in 1.3.1. This spec only ever creates/reads
   `DataPlane`; the rest is listed here only so a future spec doesn't
   have to re-derive it from scratch.
3. **Readiness-check mechanism: Helm's own `--wait`/`--timeout`**, not
   a hand-rolled poll loop — see resolution 6 above. A post-install
   sanity check (confirm the known Deployments report
   `status.readyReplicas >= 1` via the existing
   `providers/kubernetes.Client`, mirroring the read-only half of spec
   008's pattern without the polling loop) adds a second,
   independent confirmation beyond trusting Helm's exit code, cheap to
   include.
4. **Namespace creation: `--create-namespace` on every `helm upgrade
   --install` call** (control-plane → `openchoreo-control-plane`,
   data-plane → `openchoreo-data-plane`, cert-manager →
   `cert-manager`) — Helm's own flag, not a separate `providers/
   kubernetes.Client` namespace-ensure call like specs 007/008 used,
   since these namespaces aren't shared with anything else the way
   `serendip-system` is.

## Approach

1. **`providers/application/openchoreo/helm.go`** (new): a small
   `runHelm(ctx, args ...string) error` wrapper around
   `os/exec.CommandContext("helm", args...)` — checks `helm` is on
   `$PATH` once at construction (clear error naming it as a required
   host prerequisite, not a Go dependency, if missing), captures
   combined stdout/stderr into the returned error on non-zero exit.
   Kept minimal and local to this package (not a shared `providers/
   helm` package) — constitution rule 10: no second consumer exists
   yet to justify the abstraction; extract later if one appears.
2. **`providers/application/openchoreo/deploy.go`** (new): `Deployer`
   struct (mirrors `openbao.Deployer`'s naming), `NewDeployer`,
   `Ensure(ctx) error` — runs, in order: cert-manager's `helm upgrade
   --install` (`--wait --timeout 5m`), the control-plane chart's
   (`--set security.enabled=false --set backstage.enabled=false --set
   gateway.enabled=false --wait --timeout 10m`), the data-plane
   chart's (`--set gateway.enabled=false --wait --timeout 10m`), each
   step's error wrapped naming which release failed. `helm upgrade
   --install` is itself idempotent (a second `Ensure` call is a cheap
   no-op against an already-installed release at the same version) —
   no separate GET-then-install check needed, unlike spec 007/008's
   hand-built-object pattern.
3. **`providers/application/openchoreo/dataplane.go`** (new):
   `EnsureDefaultDataPlane(ctx, client *kubernetes.Client) error` —
   idempotent GET-then-Create (same pattern as every prior provider)
   of the `DataPlane` CR from resolution 7 above, via the generic
   `providers/kubernetes.Client.Get`/`Create` (no new client methods
   needed — this is a plain custom-resource POST, same shape as
   spec 007/008's Namespace/ConfigMap/PVC calls).
4. **`providers/application/openchoreo/readiness.go`** (new):
   `CheckReady(ctx, client *kubernetes.Client) error` — GETs the known
   Deployments (`cluster-gateway`, `controller-manager`,
   `event-forwarder`, `openchoreo-api` in `openchoreo-control-plane`;
   `cluster-agent` in `openchoreo-data-plane` — exact Helm-release-
   name-prefixed names confirmed during T-whichever implementation
   task against the real installed chart, not assumed verbatim here)
   and confirms `status.readyReplicas >= 1` on each, for the reasons
   in resolution 3.
5. **`cmd/serendipd/main.go`**: new `wireOpenChoreo(ctx, client,
   registry) error`, called from `wireKubernetes` alongside
   `wireOpenBao` (after it, since OpenChoreo has no dependency on
   Secrets, but keeping Kubernetes-facing wiring calls in one visible
   block) — runs `Deployer.Ensure`, `EnsureDefaultDataPlane`,
   `CheckReady`, then `registry.Register("application-platform",
   <marker value>)`. No resource kind exists yet for this registration
   to back — the registered value is a placeholder satisfying a
   minimal marker interface (e.g. a one-method `Reachable(ctx) error`
   or similar), reachable for a direct smoke-test only, same spirit as
   every prior foundation spec's registration.

## Interfaces / types to create

```go
// providers/application/openchoreo/deploy.go
type Deployer struct { /* unexported helm-invocation config */ }
func NewDeployer() (*Deployer, error) // errors if `helm` isn't on $PATH
func (d *Deployer) Ensure(ctx context.Context) error

// providers/application/openchoreo/dataplane.go
func EnsureDefaultDataPlane(ctx context.Context, client *kubernetes.Client) error

// providers/application/openchoreo/readiness.go
func CheckReady(ctx context.Context, client *kubernetes.Client) error
```

No `core/` changes at all — no resource kind, no capability-shaped
interface, no Reconciler. This spec is pure `providers/` +
`cmd/serendipd` wiring, matching specs 006/008's foundation-stage
precedent exactly.

## Files touched

- `providers/application/openchoreo/helm.go` (new)
- `providers/application/openchoreo/helm_test.go` (new)
- `providers/application/openchoreo/deploy.go` (new)
- `providers/application/openchoreo/deploy_test.go` (new)
- `providers/application/openchoreo/dataplane.go` (new)
- `providers/application/openchoreo/dataplane_test.go` (new)
- `providers/application/openchoreo/readiness.go` (new)
- `providers/application/openchoreo/readiness_test.go` (new)
- `cmd/serendipd/main.go` (edit: new `wireOpenChoreo`, called from
  `wireKubernetes`)
- `docs/architecture/` — a new `application-platform.md` (mirrors
  `storage.md`/`secrets.md`'s pattern — this capability has no
  dedicated architecture doc yet) or an addition to an existing one;
  decide exact placement during T009-equivalent docs-sync, matching
  spec 008's own "decide in plan.md vs. defer" precedent for where new
  docs land.

## Risks

- **`helm` as a new host-level external tool dependency.** Unlike
  `kubectl`-equivalent raw API calls (this codebase's norm since spec
  006), this is the first time `serendipd`'s host environment needs a
  specific CLI tool beyond Go itself. Mitigated the same way spec
  005's `get.k3s.io` shell-out already established the precedent for
  "shell out to official tooling rather than reimplement it" — `helm`
  availability is checked explicitly at `NewDeployer` construction
  time with a clear error, not discovered as a confusing failure deep
  into `Ensure`.
- **Three sequential Helm installs, each with its own `--timeout`,
  mean `Ensure` can legitimately take several minutes on first run**
  (image pulls for ~6 real Deployments plus cert-manager plus the
  data-plane agent, well beyond spec 008's single-StatefulSet case).
  `wireKubernetes`/`cmd/serendipd` startup already tolerates spec
  008's OpenBao wait; this is the same category of cost, just larger
  in absolute time. Not mitigated further here — a genuine, accepted
  cost of this capability's real footprint, consistent with the
  user's own "don't under-scope for hardware reasons on this machine"
  direction.
- ~~Exact Helm-release-name-prefixed Deployment names for the
  readiness check~~ — **resolved by T006**: there is no release-name
  prefix at all; `readiness.go` is fixed to the real, confirmed bare
  names (`cluster-gateway`, `controller-manager`, `event-forwarder`,
  `openchoreo-api`, `cluster-agent-dataplane`).
- ~~The `DataPlane` CR's own namespace~~ — **confirmed correct by
  T006**: `openchoreo-control-plane` is accepted by the real
  `controller-manager`, which updated `status.conditions` to
  `Created: True` and tracked the agent connecting successfully.
- **Real-cluster behavior did again invalidate part of this plan** —
  three times in this one T006 pass (the K8s-version-specific CEL
  bug, the data-plane's independent `gateway.tls.enabled` gate, and
  the genuinely-missing CA-extractor mechanism), confirming the
  pattern every prior capability spec in this project has already
  hit at least once. All three are now fixed and verified end-to-end
  against a real cluster — see the correction block at the top of
  this file.

## Explicitly NOT done in this spec

- **The `Application`/`Component` resource kinds, any
  `ApplicationPlatformProvider` core interface, and any reconciler** —
  a later spec's job, same split as specs 006→007 and 008→009.
- **Thunder, kgateway, the Build Plane, the Observability Plane** —
  all explicitly deferred (resolutions 2-4 above, and ADR 0005's own
  Build Plane mitigation); this spec gets the control plane + data
  plane running with those features turned off, not removed from the
  roadmap.
- **Backstage (the developer portal UI)** — disabled for this spec;
  revisit once `Project`/`Component` CRs exist and there's something
  for it to actually show.
- **Satisfying ADR 0005's T470 benchmark gate** — needs real reference
  hardware; this spec's real-cluster verification produces a current
  dev-machine data point, not the final benchmark.
- **Any change to `api/`** — pure `providers/` + `cmd/serendipd`
  wiring; no new resource kind exists yet for `api/` to expose.
