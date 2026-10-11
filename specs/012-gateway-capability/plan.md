# Plan: Spec 012 — Gateway capability

Status: Draft — human approval required before implementation

Upstream facts come from two oss-researcher passes on 2026-10-11: one on
kgateway, one on OpenChoreo v1.3.1's gateway wiring (Go types,
releasebinding controller, sample component type, data-plane chart).
Items marked **(unconfirmed)** are settled in T009 on a real cluster, and
the code is corrected there if they turn out wrong. That's the same "plan.md
is a hypothesis" discipline specs 007–011 all needed.

## Upstream facts this plan rests on

**kgateway**
- CNCF Sandbox since 2025-03, Apache-2.0, Go control plane + Envoy data
  plane. Minor releases every 2–3 months; two minors patched in parallel.
- Version matrix:

  | kgateway | Kubernetes | Gateway API |
  |---|---|---|
  | 2.4.x (latest 2.4.6, 2026-10-06) | 1.32–1.36 | 1.4–1.6 |
  | 2.3.x | 1.31–1.35 | 1.3–1.5 |

- Install via two OCI Helm charts in `kgateway-system`:
  `oci://cr.kgateway.dev/kgateway-dev/charts/kgateway-crds`, then
  `.../kgateway`. The main chart creates GatewayClass `kgateway`.
- Experimental Gateway API features are **on by default**
  (`KGW_ENABLE_EXPERIMENTAL_GATEWAY_API_FEATURES`). We install
  standard-channel CRDs only, so we turn this off.
- Each Gateway gets its own Envoy Deployment + Service. The Service is
  type LoadBalancer by default. The `GatewayParameters` CRD
  (`gateway.kgateway.dev/v1alpha1`) changes that via
  `spec.kube.service.type: NodePort` and `ports[].nodePort`. How a Gateway
  links to its GatewayParameters (`spec.infrastructure.parametersRef`) is
  **(unconfirmed)**.
- The docs publish no footprint numbers. We measure it ourselves (T009).

**K3s and kind**
- Spec 005 left K3s's Traefik running, and its ServiceLB pods hold host
  ports 80/443. A second LoadBalancer Service on port 80 stays Pending.
- Other ports, and NodePorts, coexist fine.
- kind has no LoadBalancer implementation. NodePort plus `extraPortMappings`
  (with `containerPort` equal to the nodePort) is the standard way to reach
  a Service from the host.

**OpenChoreo 1.3.1**
- `DataPlane.spec.gateway` and `Environment.spec.gateway` share the type
  `GatewaySpec`:

  ```
  ingress.external: {name, namespace, http: {port, host, listenerName?}, https?, tls?}
  ```

  - Both are **mutable**.
  - The ReleaseBinding controller **watches DataPlane** (but not
    Environment), so editing the DataPlane triggers a re-render.
  - If the Environment has no gateway block, the DataPlane's is used.
- A Workload endpoint with `visibility: [external]` **and** `type: HTTP`
  makes the sample `service` component type render an HTTPRoute:
  - `parentRefs` points at `gateway.ingress.external.{name,namespace}`.
  - `hostnames` is `<environment>-<orgNamespace>.<http.host>`.
  - The path match is `PathPrefix /<component>-<endpoint>`, rewritten to
    the endpoint's base path.
  - TCP-typed endpoints never get a route.
  - The sample also adds a CORS filter. That needs newer, experimental
    Gateway API support, and is dropped here.
- OpenChoreo already computes the public URL in
  **`ReleaseBinding.status.endpoints[].invokeURL`** (and
  `externalURLs.http`):
  - host comes from the route's `hostnames[0]`, path from the route
    match, port from the gateway spec's `http.port`.
  - With empty `hostnames`, no URL is reported.
- A Gateway in another namespace works, provided its listener sets
  `allowedRoutes.namespaces.from: All`. No ReferenceGrant is needed.
