// Refuses to run installs or builds with credentials in the environment
// (specs/016-ui-mvp plan.md decision 10). A compromised build tool can read
// every environment variable; this keeps there being nothing worth taking.
const risky = ["GITHUB_TOKEN", "GH_TOKEN", "NPM_TOKEN", "NODE_AUTH_TOKEN", "NPM_CONFIG__AUTH", "NPM_CONFIG__AUTHTOKEN"];
const found = Object.keys(process.env).filter((k) => risky.includes(k.toUpperCase()) && process.env[k]);
if (found.length > 0) {
  console.error(`refusing to run: unset ${found.join(", ")} first (ui/CLAUDE.md, "Install hygiene")`);
  process.exit(1);
}
