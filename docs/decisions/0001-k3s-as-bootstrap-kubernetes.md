# ADR 0001: K3s as the bootstrap Kubernetes distribution

Status: Accepted
Date: 2026-08

## Context
Serendip must bootstrap Kubernetes on very weak hardware (T470-class) with a
single binary, minimal RAM, and a path to multi-node HA. Candidates: K3s,
RKE2, Talos.

## Decision
K3s for edge-1 and small-3 profiles. Revisit RKE2 for hardened/enterprise
profiles later; Talos rejected for v1 because Serendip's bootstrap agent
assumes a general-purpose Linux host it can also use for diagnostics.

## Consequences
- serendip-agent installs/configures K3s; embedded etcd for 3-node HA.
- Profile definitions pin a tested K3s version.
- RKE2 migration path documented before any enterprise profile ships.
