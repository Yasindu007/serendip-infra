# Architecture Documentation

Source of truth for Serendip Infra design. Each file should be readable
standalone in under ~5 minutes and contain decisions, not exploration.

## Files

| File | Contents | Status |
|------|----------|--------|
| `overview.md` | What Serendip is, positioning, the planes, build-vs-adopt table | Done |
| `resource-model.md` | Org → Team → Project, which owns Applications (→ Component) and Environments (→ Deployment, spec 020, ADR 0019); Site/Cluster/Node; Network/Database/Storage; Incident/Alert. Fields, relationships, status conventions | Done |
| `capability-model.md` | Capability list, capability spec format, default providers per capability | Done |
| `provider-model.md` | Provider interface contracts (Go), registration, lifecycle, replacement, "bring your own capability" | Done |
| `deployment-profiles.md` | edge-1 / small-3 / standard-5 / private-cloud; what each enables; upgrade paths between them; topology planner behavior | Done |
| `reconciliation.md` | Reconcile engine model: events, resync, failed-retry, early re-check (`RequeueAfter`), foundations re-ensure loop, degraded startup, readiness API | Done |
| `storage.md` | Local → Longhorn(2 replicas) → external progression; primary vs backup storage separation; storage classes/policies; hardware-aware placement | Done |
| `bootstrap-agent.md` | serendip-agent design: lives outside Kubernetes; hardware discovery, enrollment, bootstrap, emergency diagnostics | Done |
| `operations.md` | Alert → Incident → Diagnosis → Recommendation → Approval → Action → Verification → Postmortem lifecycle; SLO/error budgets; change intelligence | Done |
| `ai-operations.md` | Severity-based autonomy matrix, approval engine, what context AI receives, MCP surface (the `serendip` CLI, `serendip mcp`, `/mcp`; spec 021, ADRs 0017 and 0018; reference in `../cli-and-mcp.md`) | Done |
| `identity.md` | Three identities (human, platform, workload); workload identity abstraction over Entra/Keycloak/Thunder/ServiceAccount; subjects and groups for authorization; agent identities; CLI sign-in and delegated AI assistants (`Via`) | Done |
| `application-platform.md` | The Application Platform capability on OpenChoreo: object mapping, conditions, exposure, and spec 020's environments, Deployments, promotion, runtime view, schemas, dependencies, platform view and packages (ADRs 0005, 0010, 0019) | Done |
| `approvals.md` | The `Approval` kind: a record for every destructive action, decided by policy unless it needs a person; phases, conditions, routes, who may decide, execution with stale checks, clean-up and verification, audit, the console (ADR 0016) | Done |
| `authorization.md` | Azure-RBAC model (ADR 0014): roles at scopes, inheritance, data actions, constrained delegation; enforcement in `api/authz.go`; evaluation; bootstrap and break-glass; the console's Access control (IAM) | Done |

Extracted from the founding design discussions (`docs/raw/nexus.md`,
`docs/raw/other.md` — retained as historical source; the project was called
"Nexus" during those conversations and was renamed to Serendip Infra
afterward, so the raw files still say "Nexus" throughout). Each doc above
states decisions as decisions; unresolved items are called out in that
file's "Open questions" section rather than guessed at. A handful of
foundational default-provider choices (see `overview.md` and
`capability-model.md` Open questions) are flagged as candidates for their
own ADRs rather than assumed settled by this extraction.
