# Plan 021: Serendip's own MCP server and CLI

Status: Approved 2026-10-20. Spec: `spec.md` (approved 2026-10-20).

## What the research found (2026-10-20)

**MCP** (modelcontextprotocol.io):
- **The current version is 2026-07-28, and it's stateless.**
  - There's no `initialize` handshake and no sessions. Each request
    carries its protocol version and client capabilities in `_meta`.
  - `server/discover` is required. `ping` is removed.
  - Results carry `resultType` (`complete` or `input_required`).
- **Clients still speak 2025-11-25 and earlier,** with `initialize`,
  `Mcp-Session-Id` and `ping`. A server for real clients today handles
  both.
- **Streamable HTTP:** one POST endpoint. It checks the
  `MCP-Protocol-Version`, `Mcp-Method` and `Mcp-Name` headers, and
  **validates `Origin`**. JSON responses are enough (SSE is optional).
- **stdio** is newline-delimited JSON-RPC.
- **Tools:**
  - **Annotations** (`readOnlyHint`, `destructiveHint`, `idempotentHint`,
    `openWorldHint`) are hints clients may not trust. Set them explicitly;
    the defaults are pessimistic.
  - **`outputSchema` and `structuredContent`** are optional.
  - **Tool errors** go in the result (`isError`), not as JSON-RPC errors.
- **Elicitation** (a server asking the user to confirm or give a reason)
  exists, but whether today's clients support the 2026-07-28 form is
  unconfirmed.
- **Authorization (HTTP only):**
  - **The MCP endpoint is an OAuth resource server.** It publishes RFC
    9728 Protected Resource Metadata, answers 401 with
    `WWW-Authenticate: Bearer resource_metadata=…`, and validates the
    issuer, expiry and **audience** (RFC 8707).
  - **It must never pass a client's token through to another API.**
  - **A server that only accepts tokens from a fixed issuer** (agents
    using `client_credentials`) is allowed.
  - **stdio uses no OAuth;** it reads credentials from the environment.
- **The official Go SDK** (`github.com/modelcontextprotocol/go-sdk`,
  v1.8.0, Apache-2.0/MIT, maintained with Google):
  - supports 2026-07-28 and 2025-11-25 down to 2024-11-05, over stdio and
    streamable HTTP;
  - requires Go 1.25 (we're on 1.25.0);
  - adds 8 direct and 3 indirect modules, including `golang-jwt`,
    `golang.org/x/oauth2`, `jsonschema-go` and `segmentio/encoding`;
  - ships a new minor version every 1–2 months.
- **Claude Code connects to:**
  - **stdio:** `claude mcp add --transport stdio …`;
  - **HTTP with a bearer token:** `--transport http URL --header
    "Authorization: Bearer …"`, or `headersHelper`, a script that prints
    the header and is re-run on 401.

  `client_credentials` isn't built into Claude Code. Claude Desktop is
  best treated as stdio-only for a local install.

**ThunderID 1.0.1** (thunder-id/thunderid, tag v1.0.1):
- **Authorization code with PKCE for public clients:** yes. **Redirect
  URIs match exactly,** with no RFC 8252 any-port loopback. Our `serendip`
  client already has the fixed loopback `http://127.0.0.1:8250/callback`,
  registered "for a CLI later" (`providers/identity/thunderid/values.go`).
- **The Device Authorization Grant (RFC 8628): not supported.** CIBA
  exists, but it's a different flow.
- **Refresh tokens for public clients:** yes, with optional rotation; a
  replayed refresh token revokes its family. **Revocation**
  (`/oauth2/revoke`) exists.
- **Token exchange (RFC 8693):** yes, with actor tokens. Whether it emits
  an `act` claim is inferred, not seen.
- **Agents are a first-class resource:** `POST /agents` with an `owner`,
  defaulting to the caller. Their credentials use `client_credentials`;
  the secret is shown only on create or update, and the owner can't be
  deleted while the agent exists.

## Spike findings (T001, 2026-10-20, on `serendip-t018`)

Observed against ThunderID 1.0.1 with throwaway clients, deleted
afterwards; claims read, tokens never printed.

1. **Token exchange works, from a public client too.** The exchanged
   token keeps `sub` = the person and `iss`, and carries `grant_type:
   urn:ietf:params:oauth:grant-type:token-exchange`.
   - **`client_id` and `aud` are the exchanging client.** The scope is
     the subject token's, and a request for more (`system`) is silently
     dropped.
   - **Lifetime** is the exchanging client's access token setting (600 s
     in the spike). **No refresh token** is issued.
   - **`act` appears only with an `actor_token`** (and the client's
     `includeActClaim`). An actor token needs `client_credentials`, so a
     confidential client, so a secret on disk. Without one, there's no
     `act`.
   - **`resource=` must name a registered resource server;** `aud`
     otherwise defaults to the client.
   - **Chaining** (exchanging an exchanged token) is allowed, and keeps
     the same person and client.
   - **Logout stops it:** once the person's refresh token is revoked,
     ThunderID refuses to exchange the person's access token too ("Invalid
     subject_token"); it revokes the token family. Revoking the access
     token does the same.