- The data-plane chart's own `gateway.enabled=true` would create a
  `gateway-default` Gateway plus a kgateway `HTTPListenerPolicy`. It
  doesn't install kgateway, and saves almost nothing over creating the
  Gateway ourselves.

## Resolved design decisions

### 1. kgateway 2.4.6, and bump the pinned Gateway API CRDs to v1.6.1

- **Why not 2.3.x:** keeping spec 011's v1.3.0 CRDs would force kgateway
  2.3.x, which:
  - is untested on our real K3s pin (1.36);
  - drops out of patch support when 2.5 goes GA.
- **What changes in spec 011's code:**
  - `EnsureGatewayAPICRDs` moves its pin to **Gateway API v1.6.1**
    standard-install. Exact sha256 and CRD list are taken from the real
    release asset in T003, not guessed. If v1.6.1 doesn't exist, we use
    the latest 1.6.x.
  - Its "never touch existing CRDs" rule becomes **upgrade-only**:
    - A CRD whose `gateway.networking.k8s.io/bundle-version` annotation is
      older than the pin is updated to the pinned definition with a merge
      patch.
    - Newer versions are never downgraded, so a cluster with its own newer
      gateway setup keeps it.
    - The download-skip check becomes "all pinned CRDs present at ≥ the
      pinned version".
  - This is what makes clusters already set up under spec 011 (holding
    v1.3.0 CRDs) work, which acceptance criterion 7 requires us to handle.
- **OpenChoreo compatibility:** OpenChoreo only renders `v1` HTTPRoutes and
  needs Gateway API ≥ 1.2, so v1.6.1 is compatible. T009 verifies this.

### 2. Expose the gateway on a fixed NodePort; leave Traefik alone

- The gateway's Envoy Service is **type NodePort on a fixed port, default
  30080**, configured through a `GatewayParameters` object referenced by
  the Gateway. It's configurable via `SERENDIP_GATEWAY_NODE_PORT`.
- **The Traefik/ServiceLB overlap is resolved by not contending at all.** A
  NodePort never touches ServiceLB or ports 80/443. So:
  - no `agent/bootstrap` change;
  - no migration for already-bootstrapped clusters;
  - no risk of an existing cluster's working Traefik breaking.
- The **same mechanism works identically on kind and K3s**. On kind, the
  cluster is created with an `extraPortMappings` entry mapping
  `127.0.0.1:30080` to container port 30080. The URL reported in status is
  then exactly the URL the host uses; the verification stand-in has no
  translation layer that could hide a lie.
- **Cost, stated plainly:** URLs carry `:30080` instead of using port 80.
  - Disabling Traefik and serving on 80/443 through a LoadBalancer Service
    is a reasonable later improvement.
  - It needs its own migration story, so it's recorded in "Explicitly NOT
    done", not attempted here.
  - Traefik keeps running unused, which is a known footprint cost noted
    for the T470 benchmark.

### 3. Addressing: OpenChoreo's own hostname scheme, host configurable, default `localhost`

- The OpenChoreo DataPlane gets `http.port = <nodePort>` and
  `http.host = SERENDIP_GATEWAY_HOST` (default `localhost`). Routes then
  get the hostname `development-openchoreo-control-plane.localhost`.
- `*.localhost` resolves to the loopback address in browsers and curl, so
  on the node itself the reported URL works with zero DNS setup. Example:
  `http://development-openchoreo-control-plane.localhost:30080/cmp-<h>-http`.
- **Honest limitation (documented):** from another machine,
  `*.localhost` doesn't resolve. The operator sets
  `SERENDIP_GATEWAY_HOST=<node-ip>.nip.io`, or a real wildcard DNS name.
- Serendip **does not invent URLs**. It reports OpenChoreo's own
  `invokeURL`, so whatever the route really matches is what status shows.
- Per-Component paths (`/cmp-<h>-<port>`) are ugly but correct. Friendly
  paths and hostnames come with custom domains, which are Scope-OUT.

### 4. Capability split: Gateway provides a shared ingress point; Application Platform routes through it

