# Tasks: Spec 015 — Identity foundation (human login)

Ordered. `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). Run `contract-guardian` before committing anything under
`core/` or `providers/`.

Dependency sketch: T001 → (T002 ∥ T003 ∥ T004) → T005 ∥ T006 → T007 →
T008 → T009 → T010. T011 [A] can run any time after T001.

## T001 — Spike: a real ThunderID 1.0.1 install on kind

Settles every **(spike)** item in plan.md before code depends on it. This
uses scratch scripts and a scratch values file only; nothing is committed
except plan.md amendments.

- **Do**: On a fresh kind cluster with the gateway set up as spec 012 does,
  `helm install` the pinned chart with plan.md decision 1's values:
  `passwordRef` to a hand-made Secret, and a `70-serendip-client.yaml`
  bootstrap file modelled on the shipped `Console` app. Add a hand-applied
  HTTPRoute (decision 6). Then confirm, recording evidence for each:
  1. The image `ghcr.io/thunder-id/thunderid:1.0.1` exists and starts.
     Record the database mount path and pod Ready time.
  2. The `serendip` client exists with our fields: client ID, PKCE, public
     client, the loopback redirect URI, `username`/`email` in the access
     token.
  3. Discovery is served through the gateway at
     `http://identity.localhost:30080`, and `issuer` equals it exactly.
     The JWKS is reachable through the Kubernetes Service proxy.
  4. **Scripted login works**: authorize → `executionId` → `POST
     /flow/execute` (admin username/password) → 302 to the loopback with
     `code` → token exchange with the PKCE verifier. If it can't be
     scripted, record why and use the browser fallback (plan.md Risks).
  5. Decode the access token: `alg`, `kid`, `iss`, `aud`, `sub` format,
     `client_id`, `scope`, `username`/`email`, `exp`. Decode the ID token
     too, and confirm what tells the two apart.
  6. Which routed paths the login flow actually hit (is `/auth` needed?).
     Confirm `/console` and `/applications` return 404 through the
     gateway.
  7. `kubectl delete pod`: the same `kid` and keys come back, and the
     earlier token still verifies against the new JWKS.
  8. `helm upgrade --install` again with a changed value: no setup job
     runs, the admin password and client are unchanged.
  9. Idle memory of the pod after 3 minutes (`kubectl top` or the kubelet
     Summary API). This is a rough number, not the T009 measurement.
  10. For the UI spec only, note how a redirect URI could be added after
      install (management API with a `system`-scoped token? re-import?).
      Nothing is built for it.
- **Files**: `specs/015-identity-foundation/plan.md` (a "Spike findings"
  section; any decision that changes is amended in place and marked).
- **Verify**: every item above has observed evidence (command output, a
  decoded token, an HTTP status) in "Spike findings". Delete the scratch
  cluster.

## T002 [P] — `core/identity` + API auth middleware

- **Do**:
  - Add `core/identity` with `Principal`, `Authenticator`,
    `ErrInvalidCredential` and `Actor` (plan.md decision 10). No product
    names.
  - In `api/middleware.go`, replace `ActorMiddleware` with
    `AuthMiddleware(apiKeys, authn)` (decision 11): the API key first,
    then `authn`.
    - `ErrInvalidCredential` → 401.
    - `reconcile.ErrProviderNotReady` → 503 via the existing problem
      mapping.
    - A nil `authn` behaves exactly as today.
  - `NewServer` takes the authenticator; update callers.
  - In `cmd/serendipd`, `parseAPIKeys` rejects `person:` actors
    (decision 8).
- **Files**: `core/identity/*`, `api/middleware.go`,
  `api/middleware_test.go`, `api/server.go`, other `api/*_test.go` that
  build a server, `cmd/serendipd/main.go`, `cmd/serendipd/main_test.go`.
- **Verify**: `go test ./core/identity/... ./api/... ./cmd/serendipd/...`.
  Table-driven middleware tests with a fake authenticator, covering:
  - an API key accepted, with its actor in context;
  - a valid token → actor `person:<name>`;
  - an invalid token → 401 with a generic message;
  - not ready → 503;
  - nil authn with an unknown token → 401;
  - no header → 401;
  - an API key named `person:x` skipped.

  Also run `bash .claude/scripts/check-imports.sh` and check that no
  product names appear in `core/identity` (grep).

## T003 [P] — `providers/identity/oidc`: standard-library verifier

- **Do**: Implement `Verifier` (plan.md decisions 7 and 9):
  - JWS compact parse;
  - JWK → RSA/EC/OKP public keys;
  - RS256/384/512, ES256/384, EdDSA;
  - `alg`/`kty` consistency, rejecting `none`, HMAC and unknown algs;
  - claims: `iss`, `aud` (string or array), `exp`/`nbf`/`iat` with
    leeway, required `client_id`;
  - display name: `username` → `email` → `sub`;
  - discovery-or-override JWKS source;
  - a key cache with unknown-`kid` refresh, rate-limited to once per 30 s;
  - `Warm`.

  Returns `identity.ErrInvalidCredential` (wrapped with an internal reason
  for logs) for every rejection.