2. **CLI sign-in through the `serendip` client works as planned.**
   Authorization code with PKCE on `http://127.0.0.1:8250/callback`, and
   refresh. **Refresh rotates:** each refresh issues a new refresh token,
   and reusing the old one is refused. `/oauth2/revoke` works from the
   public client (200), and a revoked refresh token is refused afterwards.
3. **Claude Code 2.1.304 speaks 2026-07-28,** over stdio and HTTP.
   - It opens with `server/discover`, not `initialize`; then `tools/list`
     and `tools/call`.
   - Over HTTP it sends `Mcp-Protocol-Version`, `Mcp-Method`, `Mcp-Name`
     (on calls) and the configured `Authorization` header. **It sends no
     `Origin`.**
   - It **declares elicitation** (`form` and `url`) and `roots`.

## Decisions

1. **A person-launched AI: token exchange ("on behalf of") first,
   decided by T001's proof of concept, with a separate agent identity as
   the fallback.** Revised 2026-10-20 at the user's question; the first
   draft had it the other way round.
   - **The preferred design (token exchange, RFC 8693, which ThunderID
     supports):**
     - **The exchange:** the person signs in (decision 2). `serendip mcp`
       exchanges their token for a short-lived token for Serendip's API,
       with the AI client as the **actor**.
     - **Serendip's verifier** treats a token whose actor is an AI client
       as a **delegated principal**: the person, acting through an agent.
     - **The policy engine** decides it **as the person** (their own
       roles, so it's capped at them automatically), **then applies
       spec 018's agent guardrails**: never access management, and every
       delete needs an Approval.
     - **Audit** records the person as the subject and the AI as the
       actor.
   - **Why it's better for this case:**
     - **no long-lived secret on disk:** the token dies with the person's
       session, and signing out revokes it;
     - **no role-granting friction:** the AI can do what the person can,
       within agent guardrails, and never more;
     - **less to build:** no agent creation through ThunderID, no new
       management-channel writes, and no agent routes in the API.
   - **T001 result: token exchange, with the exchanging client as the
     marker instead of `act`** (spike finding 1).
     - **A dedicated public client, `serendip-mcp`.** `serendip mcp`
       exchanges the person's token through it. **As built (T004):**
       - **Declared like `serendip`:** in the bootstrap file for fresh
         installs (`71-serendip-mcp-client.yaml`), and created when missing
         by the operator tool `cmd/serendip-identity-redirects` for
         existing ones. **`serendipd` gains no write path** to the identity
         provider (ADR 0015 stays read-only).
       - **ThunderID requires a public client to allow the authorization
         code grant** (APP-1037), so token exchange can't be its only
         grant. Signing in to `serendip-mcp` directly gives a token
         `serendipd` refuses, since it must come from an exchange
         (verified live).
     - **Serendip trusts the signed `client_id`, not `act`:**
       - **`serendip`**, not exchanged: the person.
       - **`serendip-mcp`**, exchanged: **delegated**, with the person as
         subject and `serendip-mcp` as actor. An `serendip-mcp` token that
         isn't an exchange is refused.
       - **Anything else** is refused, as today (the audience check).
     - **No actor token, so no secret on disk.** Which assistant it is
       (Claude Code, an IDE) is only a declared label in audit.
     - **Lifetime:** the exchanged token lasts 5 minutes, and `serendip
       mcp` re-exchanges as needed. Logout stops new exchanges, so the AI
       loses access within 5 minutes.
     - **`act` is honoured later:** if a token does carry one (an actor
       token, from a future agent), the verifier records it as the actor.
       Nothing depends on it in v1.
   - **If it doesn't work, the fallback** is a separate agent identity per
     AI, sponsored by the person:
     - `serendip agents create` makes a ThunderID agent owned by the
       person, through the management channel.
     - It acts as `agent:local/<id>` with its own role assignments, which
       start empty.
     - It needs agent routes, management-channel writes, and an on-disk
       secret (0600). It remains right for **autonomous or hosted**
       agents, which spec 018's agent issuers already cover.
   - **Rejected either way: Serendip issuing tokens itself.** That's
     building an identity provider.
   - **Both identity kinds, each built when needed** (agreed with the
     user, 2026-10-20):
     - **A person's own assistant:** token exchange, in this spec.
     - **An autonomous or hosted agent:** its own identity.
       - **From an external agent issuer** (WSO2 Agent Manager): exists
         since spec 018.
       - **Self-service in Serendip's own ThunderID** (an edge-1 user with
         no Agent Manager creates an agent they sponsor): a **separate
         small spec after 020**. It moves into this spec only if T001
         rules out token exchange, as the fallback above.

