# Tasks: Spec 018 — Agent identities and the identity-provider management channel

Ordered. `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests or docs). Run `contract-guardian` before committing anything under
`core/` or `providers/`, and `go-reviewer` after each non-trivial Go task.

Dependency sketch: T001 → T002 → T003 → (T004 ∥ T007) → T005 → T006 →
T008 → T009 → T010 → T011 → T012 → T013 → T014.

## T001 — Spike: public paths, port-forward transport, management API — DONE 2026-10-18 (plan.md "Spike findings"; all four observed, on kind 1.34 and K3s v1.36.4)

- **Do**: on kind `serendip-t018`, which runs the spec 017 stack:
  1. Log in and out of the console through the Gateway, capturing every
     ThunderID path the browser calls. Settle `/oauth2/auth/callback` and
     `/oauth2/par` (decision 1).
  2. Through the Gateway, record what `/oauth2/introspect`,
     `/oauth2/dcr/register`, `/oauth2/auth/callback`, `/users` and
     `/design/themes` return today.
  3. With a throwaway Go program (scratchpad), port-forward through the
     `v4.channel.k8s.io` WebSocket mode to the ThunderID pod and call
     `GET /users` with the admin token. Do this on kind 1.34 **and** on a
     k3d cluster running `rancher/k3s:v1.36.4-k3s1` (our pin).
  4. Record the shapes of `GET /users`, `/groups` and `/users/{id}/groups`,
     the management token's `exp`, and whether a refresh token is issued.
- **Files**: `specs/018-agent-manager-poc/plan.md` (a "Spike findings"
  section, with decisions 1–3 amended in place).
- **Verify**: every item has observed evidence. If item 3 fails, amend
  decision 2 (the SPDY-tunnel fallback) before T004. Delete the scratch
  clusters except `serendip-t018`.

## T002 — Spike: Agent Manager quick start and a real agent token — DONE 2026-10-18 (plan.md "Spike findings (T002)"; decisions 6 and 7 amended)

- **Do**:
  - Check the host's RAM and the `.wslconfig` headroom. If the two
    clusters don't fit, stop and ask the user.
  - Run the `amp/v1.0.0` quick start. Create an agent.
  - In that environment's ThunderID, register a resource server with
    identifier `https://serendip.local/api`, add a permission and a role,
    and assign the role to the agent.
  - Get a token with `client_credentials` and `resource=`. Decode it:
    `iss`, `sub` compared with the agent's ID, `aud`, `scope`, `iat`/`exp`,
    and any other claims.
  - Check whether the agent's `owner` is readable, and how. Note the
    issuer URL and the JWKS URL, and whether they can be reached from the
    Windows host.
- **Files**: plan.md "Spike findings" (decisions 6–8 amended if the
  claims differ); notes kept for `findings.md`.
- **Verify**: one real decoded token is recorded (signature not
  included).

## T003 — Exact-path identity route — DONE 2026-10-18 (contract-guardian PASS; on `serendip-t018` the existing six-prefix route was replaced by the next foundations pass, the four `/oauth2` extras and the management paths are 404, and browser login, `/v1/session` and logout work)

- **Do**: replace `routePaths` with the allowlist settled in T001, using
  `Exact` matches and the two prefixes. Update `VERSIONS.md`.
- **Files**: `providers/identity/thunderid/route.go`,
  `thunderid_test.go`, `VERSIONS.md`.
- **Verify**:
  - `go test ./providers/identity/thunderid/...`.
  - On `serendip-t018`, after a `serendipd` restart: the T001 item 2
    paths return 404 through the Gateway, and console login and logout
    still work (AC 1).

## T004 — Port-forward dialer (`providers/kubernetes`) — DONE 2026-10-18 (contract-guardian PASS; go-reviewer findings fixed: cancellation aborts the handshake, latched write errors, `net.ErrClosed` after Close, the overflow-safe length check, `%q` on server text, no bearer token over plain http to a non-loopback host. Live on kind: ReadyPod, about 33 ms per request through the tunnel, a missing pod gives ErrNotFound. Consumers must set `IdleConnTimeout` so a restarted pod is picked up: T005)

