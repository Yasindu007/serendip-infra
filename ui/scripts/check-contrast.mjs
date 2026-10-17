// Checks WCAG 2.2 contrast for every text/surface pair the design system
// uses, in both themes (ui/DESIGN.md "Colour"). Standard library only.
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../src/styles/tokens.css", import.meta.url), "utf8");

function block(selector) {
  const start = css.indexOf(selector);
  if (start < 0) throw new Error(`no ${selector} block`);
  const open = css.indexOf("{", start);
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === "{") depth++;
    if (css[i] === "}" && --depth === 0) return css.slice(open + 1, i);
  }
  throw new Error("unbalanced braces");
}

function vars(text) {
  const out = {};
  for (const m of text.matchAll(/--([a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)) out[m[1]] = m[2];
  return out;
}

const light = vars(block(":root {"));
const dark = { ...light, ...vars(block(':root[data-theme="dark"]')) };

function luminance(hex) {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  const [r, g, b] = c.map((v) => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function ratio(a, b) {
  const [l1, l2] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (l1 + 0.05) / (l2 + 0.05);
}

// [foreground, background, minimum]. 4.5 for text, 3 for UI boundaries.
const pairs = [
  ["text", "surface-page", 4.5],
  ["text", "surface-raised", 4.5],
  ["text", "surface-sunken", 4.5],
  ["text-muted", "surface-page", 4.5],
  ["text-muted", "surface-raised", 4.5],
  ["accent", "surface-page", 4.5],
  ["accent", "surface-raised", 4.5],
  ["accent-text", "accent", 4.5],
  ["ok", "surface-raised", 4.5],
  ["ok", "surface-page", 4.5],
  ["progress", "surface-raised", 4.5],
  ["danger", "surface-raised", 4.5],
  ["danger", "danger-bg", 4.5],
  ["warn-text", "warn-bg", 4.5],
  ["unknown", "surface-raised", 4.5],
  ["rail-text", "rail-bg", 4.5],
  ["rail-muted", "rail-bg", 4.5],
  ["rail-text-strong", "rail-active", 4.5],
  ["border-strong", "surface-raised", 3],
  ["focus", "surface-page", 3],
  ["focus", "surface-raised", 3],
  ["ok-mark", "surface-raised", 3],
];

let failed = 0;
for (const [name, theme] of [["light", light], ["dark", dark]]) {
  for (const [fg, bg, min] of pairs) {
    if (!theme[fg] || !theme[bg]) {
      console.error(`${name}: missing token --${theme[fg] ? bg : fg}`);
      failed++;
      continue;
    }
    const r = ratio(theme[fg], theme[bg]);
    const ok = r >= min;
    if (!ok) failed++;
    console.log(`${ok ? "pass" : "FAIL"} ${name.padEnd(5)} ${fg} on ${bg}: ${r.toFixed(2)} (min ${min})`);
  }
}
if (failed > 0) {
  console.error(`${failed} contrast check(s) failed`);
  process.exit(1);
}
