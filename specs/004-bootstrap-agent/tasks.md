# Tasks: Spec 004 — Bootstrap agent (discovery, identity, self-registration)

Ordered; `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). This spec touches no `core/` or `api/` file (beyond reading
`core/id`), so the CLAUDE.md rule to run `contract-guardian` before every
`core/`/`providers/` commit doesn't strictly apply — still run one
holistic pass at T009 given `agent/` is a new top-level component.

## T001 — agent/ + cmd/serendip-agent scaffold

- **Do**: Create `agent/{discover,identity,client}/` package directories
  (doc.go per package) and `cmd/serendip-agent/main.go` (stub
  `func main() {}`).
- **Files**: `agent/discover/doc.go`, `agent/identity/doc.go`,
  `agent/client/doc.go`, `cmd/serendip-agent/main.go`.
- **Verify**: `go build ./...` succeeds.

## T002 — agent/discover: hardware parse functions

- **Do**: `parseMemTotal`, `parseVirtualizationSupport`,
  `rootBlockDevice`, `parseDiskStats` — pure functions over
  already-read bytes/strings, no file I/O. Commit real
  `/proc/meminfo`/`/proc/cpuinfo`/`/proc/mounts` sample content as test
  fixtures (`testdata/`).
- **Files**: `agent/discover/discover.go`, `agent/discover/discover_test.go`,
  `agent/discover/testdata/*`.
- **Verify**: `go test ./agent/...` — table-driven tests cover: a normal
  meminfo sample, a cpuinfo sample with `vmx`/`svm` present and absent,
  root-device resolution for both a traditional (`sdaN`) and NVMe
  (`nvme0n1pN`) partition naming scheme, disk-stats parsing for
  SSD/HDD/NVMe rotational values.

## T003 — agent/discover: real Discover() + environment facts

- **Do**: `Discover(ctx) (Result, error)` — the thin real-I/O layer:
  reads `/proc/meminfo`, `/proc/cpuinfo`, `/proc/mounts`,
  `/sys/block/<device>/{size,queue/rotational}`; `runtime.NumCPU()` for
  `CPUCores`; `net.Interfaces()` + `/sys/class/net/<iface>/speed` for
  `NetworkMbps` (highest among up, non-loopback interfaces; 0 if none
  determinable); `runtime.GOOS` + `net.Interfaces()` for `OS`/
  `IPAddresses`; parses `/etc/resolv.conf` for `DNSServers`; a bounded
  outbound TCP dial (short timeout) for `InternetReachable`.
- **Files**: `agent/discover/discover.go` (extends T002).
- **Verify**: `go build ./agent/...` succeeds. Real-value verification
  happens in T008 (WSL2, real host) — this task's own automated tests
  are limited to "doesn't panic / returns a Result" since real hardware
  values aren't portably assertable in CI.

## T004 — agent/identity

- **Do**: `Load(stateDir) (id string, err error)` — reads the persisted
  ID file if present; otherwise generates one via `core/id.New()` and
  writes it (creating `stateDir` if needed).
- **Files**: `agent/identity/identity.go`, `agent/identity/identity_test.go`.
- **Verify**: `go test ./agent/...` — first `Load()` on an empty
  `t.TempDir()` generates and persists an ID; a second `Load()` against
  the same directory returns the identical ID, not a new one; a
  corrupted/empty-but-existing file is handled with a clear error, not a
  panic.

## T005 — agent/client

- **Do**: `NodeStatusWire`/`NodeWire` structs (JSON-tag-matched to
  `/v1/nodes`' actual wire shape — verify against `api/handler.go` +
  `core/resource/infra.go`'s json tags directly, don't guess), `Client`,
  `Upsert` (GET `/v1/nodes/{id}` → 404 means POST to create, 200 means
  PUT with the fetched `ResourceVersion` to update; one retry on a 409
  from the PUT).
- **Files**: `agent/client/client.go`, `agent/client/client_test.go`.
- **Verify**: `go test ./agent/...` — `httptest.Server`-backed tests
  (not a real `serendipd`, that's T008) cover: create-when-missing,
  update-when-existing (confirms PUT carries the GET'd
  `ResourceVersion`), and the one-retry-on-409 path.

## T006 — cmd/serendip-agent wiring

- **Do**: Real `main()`: `discover` subcommand (prints `Result` as
  JSON), `register` subcommand (`--control-plane-url`, `--token`,
  `--name` defaulting to `os.Hostname()`, `--state-dir` defaulting to
  `/var/lib/serendip/agent`) wiring `agent/identity` → `agent/discover`
  → `agent/client`.
- **Files**: `cmd/serendip-agent/main.go` (replaces T001's stub).
- **Verify**: `go run ./cmd/serendip-agent discover` runs and prints
  JSON without error (on whatever host this is run on — real-value
  correctness is T008's job, this just proves the wiring doesn't crash).

## T007 [P] — Import boundary check

- **Do**: Extend `.claude/scripts/check-imports.sh` (or add an
  equivalent explicit check) confirming `agent/` imports only `core/id`
  from `core/` — not `core/store`, `core/registry`, `core/reconcile`,
  and not `api/` at all.
- **Files**: `.claude/scripts/check-imports.sh`.
- **Verify**: the check passes against the actual T001-T006 code, and
  would fail if a stray `core/store` (etc.) import were added (confirm
  by temporarily adding one, running the check, then reverting —
  same "prove the check actually catches the violation" discipline
  spec 002's import-boundary work used).

## T008 — Real-host verification (WSL2): acceptance criteria 2-4

- **Do**: Build the real `cmd/serendip-agent` binary in WSL2 (mirrors
  specs 002/003's approach). Run `discover`, confirm every hardware
  field is a plausible real value for the WSL2 environment (CPU count
  matches `nproc`, memory is in a sane range, etc. — a scripted
  comparison, not just eyeballing). Confirm identity stability across
  two invocations. Start a real `serendipd` (also in WSL2), run
  `register` twice against it, confirm a `Node` resource is created on
  the first run and updated (same ID, incremented `ResourceVersion`) on
  the second, with `NodeStatus` fields matching standalone `discover`
  output.
- **Files**: none (verification task; a scratch shell script is fine,
  doesn't need to live in the repo unless it's worth keeping as a
  reusable check).
- **Verify**: all of the above observed directly, not assumed from unit
  tests alone.

## T009 — Final acceptance pass + contract-guardian

- **Do**: `go test ./agent/... -v` full run, re-check all 7 acceptance
  criteria against actual test output + T008's real-host results,
  `go vet ./...`, `gofmt -l .`, the T007 import-boundary check, record
  `cmd/serendip-agent`'s binary size (`go build -o ... && ls -la`)
  against `agent/CLAUDE.md`'s <30MB target — report honestly if it
  doesn't meet that yet rather than asserting it does. One holistic
  `contract-guardian` review across all of `agent/` and
  `cmd/serendip-agent`, explicitly re-reading spec.md's Scope-IN line by
  line against the actual code (specs 002/003's T019/T010 lesson).
- **Files**: none (review/gate task).
- **Verify**: all green, contract-guardian PASS, all 7 acceptance
  criteria individually confirmed.

## T010 [A] — Docs sync

- **Do**: Update `docs/PROGRESS.md` (spec 004 complete or in-progress
  status, Phase 2 status per `docs/PRD.md`'s roadmap). Update
  `docs/architecture/bootstrap-agent.md` if implementation revealed
  anything the original design discussion didn't anticipate — in
  particular, note the environment-discovery-facts-have-no-resource-home
  gap (plan.md Risks) as a breadcrumb for whoever designs the Network
  resource or extends `NodeStatus`.
- **Files**: `docs/PROGRESS.md`, possibly
  `docs/architecture/bootstrap-agent.md`.
- **Verify**: no stale language remains describing spec 004 as
  not-started.
