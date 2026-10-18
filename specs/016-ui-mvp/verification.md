# Spec 016 — verification record (T010)

Everything here was observed directly on 2026-10-18 unless marked
otherwise.

## Setup

- **Cluster:** a fresh kind cluster `serendip-t010` (`kindest/node:v1.34.0`,
  `profiles/edge-1/bench/kind-t470.yaml` port mapping).
- **serendipd:** built from the working tree with the console embedded,
  `SERENDIP_ADDR=:8080`, default public URL `http://localhost:8080`.
- **The user:** signed in from their own browser and created the
  Application and Component through the console.
- **Automated checks:**
  - a scratch Python driver that signs in through `serendipd`'s own
    `/auth/login` and `/auth/callback` against the real ThunderID;
  - a dependency-free Node script driving headless Chrome over the
    DevTools protocol.

  The admin password was read into the environment, never printed by the
  scripts.

## Found and fixed during T010

1. **The Kubernetes Service proxy strips `Authorization`,** so the
   management API can't be reached through it (plan.md "T010 finding").
   - Fresh installs now register the console URIs through the bootstrap
     file.
   - Every pass first checks read-only, without credentials.
   - On the fresh install, the log showed `console login enabled` with
     no admin login at all.
2. **The bootstrap client had no `signOutFlowId`,** so logout returned 500
   ("Sign-out flow is not configured for the application"). It now points
   at ThunderID's default sign-out flow (`…006a`).
3. **Phone-width layout:**
   - status words and table cells broke mid-word;
   - the rail wrapped its two "not available yet" items;
   - the Overview repeated the banner's sentence.
   Fixed in the UI; the screenshots below were retaken after fixes 1–2 of
   this list, and the cell-wrapping fix was rebuilt afterwards.

## Acceptance criteria

| AC | Observed |
|---|---|
| 1 | **Fresh install:** `/auth/login` → ThunderID → `/auth/callback` → 303 to the requested return path. **Requests:** authorize carries `code_challenge_method=S256` and a `nonce`. **Cookies:** pre-login `Path=/auth; Max-Age=600; HttpOnly; SameSite=Lax`; session `Path=/; HttpOnly; SameSite=Strict`. **What page JS can see** on every captured page: `document.cookie` = `""`, `localStorage.length` = 0, `sessionStorage.length` = 0. **Logs:** neither `serendipd` log contains a JWT, `access_token`, `refresh_token` or `serendip_session=`. |
| 2 | The user created `storefront` and `web` (`nginx:1.27`, http 80 exposed) in the console. **Conditions:** Application `Ready=True/Deployed`; Component `Ready=True/Deployed` and `Reachable=True/Serving`, with one endpoint. **Endpoint** → **200**. **Audit rows** for both creations: actor `person:admin`, interface `http`. |
| 3 | Every form shows the request it sends. A UI test asserts that the shown text equals the sent request; the curl repeat is in the T008 smoke test (API key path). |
| 4 | **Cookie without `X-Serendip-Csrf`:** 403. **Replayed callback:** 303 to `/signin?error=expired`. **An ended session:** 401, and the UI shows sign-in (UI test). Cross-site POSTs are covered by the `api` tests. |
| Logout | **Verified with the automated session:** `POST /auth/logout` with the CSRF header and `Origin` clears the cookie (`Max-Age=0`), and the response's `location` carries no `id_token_hint`. ThunderID's `/oauth2/logout` → 302 to `/gate/signout`. The old session then gets **401**. |
| 5 | **While identity was not ready** (a no-cluster smoke run): `/auth/login` → `/signin?error=provider_unavailable`, and API keys kept working. |
| 6 | **Headers:** the CSP and other security headers are present (curl smoke test). **Violations:** zero CSP violations and zero console errors across 28 captures. The pages captured: Overview, Applications, Create application, Application detail, Component detail, Edit component and Sign-in, each at 1440 and 390 px, in light and dark. |
| 7 | **Install:** `pnpm install --frozen-lockfile` with scripts off; `pendingBuilds` empty. **Audit:** no known vulnerabilities. **Release age:** pnpm refused a 3-day-old React. **ADR 0012** names every package. |
| 8 | **Without Node:** `go build ./...` and `go test ./...` pass (the `dist` placeholder). **RSS with the UI embedded** (T009, simulated T470): 34.5 MiB idle, 0.00% CPU — rule 4 **PASS** (spec 015: 26.2 MiB). |
| 9 | **Screenshots:** as in AC 6, in `scratchpad/t010/shots/` (session-local). **Keyboard-only use** of the create-Component flow: *not yet verified by a person*. **Contrast:** the script passes all 44 pairs. |
| 10 | No delete action on any screen (UI test plus a grep). Secrets and Volumes are labelled "not available yet". |
| 11 | Threat-model rows have their tests (T003/T004/T006/T008). Session tests pass: state, nonce, replay, concurrent refresh, idle and absolute expiry, logout. |
| 12 | **T009, simulated T470** (`profiles/edge-1/bench/002-edge1-footprint.md`, "Spec 016 re-run"): <br>• **Cookie path:** +2.7 µs per request (target < 50 µs). <br>• **Refresh through the Service proxy:** p95 48 ms (target < 300 ms). <br>• **3 polling tabs:** 1.28 requests/s, 0 errors; `serendipd` 0.04% CPU, 35.9 MiB (**PASS**). <br>• **Cluster groups:** all within tolerance of spec 015. <br>The **413** for oversized bodies is covered by the curl smoke test and the `api` tests. The cut-off of slow-header clients rests on the server's `ReadHeaderTimeout` (10 s); there's no dedicated slow-client test. |

## Option B: registering the URIs on an existing install

Verified on the benchmark cluster on 2026-10-18.

- **Before:** `http://localhost:9090/auth/callback` was rejected at
  authorize (`/gate/error`).
- **Steps:** a `kubectl port-forward`, then
  `go run ./cmd/serendip-identity-redirects --public-url http://localhost:9090`.
  - **First run:** it registered both URIs.
  - **Second run:** a no-op that still succeeds.
- **After:** authorize accepted the URI (`/gate/signin`).
- **Mistakes:**
  - A wrong password gives "credentials not accepted", without echoing
    the password.
  - A missing password says where to find it.

## Not verified yet

- A keyboard-only pass by a person (AC 9).
