# ADR 0012: Console toolchain and dependencies, under supply-chain controls

Status: Accepted
Date: 2026-10-17

## Context

Spec 016 builds the first web console (`ui/`). CLAUDE.md already fixes
React + Vite + TypeScript, and `ui/CLAUDE.md` forbids heavy component
frameworks without an ADR. This ADR records every package the console
uses, and the controls around installing them.

The browser is Serendip's most exposed client, and npm is the most
attacked package ecosystem. The Shai-Hulud worms (2025) spread through
hijacked maintainer accounts. Their install scripts harvested `GITHUB_TOKEN`
and `NPM_TOKEN` from the environment and republished themselves. A
console that runs on a control-plane machine has to assume this will
happen again.

Facts below were taken from the npm registry and the installed tree on
2026-10-17.

## Decision

### Packages

| Package | Version | Kind | Why | Licence |
|---|---|---|---|---|
| `react`, `react-dom` | 19.3.0 | runtime | The UI library CLAUDE.md names. With `scheduler`, the whole production tree is **3 packages**. | MIT |
| `vite` | 8.3.2 | dev | Bundler and dev server. Native bindings come as per-platform optional packages, so no install script is needed. | MIT |
| `@vitejs/plugin-react` | 6.1.1 | dev | JSX transform for Vite. | MIT |
| `typescript` | 7.0.2 | dev | Strict type checking (`tsc --noEmit`); Vite does the transpiling. | Apache-2.0 |
| `vitest` | 5.0.3 | dev | Unit and component tests; shares Vite's config. | MIT |
| `@testing-library/react`, `@testing-library/dom` | 16.3.3, 10.4.2 | dev | Render components and query them the way a user would. | MIT |
| `jsdom` | 30.1.1 | dev | DOM for tests. | MIT |
| `@types/react`, `@types/react-dom` | 19.3.0 | dev | Types. | MIT |
| `@fontsource/ibm-plex-sans`, `@fontsource/ibm-plex-mono` | 5.3.0 | dev | Font files only, bundled into the build so no font loads from a remote origin. | OFL-1.1 |

**The whole tree is 98 packages**, all dev-only apart from the three above.
The licences are MIT (76), Apache-2.0 (6), MIT-0, ISC, BSD-2/3, CC0,
BlueOak, OFL-1.1 (the fonts), and MPL-2.0 (2, dev-only, file-level
copyleft, nothing shipped). The licences were checked with
`pnpm licenses list`.

Every version was the newest release at least 14 days old on 2026-10-17.

### Rejected

- **Tailwind:** a build-time framework plus a class vocabulary we'd have to
  police. Plain CSS on custom-property tokens (`ui/DESIGN.md`) does the
  same job for a dozen screens.
- **A component kit (shadcn/ui, MUI, Radix):** weight, and an untouched kit
  is itself a recognisable "AI default". The few primitives we need are
  native: `<dialog>`, `<details>`, `<button>`, `<table>`.
- **TanStack Query, react-router, any state library:** the console polls a
  handful of endpoints and has about five routes. A typed `fetch` wrapper,
  a `usePoll` hook and a ~60-line history router are smaller than any of
  them, and add no dependency.
- **lucide-react:** about ten icons are copied as inline SVG instead (ISC,
  attributed in `ui/THIRD_PARTY.md`).
- **ESLint:** dozens of transitive packages. Strict `tsc` covers the type
  level; lint rules can come later with their own entry here.
- **npm or yarn as the package manager:** both lack an enforced
  minimum-release-age setting and can't disable dependency build scripts
  per package.
- **pnpm 12.x** (current major): released recently. Pinned instead to
  10.34.6, a mature line still being patched (last release 2026-09-28). It
  has `minimumReleaseAge` and blocks dependency build scripts by default.
  Revisit when 12.x has a few months of patches.
- **Installing third-party design "skills" with `npx`.** They were
  reviewed by reading their source only. Two of them would download and run
  binaries or scripts (Impeccable's launcher, taste-skill's inspector). The
  rules adopted from them live as reviewed markdown in `ui/design-rules/`.

### Controls

1. **Install scripts.** No install or build script ever runs.
   - `ignore-scripts=true` is set in `ui/.npmrc`.
   - `pnpm-workspace.yaml` has an empty `onlyBuiltDependencies` and
     `strictDepBuilds: true`, so a dependency that wants a script fails
     the install rather than being skipped silently.
   - Verified on 2026-10-17: `pendingBuilds` is empty after install.
2. **Release age.** No version younger than 7 days can be installed
   (`minimumReleaseAge: 10080`). Verified on 2026-10-17: `pnpm add
   react@19.3.1`, published three days earlier, was refused.
3. **Pinning.** Versions are exact (`save-exact`), the lockfile is
   committed, and verification uses `--frozen-lockfile`.
4. **The pnpm binary.** pnpm comes from corepack, pinned by version and
   sha512 in `package.json`'s `packageManager`. Nothing is installed
   globally.
5. **No credentials during install or build.** `scripts/guard-env.mjs`
   refuses to run if `GITHUB_TOKEN`, `GH_TOKEN`, `NPM_TOKEN` or
   `NODE_AUTH_TOKEN` is set.
6. **Audit.** `pnpm audit --audit-level high` is part of verification.
   The result on 2026-10-17: no known vulnerabilities.
7. **Runtime.** Node never runs in production. The build is static files
   embedded in `serendipd` (`cmd/serendipd/console`). They're served
   under a strict CSP (`script-src 'self'`, `connect-src 'self'`). So even
   compromised build output can't load code from, or send data to, another
   origin.
8. **Go tooling isolation.** `ui/go.mod` fences `ui/` off from the root Go
   module, so `go build ./...` never compiles a `.go` file that an npm
   package ships.

## Consequences

- Adding a package means: a row in the table above, a reason, and its
  transitive count. A dependency that needs an install script needs a
  named entry in `onlyBuiltDependencies` and a reason here.
- Every update waits at least 7 days, by design. An urgent security fix
  younger than that is added through `minimumReleaseAgeExclude` for that
  one package, with a note here.
- The console's runtime footprint is React plus our own code: 66 KiB of
  gzipped JS for the scaffold, against a 160 KiB budget the build enforces.