- **Files**: `providers/identity/oidc/*`.
- **Verify**: `go test -race ./providers/identity/oidc/...`, using an
  `httptest` issuer that generates RSA, EC and Ed25519 keys and signs
  tokens. Table cases:
  - valid for each alg;
  - expired; `nbf` in the future; within leeway;
  - wrong issuer; wrong audience; missing `client_id`;
  - a signature by a different key; tampered payload;
  - `alg: none`; HS256 (with the RSA public key as secret);
  - `alg`/`kty` mismatch; unknown `kid` triggering one refresh, then
    rate-limited;
  - malformed segments.

  Plus:
  - **provider neutrality**: a second, independently configured issuer
    (different keys, discovery-only) verifies with configuration alone;
  - cached keys keep verifying while the JWKS server is down;
  - an unreachable JWKS with an empty cache → an error the composition
    root maps to not-ready.

## T004 [P] — `providers/identity/thunderid`: deploy, admin Secret, route, readiness

- **Do**:
  - `EnsureAdminSecret`: create if absent, never update (decision 3).
  - `Deployer.Ensure(ctx, issuer)`: render the values file, including the
    `70-serendip-client.yaml` bootstrap from T001's confirmed shape, to a
    temp file. Then `helm upgrade --install thunderid <chart> --version
    1.0.1 -n serendip-identity --create-namespace -f <file> --wait
    --timeout 5m`, and remove the temp file.
  - `CheckReady`: `/health/readiness` via the Service proxy.
  - `EnsureRoute(ctx, client, ep, host)`: server-side-apply the HTTPRoute
    with only the decision 6 path prefixes.
  - `JWKSProxyURL`.
  - Issuer derivation helper: the default from the gateway host and port,
    or the `SERENDIP_IDENTITY_ISSUER` override, no trailing slash.
- **Files**: `providers/identity/thunderid/*`.
- **Verify**: `go test ./providers/identity/thunderid/...`:
  - the rendered values are golden-tested: replicas 1, all four databases
    sqlite, httpOnly, publicUrl, ingress off, pinned tag, resources,
    passwordRef, the bootstrap file content;
  - Helm args via a fake `helm.Runner`;
  - a fake API server for: the Secret not overwritten when present; the
    HTTPRoute body with exactly the six prefixes (plan.md decision 6, as amended by T001), the hostname, the
    parentRef from `GatewayEndpoint`, and no `/` rule; `CheckReady` on
    200/503;
  - issuer derivation cases.

## T005 — Wiring: setup, lazy authenticator, foundations, readiness

- **Do**:
  - In `cmd/serendipd`, add `capIdentity` to the tracker and
    `lazyAuthenticator`, which returns `reconcile.ErrProviderNotReady`
    until set.
  - The identity setup step runs after the gateway, in parallel with the
    platform (decision 12): Secret → Helm → ready → route → verifier with
    the Service-proxy JWKS and the Kubernetes HTTP client → `Warm` →
    `set`.
  - The foundations loop re-ensures the route and Secret (`repaired`).
  - Pass the lazy authenticator to `api.NewServer` when a kubeconfig is
    set, nil otherwise.
  - Log the issuer and where the admin password lives: the Secret name,
    not the value.
- **Files**: `cmd/serendipd/{wiring.go,setup.go,lazy.go,main.go,setup_test.go}`.
- **Verify**: `go test -race ./cmd/serendipd/...`, with tests for:
  - identity waits for the gateway and runs alongside the platform;
  - a setup failure is retried and reported `SetupFailed`;
  - the lazy handle answers not-ready, then delegates;
  - the foundations loop calls the identity step and reports
    `RepairFailed`;
  - `all capabilities ready` is logged only when identity is ready too;
  - no kubeconfig → nil authenticator.

  `go build ./... && go vet ./...` clean.

## T006 [P] [A] — Bench harness: Identity group

- **Do**: Map `serendip-identity` → `Identity (ThunderID)` in
  `namespaceGroups`; include it in summary ordering.
- **Files**: `cmd/bench-edge1/summary.go`, `cmd/bench-edge1/*_test.go`.
- **Verify**: `go test ./cmd/bench-edge1/...`; a fixture pod in
  `serendip-identity` lands in the new group, not "unattributed".

## T007 — Real-cluster verification (kind): acceptance criteria 2–8