- New `core/reconcile` types:

  ```go
  // GatewayEndpoint is where the shared external gateway can be attached to and reached.
  type GatewayEndpoint struct {
      Name, Namespace string // identifies the gateway object routes attach to
      Host            string // base host external routes are published under
      Port            int32  // port clients connect to
  }

  type GatewayProvider interface {
      // EnsureGateway makes the shared external gateway exist and reports
      // whether it is ready to accept routes.
      EnsureGateway(ctx context.Context) (ep GatewayEndpoint, ready bool, reason, message string, err error)
  }
  ```

- **Composition, in `cmd/serendipd`:**
  1. `wireGateway` looks up `"gateway"` in the Registry, type-asserts
     `GatewayProvider`, and calls `EnsureGateway`. Startup fails loudly if
     the gateway isn't ready after the provider's bounded wait, the same
     contract as spec 010's `CheckReady`.
  2. The resulting `GatewayEndpoint` is passed into `wireOpenChoreo`.
  3. The OpenChoreo provider writes it into `DataPlane.spec.gateway`.
- **Neither provider imports the other.** Both depend only on `core/`
  types, and either is removable (constitution rule 2).
- **No new resource kind.** Gateway health shows up where users feel it:
  on each exposed Component's `Reachable` Condition (decision 6).
  - A singleton "Gateway" resource with its own reconciler is the natural
    next step once there's more than one gateway, or a bring-your-own
    provider. It's deferred because one startup-ensured gateway doesn't
    need it.
  - That matches how spec 010/011's DataPlane and platform defaults are
    handled.
- **Known trade-off (same as spec 011's platform defaults):** the gateway
  is ensured at startup, not reconciled live.
  - If someone deletes it by hand, Components report `Reachable=False`
    honestly.
  - It's repaired on the next restart.

### 5. Exposure field: `Expose bool` on `ComponentPort`

```go
type ComponentPort struct {
    Name   string `json:"name"`
    Port   int32  `json:"port"`
    Expose bool   `json:"expose,omitempty"` // reachable from outside the cluster through the gateway
}
```

- **A bool, not a visibility enum.** Internal and namespace visibility are
  Scope-OUT, and a later enum can be introduced additively (an `expose`
  field that maps to it). Constitution rule 10 says don't model the
  deferred case.
- It defaults to false, so every spec-011 Component is unchanged.

**Provider mapping:**
- An exposed port becomes a Workload endpoint with
  `{type: HTTP, port, visibility: [external]}`.
- A non-exposed port stays `{type: TCP, port}`, exactly as in spec 011.
- Toggling `expose` changes only the Workload. Spec 011's drift check
  patches it, autoDeploy cuts a new release, and the route appears or
  disappears.
- `workloadEndpoint` gains `Visibility []string` (`omitempty`), included
  in the equality check and the merge patch.

### 6. Status: endpoint URLs plus a `Reachable` Condition, written only when something exposes

```go
type ComponentEndpointStatus struct {
    Name string `json:"name"` // port name
    URL  string `json:"url"`
}
type ComponentStatus struct {
    Status
    Endpoints []ComponentEndpointStatus `json:"endpoints,omitempty"`
}
```

- **`EnsureComponent`'s return grows into a struct** (replacing the
  4-value return):

  ```go
  type ComponentObservation struct {
      Ready                   bool
      Reason, Message         string
      Reachable               *Reachability // nil when no port is exposed
      Endpoints               []ComponentEndpoint
  }
  type Reachability struct { Reachable bool; Reason, Message string }
  ```

- **`Reachable` Condition reasons (fixed, product-neutral):**

  | Reason | Status | Meaning |
  |---|---|---|
  | `Serving` | True | Workload Deployed, every exposed port's route attached to the gateway, URL known |
  | `Pending` | False | Workload not yet Deployed, route not yet rendered or attached, or URL not yet reported |
  | `RouteRejected` | False | The gateway rejected a route (upstream reason/message carried in `Message`) |

