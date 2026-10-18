# Spec 018 — verification record (T011)

Everything here was observed directly on 2026-10-18 unless marked
test-only.

## Setup

- **Serendip:**
  - **Cluster:** kind `serendip-t018` (kindest/node v1.34.0) with the
    spec 017 stack: storage, OpenBao, kgateway, OpenChoreo and ThunderID
    1.0.1.
  - **`serendipd`:** built from the working tree (`c57ba86`) with the
    console embedded, on `:8088`. Agent Manager holds 8080 (T002 finding
    2), so the console URIs for `:8088` were registered once with
    `serendip-identity-redirects --public-url http://localhost:8088`.
  - **Configuration:** `SERENDIP_AGENT_ISSUERS=am-dev=http://default-idp.amp.localhost:8080|https://serendip.local/api`.
- **Agent Manager:** `amp/v1.0.0`, from its official quick start, in its
  own k3d cluster `amp-local`, beside the kind cluster.
  - **The agent:** `serendip-ext`, an external agent whose AgentID is
    `01a14f26-cb2a-753e-89da-54e4993b7ad5`.
  - **Serendip as a resource server:** registered in the default
    environment's ThunderID with identifier `https://serendip.local/api`
    (T002).
  - **Tokens:** a fresh token was fetched for each run, with
    `client_credentials` and `resource=https://serendip.local/api`.
- **Name resolution:** the user added `127.0.0.1 default-idp.amp.localhost`
  to the Windows hosts file. Go on Windows doesn't resolve `*.localhost`
  subdomains (T002 finding 8). With the entry, `serendipd` fetched the
  agent issuer's discovery document and JWKS itself. It logged "agent
  issuer am-dev trusted: … (agent tokens only)" at startup.
- **Data:** an Organization, then Projects A and B, each with an
  Environment, an Application and a Component.
- **In ThunderID, through the management API over the tunnel tool:**
  - a group `t011-live` with the admin in it;
  - a person `t011-newbie` who has never signed in.
- **Drivers (scratch, session-local):**
  - a stdlib Python driver for the API checks, with the API key (Owner at
    `/`) and the real agent token;
  - a headless-Chrome driver over the DevTools protocol for the console.
    It signs in through the real ThunderID login page.
  - Passwords were read into the environment, and no token, secret or
    password was printed.

## Acceptance criteria

