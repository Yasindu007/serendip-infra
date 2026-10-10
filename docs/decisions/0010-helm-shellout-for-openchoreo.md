# ADR 0010: Shell out to `helm` for OpenChoreo, as a per-capability exception

Status: Accepted
Date: 2026-10-11

## Context

ADR 0009 (OpenBao) made "No Helm" an explicit decision: "Construct the
StatefulSet/Service/PVC/ConfigMap directly via the existing generic
`providers/kubernetes.Client` ... Introducing Helm as a new tool/dependency
is not justified by one provider's manifests." Spec 007 (Storage,
local-path) set the same precedent first, by bypassing even a `StorageClass`
in favor of direct API object construction. Across both, the reasoning was
the same: the real manifest surface was a handful of objects, and hand-rolling
them directly against `providers/kubernetes.Client` was simpler and added no
new host-level tool dependency.

Spec 010 (OpenChoreo deployment foundation) hit a genuinely different
situation, laid out in its own spec.md Problem section: OpenChoreo's own
first-party distribution mechanism *is* Helm, shipped as OCI-registry charts
(`oci://ghcr.io/openchoreo/helm-charts/openchoreo-control-plane` and
`-data-plane`). The real surface those charts manage is not a handful of
objects — CRDs, a controller-manager, an api-server, a cluster-gateway, and
(confirmed once the chart was actually pulled and inspected) a hard
dependency on a separately-running cert-manager. Reimplementing that surface
by hand would mean either vendoring the chart's entire resource set (fragile,
fights constitution rule 10's "integrate, don't rebuild") or missing pieces
no amount of upstream-doc research could fully enumerate in advance
(oss-researcher could not produce an authoritative, exhaustive CRD list
during spec 010's planning).

Spec 005 already established the relevant precedent for this exact
situation, just for a different product: `agent/bootstrap` shells out to
K3s's own official `get.k3s.io` install script rather than reimplementing
K3s's install logic — "use the project's own official tooling rather than
reimplementing it." Spec 010 applies the identical reasoning to OpenChoreo,
with `helm` standing in for `get.k3s.io`.

## Decision

`providers/application/openchoreo` shells out to the real `helm` CLI
(`os/exec.CommandContext("helm", ...)`, wrapped in `providers/application/
openchoreo/helm.go`) to install cert-manager and both OpenChoreo charts via
`helm upgrade --install`, instead of constructing the equivalent Kubernetes
objects directly.

This makes `helm` a required host-level external tool dependency of
`serendipd`'s environment — checked explicitly via `exec.LookPath` at
`NewDeployer` construction time, with a clear error naming it as a
prerequisite, not a Go dependency. It is **not** a new Go dependency and so
does not trigger constitution rule 4's "new Go dependency requires an ADR"
clause on its own; this ADR exists instead for the traceability
`contract-guardian`'s spec 010 review asked for, since this is a real
reversal of explicit "No Helm" reasoning written into two existing ADRs
(ADR 0002's storage-progression precedent as applied by spec 007, and ADR
0009 directly).

## Scope of this reversal

**This does not reopen ADR 0002 or ADR 0009.** Storage (`providers/storage/
localpath`) and Secrets (`providers/secrets/openbao`) both continue to
construct their Kubernetes objects directly — nothing about this ADR changes
either provider's shipped code or revisits either decision. This is a
per-capability call, scoped to OpenChoreo only, not a blanket "Helm is now
the house style" policy change.

Future capabilities should evaluate the same way, on their own facts, not by
treating either precedent as automatically binding:

- If the upstream project's own distribution mechanism is Helm-native and
  the real manifest surface is large (CRDs, controllers, a dependency graph
  of its own) — shelling out to the official tool is the justified choice,
  the same "use official tooling, don't reimplement it" reasoning spec 005
  and this ADR both apply.
- If the real surface is a handful of objects a direct `providers/
  kubernetes.Client` call can construct as reliably as a chart would —
  direct construction stays preferred, per ADR 0002/0009's original
  reasoning. Pulling in Helm for that case is still not justified by "one
  provider's manifests," unchanged.

## Consequences

- `helm` joins `get.k3s.io`'s install script (spec 005) as the second
  official external CLI tool this codebase's providers shell out to,
  rather than reimplementing. `providers/application/openchoreo/helm.go`'s
  `helmRunner` abstraction (a function value, not an interface) is local to
  that package — no shared `providers/helm` package exists yet, since no
  second Helm-consuming provider exists to justify the abstraction
  (constitution rule 10).
- Readiness for Helm-installed resources is Helm's own `--wait`/`--timeout`
  logic, not a hand-rolled poll loop — a real simplification versus
  `providers/secrets/openbao/deploy.go`'s `waitStatefulSetReady`, which
  exists only because that spec built objects directly with nothing to
  `--wait` on.
- `helm` availability is a new host prerequisite documented wherever
  `serendipd`'s other host prerequisites are tracked (kubeconfig,
  `SERENDIP_KUBECONFIG`) — not assumed present.
- No change to `core/`: this ADR is entirely a `providers/` implementation
  decision, consistent with constitution rule 2 (`core/` imports no provider
  package and has no opinion on how a provider talks to its product).
