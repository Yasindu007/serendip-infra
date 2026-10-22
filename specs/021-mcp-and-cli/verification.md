# Spec 021 verification (T011, real use)

Run 2026-10-22 on `serendip-t018`: kind, `kindest/node:v1.34.0`, with the
spec 017/018 stack (storage, OpenBao, kgateway, OpenChoreo, ThunderID
1.0.1). Tokens, codes, passwords and keys were never printed.

## Setup

- **`serendipd`:** built from spec 021's code (`d6f9980` plus the fix
  below), on `:8088`, with a **copy** of spec 019 T011's store, so it
  had Applications and Components. Identity is enabled, so `/mcp` and
  `/.well-known/oauth-protected-resource` are served.
- **OpenBao:** unsealed with spec 018 T001's `openbao-init.json`, as in
  spec 019.
- **ThunderID:** has the `serendip-mcp` client, created on 2026-10-21 by
  `cmd/serendip-identity-redirects` (T004).
- **Sign-in:** `serendip login --no-browser` (the paste-back path) was
  driven by a scratch program standing in for the browser.
  - It read the sign-in link the CLI printed, and signed in to ThunderID
    as ThunderID's own sign-in page does: authorize, the credentials flow
    as `admin`, then the flow callback.
  - It pasted back the address the browser would land on.
  - Everything else is the real CLI against the real ThunderID.
  - **A person signing in through a real browser is still to be shown.**
    The browser path (the loopback on 127.0.0.1:8250) is covered by
    `cmd/serendip` tests only.
- **The AI client:** Claude Code 2.1.304, `claude -p` with
  `--mcp-config` starting `serendip.exe --server http://localhost:8088
  mcp` over stdio, with `--strict-mcp-config` and only `mcp__serendip__*`
  tools allowed.

## Evidence, by acceptance criterion

1. **CLI sign-in:**
   - **The server names its identity provider.** `GET
     /.well-known/oauth-protected-resource` answered with issuer
     `http://identity.localhost:30080`, CLI client `serendip` and
     assistant client `serendip-mcp`.
   - **Sign-in worked:** `serendip login --no-browser` printed "Signed in
     to http://localhost:8088.", and `serendip whoami` answered `{"actor":
     "person:admin", "method": "bearer"}`.
   - **`serendip logout`** revoked the session at ThunderID ("Signed
     out."), and signing in again worked.
   - **The session file is private:** see "Found and fixed" below. After
     the fix, `icacls` showed the folder `YASINDU_DS\malit:(OI)(CI)(F)`
     and `credentials.json` `YASINDU_DS\malit:(I)(F)`, nothing else.
6. **The AI is an agent:**
   - **The person,** through the CLI, can do anything Owner allows.
   - **Their Claude Code,** through `serendip mcp`, listed the
     Applications (`t019-app`, `t019-stale`) and read Component
     `t019-stale-web`. Its `components_delete` answered "HTTP 403,
     approval required (an agent's delete needs an approval). Nothing was
     changed."
7. **MCP approvals:**
   - **Claude Code filed the request** with `approvals_request`: Approval
     `01a16432-1954-7ee4-a52c-6f91f9388532`, **Pending**, with reason
     "T011: stale test component, no longer used".
   - **It read it back** with `approvals_get`.
   - **Asked to approve it itself,** it reported that no tool it has can,
     because approve and reject aren't MCP tools.
   - **The stored request** names `requestedBy.actor`
     `agent:serendip-mcp/01900000-0000-7000-8000-000000000030`, with the
     principal `{type: person, id: <admin>, via: serendip-mcp}` and the
     admin as sponsor.
   - **The person approved it from the CLI** (`serendip approvals approve
     <id> --comment …`): **Approved**, decided by `person:admin`.
   - **The approval reconciler then executed it: Executed.** `serendip
     components get <id>` exited 5 (not found).
9. **Audit:** the store's rows for that Approval and Component:
   - the AI's request, as `agent:serendip-mcp/<admin>`, with sponsor the
     admin and interface **`mcp (declared)`**;
   - the person's decision, as `person:admin`, interface **`cli
     (declared)`**;
   - the deletion, as `system:reconciler`.
16. **Remote MCP** (`/mcp`, HTTP, 2026-07-28 requests):
   - **The person's token, exchanged** for `serendip-mcp` at ThunderID's
     token endpoint (200): `tools/call applications_list` answered 200,
     with the list.
   - **The person's own token:** 403, "a person's own token can't be used
     through MCP; an AI acts for you with an agent's guardrails through
     serendip mcp".
   - **The API key:** 403, "an API key can't be used through MCP: an AI
     acts as an agent, never with a full credential".
   - **No token:** 401, with `WWW-Authenticate: Bearer
     resource_metadata="http://localhost:8088/.well-known/oauth-protected-resource"`.
   - **An Agent Manager agent's token against `/mcp`:** not run live,
     because `amp-local` is stopped. The agent-issuer path through `/mcp`
     is covered by `api/mcp_handler_test.go`, and the issuer itself by
     spec 018's live run.

## Found and fixed during T011

**The CLI's session file wasn't private on Windows.** The plan assumed
`%AppData%` is restricted to its owner. On this machine `%AppData%`
grants a `CodexSandboxUsers` group read access, inherited. The scratch
configuration folder under `%TEMP%` granted several accounts *modify*, so
`credentials.json` inherited them.

**Fix:** `cmd/serendip/private_windows.go` gives the configuration folder
a protected access list naming only the current user, on every save. The
file inherits it. On other systems the folder is tightened to 0700, even
if it already existed.
- **Test:** `TestCredentialsArePrivateOnWindows` reads both access lists
  back.
- **New import:** this uses `golang.org/x/sys/windows`, a direct import
  of a module ADR 0017 already brought in (the ADR is amended).

## Footprint

See `profiles/edge-1/bench/results/2026-10-22-spec021-mcp/summary.md`:
`serendipd` +0.7 MiB at idle; 22.8 MiB live with `/mcp`; `serendip
mcp` 17.8 MiB idle.
