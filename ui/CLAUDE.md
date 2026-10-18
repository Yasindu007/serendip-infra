# ui/ — Serendip Console

React + Vite + TypeScript (strict). Spec: `specs/016-ui-mvp/`. Design
rules: `ui/DESIGN.md`. Dependency decisions: ADR 0012.

## Rules

- Pure API client; no privileged calls, no logic the API does not expose
  (constitution 7). Every form shows the API request it sends.
- Honest capability display (constitution 6): single-node installs say so;
  unbuilt features are labelled "not available yet", never mocked. No fake
  metrics, charts or activity.
- No delete action anywhere until approval objects exist (constitution 5).
  One exception, by the user's decision (spec 017 T008): removing a role
  assignment. It revokes access rather than destroying anything, is
  guarded by RBAC (`roleAssignments/delete`) and the last-Owner rule, and
  is confirmed first.
- Never render API data as HTML (`dangerouslySetInnerHTML`, `innerHTML`):
  text only. Links from API data only for `http:`/`https:` URLs.
- No token, session ID or secret value in JS, storage, URLs or logs. The
  browser holds only the HttpOnly session cookie; every authenticated call
  is a same-origin `fetch` with the `X-Serendip-Csrf: 1` header.
- The production build runs under a strict CSP (`script-src 'self'`, no
  inline scripts or styles, `connect-src 'self'`). Nothing loads from a
  third-party origin: fonts are bundled, no CDN, no analytics.
- One design system: tokens and shared components only (`ui/DESIGN.md`).
  No one-off colours, sizes or components in screens.

## Dependencies (supply chain)

- Runtime dependencies are only `react` and `react-dom`. Adding any
  package (runtime or dev) needs an ADR 0012 update first: what it buys,
  maintainers, licence, transitive count.
- Versions are exact and at least 14 days old when added; pnpm also
  refuses anything younger than 7 days (`minimumReleaseAge`).
- No dependency may run an install or build script (`ignore-scripts`,
  empty `onlyBuiltDependencies`, `strictDepBuilds`).
- Review the `pnpm-lock.yaml` diff on every dependency change.

## Install hygiene

Run installs and builds with **no credentials in the environment**: no
`GITHUB_TOKEN`, `GH_TOKEN`, `NPM_TOKEN` or `NODE_AUTH_TOKEN`. The
`guard` script refuses to run otherwise. pnpm comes from corepack, pinned
by hash in `package.json` (`packageManager`); nothing is installed globally.

## Commands (from `ui/`)

```
corepack pnpm install --frozen-lockfile   # never a plain install in CI/verify
corepack pnpm audit --audit-level high
corepack pnpm typecheck
corepack pnpm test
corepack pnpm build    # writes ../cmd/serendipd/console/dist/app, checks the JS budget (160 KiB gz)
corepack pnpm dev      # vite dev server, proxies /v1 and /auth to serendipd on :8080
```

After `build`, rebuild `serendipd` to embed the new console. Without a
build, `serendipd` serves a "console not built" page and the API works.

`ui/go.mod` only fences this directory off from the root Go module, so Go
tooling never walks `node_modules`. Don't put Go code here.
