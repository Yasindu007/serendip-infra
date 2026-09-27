# Tasks: Spec 005 — K3s bootstrap

Ordered; `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). No `core/`/`providers/` file is touched, so the mandatory
contract-guardian-before-commit rule doesn't strictly apply — still run
one holistic pass at T008 given the real-host-mutation risk involved.

## T001 — agent/bootstrap scaffold

- **Do**: Create `agent/bootstrap/` package (`doc.go`).
- **Files**: `agent/bootstrap/doc.go`.
- **Verify**: `go build ./...` succeeds.

## T002 — Pre-flight checks

- **Do**: `parseSystemdStatus`, `parseIPTablesVersion` — pure functions
  over already-captured command output, table-driven-tested against
  fixtures (a `systemctl is-system-running` "running" sample, a
  "degraded" sample, a not-found/ENOENT case; an `iptables --version`
  sample in each of nf_tables/legacy mode, at both a risky (1.8.0-1.8.4)
  and safe version). `checkLinux`/`checkRoot`/`checkSystemd`/
  `checkIPTablesMode` are thin real-exec wrappers around the parse
  functions plus `runtime.GOOS`/`os.Geteuid()`.
- **Files**: `agent/bootstrap/preflight.go`, `agent/bootstrap/preflight_test.go`,
  `agent/bootstrap/testdata/*`.
- **Verify**: `go test ./agent/...` — all parse-function cases pass;
  this is acceptance criterion 1 and most of criterion 6.

## T003 — Install (dry-run path first)

- **Do**: `InstallOptions`, `DefaultVersion` const, `InstallEnv` (pure,
  returns the env vars that would be set — the single source of truth
  both `--dry-run` and the real path use, per plan.md).
- **Files**: `agent/bootstrap/install.go`, `agent/bootstrap/install_test.go`.
- **Verify**: `go test ./agent/...` — `InstallEnv` with default and
  overridden `Version` produces the expected `INSTALL_K3S_VERSION=...`
  entry, nothing else surprising.

## T004 — Install (real path)

- **Do**: `Install(ctx, opts)` — fetches `https://get.k3s.io` via
  `net/http`, pipes the body to `sh -s -` with `InstallEnv(opts)` set on
  the command's environment (not a shell pipeline string). Download
  errors and exec errors are distinguishable in the returned error.
- **Files**: `agent/bootstrap/install.go` (extends T003).
- **Verify**: `go build ./...` succeeds; real execution is T007's job
  (this task doesn't run a real install, doing so isn't safe outside
  the disposable test environment).

## T005 — Verify + Uninstall

- **Do**: `WaitForReady(ctx, timeout)` — polls `systemctl is-active
  k3s` then `k3s kubectl get nodes` for a `Ready` condition, tolerating
  early "not up yet" errors (API server not existing immediately after
  install exits — plan.md Risks) rather than treating them as fatal
  before the timeout. `Uninstall(ctx)` — checks
  `/usr/local/bin/k3s-uninstall.sh` exists (clear error if not — "was
  k3s ever installed?"), runs it.
- **Files**: `agent/bootstrap/verify.go`, `agent/bootstrap/uninstall.go`.
- **Verify**: `go build ./...` succeeds; real execution is T007's job.

## T006 — cmd/serendip-agent bootstrap subcommand

- **Do**: New `bootstrap` subcommand: `--dry-run` (prints `InstallEnv`
  output + script source URL, no root/host-mutation, works regardless
  of `--confirm`), `--confirm` (required for both install and
  uninstall paths; without it, prints the same dry-run summary and
  exits non-zero), `--uninstall` (switches to the uninstall path),
  `--version` (default `bootstrap.DefaultVersion`), `--timeout` (default
  5m, passed to `WaitForReady`). Runs pre-flight checks before the real
  install path; `checkSystemd` failing is a hard abort, `checkIPTablesMode`
  warning is printed but doesn't block.
- **Files**: `cmd/serendip-agent/main.go` (extends spec 004's file).
- **Verify**: `go build ./...`; `serendip-agent bootstrap --dry-run`
  runs and prints the expected summary without touching anything, on
  whatever host this is run on (Windows dev machine included — should
  print cleanly, not crash, even though the real path would reject
  non-Linux).

## T007 — Real-host verification (disposable WSL2 clone): acceptance
criteria 2, 4, 5, 6

- **Do**: `wsl --export Ubuntu <tarfile>` + `wsl --import
  serendip-k3s-test <dir> <tarfile>` (plan.md's resolved approach —
  `wsl --install`'s catalog fetch failed in this environment). Inside
  the clone: build the real `serendip-agent` binary, run `bootstrap
  --dry-run` (sanity check), then `bootstrap --confirm`, confirm
  `systemctl is-active k3s` and `k3s kubectl get nodes` both report
  healthy within the timeout, confirm `k3s --version` matches the
  pinned version exactly (criterion 5). Then `bootstrap --uninstall
  --confirm`, confirm `systemctl is-active k3s` no longer reports
  active and the binary/service are gone (criterion 4). Finally
  `wsl --unregister serendip-k3s-test` to destroy the clone
  completely — the persistent `Ubuntu` distro must be untouched
  throughout (confirm nothing changed there either, before and after).
- **Files**: none (verification task; a scratch shell script is fine).
- **Verify**: all of the above observed directly, not assumed. Confirm
  the persistent dev WSL distro (`Ubuntu`) has no k3s service/files
  afterward — this task must leave zero trace outside the disposable
  clone.

## T008 — Final acceptance pass + contract-guardian

- **Do**: `go test ./agent/... -v` full run, re-check all 7 acceptance
  criteria against actual test output + T007's real-host results,
  `go vet ./...`, `gofmt -l .`, `.claude/scripts/check-imports.sh`
  (confirms `agent/bootstrap` doesn't add a stray `core/`/`api/`
  import). One holistic `contract-guardian` review across all of
  `agent/bootstrap` and the `cmd/serendip-agent` diff, explicitly
  re-reading spec.md's Scope-IN line by line against the actual code
  (specs 002/003/004's established lesson) — pay particular attention
  to whether `--confirm`/`--dry-run` gating is airtight (no code path
  reaches a real mutation without it), since that's this spec's core
  safety property.
- **Files**: none (review/gate task).
- **Verify**: all green, contract-guardian PASS, all 7 acceptance
  criteria individually confirmed.

## T009 [A] — Docs sync

- **Do**: Update `docs/PROGRESS.md` (spec 005 complete, Phase 3 status
  per `docs/PRD.md`'s roadmap). Correct `docs/PRD.md`'s POC scope item 2
  — it currently attributes K3s bootstrapping to "spec 004," which was
  discovered stale (spec 004 explicitly deferred it; this spec is the
  one that actually did it). Update `docs/architecture/bootstrap-agent.md`
  if implementation revealed anything the design discussion didn't
  anticipate (e.g. the Flannel/Cilium fresh-cluster-not-migration cost,
  if worth a permanent breadcrumb there rather than just this spec's
  plan.md).
- **Files**: `docs/PROGRESS.md`, `docs/PRD.md`, possibly
  `docs/architecture/bootstrap-agent.md`.
- **Verify**: no stale language remains describing spec 005 as
  not-started, and PRD.md's item 2 attribution is corrected.
