# Plan 018: Agent identities (WSO2 Agent Manager POC) and the identity-provider management channel

Status: Approved 2026-10-18. Spec: `spec.md` (approved 2026-10-18).

## Approach in one paragraph

First we close what the public identity route exposes beyond login,
because `/oauth2` is routed as a prefix today. Then we build one
management channel to ThunderID. It's an in-process port-forward through
the Kubernetes API, which keeps the `Authorization` header, plus
`serendipd`'s own administrator login, which already exists in the code.
That channel sits behind a new `identity.Directory` interface in `core/`.
The directory feeds two API routes: directory search and a person's live
groups. The console's picker and Check access use those routes. For agents,
Agent Manager's ThunderID becomes a configured **agent issuer**: tokens
are routed to a verifier by their `iss`, and anything from an agent issuer
becomes an `agent` principal. `core/policy` gains three agent guardrails:
agents can't hold access-management roles, they get *needs approval* on
delete, and they have one sponsor each. The POC then runs Agent Manager's
quick start beside a kind Serendip, and the reuse variant runs on a
throwaway cluster. The findings report records the token's real claims and
the footprint.

## Decisions

1. **The public identity route becomes an exact-path allowlist.**
   - **Why:** today `route.go` routes `/oauth2` as a prefix. That prefix
     includes `/oauth2/introspect` (client-authenticated but sensitive)
     and `/oauth2/dcr/register` (needs a `system` token, but it's
     management). It also includes `/oauth2/auth/callback`, which has no
     documented client authentication. The research describes it as
     called by the Gate, ThunderID's login page.
   - **What's routed after this:**
     - **Exact paths:** `/oauth2/authorize`, `/oauth2/token`,
       `/oauth2/jwks`, `/oauth2/userinfo`, `/oauth2/revoke`, the
       end-session path, `/flow/execute`, `/flow/meta` and
       `/design/resolve`.
     - **Prefixes:** `/.well-known` and `/gate`. `/gate` is the login
       page's own assets.
   - **Settled by T001** ("Spike findings"):
     - `/oauth2/auth/callback`: the browser POSTs to it during login, so
       it stays routed as an exact path. The risk is recorded.
     - `/oauth2/logout` and `/oauth2/logout/callback` are routed: the
       browser uses them to log out.
     - `/oauth2/par` isn't used, so it isn't routed.
     - Neither are `/oauth2/introspect`, `/oauth2/dcr/register` or
       `/oauth2/bc-authorize`.
   - **Spec wording:** the spec listed "ThunderID's own console" among the
     routed paths. It stays **unrouted**, as it is today. It's an admin
     client, so exposing it would add surface without helping a Serendip
     user. Operators reach it through a port-forward.
   - **Gateway API:** this uses `type: Exact` matches. An existing install
     is fixed by the next foundations pass: server-side apply replaces the
     rule list.
