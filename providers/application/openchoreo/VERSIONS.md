# Versions: providers/application/openchoreo

Upstream version assumptions for this provider. Update here and in the
constants named below together.

| Item | Version / value | Defined in |
|---|---|---|
| OpenChoreo control-plane chart | 1.3.1, `oci://ghcr.io/openchoreo/helm-charts/openchoreo-control-plane` | `deploy.go` (`openChoreoVersion`, `controlPlaneChart`) |
| OpenChoreo data-plane chart | 1.3.1, `oci://ghcr.io/openchoreo/helm-charts/openchoreo-data-plane` | `deploy.go` (`openChoreoVersion`, `dataPlaneChart`) |
| cert-manager chart | 1.21.2, `oci://quay.io/jetstack/charts/cert-manager` | `deploy.go` (`certManagerVersion`) |
| OpenChoreo API group/version | `openchoreo.dev/v1alpha1` | `defaults.go` (`apiVersion`) |
| Gateway API CRDs | v1.3.0, standard channel (`standard-install.yaml`) | `gatewayapi.go` (`gatewayAPIVersion`, `gatewayAPIManifestURL`) |
| Gateway API manifest sha256 | `78796d5c51450fc55d8dc8092ba8137f8c807982d7508d7875d5c537a24082b9` | `gatewayapi.go` (`gatewayAPIManifestSHA`) |
| Platform defaults derived from | OpenChoreo v1.3.1 `samples/getting-started/all.yaml` (ClusterProjectType `default`, ClusterComponentType `service`, trimmed) | `defaults.go` |
| Verification cluster | `kindest/node:v1.34.0` (kind 1.31 node image hits a Kubernetes CEL bug on the `ClusterAuthzRoleBinding` CRD, spec 010) | n/a |

Notes:

- Gateway API CRDs are required by OpenChoreo's RenderedRelease controller
  even with `gateway.enabled=false`; the charts need Gateway API >= v1.2.
  Only definitions are installed, never upgraded if already present.
- Fresh installs download the Gateway API manifest from github.com.
- `helm` must be on `$PATH` (ADR 0010).
- Details: `docs/architecture/application-platform.md`.
