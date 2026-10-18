# Spec 018: Agent identities (WSO2 Agent Manager POC) and the identity-provider management channel

Status: Approved 2026-10-18

## Problem

Spec 017 gave Serendip Azure-style roles at scopes, and it reserved an
`agent` principal type that must name a human sponsor. Nothing can use
that type yet. Serendip accepts tokens only from its own identity
provider (ThunderID) and treats every token holder as a person. So there
is no way for an AI agent to call the API under its own, narrow,
auditable identity. Constitution 5 requires exactly that: AI never gets
broad power, and every action flows through policy and audit.

WSO2 Agent Manager (1.0.0 GA, 2026-09-07, Apache-2.0) already manages
agents and issues each one an identity in ThunderID: an AgentID with a
`client_credentials` client. The user's direction (2026-10-18) is a
**proof of concept**, beside Serendip, never inside edge-1. In it,
Serendip trusts Agent Manager's ThunderID as a second token issuer, for
agents only, and maps agents to role assignments.

A second gap blocks this, and spec 017 found it. **`serendipd` can't
call ThunderID's management API.** The Kubernetes Service proxy strips
the `Authorization` header, so we can't use the path we use for OpenBao.
The effects today:

- **Check access** shows a person's groups as they were at their last
  sign-in, not as they are now.
- **The principal picker** only knows people who have already signed in.
- **Anything we automate later in the identity provider** would stay a
  manual operator step: agent identities, Serendip-managed groups,
  automatic console URIs, credential cleanup.

Research (2026-10-18, ThunderID 1.0.1) also found that **the management
API shares ThunderID's one port with OIDC.** That means it's reachable
through the public Gateway route today, protected only by its
bearer-token check. The user decided this restriction belongs in this
spec, done before or together with anything that relies on the
management channel.

## Scope: IN

### Part A: the identity-provider management channel

- **Restrict the public identity route** to the paths a browser and an
  OIDC client need, which are login, discovery, flows and ThunderID's own
  console. The plan confirms the exact list from upstream. Requests to
  the management API from outside are refused at the Gateway.
- **One channel for `serendipd` to reach the management API.** Plan.md
  chooses it after weighing privilege, code size and dependencies
  (constitution 4), and records the choice in an ADR. The candidates:
  - **acting as the signed-in person** (Azure's pattern: the portal calls
    Microsoft Graph as you). The admin's management token is used, and
    there's no standing credential. Research rates this as likely
    feasible, so plan.md starts with a spike to test it;
  - a workload identity holding a management credential. On ThunderID
    1.0.1 this is effectively a full-admin secret, because finer
    permissions aren't grantable yet;
  - a restricted cluster-internal route;
  - an in-process port-forward tunnel.

  The channel is settled once, as the channel for every later
  identity-provider feature. It sits behind the Identity capability's
  interface. No ThunderID concept reaches `core/` (constitution 1).
- **Live lookups that use the channel:**
  - **Check access** resolves a person's current groups from the
    identity provider and marks the answer as live. When the provider
    can't be reached, it falls back to groups from the last sign-in,
    labelled as such.
  - **The principal picker** searches the identity provider's people and
    groups, not only those who have signed in.
  - Both are ordinary `/v1` API routes under spec 017's policy, so the
    console, CLI and MCP get the same lookups (constitution 7).
- **Graceful degradation.** If the channel is down, Serendip keeps
  serving: authentication, authorization and every existing route work
  as before. Only the live lookups degrade, and they say so.

### Part B: agents as principals (the POC)

- **Trusted agent issuers:**
  - An operator configures one or more agent issuers. Each has an issuer
    URL and the audience that issuer puts in tokens meant for Serendip.
    For the POC this is server configuration, like the existing identity
    issuer. Managing issuers through the API is out of scope.
  - **A token from an agent issuer is only ever an agent.** It never
    becomes a person, and a token from the human issuer never becomes an
    agent.
  - Tokens must pass the usual checks: signature (keys from the issuer's
    discovery document), issuer, exact audience and expiry.
  - Agent tokens can't be revoked upstream (ThunderID can't revoke issued
    tokens), so Serendip enforces a maximum token lifetime it will accept.
    Plan.md sets the value.
- **Agent principals:**
  - An agent's ID is its issuer plus its token `sub` (the agent's ID in
    that ThunderID). Its audit actor is `agent:<issuer-alias>/<sub>`.
  - **A sponsor is recorded in Serendip.** Spec 017 already requires a
    person sponsor on every agent assignment. Agent Manager has an
    `owner`, but research found it isn't in the token. Plan.md checks
    whether it can be read and used to pre-fill or cross-check the
    sponsor. The sponsor on record is the accountable one either way.
  - **An agent with no assignment has no access.** A valid token alone
    grants nothing. Agents don't get API keys' "Owner at `/` when never
    assigned" grandfathering.
- **Guardrails beyond roles,** because constitution 5 is stricter for AI
  than for people:
  - **No agent can hold authority over access.** An assignment to an
    agent is refused if its role grants any write on role assignments or
    role definitions. That rules out Owner, User Access Administrator,
    RBAC Administrator, and custom roles with those actions. An agent
    can never widen its own access, or anyone else's.
  - **Agents can't delete yet.** Until spec 019 (Approvals) exists,
    policy answers **needs approval** for any agent delete action, even
    if a role allows it. With no approval path, the request is refused
    with a response that says it needs approval. Spec 019 turns this
    into a real approval request, which the agent can never approve
    itself.
  - Data actions (reading Secret values) are allowed only when a role
    explicitly grants them, as for anyone.
- **Audit:** every agent request that mutates, and every denial, records
  the agent, its sponsor, and the deciding assignment or the reason for
  denial.
