# Spec 016: UI MVP (Serendip Console)

Status: Complete 2026-10-18 — evidence in `verification.md` (approved 2026-10-17; shipping without RBAC confirmed by the user)

## Problem

Everything built so far (specs 001-015) is reachable only through the HTTP
API. Spec 015 gave people a real login (ThunderID over OIDC), which was the
missing prerequisite for a browser console (`overview.md` "Experience": Web
UI). This spec builds the first slice of that console in `ui/` (React +
Vite + TypeScript, per CLAUDE.md and `ui/CLAUDE.md`).

It should look and feel like a serious infrastructure product: minimal,
dense enough for operators, readable for developers who don't know
Kubernetes. And because the browser is the most exposed client Serendip
has, it is designed against current supply-chain attacks, such as the npm
worms (Shai-Hulud) that ran install scripts to steal tokens. Security is a
requirement here, not polish.

## Interface parity (constitution 7) — how this spec applies it

Rule 7 already says UI = CLI = API = MCP. For this spec that means:

- **The UI can do nothing the API can't.** Every screen action is one
  documented API call. No UI-only endpoint performs a resource operation.
  The only new server routes are for browser login sessions (below), which
  aren't resource operations.
- **Parity is visible.** Every form that changes something shows the exact
  API request it will send (method, path, JSON body; never a credential),
  so a person, a script, or an AI can repeat it.
- **AI has the same capabilities, not the same authority.** Through MCP
  (a later spec), an AI can express anything a human can. Rule 5 still
  holds: destructive or infrastructure-changing actions need an approval
  object, and the AI never holds the authority to approve on its own. This
  spec builds nothing AI-specific and doesn't pretend AI features exist.

## Scope — IN

