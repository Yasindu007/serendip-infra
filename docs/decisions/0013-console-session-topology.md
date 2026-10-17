# ADR 0013: Console login is a server-side session inside serendipd

Status: Accepted
Date: 2026-10-17

## Context

Spec 016's console needs a logged-in person to call the API. Spec 015 gave
the API OIDC bearer tokens from the Identity provider (ThunderID, ADR
0006), registered as a public client with PKCE. The question is where the
tokens live while someone uses the console, and who talks to the identity
provider.

Constraints:
- Malicious script in the page (XSS, or a compromised npm package) must
  not be able to steal a token (`specs/016-ui-mvp/spec.md`).
- edge-1 runs on a T470 (constitution 4); every extra process counts.
- `serendipd` is the API. There are no downstream services to call on the
  person's behalf.
- One `serendipd` instance today; no HA (as with ADR 0002).

## Options

1. **The SPA holds the tokens** (authorization code + PKCE in the
   browser, tokens in memory).
   - Pro: no server state.
   - Con: every token is readable by any script on the page, which is
     exactly the threat. ThunderID would also need CORS for the token
     endpoint, and `serendipd` would need CORS too, or the same origin.
2. **A separate BFF proxy process** in front of `serendipd`.
   - Pro: the usual pattern for a fleet of backends.
   - Con: an extra hop and an extra always-on process on a laptop, plus a
     second place to configure. It buys nothing, because there is exactly
     one backend.
3. **A BFF inside `serendipd`** (chosen).
   - `serendipd` runs the OIDC flow server side, keeps the tokens, and
     gives the browser one opaque `HttpOnly`, `SameSite=Strict` cookie.
   - The cookie resolves in the same auth middleware as API keys and
     bearer tokens, to the same `person:<name>` audit actor.
   - Pro: no proxying (so no SSRF or routing surface), same origin (so no
     CORS anywhere), and no new process.

## Decision

Option 3, as specified in `specs/016-ui-mvp/plan.md` decisions 2–9.
Highlights:

- **Session store: in memory.**
  - Only the SHA-256 of each session ID is kept.
  - Expiry: idle 30 min, absolute 12 h. The table holds at most 1024
    sessions, dropping the least recently used.
  - Refresh tokens never touch disk, because the control plane has no
    at-rest encryption key yet. The cost: restarting `serendipd` signs
    everyone out. On one instance that's acceptable, and it's documented.
- **Refresh runs at most once per session at a time.** ThunderID rotates
  refresh tokens, and replaying an old one revokes the whole family (T001
  spike finding 3). So two concurrent refreshes would end the session.
- **CSRF:**
  - `SameSite=Strict`;
  - a required `X-Serendip-Csrf` header on every cookie request;
  - an exact `Origin` match on unsafe methods;
  - no CORS headers, ever.
- **The console shell is public.** Every authenticated call is a
  same-origin `fetch`. After the cross-site return from login, the browser
  withholds a Strict cookie on the document request but sends it on the
  first `fetch` (T001 spike finding 6).
- **Removing access.** ThunderID has no disabled-user state. Deleting a
  person makes their next refresh fail, so console access ends within one
  access-token lifetime (≤ 60 min) (T001 spike finding 5).

## Revisit when

- **More than one `serendipd` instance:** sessions need a shared store
  with at-rest encryption, or sticky routing.
- **HTTPS lands:** the cookie gains `Secure`, which is automatic from an
  `https` public URL. Remote browsers stop needing the insecure-origin
  opt-in.
- **RBAC lands:** the session's principal feeds the policy layer, and 403s
  become real.
- **A second backend appears** (e.g. a separate MCP server): reconsider a
  shared session or token-exchange layer before copying this one.
