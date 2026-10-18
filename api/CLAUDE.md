# api/ — HTTP API over core

- net/http + stdlib routing (Go 1.22 patterns). No web framework without an ADR.
- Handlers are thin: decode -> core/store call -> encode. No business
  logic, no direct provider calls. Calling `core/store.Store[S,St]`
  directly from a handler *is* the core call (spec 002) — there's no
  separate domain-logic layer between them yet.
- Authorization (spec 017, `authz.go`): every `/v1` route is registered
  through `NewServer`'s authorizing mux, which panics at startup for a
  route missing from `routeTable()`. Adding a route means adding its rule
  (action, and how its target scope is found). Handlers take the `Mux`
  interface, never `*http.ServeMux` directly. List handlers must pass
  their results through `filterReadable`, and graph handlers through
  `filterReadableRefs`. A refusal is 403 naming the missing action and
  scope. A refused mutation gets an audit row; the decision that allowed
  a mutation lands in `policy_decision` via `audit.WithPolicyDecision`.
  - A route whose target comes from the body must read it through
    `bodyDoc` (the same decoder as `decodeBody`); an unreadable body is
    refused, never passed on.
  - `/v1/authorization/*` (`authorization_handler.go`): every role
    assignment or definition change holds `Engine.LockAssignments` across
    its checks and its write, and uses `CheckAssign`/`CheckUnassign`
    (delegation, assignable scopes, the last Owner at `/`).
  - The server chooses IDs for role definitions, assignments and Secrets;
    a client-supplied `metadata.id` is dropped.
  - A 5xx answer never carries internal error text (`writeStoreErr`).
  - The model: `docs/architecture/authorization.md`, ADR 0014.
- Actor resolution happens in `AuthMiddleware` (`middleware.go`, spec 015):
  - the static `SERENDIP_API_KEYS` token->actor map first;
  - then the Identity capability's `core/identity.Authenticator`, which
    yields a `person:<name>` actor.

  Outcomes: an invalid token gets 401 with a generic message, and
  `reconcile.ErrProviderNotReady` gets 503. A nil authenticator means API
  keys only. Keep `api/` provider-neutral: it sees only the interface,
  never a product. Everything downstream reads only the resolved actor
  from context (`core/audit.ActorFrom`), never how it got there.
- With the web console wired (`*Console`, spec 016, ADR 0013), the
  session cookie `serendip_session` is a third credential, resolved in
  the same middleware to the same `person:` actor:
  - Cookie requests must carry `X-Serendip-Csrf: 1`, and unsafe methods
    an exact `Origin`; never add CORS headers.
  - Bearer + cookie together is a 400.
  - Tokens live only in `session.go`'s in-memory table; nothing in `api/`
    may log, return or persist a token or a raw session ID.
  - Refresh is serialised per session (`ensureFresh`); keep it that way
    (rotated refresh tokens are single-use).
  - Login routes (`/auth/*`) live on the outer mux beside the console's
    static files, outside `AuthMiddleware`.
- Hardening applies to every client (`hardening.go`): server timeouts,
  a 1 MiB body limit (use `decodeBody`, which answers 413), and
  `Cache-Control: no-store` on `/v1`.
- Routes needing no auth (e.g. `GET /healthz`) are registered on a
  separate outer `http.ServeMux`, with the authenticated `/v1/` subtree
  mounted as a single `AuthMiddleware`-wrapped handler (`server.go`) —
  not a per-route bypass flag. Follow this pattern for any future
  no-auth route rather than special-casing inside `AuthMiddleware`.
- Every mutating endpoint creates desired state; reconcilers do the work.
- Errors: RFC 7807 problem+json. Every response includes request ID.
- Interface parity (constitution 7): UI/CLI/MCP consume THIS API only.