- **The console served by `serendipd` on its own origin.** The built
  static UI is embedded in the `serendipd` binary and served on the
  existing listener. One origin means no CORS on either `serendipd` or
  ThunderID, and Node.js is needed only at build time, never at runtime.
  `go build ./...` still works on a machine without Node (a placeholder is
  embedded when the UI hasn't been built).
- **Browser login, with tokens kept away from page JavaScript (BFF).**
  - **Topology.** `serendipd` *is* the API, so the BFF isn't a proxy in
    front of other services. A cookie session is resolved in the same auth
    middleware as bearer tokens and API keys, into the same actor. So
    there's no forwarding, no downstream token and no proxy-routing
    surface.
  - **Login flow.** `serendipd` runs the OIDC flow server side against the
    configured issuer, through the existing `serendip` client. It uses
    authorization code + PKCE, a single-use `state`, and an ID-token
    `nonce`. Pending logins are capped in number and lifetime, so
    unauthenticated callers can't fill memory with them.
  - **What the browser holds.** Only an opaque, high-entropy session ID in
    an `HttpOnly`, `SameSite=Strict`, host-only, `Path=/` cookie (`Secure`
    once HTTPS exists).
  - **Where tokens live.** Access and refresh tokens stay inside
    `serendipd`. They never reach page JavaScript, browser storage, URLs,
    logs, or error responses. Session IDs are never logged or stored in
    plain form.
  - **Session lifecycle:**
    - a new session ID is issued at login (no fixation);
    - idle and absolute expiry, with values fixed in plan.md;
    - server-side refresh that runs at most once per session at a time, so
      concurrent requests can't race each other's tokens;
    - a failed refresh ends the session. Someone disabled at the identity
      provider loses console access within a bounded, documented time.
  - **Logout** ends the server-side session, revokes the refresh token
    where the provider supports it, and ends the provider session.
  - **CSRF.** Requests authenticated by cookie are protected beyond
    `SameSite=Strict`: state-changing requests must pass an `Origin` check
    against the configured origin *and* carry a custom header that the
    browser won't send cross-site. CORS stays off: no
    `Access-Control-Allow-*` headers are ever sent.
  - **The audit actor** for a cookie request is the same person-form actor
    spec 015 records for a bearer token.
  - Bearer tokens and static API keys keep working unchanged.
- **API hardening the browser client needs.** All of these are missing
  today (verified in `api/` and `cmd/serendipd`):
  - request body size limits;
  - server read-header, read, write and idle timeouts;
  - `Cache-Control: no-store` on `/v1` and session responses;
  - simple per-client rate limiting on the login routes.
- **The console's redirect URI is registered on the `serendip` client.**
  It's added idempotently through ThunderID's management API, on new
  installs and on installs that already exist (plan.md Spike finding 10 of
  spec 015). Bootstrap files only import on first install, so editing them
  isn't enough.
- **Screens (the first workflow, end to end):**
  1. **Sign-in / signed-out**, with an honest error when the Identity
     provider isn't ready.
  2. **Overview:** `/v1/readiness` per capability, with reasons. The
     install's topology is stated plainly (constitution 6), e.g. "single
     node, no node-failure protection".
  3. **Applications:** list, create, detail.
  4. **Components** inside an Application:
     - create and edit the image, ports, which ports are exposed, and env;
     - detail view with Conditions (type, status, reason, message, last
       transition), the external endpoints once they're actually serving,
       and the relationships.
  5. **Empty, loading, error, not-ready, and session-expired** states for
     every screen above. API errors (problem+json) are shown with their
     request ID, never with stack traces.
- **Live-enough status by polling.** The API has no watch or stream today,
  so the UI polls the resources on screen and says when it last refreshed.
  Polling backs off when the tab is hidden.
- **A design system, recorded in `ui/DESIGN.md`.**
  - Three visual directions are presented for choice during plan.md, then
    one is recorded as tokens (type, spacing, colour, status semantics)
    and a small set of shared components.
  - Light and dark themes.
  - WCAG 2.2 AA contrast; full keyboard operation with visible focus.
  - Status is never shown by colour alone.
  - No fabricated metrics, charts or activity. Anything not built is
    labelled "not available yet", never mocked.
- **Supply-chain and frontend security controls (ADR 0012):**
  - **Tool and package choices:** the build toolchain, package manager and
    every runtime dependency are justified in the ADR. Runtime
    dependencies are minimal: React and React DOM, plus anything the ADR
    explicitly justifies. No CSS framework, component kit or data-fetching
    library by default.
  - **How dependencies are installed:**
    - an exact-pinned lockfile;
    - dependency install scripts disabled;
    - a minimum release age before a new version can be installed;
    - a frozen-lockfile install in verification;
    - a dependency audit as part of Verify.
  - **What the browser loads at runtime:** no third-party origins (no CDN,
    remote fonts, analytics or telemetry).
  - **Response headers:** UI responses carry a strict Content-Security-Policy
    (no inline script, no `eval`) and `frame-ancestors 'none'`, plus
    `nosniff`, a referrer policy and a permissions policy.
  - **Safe rendering:** no raw-HTML rendering of API data. Values are
    shown as text, and links from API data (e.g. endpoint URLs) are allowed
    only for `http`/`https`.
- **Footprint.**
  - The JS bundle has a size budget (number fixed in plan.md).
  - `serendipd` idle RSS is re-measured with the UI embedded (constitution
    4, spec 013 harness).
- **Docs:**
  - `ui/DESIGN.md`;
  - ADR 0012;
  - `ui/CLAUDE.md` updated with build, test and security rules;
  - a short "Console" section in `overview.md` or `identity.md` covering
    the session model.

## Pre-UI gate (plan.md deliverables)

These are delivered in plan.md and approved before any screen that depends
on login is built. Design exploration (directions, tokens) may go ahead in
parallel.

1. **Trust-boundary diagram**: browser, `serendipd` (API + BFF), ThunderID,
   the session store, the cluster providers.
2. **ADR 0013, session topology.** It compares three options:
   - a BFF inside `serendipd` (chosen);
   - the SPA holding tokens in memory (needs CORS on ThunderID; tokens
     reachable by any script);
   - a separate BFF proxy process (an extra hop and an extra component on
     a T470).
   It also decides the session store. A single `serendipd` instance is the
   current topology, so it states what changes when there are several.
3. **Threat model.** For each risk: the attack path, impact, existing
   controls, missing controls, mitigation, and a test that proves it.
   - **Covered:** XSS and malicious dependencies (HttpOnly doesn't stop
     injected script from *using* a live session, so CSP, few dependencies
     and short idle expiry are the real controls); CSRF; session fixation
     and replay; token leakage; open redirects on login/logout return
     paths; oversized payloads and slow clients; cache leakage; install-
     time scripts and build secrets.
   - **Not applicable, with the reason written down:**
     - SSRF and proxy-routing confusion: there's no proxy, and the issuer
       comes from operator config, never from a request;
     - prompt injection: no AI in this spec;
     - cross-tenant access: there's no authorization layer yet (gap 3).
4. **Performance assessment, measured not estimated:**
   - the extra latency of the cookie path versus the API-key path;
   - the cost of a token refresh;
   - the steady load polling places on `serendipd` (a few open tabs) on the
     simulated T470, against a stated target.
   Streaming, log downloads and WebSockets don't exist yet, and this is
   recorded as such.
5. **The API contract the UI uses:**
   - a session-status endpoint;
   - login, callback and logout routes;
   - a problem+json shape and UI behaviour for 401, 403 (reserved for
     authorization), 413, 429, 503 (provider not ready), and timeouts or
     cancellation.
6. **Test plan** covering:
   - callback validation (bad `state`, `nonce`, or code);
   - refresh, including concurrent refresh;
   - idle and absolute expiry;
   - logout and revocation;
   - CSRF;
   - API-key and bearer paths unchanged;
   - security headers;
   - body limits and timeouts.
7. **Open questions and assumptions needing approval.** ThunderID's
   refresh-token rotation, revocation and end-session support are
   upstream facts that oss-researcher will check, not assume.

## Scope — OUT (explicitly)

- **Delete in the UI.** The API's `DELETE` takes effect with no approval
  object today, which conflicts with constitution 5 (see "Gaps found"). The
  UI won't put a one-click path in front of that gap.
- **MCP server and CLI.** Each is its own spec. This one only makes sure
  the UI adds nothing they couldn't also do.
- **Authorization, RBAC, tenancy.** Any signed-in person still gets full
  API access (as in spec 015). Organizations, teams and projects exist as
  resources, but the UI must not imply they isolate anything.
- **Secrets screens.** A write-only secret entry flow is a natural next
  step, not part of this MVP.
- **Other resource kinds** (clusters, nodes, volumes, providers, incidents,
  alerts) beyond what the overview summarises.
- **Logs, metrics, traces, SLOs, incidents, and AI operations views.** The
  capabilities behind them aren't built.
- **Simple / Advanced / Expert modes** and raw-manifest or Helm editing.
- **HTTPS.** Plain HTTP works only for a browser on the same machine
  (`localhost`); a remote browser needs the HTTPS spec. This limit is
  documented, and the session cookie becomes `Secure` once HTTPS exists.
- **Streaming or watch APIs.** Polling is the honest choice for now.
- **Internationalisation.**

## Gaps found while writing this spec (pre-existing, not fixed here)

1. **`DELETE /v1/{kind}/{id}` takes effect with no approval object**
   (`api/handler.go`), against constitution 5 ("always require an explicit
   approval object, even from humans via API"). This needs its own spec
   (approval resource + policy hook) before any client, UI or MCP, gets a
   delete action.
2. **There's no API to read audit records.** They're written (rule 8), but
   no API returns them, so an "activity" view isn't possible yet.
3. **There's no authorization layer** (`core/policy` doesn't exist), which
   was already known from spec 015. Until it exists, every account in the
   identity provider is effectively an administrator, and the docs say so.
   Tenant isolation and 403s can't be tested yet.
4. **The HTTP server has no body size limits, no timeouts, and no
   `Cache-Control`.** This spec fixes it, because a browser client makes
   it matter.

## Affected resources and capabilities

- **Identity:** the console is a second consumer of the OIDC setup. A
  redirect URI is added to the `serendip` client, and `serendipd` gains a
  server-side session.
- **API:** new browser-session routes, and cookie authentication alongside
  bearer authentication in the same auth middleware. No new resource
  routes.
- **Application, Component:** read and written through the existing
  routes, unchanged.
- **`ui/`:** created.

## Acceptance criteria

1. On a fresh install and on an existing spec-015 install, opening
   `http://localhost:<port>/` in a browser on the same machine leads to
   ThunderID login. After login the Overview shows real readiness, and no
   token appears in `localStorage`, `sessionStorage`, cookies readable by
   JS, URLs, or `serendipd` logs.
2. A signed-in person can create an Application and a Component with an
   exposed port. They can then watch its Conditions change to Ready and
   open its external endpoint, entirely from the UI. Every mutation records
   that person as the audit actor.
3. Each form shows the equivalent API request. Sending that request with an
   API key produces the same result.
4. A cross-site POST that carries the session cookie is rejected. A
   request with an expired or ended session gets a 401, and the UI shows a
   sign-in prompt instead of breaking.
5. With the Identity provider not ready, the UI says so plainly. API-key
   clients are unaffected.
6. The CSP and other security headers are present on UI responses. The
   browser console shows no CSP violations in normal use.
7. The dependency install runs with install scripts disabled and a frozen
   lockfile, and the audit reports no known high or critical
   vulnerabilities. Every runtime dependency is listed in ADR 0012.
8. `go build ./...` and the Go tests pass on a machine without Node.
   `serendipd`'s idle RSS with the UI embedded is measured and recorded,
   and still meets constitution 4.
9. The UI was run in a real browser, with screenshots at desktop and
   narrow widths, in light and dark themes. Keyboard-only use of the
   create-Component flow works, and the contrast checks pass.
10. Delete isn't offered anywhere in the UI. Unbuilt features are labelled,
    not mocked.
11. Every threat-model row has a test that was run, or is marked
    explicitly as "not testable yet" with the reason. The session tests
    pass: bad `state` or `nonce`, a replayed callback, concurrent refresh,
    idle and absolute expiry, and logout.
12. The cookie-path latency overhead and the polling load are measured on
    the simulated T470 and recorded against their targets. Oversized
    bodies get a 413, and slow-header clients are cut off by the server
    timeouts.
