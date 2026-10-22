# ADR 0017: The official MCP Go SDK for Serendip's MCP server

Status: Accepted
Date: 2026-10-21

## Context

Spec 021 gives Serendip an MCP server: `serendip mcp` over stdio for AI
assistants on a person's machine, and `/mcp` in `serendipd` over
streamable HTTP for hosted agents. CLAUDE.md puts the standard library
first, and any new dependency needs an ADR (constitution 4).

The protocol is moving fast:
- **2026-07-28 is stateless:** no `initialize` handshake and no sessions.
  Each request carries its version and client capabilities, and
  `server/discover` replaces the handshake.
- **Clients still speak 2025-11-25 and earlier.**
- **Claude Code 2.1.304 already speaks 2026-07-28** (spec 021 T001).

So a server for real clients has to speak two protocol generations, and
keep up with the next.

The user's priority, 2026-10-20: "less work, less to maintain". A
dependency that saves us tracking a churning protocol is worth more than a
hand-written layer we'd have to keep current.

Facts below were taken from the module cache and a build on 2026-10-21.

## Decision

**Adopt `github.com/modelcontextprotocol/go-sdk` v1.8.0**, pinned in
`go.mod`, used only by the top-level `mcp/` package (spec 021 T003).

- **Maintainers:** the Model Context Protocol project, with Google. It's
  past v1, and ships a minor version every one to two months.
- **Protocol coverage:** 2026-07-28, 2025-11-25, and older versions back
  to 2024-11-05, over stdio and streamable HTTP. A stateless mode suits
  `/mcp`.
- **Go:** it requires Go 1.25, which we're on.

### What it brings in

| Module | Version | Licence | Linked into the binary? |
|---|---|---|---|
| `github.com/modelcontextprotocol/go-sdk` | v1.8.0 | Apache-2.0, with older contributions under MIT | Yes: `mcp`, plus `auth`, `oauthex` and `jsonrpc`, which `mcp` imports |
| `github.com/google/jsonschema-go` | v0.4.3 | MIT | Yes |
| `github.com/segmentio/encoding` | v0.5.4 | MIT | Yes (fast JSON, used internally) |
| `github.com/segmentio/asm` | v1.1.3 | MIT | Yes (assembly helpers for the above) |
| `github.com/yosida95/uritemplate/v3` | v3.0.2 | BSD-3-Clause | Yes |
| `golang.org/x/oauth2` | v0.35.0 | BSD-3-Clause | Yes, through the SDK's `auth` package, though Serendip never calls it |
| `golang.org/x/sync` | v0.20.0 | BSD-3-Clause | Yes (`errgroup`) |
| `golang.org/x/time` | v0.15.0 | BSD-3-Clause | Yes (`rate`) |
| `golang.org/x/sys` | v0.41.0 | BSD-3-Clause | Yes (`cpu`) |

That's **9 modules, all permissively licensed**.

**Amended 2026-10-22 (spec 021 T011):** `cmd/serendip` also imports
`golang.org/x/sys/windows` directly. It sets an owner-only access list on
the CLI's configuration folder, because a folder's permissions on Windows
come from its parents, and `%AppData%` can grant other accounts read
access. The module was already in `go.mod` through the SDK; nothing new is
downloaded. The SDK's JWT dependency
(`golang-jwt`) is **not** linked.

- **Correction:** spec 021's plan first said its OAuth code wouldn't be
  compiled in. It is, because the `mcp` package imports `auth` itself.
  Serendip doesn't use it: token verification stays in
  `providers/identity/oidc`.
- **Size:** a stripped minimal server is 8.4 MB, against 5.9 MB for a
  stdlib-only program of the same shape, so about **+2.4 MB at most**.
  `serendipd` (14.5 MB stripped today) already links most of what they
  share, so its growth will be smaller. Spec 021 T010 measures memory.

### How it's contained

- **Only `mcp/` imports the SDK.** It's a thin adapter that registers
  `api/catalog`'s operations as tools. `bash
  .claude/scripts/check-imports.sh` keeps it out of `core/` and
  `providers/`, and spec 021 T003's test fails if any other package
  imports it.
- **Only the low-level `Server.AddTool` is used,** with our own schemas
  from the catalogue, not the SDK's reflection-based schema inference.
  Arguments are validated by the catalogue, and every decision stays with
  Serendip's API.
- **Authorization is Serendip's:** `/mcp` sits behind the same bearer
  verification as `/v1` (spec 021 plan decision 3). None of the SDK's
  auth middleware is used.
- **Upgrades** are a version bump in `go.mod`, plus the adapter's
  recorded-exchange tests for both protocol generations.

## Alternatives rejected

- **A hand-written protocol layer** (standard library, about 1,000 lines):
  - JSON-RPC, stdio, streamable HTTP, `server/discover`, plus the older
    `initialize` and `ping` for current clients.
  - Every protocol revision would be ours to track and test. Rejected for
    upkeep, at the user's priority. It was the plan's first draft, and
    `docs/build-vs-adopt.md` records the reversal.
- **`github.com/mark3labs/mcp-go`:** community-maintained, and without
  2026-07-28 support when checked (spec 021 research, 2026-10-20).

## Consequences

- **Nine new modules in `go.mod`,** to keep current. They're
  permissive, and none needs CGO.
- **Up to 2.4 MB more** in binaries that serve MCP: `serendip` and
  `serendipd`.
- **Elicitation is deferred:** confirming a destructive tool with the
  user, which 2026-07-28 does as an `input_required` round trip. It
  isn't a safety boundary: the API's approvals are (spec 019), and MCP
  clients such as Claude Code already ask before running a tool marked
  destructive.
- **Revisit:**
  - **if the SDK stalls,** or falls behind a protocol revision our
    clients need;
  - **if its dependencies grow** to something we wouldn't accept
    directly;
  - **or if the protocol settles** enough that a hand-written layer is
    cheap to keep.

  Replacing it is confined to `mcp/`.