2. **CLI sign-in: authorization code with PKCE on the fixed loopback
   `127.0.0.1:8250`, with a paste-back fallback.**
   - **The normal case:** the CLI opens the browser, receives the code on
     `127.0.0.1:8250/callback`, and exchanges it with PKCE through the
     `serendip` public client that already exists.
   - **The fallback** (port busy, no browser, or remote shell): the CLI
     prints the sign-in link. The person opens it anywhere and pastes back
     the address the browser was sent to; the CLI takes the code from it
     and checks `state`.
   - **Tokens** are kept in the user's configuration directory (0600) and
     refreshed with the refresh token. `serendip logout` revokes them
     (`/oauth2/revoke`) and deletes the file.
   - **T001 confirmed** loopback PKCE, rotating refresh, and revocation as
     a public client (spike finding 2). The CLI stores the rotated refresh
     token each time.
   - **As built (T005):**
     - **The CLI finds the identity provider from the server,** through
       RFC 9728 Protected Resource Metadata at
       `/.well-known/oauth-protected-resource`, plus two extension members
       naming the CLI and assistant clients. Signing in needs only the
       server's address. The endpoint arrived in T005, not T007, because
       the CLI needs it first.
     - **`*.localhost` names dial loopback** (RFC 6761). Go's resolver on
       Windows doesn't, and a local install publishes
       `identity.localhost`.
     - **Credentials** are `credentials.json` (0600) under the user's
       configuration directory, written by atomic rename. **Corrected in
       T011:** `%AppData%` is *not* necessarily owner-only (on the
       development machine a sandbox group can read it). So every save
       gives the folder a protected, owner-only access list on Windows,
       and 0700 elsewhere (`cmd/serendip/private_*.go`).
     - **Refreshes are serialised by a lock file:** rotated refresh tokens
       are single-use, and ThunderID revokes the family on a replay.
     - **A Secret's value is never taken from the command line,** only
       from `@file` or standard input.
     - **Exit codes:** 0 ok, 1 error, 2 usage, 3 denied, 4 needs approval,
       5 not found, 6 conflict, 7 server error, 8 not signed in.
     - **Hardened after review** (go-reviewer, T005):
       - **A stored sign-in goes only to the server it was made for.**
       - **Plain `http` is refused** except to this machine, unless
         `--insecure` is given.
       - **The identity provider's endpoints must be on the issuer's
         origin,** over https or loopback, before a browser opens one or a
         refresh token is sent to one.
       - **The refresh lock holds a nonce,** and is taken over only by
         renaming it away, so two processes never refresh at once.
       - **Logout holds the lock.**
       - **A forged loopback request can't abort sign-in.**
       - **Proxies are bypassed for `*.localhost`.**
       - **Server-supplied text is stripped** of terminal control
         characters.
   - **`serendip mcp` (T006):**
     - **It runs only as the signed-in person through the assistant
       client.** It refuses `SERENDIP_API_KEY` and `SERENDIP_TOKEN`, which
       would give the AI a full credential of its own.
     - **Exchanged tokens are kept in memory** and exchanged again 30 s
       before they expire.
     - **The exchange is tried at start,** so a revoked sign-in fails
       there, not at the first tool call.
     - **Standard output carries only the protocol;** logs go to standard
       error.
   - **Spec amendment:** the spec's "device code" becomes this paste-back
     fallback, because ThunderID 1.0.1 has no device grant.
   - **Scripts** use `SERENDIP_API_KEY` or `SERENDIP_TOKEN` from the
     environment, never a command-line flag.

