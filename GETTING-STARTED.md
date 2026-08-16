# Getting started (day 1 runbook)

1. git init; copy this kit in; commit as chore(repo): scaffold.
2. Drop the two founding conversation exports into docs/raw/ (create it).
3. Open Claude Code at repo root. First session:
   "Use the docs-keeper subagent approach: extract docs/architecture/*.md
    from docs/raw/ per docs/architecture/README.md. Decisions only,
    exploration dropped, open questions at the bottom of each file."
   Review each file yourself. This is your architecture freeze.
4. /clear. Then: "/plan-spec 001-resource-model" -> review plan.md and
   tasks.md carefully (this is the highest-leverage review of the project).
5. Implement task by task. /ship before every commit. /clear between tasks.
6. Only after 001 merges: /new-spec 002-http-api, then 003-reconciler,
   then 004-bootstrap-agent.

Rules of thumb:
- You review plans and contracts; Claude writes code and tests.
- Never accept "done" without the Verify line output.
- Anything upstream (K3s flags, Longhorn CRDs) -> oss-researcher, never
  pasted into the main session.

## CodeRabbit (PR review)

Config lives at `.coderabbit.yaml` (path-scoped review instructions for
core/, providers/, specs/, ADRs). Takes effect once the GitHub App is
installed -- one-time, browser-only, requires an org owner:

1. Go to github.com/apps/coderabbitai (or github.com/marketplace/coderabbitai).
2. Install/Request for the `Serendip-Infra` org, select `serendip-infra`
   (or all repos).
3. If prompted, approve under org Settings -> Third-party access (org
   owner only).

No API-driven install path exists -- this step can't be scripted.

Once installed, CodeRabbit only auto-reviews PRs from **outside the org**
(`.coderabbit.yaml`'s `auto_review.labels` allowlist +
`.github/workflows/label-external-contributions.yml`, which labels a PR
`external-contribution` based on `author_association`). Org members' PRs
aren't auto-reviewed -- internal review already covers those. Any
maintainer can still request a review manually on any PR by commenting
`@coderabbitai review`.

### Local review (advisory, WSL2)

CodeRabbit CLI ships Linux/macOS binaries only. On Windows it runs through
WSL2. `git config core.hooksPath .githooks` is already set for this repo
clone -- `.githooks/post-commit` fires after every commit, runs
`coderabbit review --light` in the **background** (never delays or blocks
`git commit` -- a full review can take several minutes), and writes
findings to `.coderabbit/review-<sha>.log` (gitignored, local only). If the
CLI isn't installed anywhere it can find, the hook silently no-ops --
nothing breaks for a clone that skips this setup.

One-time setup inside WSL2 Ubuntu (needs your `sudo` password, can't be
scripted):

```sh
wsl
sudo apt-get update && sudo apt-get install -y unzip
sh /tmp/coderabbit-install.sh   # already downloaded and vetted; re-download
                                 # from https://cli.coderabbit.ai/install.sh if missing
coderabbit auth login           # opens a browser; or --api-key "cr-..." for headless
```

After that, every commit gets a background review automatically -- check
`.coderabbit/review-<sha>.log` whenever you want the findings.
