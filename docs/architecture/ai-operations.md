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
when a human is the one approving (constitution 5).

## Approval engine

Every AI-initiated change flows through the same path as every other
mutation in Serendip:

```
AI → Serendip Action → Policy check → Provider → Execution → Verification → Audit record
```

- **Serendip Action** — a structured, typed request, not free-form
  automation; it references a Runbook or a specific resource mutation.
- **Policy check** — evaluates severity, resource sensitivity, and
  organizational policy before anything is allowed to proceed.
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
