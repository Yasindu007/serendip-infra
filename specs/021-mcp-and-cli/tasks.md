# Tasks: Spec 021 — Serendip's MCP server and CLI

Ordered. `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests or docs). Run `contract-guardian` before committing anything under
`core/` or `providers/`, and `go-reviewer` after each non-trivial Go task.

Dependency sketch: T001 → T002 → (T003 ∥ T004) → T005 → T006 → (T007 ∥
T008) → T009 → T010 → T011 → T012 → T013.

## T001 — Spike: ThunderID agents, PKCE loopback, and MCP clients

- **Do**, on `serendip-t018`:
  1. **Agents:** create an agent in Serendip's ThunderID through the
     management channel (`POST /agents` with `owner` set to a person),
     get a `client_credentials` token, and record its claims (`iss`,
     `sub`, `aud`, `grant_type`, owner visibility).
     - Check whether a non-admin token can call `POST /agents`.
     - Check whether `resource=` sets `aud`.
  2. **CLI sign-in:** authorization code with PKCE through the `serendip`
     client on `127.0.0.1:8250/callback`, then refresh, then
     `/oauth2/revoke` as a public client.
  3. **MCP clients:** run a throwaway stdlib MCP stub (scratch) and
     record which protocol versions and methods Claude Code sends over
     stdio and over HTTP, and whether it declares elicitation.
- **Files**: `plan.md` ("Spike findings"; decisions 1, 2, 4 and 5 amended
  if the facts differ).
- **Verify**: each item has observed evidence. Scratch clients are
  removed afterwards.

## T002 — Spec amendment and the catalogue

- **Do**:
  - Amend spec.md: the device code becomes the paste-back fallback
    (plan decision 2).
  - Create `api/catalog`: every current route with its CLI command, MCP
    tool (name, description, input schema, annotations), or `notExposed`
    reason.
  - Add the parity test against `routeTable()`.
- **Files**: `specs/021-mcp-and-cli/spec.md`, `api/catalog/*.go`,
  `api/catalog/catalog_test.go`.
- **Verify**: `go test ./api/...`. The parity test fails when a route is
  added to `routeTable()` without an entry (checked by adding one in a
  test-only mux).

## T003 [P] — The `mcp` protocol package

- **Do**: `mcp/` (stdlib only):
  - JSON-RPC 2.0;
  - the stdio transport;
  - the streamable HTTP handler (POST only, header checks, `Origin`
    validation, JSON responses);
  - `server/discover`, `tools/list` (paginated, with `ttlMs` and
    `cacheScope`) and `tools/call`;
  - for older clients, `initialize`, `notifications/initialized`, `ping`,
    and tolerating `Mcp-Session-Id`;
  - tool errors as `isError` results;
  - optional elicitation.
- **Files**: `mcp/*.go`, `mcp/*_test.go` and `mcp/testdata/` (recorded
  exchanges for both protocol generations).
- **Verify**:
  - `go test ./mcp/...`, table-driven over recorded requests: correct
    responses, rejected bad headers or origins, and both generations.
  - `bash .claude/scripts/check-imports.sh`. `mcp/` imports nothing from
    `core/` or `providers/`.

## T004 [P] — Agent identities through Serendip

- **Do**:
  - Add `core/identity.AgentRegistry` (create with a sponsor, rotate,
    delete, list).
  - Implement it in the ThunderID provider over the management channel.
  - Add the built-in `local` agent issuer.
  - Add the API routes `POST/GET /v1/agents`,
    `POST /v1/agents/{id}/secret` and `DELETE /v1/agents/{id}` (through an
    Approval), with their route rules and audit.
- **Files**: `core/identity/*.go`, `providers/identity/thunderid/agents.go`
  and tests, `api/agents.go` and tests, `api/authz.go`,
  `cmd/serendipd/wiring.go`.
- **Verify**:
  - `go test ./...`. Tests cover:
    - creating an agent with yourself as sponsor (anyone signed in);
    - a sponsor that isn't the caller, refused;
    - the secret returned once;
    - delete through an Approval;
    - the `local` agent token resolving to `agent:local/<id>` with that
      sponsor;
    - a `local` *person* token staying a person.
- **Then**: run `contract-guardian` on T003–T004.

## T005 — The CLI

- **Do**: `cmd/serendip`:
  - `login`: PKCE loopback with the paste-back fallback, a 0600 token
    file (an owner-only ACL on Windows), and refresh;
  - `logout`, which revokes;
  - `whoami`;
  - every catalogue command, with table and JSON output;
  - exit codes;
  - `--dry-run`, which prints the request;
  - `--show-value` for secrets;
  - approvals in the flow ("needs approval: file it? `--reason`");
  - `agents create|list|rotate|delete`, storing the agent's credentials.
- **Files**: `cmd/serendip/*.go` and tests.
- **Verify**:
  - `go test ./cmd/serendip/...`, against an in-process `serendipd`
    test server. Tests cover JSON parsing, exit codes, a dry run that
    sends nothing, values hidden by default, credentials from the
    environment only, and token file permissions.
  - `go build`.

## T006 — `serendip mcp` (stdio)

- **Do**: the stdio MCP server in the CLI, with tools from the catalogue,
  run as the agent from the local credentials (`client_credentials` to
  Serendip's ThunderID). Add `--no-secret-values`, and the declared `mcp`
  interface.
- **Files**: `cmd/serendip/mcp.go` and tests.
- **Verify**: `go test`. A recorded stdio session lists tools, reads an
  Application, and files a delete request. The tools omit secret values
  with the flag.

## T007 [P] — `/mcp` in `serendipd` (remote)

- **Do**:
  - Serve the streamable HTTP handler at `/mcp`, outside the console's
    session middleware: bearer tokens only, with the same verifier and
    audience as the API.
  - Dispatch each tool in process through the authorizing mux.
  - Publish RFC 9728 metadata at `/.well-known/oauth-protected-resource`.
  - Answer 401 with `WWW-Authenticate`.
  - Set the audit interface `mcp` authoritatively.
- **Files**: `api/mcp_handler.go` and tests, `api/server.go`.
- **Verify**: `go test ./api/...`. Tests cover:
  - an agent token's tool calls decided by its roles;
  - a delete answering "needs approval", then a request filed;
  - a person token allowed to read but never treated as an agent;
  - no token: 401 with metadata;
  - a wrong audience: 401;
  - a bad `Origin`: 403;
  - audit rows with `mcp`.

## T008 [P] — The declared interface in audit

- **Do**: accept `X-Serendip-Client: cli|mcp` from a known set, and record
  it as declared.
- **Files**: `api/middleware.go`, `core/audit`, and tests.
- **Verify**: `go test`. A known value is recorded as declared, and an
  unknown one is ignored.
- **Then**: run `contract-guardian` and `go-reviewer` on T005–T008, then
  commit.

## T009 [A] — End-to-end acceptance tests

- **Do**: tests for spec acceptance criteria 1–10 in process. A person
  CLI session, an agent CLI script, stdio MCP, and remote MCP, all against
  one test `serendipd`, including "the same action as the person
  succeeds, through MCP it needs approval".
- **Files**: `cmd/serendip/acceptance_test.go`,
  `api/mcp_acceptance_test.go`.
- **Verify**: `go test -race ./...` is green.

## T010 — Footprint

- **Do**: measure `serendipd`'s idle memory with `/mcp` enabled against
  spec 019's baseline, and the stdio `serendip mcp` process at idle.
- **Files**: `profiles/edge-1/bench/results/2026-10-xx-spec021-mcp/`.
- **Verify**: rule 4 passes, and the numbers are recorded.

## T011 — Real use

- **Do**, on `serendip-t018`:
  - a person runs `serendip login` against the real ThunderID, then
    `serendip agents create`;
  - Claude Code is connected to `serendip mcp` (stdio) and lists
    Applications, reads a Component, and files a delete request, which
    the person approves in the console or CLI before it executes;
  - a remote check: an Agent Manager agent token against `/mcp`, if that
    cluster is running; otherwise a `local` agent token over HTTP.
- **Files**: `specs/021-mcp-and-cli/verification.md`.
- **Verify**: every criterion has observed evidence. No secrets are
  printed.

## T012 — Final reviews

- **Do**: run `contract-guardian`, `go-reviewer` and `/security-review` on
  the spec diff, and fix the findings. The security review should focus
  on the token file, the management channel's writes, `/mcp`
  authentication, `Origin` handling, and secrets through MCP.
- **Files**: as the findings require.
- **Verify**: all three pass, and `go test ./...` is green.

## T013 [A] — Docs, ADR, close

- **Do**:
  - Write the ADR for decisions 1, 3 and 4.
  - Update `ai-operations.md` and `identity.md`.
  - Write the CLI and MCP reference, with client configuration examples
    that contain no credentials.
  - Add the risk register entries: agent role friction, protocol churn,
    and the secret on disk.
  - Update `docs/PROGRESS.md`, with 020 next.
- **Files**: `docs/decisions/00NN-*.md`, `docs/architecture/*.md`,
  `docs/cli-and-mcp.md`, `docs/risk-register.md`, `docs/PROGRESS.md`.
- **Verify**: `docs-keeper` reports the docs in sync. Every catalogue
  entry appears in the reference.
