# Versions: providers/build/openchoreo

Upstream version assumptions for the builds package (spec 022). Update
here and in the constants named below together. The "Defined in" column
names files T005 and T006 create.

| Item | Version / value | Defined in |
|---|---|---|
| OpenChoreo workflow-plane chart | 1.3.1, `oci://ghcr.io/openchoreo/helm-charts/openchoreo-workflow-plane` (digest `sha256:5f570ab14e853779b2ed2b58cb71eaa99d5f6ca39025154fea33513f083c9a2d`) | `installer.go` |
| Argo Workflows (subchart) | chart 1.0.23, app `v4.0.8` (`quay.io/argoproj/argoexec:v4.0.8` runs in every build pod) | brought by the chart |
| Argo CRD install Job | `registry.k8s.io/kubectl:v1.36.3`, downloads the CRDs from GitHub on every install/upgrade | brought by the chart |
| Cluster agent | `ghcr.io/openchoreo/cluster-agent`, the chart's appVersion (`v1.3.1`) | brought by the chart |
| Registry | `registry:2.8.3` (distribution), `hostNetwork`, port 5000 | `registry.go` |
| Checkout step | `alpine/git:v2.52.0` | `workflow.go` |
| Build-and-push step | `ghcr.io/openchoreo/podman-runner:v1.2` | `workflow.go` |
| OpenChoreo API group/version | `openchoreo.dev/v1alpha1` (`ClusterWorkflowPlane`, `ClusterWorkflow`, `WorkflowRun`) | `installer.go`, `builds.go` |
| Argo API group/version | `argoproj.io/v1alpha1` (`Workflow`, read for run outputs) | `builds.go` |
| Verification cluster | `kindest/node:v1.34.0`, containerd 2.1.3 | n/a |

Notes (spec 022 T001, verified on 1.3.1, 2026-10-24; details in
`specs/022-builds/plan.md`, "Spike findings"):

- **Trust:** the agent needs a `cluster-gateway-ca` ConfigMap in
  `openchoreo-workflow-plane` before install, copied from the control
  plane's `cluster-gateway-ca` Secret, the same gap as the data plane.
- **Agent connection:** `ClusterWorkflowPlane.status.agentConnection.connected`
  turns true at the controller's poll, about 60 s after the agent
  connects.
- **Runs are standalone:** `WorkflowRun`s carry no `openchoreo.dev/component`
  or `openchoreo.dev/project` labels. With them, 1.3.1 requires the
  ComponentType's `allowedWorkflows`.
- **Where runs land:** a run in namespace `serendip-builds` runs its
  Workflow and pods in `workflows-serendip-builds`. The controller creates
  that namespace and `workflow-sa` on the first run only.
- **Outputs:** only the Argo Workflow carries them, at
  `status.nodes[*].outputs.parameters`. `WorkflowRun.status` doesn't.
- **User namespaces:** `hostUsers: false` fails on kind, because kind's
  OCI hook breaks in a user namespace. The build step is `privileged`
  without it. Recheck on K3s.
- **CRDs on uninstall:** the Argo CRDs are installed by a hook Job, not
  owned by Helm, so they survive uninstall. Serendip leaves them.
- **Images built from source** are pulled by workloads as
  `localhost:5000/...`. containerd uses plain HTTP for `localhost` with no
  node configuration. That's single-node only.
- **The chart's names** (from the pulled 1.3.1 chart, T005): Deployments
  `argo-workflow-controller` (Argo subchart, `fullnameOverride: argo`)
  and `cluster-agent-workflowplane`, the agent's `cluster-agent-tls`
  Secret, and `clusterAgent.planeID: default`. The quick checks depend on
  these names.
- **The build image's `realpath`** doesn't accept `--`. The build step's
  symlink guard (RP-040) calls it without that.
- `helm` must be on `$PATH` (ADR 0010), at 3.13 or later: Uninstall uses
  `helm uninstall --ignore-not-found`. Verified with 3.16.

Re-check all of these on every OpenChoreo upgrade.