- **Do**:
  - A minimal client-side WebSocket implementation: the handshake with
    the `Sec-WebSocket-Accept` check, masked frames, fragmentation,
    ping/pong, close, and a frame-size bound.
  - The `v4.channel.k8s.io` channel framing and port preamble.
  - `ReadyPod`, and a `PortForwardDialer` usable as
    `http.Transport.DialContext`.
- **Files**: `providers/kubernetes/websocket.go`, `portforward.go`, and
  tests (an `httptest` server that speaks the protocol, plus bad-accept,
  oversize-frame and error-channel cases).
- **Verify**: `go test -race ./providers/kubernetes/...`; go-reviewer;
  contract-guardian.

## T005 — `identity.Directory` and the ThunderID directory — DONE 2026-10-18 (contract-guardian PASS; go-reviewer findings fixed. Live on kind: "identity directory reachable through the API server's port-forward (2 groups)". `identity.Principal` Kind/Issuer moved to T007, where they're first used)

- **Do**:
  - Add `Kind`, `Issuer` and `Directory` to `core/identity`.
  - Add `thunderid.Directory` over the dialer:
    - the admin login (sharing `managementToken`), with the token cached
      until `exp − 60s`;
    - paged search capped at 1,000 entries, returning `truncated`;
    - `GroupsOf`;
    - `ErrProviderNotReady` when the provider can't be reached.
  - Wire it in `cmd/serendipd`.
- **Files**: `core/identity/identity.go`,
  `providers/identity/thunderid/directory.go`, `clients.go`,
  `cmd/serendipd/wiring.go`, and tests.
- **Verify**:
  - `go test ./core/identity/... ./providers/identity/... ./cmd/serendipd/...`.
  - On the cluster: a log line shows one successful directory call.
  - `bash .claude/scripts/check-imports.sh`; contract-guardian.

## T006 — Directory and live-groups API routes — DONE 2026-10-18 (go-reviewer PASS, nits fixed. Live on kind: person and group search and a person's groups `source: live`, an unknown person 404; with ThunderID scaled to 0, every answer `signedIn` in about 15 ms and other routes unaffected; live again once the pod returned, without a restart. Wired as `api.WithDirectory`, an optional `NewServer` argument)

- **Do**:
  - Add `GET /v1/authorization/directory` and
    `GET /v1/authorization/principals/person/{id}/groups`, with the gate,
    the `source` field and the registry fallback (decision 5).
  - Add both to the route table.
- **Files**: `api/authorization_handler.go`, `api/authz.go`, and tests
  (the fallback when the directory is down, and a 403 for a Reader).
- **Verify**: `go test ./api/...`, including the fail-closed route test.

## T007 — Agent issuers and agent principals — DONE 2026-10-18 (contract-guardian PASS; go-reviewer found no way to authenticate across issuers, and its findings are fixed: `grant_type` required for agents, the agent subject bounded, sponsors indexed at reload, 503 when the sponsor can't be read. A real Agent Manager token was verified with the real router and verifier code, with the JWKS served locally; the full live run is T011, after the hosts entry)

- **Do**:
  - Parse `SERENDIP_AGENT_ISSUERS` (decision 6; a bad entry is fatal).
  - Add `oidc.Config.Kind`, `MaxLifetime`, a required `iat` for agents,
    and `IgnoreGroups`. Add `oidc.IssuerRouter`.
  - In the middleware: the agent principal, the `agent:` actor, the
    registry entry, and the reserved `agent:` prefix for API keys.
  - Add the `actor_sponsor` audit column, `audit.WithSponsor`, and
    `policy.Engine.SponsorOf`.
- **Files**: `providers/identity/oidc/verifier.go`, `router.go`,
  `cmd/serendipd/main.go`, `wiring.go`, `api/middleware.go`,
  `core/audit`, `core/store/sqlite/store.go`, and tests: issuer
  separation in both directions, wrong audience, unknown issuer,
  lifetime over the maximum, missing `iat`, groups ignored.
- **Verify**: `go test -race ./...`; contract-guardian; go-reviewer.

