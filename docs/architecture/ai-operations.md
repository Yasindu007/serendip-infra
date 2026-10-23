# AI operations

AI in Serendip is an **operations engine, not a chatbot**. It doesn't exist
as a general "ask me anything" surface bolted onto the platform; it consumes
the same resource graph and incident model every other part of Serendip
uses, and it acts only through the same policy/audit path everything else
does.

```
AI
├── Diagnosis       ← Logs, Metrics, Traces, Events
├── Recommendation  ← Runbook, Knowledge, History
└── Action          ← Workflow, Kubernetes, Infrastructure
```

## Severity-based autonomy matrix

This is constitution rule 5, made concrete per severity:

| Severity | AI may |
|---|---|
| SEV4 | Diagnose, suggest — nothing else |
| SEV3 | Diagnose, suggest, prepare a remediation, ask for approval |
| SEV2 | Diagnose, recommend, execute a **pre-approved runbook** only |
| SEV1 | Diagnose, recommend; execution requires explicit human approval; everything audited |

At no severity does AI receive cluster-admin, raw `kubectl`, or direct
provider credentials. Destructive operations (delete, scale-to-zero, node
drain, volume operations) always require an explicit approval object, even
when a human is the one approving (constitution 5). Spec 019 built this
for deletes: `approvals.md`.

## Approval engine

Every AI-initiated change flows through the same path as every other
mutation in Serendip:

```
AI → Serendip Action → Policy check → Provider → Execution → Verification → Audit record
```

- **Serendip Action** — a structured, typed request, not free-form
  automation; it references a Runbook or a specific resource mutation.
- **Policy check** — evaluates severity, resource sensitivity, and
  organizational policy before anything is allowed to proceed. Today it
  is the role check in `core/policy` (`authorization.md`). For an agent,
  or a person acting through an AI assistant, a delete returns
  `NeedsApproval`: the AI files `POST /v1/approvals`
  with a reason, a person allowed to delete the target approves it, and
  the approval reconciler executes it and verifies the result. The same
  path records an Approval, decided by policy, for a person's delete
  (`approvals.md`). Severity and sensitivity rules, and pre-approved
  runbooks, don't exist yet.
- **Provider / Execution** — same provider abstraction as any other
  reconciliation (`provider-model.md`); AI never talks to Kubernetes or a
  cloud API directly.
- **Verification** — the action's effect is checked against the expected
  outcome before the Incident can move to resolved.
- **Audit** — recorded unconditionally, regardless of whether the action was
  auto-executed or human-approved (constitution 8).

## What context AI receives

Diagnosis and recommendation are only as good as the context supplied. AI is
given:

- The Incident itself (symptoms, severity, affected resources)
- Observability data: logs, metrics, traces (via the Observability
  capability, `capability-model.md`)
- The resource graph (`resource-model.md`) — dependency and blast-radius
  context
- Recent changes / deployment history (`operations.md`'s change
  intelligence)
