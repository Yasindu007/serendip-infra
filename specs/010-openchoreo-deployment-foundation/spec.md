# Spec 010: OpenChoreo deployment foundation

Status: Draft — human review required before plan.md

## Problem

Application Platform (`capability-model.md:46`, `overview.md:64`) already
names OpenChoreo as its default provider, and ADR 0005 formalizes that
choice, its risks (CNCF Sandbox governance, WSO2 commit concentration,
unverified edge-1 resource footprint), and the mitigations attached to
accepting it anyway. Nothing has been built yet: no OpenChoreo deployment
exists in the cluster, no client talks to it, and no resource kind
represents an Application or Component.

This is explicitly a **foundation** spec, not the Application Platform
capability itself — mirroring specs 005+006→007 (Kubernetes access →
Storage) and 008→009 (OpenBao deployment → Secrets capability). Spec
010 gets a real OpenChoreo control plane (and co-located data plane)
running and reachable; a later spec builds the actual `Application`/
`Component` resource kinds, an `ApplicationPlatformProvider` interface,
and a reconciler on top of it.

**This spec deviates from specs 007/008's established "no Helm, build
Kubernetes objects directly" precedent — flagged explicitly for approval,
not silently decided.** OpenChoreo's own first-party distribution
mechanism is Helm, via OCI-registry charts
(`oci://ghcr.io/openchoreo/helm-charts/openchoreo-control-plane` and
`-data-plane`) — not a handful of objects a hand-rolled client can
construct directly (ADR 0002/0009's "no Helm" reasoning was specifically
that a StatefulSet/Service/PVC/ConfigMap was simpler to build directly
than to pull in Helm for). OpenChoreo's chart installs CRDs, a
controller-manager, an api-server, and bundles its own `cert-manager`
dependency — reimplementing that surface by hand would mean either
vendoring the chart's entire resource set (fragile, actively fights
"integrate, don't rebuild," constitution rule 10) or missing pieces this
spec can't fully enumerate today (oss-researcher could not find an
authoritative, exhaustive CRD list — see Open Questions). The proposed
approach: shell out to the real `helm` CLI (`helm upgrade --install ...`,
naturally idempotent), the same "use the project's own official tooling
rather than reimplementing it" precedent spec 005 already set for K3s's
`get.k3s.io` install script. This makes `helm` an external tool
dependency of `serendip-agent`/`serendipd`'s host environment, not a new
Go library dependency (no ADR required under constitution rule 4's "new
Go dependency" trigger — it isn't one).

## Scope — IN

- **OpenChoreo control-plane + data-plane Helm install**, via `helm
  upgrade --install` against the OCI charts above, each into its own
  namespace (`openchoreo-control-plane`, `openchoreo-data-plane`).
  Single-cluster co-location — the control plane and data plane are the
  same K3s cluster, different namespaces, confirmed by oss-researcher as
  an officially documented, supported topology (not a workaround).
  **Version pinned at or above the CVE-2026-73843-patched baseline**
  (≥1.0.2 / ≥1.1.2 / ≥1.2.0 depending on release line — CVSS 9.6, an
  unauthenticated management API exposed on the same listener as
  data-plane agent connections, fixed in those versions) — exact version
  resolved in plan.md (open question below).
- **Default `DataPlane` custom resource registration.** Per
  oss-researcher, nothing can actually deploy through OpenChoreo until a
  `DataPlane` CR exists pointing at the (here, co-located) data-plane
  cluster — this spec creates that CR as part of foundation setup, the
  same "get it to a genuinely usable baseline, not just technically
  running" bar spec 008 held OpenBao's init/unseal to.
- **A minimal `providers/application/openchoreo` package** — enough to
  construct the `DataPlane` CR via the existing generic
  `providers/kubernetes.Client` (spec 006), and to verify the control
  plane's own Deployments (controller-manager, api-server) and the
  bundled `cert-manager`'s Pods report healthy/ready — mirrors spec
  008's `StatefulSet.status.readyReplicas` wait pattern, since
  OpenChoreo's control plane doesn't expose an OpenBao-style REST health
  endpoint to poll instead.
- **`cmd/serendipd` wiring**: idempotently run the Helm install(s),
  verify readiness, register a "reachable" marker under a new capability
  name (e.g. `"application-platform"`) in `core/provider.Registry` —
  gated the same way every other Kubernetes-facing wiring already is
  (`SERENDIP_KUBECONFIG` unset ⇒ skip, not fail). Reachable for a direct
  smoke-test only, same spirit as spec 006/008's own foundation-stage
  registrations before a real capability existed to consume them.
- **Confirm (don't assume) whether Thunder and/or kgateway are hard
  prerequisites for the control plane to start at all.** oss-researcher
  found this plausible-but-unconfirmed from documentation alone (ThunderID
  reads as bundled automatically by the control-plane chart; kgateway
  reads as a data-plane routing concern, not a control-plane-startup
  one) — this spec's real-cluster verification task is where this gets
  settled for real, the same "plan.md is a hypothesis, real-cluster
  testing finds the truth" discipline specs 007/008 already established
  twice. If either turns out to be a hard pre-req, stop and propose the
  scope change explicitly before silently pulling Thunder/kgateway
  deployment into this spec.
- **A real resource-footprint number for the control plane + data plane
  (no Build Plane, no Observability Plane) on whatever hardware this
  session's `kind`-cluster verification runs on** — ADR 0005's own
  numbers could not be re-confirmed from primary sources (fetch
  failures during research) and were already flagged as unverified
  when the ADR was written. This spec doesn't satisfy ADR 0005's actual
  T470 benchmark gate (that needs real T470 hardware, not the dev
  machine), but it should at least produce one real, current data point
  instead of continuing to cite unverified numbers.

## Scope — OUT (explicitly)

- **The `Application`/`Component` resource kinds, any
  `ApplicationPlatformProvider` core interface, and any reconciler** — a
  later spec's job, built on this spec's foundation, same split as
  specs 006→007 and 008→009.
- **The Build Plane** (Argo Workflows-based CI, ~8GB extra per ADR 0005)
  — explicitly deferred per ADR 0005's own mitigation; pre-built
  images/external CI is the assumed path until a profile has budget for
  it.
- **The Observability Plane** — not needed to prove the control plane
  is reachable; `capability-model.md`'s observability-backend default
  is separately unresolved anyway.
- **Deploying Thunder or kgateway as part of this spec**, unless
  real-cluster verification proves one is a genuine hard pre-req for
  the control plane to start (see Scope-IN) — not assumed in advance.
- **Any actual Project/Component/deployment through OpenChoreo** — this
  spec proves the control plane is up and minimally usable (a
  `DataPlane` registered), not that a real workload can be deployed
  through it yet. That needs the resource kinds this spec explicitly
  defers.
- **Multi-cluster / a separate data-plane cluster** — single-node
  `edge-1` target, single K3s cluster for both planes.
- **Any change to `api/`** — pure `providers/` + `cmd/serendipd` wiring,
  matching specs 006/008's precedent; no new resource kind exists yet
  for `api/` to expose.
- **Satisfying ADR 0005's T470 benchmark gate** — that needs real
  reference hardware, not this session's dev machine or a `kind`
  cluster; this spec produces a current data point, not the final
  benchmark.

## Acceptance criteria

1. `go test ./providers/application/openchoreo/...` green: whatever
   Go code this spec ships (DataPlane CR construction, readiness checks
   against the existing `providers/kubernetes.Client`) is table-driven-
   tested against a fixture HTTP server, no live cluster needed in CI —
   same discipline as every prior provider.
2. Against a real cluster (`kind`, this session's established
   preference — see `docs/PROGRESS.md`): both Helm charts install
   successfully (`helm upgrade --install` exits 0), every control-plane
   and data-plane Pod (including the bundled `cert-manager`) reaches
   `Running`/`Ready`, confirmed via `kubectl` directly, not just "the
   Helm command didn't error."
3. The default `DataPlane` CR exists and is accepted by the control
   plane (no `Failed`/error status on the object itself), confirmed via
   `kubectl get dataplane` or the equivalent API call.
4. The pinned OpenChoreo version is confirmed, by inspection of the
   actually-running images/chart version, to be at or above the
   CVE-2026-73843-patched baseline for whichever release line was
   chosen.
5. `serendipd` with `SERENDIP_KUBECONFIG` unset still starts
   successfully — the whole OpenChoreo wiring sequence is skipped, same
   gating as every other Kubernetes-facing piece (mirrors spec 008's
   T006 acceptance check).
6. `go vet ./...`, `gofmt -l .` clean; `check-imports.sh` confirms
   `core/` imports nothing under `providers/` and gained zero changes
   from this spec (no resource kind yet, so nothing in `core/` should
   need to change at all).
7. A written finding (plan.md or `docs/architecture/`) on whether
   Thunder/kgateway were actually required for the control plane to
   start, settled empirically against the real cluster in (2)-(3), not
   left as a guess.

## Open questions (resolve during plan review)

- **Exact OpenChoreo version to pin.** Newest is v1.3.1 (2026-10-01,
  includes the CVE fix and recent under-provisioned-default-limits
  fixes already); GitHub's "Latest release" tag instead points at
  v1.1.8, which may be a deliberate LTS/stable line rather than mere
  staleness. Resolve with a concrete decision (and reasoning) in
  plan.md, not left ambiguous.
- **Exact CRD surface.** oss-researcher confirmed sightings of
  `Organization`, `Project`, `Component`, `Environment`,
  `DataPlane`/`ClusterDataPlane`, `BuildPlane`/`ClusterBuildPlane`,
  `ClusterObservabilityPlane`, `ComponentType`, `Trait`, `Workflow` but
  could not confirm this is exhaustive, nor whether `DeploymentPipeline`
  is a standalone CRD kind. Pull the real list via `kubectl get crds`
  against an actual install during planning/implementation rather than
  assuming this list is complete.
- **Readiness-check mechanism specifics** — which exact Deployments/
  Pods this spec's health check watches (controller-manager, api-server,
  cert-manager's three pods, anything else the chart creates) needs the
  real chart's output inspected during planning, not guessed from docs
  alone.
- **Namespace-creation ordering and ownership** — whether
  `openchoreo-control-plane`/`openchoreo-data-plane` namespaces are
  created by the Helm charts themselves (`--create-namespace`) or need
  to be ensured by this provider first, consistent with how spec
  007/008 handle the shared `serendip-system` namespace.
