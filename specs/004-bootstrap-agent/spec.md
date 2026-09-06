# Spec 004: Bootstrap agent — discovery, identity, self-registration

Status: Draft — human review required before plan.md

## Problem

`docs/architecture/bootstrap-agent.md` lists 8 responsibilities for
`serendip-agent`: hardware discovery, environment discovery, node
identity, K3s bootstrap, cluster enrollment, health reporting, low-level
upgrades, emergency diagnostics. `core/resource.NodeStatus`'s spec-001
doc comment names the first as this spec's job explicitly: "Nothing
populates these fields in spec 001 -- that's spec 004's job." This spec
builds that slice — hardware/environment discovery, a stable local node
identity, and self-registration against the already-existing `/v1/nodes`
API — not all 8 responsibilities at once.

## Why this spec is narrower than "bootstrap agent"

Three of the eight responsibilities need something that doesn't exist
yet or can't be safely automated-tested right now, so building them here
would mean guessing or mutating a real host as a side effect of running
tests — the same reasoning specs 002/003 already applied to defer real
provider implementations and the apply-side of reconciliation:

- **K3s bootstrap** (installing K3s on a fresh node) requires root,
  downloads and runs a real install script, and mutates real host state
  (a systemd service, `/etc` changes). There's no disposable test host in
  this workflow yet (unlike discovery, which is safe to run for real on
  the dev machine's WSL2 Linux environment — the same approach specs
  002/003 already used for Linux-specific verification). Automating this
  safely needs a throwaway VM/container, not something to bolt onto this
  spec's test suite.
- **Full cluster enrollment** ("the platform reports back hardware
  compatibility and a recommended role... user confirms") needs a
  topology planner and a policy/approval flow, neither of which exist.
  What this spec builds instead — the agent publishing its own discovery
  data to its own `Node` resource via the *existing* generic
  `/v1/nodes` CRUD (spec 002) — is a real, honest sub-slice of
  enrollment, not the full negotiated join.
- **Health reporting** as bootstrap-agent.md describes it is an ongoing
  process (the agent keeps reporting while the node is up). This spec's
  `register` is a one-shot CLI invocation; a periodic/daemon-mode variant
  is a later increment once there's a reason to build it (a real
  multi-node deployment to observe).

Low-level upgrades and emergency diagnostics are not attempted at all
this spec — no code, no partial version.

## Scope — IN

- `agent/discover`: hardware discovery (CPU core count, total memory,
  disk type/size, network interface names + link speed, hardware
  virtualization support) and environment discovery (OS, IP addresses,
  DNS servers, internet reachability) — all read-only, all safe to run
  unconditionally (`agent/CLAUDE.md`: "discovery is always safe/
  read-only"). Zero non-stdlib dependencies (`agent/CLAUDE.md`: "zero
  heavy deps") — reads Linux's stable `/proc`/`/sys` kernel interfaces
  directly plus Go's stdlib `net`/`runtime` packages, no shelling out to
  `lsblk`/`ip`/etc.
- `agent/identity`: a stable local node identity, generated once and
  persisted to a local file, reused (not regenerated) on every
  subsequent invocation.
- `agent/client`: a minimal `net/http`-based client that upserts the
  agent's own `Node` resource (create on first run, update on
  subsequent runs, both keyed by the locally-persisted identity) against
  a configured control-plane base URL + bearer token — reusing spec
  002's existing static-API-key auth mechanism and the existing generic
  `/v1/nodes` routes verbatim. No new `api/` or `core/` code.
- `cmd/serendip-agent/main.go`: the CLI binary. Two subcommands:
  `discover` (prints discovered facts as JSON to stdout, no network
  call, always safe) and `register` (discovers, then upserts via
  `agent/client`). Neither needs `--confirm`
  (`agent/CLAUDE.md`'s `--confirm` gate is for *host* mutations —
  installing K3s, formatting a disk; `register` mutates the control
  plane's data, not the local host, and `discover` mutates nothing).

## Scope — OUT (explicitly)

- Installing/bootstrapping K3s, or any other host mutation.
- The full enrollment/join flow (hardware-compatibility response,
  recommended role, user approval, topology-planner integration).
- Periodic/daemon-mode health reporting — `register` is one-shot.
- Low-level upgrades, emergency diagnostics.
- Any change to `api/` or `core/` — this spec is purely additive under
  `agent/` and `cmd/serendip-agent/`, reusing `/v1/nodes` as-is.
- Non-Linux discovery support — `agent/CLAUDE.md`'s target is "a bare
  Debian/Ubuntu host"; discovery on any other OS returns a clear error,
  not a supported code path.

## Acceptance criteria

1. `go test ./agent/...` green: the parsing logic for each discovery
   fact (`/proc/meminfo`, `/sys/block/*/{size,queue/rotational}`,
   `/proc/cpuinfo` virtualization flags) is table-driven-tested against
   canned fixture text, runnable in CI without a real Linux host.
2. On a real Linux host (verified in WSL2, mirroring specs 002/003's
   verification approach): `serendip-agent discover` reports plausible
   real values for every `NodeStatus` field it's responsible for.
3. Node identity is stable: running `serendip-agent discover` (or
   `register`) twice in a row produces the same local identity both
   times, not a freshly generated one.
4. End-to-end, against a real running `serendipd` (verified in WSL2):
   `serendip-agent register` creates a `Node` resource on the first run;
   running it again updates the *same* `Node` resource (same ID, higher
   `ResourceVersion`), not a duplicate. The resulting `NodeStatus` fields
   match what `discover` reported standalone.
5. `go.mod`/`go.sum` gain no new external dependency — `agent/identity`
   reuses `core/id` (already `google/uuid`, ADR 0007); everything else
   is stdlib only, satisfying `agent/CLAUDE.md`'s "zero heavy deps."
6. `go vet ./...`, `gofmt -l .` clean; `agent/` imports `core/id` only
   (no `core/store`, `core/registry`, `core/reconcile`, or `api/`) —
   confirmed by extending `.claude/scripts/check-imports.sh` or an
   equivalent explicit check, not just asserted in a comment.
7. `cmd/serendip-agent` binary size is measured and recorded (not
   necessarily meeting the full `agent/CLAUDE.md` <30MB target yet if
   Go's baseline static-binary floor makes that unrealistic at this
   stage — measure honestly, don't assert a number without checking).

## Open questions (resolve during plan review)

- **Local identity file path**: where does the persisted node-identity
  file live by default, and how is it overridden for testing (env var,
  flag)? Needs a concrete default, not left implicit.
- **Wire format for `agent/client`**: does the agent define its own
  minimal Go structs matching the `/v1/nodes` JSON shape (keeping
  `agent/` fully decoupled from `core/resource`, consistent with it
  being "just another API client" like any future UI/CLI/MCP consumer —
  constitution rule 7), or import `core/resource.NodeStatus` directly to
  avoid shape duplication? Trade-off worth deciding explicitly, not
  defaulting silently.
- **Disk selection**: `NodeStatus` has one `DiskType`/`DiskBytes` pair,
  but a real host can have multiple block devices. Which one does
  discovery report (largest? the one backing `/`? all of them summed?)
  needs a concrete, defensible rule.