- **URLs are populated only when `Reachable` is True.** A `Pending`
  Component has no URLs. Un-exposing every port removes both the
  `Reachable` Condition and `Endpoints`. That's the "never report an
  address that doesn't answer" rule (acceptance criterion 4).
- **What "attached" means:**
  - the rendered HTTPRoute (found by labels `openchoreo.dev/component`
    and `openchoreo.dev/endpoint-visibility=external` in the cell
    namespace from `ProjectReleaseBinding.status.namespace`) has a
    `status.parents[]` entry for our Gateway;
  - with `Accepted=True` and `ResolvedRefs=True`;
  - plus the ReleaseBinding's `endpoints[].invokeURL` is set for that port.
- `kgateway.dev/Programmed`-style vendor conditions stay inside the
  provider.
- **Idempotency:** the reconciler writes when `Ready`'s or `Reachable`'s
  `{Status, Reason}` changes, or the `Endpoints` list changes. Otherwise it
  makes zero writes (same rule as spec 011, extended to the new fields).
- **No HTTP probe from `serendipd`.** It runs outside the cluster, and the
  `*.localhost` URL isn't necessarily resolvable from wherever `serendipd`
  runs. Route attachment plus upstream readiness is the signal, and T009
  proves it matches reality by actually curling from the host.

### 7. Component type update path, and restoring external routes

- `serendip-service` regains the `httproute-external` resource, copied
  from the v1.3.1 sample with:
  - the CORS filter dropped;
  - the `forEach` filter on `"external" in ep.visibility && ep.type == "HTTP"`.
- **New problem this creates:** spec 011's `EnsurePlatformDefaults` is
  GET-then-create, so clusters that already have `serendip-service` would
  never see the change.
- **Fix:** defaults gain an **update-if-different** path for the
  ClusterComponentType only. The provider compares the stored `spec`
  (decoded) with the desired one and merge-patches on a difference.
- Existing releases snapshot the component type, so running Components are
  untouched until their next Workload change. Exposing a port is such a
  change, so the new type applies exactly when it's needed.

### 8. Shared Helm runner

- `providers/application/openchoreo/helm.go` moves to
  **`providers/internal/helm`**, unchanged apart from its package name.
  Both the OpenChoreo and kgateway providers use it.
- Deleting either provider still builds, because `internal/` is only
  imported by providers.
- ADR 0011 records that kgateway also ships first-party Helm charts, so
  ADR 0010's reasoning (official distribution mechanism, well-tested
  `--wait`) applies on its own merits, not by inheritance.

### 9. kgateway provider (`providers/gateway/kgateway`)

- **`Deployer.Ensure`:**
  - `helm upgrade --install kgateway-crds ... --version v2.4.6 -n kgateway-system --create-namespace --wait`
  - `helm upgrade --install kgateway ... --version v2.4.6 -n kgateway-system --set controller.extraEnv.KGW_ENABLE_EXPERIMENTAL_GATEWAY_API_FEATURES=false --wait`
  - The exact values key for extra env is confirmed against
    `helm show values` in T005.
- **`Provider.EnsureGateway`** does a GET-then-create of:
  - `GatewayParameters/serendip-external` (NodePort, `ports: [{port: 80, nodePort: <cfg>}]`);
  - `Gateway/serendip-external` in `kgateway-system`: class `kgateway`,
    one HTTP listener on port 80, `allowedRoutes.namespaces.from: All`,
    `infrastructure.parametersRef` → the GatewayParameters.

  Both are patched if they differ (configurable nodePort). It then waits,
  bounded at 2 minutes / 2s interval like spec 008's StatefulSet wait, for
  `Programmed=True`, and returns `GatewayEndpoint{Name, Namespace, Host:
  cfg, Port: nodePort}`.
- Whether `Programmed` needs a Service address that a NodePort Service
  never gets is **(unconfirmed)**. If it does, T009 switches the readiness
  signal to `Accepted=True` plus the Envoy Deployment being ready, and
  records the change.

### 10. Wiring order in `cmd/serendipd`