3. **One binary, `serendip`, plus the remote MCP endpoint in `serendipd`**
   (spec open question 2).
   - **`cmd/serendip`** is the CLI. `serendip mcp` runs the **stdio** MCP
     server for local AI clients. It's an HTTP client of the API, using
     the agent's credentials from decision 1.
   - **Remote MCP is served by `serendipd` itself,** at `/mcp`:
     - It's **the same resource server and audience as the API,** so
       there's no token passthrough: the caller's own token, issued for
       Serendip, is validated once.
     - Each tool call is dispatched **in process through the same
       authorizing API mux,** with the caller's identity. RBAC, approvals
       and audit are identical by construction.
     - It publishes RFC 9728 metadata pointing at the agent issuers, and
       checks `Origin`.
   - **As built (T007):** `api/mcp_handler.go`.
     - **Who may use it:** only an agent, or a person acting through an AI
       assistant (a delegated token). API keys and a person's own token
       get 403; no token or a bad one gets 401 with
       `WWW-Authenticate: Bearer resource_metadata=…`.
     - **Each tool call** is dispatched in process to the same `/v1`
       handler with only the caller's `Authorization`, under a 30 s
       deadline. It gets its own request ID, and the audit interface `mcp`,
       which no client can set.
     - **The token is verified twice:** at the gate, and again by `/v1`,
       which stays authoritative.
   - **Why not a separate MCP daemon:** one more process, one more port,
     one more credential path, and a second place to get authorization
     wrong.

4. **MCP implementation: the official Go SDK** (spec open question 3).
   Revised 2026-10-20 at the user's priority: "less work, less to
   maintain".
   - **Why:** the protocol is churning. 2026-07-28 removed the handshake
     and sessions, and clients still speak 2025-11-25 and earlier.
     Tracking several generations ourselves is the upkeep that bites. The
     SDK already speaks five protocol versions, is maintained by the MCP
     project with Google, and is v1.x.
   - **Weight (constitution 4, ADR 0017):** `go.mod` gains 9 modules,
     all permissively licensed. **Corrected in T003:** the SDK's `auth`
     package and `golang.org/x/oauth2` *are* linked, because the `mcp`
     package imports them itself. We never call them, and verify tokens
     ourselves. `golang-jwt` isn't linked. The SDK adds at most about
     2.4 MB to a stripped binary. Updates are a version bump plus our
     recorded-exchange tests.
   - **Contained:** a thin adapter registers the catalogue's tools with the
     SDK. Nothing else in Serendip imports it, so replacing it later is an
     ADR, not a rewrite.
   - **Rejected:** a hand-written stdlib protocol layer (about 1,000
     lines, plus tracking every spec revision), and `mark3labs/mcp-go`
     (community-maintained, no 2026-07-28 support).
   - **Note:** the SDK accepts 2026-07-28 over HTTP only in
     `Stateless` mode, which fits `/mcp` (decision 3).

5. **Elicitation is optional, never required.**
   - **A destructive tool's confirmation** uses `input_required`
     elicitation **only when the client declares support.** Otherwise the
     tool answers with what it would do and asks the model to call again
     with `confirm: true` and a `reason`.
   - **Either way, the server decides.** An agent's delete is still a
     `needs approval` request that a person decides (spec 019).
     Elicitation is a convenience, not a safety boundary.
   - **T001:** Claude Code 2.1.304 declares elicitation (`form`), so
     the confirmation path is real for it. Other clients still get the
     fallback.
   - **Amended in T003: elicitation is deferred,** and so is the
     `confirm: true` fallback.
     - In 2026-07-28, elicitation in a stateless server is an
       `input_required` round trip, not a simple server call.
     - It isn't a safety boundary: the API's approvals are. An agent's
       delete always answers "needs approval".
     - Clients already ask before running a tool marked destructive
       (Claude Code does).
     - A confirmation step would add a round trip and code, but no
       protection. ADR 0017 records this, and a later spec can add it.

