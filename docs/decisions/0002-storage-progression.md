# ADR 0002: Storage progression — local first, Longhorn at 3 nodes, replicas=2

Status: Accepted
Date: 2026-08

## Context
Distributed storage on 1-2 nodes adds cost without node-failure protection.
Reference hardware has one disk per node.

## Decision
- 1-2 nodes: local-path storage only. UI reports distributed storage unavailable.
- 3+ nodes: Longhorn offered (not forced), default replica count 2.
- Primary storage and backup storage are separate capabilities. Backups go to
  S3/NFS/external, never counted as redundancy.
- Ceph is out of scope for core; possible marketplace provider later.

## Consequences
- Storage is a capability with providers: local-path, longhorn, external-csi.
- CloudNativePG profiles choose storage per database replication mode to avoid
  replication-on-replication waste.
- Longhorn is benchmarked on T470s before becoming a profile default.
