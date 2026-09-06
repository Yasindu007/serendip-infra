# Plan: Spec 004 — Bootstrap agent (discovery, identity, self-registration)

Status: Draft — awaiting human approval before implementation.

## Open-question resolutions

1. **Identity file path**: `/var/lib/serendip/agent/node-id` by default,
   overridable via `SERENDIP_AGENT_STATE_DIR` (mirrors `cmd/serendipd`'s
   `SERENDIP_DB_PATH`/`SERENDIP_ADDR` env-var pattern). Tests always
   override it to a `t.TempDir()`, never touching the real path.
2. **Wire format: agent defines its own minimal structs, does not import
   `core/resource`.** Keeps `agent/` a true "just another API client"
   (constitution rule 7 — UI/CLI/MCP are all clients of the same API,
   none of them reach into `core/`) and avoids any coupling beyond the
   one narrow, justified exception (`core/id`, a single-function
   dependency-free utility). If the wire shape ever drifts from
   `core/resource.NodeStatus`, acceptance criterion 4's real end-to-end
   test (real `serendipd` + real agent binary) catches it directly —
   that's a stronger guarantee than a shared Go type would give against
   a JSON *contract* mismatch anyway.
3. **Disk selection: report the disk backing the root filesystem `/`.**
   Resolved via `/proc/mounts` (find the device mounted at `/`) → strip
   the partition suffix to get the underlying block device → read
   `/sys/block/<device>/{size,queue/rotational}`. This is "the OS disk,"
   the one actually relevant to a bootstrap/control-plane placement
   decision — not "largest" (could be an unrelated data disk) or "sum of
   all" (meaningless once disk *types* differ).

## Approach

**`agent/discover`: parse-from-bytes functions, separated from
real-file-reading.** Every fact-extraction function takes the *content*
already read (a `string`/`[]byte`), not a path — e.g.
`parseMemTotal(meminfo []byte) (int64, error)`,
`parseVirtualizationSupport(cpuinfo []byte) (bool, error)`,
`parseDiskStats(sizeStr, rotationalStr string) (bytes int64, diskType string, err error)`.
This is what makes acceptance criterion 1 possible without a real Linux
host in CI: table-driven tests feed canned fixture text (a real
`/proc/meminfo`/`/proc/cpuinfo` sample committed as a test fixture) into
the parse functions directly. A thin `Discover(ctx) (Result, error)`
function does the actual file reads (`os.ReadFile("/proc/meminfo")`,
etc.) and calls the parse functions — this thin layer is what
acceptance criterion 2 verifies for real, in WSL2, not via `go test`.

```go
// agent/discover/discover.go
type Result struct {
    // Hardware facts -- these map onto core/resource.NodeStatus's fields
    // one-to-one (agent/client's wire struct, not this package, owns
    // that mapping).
    CPUCores              int
    MemoryBytes           int64
    DiskType              string // "nvme", "ssd", "hdd", or "" if undetermined
    DiskBytes             int64
    NetworkMbps           int // highest observed among "up" non-loopback interfaces; 0 if none determinable
    VirtualizationSupport bool

    // Environment facts -- bootstrap-agent.md's responsibility #2, but
    // core/resource.NodeStatus has no corresponding fields for these yet
    // (only hardware facts were ever modeled). Printed by `discover` for
    // a human to see; NOT sent by `register` -- there's nowhere on the
    // server side to put them yet. Not silently dropped: flagged in
    // Risks below for whoever eventually needs them.
    OS          string
    IPAddresses []string
    DNSServers  []string
    InternetReachable bool
}

func Discover(ctx context.Context) (Result, error)
```

**`agent/identity`**: reads the persisted ID file if it exists; if not,
generates one via `core/id.New()` (UUIDv7, ADR 0007's existing choice —
reusing it here means the agent's local node identity looks exactly
like any other Serendip resource ID, and needs no new ID scheme
decision) and persists it, creating the state directory if needed.

```go
// agent/identity/identity.go
func Load(stateDir string) (id string, err error) // reads existing or generates+persists a new one
```