6. **One shared catalogue drives the CLI, MCP and parity** (spec open
   question 5).
   - **`api/catalog`** lists every route with its CLI command, its MCP
     tool (name, description, input schema, annotations), or `notExposed:
     <reason>`.
   - **A test fails** when `routeTable()` has a route the catalogue
     doesn't, or the other way round. Spec 020 adds its routes there.
   - **The CLI and MCP both read the catalogue.** That's one source of
     truth, and the console's "Same request through the API" text matches
     it.
   - **Not exposed in v1:**
     - **Console session and login routes:** browser-only.
     - **The directory search:** CLI only, for people; agents can't read
       the directory anyway (spec 018).

7. **Interface in audit.**
   - **`/mcp` in `serendipd`** sets `mcp` itself, authoritatively.
   - **The CLI and `serendip mcp`** declare `cli` or `mcp` in
     `X-Serendip-Client`. The API records a declared interface only from
     a known set, and audit marks it as declared (`cli (declared)`).
     Identity, not the interface label, is the security boundary.
     Spoofing the label only mislabels one's own rows.

8. **Secrets through MCP:**
   - **Separate tools for values:** `secrets_read_value` and
     `secrets_write_value` are their own tools, marked as reading
     sensitive data and as destructive.
   - **`serendip mcp --no-secret-values`** omits them from `tools/list`
     altogether.
   - **Values never appear in MCP logs** (stderr), and the CLI prints a
     value only with `--show-value`.

## Files

- **cmd:**
  - `cmd/serendip/` (new): CLI commands from the catalogue, login, config,
    and the `mcp` subcommand.
  - `cmd/serendipd`: wire `/mcp`, and the built-in `local` agent issuer.
- **mcp (new top-level):** a thin adapter from the catalogue to the MCP Go
  SDK (stdio and streamable HTTP). It's the only importer of the SDK.
- **api:**
  - `api/catalog/` (new, with the parity test);
  - the delegated-principal handling (token exchange), or the agent
    routes if T001 picks the fallback;
  - the `/mcp` handler and RFC 9728 metadata;
  - the declared-interface audit;
  - route-table rules.
- **providers:** `providers/identity/oidc` and `thunderid`: the
  token-exchange client, and actor-claim verification. Agent creation
  through the management channel only on the fallback.
- **core:**
  - `core/identity`: the delegated principal (subject plus actor);
  - `core/policy`: decide as the subject, then apply agent guardrails to
    a delegated principal;
  - `core/audit`: declared interfaces.
- **docs:**
  - an ADR for decisions 1, 3 and 4: the agent identity per AI and the
    in-process MCP;
  - `ai-operations.md` (the MCP surface becomes real) and `identity.md`;
  - a CLI and MCP reference, including client configuration with no
    credentials inline;
  - the risk register.

## Risks

- **The management channel gains writes** (creating agents and secrets).
  It needs its own audit, and creation is limited to oneself as sponsor.
  Deleting an agent is an approved delete.
- **Two MCP protocol generations to support.** Recorded-exchange tests
  pin both. A future spec revision may need changes: watch the
  changelog.
- **The agent's secret on disk** (0600) is a credential. It can be rotated
  (`serendip agents rotate`) and revoked by deleting the agent. On
  Windows the CLI writes it with an owner-only ACL.
- **Agents start with no roles.** Someone who may assign roles must grant
  them. That's honest friction, recorded as an RP entry until a
  sponsor-delegation rule exists.
- **Clients may not support elicitation** (decision 5): degraded, but
  still safe.
- **Fixed port 8250** may be taken. The paste-back fallback covers it.

## Explicitly NOT done

Everything in spec "Scope — OUT", plus:
- whichever of token exchange or separate agent identities T001 doesn't
  pick (both are recorded in `docs/build-vs-adopt.md`);
- a sponsor-delegation rule for granting one's own agent roles (only
  relevant on the fallback);
- a hand-written MCP protocol layer;
- MCP resources and prompts (tools only);
- `headersHelper` packaging for Claude Code's remote mode (documented
  manually).
