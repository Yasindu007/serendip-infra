# api/ — HTTP API over core

- net/http + stdlib routing (Go 1.22 patterns). No web framework without an ADR.
- Handlers are thin: decode -> core/store call -> encode. No business
  logic, no direct provider calls. Calling `core/store.Store[S,St]`
  directly from a handler *is* the core call (spec 002) — there's no
  separate domain-logic layer between them yet. There is no per-request
  policy/authz decision beyond actor resolution (below) until a real
  policy engine exists (`audit.Record.PolicyDecision` is reserved for
  that, spec 003+); don't invent an authz step that doesn't exist.
- Actor resolution happens in `AuthMiddleware` (`middleware.go`, spec 015):
  - the static `SERENDIP_API_KEYS` token->actor map first;
  - then the Identity capability's `core/identity.Authenticator`, which
    yields a `person:<name>` actor.

  Outcomes: an invalid token gets 401 with a generic message, and
  `reconcile.ErrProviderNotReady` gets 503. A nil authenticator means API
  keys only. Keep `api/` provider-neutral: it sees only the interface,
  never a product. Everything downstream reads only the resolved actor
  from context (`core/audit.ActorFrom`), never how it got there.
- Routes needing no auth (e.g. `GET /healthz`) are registered on a
  separate outer `http.ServeMux`, with the authenticated `/v1/` subtree
  mounted as a single `AuthMiddleware`-wrapped handler (`server.go`) —
  not a per-route bypass flag. Follow this pattern for any future
  no-auth route rather than special-casing inside `AuthMiddleware`.
- Every mutating endpoint creates desired state; reconcilers do the work.
- Errors: RFC 7807 problem+json. Every response includes request ID.
- Interface parity (constitution 7): UI/CLI/MCP consume THIS API only.
