# Architecture Documentation

Source of truth for Serendip Infra design. Each file should be readable
standalone in under ~5 minutes and contain decisions, not exploration.

## Files (to be extracted from the founding design discussions)

| File | Contents | Status |
|------|----------|--------|
| `overview.md` | What Serendip is, positioning, the planes, build-vs-adopt table | TODO |
| `resource-model.md` | Org → Team → Project → Environment → Application → Component; Site/Cluster/Node; Network/Database/Storage; Incident/Alert. Fields, relationships, status conventions | TODO |
| `capability-model.md` | Capability list, capability spec format, default providers per capability | TODO |
| `provider-model.md` | Provider interface contracts (Go), registration, lifecycle, replacement, "bring your own capability" | TODO |
| `deployment-profiles.md` | edge-1 / small-3 / standard-5 / private-cloud; what each enables; upgrade paths between them; topology planner behavior | TODO |
| `storage.md` | Local → Longhorn(2 replicas) → external progression; primary vs backup storage separation; storage classes/policies; hardware-aware placement | TODO |
| `bootstrap-agent.md` | serendip-agent design: lives outside Kubernetes; hardware discovery, enrollment, bootstrap, emergency diagnostics | TODO |
| `operations.md` | Alert → Incident → Diagnosis → Recommendation → Approval → Action → Verification → Postmortem lifecycle; SLO/error budgets; change intelligence | TODO |
| `ai-operations.md` | Severity-based autonomy matrix, approval engine, what context AI receives, MCP surface | TODO |
| `identity.md` | Three identities (human, platform, workload); workload identity abstraction over Entra/Keycloak/Thunder/ServiceAccount | TODO |

## Extraction task

The raw design material lives in the founding conversation exports. First task
of the docs-keeper agent: extract each file above from that material,
preserving decisions and dropping exploration. Every "we decided X" becomes a
statement; every "we should evaluate X vs Y" becomes an open question section
at the bottom of the relevant file or an ADR stub.
