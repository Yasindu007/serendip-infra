# Serendip Infra Constitution

These rules are non-negotiable. If a prompt, spec, or convenience conflicts
with this document, this document wins. Flag the conflict instead of coding
around it.

## 1. Capability first, vendor second

- The core domain model speaks in capabilities (Identity, Secrets, Storage,
  Observability, Gateway, Database, Compute, Ticketing), never in product
  names.
- No OSS product name (Longhorn, Thunder, OpenBao, OpenChoreo, Rancher,
  Crossplane, Cilium, kgateway, OpenObserve, SigNoz, Zammad...) may appear in
  `core/` types, function names, or comments-as-behavior. Products exist only
  under `providers/` and in `profiles/`.
- Every capability has: a Go interface in `core/`, at least one provider in
  `providers/`, and an `external` escape hatch planned (bring your own).

## 2. Provider isolation

- `core/` imports no provider packages. Providers import `core/`. Enforced by
  review and by `.claude/scripts/check-imports.sh`.
- Providers are registered, discovered, and selected through the Provider
  Registry — never instantiated directly by business logic.
- A provider must be removable: deleting a provider package must not break
  `go build ./core/... ./api/...`.

## 3. Declarative and reconciled

- All state changes flow: desired state → reconciliation engine → provider →
  verification. No imperative side-channels that mutate infrastructure
  directly from an API handler.
- Reconciliation loops are idempotent: observe current state, compute diff,
  apply, verify. Re-running a loop with no changes must be a no-op.
- Every resource has a status subresource with conditions
  (type/status/reason/message), following Kubernetes API conventions even for
  non-Kubernetes resources.

## 4. Lightweight is a feature, not an optimization

- Reference hardware is a ThinkPad T470 (2c/4t i5-6300U class, 16-32GB RAM,
  SATA/NVMe SSD). If it doesn't run well there, it doesn't ship as a default.
- Control-plane components (core + api + agent) target: < 256MB RSS combined
  at idle, < 5% CPU at idle on reference hardware. Measure before merging
  anything that plausibly changes this.
- Standard library first. A new Go dependency requires an ADR stating what it
  buys us and its transitive weight. Heavy frameworks (full ORMs, DI
  containers, kitchen-sink SDKs) are rejected by default.
- Embedded SQLite is the default store. Anything requiring "just run
  PostgreSQL" as a hard dependency of the control plane is rejected.

## 5. Safety hierarchy for automation and AI

- AI/automation never receives cluster-admin or raw kubectl. All actions flow:
  AI → Serendip Action → Policy check → Provider → Execution → Verification →
  Audit record.
- Severity-based autonomy: diagnose/suggest freely; prepare remediations with
  approval; auto-execute only pre-approved runbooks; always audit.
- Destructive operations (delete, scale-to-zero, node drain, volume ops)
  always require an explicit approval object, even from humans via API.

## 6. Honest capability reporting

- Never pretend HA exists when it doesn't. A 1-node install reports
  "single instance, no node-failure protection" — plainly, in API and UI.
- Replication is not backup. They are separate capabilities, separately
  reported, never conflated.
- Capacity is reported as usable-after-replication, not raw.

## 7. Interface parity

- UI = CLI = API = MCP. Anything a human can do in the UI must be expressible
  through the API; UI and CLI and MCP are clients of the same API and the same
  RBAC/policy layer. No UI-only or MCP-only capabilities.

## 8. Everything auditable

- Every mutation records: who (identity), what (resource + diff), when, via
  what interface, under what policy decision. This is not optional per
  component; it is provided by core.

## 9. Testing discipline

- Table-driven unit tests for core logic. envtest/KIND for anything touching
  Kubernetes. No task is "done" without its Verify line executed.
- Benchmarks live in the repo (`profiles/*/bench/`) and are run on reference
  hardware before profile defaults change.

## 10. Scope discipline

- Do not build: a database engine, a Kubernetes distribution, a secrets
  engine, a metrics TSDB, a service mesh, a Git server, a CI engine, an API
  gateway, or a VM hypervisor. We integrate those.
- Build only: resource model, capability/provider registries, resource graph,
  reconciliation, policy, audit, events, topology planner, bootstrap agent,
  incident/ops layer, AI operations engine, profiles/installer, UI/CLI/MCP.
