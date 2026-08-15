# providers/ — capability implementations

Each subdirectory implements one or more core interfaces for a specific
product (kubernetes/, storage/local/, storage/longhorn/, identity/thunder/,
secrets/openbao/, observability/...).

Rules:
- Import core/; NEVER the reverse. Register via the provider registry.
- A provider must declare: capabilities provided, resource requirements
  (RAM/CPU on reference hardware), health check, and upgrade behavior.
- Product-specific knowledge (CRDs, APIs, flags) stays in here. Use the
  oss-researcher subagent for upstream facts; record version assumptions in a
  VERSIONS.md per provider.
- Deleting any provider directory must not break core/ or api/ builds.
