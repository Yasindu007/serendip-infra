# Gateway

This document describes the Gateway capability (`capability-model.md`) as it
exists after spec 012: one shared external gateway (kgateway) that
Components deployed through the Application Platform are exposed on. ADR 0011
is the authoritative record for *why kgateway*, the version pin, the install
mechanism and the fallbacks.

Code: `core/reconcile/gateway.go` (interface), `providers/gateway/kgateway/`
(provider), `providers/internal/gatewayapi/` (Gateway API definitions),
`providers/application/openchoreo/` (route rendering side), `cmd/serendipd`
`wiring.go`/`setup.go` (wiring and background setup). Pins: `providers/gateway/kgateway/VERSIONS.md`.

## Capability split

Serendip does not write HTTPRoutes. OpenChoreo renders them from Workload
endpoint visibility. So the Gateway capability's job is narrow: install the
gateway, own the one shared Gateway, and say where it is.

```go
type GatewayEndpoint struct {
    Name, Namespace string // the gateway object routes attach to
    Host            string // base host external routes are published under
    Port            int32  // port clients connect to
}
type GatewayProvider interface {
    EnsureGateway(ctx) (ep GatewayEndpoint, ready bool, reason, message string, err error)
}
```

Composition happens only in `cmd/serendipd`. The gateway's background setup
step (spec 014) installs kgateway and calls `EnsureGateway`; a gateway that is
not programmed within the provider's wait is a failed attempt, retried with
backoff and reported by `GET /v1/readiness`, not a startup failure. On
success it registers the provider under `"gateway"`, and the resulting
`GatewayEndpoint` is handed to the application platform's setup step, which
applies it as `DataPlane/default.spec.gateway.ingress.external = {name,
namespace, http:{port, host}}` (`EnsureDataPlane`) and passes it to
`openchoreo.NewProvider`. OpenChoreo then renders each external endpoint's
HTTPRoute with that Gateway as `parentRef`. Neither provider imports the
other. The Gateway is configured on the DataPlane, not the Environment,
because OpenChoreo's release controller watches DataPlanes and re-renders on
change.

There is no `Gateway` resource kind. The gateway is ensured at setup and then
re-ensured on the foundations interval (Gateway API CRDs, then
`EnsureGateway`; `SERENDIP_REENSURE_INTERVAL`, default 5m; see
`reconciliation.md`), so a deleted or edited Gateway is restored without a
restart. Its health also surfaces on each exposed Component's `Reachable`
Condition.

## Install

`kgateway.Deployer.Ensure` runs, in order: `gatewayapi.EnsureCRDs`, then
`helm upgrade --install` of `kgateway-crds` and `kgateway` (both **v2.4.6**,
namespace `kgateway-system`, `--wait`, via `providers/internal/helm`). The
controller runs with `KGW_ENABLE_EXPERIMENTAL_GATEWAY_API_FEATURES=false`
because only standard-channel CRDs are installed. The chart does not render a
GatewayClass; the controller creates `kgateway` at runtime.

`EnsureGateway` then server-side-applies (field manager `serendip`;
`provider-model.md`):

- `GatewayParameters/serendip-external` (`gateway.kgateway.dev/v1alpha1`):
  Service type `NodePort`, `ports: [{port: 80, nodePort}]`.
- `Gateway/serendip-external` in `kgateway-system`: class `kgateway`, one HTTP
  listener on 80 with `allowedRoutes.namespaces.from: All` (routes live in
  per-Application namespaces), `spec.infrastructure.parametersRef` to the
  GatewayParameters, and `spec.infrastructure.labels` (see proxy labels).

It polls (2s interval, 2 min bound) for `Programmed=True` on the current
generation. Timeout is reported as not-ready with upstream's reason, which the
setup step turns into a retried failure whose message names
`SERENDIP_GATEWAY_NODE_PORT` (a port already in use is the usual cause).

### Gateway API pin (v1.6.1, upgrade-only)

