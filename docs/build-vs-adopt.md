# Built, adopted, rejected

What Serendip builds itself, what it adopts from other projects, and what
it considered and turned down, with the reason and where the decision is
recorded. Constitution rule 10 sets the frame: Serendip builds the control
layer (resource model, registries, reconciliation, policy, audit, the
console, CLI and MCP) and integrates everything else.

- **Add a row** whenever a spec adopts something, rejects an alternative
  worth remembering, or decides to build instead of adopt.
- **A reversal gets a new row,** linking the old one, which stays.
- **Accepted risks and costs** of these choices live in
  `docs/risk-register.md`.

Last updated: 2026-10-21.

## Built (Serendip's own code)

| What | Why built, not adopted | Where |
|---|---|---|
| Resource model, graph, store | The core of the product; nothing off the shelf fits the capability model | Spec 001, ADR 0003 |
| HTTP API | Standard library `net/http`; a framework buys little | Spec 002 |
| Reconciliation engine | Small, single goroutine, sized for edge-1 | Spec 003, spec 014 |
| Bootstrap agent | Node enrolment outside Kubernetes | Spec 004, spec 005 |
| Kubernetes client | A small REST client; `client-go` is too heavy (constitution 4) | Spec 006 |
| Port-forward over WebSocket | Stdlib client for the identity provider's management channel; Service proxy and in-cluster helpers rejected | ADR 0015 |
| OIDC verification | Stdlib JWT and JWKS (RS256, ES256) | Spec 015 |
| Authorization (Azure-style RBAC) | Serendip's own policy engine; the model is Azure's | Spec 017, ADR 0014 |
| Approvals | Every destructive action's record, decided by policy or a person | Spec 019, ADR 0016 |
| Web console | React and Vite, our own design system, two runtime dependencies | Spec 016, ADR 0012 |
| OpenChoreo portal features | Rebuilt in our console instead of Backstage | Spec 020 |
| CLI | `cmd/serendip`, a client of the API | Spec 021 |
| Operation catalogue | `api/catalog`: one list of actions the CLI and MCP both build from, with a route parity test | Spec 021 |
| Benchmark harness | `cmd/bench-edge1` | Spec 013 |
| Pipeline enforcement | The platform doesn't check promotion order, so Serendip does (`resource.AdmitRelease`) on every deploy and promotion | Spec 020, ADR 0019 |
| Releases and bindings | Serendip writes every release and binding with the platform's auto-deploy off: one writer per binding, and Serendip's overrides never compete with it | Spec 020, ADR 0019 |
| Log following | NDJSON over `fetch`, with caps, idle and maximum timers; the console reconnects | Spec 020 plan decision 8 |
| JSON Schema validation | `core/jsonschema`: the platform's schemas use 7 keywords. It's about 300 lines of stdlib, fails closed on anything else, and refuses unknown settings the platform would silently drop | Spec 020 T008 |

## Adopted (other projects, integrated)

| What | For | Where | Notes |
|---|---|---|---|
| K3s | Compute (Kubernetes) | ADR 0001 | Bootstrap distribution |
| Go, SQLite (`modernc.org/sqlite`) | Language, default store | ADR 0003 | Pure Go, no CGO |
| `google/uuid` | Resource IDs (UUIDv7) | ADR 0007 | |
| `sigs.k8s.io/yaml` | Kubeconfig parsing | ADR 0008 | |
| OpenChoreo (control and data planes) | Application platform | ADR 0005 | Driven through its CRDs directly, not its API (spec 020) |
| `helm` (shelled out) | Installing OpenChoreo | ADR 0010 | Per-capability exception |
| ThunderID | Identity (OIDC) | ADR 0006 | Also the agent issuer in WSO2 Agent Manager (spec 018). Its token exchange (RFC 8693) carries a person's assistant (spec 021) |
| OpenBao | Secrets and PKI | ADR 0009 | |
| kgateway | Gateway | ADR 0011 | |
| local-path provisioner | Storage (edge-1) | ADR 0002 | Longhorn at 3 nodes |
| cert-manager | OpenChoreo dependency | Spec 010 | |
| WSO2 Agent Manager | AI agent identities (POC, beside Serendip) | Spec 018 | Not part of edge-1 |
| MCP Go SDK (`modelcontextprotocol/go-sdk` v1.8.0) | MCP protocol (stdio and streamable HTTP) | ADR 0017, spec 021 | Chosen over hand-written, to avoid maintaining protocol churn ourselves. 9 modules, at most +2.4 MB; confined to `mcp/` (its `golang.org/x/sys` is also used directly by the CLI for an owner-only folder ACL on Windows) |