- **The console:** the Access control page can add an assignment for an
  agent, with a required sponsor. Agents are shown as their own type, and
  roles an agent can't hold aren't offered.

### Part C: the POC itself

- **Primary setup:** Agent Manager's own quick start (its k3d cluster,
  its OpenChoreo and ThunderID), beside a Serendip install. Register
  Serendip as a resource server in that ThunderID, create an agent, give
  it a Serendip role, and have it call the Serendip API.
- **Reuse variant (the user, 2026-10-18), on a throwaway cluster only:**
  run Agent Manager against our existing ThunderID and OpenChoreo. The
  OpenChoreo community module for Agent Manager 1.0.0 on OpenChoreo 1.3
  was tested with ThunderID 1.0.1, which matches our versions. Two
  things are recorded:
  - what works, and what Agent Manager changes in ThunderID and
    OpenChoreo settings;
  - a **performance and footprint measurement** with the spec 013
    harness, compared with the edge-1 baseline.
- **What Agent Manager's install conflicts with in edge-1** is recorded,
  not fixed. Its quick start pins OpenChoreo 1.2.0, kgateway 2.2.1 and
  Gateway API 1.4.1. We run 1.3.1, 2.4.6 and 1.6.1. It also patches
  OpenChoreo's authentication and adds OpenSearch and PostgreSQL.
- **A findings report** in the spec folder covers:
  - the real agent token's claims and lifetime;
  - whether the owner can be read;
  - each setup's footprint;
  - what Agent Manager would need to change before it could be an
    optional edge-1 capability, with a recommendation.

### Docs

- An ADR for the management channel.
- Updates to `identity.md` (agent identity, trusted issuers, the
  channel), `authorization.md` (agent guardrails) and `gateway.md` (the
  restricted identity route).

## Scope: OUT (explicitly)

- **Agent Manager inside edge-1,** or as any profile's default. The
  reasons were recorded in spec 017: version conflicts, patches to
  OpenChoreo and ThunderID, and roughly 6–8 GB more components
  (constitution 4).
- **Creating or deleting agents from Serendip.** Agents are created in
  Agent Manager. Serendip only trusts them and assigns roles.
- **Approvals.** That's spec 019. This spec only makes policy answer
  "needs approval" for agent deletes.
- **On-behalf-of delegation,** an agent acting for a person (ThunderID's
  OBO, the `act` claim). The findings report notes what exists.
- **A Serendip MCP server.** Agents call the HTTP API directly here.
- **Managing agent issuers through the API,** and multi-issuer for
  people.
- **The rest of the identity-provider automation list:**
  Serendip-managed groups, automatic console-URI updates (they come with
  HTTPS), and credential cleanup. The channel is built so each of these
  is just another use of it.
- **Agent Manager's own guardrails, AI Gateway and observability** in
  Serendip.

## Affected resources and capabilities

- **Identity capability** (`core/identity` interfaces,
  `providers/identity/...`):
  - a second kind of authenticator for agent issuers;
  - the management-channel interface, and the live group and directory
    lookups.
- **`core/policy`:** the agent guardrails (refused roles, needs approval
  on delete). `core/resource` already has the agent principal and
  sponsor.
- **`api`:** agent authentication in the middleware; the directory
  lookup and live-groups routes; problem+json for "needs approval".
- **Gateway provider:** the identity route is restricted by path.
- **Audit:** agent actor and sponsor.
- **`ui`:** agents on Access control, live groups in Check access,
  directory search in the picker.

## Acceptance criteria

1. **Management API closed publicly.** From outside the cluster, a
   request to a ThunderID management path through the Gateway is
   refused. Login, discovery, token and ThunderID's console still work,
   and console sign-in still works end to end.
2. **Live groups.** A person is added to a group in ThunderID and does
   not sign in again. Check access shows the new group, marked live, and
   any role assigned to that group. With the channel down, Check access
   shows last-sign-in groups, labelled as such, and nothing else breaks.
3. **Directory picker.** The principal picker finds a person and a group
   that have never signed in to Serendip, and an assignment to them
   works when that person signs in.
4. **Channel decision.** The ADR records the choice, the privilege it
   holds and the alternatives. If a standing credential is used, its
   custody matches the existing pattern (root-only file).
5. **Agent call.** An agent created in Agent Manager gets a token from
   its ThunderID for Serendip's audience. With Reader on a Project, it
   lists that Project's Components and gets 403 elsewhere. With no
   assignment, it gets 403 everywhere. The audit shows
   `agent:…` with its sponsor.
6. **Issuer separation.** An agent-issuer token can't be used as a
   person, and a human-issuer token can't be used as an agent. Tokens
   with the wrong audience, an unknown issuer, or a lifetime over the
   maximum are rejected with 401.
7. **Guardrails.** Assigning Owner, User Access Administrator, RBAC
   Administrator, or a custom role with assignment-write actions to an
   agent is refused at any scope. An agent with Contributor that tries a
   delete gets a "needs approval" refusal, and nothing is deleted.
8. **Console.** An Owner adds a Reader assignment for an agent with a
   sponsor. The role list doesn't offer the roles an agent can't hold.
9. **Reuse variant.** On a throwaway cluster, Agent Manager runs against
   our ThunderID and OpenChoreo, or the report records exactly why it
   doesn't. The footprint is measured with the spec 013 harness and
   compared with the edge-1 baseline.
10. **Findings report** is written, covering the items in Part C.
11. **Footprint and regressions.** Every existing test, the benchmark
    harness and a real-cluster run pass. `serendipd` stays within
    constitution 4. Agent token checks add a measured, small cost.
