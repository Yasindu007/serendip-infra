# Operations

Serendip owns incident, alerting, and SLO semantics natively — this is one
of the areas the project is explicitly meant to differentiate on, rather
than embed a third-party ITSM/observability product wholesale
(`capability-model.md`'s native/external split).

## Incident lifecycle

```
Alert → Incident → Diagnosis → Recommendation → Approval → Action → Verification → Postmortem
```

- **Alert** — a single detected anomaly (metric threshold, log pattern,
  synthetic check).
- **Incident** — one or more correlated Alerts, triaged with a severity and
  affected-resource set. First-class Serendip resource (`resource-model.md`).
- **Diagnosis** — AI or human analysis of probable cause, using the
  resource graph and recent-change history (see below).
- **Recommendation** — a proposed remediation, often referencing a Runbook.
- **Approval** — required before any non-pre-approved action executes
  (constitution 5).
- **Action** — the executed remediation, always via
  `AI → Serendip Action → Policy → Provider → Execution` (never direct
  infra access).
- **Verification** — confirms the action actually resolved the symptom
  before the Incident is closed.
- **Postmortem** — recorded root cause, timeline, and action taken, once
  resolved.

### Incident shape (illustrative)

```yaml
Incident:
  id: INC-2026-001
  severity: SEV2
  service: payments
  environment: production
  detected_by: [metric, log, trace]
  symptoms:
    - HTTP 500 increased
    - DB latency increased
  ai:
    diagnosis: "Database connection exhaustion"
    confidence: 0.91
  actions:
    proposed: [increase pool, restart affected pods, scale database]
    approved: [restart affected pods]
```

This structure is what makes AI diagnosis operate on something concrete
rather than free text — see `ai-operations.md`.

## SLOs and error budgets

SLO/SLI/error-budget/burn-rate are first-class Serendip concepts, not just a
Grafana dashboard convention:

```
Application
├── SLO (e.g. 99.95%)
├── SLI
├── Error Budget
├── Burn Rate
└── SLO Alerts
```

The observability backend (`capability-model.md`) may *visualize* these
(e.g. via Grafana-style dashboards), but Serendip owns the semantics —
computing burn rate and firing SLO alerts is not delegated to the backend
provider.

## Change intelligence

Serendip tracks changes as first-class events and correlates them against
degradation:

```
Change (deployment, config, Terraform/Crossplane, Helm, Kubernetes,
        secret rotation, infra change)
  → Metric degradation
  → Alert
  → Incident
```

This is powered by the resource graph (`resource-model.md`): an Incident on
a Component can be correlated against the most recent Change on that
Component or its dependencies (e.g. "the strongest correlation is a
PostgreSQL connection-saturation event that began 4 minutes after
deployment #184").

## Runbooks

A Runbook is a structured, referenceable remediation procedure:

```yaml
Alert: PostgreSQL connections > 90%
Runbook:
  1. Inspect connection pool
  2. Check recent deployments
  3. Check DB CPU
  4. Check long-running queries
  5. Scale pool if approved
```

AI recommendations reference Runbook steps rather than inventing arbitrary
actions — this keeps recommendations auditable and keeps the
severity-based autonomy matrix (`ai-operations.md`) meaningful.

## Resource / service graph

Incident diagnosis depends on the same resource graph defined in
`resource-model.md` (`depends-on` / `runs-on` edges). Example:

```
Internet → Gateway → Frontend → API → {Redis, PostgreSQL} → Kubernetes → Node
```

When the API degrades, the graph is what lets diagnosis walk from symptom to
probable cause instead of surfacing an undifferentiated pile of alerts.

## Ticketing / ITSM

Incidents (operational, Serendip-native) and Tickets (ITSM, e.g. a customer
support request) overlap but are not the same resource. Native Incidents are
always available; ITSM systems (ServiceNow, Jira, Zammad, GLPI) are optional
providers for the Ticketing capability (`capability-model.md`) that
Serendip correlates against, rather than replaces.

## Open questions

- On-call scheduling/paging is explicitly deferred — the source material
  marks it "integrate/provider initially," not a native capability for v1.
- Cost/FinOps, security posture, vulnerability scanning, status pages, and
  synthetic monitoring are all marked "later" in the source discussion —
  not scoped for v1, not designed yet.
