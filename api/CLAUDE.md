# api/ — HTTP API over core

- net/http + stdlib routing (Go 1.22 patterns). No web framework without an ADR.
- Handlers are thin: decode -> authz (policy) -> core call -> encode. No
  business logic, no direct store access, no direct provider calls.
- Every mutating endpoint creates desired state; reconcilers do the work.
- Errors: RFC 7807 problem+json. Every response includes request ID.
- Interface parity (constitution 7): UI/CLI/MCP consume THIS API only.
