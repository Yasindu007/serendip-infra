# Plan 021: Serendip's own MCP server and CLI

Status: Draft, awaiting approval. Spec: `spec.md` (approved 2026-10-20).

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

## Decisions

1. **A person-launched AI gets its own agent identity, sponsored by that
   person** (spec open question 1).
   - **How it works:**
     - A person creates an agent through Serendip, for example `serendip
       agents create laptop-assistant`.
     - Serendip creates it in **its own ThunderID** as an agent owned by
       that person, through the management channel (ADR 0015, which gains
       its first write).
     - The client secret is returned **once**, and the CLI stores it in the
       person's configuration (mode 0600).
     - The local MCP server (`serendip mcp`) gets a `client_credentials`
       token with it and acts as `agent:local/<id>`, with spec 018's
       guardrails and the person as sponsor.
   - **Serendip's own ThunderID becomes a built-in agent issuer** (alias
     `local`). It's routed by issuer like `SERENDIP_AGENT_ISSUERS`, and
     only its agent tokens become agents; its people stay people.
   - **What the agent may do** comes from **role assignments**, made like
     any other. It starts with **none**, since "a valid token grants
     nothing" (spec 018).
     - In v1 someone who may assign roles grants them: on edge-1 that's
       the Owner.
     - A rule letting a sponsor grant their own agent the roles they
       themselves hold is a later spec, recorded as an RP entry.
   - **Why this option:**
     - **The AI is provably never the person.** It's a different
       principal, with its own audit identity and guardrails.
     - **It reuses spec 018's model unchanged** (agent principal, sponsor,
       guardrails) and ThunderID's own agent resource. That's
       Azure-like: a managed identity per app.
   - **Rejected:**
     - **Token exchange (the person's token for a delegated one).**
       ThunderID supports it, but `act` is unverified. "Capped at the
       person's roles" would need a new intersection rule in the policy
       engine, and the result would still look like the person to the
       verifier unless we invent delegation claims. Revisit when
       on-behalf-of is its own spec.
     - **Serendip issuing tokens itself:** that's building an identity
       provider, and ThunderID already does it.
   - **New API routes:** `POST /v1/agents` (create an agent with yourself
     as sponsor; any signed-in person), `GET /v1/agents` (yours, or
     everyone's for those who can read access management),
     `DELETE /v1/agents/{id}` (removing an agent and revoking its
     credentials goes through an Approval, spec 019), and
     `POST /v1/agents/{id}/secret` (rotation).

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
   - **Why not a separate MCP daemon:** one more process, one more port,
     one more credential path, and a second place to get authorization
     wrong.

4. **MCP implementation: the standard library, not the SDK** (spec open
   question 3). This is a judgement call, made deliberately (constitution
   4).
   - **What we need is narrow:**
     - JSON-RPC over stdio and one POST endpoint;
     - `server/discover`, `tools/list` and `tools/call`;
     - and for older clients, `initialize`, `notifications/initialized`
       and `ping`, plus tolerating `Mcp-Session-Id`.
   - **What the SDK would add is mostly unused:** OAuth client support,
     schema generation, a JWT library, and `x/oauth2`. We already verify
     tokens ourselves (`providers/identity/oidc`), and our tool schemas are
     hand-written. Its upgrade churn would also be ours to track.
   - **Estimate:** 800–1,200 lines in a new top-level package `mcp/`
     (protocol only, no Serendip logic), table-tested against recorded
     message exchanges for both protocol generations.
   - **Revisit if** the protocol's churn or a client incompatibility
     costs more than adopting the SDK would. That switch would need an ADR
     (it's in the risk register).

5. **Elicitation is optional, never required.**
   - **A destructive tool's confirmation** uses `input_required`
     elicitation **only when the client declares support.** Otherwise the
     tool answers with what it would do and asks the model to call again
     with `confirm: true` and a `reason`.
   - **Either way, the server decides.** An agent's delete is still a
     `needs approval` request that a person decides (spec 019).
     Elicitation is a convenience, not a safety boundary.

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
- **mcp (new top-level, stdlib):** JSON-RPC, stdio, HTTP transport, both
  protocol generations, and discovery and tools dispatch from the
  catalogue.
- **api:**
  - `api/catalog/` (new, with the parity test);
  - `api/agents.go`, the agent routes;
  - the `/mcp` handler and RFC 9728 metadata;
  - the declared-interface audit;
  - route-table rules.
- **providers:** `providers/identity/thunderid` (create, rotate and
  delete agents through the management channel).
- **core:**
  - `core/identity`: an `AgentRegistry` interface for creating, rotating
    and deleting agents; capability-shaped, with no product names.
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
- token exchange and on-behalf-of delegation;
- a sponsor-delegation rule for granting one's own agent roles;
- the MCP Go SDK;
- MCP resources and prompts (tools only);
- `headersHelper` packaging for Claude Code's remote mode (documented
  manually).
