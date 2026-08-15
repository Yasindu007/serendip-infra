# Spec 001: Serendip Resource Model + Registries (Foundation v0.1)

Status: Draft — human review required before plan.md

## Problem

Serendip needs its own domain model before any OSS integration. Everything
later (providers, profiles, topology planner, incidents, AI ops) hangs off
these types and the capability/provider registries. Getting these contracts
wrong makes every future provider painful.

## Scope — IN

Resources (Go types, validation, store, CRUD via core — no HTTP yet):
- Organization, Team, Project, Environment
- Site, Cluster, Node
- Application, Component
- Capability, Provider, ProviderInstance
- Incident, Alert (types only; no lifecycle logic yet)

Machinery:
- Resource envelope: metadata (id, name, labels, created/updated, owner refs),
  Spec, Status with Conditions (type/status/reason/message/lastTransition).
- Store interface + SQLite implementation (ADR 0003): Get/List/Create/Update/
  Delete with optimistic concurrency (resourceVersion).
- Capability Registry: register capability definitions
  (name, required, provider slots, default provider).
- Provider Registry: register provider descriptors
  (name, capabilities provided, resource requirements, health check contract).
- Resource Graph: typed relationships (owns, runs-on, depends-on, provided-by)
  with traversal (children, dependents, blast radius).
- Event bus (in-process): resource created/updated/deleted events.
- Audit: append-only record for every mutation (who/what/diff/when/interface).

## Scope — OUT (explicitly)

- HTTP API (spec 002), reconciliation engine (spec 003), bootstrap agent
  (spec 004), any actual provider implementation, RBAC enforcement (types may
  reserve fields), UI, MCP, Kubernetes anything.

## Acceptance criteria

1. `go test ./core/...` green; table-driven tests cover validation, optimistic
   concurrency conflicts, graph traversal, and audit emission on every mutation.
2. Creating an Application under a Project under an Organization produces
   correct graph edges; deleting the Project with children is rejected unless
   cascade requested.
3. Registering two providers for one capability and switching the default
   requires no changes outside registry data.
4. check-imports.sh passes; zero vendor names in core/ identifiers.
5. Combined test binary demonstrates < 50MB RSS handling 10k resources in
   SQLite (smoke benchmark recorded in profiles/edge-1/bench/).

## Open questions (resolve during plan review)

- IDs: ULID vs UUIDv7.
- Do Conditions live per-resource-type or one shared status package? (lean: shared)
- Label/selector semantics: adopt Kubernetes label selector grammar verbatim?
