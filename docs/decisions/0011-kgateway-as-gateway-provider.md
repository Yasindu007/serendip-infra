# ADR 0011: kgateway as the default Gateway provider

Status: Accepted
Date: 2026-10-11

## Context

`capability-model.md` lists Gateway/ingress as a required capability with
kgateway as its default, but no ADR ever examined that choice. Spec 012
makes it real: Components deployed through the Application Platform
(spec 011) must be reachable from outside the cluster.

Constraints:

- OpenChoreo (ADR 0005) renders HTTPRoutes itself, from Workload endpoint
  visibility. It targets a Gateway API `Gateway` named in
  `DataPlane.spec.gateway`, and its charts and sample component types
  assume the `kgateway` GatewayClass. Whatever we pick must be a
  conformant Gateway API implementation.
- edge-1 runs on a T470-class laptop (constitution rule 4). Every new
  always-on component counts.
- K3s (ADR 0001, pinned v1.36.4) ships Traefik plus ServiceLB, which hold
  node ports 80/443 (spec 005 deliberately left them enabled).
- Spec 011 installs Gateway API **v1.3.0** standard-channel CRDs as an
  OpenChoreo prerequisite.

Facts below come from an oss-researcher pass on 2026-10-11 (kgateway
docs, versions matrix, release notes, CNCF page, K3s and kind docs).
Items marked *(unconfirmed)* could not be verified from reachable
sources.

## Governance and maturity

- **CNCF Sandbox**, accepted 2025-03-04. It is the open-source
  continuation of Solo.io's Gloo Gateway. No incubation application was
  found.
- **License:** Apache-2.0 *(unconfirmed in this pass; consistent with CNCF
  Sandbox requirements)*.
- **Releases:** a new minor roughly every 2–3 months, patches every 1–3
  weeks, and two minors patched in parallel. As of 2026-10-11 the latest
  stable is v2.4.6 (2026-10-06); v2.3.10 was released the same day, and
  v2.5.0-beta.1 is tagged.
- **Vendor concentration:** Solo.io originated the project and very likely
  still dominates maintainership *(unconfirmed: MAINTAINERS.md was
  unreachable)*. The same watch item ADR 0005 records for WSO2/OpenChoreo.
- **Architecture:** Go control plane, Envoy data plane, a straightforward
  fit for the stack. No JVM, no database.

## Decision

1. **kgateway v2.4.6** is the default Gateway provider.
2. **Bump the pinned Gateway API CRDs from v1.3.0 to v1.6.1**
   (standard channel), forced by the versions matrix:

   | kgateway | Kubernetes (tested) | Gateway API |
   |---|---|---|
   | 2.4.x | 1.32–1.36 | 1.4–1.6 |
   | 2.3.x | 1.31–1.35 | 1.3–1.5 |

   - 2.3.x would keep spec 011's v1.3.0 CRDs. But it is untested on our
     real K3s pin (1.36) and leaves patch support once 2.5 goes GA.
   - 2.4.x matches the Kubernetes target, so the CRD bump is the cheaper
     long-term choice.
   - Spec 011's CRD installer becomes upgrade-only: older bundle versions
     are upgraded, newer ones never downgraded. That keeps clusters set up
     under spec 011 working.
3. **Install via kgateway's first-party OCI Helm charts** (`kgateway-crds`,
   then `kgateway`, namespace `kgateway-system`), through the same `helm`
   shellout as ADR 0010.
   - This rests on kgateway's own merits, not on ADR 0010 setting a
     precedent. Helm is the project's documented install path, the chart
     owns the CRDs, RBAC, controller, and GatewayClass, and `helm upgrade
     --install --wait` gives tested idempotency and readiness.
   - Hand-building that object graph would repeat the risk ADR 0010
     rejected for OpenChoreo.
   - Storage and Secrets stay direct-object (ADR 0002/0009). This is still
     a per-capability call.
