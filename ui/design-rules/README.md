# Design rules (vendored, reviewed)

Rule text the console is designed and reviewed against
(`specs/016-ui-mvp/plan.md` decision 11). Each file was read in full
before it was copied here. Only the markdown is vendored: no launcher,
scanner, script or live URL fetch from these projects is used (ADR 0012,
"Rejected").

| File | Source | Commit | Licence |
|---|---|---|---|
| `impeccable-operate.md` | github.com/pbakaus/impeccable `plugin/skills/impeccable/reference/mode-operate.md` | `08d5b47` | Apache-2.0 |
| `impeccable-craft-floor.md` | same repo, `reference/craft-floor.md` | `08d5b47` | Apache-2.0 |
| `anthropic-frontend-design.md` | github.com/anthropics/skills `skills/frontend-design/SKILL.md` | `dbd4588` | Apache-2.0 |
| `unslop-tells.md` | github.com/JCarterJohnson/vibecoded-design-tells `skill/references/tells.md` | `f7c4aef` | MIT |
| `vercel-web-interface-guidelines.md` | github.com/vercel-labs/web-interface-guidelines `command.md` | `434b7f9` | MIT |

Copied 2026-10-17. To update one, re-read the new version in full first,
then replace the file and the commit above in one change.

## How they're used

`ui/DESIGN.md` is the contract; these files are the reasoning behind it.
Where they disagree, `DESIGN.md` records the decision:

- **Base:** Impeccable's *Operate* mode. A console is a tool, so
  scanability and familiar controls come before expression. The world
  contributes type, palette, density and one signature element only.
- **Audit:** the frontend-design tells (§ "Process") and the unslop
  catalogue, checked on every screen.
- **Mechanics:** Vercel's guidelines for accessibility, focus, forms,
  content handling and copy.
- **Conflicts resolved:**
  - Vercel asks for Title Case headings and buttons; frontend-design and
    Impeccable prefer sentence case. **Sentence case wins.**
  - taste-skill (not vendored: it targets landing pages) bans all thin
    rules; Impeccable and Vercel allow them in real tables. **Rules are
    allowed in tables and form fields only.**
  - Monospace: allowed only for real data (image references, IDs, ports,
    API text), never as a "technical" costume.
