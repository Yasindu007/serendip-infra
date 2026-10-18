# Plan 016: UI MVP (Serendip Console)

Status: Approved 2026-10-17 (after T001; direction "Combined" chosen)

Spec: `spec.md` (approved 2026-10-17; shipping without RBAC confirmed).
Upstream ThunderID facts are in "Upstream facts" below. They came from an
oss-researcher pass on 2026-10-17. Items marked **(spike)** are settled by
T001 on a real install before anything depends on them.

## Approach in one paragraph

`serendipd` serves the built console from its own listener, embedded in the
binary. It also becomes the console's OIDC relying party (a BFF): it sends
the browser to ThunderID, exchanges the code server side through the
Kubernetes Service proxy (as spec 015 already does for keys), verifies the
tokens with the existing provider-neutral verifier, and keeps the tokens in
an **in-memory** session table. The browser gets an opaque `HttpOnly`
`SameSite=Strict` cookie. `AuthMiddleware` gains a third credential, the
session cookie, resolved to the same `person:<name>` actor. Cookie
requests must carry a custom header, and state-changing ones must also
pass an exact `Origin` check. The UI is React + TypeScript built by Vite,
with two runtime dependencies (`react`, `react-dom`) and plain CSS on
design tokens. It's installed with pnpm, with install scripts off, a
minimum release age, and a frozen lockfile. The UI polls; nothing streams.

## Trust boundaries

```
 ┌──────────── browser (untrusted; may run injected script) ───────────┐
 │  console JS (same origin)      cookie: sid (HttpOnly, Strict)       │
 └──────┬───────────────────────────────────────────┬──────────────────┘
        │ GET /, /assets/*  (public)                │ top-level redirects
        │ /v1/* + cookie + X-Serendip-Csrf          │ to the login page
        │ /auth/login|callback|logout               │
 ═══════▼═══════════════ boundary 1: HTTP ══════════▼══════════════════
 ┌──────────────────────── serendipd (trusted) ─────────────────────────┐
 │ AuthMiddleware: API key | bearer JWT | session cookie → actor        │
 │ session table (memory): hash(sid) → tokens, principal, expiries      │
 │ pending logins (memory, capped): state → PKCE verifier, nonce        │
 │ API handlers → store (SQLite) → reconcilers → providers              │
 └───────┬──────────────────────────────────────────────┬───────────────┘
 ════════▼═══ boundary 2: kube API (Service proxy) ═════▼════════════════
 ┌─ ThunderID (serendip-identity) ─┐        ┌─ cluster providers ──────┐
 │ /oauth2/token, /jwks, revoke    │        │ OpenChoreo, kgateway, …  │
 │ management API (redirect URIs)  │        └──────────────────────────┘
 └──────────────▲──────────────────┘
                │ boundary 3: the browser reaches only the login-facing
                │ paths, through the shared gateway (spec 015 decision 6)
```

- **Boundary 1.** The only credential the browser holds is the session
  ID. Nothing the browser sends picks an upstream destination. The
  issuer, token URL and redirect URI come from operator config and
  discovery, never from a request.
- **Boundary 2** is unchanged from spec 015: `serendipd`'s kubeconfig. The
  token, revocation and management calls ride it, exactly like the JWKS
  fetch.
- **Boundary 3.** The browser goes to ThunderID's public issuer URL only
  for the login UI and the end-session redirect.

## Decisions

1. **Same origin, embedded.**
   - **Embedding** (amended in T005). `cmd/serendipd/console/console.go`
     embeds `all:dist`; `pnpm -C ui build` writes `dist/app` there.
     `dist/.gitkeep` is committed, so `go build ./...` works without Node.
     `ui/go.mod` (module `serendip.invalid/ui-fence`) holds no Go code: it
     only keeps Go tooling out of `ui/node_modules`.
   - **Serving.** `console.Handler()` serves the files. For any path that
     isn't a file or `/v1`, `/auth` or `/healthz`, it returns `index.html`
     (client-side routes); a missing `/assets/` file is a 404. When
     `index.html` is missing, it says the console isn't built and how to
     build it. Security headers (decision 8) are set by `api`, not here.
   - **Wiring.** `api.NewServer` takes a `*api.Console` (nil = no console)
     that carries the handler, public origin, login client and session
     table, so `api` never imports the console package.
   - **Headers.** Hashed asset filenames get `Cache-Control: public,
     max-age=31536000, immutable`, and `index.html` gets `no-cache`.
2. **Public origin is configured, not guessed.**
   - **Where it comes from.** `SERENDIP_PUBLIC_URL` (default
     `http://localhost:<port of SERENDIP_ADDR>`) is the console origin. The
     redirect URI is `<public>/auth/callback` and the post-logout URI is
     `<public>/`. Both must be absolute `http(s)` URLs with no path, query
     or fragment beyond what we add.
   - **Unexpected `Host`.** A request whose `Host` differs from the public
     origin isn't served the console; it's redirected to the public URL.
     This keeps `127.0.0.1` vs `localhost` cookie confusion from producing
     half-logged-in states.
   - **Remote browsers.** A non-`localhost` `http` origin is refused at
     startup unless `SERENDIP_ALLOW_INSECURE_ORIGIN=1`. Browsers treat
     plain HTTP as secure only on `localhost`, and we won't ship a cookie
     over a network in clear without the operator opting in (until the
     HTTPS spec).
