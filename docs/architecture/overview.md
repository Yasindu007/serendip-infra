# Overview

Serendip Infra is an open-source, self-hostable **platform operating system**:
one control layer (resource model, capability/provider registries,
reconciliation, AI operations) over composable OSS infrastructure. It gives a
small team the operational capabilities of a cloud provider — identity,
secrets, networking, storage, observability, incident management, API
management — without cloud-provider scale or cost.

Positioning: *"an open-source, self-hostable platform operating system for
running applications, infrastructure, and DevOps operations on your own
hardware or cloud."* Not "an AWS/Azure clone" — that framing sets the wrong
expectations (billing, regions, IAM parity). The relevant story is **"from
three old laptops to a multi-node private cloud, without reinstalling."**

Reference deployment target and hardware-scaling story live in
`deployment-profiles.md`. This file covers what Serendip *is* and how its
scope is split between what we build and what we adopt.

## Design principle

> Serendip defines capabilities. It chooses a default implementation for
> each. Users can replace any implementation ("bring your own capability").

This is constitution rule 1 (capability first, vendor second) applied at the
product level. See `capability-model.md` and `provider-model.md` for the
mechanics.

## The planes

Serendip is organized into logical planes. Each plane maps loosely to a
`core/`, `api/`, `agent/`, or `providers/` package group — see the repo
`CLAUDE.md` for the literal directory layout.

| Plane | Owns | Serendip-native or adopted |
|---|---|---|
| Experience | Web UI, CLI, API, MCP — one resource model behind all four (constitution 7) | Native |
| Control | Resource model, capability/provider registries, resource graph, reconciliation, policy, RBAC, audit, event bus | Native |
| Identity & Security | Human/platform/workload identity, secrets, PKI, certificates | Adopted providers behind a native abstraction — see `identity.md` |
| Application Platform | Application/component deployment, GitOps, build/registry | Adopted providers behind a native abstraction |
| Infrastructure | Cluster lifecycle, networking, compute/VM, storage provisioning | Adopted providers behind a native abstraction |
| Observability | Logs, metrics, traces, dashboards | Adopted providers, OpenTelemetry as the common collection layer |
| Operations | Incidents, alerts, SLOs, runbooks, postmortems, change intelligence | Native — see `operations.md` |
| AI Operations | Diagnosis, recommendation, approval-gated action | Native — see `ai-operations.md` |

## Build vs. adopt

Scope discipline (constitution 10) is the filter: build only what is
genuinely Serendip-specific; adopt everything else.

### Build ourselves (Serendip-native)

Resource model · capability registry · provider registry · resource graph ·
reconciliation engine · policy engine · RBAC model · audit model · event bus ·
installer/profile engine · topology planner · bootstrap agent · incident
correlation engine · AI operations engine · AI approval system · unified UI ·
CLI · MCP · unified API.

### Adopt (default OSS dependencies, one per capability)

| Capability | Default provider | Notes |
|---|---|---|
| Kubernetes distribution | K3s | ADR 0001 |
| Application platform | OpenChoreo | Application/component deployment, GitOps. ADR 0005 |
| Cluster management (multi-cluster) | Rancher | Enabled once useful (3+ nodes / multi-cluster); not required at edge-1 |
| Infrastructure provisioning | Crossplane | Composition/reconciliation engine for non-K8s infra |
| Identity | Thunder | Go-based, avoids the Java-heavy WSO2 Identity Server / Keycloak footprint. ADR 0006 |
| Secrets / PKI | OpenBao | Secrets, encryption, dynamic credentials, PKI |
| GitOps delivery | Argo (CD/Workflows) | |
| Networking (CNI) | Cilium | |
| Gateway / ingress | kgateway | Gateway API implementation |
| Certificates | cert-manager | |
| Telemetry collection | OpenTelemetry | Common pipe regardless of backend |
| Observability backend | *unresolved* | OpenObserve vs. SigNoz vs. OpenSearch — pending on-hardware (T470) benchmark. See Open questions |
| Primary/distributed storage | local-path → Longhorn | ADR 0002 |
| Database (user workloads) | CloudNativePG (PostgreSQL) | Distinct from the control plane's own SQLite store (ADR 0003) |
| VM/compute (later) | Harvester (KubeVirt) | Not v1 — see `deployment-profiles.md` |

### Optional integrations (bring-your-own-capability, never required)

WSO2 API Manager (advanced API management), GitHub/GitLab/Jenkins (external
CI), external Kubernetes, AWS/Azure/GCP, ServiceNow/Jira/PagerDuty, Zammad/GLPI
(ticketing/ITSM), an already-running observability stack, an already-running
identity provider.

### Explicitly out of scope (constitution 10)

Serendip does not build a database engine, a Kubernetes distribution, a
secrets engine, a metrics TSDB, a service mesh, a Git server, a CI engine, an
API gateway, or a VM hypervisor. It integrates those.

## Open questions

- **Observability backend default** (OpenObserve vs. SigNoz vs. OpenSearch)
  is explicitly unresolved in the source design discussion — the stated plan
  is to benchmark all three on reference (T470) hardware before locking a
  default, not to guess now.
- ~~OpenChoreo and Thunder defaults lacked dedicated ADRs~~ — resolved:
  see ADR 0005 (OpenChoreo) and ADR 0006 (Thunder). Both carry explicit
  risk/fallback conditions rather than being unconditional.
