# Serendip Console — design system

The contract every screen follows. Direction: **"Combined"** (spec 016
plan.md decision 11; canvas: https://claude.ai/artifact/DjRkfV2nLaoBn6sK3UtK57).
The rule sources and how their conflicts were settled are in
`design-rules/README.md`.

**Consistency rule:** screens use only the tokens in
`src/styles/tokens.css` and the components in `src/components/`. Screens
have no CSS, no inline `style`, and no one-off colours or sizes. When a
screen needs something new, it becomes a component here first and is
documented below.

## Character

- **An operator's tool.** People come to get a job done, so familiar
  controls and scanability come before expression (Impeccable "Operate").
- **Restrained:** light-first, with a dark theme. One accent colour.
  Dense tables where there is data, plain sentences where there isn't.
- **Character comes from three things only:**
  - a dark navigation rail;
  - IBM Plex type;
  - one signature element, the **reconciliation path**: where a resource
    is on its way from saved to serving, drawn as a line of stages taken
    from its real Conditions.

## Tokens (`src/styles/tokens.css`)

| Group | Tokens | Notes |
|---|---|---|
| Type | `--font-sans` (IBM Plex Sans), `--font-mono` (IBM Plex Mono); `--text-xs` 12 · `--text-sm` 13 · `--text-md` 14 (body) · `--text-lg` 15 (section titles) · `--text-xl` 22 (page title) | Five sizes, no more. Fonts are bundled; nothing loads from another origin. |
| Space | `--space-1` 4 … `--space-12` 48 | A 4 px grid. More space above a heading than below it. |
| Radius | `--radius-sm` 4 (controls), `--radius-md` 6 (panels) | Two radii, by hierarchy. |
| Surfaces | `--surface-page`, `--surface-raised`, `--surface-sunken` | Depth comes from a tone step, never a shadow. |
| Text | `--text`, `--text-muted` | Running text is always `--text`. |
| Lines | `--border` (dividers), `--border-strong` (inputs, table header rule) | Lines appear in tables and form fields only. |
| Accent | `--accent`, `--accent-hover`, `--accent-text`, `--focus`, `--selection` | The primary action, links, and focus. |
| Rail | `--rail-bg`, `--rail-active`, `--rail-text`, `--rail-text-strong`, `--rail-muted` | |
| Status | `--ok`, `--ok-mark`, `--progress`, `--warn-text`/`-bg`/`-border`, `--danger`/`-bg`/`-border`, `--unknown` | Each is always paired with an icon and a word. |

**Dark theme:** the same names, redefined under
`prefers-color-scheme: dark` and under `[data-theme="dark"]`.

**Contrast:** `node scripts/check-contrast.mjs` checks every
text/surface pair in both themes, at 4.5:1 for text and 3:1 for UI
boundaries and focus. It must pass before merging any token change.

## Status vocabulary

There are exactly five states, rendered only by `StatusText`, as an icon
plus a word in the state colour:

| Kind | Icon | Used for |
|---|---|---|
| `ok` | check | Condition `True` |
| `progress` | spinning arc | `Progressing`, `Pending`, `WaitingForApplication`, `ProviderNotReady` |
| `warn` | triangle | `False` with an unrecognised reason |
| `error` | circled x | `DeployFailed`, `Unsupported`, `InvalidSpec`, `EnsureFailed`, `RouteRejected` |
| `unknown` | circled i | no Condition yet, or `Unknown` |

The reasons come from `core/reconcile/application.go`. A new reason is
shown as `warn` with its real text until it's added here; it's never
guessed at.

## Components (`src/components/`)

| Component | Purpose |
|---|---|
| `Shell` | The rail (Overview, Applications; Secrets and Volumes labelled "not available yet"), skip link, signed-in person, sign-out, and the banner slot. |
| `PageHeader` | Breadcrumb, title, status, actions, and a meta line ("Updated 12 seconds ago"). |
| `StatusText`, `ReadyStatus` | The status vocabulary above. |
| `ReconciliationPath` | The signature element. Stages come from real Conditions only, and each stage's state is spoken to screen readers. |
| `ConditionsTable` | Conditions exactly as the API reports them: type, status, reason, message, since. |
| `KeyValueList` | Configuration facts. |
| `SafeLink` | Links API-supplied URLs only when they are `http(s)`; otherwise plain text. |
| `ApiEquivalent` | The request a screen sends (method, path, JSON body), never headers or credentials. On every form and detail page. |
| `TextField` | Label, hint and error tied to one input with `aria-describedby`. |
| `SelectField` | `TextField` for a fixed set of choices. |
| `Guarded` | An action the caller may take, or the same action disabled with the reason beneath (spec 017). Disabled while the caller's permissions are still loading, never offered on a guess. |
| `Banner`, `Notice` | Page-wide and inline notices (warn or danger). |
| `EmptyState`, `LoadingState`, `ErrorState` | Every screen's non-happy states. `ErrorState` shows the request ID. |
| `Icon` | Eight Lucide stroke icons, inline SVG, `aria-hidden`. |

## Writing

- Sentence case everywhere, including buttons ("Create component").
- An action keeps its name through the flow: the "Create component"
  button leads to the "Component created" state.
- Errors say what happened and what to do, in the interface's voice. No
  apologies, no vagueness.
- No eyebrow labels above headings, no uppercase section labels, no "·"
  separators in meta lines, no "→" on buttons.
- Use the product's own words: Application, Component, Condition. Don't
  replace them with Kubernetes terms, and don't invent marketing words.

## Rules that are checked

- **No raw HTML from data.** `grep -r "dangerouslySetInnerHTML\|innerHTML" src`
  must be empty.
- **No fabricated content:** no fake metrics, charts, activity or
  placeholder statistics. A feature that isn't built is labelled "not
  available yet".
- **No delete action anywhere** until approval objects exist
  (constitution 5). That includes removing a role assignment: the Access
  control page shows the `DELETE` request instead.
- **Permission-aware controls** (spec 017): an action the caller can't
  take is hidden when it creates something, and disabled with a reason
  (`Guarded`) when it changes something that's on screen. The decision
  comes from `GET /v1/authorization/permissions`; the server still
  decides every request.
- **Monospace for real data only:** image references, IDs, condition
  reasons, API text.
- **Motion:** only the progress spinner, and none under
  `prefers-reduced-motion`.
- **Keyboard:** every action is reachable and visibly focused
  (`:focus-visible`), and a skip link leads to `#main`.
- **Layout:** works from 390 px wide. The rail becomes a top bar under
  720 px, and wide tables scroll inside their own box.
