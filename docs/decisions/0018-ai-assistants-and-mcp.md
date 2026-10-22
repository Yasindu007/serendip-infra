# ADR 0018: A person's AI assistant acts through token exchange; MCP is served inside serendipd

Status: Accepted
Date: 2026-10-22

## Context

Spec 021 gives Serendip a CLI and an MCP server. Two rules shape how an AI
reaches Serendip:
- **Constitution 5:** an AI acts through Serendip's own actions, policy
  and audit.
- **The spec:** an AI is always an agent, "even when a person launched the
  AI on their own machine", and never quietly gets a person's full
  authority.

Spec 018 already makes agents from external agent issuers (WSO2 Agent
Manager) principals with sponsors and guardrails: no access management,
and deletes through approvals. Two questions were left:
- **How does an assistant a person runs** (Claude Code, an IDE) get an
  agent's guardrails?
- **Where does the remote MCP transport live?**

## Decision

### 1. A person's assistant: token exchange, marked by the client

**The exchange:** `serendip mcp` exchanges the person's CLI sign-in for a
short-lived token issued to a dedicated public client, `serendip-mcp`
(RFC 8693 token exchange, ThunderID 1.0.1).

**`serendipd` trusts the signed `client_id`:**
- **A `serendip-mcp` token from a token exchange** is the person *acting
  through an assistant*: `identity.Principal.Via` and
  `policy.Principal.Via` are set.
- **A `serendip-mcp` token from any other grant** (signing in to it
  directly) is refused.
- **An exchanged token for any other client** is refused.

**Policy decides with the person's own role assignments,** so the
assistant can never do more than the person. The agent guardrails apply
on top (`Principal.guarded()`):
- **never access management;**
- **every delete needs an Approval** that a person decides;
- **the assistant never decides an approval.** The person may decide what
  their assistant requested, as a sponsor decides their agent's.

**Audit records** `agent:serendip-mcp/<subject>`, with the person as
sponsor.

**Tokens and logout:** exchanged tokens last 5 minutes. Signing out
revokes the person's token family, so ThunderID refuses further exchanges.

**The `serendip-mcp` client** comes from ThunderID's bootstrap file on
fresh installs, and from the operator tool on existing ones. `serendipd`
gains no write path to the identity provider (ADR 0015 stays read-only).

### 2. Remote MCP inside serendipd, dispatching in process

**`serendipd` serves `/mcp`** (streamable HTTP, stateless) beside `/v1`.
- **Each tool call** is dispatched in process to the same `/v1` handler
  with only the caller's `Authorization`, so RBAC, approvals and audit
  are the API's by construction. No token is passed to another service.
- **Who may call it:** only agents and delegated assistants. API keys and
  a person's own token are refused.
- **Audit:** the interface `mcp` is set by the server, and can't be
  declared by a client.
- **Signing in:** a 401 points at the RFC 9728 document
  (`/.well-known/oauth-protected-resource`), which also tells the CLI
  where to sign in.

## Alternatives rejected

- **A separate agent identity per assistant** (an agent created in
  ThunderID, owned by the person):
  - **Kept for autonomous agents.** For a person's assistant it means a
    long-lived secret on disk, an admin granting the agent roles, and the
    management channel gaining writes.
  - **It stays the fallback, and the model for self-service autonomous
    agents** (a later spec); external agent issuers already cover hosted
    ones (spec 018).
- **ThunderID's `act` claim as the marker:** it needs an actor token from
  a confidential client, so a secret on disk again (spec 021 T001).
  Signed `client_id` plus `grant_type` mark delegation without one.
- **Serendip issuing its own tokens:** that's building an identity
  provider.
- **A separate MCP daemon:** another process, port and credential path,
  and a second place to get authorization wrong.
- **Letting a person's own token or an API key use `/mcp`:** that would
  hand an AI a full credential.

## Consequences

- **`core/identity.Principal`, `core/policy.Principal` and
  `core/resource.PrincipalRef` gain `Via`.** Every agent guardrail checks
  `guarded()`, not the principal type alone.
- **Two accepted risks:**
  - **RP-019:** a local assistant running as the person can read their
    CLI sign-in.
  - **RP-021:** an assistant keeps access for up to 5 minutes after the
    person signs out.
- **Which assistant acted** (Claude Code or another) is only the client's
  declared label. Identity is the person through `serendip-mcp`.
- **ThunderID-specific facts** live in
  `providers/identity/thunderid/VERSIONS.md`; another identity provider
  needs the same exchange and a delegate client.