| AC | Result |
|---|---|
| 1 Management API closed publicly | See the AC 1 details below. **PASS.** |
| 2 Live groups | See the AC 2 details below. **PASS** (the console's fallback text is test-only). |
| 3 Directory picker | See the AC 3 details below. **PASS.** |
| 4 Channel decision | The ADR is written in T014. The privilege held and the custody of the credential are in plan.md decision 3. **Pending T014.** |
| 5 Agent call | See the AC 5 details below. **PASS.** |
| 6 Issuer separation | See the AC 6 details below. **PASS** (four cases are test-only). |
| 7 Guardrails | See the AC 7 details below. **PASS.** |
| 8 Console | See the AC 8 details below. **PASS.** |
| 9 Reuse variant | T012. |
| 10 Findings report | T014. |
| 11 Footprint and regressions | See the AC 11 details below. **PASS.** |

### AC 1: management API closed publicly

- **Through the Gateway:** `/oauth2/introspect`, `/oauth2/dcr/register`
  and `/oauth2/par` return 404, and so do `/users`, `/groups` and
  `/console/`.
- **Still served:** `/oauth2/jwks` and `/.well-known/openid-configuration`
  return 200.
- **Real browser:** signing in to the console as `admin`, then as
  `t011-newbie`, through ThunderID's login page worked. So did signing
  out (T003 and T011 runs).

### AC 2: live groups

- **Through the API:** `t011-live` was created and the admin added to it,
  with no new sign-in. `GET …/principals/person/<admin>/groups` then
  returned `{"source":"live","groups":["Administrators","t011-live","t018-readers"]}`,
  while the sign-in record still said `["Administrators","t018-readers"]`.
- **In the browser:** Check access for `admin` said "Live from the
  identity provider: admin is in Administrators, t011-live,
  t018-readers."
- **With the channel down:** ThunderID was scaled to 0.
  - Groups and directory answered with `"source":"signedIn"`.
  - API-key requests and agent requests kept working; agent requests have
    a separate issuer.
  - The public identity route returned 503.
  - After scaling back to 1, answers were `"source":"live"` again with no
    restart.
- **Test-only:** the console's "can't be asked" wording
  (`access.test.tsx`).

### AC 3: directory picker

- **Found before any sign-in:** directory search for `newbie` returned
  `t011-newbie` (`source: live`).
- **In the console:** the Person picker suggested `t011-newbie
  (01a1501a-…)` after typing "newb".
- **Assignment by directory ID:** Reader on Project A was assigned to
  that ID. On the first sign-in, the person's console requests got
  `GET /v1/projects/<A>` 200, `GET /v1/projects/<B>` 403, and
  `permissions?scope=Project/<A>` 200.

### AC 5: agent call

The real agent token was used throughout.
- **No assignment:**
  - A's Component: 403;
  - A's list: empty.
- **Reader on Project A,** sponsored by `admin`:
  - A's Component: 200;
  - A's list: exactly A's Component;
  - B's Component: 403;
  - B's list: empty;
  - creating something: 403, and the refusal is audited.
- **Audit:** every row for the agent has actor
  `agent:am-dev/01a14f26-…`, `actor_subject` `am-dev/01a14f26-…`, and
  `actor_sponsor` the admin's subject. That holds for the refused create
  (`deny: …`), the allowed create (`allow: Contributor …`), both deletes
  (`needsApproval: …`) and the refused role assignment.

### AC 6: issuer separation

- **Observed live:**
  - an agent-issuer token without `resource`, whose `aud` is the client
    ID, is refused with 401, logged as "audience";
  - a garbage token is refused with 401;
  - a person signing in through the human issuer is `person:admin`, never
    an agent.
- **Test-only, in `providers/identity/oidc/agent_test.go`:**
  - a person's login token from the agent issuer;
  - the human issuer's token with an agent issuer's `iss`;
  - an unknown issuer;
  - a lifetime over the maximum, and a missing `iat`.

  These need tokens neither identity provider will issue on request.
  A real Agent Manager token also passed the real router in T007.

### AC 7: guardrails

- **Refused for the agent with 400 "agent guardrail":**
  - Owner, User Access Administrator and RBAC Administrator;
  - a second sponsor for the same agent.
- **Contributor on Project A,** for the agent:
  - creating an Application: 201;
  - deleting a Component: 403 `urn:serendip:problem:needs-approval`
    "approval required", and the Component is still there;
  - a cascade delete of an Application: the same;
  - assigning a role: 403 "an agent can't change role assignments or
    role definitions".

### AC 8: console

Signed in as the admin, at Access control for Project A:
- **The role list for an agent** offered only Contributor, Reader,
  Secrets Officer and Secrets User.
- **The request shown** on the page was exactly what was sent:
  - principal type `agent`, ID `am-dev/01a14f27-…`;
  - `sponsor` the admin's subject, resolved from the name "admin";
  - the role Reader.
- **The result:** "Reader assigned to am-dev/01a14f27-…", and the table
  lists the agent as type Agent with "Sponsor: admin".
- **Check access for the first agent** showed its Reader and Contributor
  roles and "Agents have no groups…".
- **Screenshots** were saved in the session scratchpad.

### AC 11: footprint and regressions

- **Tests:** `go test -race ./...`, 75 UI tests, typecheck and the build
  all pass.
- **Benchmarks:** T010 (`profiles/edge-1/bench/results/2026-10-18-spec018-agents/`).
- **This run** completed with no restarts of `serendipd`.

## Found during T011

Nothing in the code. Two problems in my own driver scripts were fixed
there:
- a label lookup that matched "Principal type" for "Principal";
- a token request missing the `amp:agent:*` scopes.
