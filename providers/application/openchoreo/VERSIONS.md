# Versions: providers/application/openchoreo

Upstream version assumptions for this provider. Update here and in the
constants named below together.

| Item | Version / value | Defined in |
|---|---|---|
| OpenChoreo control-plane chart | 1.3.1, `oci://ghcr.io/openchoreo/helm-charts/openchoreo-control-plane` | `deploy.go` (`openChoreoVersion`, `controlPlaneChart`) |
| OpenChoreo data-plane chart | 1.3.1, `oci://ghcr.io/openchoreo/helm-charts/openchoreo-data-plane` | `deploy.go` (`openChoreoVersion`, `dataPlaneChart`) |
| cert-manager chart | 1.21.2, `oci://quay.io/jetstack/charts/cert-manager` | `deploy.go` (`certManagerVersion`) |
| OpenChoreo API group/version | `openchoreo.dev/v1alpha1` | `defaults.go` (`apiVersion`) |
| Gateway API CRDs | v1.6.1, standard channel (`standard-install.yaml`), upgrade-only | `providers/internal/gatewayapi` (`Version`, `manifestURL`); `gatewayapi.go` here only delegates |
| Gateway API manifest sha256 | `24d931f22abd8e40c973264319ead7cfa09d0fb7716b7ab1ee2ff174cb063a73` | `providers/internal/gatewayapi` (`manifestSHA`) |
| Platform defaults derived from | OpenChoreo v1.3.1 `samples/getting-started/all.yaml` (ClusterProjectType `default`, ClusterComponentType `service`, trimmed) | `defaults.go` |
| Verification cluster | `kindest/node:v1.34.0` (kind 1.31 node image hits a Kubernetes CEL bug on the `ClusterAuthzRoleBinding` CRD, spec 010) | n/a |

Notes:

- Gateway API CRDs are required by OpenChoreo's RenderedRelease controller
  even with `gateway.enabled=false`; the charts need Gateway API >= v1.2.
  Only definitions are installed. Older ones (e.g. spec 011's v1.3.0) are
  upgraded in place by bundle-version annotation; newer ones are never
  downgraded.
- The Gateway API pin moves together with the kgateway pin; kgateway's own
  pins live in `providers/gateway/kgateway/VERSIONS.md`.
- Fresh installs and upgrades download the Gateway API manifest from
  github.com.
- External routes: the `serendip-service` component type renders
  `httproute-external`; verified against OpenChoreo 1.3.1, whose
  `ReleaseBinding.status.endpoints[].externalURLs.http` carries the URL.
- `helm` must be on `$PATH` (ADR 0010).
- **The OpenChoreo API is closed by a NetworkPolicy** (risk register
  RP-018). The control plane runs with `security.enabled=false`, which
  makes `openchoreo-api` anonymous with no authorization, and its
  ClusterIP Service (port 8080) has no NetworkPolicy of its own.
  `serendip-deny-openchoreo-api` (`defaults.go`) denies all ingress to pods
  labelled `app.kubernetes.io/component=api-server`. That rests on these
  facts, verified on 1.3.1 (2026-10-20):
  - those pods carry that label;
  - nothing else in the control plane (controller-manager, event-forwarder,
    cluster-gateway) calls the API; only the kubelet's `/ready` and
    `/health` probes reached it in 24 h;
  - kindnet (kindest/node v1.34.0) enforces NetworkPolicy and still lets
    kubelet probes through (verified live). K3s enforces it with its
    embedded network-policy controller, but that's not yet verified live on
    our K3s pin: check it on the first K3s run.

  Re-check all three on every OpenChoreo upgrade. If Serendip ever calls
  the API, turn its OIDC security on rather than opening the policy.
- Details: `docs/architecture/application-platform.md`.
