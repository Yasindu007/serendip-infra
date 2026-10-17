// Fails the build when the console's gzipped JavaScript exceeds the budget
// (specs/016-ui-mvp plan.md decision 10). Standard library only.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { gzipSync } from "node:zlib";

const BUDGET = 160 * 1024;
const dir = new URL("../../cmd/serendipd/console/dist/app/assets/", import.meta.url);
let total = 0;
for (const name of readdirSync(dir)) {
  if (!name.endsWith(".js")) continue;
  const size = gzipSync(readFileSync(join(dir.pathname.replace(/^\/([A-Za-z]:)/, "$1"), name))).length;
  total += size;
  console.log(`${name}: ${(size / 1024).toFixed(1)} KiB gz`);
}
console.log(`total JS: ${(total / 1024).toFixed(1)} KiB gz (budget ${BUDGET / 1024} KiB)`);
if (total > BUDGET) {
  console.error("over budget");
  process.exit(1);
}
