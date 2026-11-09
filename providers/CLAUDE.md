# providers/ — capability implementations

Each subdirectory implements one or more core interfaces for a specific
product (kubernetes/, storage/local/, storage/longhorn/, identity/thunder/,
secrets/openbao/, observability/...).

Rules:
- Import core/; NEVER the reverse. Register via the provider registry.
- A provider must declare: capabilities provided, resource requirements
  (RAM/CPU on reference hardware), health check, and upgrade behavior.
  - An optional package declares its cost as a `core/provider.Requirements`
    value in one place (e.g. `build/openchoreo/requirements.go`), filled
    from a benchmark run at the node level, never guessed. Without it the
    package can't be installed (spec 022; RP-037 for the older providers).
- Shared, product-neutral helpers for providers live in `internal/`
  (`helm`, `gatewayapi`, `gitremote`).
- Product-specific knowledge (CRDs, APIs, flags) stays in here. Use the
  oss-researcher subagent for upstream facts; record version assumptions in a
  VERSIONS.md per provider.
- Deleting any provider directory must not break core/ or api/ builds.