4. **Experimental Gateway API features off.** kgateway ≥2.2 enables
   them by default (`KGW_ENABLE_EXPERIMENTAL_GATEWAY_API_FEATURES`), but
   only standard-channel CRDs are installed. Starting the controller
   against experimental kinds that don't exist is avoidable risk.
5. **One shared Gateway, exposed as a NodePort (default 30080)** via
   kgateway's `GatewayParameters`.
   - It is not a LoadBalancer on port 80. That would contend with K3s's
     Traefik/ServiceLB for 80/443: a second LoadBalancer Service on port
     80 stays Pending.
   - A NodePort never touches ServiceLB. So no `agent/bootstrap` change,
     no migration for existing clusters, and the same mechanism on kind
     (via `extraPortMappings`) and on K3s.
   - Serving on 80/443 by disabling Traefik is a deliberate later step,
     with its own migration story.

## Consequences

- External URLs carry the NodePort (`…:30080`).
- Traefik keeps running unused on K3s installs, a known footprint cost to
  weigh in the T470 benchmark.
- Every kgateway minor narrows its supported Gateway API range, so
  upgrading kgateway can force another CRD bump. Pin both together and
  bump them in the same change.
- One more always-on controller plus one Envoy Deployment on edge-1.
- **Footprint:** the docs publish no idle numbers, and the chart sets no
  controller resource requests. The real figure is measured during spec
  012's real-cluster verification and appended below.

## Fallback conditions

Revisit this default if any of these become true:

- kgateway stalls in Sandbox, or loses multi-vendor maintainership in a
  way that threatens patch support.
- Its idle footprint (controller plus proxy) is disproportionate for
  edge-1 once measured on reference hardware.
- Gateway API compatibility churn forces CRD bumps faster than other
  installed consumers (OpenChoreo) can follow.

Fallbacks:

- **Envoy Gateway** (CNCF, Envoy data plane, Gateway API conformant).
- **Cilium's Gateway API implementation**, once a Cilium networking
  provider exists. It would avoid a separate data plane entirely.

OpenChoreo only needs a conformant Gateway it can reference by name. A
swap means changing this provider and the component types' expectations
of the class name, not the Application Platform design.

## Measured footprint

Spec 012 T009, measured 2026-10-11 on a kind cluster (Kubernetes 1.34,
dev machine, Docker Desktop, 8 GB). This is not the T470 benchmark.

| Component | Memory (idle) | CPU (idle) |
|---|---|---|
| kgateway controller | ~50 MB | ~0.2% |
| Envoy proxy (one Gateway) | ~19 MB | ~0.8% |
| **Gateway capability total** | **~70 MB** | **~1%** |

For context: the whole kind node idled at about 1.67 GiB with the full
stack running (OpenBao, cert-manager, OpenChoreo control and data planes,
kgateway). `serendipd` itself used about 21 MB.

## Real-cluster findings (spec 012 T009)

- **Confirmed:**
  - The controller creates GatewayClass `kgateway` at runtime; the chart
    doesn't render it.
  - `spec.infrastructure.parametersRef` links a Gateway to its
    GatewayParameters.
  - `Programmed` becomes True with a NodePort Service.
  - The Gateway API CRD upgrade from v1.3.0 to v1.6.1 by merge patch works
    in place, including upstream's `safe-upgrades` ValidatingAdmissionPolicy
    (now installed alongside the CRDs).
- **The application platform locks workloads down.** OpenChoreo gives every
  Component a NetworkPolicy that admits traffic on exposed ports only from
  same-namespace pods, or from pods labeled
  `openchoreo.dev/system-component`. Without that label on the proxy pods,
  routes were Accepted and Programmed but every request timed out (503).
  - The kgateway provider now takes proxy pod labels from the composition
    root and sets `spec.infrastructure.labels`. The OpenChoreo provider
    supplies the label it requires, so neither provider names the other.
  - Any future gateway provider, including the fallbacks above, must support
    labeling its proxy pods the same way.
