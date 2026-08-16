# Storage

This document elaborates the storage architecture around ADR 0002
(local-first, Longhorn at 3+ nodes, replicas=2), which remains the
authoritative decision record. This doc does not re-decide anything ADR
0002 already settled — it adds the supporting detail (classes, policies,
placement, capacity reporting) that the ADR intentionally kept out of scope.

## Two separate capabilities

Serendip treats primary storage and backup storage as **separate
capabilities, never conflated** (constitution 6):

```
Storage
├── Primary Storage   — for live workloads (local-path / Longhorn / external CSI)
└── Backup Storage    — for recovery (S3-compatible / NFS / object storage)
```

Replication is not backup. A Longhorn volume with 2 replicas happily
replicates a `DROP DATABASE`. Availability (replicated) and recovery (backed
up) are reported as distinct, separately-visible status.

## Progression (ADR 0002)

| Nodes | Primary | Backup |
|---|---|---|
| 1 (edge-1) | Local storage (local-path CSI) | External (disk/NFS/S3) |
| 2 | Local storage — distributed storage still not offered | External |
| 3 (small-3) | Local storage, or Longhorn (optional, replicas=2) | S3/NFS |
| 5 (standard-5) | Longhorn (replicas=2) | Dedicated backup target (S3/NFS) |
| 6-8+ (private-cloud, design-stage) | Longhorn / external CSI | Dedicated object storage node(s) |

Below 3 nodes, Longhorn provides no real node-failure protection and only
adds CPU/RAM/management overhead — so it is not offered, and the UI reports
"distributed storage unavailable" rather than a degraded approximation of it
(constitution 6).

## Why replicas=2, not 3

Reference hardware (T470-class) typically has one disk per node. At 3 nodes
with 1TB each: 3 replicas gives ~1TB usable out of 3TB raw; 2 replicas gives
~1.5TB usable. Two replicas is the balance point between availability and
usable capacity on small hardware, and is consistent with Longhorn's own
guidance for data-intensive workloads on constrained hardware. This is
codified in ADR 0002; do not change it here.

Database engines that already replicate at the application layer (e.g.
CloudNativePG running primary + replicas) should not automatically also get
3x infrastructure-level replication underneath — that compounds redundancy
without a proportional benefit. Storage policy selection (below) is how a
workload's own replication model is taken into account.

## Storage classes / tiers

Exposed to users as tiers, not raw CSI implementation names:

| Tier | Backing | Best for |
|---|---|---|
| Fast SSD/NVMe | local-path on fastest local disk | Databases, latency-sensitive apps |
| Distributed | Longhorn | HA workloads, workloads that must survive node loss |
| Object | S3-compatible / MinIO | Backups, artifacts, archives |
| Archive | HDD / NFS | Cold data |

## Storage policies

A named policy resolves to a concrete provider configuration, so
applications request outcomes, not CSI details:

```yaml
storagePolicy:
  name: production-database
  performance: high
  replication: 2
  backup: daily
  retention: 14d
  encryption: required
```

`storagePolicy: production-database` resolves (today) to: Longhorn,
replicas=2, scheduled onto NVMe-tagged nodes, backup target = S3.

## Hardware-aware placement

Nodes are not treated as interchangeable disks. The bootstrap agent reports
each node's storage type (NVMe / SATA SSD / HDD) and Serendip uses that for
placement:

```
PostgreSQL → NVMe-tagged nodes
Application → SSD-tagged nodes
Backup/archive → HDD-tagged nodes
```

Production guidance (carried from Longhorn's own recommendations): use a
dedicated disk for distributed storage rather than the OS/root disk where
hardware allows it (e.g. a 500GB NVMe for OS+K3s, a separate 1TB disk for
Longhorn), rather than one large disk serving both roles.

## Capacity reporting

Capacity is always reported usable-after-replication, never raw
(constitution 6):

```
Raw capacity:        3 TB
Replica policy:       2
Approx. usable:       1.5 TB
Reserved/system:      ...
Available:            ...
```

## Dedicated storage nodes

A node dedicated to storage (NFS/S3/backup/archive) is supported, but is
introduced for **backup storage first**, not primary. Making a single node
the primary storage dependency for every workload reintroduces the single
point of failure that distributed primary storage (Longhorn) exists to
avoid — Longhorn's model spreads primary storage across the same nodes that
run compute, rather than centralizing it. Backup targets don't have this
problem: backups are, by design, not on the hot path.

## Out of scope for core

Ceph is explicitly out of scope for `core/` (ADR 0002) — it is
significantly more operationally involved than a lightweight self-hosted
default needs. It remains a possible future marketplace/provider option, not
a default.

## Networking note

Longhorn replication traffic and workload traffic share the network by
default (1GbE is workable for a small deployment). A dedicated storage
network/VLAN is supported when the underlying infrastructure has it, but is
not required.

## Open questions

- None beyond what ADR 0002 already lists as future work (benchmarking
  Longhorn on T470-class hardware before it becomes a profile default is a
  stated ADR 0002 consequence, not yet recorded as done).
