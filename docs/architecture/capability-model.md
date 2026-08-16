# Capability model

Serendip's domain speaks in capabilities, never products (constitution 1).
This document is the capability catalog: what capabilities exist, the spec
format that defines one, and the default provider chosen for each. Vendor
names below are defaults and examples — never the organizing principle.

## Capability spec format

A capability declaration (registered in the Capability Registry, see
`provider-model.md`) has this shape:

```yaml
capability:
  name: identity
  required: true          # can the platform run at all without it?
  providers:
    - thunder
    - keycloak
    - external-oidc
  default: thunder
```

Fields:
- **name** — the capability, e.g. `identity`, `secrets`, `observability`.
- **required** — whether a Serendip install is functional without it.
- **providers** — registered implementations available for this capability.
- **default** — which provider a fresh install picks absent user choice.

Users change `default` per Environment or Organization without touching
Serendip's own code — that's the entire point of the registry (constitution
1: "every capability has ... at least one provider ... and an `external`
escape hatch").

## Capability catalog

| Capability | Required | Default provider | Alternative / optional providers |
|---|---|---|---|
| Identity | Yes | Thunder (ADR 0006) | Keycloak, external OIDC/Entra ID |
| Secrets & PKI | Yes | OpenBao | External Secrets Operator + external vault |
| Certificates | Yes | cert-manager | — |
| Networking (CNI) | Yes | Cilium | — |
| Gateway / ingress | Yes | kgateway | WSO2 API Manager (advanced API management) |
| Compute (Kubernetes) | Yes | K3s (ADR 0001) | RKE2 (future, hardened profiles) |
| Cluster management | No | Rancher | — (native single-cluster mgmt below 3+ nodes) |
| Application platform | Yes | OpenChoreo (ADR 0005) | KubeVela + Crossplane + Argo (evaluated, not selected) |
| Infrastructure provisioning | No | Crossplane | Terraform/OpenTofu |
| GitOps delivery | Yes | Argo CD / Argo Workflows | — |
| Storage (primary) | Yes | local-path → Longhorn | External CSI (ADR 0002) |
| Storage (backup) | No | S3-compatible / NFS | — (never counted as primary redundancy) |
| Database | No | CloudNativePG (PostgreSQL) | Embedded, managed/external PostgreSQL |
| Observability (telemetry collection) | Yes | OpenTelemetry | — |
| Observability (backend) | Yes | *unresolved* | OpenObserve, SigNoz, OpenSearch — see Open questions |
| Ticketing / ITSM | No | Native Serendip Incidents | Zammad, GLPI, ServiceNow, Jira |
| API analytics | No | Native (via kgateway/Envoy + OTel) | Moesif adapter |
| Compute (VM) | No | *not v1* | Harvester (KubeVirt) — see `deployment-profiles.md` |
| AI inference | Yes | *unresolved* | See `ai-operations.md` open questions |

Capabilities marked "Required: Yes" must have at least a working default at
`edge-1` (see `deployment-profiles.md`); "No" capabilities are enabled only
when the topology or the user justifies them (e.g. cluster management below
3 nodes has nothing to manage).

## Native vs. external split (operations-adjacent capabilities)

A few capabilities are intentionally Serendip-native rather than delegated
to a single external product, because they are the cross-cutting layer that
makes the rest of the platform coherent:

| Native (built) | External (optional integration) |
|---|---|
| Alerting, SLO/SLI, error budgets | — (visualized via observability backend, semantics owned natively) |
| Incident management, runbooks, postmortems | ServiceNow, Jira, PagerDuty, Zammad, GLPI |
| Change intelligence, resource/service graph | — |
| AI diagnosis/recommendation/approval | — |

This split is deliberate: Serendip owns the *semantics* (what an Incident is,
what counts as a change, how SLO burn rate is computed) while letting the
*storage/UI* of tickets be swapped. See `operations.md`.

## Bring your own capability

Every capability plans for an `external` provider: point Serendip at
something the user already runs (an existing Keycloak, an existing Grafana
stack, an existing ServiceNow instance) instead of the bundled default.
Serendip's job in that case shrinks to correlation/abstraction rather than
hosting the implementation.

## Open questions

- **Observability backend default** is explicitly unresolved pending a
  benchmark of OpenObserve, SigNoz, and OpenSearch on reference (T470)
  hardware. Do not treat any of the three as "the default" until that
  benchmark is recorded.
- **AI inference backend** (local model runtime + model choice) was only
  discussed speculatively (e.g. Ollama + a Qwen-class model as one example)
  and was never settled. See `ai-operations.md`.
- ~~Application platform (OpenChoreo) and Identity (Thunder) defaults lacked
  ADRs~~ — resolved: see ADR 0005 and ADR 0006. Both carry explicit
  risk/fallback conditions (governance, resource footprint, maturity)
  rather than being unconditional defaults.