```
wireKubernetes:
  storage → secrets (OpenBao)
  → wireGateway:  EnsureGatewayAPICRDs (moved here from wireOpenChoreo)
                  → kgateway Deployer.Ensure → registry "gateway" → EnsureGateway → GatewayEndpoint
  → wireOpenChoreo(…, gatewayEndpoint): Deployer.Ensure → EnsureDefaultDataPlane
                  → EnsureDataPlaneGateway(endpoint) → EnsurePlatformDefaults → CheckReady → provider + reconcilers
```

- `EnsureGatewayAPICRDs` moves to `providers/internal/gatewayapi`. Both
  kgateway and OpenChoreo need the CRDs, so neither provider should own
  them.
- *(Corrected in T003.)* Go's `internal/` rule stops `cmd/serendipd` from
  importing it directly, so **each provider ensures its own
  prerequisite**:
  - the kgateway `Deployer.Ensure` calls `gatewayapi.EnsureCRDs` first;
  - OpenChoreo keeps `EnsureGatewayAPICRDs` as a one-line delegate.

  Whichever runs first does the work. The other is about 12 GETs. Either
  provider still works if the other is removed.
- `EnsureDataPlaneGateway` merge-patches `DataPlane/default.spec.gateway`
  only when it differs.

## Files touched

- **New:** `docs/decisions/0011-kgateway-as-gateway-provider.md`
- **Moved:**
  - `providers/application/openchoreo/helm.go` + test → `providers/internal/helm/`
  - `providers/application/openchoreo/gatewayapi.go` + test → `providers/internal/gatewayapi/`
    (v1.6.1 pin + upgrade-only semantics)
- `core/resource/app.go` (+ test): `ComponentPort.Expose`,
  `ComponentStatus.Endpoints`.
- `core/reconcile/application.go` (+ test): `ComponentObservation`,
  `Reachability`, `Reachable` Condition, endpoint status, the extended
  idempotency rule.
- `core/reconcile/gateway.go` (new): `GatewayEndpoint`, `GatewayProvider`.
- **New `providers/gateway/kgateway/`:** `deploy.go`, `gateway.go`,
  `VERSIONS.md` + tests.
- `providers/application/openchoreo/`:
  - `deploy.go`: uses `internal/helm`;
  - `defaults.go`: `httproute-external`, component-type update path;
  - `dataplane.go`: `EnsureDataPlaneGateway`;
  - `provider.go`: exposure mapping, route/URL observation;
  - tests and `VERSIONS.md` (Gateway API pin moves).
- `cmd/serendipd/main.go`: `wireGateway`, the new `wireOpenChoreo`
  parameter, and env vars `SERENDIP_GATEWAY_HOST` /
  `SERENDIP_GATEWAY_NODE_PORT`.
- **Docs (T011):**
  - new `docs/architecture/gateway.md`;
  - `application-platform.md`, `capability-model.md` (ADR link),
    `provider-model.md` (4th interface data point), `PROGRESS.md`.
- **No `api/` change.** New spec/status fields flow through the generic
  handlers.
- **No new Go dependency.**

## Risks

1. **Unconfirmed upstream details**, settled in T009:
   - the GatewayParameters link field;
   - whether `Programmed` works with NodePort;
   - the extra-env values key;
   - whether HTTPRoute `status.parents` names our Gateway as expected;
   - OpenChoreo behavior on Gateway API v1.6.1;
   - the exact `invokeURL` format, including port.
2. **CRD upgrade by merge patch.** Merge patch replaces arrays, so
   `versions` is fine. But map keys removed between v1.3 and v1.6 schemas
   would survive as stale properties. Mitigation: T009 runs the upgrade
   path for real, on a cluster pre-seeded with v1.3.0 CRDs, and checks
   that the CRDs report v1.6.1 and that kgateway and OpenChoreo both work.
   If merge patch proves unsound, fall back to a full replace (a `Replace`
   method preserving `resourceVersion`), and record why.
