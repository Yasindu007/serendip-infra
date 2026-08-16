# core/ — Serendip domain layer

The heart of the project: resource model, capability registry, provider
registry, resource graph, reconciliation engine, policy, events, audit.

Hard rules (constitution 1-3):
- NO imports of providers/ or vendor SDKs (no client-go, no product clients).
- NO product names in identifiers. Speak in capabilities.
- Every resource: Spec + Status with Conditions; every mutation audited.
- Reconcilers are idempotent and context-aware.

Conventions:
- One package per concern: resource/, registry/, graph/, reconcile/, policy/,
  event/, audit/, store/.
- store/ hides SQLite behind an interface (ADR 0003). graph/ also contains
  direct SQL against the shared edges table (specs/001-resource-model/plan.md,
  T013) -- it's tightly coupled to store/sqlite's schema by design, not a
  layering violation. No SQL outside store/ and graph/.
- Interfaces defined here, implemented in providers/.
Run go test ./core/... after every change here.
