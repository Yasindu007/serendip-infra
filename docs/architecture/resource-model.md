# Resource model

Serendip's domain model is the thing every provider, profile, and UI/CLI/MCP
surface hangs off. This document describes what exists today (types) and the
conventions that apply to all of them. Field-level Go definitions live in
`core/` and `specs/001-resource-model/`; this doc stays at the architecture
level.

## Resource envelope

Every Serendip resource — regardless of type — shares one envelope:

```
Resource
├── Metadata   (id, name, labels, created/updated timestamps, owner refs,
│               resourceVersion for optimistic concurrency)
├── Spec       (desired state, type-specific)
└── Status     (observed state, type-specific fields + Conditions)
```

This follows Kubernetes API conventions deliberately (constitution 3), even
for resources that have nothing to do with Kubernetes underneath (an
`Incident` gets the same envelope as a `Cluster`).

## Hierarchy

### Organizational tree

```
Organization → Team → Project → Environment → Application → Component
```

- **Organization** — top-level tenant boundary.
- **Team** — groups humans and RBAC bindings within an Organization.
- **Project** — groups related Applications; roughly "a product."
- **Environment** — a deployment target within a Project (production,
  staging, ...); carries its own capability configuration (e.g. "this
  Environment's Database is HA, that one isn't").
- **Application** — a deployable unit a developer thinks in terms of.
- **Component** — a piece of an Application (e.g. `frontend`, `backend`,
  `worker`); the unit that actually maps to a workload.

### Infrastructure tree

```
Site → Cluster → Node
```

- **Site** — a physical or logical location (a room, a cloud region, a
  colo). Groups Clusters that share physical proximity/network locality.
- **Cluster** — a Kubernetes (or other compute substrate) cluster. Has a
  `Provider` (K3s, Rancher-managed, external, Harvester, ...).
- **Node** — a machine. Carries hardware facts (CPU, RAM, disk type/size,
  network, virtualization support) reported by the bootstrap agent — see
  `bootstrap-agent.md`. Nodes are the input to the topology planner
  (`deployment-profiles.md`).

### Cross-cutting infrastructure resources

- **Network** — virtual network / subnet / security-policy abstraction. Not
  yet detailed in a dedicated capability (see `capability-model.md` open
  questions); referenced here for completeness.
- **Database** — a managed data store request (engine, size, environment,
  replication mode). Distinct from the control plane's own persistence
  (ADR 0003 governs *Serendip's* store only, not user Databases).
- **Storage** — a primary or backup storage request. See `storage.md`.

### Operational resources

- **Alert** — a single detected anomaly (metric threshold, log pattern,
  synthetic check failure, ...).
- **Incident** — a correlated, triaged operational event, potentially
  produced by one or more Alerts. Carries severity, affected resources,
  diagnosis, and the approval/action trail. Full lifecycle in
  `operations.md`.

## Relationships (resource graph)

Resources are connected by typed edges, not implicit foreign keys:

| Edge | Example |
|---|---|
| `owns` | Project owns Environment; Application owns Component |
| `runs-on` | Component runs-on Cluster; Cluster runs-on Node(s) |
| `depends-on` | Component depends-on Database; Application depends-on Application (service-to-service) |
| `provided-by` | Database provided-by a Provider instance (e.g. CloudNativePG) |

The graph supports traversal in both directions (children, dependents) and
blast-radius queries ("what breaks if this Node dies") — this is what makes
AI root-cause correlation possible (`operations.md`, `ai-operations.md`).

Deleting a resource with children is rejected unless cascade is explicitly
requested.

## Status conventions

Every resource's `Status` carries `Conditions`: a list of
`{type, status, reason, message, lastTransitionTime}`, Kubernetes-style
(constitution 3). This applies uniformly — a `Cluster` and an `Incident` use
the same Condition shape, just different `type` values.

### Honest reporting (constitution 6)

Status must never imply a capability that doesn't exist:

- A single-node install reports `Availability: single instance, no
  node-failure protection` — not silently "healthy" in a way that implies HA.
- Replication and backup are reported as separate, never-conflated
  Conditions/fields (see `storage.md`).
- Storage capacity is reported usable-after-replication, not raw.

## Open questions

Carried forward from `specs/001-resource-model/spec.md`, unresolved as of
this writing:

- **ID format**: ULID vs. UUIDv7 — not decided.
- **Conditions**: one shared status package across all resource types, or
  per-resource-type Condition types? Current lean is shared, not finalized.
- **Label/selector semantics**: adopt the Kubernetes label-selector grammar
  verbatim, or define a Serendip-specific one? Not decided.
- **Network resource**: the source design discussion sketches a Network
  capability (VPC/subnet/security-group equivalents) in detail conceptually
  but it has no committed field-level shape yet — needs its own spec before
  implementation.
