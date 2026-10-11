# kgateway provider — version assumptions

ADR 0011 records why these versions were chosen.

| What | Pinned | Where |
|---|---|---|
| kgateway CRDs chart | `oci://cr.kgateway.dev/kgateway-dev/charts/kgateway-crds` `v2.4.6` | `deploy.go` |
| kgateway chart | `oci://cr.kgateway.dev/kgateway-dev/charts/kgateway` `v2.4.6` | `deploy.go` |
| Gateway API (standard channel) | `v1.6.1` | `providers/internal/gatewayapi` |
| GatewayParameters API | `gateway.kgateway.dev/v1alpha1` | `gateway.go` |
| Gateway API Gateway | `gateway.networking.k8s.io/v1` | `gateway.go` |

## Facts confirmed against the v2.4.6 charts

- `controller.extraEnv` is the chart values key for controller env vars.
  Rendering the chart with
  `KGW_ENABLE_EXPERIMENTAL_GATEWAY_API_FEATURES=false` sets that env var on
  the controller Deployment.
- The chart does **not** render a GatewayClass. The controller manages the
  default classes (`kgateway`, `kgateway-waypoint`) itself. Waypoint is off
  by default.
- The `kgateway-crds` chart holds kgateway's own CRDs only (GatewayParameters,
  TrafficPolicy, HTTPListenerPolicy, ...), not Gateway API's.
- GatewayParameters `spec.kube.service.type` accepts `NodePort`, and
  `spec.kube.service.ports[]` takes `{port, nodePort}`.

## Measured footprint (spec 012 T009, kind, dev machine; not the T470 benchmark)

Controller about 50 MB, Envoy proxy about 19 MB, about 70 MB and about 1% CPU
idle in total. See ADR 0011.

## Compatibility

kgateway 2.4.x is tested on Kubernetes 1.32–1.36 with Gateway API 1.4–1.6.
Each kgateway minor narrows its Gateway API range, so bump kgateway and the
Gateway API pin together.