2. **The channel's transport is an in-process port-forward over the
   Kubernetes API.** It uses the kubelet's WebSocket mode, subprotocol
   `v4.channel.k8s.io`.
   - **How it works:** one WebSocket per TCP connection, a 1-byte channel
     prefix, and a 2-byte port preamble on each channel, which is
     discarded.
   - **How it's built:** a small stdlib WebSocket client in
     `providers/kubernetes`. That's client side only: masked frames,
     binary frames, fragmentation, ping/pong, and close.
   - **How it's exposed:** a `DialContext` that plugs into an
     `http.Transport`. It picks a ready ThunderID pod from the Service's
     endpoints.
   - **No new dependency,** so no dependency ADR. The channel itself gets
     ADR 0015.
   - **Rejected:**
     - **the Service proxy:** it strips `Authorization`;
     - **the public Gateway route:** it contradicts decision 1;
     - **the ClusterIP from the host:** it fails when `serendipd` isn't
       a cluster node. That covers today's kind development setup, and
       any future off-node install;
     - **an in-cluster helper:** a new deployed component and image to
       keep up (ADR 0012's supply-chain bar), for a header rename;
     - **KEP-4006's SPDY-over-WebSocket tunnel:** it needs a SPDY/3.1
       implementation, which is too heavy.
   - **Versions:** our agent pins **K3s v1.36.4+k3s1**
     (`agent/bootstrap/install.go`), and kind verification uses 1.34.
     WebSocket port-forward has been beta and on by default since 1.31,
     so both support it. The legacy mode needs no feature gate at all
     (inferred). T001 still confirms it on **kind 1.34 and k3d
     `rancher/k3s:v1.36.4-k3s1`**, because the research couldn't read the
     API server's upgrade-proxy source.
3. **The channel's credential is `serendipd`'s own administrator login.**
   - **How it works:**
     - It uses the existing `managementToken` flow: the CONSOLE client,
       `openid system`, and the PKCE code flow.
     - The password comes from the admin Secret that `serendipd` itself
       creates. It's read from the Kubernetes API at each login and never
       written to disk or logged.
     - The token is cached in memory until 60 s before it expires
       (3600 s on 1.0.1). After that, `serendipd` logs in again rather
       than refreshing. ThunderID shrinks a rotated refresh token's
       lifetime with each rotation (issue #5281), so re-login is simpler
       and no worse.
   - **What it's used for in this spec:** reads only (`GET /users`,
     `/groups`, `/users/{id}/groups`). Who may *see* those answers is
     decided by Serendip's RBAC, as an app with directory-read permission
     works in Azure.
   - **Why not act as the signed-in person (Azure's portal pattern)?**
     - `serendipd` already deploys ThunderID and holds the admin Secret,
       so delegation wouldn't take away any privilege it has.
     - It would only work while a ThunderID administrator happens to be
       signed in to the console.
     - It would never work for API keys or the CLI, which breaks
       constitution 7.
   - **Why not a narrower machine identity?** 1.0.1 has only the coarse
     `system` permission, so any machine identity would be full admin
     anyway.
   - **Custody (AC 4):** the credential stays in its existing Secret. It's
     reached with the kubeconfig `serendipd` already uses, which is a
     root-only file. There's no second copy.
   - **Later identity-provider automation** reuses this channel:
     automatic console URIs (the `UpdateClient` code), managed groups,
     and cleanup.
4. **`core/identity.Directory`** is the capability-level interface.
   - It has three methods: `People(ctx, query)`, `Groups(ctx, query)` and
     `GroupsOf(ctx, subject)`.
   - It returns `reconcile.ErrProviderNotReady` when the provider can't
     be reached.
   - It's product-neutral. ThunderID is only in `providers/identity/thunderid`.
   - **Search:** ThunderID 1.0.1's `filter` supports `eq` only. So
     `People` and `Groups` page through at most 1,000 entries (10 pages
     of 100) and match a case-insensitive substring on username, display
     name or group name. They return at most 50.
   - **The limit is stated in the API response** (`truncated: true`), not
     hidden. A person's ID is ThunderID's user ID, which is the token
     `sub` (spec 017 T001).
5. **Two new routes,** both under spec 017's policy and both in the route
   table, so the fail-closed test covers them:
   - **`GET /v1/authorization/directory?type=person|group&q=`** searches
     the directory. It's gated like `/principals`: the caller must be able
     to write role assignments somewhere.
     - Response: `{source: "live" | "signedIn", truncated, items: [...]}`.
     - If the provider is down, it falls back to the known-principals
       registry, filtered, with `source: "signedIn"`.
   - **`GET /v1/authorization/principals/person/{id}/groups`** returns a
     person's current groups, for Check access, with the same gate and
     the same `source` fallback. It doesn't update the registry: that
     stays "as of last sign-in", by definition.
6. **Agent issuers are configured in `SERENDIP_AGENT_ISSUERS`.**
   - **Format:** comma-separated `alias=issuer|audience[|maxLifetime]`.
     For example: `am-dev=http://localhost:8095|https://serendip.local/api|1h`.
   - **Aliases** are `[a-z0-9-]{1,32}` and must be unique.
   - **An issuer can't be the human issuer.** A malformed entry stops
     startup; unlike API keys, it isn't skipped. A trust root that's
     wrong must be visible.
   - **`maxLifetime`** defaults to 1 h, which is ThunderID's default token
     lifetime, and is capped at 24 h. A token is accepted only if
     `exp − iat` is at most the maximum (plus leeway). A token without
     `iat` is rejected; ThunderID always sets it.
7. **Tokens are routed by issuer.**
   - A new `oidc.IssuerRouter` (an `identity.Authenticator`) reads the
     unverified `iss` from the token's payload. It picks the verifier for
     exactly that issuer and then runs full verification. No issuer is
     ever tried as a fallback for another.
   - **`identity.Principal` gains `Kind`** (`KindPerson` or `KindAgent`)
     and `Issuer`, which is the alias, empty for the human issuer.
   - **Agent tokens' groups are ignored.** Group names are per issuer, so
     an agent issuer's `Administrators` must never match ours.
   - **The middleware** maps an agent to `policy.Principal{Type: agent,
     ID: "<alias>/<sub>"}`. Its audit actor is `agent:<alias>/<sub>`, and
     it's recorded in the known-principals registry as type `agent`.
   - **Static API keys** may not use the `agent:` prefix, the same rule
     as for `person:`.
8. **One sponsor per agent, recorded in audit.**
   - **`CheckAssign`:** every assignment for an agent must name the same
     sponsor. A different sponsor is refused while other assignments
     exist.
   - **Sponsor lookup:** `policy.Engine.SponsorOf(ctx, agentID)` returns
     it, and the middleware puts it in context.
   - **Audit:** a new `actor_sponsor` column in `audit_log`, through an
     additive migration.
   - **Agent Manager's `owner`** is in that ThunderID, not ours. Our
     channel doesn't reach it, so it isn't cross-checked in code. The
     findings report records whether it can be read, for later.
9. **Agent guardrails live in `core/policy`.**
   - **At assignment:** an assignment to an agent is refused (400
     validation) if any of the role's non-data permission blocks grants
     `serendip/authorization/roleAssignments/write`, `.../delete`,
     `serendip/authorization/roleDefinitions/write` or `.../delete`. That
     uses the same `grants` matcher, so `*` is covered. Custom roles can't
     be updated (spec 017), so an assignment can't become forbidden
     later.
   - **At evaluation**, as a second line of defence:
     - for an agent, those four actions are always **Deny**;
     - any `…/delete` action that would be allowed returns
       **NeedsApproval**, with the evidence.
   - **In the API**, `NeedsApproval` means 403 problem+json with
     `type: …/needs-approval`, title "approval required", and a detail
     that says approvals arrive with spec 019. It's audited as a refused
     mutation with `policy_decision` `needsApproval: …`.
   - **For people and API keys** nothing changes: they never get
     NeedsApproval in this spec.
10. **The role definition view gains a computed `assignableToAgents`.**
    - It's computed by the same rule as decision 9, so the console filters
      roles from the server's answer and never re-implements the rule.
    - It's an additive response field.
11. **The console:**
    - **Access control → Add assignment** offers type `agent`, with a
      required sponsor picked from people. It lists only roles where
      `assignableToAgents` is true.
    - **The principal picker** uses `/directory`.
    - **Check access** calls `/groups` and labels the result "live" or
      "as of last sign-in".
    - **Agents** show with their issuer alias.
12. **The POC environment** has two clusters on this machine.
    - **Primary:**
      - **Agent Manager:** the official `amp/v1.0.0` quick start in its
        own k3d cluster (`amp-local`).
      - **Serendip:** on kind `serendip-t018` with the spec 017 stack.
      - **How Serendip trusts it:** `serendipd` uses Agent Manager's
        per-environment ThunderID issuer. Its JWKS URL comes from
        discovery, or from `JWKSURL` if the issuer's hostname doesn't
        resolve from the host. In that ThunderID we register a resource
        server whose identifier is Serendip's audience, give it a
        permission, add that permission to a role, and assign the role to
        the agent.
      - **Memory:** Agent Manager asks for 8 GB for Docker. Docker Desktop
        runs inside WSL2, so `.wslconfig` has to go from 8 GB to about
        14 GB for both clusters, and that needs the host's RAM. I'll check
        it before T002 and ask you if it's short.
    - **Reuse variant:** a throwaway kind cluster running our stack
      (ThunderID 1.0.1 and OpenChoreo 1.3.1). We apply the
      `openchoreo/community-modules` PR #679 module (Agent Manager 1.0.0
      on OpenChoreo 1.3).
      - **Before and after,** we diff ThunderID's applications, resource
        servers and settings, and OpenChoreo's auth config and
        ClusterAuthzRoleBindings.
      - **We measure** with `bench-edge1` and compare against the spec 017
        baseline (`profiles/edge-1/bench/results/2026-10-18-spec017-authz`).
      - **It's never** applied to edge-1 or a persistent cluster.
13. **The findings** go in `specs/018-agent-manager-poc/findings.md`:
    - the token's claims, lifetime and owner;
    - both footprints;
    - what Agent Manager writes into ThunderID and OpenChoreo;
    - the version conflicts;
    - a recommendation on making it an optional edge-1 capability.

## Upstream facts (research 2026-10-18; to verify in T001 and T002)

**Agent Manager 1.0.0** (`wso2/agent-manager`, Apache-2.0, GA 2026-09-07)
- It creates each agent in ThunderID with `POST /agents`, using
  `client_credentials` and `client_secret_basic`.
- **Token requests** pass `resource=<identifier>` (RFC 8707).
- **The token** carries `iss`, `sub` (the agent's entity ID, inferred),
  `aud` (the identifier) and `scope`. There's no agent-type claim, and the
  owner isn't in it (inferred). The lifetime defaults to 3600 s (inferred
  from ThunderID's defaults). `iat` is always set.
- **Revocation:** ThunderID can't revoke issued tokens.
- **The second JWT:** Agent Manager also issues its own JWT
  (`/agents/{a}/token`). That's a different issuer, and we don't trust it.
- **Pinned versions:** OpenChoreo 1.2.0, kgateway 2.2.1, Gateway API
  1.4.1. It patches OpenChoreo's auth configuration (`sub` → `client_id`).

**ThunderID 1.0.1**
- **Management paths** are bare: `/users`, `/groups`, `/agents`,
  `/applications`, `/resource-servers`, `/flows`, `/design/themes`,
  `/design/layouts` and others. They need the `system` scope.
- **Directory endpoints:**
  - `GET /users?limit≤100&offset&filter=<attr> eq "<v>"`;
  - `GET /users/{id}/groups` (direct groups);
  - `GET /groups`;
  - `GET /groups/{id}/members` (members can be users, groups, apps or
    agents).
- **Resource servers:** `POST /resource-servers {name, ouId, identifier,
  type}`. The `identifier` becomes `aud`.
- **Lifetimes:** the CONSOLE client's access token lasts 3600 s. The global
  refresh token lasts 86,400 s and is rotated.

**Kubernetes port-forward**
- **The kubelet's legacy WebSocket mode:** `v4.channel.k8s.io`. Port index
  i uses data channel 2i and error channel 2i+1, with a uint16 LE port
  preamble.
- **`PortForwardWebsockets` (the SPDY-tunnelling mode):** alpha in 1.30,
  beta in 1.31, GA in 1.38.
- **K3s:** the stable line is v1.36.5. Our pin is v1.36.4+k3s1.

## Spike findings (T001, 2026-10-18)

Run on kind `serendip-t018` (kindest/node v1.34.0, the spec 017 stack
from a fresh `serendipd`), and on a throwaway K3s `v1.36.4+k3s1`
container. Scratch programs: a stdlib Go port-forward client, and a
headless-Chrome driver over the DevTools protocol. Neither is kept.

1. **The browser login and logout through the Gateway** use these
   ThunderID paths:
   - `GET /oauth2/authorize`;
   - `/gate/*` (the page and its assets);
   - `GET /flow/meta`;
   - `POST /flow/execute`;
   - `POST /oauth2/auth/callback`;
   - `GET /oauth2/logout`;
   - `GET /gate/signout`;
   - `POST /oauth2/logout/callback`.

   `/v1/session` was 200 after login, and logout ended back at the
   console. `serendipd`'s own server-side calls (token, revoke, keys) go
   through the Service proxy, not the Gateway. The public routes for
   token, jwks, userinfo, revoke and discovery are kept for standard
   OIDC clients. Decision 1 is amended.
2. **What the Gateway passes today:**
   - **Management paths are already not routed:** `/users`,
     `/applications`, `/flows`, `/design/themes` and `/console/` are all
     404.
   - **But the `/oauth2` prefix reaches ThunderID** for `introspect`
     (400, missing `client_id`), `dcr/register` (401), `par` (400) and
     `auth/callback` (400, missing `authId`). They're protected by
     ThunderID's own checks, but there's no reason to expose the first
     three.
   - **Correction to the earlier research:** the management API was
     *not* publicly reachable. Spec 016's route already excluded it. The
     real gap was the `/oauth2` prefix.
3. **The port-forward transport works** on both clusters.
   - **What was used:** the kubelet WebSocket mode, subprotocol
     `v4.channel.k8s.io`. The server echoed it, `Sec-WebSocket-Accept`
     verified, and each channel sent a 2-byte LE port preamble (8090).
   - **On kind 1.34**, over the tunnel:
     - the full admin login worked (about 300 ms, 6 connections);
     - `GET /users` was 200.
     - **Control:** the same token through the Service proxy got **401**
       (`AUTH-4010`). That confirms the header is stripped there and
       kept in the tunnel.
   - **On K3s v1.36.4**, a header-echo pod received `Authorization:
     Bearer …` byte for byte.
   - **Failures look like this:**
     - A closed port gives a message on the error channel, then a close
       frame. The message names the pod UID and the netns path.
     - A missing pod fails the upgrade with 404 and a Status body.
     - Both are logged and mapped to `ErrProviderNotReady`. Neither is
       ever returned to an API client.
   - **RBAC:** the dialer needs `create` on `pods/portforward` in
     `serendip-identity`, plus a way to find a ready pod.
4. **The management API** (ThunderID 1.0.1, observed):
   - **The token:** `expires_in` 3600, a refresh token is issued, scope
     `openid system`, `aud` `https://localhost:8090/mcp`. `sub` is the
     user ID (`…0030`), and `groups` is as of issue.
   - **`GET /users?limit&filter=username eq "admin"`:**
     `{totalResults, startIndex, count, users: [{id, ouId, type,
     attributes: {username, name, given_name, family_name, email, sub,
     …}, isReadOnly}], links}`. Note that `attributes.sub` is the
     username (`admin`), **not** the token `sub`. The directory must use
     `id`.
   - **`GET /groups?limit`:** `{…, groups: [{id, name, description, ouId,
     isReadOnly}]}`.
   - **`GET /users/{id}/groups`:** `{…, groups: [{id, name, ouId}]}`. A
     group created a moment earlier (`POST /groups`, 201) appeared
     straight away, with no new sign-in. That's AC 2's live behaviour.

## Interfaces and types created

- **`core/identity`:**
  - `Kind` (`KindPerson`, `KindAgent`);
  - `Principal.Kind` and `Principal.Issuer`;
  - `AgentActorPrefix = "agent:"`, with `Actor` extended to agents;
  - `Directory` (`People`, `Groups`, `GroupsOf`), and the
    `DirectoryEntry{ID, Name}` and `DirectoryResult{Items, Truncated}`
    types.
- **`core/policy`:**
  - `AgentMayHold(role) bool`;
  - agent rules in `Decide` and `CheckAssign`;
  - `SponsorOf`.
- **`core/audit`:** `WithSponsor`, `SponsorFrom`.
- **`providers/kubernetes`:**
  - `PortForwardDialer` (a `DialContext` for a pod port), on top of a
    minimal `wsConn`;
  - `ReadyPod(ctx, ns, service)`.
- **`providers/identity/oidc`:**
  - `Config.Kind`, `Config.MaxLifetime` and `Config.IgnoreGroups`;
  - `IssuerRouter`.
- **`providers/identity/thunderid`:** `Directory`, which holds an
  `http.Client` over the dialer plus a cached admin token.
- **`api`:** the directory and live-groups handlers, the `needs-approval`
  problem type, and `assignableToAgents` on the role view.

## Files touched

- **Identity, core side:** `core/identity/identity.go`, plus tests.
- **Policy:** `core/policy/engine.go` and `builtins.go` (or a new
  `agents.go`), plus tests.
- **Audit:** `core/audit/*.go`, and `core/store/sqlite/store.go`
  (migration and insert).
- **Kubernetes:** `providers/kubernetes/portforward.go` and
  `websocket.go` (new), plus tests.
- **OIDC:** `providers/identity/oidc/verifier.go` and `router.go` (new),
  plus tests.
- **ThunderID:**
  - `providers/identity/thunderid/route.go`;
  - `directory.go` (new);
  - `clients.go`, which shares `managementToken`;
  - `VERSIONS.md`.
- **Wiring:** `cmd/serendipd/main.go` and `wiring.go` (agent issuers, the
  router and the directory).
- **API:** `api/middleware.go`, `authz.go`, `authorization_handler.go`
  and `problem.go`, plus the route tests.
- **Console:** `ui/src/screens/Access.tsx`, plus tests.
- **Docs:**
  - `docs/decisions/0015-identity-provider-management-channel.md`;
  - `docs/architecture/identity.md`, `authorization.md` and `gateway.md`;
  - `api/CLAUDE.md`;
  - `specs/018-agent-manager-poc/findings.md` and `verification.md`;
  - `docs/PROGRESS.md`.

## Risks

- **The legacy WebSocket port-forward may misbehave through the API
  server's upgrade proxy.** T001 checks this first on K3s 1.36.4 and kind
  1.34. The fallback is the SPDY-tunnelled mode: heavier, but on by
  default on both. If that happens, plan.md is amended before T004.
- **The WebSocket client is new security-relevant code.** It carries
  admin tokens.
  - **TLS:** it uses the kubeconfig's TLS configuration as-is.
  - **Handshake checks:** it verifies `Sec-WebSocket-Accept` and the
    echoed subprotocol.
  - **Limits:** frame sizes are bounded.
  - **Review:** go-reviewer and `/security-review` in T013.
- **`/oauth2/auth/callback` stays publicly routed,** because the browser
  needs it (T001). Accepted: it accepts only a flow assertion and
  `authId` issued by ThunderID itself (an empty body got 400, "authId is
  required").
- **A standing admin token sits in `serendipd`'s memory** for up to an
  hour. Accepted: `serendipd` can already read the admin password, so a
  token adds nothing beyond what it already holds. It's never logged or
  returned.
- **Directory search is bounded** to 1,000 entries per type. That's
  stated as `truncated`, and fine for edge-1 directories.
- **Agent tokens can't be revoked.** That's why the maximum lifetime
  exists. Removing an agent's assignments takes effect at once, since
  assignments are evaluated per request.
- **Agent Manager plus our kind cluster may not fit in this machine's
  memory.** It's checked before T002, and the user is asked if it's short.
- **Agent Manager's token claims are undocumented.** T002 observes them
  before T007 hard-codes anything. If `sub` isn't the agent ID, decision 7
  is amended.
- **Agent Manager is six weeks past GA,** and PR #679 is unmerged. The
  reuse variant can fail; AC 9 allows recording why.

## Explicitly NOT done

- Agent Manager in edge-1 or any profile; creating or deleting agents from
  Serendip.
- Approvals. Only the `NeedsApproval` answer and its 403 exist.
- On-behalf-of delegation and `act` claims.
- A Serendip MCP server.
- Managing agent issuers through the API.
- Live use of Agent Manager's `owner`.
- Using the channel for anything but reads: automatic console URIs,
  managed groups and credential cleanup come later.
- Routing ThunderID's admin console publicly.
- Groups for agents.