3. **Footprint.** Controller + Envoy are on top of a stack already near
   1 GiB, plus Traefik still idling. T009 records the number. If it's
   egregious, ADR 0011's fallback conditions apply.
4. **kgateway churn.** Fast minors, Sandbox maturity, and each minor
   dropping its oldest Gateway API version. ADR 0011 records the fallbacks:
   - Envoy Gateway, a CNCF Gateway API implementation;
   - Cilium's Gateway API, once a Cilium provider exists.
5. **NodePort collisions.** If 30080 is taken, the gateway won't program.
   Startup fails with a clear error naming the port and the env var to
   change.

## Explicitly NOT done

Everything in spec.md's Scope-OUT, plus these plan-level deferrals:

- Disabling Traefik / serving on 80/443 via LoadBalancer (decision 2).
- A Gateway resource kind, or live reconciliation of the gateway
  (decision 4).
- An HTTP reachability probe from `serendipd` (decision 6).
- Friendly paths/hostnames per Component (decision 3).
- An internal/namespace visibility enum (decision 5).
- Using the data-plane chart's own `gateway.enabled` (it saves nothing and
  adds a kgateway-specific policy object).
- Updating platform defaults other than the ClusterComponentType
  (decision 7).

## Corrections from real-cluster verification (T009)

Run on kind (Kubernetes 1.34), cluster pre-seeded with Gateway API v1.3.0.
Two findings changed the code; the rest of the hypothesis held.

**Findings fixed in code**

1. **NetworkPolicy blocked the gateway.** OpenChoreo gives every Component a
   NetworkPolicy admitting exposed-port traffic only from same-namespace pods
   or pods labeled `openchoreo.dev/system-component`. Routes were
   Accepted/Programmed but every request 503'd until the proxy pods carried
   that label. Fix: kgateway takes `proxyLabels` and sets
   `Gateway.spec.infrastructure.labels`; `cmd/serendipd` passes
   `openchoreo.GatewayProxyLabels()`, so neither provider imports the other.
2. **`invokeURL` is not what 1.3.1 reports.** The ReleaseBinding fills
   `status.endpoints[].externalURLs.http {scheme,host,port,path}`. The
   provider now assembles the URL from those reported parts (default ports
   omitted) and still honors `invokeURL` if present. Decision 3's
   "never assemble URLs" is amended to "only from the platform's own reported
   parts".

**Unconfirmed items, now settled** (plan risk 1)

- Gateway links to GatewayParameters via `spec.infrastructure.parametersRef`:
  confirmed.
- `Programmed=True` works with a NodePort Service: confirmed (no fallback to
  `Accepted` needed).
- The chart renders no GatewayClass; the controller creates `kgateway` at
  runtime. `controller.extraEnv` is the extra-env values key.
- HTTPRoute `status.parents[]` names our Gateway; OpenChoreo works on Gateway
  API v1.6.1.
- CRD upgrade by merge patch (risk 2) worked in place from v1.3.0 to v1.6.1,
  including upstream's `safe-upgrades` policy; no full-replace fallback
  needed.

**Review-driven changes (T008/T010)**

- A transient route-observation error keeps `Ready` and reports
  `Reachable=Pending` instead of failing the Component.
- `LastTransitionTime` is preserved when a Condition's status is unchanged.
- `Reachable=True` from a provider is downgraded to `Pending` when the
  workload is not Ready.
- Route conditions from an older route generation count as pending.
- A parentRef without a namespace does not match the gateway.

The `internal/` wiring correction is already recorded in decision 10.

**Known limits left as is**

- Merge patches cannot drop keys: removing a proxy label, or a key from the
  component type, leaves it on the live object.
- Pre-existing since spec 010, not caused here: `providers/internal/helm` runs
  `helm` without `--kubeconfig`, so Helm follows the default kubeconfig
  context rather than `SERENDIP_KUBECONFIG`. Verification worked only because
  both pointed at the same cluster. Tracked in PROGRESS "Next up".
