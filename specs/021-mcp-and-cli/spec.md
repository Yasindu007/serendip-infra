# Spec 021: Serendip's own MCP server and CLI

Status: Approved 2026-10-20. Done **before** spec 020, by
the user's order, so that 020's new actions land in the console, the CLI
and MCP together.

## Problem

Constitution rule 7: "UI = CLI = API = MCP… UI and CLI and MCP are clients
of the same API and the same RBAC/policy layer." `ai-operations.md` makes
MCP the AI's way in: "There is no MCP-only capability and no AI-only
control path." Serendip is meant to be a platform for people and AI alike.

Today Serendip has a REST API and a web console, and nothing else:
- **No CLI.** People and scripts use raw HTTP (`curl`) with API keys.
- **No MCP server.** An AI assistant can only reach Serendip by being
  handed raw HTTP and a credential.

OpenChoreo's own `occ` CLI and MCP server can't fill the gap, and
shouldn't:
- **Its API is closed to everyone** (risk register RP-018).
- **Even open, it would bypass** Serendip's RBAC, approvals and audit,
  which constitution 5 forbids for AI ("AI → Serendip Action → Policy
  check → … → Audit").

An admin-only way to use `occ` is deferred; the user asked to keep it in
mind.

## Goals

- **A Serendip CLI** for people and scripts. It covers the API's surface,
  with the same identity, permissions, approvals and audit.
- **A Serendip MCP server** for AI. Its tools express the same actions as
  the API and console, under an agent's guardrails (spec 018), with
  destructive actions going through approvals (spec 019).
- **Parity by construction:** a new API capability has a defined way to
  become a CLI command and an MCP tool, so spec 020 (and every later spec)
  ships all three surfaces together.

## Scope — IN

### Surface: today's API, in both the CLI and MCP

1. **Applications and Components:** list, get, create, update, delete,
   with cascade for Applications. Deletes go through approvals as the API
   does: decided by policy when roles allow, or a request when they need
   a person.
2. **Secrets:**
   - **Metadata:** list, get, create and delete.
   - **Values:** read and write, only with the data permissions
     (`serendip/secrets/value/*`).
   - **Never shown by default:** a value is never printed unless asked
     for explicitly, and never written to logs.
3. **Approvals:** list (with filters and paging), get, request, approve
   and reject, with comments.
4. **Access control:**
   - **Reading:** role definitions, role assignments at a scope, the
     caller's effective permissions, and "check access".
   - **Changing:** adding and removing assignments, under the same
     delegation and last-Owner rules.
5. **Platform state:** readiness of the capabilities, and the resource
   graph (children, dependents, blast radius).
6. **Session:** who am I, and which identity and permissions apply.

### The CLI (people and scripts)

7. **Sign-in as a person:**
   - **Through the identity provider,** with a browser on the same
     machine, or, when there's no browser, by opening the sign-in link
     anywhere and pasting back the address it lands on. No tokens are
     copied by hand. (Amended 2026-10-21: ThunderID 1.0.1 has no device
     authorization grant; plan decision 2.)
   - **The session is stored** only in the user's own configuration,
     readable by them alone.
   - **Sign-out** revokes it.
8. **Scripts:** use API keys (spec 002) or an agent token (spec 018), from
   the environment, never from command-line arguments, which leak through
   the process list.
9. **Output:**
   - human-readable tables by default;
   - JSON with a flag, for scripts;
   - exit codes distinguish denied, needs approval, not found, conflict
     and server error.
10. **Every mutating command can show the API request it would send,**
    like the console's "Same request through the API" panel, without
    sending it (a dry run).
11. **Approvals in the flow:** when a delete answers "needs approval", the
    CLI says so and can file the request with a reason. A person can
    list, inspect, approve and reject from the CLI.

### The MCP server (AI)

12. **Tools mirror the API,** one tool per action. Read tools are marked
    as reading; destructive tools as destructive, so MCP clients can warn.
13. **The AI is always an agent:**
    - An AI using MCP acts with an **agent's** identity and guardrails
      (spec 018): never access management, and deletes need an approval
      that a person decides.
    - That holds **even when a person launched the AI on their own
      machine.** An AI never quietly gets a person's full authority.
    - How a person's own assistant gets an agent identity, sponsored by
      that person, is the plan's main decision (see "Open questions").
14. **Destructive tools create approval requests.** Deleting through MCP
    returns that it needs an approval and offers to file the request, with
    a required reason. The AI can then check the request's state. It can
    never approve its own request.
15. **Secrets through MCP:** metadata only by default. Reading a value
    needs the data permission *and* is its own tool, so an AI client can
    be configured without it.
16. **Reaching it:**
    - **Locally,** for desktop AI clients over the standard local
      transport;
    - **Remotely,** for hosted agents (such as WSO2 Agent Manager's) over
      HTTP, authenticated with their agent tokens. It's served by
      `serendipd` or beside it, never with weaker authentication than the
      API's.
17. **Audit and attribution:**
    - Every action taken through MCP is audited with the agent as the
      actor and its sponsor recorded (spec 018).
    - The interface is `mcp`, so "done by an AI through MCP" is
      distinguishable from the console, CLI and API.
    - The CLI records its own interface the same way.

### Across both

18. **No new authority anywhere.** The CLI and the MCP server are pure
    clients of the API. Every decision is the API's (RBAC, approvals,
    audit), and neither has a privileged path.
19. **Parity rule for later specs:** a documented way, and a check (a
    test), that every API route is either exposed by the CLI and MCP or
    explicitly listed as not exposed, with a reason. Spec 020 adds its
    actions through it.
20. **Documentation:** a CLI reference, the MCP tool list, and how to
    connect common AI clients, all without credentials in examples.

## Scope — OUT (explicitly)

- **OpenChoreo's `occ` and MCP.** They stay closed (RP-018). The
  admin-only escape hatch is deferred.
- **An AI chat or "ask me anything" surface in the console.**
  `ai-operations.md` says AI is an operations engine, not a chatbot.
- **AI operations itself:** incidents, diagnosis, runbooks, and
  severity-based autonomy.
- **Managing agent issuers or creating agents** through the API (spec 018
  deferred it).
- **Spec 020's new actions** (deploy, promote, runtime, logs). 020 adds
  them through this spec's parity rule.
- **Shell completion, plugins, and a TUI.**
- **Publishing binaries or packages.** Built from the repo, like
  `serendipd`.

## Affected resources and capabilities

- **New programs:** the CLI, and the MCP server (or a mode of one
  program).
- **Identity:**
  - CLI sign-in for people (an OIDC flow for desktops and for machines
    without a browser, against the human issuer);
  - how a person-launched AI gets an agent identity (on-behalf-of, which
    `identity.md` lists as not done).
- **The API:** no new authority. Possibly small additions the clients
  need, such as a dry-run marker, or token endpoints for CLI sign-in.
- **Audit:** the `cli` and `mcp` interfaces.
- **Docs:** `ai-operations.md` (the MCP surface becomes real),
  `identity.md`, and a CLI and MCP reference.

## Acceptance criteria

1. **CLI sign-in:** a person signs in from the CLI through the identity
   provider (browser, or the paste-back fallback), runs a command, and
   signs out.
   Their session file is readable only by them.
2. **CLI parity:** every route in today's API is reachable through a CLI
   command, or listed as deliberately not exposed, with a reason. A test
   checks the list against the route table.
3. **CLI behaviour:**
   - JSON output parses.
   - Exit codes match the outcome (denied, needs approval, not found,
     conflict, server error).
   - A dry run prints the exact request and sends nothing.
   - A secret value is never printed without the explicit flag.
4. **CLI approvals:** a person's delete is approved by policy at once.
   An agent-token script's delete answers "needs approval"; the CLI files
   the request with a reason, and a person approves it from the CLI.
5. **MCP tools:** every API action is a tool, or listed as not exposed. A
   test checks it the same way as the CLI's. Tools carry correct
   read-only and destructive hints.
6. **The AI is an agent:**
   - **Through the remote transport,** a hosted agent's token works with
     its roles.
   - **A person-launched local AI** acts as an agent sponsored by that
     person, not as the person: it can't manage access, and its delete
     waits for an approval.
   - **Shown live:** the same action through the console (as the
     person) succeeds, while through MCP it needs approval.
7. **MCP approvals:** an AI's delete through MCP files an approval with
   its reason. The AI can read its state, can't approve it (403), and a
   person approves it in the console or CLI, after which it executes.
8. **Secrets through MCP:** without the data permission, value tools are
   refused (403), and metadata tools work.
9. **Audit:** actions through the CLI and MCP are audited with the right
   actor, sponsor and interface (`cli`, `mcp`).
10. **No new authority:** reviewers confirm that neither client has a
    privileged path. Every check is the API's.
11. **Real use:** an off-the-shelf MCP client connects locally, lists
    Applications, reads a Component, and files a delete request that a
    person approves.
12. **Footprint:** the MCP server's idle memory is measured. If it runs
    inside `serendipd`, rule 4 still passes.
13. **Nothing else breaks:** existing tests, the bench harness and a
    real-cluster run pass.

## Open questions (for plan.md)

1. **A person-launched AI's identity** (acceptance criterion 6). Options:
   - **register it as an agent** in an agent issuer, the person as its
     sponsor, with the AI using that agent's token;
   - **an on-behalf-of token exchange,** where the person signs in and
     the MCP server exchanges their session for a short-lived agent
     token sponsored by them, capped at the person's own roles and under
     agent guardrails;
   - **Serendip mints agent identities itself,** which ADR 0015 and spec
     018 would have to allow.

   The decision must keep "the AI is never the person" true.
2. **One program or two:** a single binary with `mcp` as a subcommand
   (`serendip mcp`), or separate binaries. And whether the remote MCP
   transport is served by `serendipd` itself.
3. **MCP implementation:** the MCP version and transports, and stdlib
   only versus an MCP SDK (a new dependency needs an ADR, constitution 4).
4. **CLI sign-in mechanics:** loopback redirect with PKCE, and/or the
   device authorization grant. What ThunderID supports, and how refresh
   is handled.
5. **The parity mechanism:** generated from the route table, or
   maintained by hand with a test.
