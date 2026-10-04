# core/provider — live capability implementation registry

- **Not the same thing as `core/registry`.** `core/registry`'s
  `CapabilityRegistry`/`ProviderRegistry` manage persisted *data*
  (descriptors stored as ordinary resources via `store.Store`). This
  package manages live Go values: a process-local, in-memory map from a
  capability name to whatever Go interface implementation is currently
  active for it. Nothing here is persisted; it's rebuilt from scratch
  every time `serendipd` starts.
- **No capability-specific interface types live here.** `Registry.Lookup`
  returns `any` deliberately — this package is capability-agnostic by
  design (constitution rule 1 in spirit: speak in the registration
  mechanism, not in any specific capability). Each capability defines
  its own interface (e.g. a future `StorageProvider`) wherever makes
  sense for that capability, and type-asserts after `Lookup`.
- **`core/` still imports no `providers/` package, ever** (constitution
  rule 2) — this package makes capability wiring possible without
  violating that: `Register` is called from `cmd/serendipd` (the
  composition root), which imports both `core/provider` and the
  specific `providers/` packages it wants active. `core/provider` itself
  never imports anything under `providers/`.
- Safe for concurrent `Register`/`Lookup` calls.