**`agent/client`**: `Upsert` does GET-then-POST-or-PUT, keyed by the
locally-persisted ID (spec 001's `Create` already accepts a
client-supplied `Metadata.ID` when non-empty — confirmed by reading
`core/store/sqlite/store.go`'s `Create`, not assumed). A `409` from the
`PUT` path (a rare concurrent-edit race, not expected in normal use
since nothing else edits a Node's status) triggers one GET+PUT retry,
not an unbounded loop — this is a one-shot CLI invocation, not a
long-running reconciler.

```go
// agent/client/client.go
type Client struct{ /* baseURL, token, *http.Client */ }
func New(baseURL, token string) *Client
func (c *Client) Upsert(ctx context.Context, id, name string, status NodeStatusWire) (NodeWire, error)
```

**`cmd/serendip-agent/main.go`**: stdlib subcommand dispatch
(`os.Args[1]` + one `flag.FlagSet` per subcommand — no CLI framework,
matching `agent/CLAUDE.md`'s zero-heavy-deps rule the same way
`api/CLAUDE.md` rejected a router library).

```
serendip-agent discover [--json]
    Runs agent/discover.Discover, prints Result as JSON to stdout. No
    network call. Always safe.

serendip-agent register --control-plane-url=<url> --token=<token> [--name=<name>] [--state-dir=<dir>]
    Runs agent/identity.Load, agent/discover.Discover, maps the
    hardware subset of Result to NodeStatusWire, calls
    agent/client.Upsert. --name defaults to os.Hostname().
```

## Interfaces / types to create

```go
// agent/discover/discover.go
type Result struct { /* fields above */ }
func Discover(ctx context.Context) (Result, error)
func parseMemTotal(meminfo []byte) (int64, error)
func parseVirtualizationSupport(cpuinfo []byte) (bool, error)
func rootBlockDevice(mounts []byte) (string, error)
func parseDiskStats(sizeStr, rotationalStr, deviceName string) (bytes int64, diskType string, err error)

// agent/identity/identity.go
func Load(stateDir string) (id string, err error)

// agent/client/client.go
type Client struct{ /* ... */ }
func New(baseURL, token string) *Client
type NodeStatusWire struct{ /* mirrors core/resource.NodeStatus's JSON shape */ }
func (c *Client) Upsert(ctx context.Context, id, name string, status NodeStatusWire) (NodeWire, error)

// cmd/serendip-agent/main.go
func main()
```

## Files touched

```
agent/discover/discover.go, discover_test.go, testdata/ (fixture files)
agent/identity/identity.go, identity_test.go
agent/client/client.go, client_test.go
cmd/serendip-agent/main.go
.claude/scripts/check-imports.sh          (extended: agent/ imports only core/id)
docs/PROGRESS.md                          (updated on completion)
```

No file under `api/` or `core/` (other than the read-only import of
`core/id`) is touched.

## Risks

- **Environment-discovery facts (OS/IPs/DNS/connectivity) have no home
  in the resource model.** `discover` prints them; `register` can't send
  them anywhere. Not a defect in this spec — `resource-model.md`
  already documents the Network resource shape as unresolved. Flagged
  here so whoever designs that later knows discovery already collects
  the raw facts; only the server-side field is missing.
- **`/sys/class/net/*/speed` is frequently unreadable** for virtual
  NICs, some Wi-Fi drivers, and containerized/virtualized network
  devices (returns an error or `-1`) — including, likely, WSL2's own
  virtual adapter, which is what verification actually runs against.
  `NetworkMbps` may legitimately come back `0`/omitted in the verified
  environment; acceptance criterion 2 only requires the value be
  *plausible*, not non-zero, for exactly this reason.
- **Disk-device-name partition-suffix stripping is a heuristic**
  (`sda2` → `sda`, `nvme0n1p2` → `nvme0n1`), not derived from a stdlib
  helper (none exists) — covered by table-driven tests against real
  naming patterns, but a sufficiently unusual device-naming scheme
  (uncommon storage controllers, `mmcblk0p1`-style eMMC/SD naming) could
  fall outside what's tested. Acceptable at this spec's scope (T470-class
  reference hardware uses standard SATA/NVMe naming); revisit if real
  deployment hardware needs another pattern.
- **`Upsert`'s GET-then-POST-or-PUT is not atomic** — a very unlikely
  concurrent registration of the same ID from two processes could race
  between the GET and the write. Acceptable for a one-shot, human- or
  script-invoked CLI command; the one-retry-on-409 behavior handles the
  realistic case (a stale `ResourceVersion` from a slow-running previous
  invocation), not true concurrent registration.

## Explicitly NOT done in this spec

- K3s install/bootstrap, or any other host mutation.
- Full enrollment/join (compatibility response, recommended role, user
  approval, topology-planner integration).
- Periodic/daemon-mode health reporting.
- Low-level upgrades, emergency diagnostics.
- Any `api/` or `core/` change beyond reading `core/id`.
- Non-Linux discovery support.
