# Spec 005: K3s bootstrap

Status: Draft — human review required before plan.md

## Problem

`docs/architecture/bootstrap-agent.md`'s responsibility #4 ("Bootstrap —
installs and configures K3s on a fresh node") was explicitly deferred by
spec 004: "K3s install... requires root, downloads and runs a real
install script, and mutates real host state... There's no disposable
test host in this workflow yet." `docs/PRD.md`'s POC scope item 2
("`serendip-agent` bootstraps a K3s node") is still unmet — this spec is
what closes that gap.

## Scope — IN

- `agent bootstrap` subcommand: drives a real, single-node K3s server
  install by shelling out to the **official** `get.k3s.io` install
  script (`INSTALL_K3S_*` env vars control it) — not reimplementing K3s
  install logic in Go (constitution rule 10: integrate, don't rebuild;
  also the officially-supported path for version pinning, checksums,
  and safe upgrades).
- Explicit `--confirm` gate (`agent/CLAUDE.md`'s hard rule: "all host
  mutations are explicit subcommands with `--confirm`") — refusing to
  run without it, printing what it *would* do instead.
- `--dry-run`: prints the exact install-script invocation and env vars
  that would be used, without executing anything and without requiring
  root — a cheap, additional safety net beyond `--confirm` alone, and
  the only mode of this subcommand safe to run on a non-disposable host.
- Explicit version pinning (`INSTALL_K3S_VERSION`), defaulting to the
  current K3s stable-channel release researched for this spec
  (`v1.36.4+k3s1`), overridable via flag/env — not relying on the
  installer's own unset-env "resolve against stable channel" behavior,
  for reproducibility.
- Pre-flight checks (read-only, safe, run even without `--confirm`):
  confirm Linux, confirm root/sudo capability, confirm systemd is
  present and running (`systemctl is-system-running`) — refusing to
  proceed with a clear error rather than invoking the install script
  into a host that can't support it. Best-effort detection of the
  documented iptables-nftables-mode issue (research finding: iptables
  1.8.0-1.8.4 in nftables mode causes accumulating rule bugs on fresh
  Debian/Ubuntu hosts) — detect and warn, full remediation out of scope.
- Post-install verification: poll `systemctl is-active k3s` then
  `k3s kubectl get nodes` for `Ready`, within a bounded timeout —
  `bootstrap` doesn't return success until the node is actually usable,
  not just "the install script exited 0."
- `--uninstall` mode (same subcommand): shells out to the *official*
  auto-generated `k3s-uninstall.sh`, also gated behind `--confirm` — a
  real, supported teardown path, useful both for safely testing this
  spec repeatedly and as a genuine operator escape hatch.
- K3s's own defaults for bundled components (Traefik, ServiceLB,
  local-storage, Flannel) are left **enabled** — see Scope-OUT for why.

## Scope — OUT (explicitly)

- **Recommended-profile computation** (the topology planner) —
  doesn't exist yet (`bootstrap-agent.md` itself says so). This spec
  always bootstraps one fixed, simple single-node config — no HA, no
  profile selection logic.
- **Multi-node join/enrollment** (`K3S_URL`/`K3S_TOKEN` agent-join flow,
  the richer negotiated "enrollment" `bootstrap-agent.md` describes) —
  already deferred once by spec 004, still deferred here. This spec is
  server-only, single-node.
- **Disabling K3s's bundled Traefik/ServiceLB/local-storage/Flannel** in
  favor of Serendip's eventual defaults (kgateway/Cilium/Longhorn/
  local-path) — none of those providers exist yet; disabling their K3s
  equivalents now would leave a bootstrapped cluster with no
  gateway/storage/load-balancer/networking at all until each provider's
  own future spec replaces the corresponding piece. Each replacing
  provider's spec makes that call when it actually exists, not this one.
  Flagged explicitly: swapping Flannel for Cilium later likely means a
  fresh cluster, not a live migration — acceptable at this dev/POC
  stage, a real cost to know about, not to solve now.
- **Embedded etcd / HA** — sqlite-backed single-node only (matches
  constitution's SQLite-default philosophy; research confirms etcd only
  activates via explicit `--cluster-init`/join flags, never by
  accident).
- **Installing `serendipd` onto the new K3s node** —
  `bootstrap-agent.md`'s separate later step ("Install Serendip control
  plane") needs container packaging work that doesn't exist yet.
- Non-Linux support (same reasoning as spec 004).
- Any `api/`/`core/` change — purely additive under `agent/` +
  `cmd/serendip-agent/`.

## Acceptance criteria

1. `go test ./agent/...` green: pre-flight-check logic (systemd-status
   parsing, OS check) is table-driven-tested against canned
   command-output fixtures, not requiring a real install to run in CI.
2. On a genuinely disposable Linux host (not the persistent dev WSL
   environment used for all other testing in this project —
   `serendip-agent bootstrap --confirm` actually installs K3s, and
   within a bounded timeout both `systemctl is-active k3s` reports
   active and the node shows `Ready` via `k3s kubectl get nodes`.
3. Without `--confirm`, `bootstrap` makes zero host mutations — refuses
   to run, exits non-zero, prints what it would do. `--dry-run` behaves
   the same way regardless of `--confirm`.
4. `serendip-agent bootstrap --uninstall --confirm` (via the official
   uninstall script) cleanly reverses criterion 2's install — verified
   directly (`systemctl is-active k3s` no longer active, binary/service
   gone), not assumed from the uninstall script's exit code alone.
5. The installed K3s version matches the pinned `INSTALL_K3S_VERSION`
   exactly (`k3s --version` after install), proving the "no unpinned
   stable-channel resolution" requirement holds in practice.
6. Pre-flight checks correctly detect and clearly report: non-systemd
   init (tested via a fixture, since faking this for real is
   impractical) and the iptables-nftables-mode condition where
   detectable — refusing to proceed rather than invoking the install
   script blindly.
7. `go vet ./...`, `gofmt -l .`, `.claude/scripts/check-imports.sh`
   clean; zero new Go dependency (`go.mod`/`go.sum` unchanged) — this
   feature is `os/exec` + HTTP download via the official script, no new
   library needed.

## Open questions (resolve during plan review)

- **Disposable test environment mechanics**: a genuinely disposable
  Linux host is needed for criterion 2/4's real verification, distinct
  from the persistent dev WSL distro used everywhere else in this
  project (installing K3s there would leave a permanent systemd
  service/iptables rules/containerd mutating an environment reused for
  unrelated testing). Candidate: a separate, named WSL2 distro instance
  created fresh and destroyed (`wsl --unregister`) after verification —
  needs confirming this is actually practical on this dev machine before
  committing to it as the plan.
- **Pre-flight check depth for the iptables-nftables issue**: research
  found this is a real, documented failure mode, but the *detection*
  mechanism (checking `iptables --version` output, or which
  alternative is active via `update-alternatives`) needs a concrete,
  testable design, not just "check for it" — resolve in plan.md.