kgateway 2.4.x supports Gateway API 1.4-1.6, so the pin moved from spec 011's
v1.3.0 to **v1.6.1** (standard channel). It lives in
`providers/internal/gatewayapi`, because both the gateway provider and
OpenChoreo (whose controller lists Gateway API kinds even with its own
gateway disabled) need the definitions. Each provider calls `EnsureCRDs`
itself (Go's `internal/` rule keeps `cmd/serendipd` from importing it); the
first to run does the work, the other is a handful of GETs.

Semantics are **upgrade-only**: for each pinned object, the
`gateway.networking.k8s.io/bundle-version` annotation decides. Missing is
created; older than the pin is merge-patched to the pinned definition (labels,
annotations, `spec`); equal or newer is left alone, never downgraded. The
download (sha256-verified, from github.com) is skipped when everything
already exists at the pin or newer. Pinned objects are the 10 CRDs plus
upstream's `safe-upgrades` ValidatingAdmissionPolicy and Binding.

**The in-place upgrade is intentional and cluster-wide.** Gateway API CRDs are
shared by every consumer in the cluster. A cluster set up under spec 011
(v1.3.0) gets upgraded to v1.6.1 on the next `serendipd` start, and the
safe-upgrades policy is installed alongside.

## Exposure and addressing

The Gateway's Envoy Service is a **NodePort, default 30080**
(`SERENDIP_GATEWAY_NODE_PORT`, must be in 30000-32767; invalid values fail
startup). K3s keeps Traefik and ServiceLB on node ports 80/443 (spec 005); a
NodePort never touches them. So the port overlap is resolved by not contending:
**no `agent/bootstrap` change and no migration. Already-bootstrapped clusters
need nothing.** The same mechanism works on kind (via `extraPortMappings` of
the nodePort) and on K3s.

URLs have the form
`http://<env>-<org namespace>.<SERENDIP_GATEWAY_HOST>:<nodePort>/<component>-<port name>`,
for example
`http://development-openchoreo-control-plane.localhost:30080/cmp-<hash>-http`.

- `SERENDIP_GATEWAY_HOST` defaults to `localhost`. `*.localhost` resolves to
  loopback, so the URL works **only on the node itself**. For other machines,
  set `<node-ip>.nip.io` or a name under a real wildcard DNS record.
- HTTP only. No TLS.
- Serendip reports the URL OpenChoreo itself publishes in
  `ReleaseBinding.status.endpoints[]`. OpenChoreo 1.3.1 fills
  `externalURLs.http {scheme, host, port, path}` rather than `invokeURL`, so
  the provider assembles the URL from those reported parts (default ports
  omitted); `invokeURL` is used if present.

## Exposing a Component port

`ComponentPort.Expose bool` (default false, so spec 011 Components are
unchanged). Mapping in the OpenChoreo provider:

| `expose` | Workload endpoint |
|---|---|
| true | `{type: HTTP, port, visibility: [external]}` |
| false | `{type: TCP, port}`, no visibility (spec 011 behavior) |

`serendip-service` renders an HTTPRoute (`httproute-external`) only for
endpoints that are HTTP and have `external` visibility, so only the exposed
port gets a route. Un-expose works because the Workload is server-side
applied without the visibility key, and the API server removes a field the
`serendip` manager no longer applies (spec 012 had to send an explicit null in
a merge patch); the route disappears and status follows. Toggling only changes the
Workload; autoDeploy cuts a new release.

## The `Reachable` Condition

Written only when at least one port is exposed. `EnsureComponent` returns a
`ComponentObservation` (`Ready`, `Reachable *Reachability`); a nil
`Reachable` means nothing is exposed.

| Reason | Status | Meaning |
|---|---|---|
| `Serving` | True | Workload Deployed; for every exposed port the HTTPRoute has a status entry for our Gateway with `Accepted` and `ResolvedRefs` True, and OpenChoreo has reported its URL |
| `Pending` | False | Workload not Deployed, route not rendered/attached yet, a condition Unknown or from an older route generation, URL not reported, or the routes could not be observed this pass |
| `RouteRejected` | False | The gateway reported `Accepted` or `ResolvedRefs` False; upstream reason and message go in `Message` |

`ComponentStatus.Endpoints` (`{name, url}`) is published **only at
`Serving`**. Un-exposing every port removes both the Condition and the
endpoints. Honesty rules enforced in the reconciler and provider:

- A transient route-observation error keeps `Ready` and reports
  `Reachable=Pending`; the Component does not flap to failed.
- `Reachable=True` from a provider is downgraded to `Pending` if the workload
  is not Ready.
- A parentRef without a namespace does not match the gateway (a route's
  implicit namespace is never the gateway's).
- `LastTransitionTime` is preserved when a Condition's status is unchanged.
- The write happens only when a Condition's `{Status, Reason}` or the
  endpoint list changes, so an unchanged exposed Component produces no
  `Update`, audit row, or `ResourceVersion` bump.

Route acceptance is an upstream state change, not a Serendip event, so while
`Reachable=Pending` the reconciler asks for early re-checks (2s doubling,
spec 014; `reconciliation.md`) instead of waiting for the 30s resync. There
is no HTTP probe from `serendipd`.

### Proxy labels (NetworkPolicy finding)

OpenChoreo gives every Component a NetworkPolicy admitting traffic on exposed
ports only from same-namespace pods or pods labeled
`openchoreo.dev/system-component`. Without that label on the gateway's Envoy
pods, routes were Accepted and Programmed but every request failed with 503
(upstream connect timeout). `cmd/serendipd` passes
`openchoreo.GatewayProxyLabels()` into `kgateway.NewProvider`, which sets them
as `Gateway.spec.infrastructure.labels`; neither provider names the other. Any
future gateway provider must support labeling its proxy pods.

## Real-cluster verification (spec 012, kind, Kubernetes 1.34)

Cluster pre-seeded with Gateway API v1.3.0 CRDs.

- `serendipd` upgraded the CRDs in place to v1.6.1 and installed the
  safe-upgrades policy. `Gateway/serendip-external` Programmed with a NodePort
  Service 80:30080 (Programmed works with NodePort).
- Component with exposed `http` and internal `metrics`: `Reachable` Pending to
  Serving in about 28s. `curl` from the Windows host to the reported URL
  returned 200 from nginx. Only the exposed port got an HTTPRoute.
- Idempotent across 2 resyncs (`ResourceVersion`, audit rows, Workload
  generation unchanged).
- Un-expose: route gone in about 3s, curl 404, Condition and endpoints
  removed, pod kept running. Re-expose: 200. Delete: route gone.
- A Component with nothing exposed deploys exactly as in spec 011.
- Restart (about 37s): Gateway patched once (proxy labels added); nothing else
  changed.

## Real-cluster verification (spec 014, kind, Kubernetes 1.34)

- After the server-side apply conversion the spec 012 flow passed again
  (expose, curl 200, un-expose with route removed and curl 404, re-expose).
- Exposed Component create to `Serving` with a cached image: 14.2s (4 of 4
  runs), `Serving` 0.2-0.3s after `Deployed` (was about 28s in spec 012).
- A `Gateway` deleted by hand was recreated by the foundations loop
  (`SERENDIP_REENSURE_INTERVAL=30s`); the Component returned to `Serving` and
  curl returned 200, with no restart.
- Idle: Gateway and GatewayParameters generation and `serendip` apply time
  unchanged across 75s of re-ensure passes.

## Footprint

The systematic numbers are in `profiles/edge-1/bench/002-edge1-footprint.md`
(spec 013, simulated T470, still not the reference-hardware gate): kgateway
is **about 52 MiB** at idle on a throttled node. The earlier one-off reading
below was taken with `docker stats`, which counts page cache, so it runs
higher.

Earlier one-off reading (dev machine, Docker Desktop, 8 GB): kgateway
controller about 50 MB, Envoy proxy about 19 MB, about 70 MB and about 1% CPU
idle in total. The whole kind node idled at about 1.67 GiB with the full
stack; `serendipd` about 21 MB. Traefik still idles unused on K3s. Details in
ADR 0011.

## Not done / deferred

- TLS/HTTPS; custom domains and per-Component friendly paths or hostnames.
- `internal`/`namespace` visibility (`expose` is a bool; an enum can be added
  additively).
- API management (auth, rate limiting, analytics); multiple Gateways,
  per-Application or per-Environment gateways; a bring-your-own (`external`)
  gateway provider; non-HTTP (TCP/UDP) exposure; gateway HA.
- Disabling Traefik to serve on 80/443 through a LoadBalancer Service (needs a
  migration story for existing clusters).
- A `Gateway` resource kind. (~~Live repair~~: since spec 014 the
  foundations loop restores a deleted or edited Gateway within the re-ensure
  interval.)
- An HTTP reachability probe from `serendipd`.
- ~~Removing a proxy label~~: fixed in spec 014. The Gateway is server-side
  applied, so a label dropped from the configured set is removed. Clusters
  created before spec 014 keep a dropped label once (see the migration note in
  `provider-model.md`).

## Open questions

- Footprint is unmeasured on T470-class hardware (ADR 0005 benchmark gate).
- CNCF Sandbox maturity and Solo.io concentration: standing watch item, ADR
  0011 fallbacks (Envoy Gateway, Cilium Gateway API).
- Air-gapped Gateway API manifest source (embed vs mirror), shared with
  `application-platform.md`.
