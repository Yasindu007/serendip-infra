# Gateway

This document describes the Gateway capability (`capability-model.md`) as it
exists after spec 012: one shared external gateway (kgateway) that
Components deployed through the Application Platform are exposed on. ADR 0011
is the authoritative record for *why kgateway*, the version pin, the install
mechanism and the fallbacks.

Code: `core/reconcile/gateway.go` (interface), `providers/gateway/kgateway/`
(provider), `providers/internal/gatewayapi/` (Gateway API definitions),
`providers/application/openchoreo/` (route rendering side), `cmd/serendipd`
`wireGateway` (wiring). Pins: `providers/gateway/kgateway/VERSIONS.md`.

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

Composition happens only in `cmd/serendipd`: `wireGateway` installs kgateway,
registers the provider under `"gateway"`, looks it up, calls `EnsureGateway`,
and fails startup if the gateway is not ready (same contract as
`CheckReady`). The resulting `GatewayEndpoint` is passed to `wireOpenChoreo`,
which calls `EnsureDataPlaneGateway` (merge-patches
`DataPlane/default.spec.gateway.ingress.external = {name, namespace,
http:{port, host}}`, only on difference) and hands it to
`openchoreo.NewProvider`. OpenChoreo then renders each external endpoint's
HTTPRoute with that Gateway as `parentRef`. Neither provider imports the
other. The Gateway is configured on the DataPlane, not the Environment,
because OpenChoreo's release controller watches DataPlanes and re-renders on
change.

There is no `Gateway` resource kind. The gateway is ensured at startup, not
reconciled live; its health surfaces on each exposed Component's `Reachable`
Condition.

## Install

`kgateway.Deployer.Ensure` runs, in order: `gatewayapi.EnsureCRDs`, then
`helm upgrade --install` of `kgateway-crds` and `kgateway` (both **v2.4.6**,
namespace `kgateway-system`, `--wait`, via `providers/internal/helm`). The
controller runs with `KGW_ENABLE_EXPERIMENTAL_GATEWAY_API_FEATURES=false`
because only standard-channel CRDs are installed. The chart does not render a
GatewayClass; the controller creates `kgateway` at runtime.

`EnsureGateway` then get-or-creates (merge-patching on difference):

- `GatewayParameters/serendip-external` (`gateway.kgateway.dev/v1alpha1`):
  Service type `NodePort`, `ports: [{port: 80, nodePort}]`.
- `Gateway/serendip-external` in `kgateway-system`: class `kgateway`, one HTTP
  listener on 80 with `allowedRoutes.namespaces.from: All` (routes live in
  per-Application namespaces), `spec.infrastructure.parametersRef` to the
  GatewayParameters, and `spec.infrastructure.labels` (see proxy labels).

It polls (2s interval, 2 min bound) for `Programmed=True` on the current
generation. Timeout is reported as not-ready with upstream's reason, which
`wireGateway` turns into a startup error that names `SERENDIP_GATEWAY_NODE_PORT`.

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
port gets a route. Un-expose works because the visibility key is cleared
explicitly in the Workload merge patch (a merge patch would otherwise leave
it); the route disappears and status follows. Toggling only changes the
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

Status follows the 30s resync; route acceptance is an upstream state change,
not a Serendip event. There is no HTTP probe from `serendipd`.

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

## Footprint

Dev machine (Docker Desktop, 8 GB), not the T470 benchmark: kgateway
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
- A `Gateway` resource kind and live reconciliation: a Gateway deleted by hand
  is repaired on the next restart; Components report it meanwhile.
- An HTTP reachability probe from `serendipd`.
- Removing a proxy label: a merge patch cannot drop keys it does not send, so
  dropping a label from the configured set leaves it on the live Gateway (same
  limit as the component-type update).

## Open questions

- Footprint is unmeasured on T470-class hardware (ADR 0005 benchmark gate).
- CNCF Sandbox maturity and Solo.io concentration: standing watch item, ADR
  0011 fallbacks (Envoy Gateway, Cilium Gateway API).
- Air-gapped Gateway API manifest source (embed vs mirror), shared with
  `application-platform.md`.