3. **The login flow** (`/auth/*`, mounted outside `/v1`, so it isn't
   behind `AuthMiddleware`):
   - **`GET /auth/login?return=<path>`.** `return` must be a same-origin
     *path* beginning with exactly one `/` (no `//`, no `\`, no scheme).
     Anything else becomes `/` (no open redirect).
     - It generates `state` (32 random bytes), a PKCE verifier (S256) and
       a `nonce`, and stores
       `{verifier, nonce, return, created}` under `state` in the pending
       table. The table has a cap of 256 entries, a 10-minute TTL, and
       evicts the oldest first.
     - It sets a `__Host-`-style pre-login cookie,
       `serendip_login=<state>` (`HttpOnly`, `SameSite=Lax`, `Path=/auth`,
       10 min). This ties the callback to the browser that started it. It
       has to be Lax, because the callback is a cross-site top-level
       navigation.
     - It answers 302 to `authorization_endpoint` with `response_type=code`,
       `client_id`, `redirect_uri`, `scope=openid`, `state`, `nonce`,
       `code_challenge` and `code_challenge_method=S256`.
     - It's rate limited per client IP (decision 9).
   - **`GET /auth/callback?code&state`:**
     - It requires the `serendip_login` cookie to equal `state`, and the
       pending entry to exist and be unexpired. The entry is deleted on
       first use, so a replayed callback fails. Any `error=` parameter
       goes to an error page that shows the OAuth error code only, never
       the description verbatim.
     - It exchanges the code with the verifier at the token endpoint
       through the Service proxy.
     - It verifies the **ID token**: signature, `iss`, `aud`, `exp`,
       `nonce` equal to the stored one. It verifies the **access token**
       with the existing `Authenticator`, which yields the `Principal`.
       Both must name the same `sub`.
     - It creates a session. The new ID is 32 random bytes, base64url, and
       is stored as its SHA-256. Every login gets a new ID, so session
       fixation is impossible. It sets `serendip_session` (`HttpOnly`,
       `SameSite=Strict`, `Path=/`, no `Domain`, `Secure` when the public
       URL is https), clears `serendip_login`, and answers 303 to `return`.
       - **Why Strict still works after a cross-site redirect.** The
         console shell is public. Every authenticated call is a
         same-origin `fetch` from the page, which carries Strict cookies.
         T001 checks this in a real browser.
   - **`GET /v1/session`** (cookie or bearer) → `{actor, method, subject,
     expiresAt, idleExpiresAt}` or 401. `method` is `session` or `bearer`;
     the other three fields are only present for a session. This is how the UI learns who's
     signed in. No token, ID or claim beyond these is ever returned.
   - **`POST /auth/logout`** (cookie + CSRF checks):
     - It deletes the session and revokes the refresh token at the
       revocation endpoint, best effort and bounded at 3 s, if upstream
       supports it.
     - It returns `{location}`: the provider's end-session URL with
       `post_logout_redirect_uri` and `client_id` (no `id_token_hint`; see
       Risks), if supported, otherwise `/`.
     - It's a POST and not a GET, so a cross-site `<img>` can't log people
       out.
4. **Sessions live in memory** (ADR 0013):
   - **What a session holds:**
     - the principal;
     - the access token's expiry;
     - the refresh token;
     - the ID token (kept, but no longer sent at logout);
     - its creation time and last-seen time.
   - **Expiry:**
     - **idle 30 min.** Last-seen is refreshed on each request, at most
       once a minute, so it doesn't contend on every call.
     - **absolute 12 h.** A new login is required after that, whatever
       the refresh token's lifetime.
   - **Cap:** at most 1024 sessions; past that, the least recently used is
     dropped.
   - **Why not SQLite.** A refresh token on disk is a credential at rest,
     and we have no at-rest encryption key in the control plane. Memory
     means a `serendipd` restart signs everyone out. On a single-instance
     edge-1 that's an acceptable, documented cost. Several instances will
     need a shared encrypted store or sticky routing, and ADR 0013 records
     that as the trigger to revisit.
   - **No background goroutine per session.** Expired entries are swept
     lazily on lookup, plus one sweep per minute.
5. **Refresh happens once at a time, on demand.**
   - **When.** On a cookie request whose access token expires within 60 s.
     The session's own mutex serialises it: the first request refreshes,
     the others wait for it (bounded by the request context) and then use
     the result. This uses the standard library only, no `singleflight`
     dependency.
   - **Rotation.** If the provider rotates refresh tokens, the new one
     replaces the old atomically under the same lock.
   - **Failure.** A failed refresh, meaning `invalid_grant` or a principal
     that fails verification, deletes the session, and the request gets a
     401.
   - **Provider unreachable.** If the provider is unreachable, the request
     gets a 503 "identity provider not ready". The session survives, so
     an outage doesn't sign everyone out.
   - **This serialisation is required, not optional** (amended after Spike
     finding 3). ThunderID rotates refresh tokens. Presenting a used
     refresh token revokes the whole token family. So two concurrent
     refreshes would end the session. Unit tests in T004 must prove that
     20 concurrent requests cause exactly one refresh.
   - **The result** (amended after Spike finding 5). ThunderID 1.0.1 has
     no "disabled user" state. Access is removed by **deleting** the user,
     and their next refresh then fails. So a deleted person loses console
     access within one access-token lifetime (≤ 60 min), and the docs say
     exactly that.
6. **Cookie authentication in `AuthMiddleware`.** The order is: a bearer
   header, which uses the existing paths unchanged, otherwise the
   `serendip_session` cookie. A request that carries both is rejected with
   a 400, so the two can't be confused. A cookie request must:
   - carry `X-Serendip-Csrf: 1`. A cross-site page can't add that header
     without CORS approval, and we never grant it.
   - for any method other than GET/HEAD, carry an `Origin` header exactly
     equal to the public origin. A missing `Origin` on an unsafe method is
     rejected.

   A failure is a 403 problem+json titled "cross-site request rejected".
   The audit actor is `identity.Actor(principal)`, the same as for a
   bearer token.
7. **Redirect URIs on the `serendip` client** (provider-specific, in
   `providers/identity/thunderid`). **Superseded in part by "Decision 7, as
   amended" (below, after T010):** `serendipd` only checks read-only; the
   management-API update is an operator command
   (`cmd/serendip-identity-redirects`). The original text follows.
   - New installs get `<public>/auth/callback` and `<public>/` in the
     bootstrap client document, alongside the loopback URI.
   - Existing installs: `EnsureClientRedirects` runs in identity setup and
     in the foundations loop. Spike finding 1 confirmed the bootstrap
     client is editable, so decision 7b isn't needed.
     - It obtains a management token by driving ThunderID's login flow as
       the `CONSOLE` client (`scope=openid system`,
       `resource=https://localhost:8090/mcp`) with the admin credential
       `serendipd` already owns in its Secret. It calls the management API
       through the Service proxy.
     - It GETs the application, adds any missing URI, and PUTs it back
       only on drift. A no-drift pass is reads only.
     - It never removes a URI it didn't add.
8. **Security headers on every console and `/auth` response:**
   - `Content-Security-Policy: default-src 'self'; script-src 'self';
     style-src 'self'; img-src 'self'; font-src 'self';
     connect-src 'self'; form-action 'self';
     frame-ancestors 'none'; base-uri 'none'; object-src 'none'`
     (as built: no `data:` images, and `form-action 'self'` only, since
     login is a redirect, not a form post)
     - **No `'unsafe-inline'` and no `'unsafe-eval'`.** Vite's production
       output works under this; T005 checks for zero violations.
     - **`connect-src 'self'`** means an injected script can't send data to
       a third-party origin with `fetch`.
   - `X-Content-Type-Options: nosniff`
   - `Referrer-Policy: no-referrer`
   - `Permissions-Policy: camera=(), microphone=(), geolocation=()`
   - `Cross-Origin-Opener-Policy: same-origin`
   - `X-Frame-Options: DENY` (added in T005)
   - `/v1` and `/auth` responses also get `Cache-Control: no-store`.
9. **API hardening** (`api/server.go`, `cmd/serendipd`):
   - **Server timeouts:** `ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s,
     `WriteTimeout` 60 s, `IdleTimeout` 120 s, `MaxHeaderBytes` 64 KiB.
   - **Body limit:** `http.MaxBytesReader` at 1 MiB on `/v1`. Exceeding it
     gives a 413 problem+json.
   - **Rate limit on `/auth/login` and `/auth/callback`:** a token bucket
     per remote IP, 10/min with a burst of 20. It's held in a capped map
     and gives a 429 with `Retry-After`. `/v1` isn't rate limited in this
     spec: every caller there is already authenticated, and the cost is
     bounded by timeouts and body limits.
10. **UI stack (ADR 0012):**
    - **TypeScript (strict), React 19, Vite.**
    - **Package manager:** pnpm, pinned through `packageManager` in
      `package.json` (with its integrity hash) and enabled with corepack.
      - It needs a version with `minimumReleaseAge` (10.16+). The
        installed 10.15.1 is too old, so T002 pins the newest 10.x that is
        at least 14 days old.
    - **Runtime dependencies:** only `react` and `react-dom`.
      - Routing is a ~60-line `history`-based router.
      - Data comes from a typed `fetch` wrapper plus a `usePoll` hook.
      - Icons are ~10 inline SVG components with paths copied from Lucide
        (ISC licence, attributed in `ui/THIRD_PARTY.md`).
      - Dialogs use the native `<dialog>`.
    - **Dev dependencies:**
      - `vite` and `@vitejs/plugin-react`;
      - `typescript`;
      - `vitest`, `@testing-library/react` and `jsdom`;
      - `@fontsource/ibm-plex-sans` and `@fontsource/ibm-plex-mono`, which
        are font files only. They're bundled into `dist`, so no font comes
        from a remote origin at runtime.
      - No ESLint in this spec: strict `tsc` covers the type level, and
        lint rules can come later.
    - **`.npmrc`:**
      - `ignore-scripts=true`;
      - `strict-peer-dependencies=true`;
      - `save-exact=true`;
      - `registry=https://registry.npmjs.org/`.
    - **`pnpm-workspace.yaml`** (as built, T002):
      - `minimumReleaseAge: 10080` (7 days);
      - `onlyBuiltDependencies: []` and `strictDepBuilds: true`: Vite 8's
        native bindings come as per-platform optional packages, so nothing
        needs an install script.
    - **Install hygiene.** Run installs with no credentials in the
      environment: no `GITHUB_TOKEN` and no `NPM_TOKEN`. That's what the
      Shai-Hulud worm harvested. `ui/CLAUDE.md` makes this a rule, and the
      `ui` build script refuses to run if either variable is set.
    - **Verify:**
      - `pnpm install --frozen-lockfile`;
      - `pnpm audit --audit-level high`;
      - `tsc --noEmit`;
      - `vitest run`;
      - `vite build`, plus the bundle size check.
    - **Bundle budget:** ≤ 160 KB of gzipped JS for the initial load.
11. **Design direction "Combined"** (user's pick, 2026-10-17; canvas
    https://claude.ai/artifact/DjRkfV2nLaoBn6sK3UtK57). It's direction A's
    structure and density, plus Impeccable's Operate-mode rules (a
    committed dark nav rail; one signature element, the reconciliation
    path; mono only for real data), with tells removed per
    frontend-design/unslop/avoid-ai-design (no uppercase section labels,
    no "·" separators, drawn SVG icons only, sentence case) and Vercel's
    Web Interface Guidelines (skip link, `aria-live` refresh status,
    tabular numbers, `:focus-visible`). The reviewed rule text is vendored
    as markdown in `ui/design-rules/`, pinned to the commits read; no skill
    launcher, scanner or live fetch is used. Light-first with a dark theme:
    - **Navigation:** a dark left rail that grows with the capabilities
      (Secrets and Volumes come next, labelled "not available yet").
    - **Signature element:** the reconciliation path (spec accepted →
      workload running → route serving → ready) on resource detail pages.
    - **Data:** dense tables for Conditions; IBM Plex Sans for UI and Plex
      Mono for identifiers, images and API text.
    - **Colour:** a single blue accent; status colour always paired with an
      icon and a word.
    - **Topology:** a topology banner when the install is single-node.

    `ui/DESIGN.md` records the tokens and components as built; it, not
    this list, is authoritative. The planned list (AppShell, Nav, Button,
    PortEditor, EnvEditor, ConfirmDialog, an `info` colour role) was
    narrowed while building. The built set is Shell, PageHeader,
    StatusText, ReadyStatus, ReconciliationPath, ConditionsTable,
    KeyValueList, SafeLink, ApiEquivalent, TextField, Banner, Notice,
    EmptyState, LoadingState, ErrorState and Icon.
12. **Polling.**
    - **Interval:** 5 s on a detail page and 15 s on a list. It pauses
      while `document.hidden` and resumes immediately on focus.
    - **Errors:** exponential back-off to 60 s on errors.
    - **Cancellation:** an `AbortController` cancels the in-flight request
      on navigation.
    - **Concurrency:** a page's independent requests run concurrently,
      never serially.
    - **Timeout:** each request has a 15 s timeout.
13. **Topology honesty on Overview.** The page uses `GET /v1/nodes`.
    - **Single node:** with one (or zero) Node resources, it says "Single
      node — no node-failure protection". It never says more nodes means
      HA; the API can't assert HA yet.
    - **Readiness:** each capability's state comes from `/v1/readiness`.
14. **Error contract the UI implements:**

    | Response | What the UI does |
    |---|---|
    | 401 | Session gone: sign-in prompt, keeps the current path as `return` |
    | 403 | Shows the problem's title and detail; reserved for future RBAC |
    | 413, 400, 409, 422 | Shown inline on the form, with the request ID |
    | 429 | Waits for `Retry-After` |
    | 503 | "Identity provider not ready" or "capability not ready" banner, retries with back-off |
    | Network error or timeout | "Can't reach Serendip" state, retries with back-off |
    | Aborted | Ignored |

## Performance assessment (targets; T009 measures)

| What | Expected | Target | How measured |
|---|---|---|---|
| Cookie-path auth overhead vs API key | a map lookup + SHA-256 of 32 bytes (≈ µs) | p99 < 50 µs added | Go benchmark in `api` |
| Token refresh | one Service-proxy round trip | ≤ 1 per session per ~59 min; p95 < 300 ms | timing log in T008 |
| Polling load | 3 tabs on a Component detail ≈ 0.6 GET/s | `serendipd` stays within constitution 4 (< 5% CPU idle on the simulated T470) | spec 013 harness, with and without 3 polling tabs |
| `serendipd` RSS with UI embedded | + bundle size (~0.5 MB) | still < 256 MB combined | spec 013 harness |
| Initial JS | React ≈ 60 KB gz + app | ≤ 160 KB gz | build-time check |

The "Expected" column is an estimate, labelled as such. Only the "How
measured" column produces numbers we report.

Not applicable yet: streaming, WebSockets, large downloads, logs. No such
endpoint exists. Running several instances is a documented ADR 0013
trigger.

## Threat model

| # | Threat | Attack path | Controls (new in **bold**) | Test |
|---|---|---|---|---|
| 1 | Token theft by injected script | XSS or malicious package reads tokens | **Tokens never leave serendipd; HttpOnly cookie** | T003 unit: no token in any response; T010 browser: storage and JS-readable cookies empty |
| 2 | Session abuse by injected script | script calls `/v1` with the live cookie | **CSP (no inline, `connect-src 'self'`), 2 runtime deps, no raw HTML render, 30-min idle**; delete not offered (rule 5 gap) | T005 header test; T010 zero CSP violations; `grep` gate for `dangerouslySetInnerHTML`/`innerHTML` |
| 3 | Malicious dependency at install | install script steals env tokens (Shai-Hulud) | **ignore-scripts, release-age ≥ 7 d, frozen lockfile, exact pins, no creds in env during install, audit** | T002 checks `.npmrc` + build script guard; `pnpm audit` |
| 4 | Compromised build output | tampered dist shipped | dist built from the lockfile; reviewed diff of `pnpm-lock.yaml` on every dep change | process (ui/CLAUDE.md) |
| 5 | CSRF | cross-site form/fetch with cookie | **Strict cookie + required custom header + exact Origin on unsafe methods; no CORS** | T004 unit: missing header → 403, wrong/missing Origin → 403 |
| 6 | Login CSRF / callback replay | attacker's code/state into victim browser; replayed callback | **state bound to pre-login cookie, single-use, 10-min TTL; nonce checked** | T003 unit: bad state, missing cookie, reused state, wrong nonce, sub mismatch |
| 7 | Session fixation | attacker-chosen session id | **id generated server-side at login only; unknown ids ignored** | T003 unit |
| 8 | Open redirect | `return=` or logout redirect to attacker site | **path-only `return` validation; post-logout URI fixed** | T003 table test (`//evil`, `/\evil`, `https://evil`, `%2F%2F`) |
| 9 | Token/session leakage via logs, URLs, errors | logging request URLs/cookies | **ids hashed; no token logged; error page shows OAuth error code only** | T003: log capture asserts no secrets |
| 10 | Removed user keeps access | refresh token still valid | **refresh on access-token expiry re-checks provider (deleted user → `invalid_grant`, Spike 5); 12 h absolute** | T010: delete a user, force a refresh, get a 401 |
| 11 | Cache leakage | authenticated JSON cached by browser/proxy | **`no-store` on /v1, /auth** | T005 header test |
| 12 | Clickjacking | console framed | **`frame-ancestors 'none'`** | T005 |
| 13 | Oversized payloads / slow clients | large body, slowloris | **1 MiB body limit, server timeouts, header cap** | T004 unit (413); T008 slow-header client is cut off |
| 14 | Login endpoint flooding | fill pending table / burn CPU | **pending cap + TTL, per-IP rate limit** | T003 unit (cap eviction, 429) |
| 15 | Cookie over network in clear | remote http origin | **non-localhost http origin refused unless opted in** | T006 unit |
| 16 | Host confusion | `127.0.0.1` vs `localhost`, Host-header tricks | **redirect to configured origin; Origin compared to config, never to Host** | T004/T006 unit |
| — | SSRF / proxy routing | — | N/A: no proxying; upstream URLs from config/discovery only | recorded |
| — | Prompt injection | — | N/A: no AI in this spec | recorded |
| — | Cross-tenant access / IDOR | — | Not testable: no authorization layer (spec gap 3); every account is admin, documented in UI "About" + docs | recorded |

## Interfaces and types created

- `core/identity`: a `LoginClient` interface, provider-neutral:
  ```go
  type LoginClient interface {
      AuthCodeURL(ctx context.Context, state, nonce, codeChallenge string) (string, error)
      Exchange(ctx context.Context, code, codeVerifier string) (Tokens, error)
      Refresh(ctx context.Context, refreshToken string) (Tokens, error)
      Revoke(ctx context.Context, refreshToken string) error   // ErrUnsupported if no endpoint
      EndSessionURL(ctx context.Context, idTokenHint string) (string, error) // ErrUnsupported if none
      VerifyIDToken(ctx context.Context, raw, nonce string) (subject string, err error)
  }
  type Tokens struct { AccessToken, RefreshToken, IDToken string; Expiry time.Time }
  ```
  No product names, so constitution 1 holds. (Amended in T003: discovery
  is lazy and can fail, so `AuthCodeURL` and `EndSessionURL` take a context
  and return an error. Status codes 408, 429 and 5xx count as "provider not
  ready", never as a refused credential, so a rate limit can't sign people
  out.)
- `providers/identity/oidc`: `RelyingParty` implements `LoginClient` from
  discovery plus overrides (a back-channel base URL for the Service proxy).
  It reuses the existing JWS/JWK code for the ID token, with no new
  dependency.
- `api`:
  - `session.go`: the session table;
  - `console.go`: `Console` (pending logins, `/auth/*`, cookie auth,
    refresh, `GET /v1/session`);
  - `hardening.go`: limits, security headers, rate limiter;
  - `AuthMiddleware` keeps its signature; an unexported variant adds the
    console;
  - `NewServer` gains a final `console *Console` parameter, whose `Login`
    is a lazy func (like `authn`), so the API is up before identity is.
- `providers/identity/thunderid`: `EnsureClientRedirects` (read-only
  check), `RegisterConsoleRedirects` (the operator command's call),
  `ProxyBaseURL`.
- `cmd/serendip-identity-redirects`: the operator command.
- `cmd/serendipd/console`: the embedded console.
- `ui/`: the Vite app, the `go.mod` fence, `DESIGN.md` and `THIRD_PARTY.md`.

## Files touched

- `core/identity/identity.go` (+ test)
- `providers/identity/oidc/rp.go` (new, + test)
- `providers/identity/thunderid/{values.go,deploy.go,clients.go (new)}` (+ test)
- `api/{server.go,middleware.go,session.go (new),console.go (new),hardening.go (new)}` (+ tests)
- `cmd/serendipd/{main.go,wiring.go}`: public URL config, lazy login
  client, console wiring; `cmd/serendipd/console/` (new),
  `cmd/serendip-identity-redirects/` (new)
- `ui/**` (new app), `ui/CLAUDE.md`, `.gitignore` (`!cmd/serendipd/console/dist/.gitkeep`)
- `docs/decisions/0012-ui-toolchain-and-dependencies.md`,
  `docs/decisions/0013-console-session-topology.md`
- `docs/architecture/identity.md` (Console session section), `api/CLAUDE.md`
- `profiles/edge-1/bench/` (polling-load scenario), `docs/PROGRESS.md`

## Risks

- **Strict cookie after a cross-site callback: resolved** (Spike finding
  6). Chrome withholds the cookie on the redirected document request, as
  expected, and sends it on the first same-origin `fetch`. The console
  must therefore never need the session to render its HTML shell. A test
  in T004 checks that `GET /` succeeds without a cookie.
- **Scripting ThunderID's login flow for the management token**
  (decision 7). The flow API (`/flow/execute`, `action_001`) is
  ThunderID's own UI contract and could change in a minor release. It's
  confined to `providers/identity/thunderid`, covered by T010 on a real
  install, and pinned to 1.0.1 like the chart. If a future version breaks
  it, the console redirect URI can still be added by hand, and the
  readiness reason will say so.
- **CSP vs Vite output.** Vite 6+ emits no inline scripts in production
  builds, but `@vitejs/plugin-react`'s dev preamble is inline. The CSP
  applies only to the embedded production build, never to `vite dev`,
  which proxies `/v1` and `/auth` to `serendipd`.
- **pnpm upgrade.** The installed 10.15.1 lacks `minimumReleaseAge`. If the
  pinned release lacks it too, T002 falls back to npm's `--before` date
  pin in the install script and records this in the ADR.
- **The refresh-token lifetime might be shorter than the 30-min idle
  window.** In that case sessions just end sooner, and the docs state the
  real numbers.

- **Accepted after go-reviewer (T004):**
  - A session that expires or is evicted is dropped without revoking its
    refresh token. Only `serendipd` ever held that token, and ThunderID
    expires it within 24 h.
  - The callback's three provider calls share one 10 s budget.
  - The canonical-host redirect would loop behind a proxy that rewrites
    `Host`. Nothing fronts `serendipd` today; the HTTPS spec must revisit
    this.
  - A distributed flood can evict legitimate pending logins. The cap and
    the per-address limit bound the damage.
- **Accepted after contract-guardian (T004/T007):**
  - Console sign-in and sign-out are logged with the actor, but they
    aren't audit records. Rule 8 covers mutations, and these are session
    events. An audit event for them belongs in a later spec, alongside
    the audit read API (spec gap 2).
  - `EnsureClientRedirects` changes the identity provider's configuration
    imperatively. That makes it a foundations step, like `EnsureRoute`:
    the sanctioned exception to "desired state through reconciliation"
    for the platform's own plumbing. It uses only the admin credential
    `serendipd` already holds, grants nothing new, never removes a URI,
    and has tests asserting no password or token reaches an error.
- **Logout carries no `id_token_hint`** (amended in T004). ThunderID
  accepts `client_id` alone, and this keeps the ID token out of a URL the
  browser navigates to.

## Explicitly NOT done

- delete in the UI (no approval object, rule 5);
- MCP, CLI, RBAC;
- secrets screens;
- streaming;
- HTTPS;
- a persistent or shared session store;
- ESLint;
- Simple/Advanced/Expert modes;
- i18n.

## Upstream facts (oss-researcher, 2026-10-17)

Read from tag v1.0.1 source (`backend/internal/…`), through summaries
rather than line-by-line reading. Every item marked *unverified* below was
settled by T001 on a live install; see "Spike findings", which take
precedence where they differ.

1. **Refresh tokens.** One is issued when the app allows `refresh_token`,
   with no public/confidential check (`oauth2/token/service.go`).
   - **Lifetime.** Resolved per app through
     `token.refreshToken.validityPeriod`. The global default is
     *unverified*.
   - **Rotation.** It happens only when `RenewOnGrant` is on (the default
     is *unverified*). A rotated token keeps the original family expiry,
     so there's an absolute cap.
   - **Reuse.** A reused token gets `invalid_grant`. Family revocation on
     reuse is config-gated.
   - **Effect on us.** Decision 5 handles both rotated and fixed refresh
     tokens.
2. **Revocation.** `POST /oauth2/revoke` (RFC 7009) accepts a public client
   by `client_id` alone. It's in discovery only when enabled.
   - **Risk (unverified).** The ownership check falls back to `sub` for
     refresh tokens, which carry no `client_id`, and may reject the
     revocation with `invalid_grant`.
   - **Effect on us.** Revocation stays best effort, and its result is
     logged without the token.
3. **Logout.** `/oauth2/logout` (`end_session_endpoint`, also present only
   when enabled) accepts `id_token_hint`, `client_id`,
   `post_logout_redirect_uri` and `state`.
   - **Registration.** The post-logout URI must be registered.
   - **Flow.** It runs through ThunderID's own sign-out page. So the
     browser must make a **top-level navigation** to it; a fetch won't
     work. Decision 3's `{location}` response does exactly that.
4. **The ID token** carries `nonce` when one was sent, and `auth_time`. It
   has no `sid`, so there's no back-channel logout to build on.
5. **Management API.**
   - **Routes:** `GET`/`PUT /applications/{id}`.
   - **`PUT` replaces the whole document.** Omitted fields are zeroed, and
     an omitted `clientId` means a new one is generated. So the call
     always sends the full fetched document back with the change.
   - **Declarative apps are rejected** (`ErrorCannotModifyDeclarativeResource`).
     **This is the biggest risk:** if bootstrap-imported apps count as
     declarative, an existing install's `serendip` client can't be changed
     through the API. T001 tests it first. The fallback (decision 7b)
     follows.
   - **Required scope:** *unverified*; spec 015's spike used `system`.
6. **User state on refresh.** Refresh fails for a deleted user or a
   credential changed after issue, and scopes are re-checked against
   current roles. *Unverified* for a **disabled** user (T001).
7. **No release after v1.0.1** (2026-08-25). The ADR 0006 watch date of
   ~2026-11-15 stands.

**Decision 7b: dropped.** It was the fallback in case the bootstrap
client was read-only, and Spike finding 1 showed it isn't.

## T010 finding: the Service proxy strips `Authorization` (2026-10-17)

On a real install, `EnsureClientRedirects` got **401** from
`GET /applications/{id}`. The Kubernetes API server's Service proxy
removes the `Authorization` header before forwarding, so ThunderID never
saw the management token. The T001 spike missed this because it used
`kubectl port-forward`. Calls that need no `Authorization` header are
unaffected: discovery, JWKS, token, refresh and revocation (which sends
`client_id` in the form).

**Decision 7, as amended:**
1. **Fresh installs** get the console URIs in the bootstrap client
   document (`Deployer.SetConsoleRedirects`), which was the plan's
   original first half.
2. **Every pass first checks, read-only.** It probes the public authorize
   and logout endpoints with no credentials: a registered callback is
   redirected to `/gate/signin`, an unregistered one to `/gate/error`;
   post-logout gives 302 or 400. When both are accepted, nothing else
   happens, and no admin login takes place.
3. **Changing URIs on an existing install** (an install that predates the
   console, or a changed `SERENDIP_PUBLIC_URL`) is a documented one-time
   operator step (option B, recommended to the user 2026-10-18): a port
   forward plus one management call, written up in `identity.md`
   "Console sessions". `serendipd` no longer attempts the management API.
   Through the proxy it can't succeed, and the foundations loop would
   repeat an admin login every pass for nothing (contract-guardian, T010).
   It logs the URIs and points at the procedure. The tested
   `ensureRedirects` stays for option A (an in-process tunnel that keeps
   headers), likely revisited in the HTTPS spec, which changes the public
   URL anyway.

## Spike findings (T001, 2026-10-17)

**Setup.**
- A fresh kind cluster (`kindest/node:v1.34.0`, `kind-t470.yaml` port
  mapping, no CPU limits).
- `serendipd` at `b82af3d`. It set up the full stack: identity was active
  2 m 21 s after start.
- A scratch Python driver ran the same login flow as spec 015 (authorize →
  `/flow/execute` ×2 → `/oauth2/auth/callback` → token). The management
  API was reached through `kubectl port-forward`.
- The admin password was read from the Secret into an env var and never
  printed. No token was printed.
- The scratch cluster was deleted afterwards.

**Server config** (`/opt/thunderid/deployment.yaml` in the pod):
- `declarative_resources.enabled: false`;
- `oauth.refresh_token`: `renew_on_grant: true`,
  `revoke_previous_on_renew: true`, `validity_period: 86400`.

1. **The bootstrap client is editable.** The steps were: a
   management token via `CONSOLE` (`scope=openid system` +
   `resource=https://localhost:8090/mcp`), then `GET
   /applications/01900000-…-05e1` → 200, then a `PUT` of the full
   document with the two console URIs appended → **200**.
   - **Afterwards:**
     - `redirectUris` = `[loopback, http://localhost:8080/auth/callback]`;
     - `postLogoutRedirectUris` = `[loopback, http://localhost:8080/]`;
     - `clientId` is still `serendip`;
     - `grantTypes`, `pkceRequired`, `publicClient` and `token` are
       unchanged.
   - **Login after the change:** login through the new callback works,
     and login through the original loopback still works.
   - **Documents in the PUT:** the app document's top-level keys are
     `allowedUserTypes, assertion, authFlowId, clientId, description, id,
     inboundAuthConfig, isRecoveryFlowEnabled, isRegistrationFlowEnabled,
     loginConsent, name, ouId, type`. All of them are sent back in the
     PUT.
2. **Discovery advertises `revocation_endpoint`
   (`/oauth2/revoke`) and `end_session_endpoint` (`/oauth2/logout`)** by
   default.
3. **Tokens and refresh:**
   - **The token response** contains `access_token`, `expires_in` (3600),
     `id_token`, `refresh_token`, `scope` and `token_type`.
   - **The ID token** has `nonce` equal to the one sent, and `auth_time`,
     but no `sid`.
   - **The refresh token** is a JWT with `exp − iat = 86400`.
   - **Rotation:** refresh #1 → 200 with a **new** refresh token.
   - **Reuse:** presenting the old one again → 400 `invalid_grant`
     ("Invalid refresh token"). After that, the *new* one also fails with
     `invalid_grant`: **reuse revokes the family.**
4. **Revocation works for our public client.** `POST /oauth2/revoke` with
   `token`, `token_type_hint=refresh_token` and `client_id=serendip` →
   200 with an empty body. A refresh with that token then gives 400
   `invalid_grant`. The ownership-check worry (Upstream fact 2) didn't
   happen.
5. **Users:**
   - **No "disabled" state exists.** User objects have `attributes, id,
     isReadOnly, ouId, type`. Attributes include `email, family_name,
     given_name, mobile_number, name, sub, username`; there's no
     enabled/locked field. A string search of the server binary for
     account-disabled/locked found none.
   - **Deleting a user works.** Create user → login → `DELETE
     /users/{id}` → 204 → refresh → 400 `invalid_grant`.
   - **Passwords** can't be changed through `PUT /users/{id}` (USR-1028
     "credential update not allowed"). That's a separate credentials
     route, not needed here.
6. **Strict cookie after a cross-site return.** A scratch Go server
   played Serendip on `localhost:8081`, and a fake identity provider on
   `127.0.0.1:9091` served a page that navigated by script, like
   ThunderID's sign-in page, through a 302 to `/auth/callback`. That
   handler set an `HttpOnly; SameSite=Strict` cookie and answered 303 to
   `/`. Headless Chrome, server log:
   - the callback: `Sec-Fetch-Site=cross-site`;
   - **document `GET /`: cookie not sent** (`cross-site`);
   - **the page's first `fetch('/whoami')`: cookie sent**
     (`same-origin`).

   The same sequence appeared a second time in the server log during the
   Edge run, but Edge's DOM dump printed nothing, so the Edge result rests
   on the server log alone. A real browser repeats this in T010.
7. **Logout:** `GET /oauth2/logout` with `id_token_hint`,
   `post_logout_redirect_uri=http://localhost:8080/`, `client_id` and
   `state` → 302 to `/gate/signout?applicationId&executionId&logoutId`.
   - An unregistered URI (`http://evil.example/`) → 400 `invalid
     post_logout_redirect_uri`.
   - A path under the registered one (`http://localhost:8080/x`) → **400**,
     so matching is exact.
