# Deployment profiles

Profiles are data, not code (`profiles/README.md`): a component list, pinned
versions, and a resource budget per tier. The installer and topology planner
consume profile data to decide what runs where. A user never reinstalls
Serendip to move between profiles — the same install grows into the next
tier.

## Profiles

| Profile | Nodes | Topology | Status |
|---|---|---|---|
| `edge-1` | 1 | Single node runs control-plane + workloads | Implemented (`profiles/edge-1/`) |
| `small-3` | 3 | 3 nodes, Kubernetes control-plane HA (embedded etcd, ADR 0001) | Implemented (`profiles/small-3/`) |
| `standard-5` | 5 | 3 control-plane nodes + 2 dedicated workload nodes | Implemented (`profiles/standard-5/`) |
| private-cloud | 6–8+ | Adds a dedicated compute/VM cluster (Rancher + Harvester) alongside the Kubernetes cluster | Design-stage only — not yet a `profiles/` directory |

`private-cloud` and later multi-cluster/hybrid tiers are described in the
founding design discussion as the intended next steps, but are not yet
implemented in this repo. Treat them as roadmap, not shipped.

### edge-1 — what it enables

Single machine (reference: one T470, 16-32GB RAM). Everything on one node:
Serendip control plane, Thunder, OpenBao, cert-manager, kgateway, Cilium,
OpenTelemetry, local-path storage, OpenChoreo, lightweight observability
backend.

Explicitly **not** available: HA of any kind, distributed storage, VM
infrastructure. The UI reports this honestly (constitution 6) — e.g. a
requested PostgreSQL database is created as "single instance" with backup
enabled and replication reported unavailable.

### small-3 — what it enables

Adds Kubernetes control-plane redundancy (3-node etcd quorum), workload
spreading across nodes, PostgreSQL HA capability (CloudNativePG), and
distributed storage capability (Longhorn becomes an option — ADR 0002).
None of this is forced on; the topology planner recommends, the user
approves.

### standard-5 — what it enables

Adds dedicated workload capacity: control-plane nodes are scheduling-biased
toward platform services, workload nodes toward applications (soft
preference, not a hard constraint — if a workload node dies, Kubernetes can
still reschedule onto control-plane nodes). This is also the point at which
Rancher becomes worth enabling, since there is now a real reason to think in
terms of managed clusters rather than one cluster.

### private-cloud (design-stage) — intended scope

Splits infrastructure into a Kubernetes cluster (apps, CI/CD) and a
Harvester/KubeVirt cluster (VMs), both under Rancher. Not implemented; see
Open questions.

## Upgrade paths between profiles

Nodes are added one at a time; the topology planner recomputes
recommendations after every addition rather than requiring a fixed jump:

1. **1 node** → `edge-1`. Adding a 2nd node keeps the install in an
   `edge-1`-like state (still 1 control node) — the planner explicitly does
   *not* rebalance to HA at 2 nodes, because 2 nodes cannot provide
   meaningful quorum.
2. **3rd node arrives** → the planner recommends promoting all three nodes
   to control-plane-capable and establishing etcd quorum. This transition —
   `edge-1`/2-node → `small-3` — is the first real upgrade: it is presented
   as a reviewable plan, not applied silently.
3. **4th–5th node arrive** → the planner recommends dedicating the new
   node(s) to workloads rather than adding more control-plane members
   (3 control-plane nodes is the target, not "more is better"). This is the
   `small-3` → `standard-5` transition.
4. **6th+ node / private-cloud** → design-stage; the intended trigger is
   "sufficient capacity exists to justify a dedicated compute/VM cluster
   rather than growing the existing one," at which point Rancher registers a
   second (Harvester) cluster alongside the first.

## Topology planner behavior

The topology planner is a Serendip-native component (see `overview.md`
build list). Its job, every time infrastructure inventory changes:

```
Current topology → Desired topology (given policy + hardware) → Migration plan → Apply (with approval)
```

Concrete behavior:
- **Never auto-migrates.** A recommendation is always presented for review
  (e.g. "3 control-plane-capable nodes recommended — benefits: control-plane
  redundancy, workload spreading, PostgreSQL HA capability, distributed
  storage capability") before any reconciliation runs.
- **Classifies new nodes** using their hardware profile and current cluster
  state — e.g. a node arriving when control-plane capacity is already
  sufficient is recommended as a workload node, not blindly added as a
  generic worker.
- **Understands hardware heterogeneity** — a node with an HDD is not treated
  identically to one with NVMe for placement purposes (see `storage.md`).
- **Produces an ordered migration plan** (e.g. "1. promote node, 2. establish
  quorum, 3. enable workload spreading, 4. configure storage, 5. verify")
  that the reconciliation engine executes step by step, each step verified
  before the next (constitution 3).

## Open questions

- `private-cloud` profile: node-count thresholds, exact Harvester
  integration shape, and whether it is one profile or several (private-cloud
  vs. multi-cluster vs. hybrid-cloud) are not decided — only the general
  direction is. Needs its own spec before implementation.
- Whether a `cloud` profile (managed Kubernetes, e.g. EKS/AKS, as the
  substrate instead of bare metal) is in scope at all was raised in the
  source discussion only as a long-term possibility, not committed.