- Current topology (`deployment-profiles.md`)
- Applicable Runbooks
- Applicable policies (what it is and isn't allowed to propose/execute)

AI is deliberately not given ambient access to arbitrary cluster state
outside this curated context — the context set above is the interface, not
a side door to more.

## MCP surface

Interface parity (constitution 7) applies to AI the same as UI/CLI:

```
Serendip
├── REST API
├── CLI
├── UI
└── MCP
```

All four are clients of the same API and the same RBAC/policy layer. There
is no MCP-only capability and no AI-only control path — MCP is simply
another way of expressing the same actions a human could take via the UI or
API, subject to the exact same approval engine above. This is intentional:
it avoids inventing a second, less-audited AI control interface.

Built in spec 021 (ADR 0017, ADR 0018; commands, tools and client set-up in
`docs/cli-and-mcp.md`). The CLI and MCP hold no authority of their own.

### One catalogue

`api/catalog` lists every `/v1` route with its CLI command, its MCP tool
(name, description, schema, annotations) or a reason it's left off. Both
clients are generated from it. A parity test fails when a route has
neither, and another when `docs/cli-and-mcp.md` misses an operation.
MCP covers applications, components, secrets, approvals, access reads,
the graph and readiness; other kinds are read through `resources_list` and
`resources_get`. Never exposed: access-management writes, directory reads
and approving or rejecting (an AI never decides an approval).

Spec 020 adds the application platform's operations (ADR 0019):
- **Deployments:** `deployments_*` CRUD and `deployments_promote`.
- **The runtime view:** `deployments_runtime`, `deployments_events` and
  `deployments_logs`.
  - Logs need their own permission (the Log Reader role).
  - The MCP tool answers once with recent lines. Following a stream is
    the CLI's alone (`catalog.Param.CLIOnly`): a tool call must end.
- **Reads:** `components_schemas`, `components_dependencies`,
  `applications_topology`, `platform_get` and `packages_list`.

An assistant promotes at once, since promotion is a write its person's
roles allow. Its undeploy is a delete, so it waits for a person. Shown
with Claude Code on a real cluster in `specs/020-openchoreo-console-features/verification.md`.
Log lines are workload output, so they reach the AI as data: the
assistant reads them as text, like any other tool result.

### Two ways in

| Path | Who | Identity |
|---|---|---|
| `serendip mcp` (stdio, local) | A person's assistant on their machine | The person, through the `serendip-mcp` token exchange (`identity.md`). Audit actor `agent:serendip-mcp/<sub>`, the person as sponsor. |
| `/mcp` in `serendipd` (streamable HTTP) | Hosted agents, and delegated assistant tokens | The caller's own token, validated as for `/v1`. |

- **`serendip mcp`** is an HTTP client of the API. It exchanges the
  person's CLI sign-in for a 5-minute token, in memory only, and refreshes
  it before expiry. It refuses `SERENDIP_API_KEY` and `SERENDIP_TOKEN`, so
  an AI never holds a full credential of its own. Stdout carries only the
  protocol.
- **`/mcp`** (`api/mcp_handler.go`) accepts only an agent token or a
  delegated one: API keys and a person's own token get 403, no or an
  invalid token 401 with an RFC 9728 `WWW-Authenticate` challenge, the
  console cookie is never a credential. It is the same resource server as
  the API, so there is no token passthrough. Each tool call is dispatched
  in process to the same authorizing `/v1` mux with only the caller's
  `Authorization`, under a 30 s deadline, as its own request with its own
  ID. The token is verified at the gate and again by `/v1`, which stays
  authoritative. Cross-origin browser requests are refused; clients with no
  `Origin` (Claude Code) pass.
- **Audit interface:** `/mcp` sets `mcp` itself, authoritatively. The CLI
  and `serendip mcp` declare `cli` or `mcp` in `X-Serendip-Client`, shown
  as `(declared)`: identity, not the label, is the boundary.

### Guardrails

A delegated assistant is decided as the person (their roles, never more),
then meets the agent guardrails of spec 018 (`authorization.md`): never
access management, and every delete is `NeedsApproval`. The assistant
files `approvals_request` with a reason, and a person decides it in the
console or the CLI (`approvals.md`); no MCP tool approves or rejects.
Elicitation and a `confirm: true` step are deferred: the API's approvals are
the safety boundary, not client confirmation (ADR 0017).

### Secret values

`secrets_read_value` and `secrets_write_value` are their own tools,
flagged as sensitive and destructive, and need the secrets value data
permission. `serendip mcp --no-secret-values` leaves them out of
`tools/list`. Values are never logged.

### Limits

- A local assistant that can run commands as the person can also read
  their CLI sign-in (RP-019). The guardrails cover its mistakes with
  Serendip's tools, not hostile code running as the person.
- After sign-out an assistant keeps access for up to 5 minutes (RP-021).
- The protocol and its Go SDK change every month or two (RP-023; the SDK
  is imported only by `mcp/`).

## Open questions

- **AI inference backend is unresolved.** The source design discussion
  mentions a local model runtime (e.g. Ollama) and a candidate model family
  (e.g. a Qwen-class model) only as an illustrative example, not a decision.
  No inference backend or model has been chosen — do not treat "Ollama +
  Qwen" as a default anywhere else in these docs or in code.
- Confidence-score semantics on AI diagnoses (e.g. the `confidence: 0.91`
  field shown in `operations.md`'s Incident example) are illustrative from
  the source material; how confidence is computed, and whether it gates
  autonomy within a severity tier, is not decided.
