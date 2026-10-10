# Application Platform

This document describes the Application Platform capability
(`capability-model.md:46`) as it actually exists today: spec 010's
OpenChoreo deployment foundation only. ADR 0005 remains the authoritative
decision record for *why* OpenChoreo; ADR 0010 is the authoritative record
for *why this foundation shells out to `helm`* instead of following ADR
0002/0009's direct-object-construction precedent. This doc adds the
supporting detail those ADRs intentionally kept out of scope (the real
deployment shape, the disabled-by-default feature set, the CR this
foundation registers).

There is no real capability yet — no `Application`/`Component` resource
kind, no `ApplicationPlatformProvider` core interface, no reconciler. This
mirrors specs 005+006 (Kubernetes access foundation, consumed by spec 007)
and 008 (OpenBao deployment foundation, consumed by spec 009): spec 010 gets
a real OpenChoreo control plane and data plane running and reachable; a
later spec builds the actual capability on top of it.

## Deployment model

Three Helm releases, installed in order by `providers/application/
openchoreo.Deployer.Ensure` via the real `helm` CLI (ADR 0010 — a deliberate,
scoped exception to ADR 0002/0009's "no Helm" precedent):

1. **cert-manager** (`oci://quay.io/jetstack/charts/cert-manager`,
   `1.21.2`, namespace `cert-manager`, `--set crds.enabled=true`). A real
   prerequisite, confirmed by inspecting the pulled chart artifacts
   directly: neither OpenChoreo chart's `templates/` contains a single
   cert-manager controller/webhook/CRD template — only `Certificate`/
   `Issuer` *objects* that assume a real cert-manager controller is already
   running. spec.md originally assumed cert-manager was bundled by the
   control-plane chart; it is not.
2. **OpenChoreo control plane**
   (`oci://ghcr.io/openchoreo/helm-charts/openchoreo-control-plane`,
   `1.3.1`, namespace `openchoreo-control-plane`).
3. **OpenChoreo data plane**
   (`oci://ghcr.io/openchoreo/helm-charts/openchoreo-data-plane`, `1.3.1`,
   namespace `openchoreo-data-plane`).

Control plane and data plane are **co-located on one cluster** — the same
K3s cluster, different namespaces — confirmed by oss-researcher as an
officially documented, supported topology, not a workaround.
`--create-namespace` on every release creates each namespace; no shared
namespace is reused, unlike `serendip-system` (Storage/Secrets).

Each release runs with `--wait --timeout <5m|10m|10m>`, so readiness is
**Helm's own well-tested wait logic**, not a hand-rolled poll loop — a real
simplification versus `providers/secrets/openbao/deploy.go`'s
`waitStatefulSetReady`, which exists only because that spec built Kubernetes
objects directly with no Helm release to `--wait` on. `helm upgrade
--install` is itself idempotent, so `Ensure` has no separate GET-then-install
check the way spec 007/008's Deployers do — a second call against an
already-installed release at the same version is a cheap no-op Helm performs
internally. `NewDeployer` fails fast (`exec.LookPath("helm")`) if `helm`
isn't on `$PATH`, naming it a required host prerequisite, checked at
construction rather than discovered deep inside the first install call.

The control-plane chart has six Deployments
(`backstage`, `cluster-gateway`, `controller-manager`, `event-forwarder`,
`openchoreo-api`, `portal-assistant`), not the two spec.md originally
assumed. With `backstage`/`gateway` disabled (below), the Deployments this
foundation actually verifies ready are `cluster-gateway`,
`controller-manager`, `event-forwarder`, `openchoreo-api`, plus an `authz`
bootstrap `Job` (a Helm hook — runs once, reports `Complete`, already
covered by `--wait` on the control-plane release, not a Deployment). The
data-plane chart's only real Deployment with `gateway` disabled is
`cluster-agent-dataplane`. None of these names carry a Helm-release-name
prefix — confirmed against a real install, correcting an initial guess that
assumed one.

## Disabled-by-default feature set

Three flags are set on the OpenChoreo charts, each turning off a real
dependency this foundation spec explicitly defers rather than removes —
confirmed via the charts' own `validate.yaml`/`_helpers.tpl` validation
logic (which hard-fails `helm install` on a placeholder-domain default),
not assumed:

- **`security.enabled=false`** (control plane). The default,
  `security.enabled=true`, hard-fails validation because
  `security.oidc.issuer` defaults to the placeholder domain
  `http://thunder.openchoreo.invalid`. Thunder is not a hard prerequisite for
  the control plane to start — confirmed empirically, not inferred from
  docs. Real Thunder-backed auth is a future Identity spec's job.
- **`backstage.enabled=false`** (control plane). Backstage (the
  developer-portal UI) defaults to enabled and additionally requires a
  credentials Secret (`backend-secret`, `client-secret`, `jenkins-api-key`)
  and non-placeholder hostnames/`baseUrl` — none of which this spec can
  meaningfully provide, since there's nothing to show until `Project`/
  `Component` CRs exist. Deferred for "no consumer yet," the same reason as
  the Build Plane (ADR 0005), not a resource-footprint decision.
- **`gateway.enabled=false`** (both charts), **plus
  `gateway.tls.enabled=false`** (data plane only). This `gateway` block
  creates Gateway API `Gateway` objects referencing
  `gatewayClassName: "kgateway"` for **application traffic routing** — a
  data-plane/runtime concern with nothing to route to yet (no `Component`
  exists). The control-plane chart's placeholder-domain check is gated on
  `gateway.enabled AND gateway.tls.enabled` together, so
  `gateway.enabled=false` alone suffices there; the data-plane chart's
  equivalent check (`_helpers.tpl`'s `validateHostnames`) is gated on
  `gateway.tls.enabled` **alone**, independent of `gateway.enabled` — a real
  discrepancy found during real-cluster verification, not assumed from
  planning, so the data-plane `helm` invocation sets both flags explicitly.
  kgateway deployment is deferred entirely to whichever future spec actually
  deploys a workload. (OpenChoreo's own internal `clusterGateway` — the
  control-plane/data-plane agent connection below — is a separate, always-on
  component with cluster-internal DNS names that never hit this check; it is
  not kgateway and this spec does not touch its defaults.)

All three are confirmed-safe, first-class chart toggles, not hacks — the
same "intentionally weak, temporary, documented" pattern this codebase
already uses for `SERENDIP_API_KEYS` (`api/middleware.go`).

## `DataPlane` CR registration and CA trust bootstrapping

Nothing can actually deploy through OpenChoreo until a `DataPlane` custom
resource exists pointing at the (here, co-located) data-plane cluster.
`providers/application/openchoreo.EnsureDefaultDataPlane` idempotently
GET-then-Creates exactly one, named `default`, in the
**`openchoreo-control-plane`** namespace (confirmed correct against a real
cluster — the control plane's `controller-manager` accepted it and updated
`status.conditions` to `Created: True`):

```json
{
  "apiVersion": "openchoreo.dev/v1alpha1",
  "kind": "DataPlane",
  "metadata": {"name": "default", "namespace": "openchoreo-control-plane"},
  "spec": {
    "clusterAgent": {
      "clientCA": {
        "secretKeyRef": {
          "name": "cluster-agent-tls",
          "namespace": "openchoreo-data-plane",
          "key": "ca.crt"
        }
      }
    }
  }
}
```

Constructed directly via the existing generic `providers/kubernetes.Client`
(spec 006) — not by shelling out to OpenChoreo's own
`add-default-dataplane.sh` script, spec.md's original assumption. Once the
CRD schema and the cert Secret's real name/namespace/key were traced
directly from the pulled chart artifacts, hand-construction turned out to be
exactly as reliable as the script, and avoids a second external-script
dependency beyond `helm` itself.

**The real, confirmed CA-trust gap this foundation works around**: the
control-plane chart's own comment promises a "CA extractor hook job" that
populates a `cluster-gateway-ca` ConfigMap with the real cert-manager-issued
CA, so the data plane's `cluster-agent` can trust the control plane's
`cluster-gateway`. No such job exists anywhere in the chart — grepping every
template for `kind: Job` finds exactly one Job (the unrelated authz
bootstrap job), and `controller-manager`'s own logs show zero CA-sync
activity. The ConfigMap the chart does create
(`templates/cluster-gateway/ca-configmap-placeholder.yaml`) contains a
literal, permanent placeholder string. The real CA sits the whole time in a
`cluster-gateway-ca` **Secret** (type `kubernetes.io/tls`) the chart never
reads back out — and separately, that ConfigMap is namespace-scoped, so even
single-cluster co-location doesn't make it visible across namespaces for
free.

`Deployer.ensureDataPlaneTrustsControlPlane` is inserted between the
control-plane and data-plane `helm` calls to fix this: it ensures the
`openchoreo-data-plane` namespace exists, reads the real CA directly out of
the control plane's `cluster-gateway-ca` Secret, and creates the data
plane's own `cluster-gateway-ca` **ConfigMap** with that real value —
bypassing the chart's broken placeholder mechanism entirely rather than
waiting on something that doesn't exist. Confirmed working end-to-end
against a real cluster: the data plane's `cluster-agent` logged "server CA
certificate loaded successfully" and "connected to control plane," and the
`DataPlane` CR's own `status.agentConnection` showed `connected: true,
connectedAgents: 1`.

## `CheckReady` retry behavior

`providers/application/openchoreo.CheckReady` is a second, independent
confirmation beyond trusting Helm's own `--wait` exit code — it GETs each
Deployment in `expectedDeployments` and confirms `status.readyReplicas >= 1`.
It retries the whole check up to **3 attempts, 2 seconds apart**, added
after a `go-reviewer` finding during the final review pass: a single GET
immediately after Helm's own `--wait` already reported every Deployment
ready can still race a brief apiserver/informer-cache lag, and `CheckReady`'s
error propagates all the way up through `wireOpenChoreo` /
`wireKubernetes` / `run()` to `os.Exit(1)` — a transient blip would
otherwise kill the whole `serendipd` daemon, not just this one
(reconciler-less, smoke-test-only) capability. Three attempts absorbs that
class of blip without meaningfully slowing the genuine-failure case (a truly
broken Deployment still fails after ~4 seconds, not hangs).

## Registration

`wireOpenChoreo` (`cmd/serendipd/main.go`, called from `wireKubernetes`
after `wireOpenBao`) runs `Deployer.Ensure`, then
`EnsureDefaultDataPlane`, then `CheckReady`, then registers
`openchoreo.Marker{}` — a placeholder value with no methods, nothing to
type-assert against yet — under `"application-platform"` in
`core/provider.Registry`. Reachable for a direct smoke-test only, the same
spirit as every prior foundation spec's own registration (spec 006's
`nodeLister`, spec 008's bare `*openbao.Client`). Gated the same way every
other Kubernetes-facing wiring already is: `SERENDIP_KUBECONFIG` unset ⇒ the
whole sequence is skipped, not failed.

## What does not exist yet

Per spec.md's Scope-OUT, explicitly deferred to a later spec, not silently
dropped:

- **The `Application`/`Component` resource kinds, any
  `ApplicationPlatformProvider` core interface, and any reconciler** — the
  split this document opens with. A later spec builds the real capability
  on top of this foundation, the same split as specs 006→007 and 008→009.
- **The Build Plane** (Argo Workflows-based CI) — per ADR 0005's own
  mitigation; pre-built images/external CI is the assumed path until a
  profile has budget for it.
- **The Observability Plane.**
- **Thunder and kgateway deployment** — both confirmed not hard
  prerequisites (above); deploying either is a future spec's job once
  something exists for them to serve.
- **Any change to `api/`** — pure `providers/` + `cmd/serendipd` wiring; no
  resource kind exists yet for `api/` to expose.

## Open questions

- **Resource footprint is a real, current dev-machine data point, not a
  T470 benchmark.** ADR 0005's own numbers were already flagged unverified
  when written; this spec produces one real number on whatever hardware its
  `kind`-cluster verification ran on, but does not satisfy ADR 0005's T470
  benchmark gate — that needs real reference hardware.
- **CNCF Sandbox governance / WSO2 commit concentration** — standing watch
  item per ADR 0005, not re-litigated here.
