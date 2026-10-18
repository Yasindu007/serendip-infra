# Spec 018 — findings: WSO2 Agent Manager beside Serendip

What the POC learned, for deciding what to do with Agent Manager next.
Evidence: plan.md "Spike findings" (T001, T002), `verification.md`
(T011), `profiles/edge-1/bench/results/2026-10-18-spec018-agents/`
(T010) and `profiles/edge-1/bench/results/2026-10-19-spec018-reuse/`
(T012). Agent Manager is `amp/v1.0.0` (GA 2026-09-07); ThunderID is
1.0.1 on both sides.

## Recommendation

1. **Keep Agent Manager beside Serendip, as built in this spec.**
   - Serendip trusts one Agent Manager environment's ThunderID as an
     agent issuer (`SERENDIP_AGENT_ISSUERS`) and maps its agents to role
     assignments.
   - This works today and needs nothing from edge-1.
   - It held up end to end on a real install (T011): real tokens, the
     guardrails, sponsors and audit.
2. **Don't make Agent Manager an optional edge-1 capability now.** On
   our stack its supported install needs a different OpenChoreo
   configuration and four times our whole footprint. See the reuse
   variant below.
3. **Look again when either of these happens:**
   - `openchoreo/community-modules` PR #679 (or its successor) is merged,
     and OpenChoreo can run its workflow and observability planes in a
     larger Serendip profile;
   - Agent Manager gains a light install that skips builds, traces and
     evaluation and needs only agent identities.

   Either would make a "medium" or multi-node optional pack worth a spec.

## The agent token (T002, T011)

- **How a token is requested:** `client_credentials` with
  `client_secret_basic` and `resource=<Serendip's identifier>` (RFC 8707),
  from the environment's ThunderID (`http://default-idp.amp.localhost:8080`
  in the quick start).
- **The claims:**
  - `iss` is that ThunderID.
  - **`sub` is exactly the agent's ID in it.**
  - `aud` is the registered identifier.
  - `client_id`, `jti`, `nbf` and `grant_type: client_credentials` are
    always present.
  - `iat` and `exp` give a 3600 s lifetime, matching the agent's
    `assertion.validityPeriod`.
  - `scope` appears only when requested and held.
  - **There's no groups, agent-type, owner or sponsor claim.**
- **Any agent in that ThunderID gets a Serendip-audience token,** even one
  with no role, and a scope it doesn't hold is silently dropped. So `aud`
  and `scope` grant nothing. In Serendip only role assignments do, and an
  agent without one gets nothing. This was confirmed live.
- **Tokens can't be revoked** (ThunderID). Serendip caps an agent token's
  lifetime at 1 h by default, configurable up to 24 h. Removing an
  assignment takes effect on the next request.
- **The agent's `owner` in ThunderID is `amp-system-client`,** the client
  Agent Manager provisions with, not a person. Agent Manager's own
  `requestedBy` names the API client that asked. **So the upstream owner
  can't be the sponsor.** Serendip records the sponsor on the agent's
  role assignments (one per agent) and in every audit row.
- **Agent Manager also signs JWTs of its own** (`/agents/{a}/token`, for
  telemetry). Serendip doesn't trust that issuer.

## Footprint (T002, T010, T012)

| What | Memory at idle | Where measured |
|---|---|---|
| **Serendip edge-1 stack**: K3s-equivalent node, OpenBao, cert-manager, OpenChoreo control and data planes, kgateway, ThunderID | **1.32 GiB** node total, of which Serendip-deployed about 210 MiB | kind, simulated T470 (2 CPUs), T012 |
| `serendipd` | 31.5 MiB mean, 35.3 MiB max RSS; 0.05% CPU | same run (rule 4: PASS) |
| **Agent Manager quick start**: its k3d node with OpenChoreo's four planes, OpenSearch, Prometheus, OpenBao, External Secrets, cert-manager, kgateway, Gateway Operator, Agent Sandbox, Agent Manager and PostgreSQL, two ThunderIDs | **5.13 GiB** server node | k3d, no CPU limit, T002 |
| What spec 018 added to `serendipd` | +0.2 MiB idle; an agent request costs no more than a person's (+56 µs p99 on the simulated T470) | T010 |

- **T012 compared against spec 017's baseline:** every group PASS.
- **Agent Manager's own guidance** asks for 8 GB RAM and 4 CPUs for
  Docker. Production needs three 4-CPU, 8 GB nodes. Its quick start
  alone uses most of this machine's 7.76 GiB Docker VM.

## The reuse variant (T012): Agent Manager on our ThunderID and OpenChoreo

