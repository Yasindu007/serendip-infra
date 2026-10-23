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
Organization → Team → Project ┬→ Application → Component
                              └→ Environment → Deployment ⇢ Component
```

Since spec 020 (ADR 0019), a Project is the subscription and an Environment
the resource group you deploy into. Applications sit directly under their
Project; Environments are their siblings. Before spec 020 an Application
was owned by one Environment; migration 0008 moved existing ones up.

- **Organization** — top-level tenant boundary.
- **Team** — groups humans and RBAC bindings within an Organization.
- **Project** — groups related Applications and Environments; roughly "a
  product." Its spec has a `pipeline` (`paths: [{from, to: [...]}]`, by
  Environment ID): the order releases move through its Environments.
  Serendip enforces it.
- **Environment** — a deployment target within a Project (production,
  staging, ...); carries its own capability configuration (e.g. "this
  Environment's Database is HA, that one isn't").
- **Application** — a deployable unit a developer thinks in terms of.
  `Application` still has no spec fields: it is pure grouping.
- **Component** — a piece of an Application (e.g. `frontend`, `backend`,
  `worker`); what a workload is made from. Its spec (spec 011) has an
  `image`, `ports` (`[{name, port, expose}]`), a plain `env` map and, since
  spec 020, `dependencies` (`[{component, port, env}]`: calls to other
  Components of the same Application). It is owned by exactly one
  Application. It runs nowhere by itself: it's `Released` once its current
  spec is a release.
- **Deployment** (spec 020) — one Component running in one Environment,
  owned by that Environment, with a `depends-on` edge to the Component.
  Its spec names the `component`, an optional `release` (empty: the
  latest) and per-environment `overrides` (`env`, `config`, `traits`); its
  status reports the running `release`, a bounded `history` and
  `endpoints`. At most one per Component per Environment. Deploying,
  promoting and rolling back write it; undeploying deletes it, through an
  Approval. Releases themselves are provider-reported, not a kind.

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
  Implemented (spec 007) as `core/resource.Volume`
  (`resource.KindVolume`), not literally named "Storage" as written
  above — deliberately, to avoid colliding with "Storage" already
  naming the *capability* throughout `capability-model.md`/`storage.md`.
  A resource kind and the capability it belongs to sharing one
  identifier would be confusing in API paths (`/v1/volumes`), logs, and
  error messages. Spec 007's `Volume` is deliberately minimal (one
  size, no tier/policy/replication/backup/encryption) — this doc's
  fuller "primary or backup storage request" concept, including
  `storage.md`'s `storagePolicy`, remains aspirational until a second
  storage provider (Longhorn, ADR 0002) gives it a real reason to exist.

### Operational resources

- **Alert** — a single detected anomaly (metric threshold, log pattern,
  synthetic check failure, ...).
- **Incident** — a correlated, triaged operational event, potentially
  produced by one or more Alerts. Carries severity, affected resources,
  diagnosis, and the approval/action trail. Full lifecycle in
  `operations.md`.
- **ProviderInstance** — "this provider, for this capability, installed."
  Since spec 020 it's how an optional package (builds, observability) is
  installed: `capabilityId` and `providerId` name it, it's platform-wide
  (no owner, no `environmentId`), and one exists per capability. Removing
  it goes through an Approval, whose clean-up uninstalls the package.
- **Approval** — the record of one destructive action (today, a delete):
  what was asked, who asked, what it removes (its blast radius), who or
  what decided (policy, or a person), and how execution and verification
  went. Every delete writes one. It has no owner, so a cascade never
  removes it, and it can't be deleted or changed once decided. Spec in
  `approvals.md`.

## Relationships (resource graph)

Resources are connected by typed edges, not implicit foreign keys:

| Edge | Example |
|---|---|
| `owns` | Project owns Environment and Application; Application owns Component; Environment owns Deployment |
| `runs-on` | Component runs-on Cluster; Cluster runs-on Node(s) |
| `depends-on` | Deployment depends-on its Component; Component depends-on the Components it calls (spec 020); Component depends-on Database |
| `provided-by` | Database provided-by a Provider instance (e.g. CloudNativePG) |

The graph supports traversal in both directions (children, dependents) and
blast-radius queries ("what breaks if this Node dies") — this is what makes
AI root-cause correlation possible (`operations.md`, `ai-operations.md`).

Deleting a resource with children is rejected unless cascade is explicitly
requested.

**A store delete requires an Approval** (constitution 5, `approvals.md`).
`Store.Delete` takes it from the context and writes it in the delete's own
transaction, listing every row removed. A delete with no approval on its
context is refused with `store.ErrNoApproval` and changes nothing.

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

Three of the four questions originally carried forward from
`specs/001-resource-model/spec.md` are resolved as of spec 001's
implementation (see `specs/001-resource-model/plan.md` for the full
reasoning):

- **ID format: UUIDv7**, via `github.com/google/uuid` (ADR 0007). Chosen
  over ULID for a single well-audited dependency rather than two
  time-sortable-ID options with no meaningful difference for this use.
- **Conditions: one shared status package** (`core/resource.Condition`/
  `Status`), embedded by every kind's Status struct.
- **Label/selector semantics: exact-match only.** `core/store.ListOptions`
  filters by kind/owner/exact label equality; the full Kubernetes
  set-based selector grammar (`in`/`notin`/`exists`) is deferred until a
  real consumer needs it (likely the topology planner) — building it
  speculatively would violate constitution rule 10 (scope discipline).

Still open:

- **Network resource**: the source design discussion sketches a Network
  capability (VPC/subnet/security-group equivalents) in detail conceptually
  but it has no committed field-level shape yet — needs its own spec before
  implementation.