## Rejected (considered and turned down)

| What | Instead | Why | Where |
|---|---|---|---|
| Backstage (OpenChoreo's portal) | Our own console | Up to 2 GiB and hundreds of Node packages, and it would call OpenChoreo's API directly, bypassing Serendip's RBAC, approvals and audit | Spec 020 |
| OpenChoreo's API (`openchoreo-api`) as Serendip's path | The Kubernetes API, directly | A new admin credential, a second authorization layer, and extra hops; it's closed to all pods (RP-018) | Spec 020 plan decision 3, ADR 0019 |
| OpenChoreo's `occ` CLI and MCP | Serendip's CLI and MCP | They bypass Serendip's RBAC, approvals and audit; an admin-only escape hatch is deferred | Spec 021 |
| `client-go` | Our small REST client | Weight (constitution 4) | Spec 006 |
| Service proxy, or an in-cluster helper, for the identity provider's management API | In-process port-forward | The proxy strips `Authorization`; a helper is another image to maintain | ADR 0015 |
| SPDY-over-WebSocket port-forward (KEP-4006) | Legacy WebSocket mode | Needs a SPDY implementation (RP-004 watches this) | ADR 0015 |
| A 428 "approval required" on every DELETE | Policy-approved Approvals | An approval for everything is a hassle; roles decide | Spec 019 |
| An Approval record written after the delete | In the same transaction | Not atomic | Spec 019, ADR 0016 |
| SQLite `synchronous = NORMAL` | `FULL` | Power loss could resurrect deletes and revoked access | RP-001 |
| One Application per Environment | Deployments into environments (Azure-style) | Fights OpenChoreo's model; duplicates configuration | Spec 020 plan decision 2, ADR 0019 |
| OpenChoreo's auto-deploy | Serendip creating releases and bindings | Two writers on one binding, and an ambiguous first environment | Spec 020, ADR 0019 |
| `EventSource` (server-sent events) for log following | `fetch` with a `ReadableStream` | It can't send the CSRF header cookie requests need (ADR 0013) | Spec 020 plan decision 8 |
| A hand-written MCP protocol layer | The MCP Go SDK | Two-plus protocol generations to track ourselves | Spec 021 (reversed 2026-10-20) |
| Serendip issuing its own tokens | ThunderID | Building an identity provider | Spec 021 plan decision 1 |
| `mark3labs/mcp-go` | The official MCP Go SDK | Community-maintained, no 2026-07-28 support when checked | ADR 0017 |
| ThunderID's `act` claim (an actor token) to mark an AI | A dedicated exchange-only client, `serendip-mcp` | An actor token needs a confidential client, so a secret on disk | Spec 021 T001 |
| A full JSON Schema library (e.g. `santhosh-tekuri/jsonschema`) | `core/jsonschema`, a small subset | A new dependency and ADR for keywords nothing uses; revisit if installed traits need `oneOf`, `if` or remote `$ref` | Spec 020 T008 |
| OpenSearch and Prometheus as the edge-1 default | Optional package only (spec 023); a lighter backend for edge-1 later | Too heavy for a 16 GB T470 | Spec 020 |

## Open (decided later)

| Question | Options | Where |
|---|---|---|
| Observability backend default | OpenObserve, SigNoz, OpenSearch | `capability-model.md`, after a T470 benchmark |
| Self-service autonomous agents in Serendip's own ThunderID | A small spec after 020 (both identity kinds: exchange for a person's assistant, own identity for autonomous agents) | Spec 021 plan decision 1 |