## T008 — Agent guardrails in `core/policy` and the API — DONE 2026-10-18 (contract-guardian PASS: every route for changing access and every delete route an agent can reach covered; go-reviewer findings fixed: authority checked before the guardrail so no sponsor leaks, `EffectivePermissions` agrees with `Decide` for agents, data-plane deletes count too, and a test pins every DELETE route to a `…/delete` action)

- **Do**:
  - Add `AgentMayHold`.
  - In `CheckAssign`, refuse roles an agent can't hold and require a
    consistent sponsor.
  - In `Decide`, deny the four access-management actions for agents and
    return `NeedsApproval` for an agent's allowed delete.
  - In the API, map NeedsApproval to 403 `needs-approval`, audited.
  - Add `assignableToAgents` on the role view.
- **Files**: `core/policy/engine.go` (or a new `agents.go`),
  `engine_test.go`, `api/authz.go`, `problem.go`,
  `authorization_handler.go`, and tests (table-driven, over every
  built-in role and a custom role with a wildcard).
- **Verify**: `go test ./core/policy/... ./api/...`; contract-guardian.

## T009 — Console

- **Do**:
  - In Add assignment: type `agent`, a required sponsor picker, and
    roles filtered by `assignableToAgents`.
  - Point the picker at `/directory`.
  - In Check access, show live groups with their source label.
  - Show agents with their issuer alias.
- **Files**: `ui/src/screens/Access.tsx`, and tests.
- **Verify**: `npm test` and `npm run build` in `ui/`, plus the embed
  build; a real-browser check on the cluster in T011.

## T010 — Benchmarks [A]

- **Do**:
  - Benchmark the middleware with an agent token (router plus verifier)
    against the person path.
  - Measure directory-call latency over the tunnel on kind.
  - Measure `serendipd`'s idle RSS with the directory wired and the
    tunnel idle.
- **Files**: `api/middleware_bench_test.go`;
  `profiles/edge-1/bench/results/2026-10-xx-spec018/`.
- **Verify**: the agent path's added cost is under 100 µs p99 per request,
  and `serendipd` still passes rule 4 (AC 11).

## T011 — Real-cluster verification (primary setup)

- **Do**:
  - Configure `SERENDIP_AGENT_ISSUERS` with T002's issuer.
  - Observe AC 1–8 end to end: the agent with Reader, then with no
    assignment, then with Contributor deleting (needs approval); the
    guardrail refusals; issuer separation; live groups; the directory
    picker; the channel down (scale ThunderID to 0); the console in a
    real browser.
- **Files**: `specs/018-agent-manager-poc/verification.md`.
- **Verify**: every AC 1–8 is observed, or explicitly marked test-only
  with the reason.

## T012 — Reuse variant and footprint [A for the measurement write-up]

- **Do**:
  - On a fresh throwaway kind cluster with our stack, apply PR #679's
    module, with diffs taken before and after (decision 12).
  - Point an agent at Serendip as in T011.
  - Run `bench-edge1` and `compare` against the spec 017 baseline.
  - Delete the cluster.
- **Files**: `findings.md` (the reuse section);
  `profiles/edge-1/bench/results/2026-10-xx-spec018-reuse/`.
- **Verify**: AC 9 holds: it works with measured numbers, or the report
  records exactly why it doesn't.

## T013 — Final reviews

- **Do**: contract-guardian over `core/` and `providers/`, go-reviewer
  over the whole diff, and `/security-review`. Fix what they find.
- **Verify**: all three PASS, or each finding is fixed or accepted in
  plan.md "Risks".

## T014 — Docs [A]

- **Do**:
  - ADR 0015 (the management channel: transport plus credential, the
    alternatives, and the privilege it holds; AC 4).
  - `identity.md`: agent issuers, the directory, the channel.
  - `authorization.md`: the agent guardrails, sponsor, `needs-approval`.
  - `gateway.md`: the exact-path identity route.
  - `api/CLAUDE.md`.
  - Complete `findings.md` (AC 10). Update `docs/PROGRESS.md`.
- **Verify**: docs-keeper reports no drift between the docs and the code.
