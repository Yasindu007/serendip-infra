# Tasks: Spec 016 — UI MVP (Serendip Console)

Ordered. `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). Run `contract-guardian` before committing anything under
`core/` or `providers/`. Run `go-reviewer` after each non-trivial Go task.

Dependency sketch: T001 → (T002 ∥ T003 ∥ T005) → T004 → T006 → T007 →
T008 → T009 → T010 → T011 → T012. T002 and T005 don't need T001; T003
and T007 do.

The pre-UI gate (spec.md) is closed when T001 is done and plan.md has been
amended with its findings. No screen that needs login (T008) starts
before that.

## T001 — Spike: ThunderID session behaviour on a live install — DONE 2026-10-17 (plan.md "Spike findings")

- **Do**: On a kind cluster with spec 015's stack running, use scratch
  scripts (spec 015's `oidc-login.sh` flow) to settle each item. Record
  evidence for each:
  1. **The bootstrap client can be changed** (Upstream fact 5): get an
     admin token through `CONSOLE` (`scope=system`), then
     `GET /applications/<serendip id>`, add a redirect URI, and `PUT` the
     full document. Then re-run login with the new URI. If the PUT is
     rejected as declarative, try `POST /applications` for a
     `serendip-console` client and pick decision 7b. Record the scope
     actually needed.
  2. **Refresh behaviour:**
     - The refresh-token lifetime as issued.
     - Whether refresh rotates the token: the old one used again →
       `invalid_grant`?
     - What a second refresh with the same token returns.
  3. **Revocation:** `POST /oauth2/revoke` with `client_id` and the refresh
     token, then a refresh with it must fail. Also, are `revocation_endpoint`
     and `end_session_endpoint` in discovery by default?
  4. **Logout:** `/oauth2/logout` with `id_token_hint` and a registered
     `post_logout_redirect_uri` ends on that URI. Record whether matching
     is exact.
  5. **Disabled user:** create a second user, log in, disable the user,
     refresh → it should fail. Record the result.
  6. **Strict cookie** (plan.md Risks): serve a stub page from a scratch
     Go server on `localhost:8080` whose callback sets a Strict cookie and
     303s to `/`. In a real browser, after coming back from
     `identity.localhost:30080`, the page's first same-origin `fetch`
     carries the cookie.
- **Files**: `specs/016-ui-mvp/plan.md` (a "Spike findings" section;
  amend decisions 3, 5 and 7 in place where a finding changes them).
- **Verify**: every item has observed evidence in "Spike findings", and
  no item is left *unverified*. Delete the scratch cluster.

## T002 [P] — ADRs 0012 and 0013 — DONE 2026-10-17 (Verify: every package named; user review of the ADRs pending)

- **Do**:
  - **ADR 0012 (UI toolchain and dependencies):** plan.md decision 10.
    For every runtime and dev dependency: what it buys, its maintainers
    and licence, and its transitive count from `pnpm why`/`pnpm list
    --depth Infinity`. Also the supply-chain controls, the pnpm version
    pin and why, and the rejected options: Tailwind, a component kit,
    TanStack Query, react-router, npm/yarn.
  - **ADR 0013 (console session topology):** in-process BFF vs SPA tokens
    vs a separate BFF proxy, and the in-memory session store. Triggers to
    revisit: several instances, HTTPS, RBAC.
- **Files**:
  - `docs/decisions/0012-ui-toolchain-and-dependencies.md`
  - `docs/decisions/0013-console-session-topology.md`
- **Verify**: both ADRs are present with Status Accepted after the user
  reviews them. Every package in `ui/package.json` (once T005 lands) is
  named in ADR 0012: checked with
  `node -e` over `package.json` against a grep of the ADR.

## T003 [P] — `core/identity.LoginClient` + `providers/identity/oidc` relying party — DONE 2026-10-17 (contract-guardian PASS; findings 1–4 fixed)

- **Do**:
  - Add the `LoginClient` interface and `Tokens` (plan.md "Interfaces"),
    with no product names.
  - Implement `oidc.RelyingParty`:
    - discovery, with an optional back-channel base URL override;
    - `AuthCodeURL` (S256);
    - `Exchange` and `Refresh` (form POST, public client);
    - `Revoke`, returning `ErrUnsupported` when the endpoint is absent;
    - `EndSessionURL`;
    - `VerifyIDToken`, which reuses the JWS/JWK code and checks `iss`,
      `aud`, `exp` and `nonce`.
  - Bounded timeouts, and no token in any error string.
- **Files**:
  - `core/identity/identity.go` (+ test)
  - `providers/identity/oidc/rp.go`
  - `providers/identity/oidc/rp_test.go`
- **Verify**: `go test ./core/identity/... ./providers/identity/oidc/...`
  with table tests against an `httptest` issuer:
  - a wrong nonce, issuer or audience, an expired token, an `alg:none`
    token, and an unknown kid are each rejected;
  - a token exchange that answers an error yields an error that doesn't
    contain the code or verifier;
  - discovery without revocation gives `ErrUnsupported`.

  Also: `bash .claude/scripts/check-imports.sh` passes and
  `contract-guardian` PASSes.

## T004 — `api`: sessions, `/auth/*`, `/v1/session`, cookie auth, hardening — DONE 2026-10-17 (go-reviewer: should-fix fixed with a regression test)

- **Do**: Implement plan.md decisions 3–6, 8 and 9:
  - `api/session.go`: the session table, pending logins, the per-session
    refresh lock and the sweeper;
  - `api/auth_handler.go`;
  - the cookie path in `AuthMiddleware`, with the CSRF header and Origin
    checks;
  - `console_headers.go`;
  - `MaxBytesReader` and `no-store`;
  - the per-IP limiter;
  - `NewServer`'s new parameters.

  The login client is lazy: until identity is ready, `/auth/login`
  answers 503 problem+json and the console shows it.
- **Files**: `api/{session.go,auth_handler.go,console_headers.go,middleware.go,server.go}` + tests
- **Verify**: `go test ./api/...` covering every T-row of the threat model
  marked "T003/T004 unit". The plan's numbering means these tests:
  - **callbacks:** a bad, missing, reused or expired state; a missing
    pre-login cookie; a wrong nonce; a sub mismatch;
  - **redirects:** the `return` open-redirect table;
  - **fixation:** an unknown session ID is ignored, and every login gets
    a fresh ID;
  - **CSRF:** no header → 403; a wrong or missing Origin on POST → 403; no
    CORS headers ever sent;
  - **mixed credentials:** bearer + cookie → 400;
  - **refresh:** 20 concurrent requests on an expiring session cause
    exactly 1 refresh; a failed refresh → 401 and the session is gone;
    an unreachable provider → 503 and the session is kept;
  - **expiry:** idle and absolute, using a fake clock;
  - **limits:** body > 1 MiB → 413; past the pending cap, the oldest
    entry is evicted; the limiter → 429 with `Retry-After`;
  - **leakage:** a log capture contains no token or raw session ID;
  - **unchanged paths:** existing API-key and bearer tests are unchanged
    and still pass.
  - **public shell:** `GET /` and `/assets/*` succeed with no cookie (Spike
    finding 6: the first document load after login carries no Strict
    cookie).

  `go vet ./...` is clean, and `go-reviewer` and `contract-guardian`
  PASS.

## T005 [P] — `ui/` scaffold, supply-chain controls, embed — DONE 2026-10-17 (embed lives in `cmd/serendipd/console`; `ui/go.mod` fences `node_modules` off from Go tooling)

- **Do**:
  - Pin pnpm through `packageManager` and corepack.
  - Add `.npmrc` per decision 10, and `package.json` with exact versions
    (the newest release of each that is ≥ 14 days old).
  - Add a strict `tsconfig`, a Vite config (dev proxy for `/v1` and
    `/auth` to `serendipd`), and a `scripts/guard-env.mjs` that refuses
    to run with `GITHUB_TOKEN`/`NPM_TOKEN`/`NODE_AUTH_TOKEN` set.
  - Add `ui/embed.go` and the `dist/.gitkeep` handling, and wire
    `ui.Handler()` into `cmd/serendipd`.
  - Add a size-budget check (a script over `dist/assets/*.js` gzip
    sizes).
  - Update `ui/CLAUDE.md` with the build and test commands and the
    security rules.
- **Files**:
  - `ui/{package.json,pnpm-lock.yaml,.npmrc,tsconfig.json,vite.config.ts,index.html,src/main.tsx,embed.go,scripts/*,CLAUDE.md}`
  - `.gitignore`
  - `cmd/serendipd/main.go`
- **Verify**:
  - With the credential env vars unset:
    - `pnpm install --frozen-lockfile` succeeds, and the log shows no
      lifecycle scripts run.
    - `pnpm audit --audit-level high` exits 0.
    - `pnpm build` succeeds within budget.
  - The guard script exits non-zero with `GITHUB_TOKEN=x`.
  - In a clean clone with no `dist`: `go build ./...` and
    `go test ./...` pass, and `serendipd` serves the "console not built"
    page.

## T006 — Design system: tokens, shared components, `ui/DESIGN.md` — DONE 2026-10-17 (44 contrast pairs pass in both themes; no raw HTML, no inline style)

- **Do**:
  - Record the direction the user picked (default A). Write
    `src/styles/tokens.css` for light and dark: colour roles, type scale,
    spacing, radii and focus ring. Bundle the Plex fonts with fontsource.
  - Build plan.md decision 11's components with their states (default,
    hover, focus-visible, disabled, loading, error).
  - Write `ui/DESIGN.md` covering tokens, components, the status
    vocabulary (always icon + word + colour), and the rules: no fake
    data, unbuilt features are labelled, no raw HTML.
- **Files**:
  - `ui/src/styles/*`
  - `ui/src/components/*` (+ tests)
  - `ui/DESIGN.md`
  - `ui/THIRD_PARTY.md`
- **Verify**:
  - `pnpm test` passes component tests, including: StatusText always
    renders text, ApiEquivalent never renders an `Authorization` value,
    and links render only for `http(s)` URLs (`javascript:` → plain
    text).
  - `pnpm tsc --noEmit` is clean.
  - A contrast script checks every text/background token pair in both
    themes at ≥ 4.5:1 (≥ 3:1 for large text and UI borders).
  - `grep -r "dangerouslySetInnerHTML\|innerHTML" ui/src` is empty.

## T007 — `providers/identity/thunderid`: console redirect URIs — DONE 2026-10-17 (no bootstrap-file change: one idempotent `EnsureClientRedirects` covers new and existing installs)

- **Do**:
  - Add the console URIs to the bootstrap client document for new
    installs.
  - Add `EnsureClientRedirects` for existing installs (T001 showed the
    bootstrap client is editable, so decision 7b is dropped), called from identity setup and the foundations
    loop. It's reads only when nothing has drifted, and it never removes
    a URI it didn't add.
  - Add `TokenProxyURL`/`RevokeProxyURL`, and wire the lazy
    `RelyingParty` in `cmd/serendipd/wiring.go` (with
    `SERENDIP_PUBLIC_URL` and the insecure-origin guard, decision 2).
- **Files**:
  - `providers/identity/thunderid/{values.go,clients.go,ready.go}` + tests
  - `cmd/serendipd/{main.go,wiring.go}`
- **Verify**:
  - `go test ./providers/identity/... ./cmd/...` passes, including a
    fake management API that shows: a no-drift pass makes zero PUTs;
    drift makes one PUT carrying the full document with `clientId`
    unchanged; a foreign URI is kept.
  - Origin-guard table: `http://localhost:8080` ok;
    `http://192.168.1.5:8080` refused without the opt-in; a URL with a
    path refused.
  - `contract-guardian` PASS.

## T008 — Screens: sign-in, Overview, Applications, Components — DONE 2026-10-17 (55 UI tests; 75 KiB gz JS of 160; smoke-tested on the real binary without a cluster)

- **Do**: Build the screens from spec.md Scope-IN on T006's components:
  - a typed API client (with the CSRF header, timeouts, abort, and the
    plan.md decision 14 error contract);
  - `usePoll` (decision 12);
  - the router;
  - the session gate;
  - Overview (readiness + topology), the Applications list, create and
    detail, and Component create, edit and detail (ports, expose, env
    with a "not secret" note, Conditions, endpoints, relationships);
  - the ApiEquivalent panel on every form;
  - empty, loading, error and not-ready states.

  No delete anywhere.
- **Files**: `ui/src/{api,routes,screens,hooks}/*` + tests
- **Verify**:
  - `pnpm test` passes the screen tests against a mocked `fetch`: 401 →
    sign-in prompt keeping `return`; 503 → banner; problem+json shows the
    request ID; polling stops when hidden; a request in flight is aborted
    on navigation; the ApiEquivalent body equals the request actually
    sent.
  - `grep -ri "delete" ui/src/screens` finds no delete action.
  - The `tsc` and the bundle budget pass.

## T009 — Performance measurements — DONE 2026-10-18 (all targets PASS; see verification.md AC 12)

- **Do**:
  - Write a Go benchmark of `AuthMiddleware` for API key vs cookie, and
    log the refresh timing on a real install.
  - Add a bench-harness scenario: `serendipd` idle vs 3 polling clients
    (a script issuing the console's request pattern with a session
    cookie) on the simulated T470, with the UI embedded.
- **Files**: `api/middleware_bench_test.go`,
  `profiles/edge-1/bench/{002-edge1-footprint.md,results/*}`
- **Verify**: the numbers are recorded against plan.md's targets table.
  A missed target is recorded as a FAIL, not hidden. Constitution 4 is
  re-checked.

## T010 — Real-browser verification (kind): acceptance criteria 1–6, 9–12 — DONE 2026-10-18 (verification.md; a keyboard-only pass by a person is still open)

- **Do**: Run a fresh install, and separately a spec-015 install upgraded
  to this build, then do the flow in a real browser (the built-in browser
  or Chrome tools; check which is available first):
  - log in, check Overview, and create an Application and a Component
    with an exposed port;
  - watch it reach Ready and open its endpoint;
  - check the audit actor in SQLite;
  - repeat one mutation with `curl` and an API key, using the
    ApiEquivalent text;
  - send a cross-site POST from a page on another origin;
  - test session expiry with a shortened idle setting, then logout.
  - Check `localStorage`, `sessionStorage`, `document.cookie` and the
    `serendipd` logs for tokens.
  - Check the console for CSP violations.
  - Take screenshots at 1440 and 390 px wide, in light and dark.
  - Go through the create-Component flow with the keyboard only.
- **Files**: `specs/016-ui-mvp/verification.md` (evidence plus
  screenshots under `specs/016-ui-mvp/screenshots/`)
- **Verify**: every acceptance criterion has observed evidence or an
  explicit "not verified, because …". No destructive operation is run.

## T011 — Final reviews

- **Do**: Run `contract-guardian` over everything under `core/` and
  `providers/`, `go-reviewer` over the Go changes, and `/security-review`
  over the branch. Fix what's blocking.
- **Verify**: all three report PASS, or their blocking findings are
  fixed and re-reviewed.

## T012 [A] — Docs sync + PROGRESS

- **Do**:
  - Add a "Console sessions" section to `identity.md`, with the
    no-RBAC-yet warning stated plainly.
  - Add the cookie path and CSRF header to `api/CLAUDE.md`.
  - Update `GETTING-STARTED.md` (how to open the console).
  - Add the gaps found (delete approvals, audit read API, RBAC) to
    PROGRESS "Next up".
  - Rewrite `docs/PROGRESS.md`.
- **Files**: the docs above
- **Verify**: `docs-keeper` reports no drift between the docs and the
  merged code.