- **What was tried:** the module for this is
  `openchoreo/community-modules` PR #679 ("support Agent Manager v1.0.0
  on OpenChoreo v1.3"). It's open, last pushed 2026-10-14, and tested
  only on single-node k3d. Its own read-only `scripts/preflight.sh`, at
  commit `8dab725`, ran against a fresh edge-1 stack: kind
  `serendip-t012`, OpenChoreo 1.3.1, ThunderID 1.0.1.
- **The result is recorded, not fixed,** as the spec says. The script
  makes no changes; both it and `prepare-charts.sh` were read before
  running. It found **7 hard failures** (`preflight-agent-manager-module.txt`):

| Preflight check | Our stack | What would have to change |
|---|---|---|
| OpenChoreo workflow plane | not installed | Install it, with Argo Workflows and a push-capable registry |
| OpenChoreo observability plane | not installed | Install it, with OpenSearch |
| OpenSearch logs and tracing modules | not installed | Install both |
| ThunderID helm release `thunder` | ours has another name, in `serendip-identity` | Rename or patch the check |
| Default ClusterSecretStore (Vault KV v2) | missing | External Secrets wired to our OpenBao |
| ClusterDataPlane external gateway on kgateway | ours isn't registered there | Register `serendip-external` on the ClusterDataPlane |

- **The module also needs, beyond the preflight** (README and IDENTITY.md):
  - **OpenChoreo's API auth on** (`security.enabled`) against our
    ThunderID. Ours is off, so the OpenChoreo API is cluster-internal and
    unauthenticated.
  - **OpenChoreo's `openchoreo-system-app` client** in our ThunderID.
  - **A second ThunderID per environment,** with an HTTPS JWKS URL for the
    platform issuer.
- **What it writes into ThunderID** (it previews, then writes with
  `--apply`):
  - 8 applications;
  - 3 resource servers;
  - 4 roles, plus one granting `system`;
  - an administrators group;
  - the `engineer` user type;
  - the default resource server (`urn:wso2:amp`), set if none exists;
  - a CORS origin.
- **What it writes into OpenChoreo:**
  - `features.secretManagement.enabled`, through a control-plane
    upgrade;
  - three ClusterAuthzRoleBindings;
  - build workflows, component types and traits;
  - the `default` namespace label.
- **Agent Manager's own quick start goes further** (T002): it patches
  CoreDNS and Fluent Bit, and changes OpenChoreo's service-account `sub`
  to `client_id`. Those changes must be reapplied after Helm upgrades.

**Conclusion:** reusing our ThunderID and OpenChoreo means turning
edge-1's OpenChoreo into Agent Manager's supported configuration. Memory
alone doesn't change that; the user offered 16 GB. It's the "make it a
default inside edge-1" step spec 017 and spec 018 ruled out for now.

## Version conflicts with edge-1

| | Agent Manager quick start pins | edge-1 runs |
|---|---|---|
| OpenChoreo | 1.2.0 (PR #679: 1.3.x) | 1.3.1 |
| kgateway | v2.2.1 | v2.4.6 |
| Gateway API CRDs | v1.4.1 | v1.6.1 (upgrade-only) |
| K3s | v1.32.9 (k3d) | v1.36.4 |
| cert-manager | v1.19.2 | edge-1's pin |

## Security notes

- **Agent Manager's environment ThunderID publicly exposes**
  `/oauth2/introspect`, `/oauth2/dcr/register` and `/oauth2/par`. That's
  the prefix routing T003 removed from ours.
- **Its installer mounts the Docker socket** and `chmod 666`s it when it
  can't connect. It also prints admin passwords in the install log.
- **The chart-default `amp-api-client` secret** (`amp-api-client-secret`)
  gives full Agent Manager API access in the quick start. Any real
  install must change it.
- **On Serendip's side:**
  - agents can never manage access or delete without an approval;
  - each has exactly one sponsor;
  - their group claims are ignored;
  - a token from an agent issuer is never a person, and a human-issuer
    `client_credentials` token is never a person either.

## Open items for later specs

- **Approvals** (spec 019): an agent's "needs approval" becomes a real
  approval request the agent can't approve itself.
- **On-behalf-of delegation:** ThunderID's OBO, and the `act` claim, for
  an agent acting for a person. Agent Manager uses only
  `client_credentials` today.
- **An issuer without `grant_type`:** an agent issuer that doesn't emit
  the claim can't be trusted until a per-issuer exception is designed.
- **Managing agent issuers through the API,** instead of only through
  `SERENDIP_AGENT_ISSUERS`.
- **Agent Manager's MCP gateway and guardrails** in front of Serendip.
  Not evaluated.