- **Do**: On a fresh kind cluster (the `urlshortener` cluster untouched),
  with a fresh `serendipd`:
  1. **(AC2)** Readiness:
     - `all capabilities ready` with `identity` ready in
       `/v1/readiness`;
     - discovery at the issuer from the host, with `issuer` exact;
     - the Secret, the release, the route, and the PVCs present
       (`kubectl`).
  2. **(AC3)** Scripted (or browser) auth-code + PKCE login as `admin` →
     access token:
     - `GET /v1/applications` returns 200;
     - creating an Application returns 201;
     - its audit row's actor is `person:admin`, read from SQLite.
  3. **(AC4)** Each of these returns 401 with a generic problem+json, and
     no detail leaks:
     - no token;
     - an expired token (shortened lifetime, or the leeway test via a
       token minted >1h ago, whichever T001 showed practical);
     - a token re-signed with a foreign key;
     - a token for another audience: a second throwaway client declared
       in the spike values, or an ID token;
     - garbage.
  4. **(AC5)** The spec 011/012 e2e flow with an API key, unchanged;
     `go test ./...` green.
  5. **(AC6)**:
     - Restart `serendipd`: same client, admin password unchanged (login
       still works), no setup job.
     - `kubectl delete pod` ThunderID: the pre-restart token still
       verifies.
     - With `SERENDIP_REENSURE_INTERVAL=30s`, `kubectl delete httproute`:
       it's recreated and discovery is reachable again, with no restart.
  6. **(AC7)** Stop the kind node and start `serendipd` on a fresh store:
     - API keys work;
     - `identity` is not ready with a reason;
     - a bearer token → 503 with an honest message.

     Start the node: identity becomes ready without a restart, and the
     same token → 200.
  7. **(AC8)** `check-imports.sh` clean; `grep -ri thunder core api`
     empty.
- **Files**: code fixes as needed; scratch scripts.
- **Verify**: each item observed directly (`kubectl`, curl, API JSON,
  SQLite query), not inferred.

## T008 — Benchmark re-run (AC9)

- **Do**: Two simulated-T470 runs of the spec 013 harness (same commands
  as the spec 014 re-run). Compare each against spec 014's run 1 for the
  existing groups, and report the new Identity group's idle memory/CPU,
  `serendipd` rule 4, the recomputed 16 GB verdict, and cold start.
- **Files**: `profiles/edge-1/bench/results/<date>-spec015-simulated-t470/`,
  `profiles/edge-1/bench/002-edge1-footprint.md`.
- **Verify**: `bench-edge1 compare` outputs recorded; the write-up states
  the Identity number labelled as a simulation (rule 6) and, against ADR
  0006, makes no "lighter than Keycloak" claim (Keycloak is not measured).

## T009 — Final reviews

- **Do**: Run `contract-guardian` (holistic: rule 1 in `core/identity` and
  `api/`, rule 2, rule 8 actor, rule 6 honesty in docs) and `go-reviewer`.
  Focus on the JWS/JWK code (algorithm confusion, constant-time-irrelevant
  but error-path leaks, key cache concurrency), middleware ordering, and
  setup goroutine safety. Re-read spec.md line by line against the code.
- **Files**: fixes as needed.
- **Verify**: both PASS; fixes re-tested (`go test -race ./...`,
  `go vet ./...`, `gofmt -l .`).

## T010 — Docs sync + PROGRESS

- **Do**:
  - In `identity.md`, add "What's built (spec 015)":
    - the two auth paths and the actor format;
    - the issuer and how to change it;
    - admin credential custody and how to read it;
    - plain HTTP is localhost-only;
    - no RBAC, so anyone who logs in is an administrator;
    - no revocation before expiry;
    - the signing-cert 365-day note;
    - redirect-URI limits.

    Fix that doc's stale repo URL.
  - Update `capability-model.md`/`provider-model.md` (identity has a
    provider) and `api/CLAUDE.md` (auth middleware).
  - Update `docs/PROGRESS.md`.
- **Files**: as listed.
- **Verify**: `docs-keeper`-style read-through: no doc still says "no
  identity provider implemented" (grep `ActorMiddleware`, "intentionally
  weak", "no real identity provider").

## T011 [A] — ADR 0006 re-check (AC1)

- **Do**: Add a dated "Re-check 2026-10-17" section:
  - a verdict per trigger (from the oss-researcher findings), the
    no-release-tag watch item, and the closing date;
  - the repo URL `github.com/thunder-id/thunderid`;
  - the chart `oci://ghcr.io/thunder-id/helm-charts/thunderid` and the
    pinned version 1.0.1;
  - the OpenWallet transfer marked unconfirmed.

  Restate the fallback triggers unchanged.
- **Files**: `docs/decisions/0006-thunder-as-identity-provider.md`.
- **Verify**: the ADR names the version this spec deploys, and that
  version matches `providers/identity/thunderid`'s `ChartVersion`
  constant (checked again at T009).
