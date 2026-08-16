# Benchmark: spec 001 resource model, 10k resources (smoke)

**⚠️ Measured on the dev/POC machine (RTX 4060 / Ryzen 7 / 32GB RAM), NOT
T470 reference hardware.** See `docs/PRD.md`'s Hardware section: this
number is directional only and must be re-measured on real T470-class
hardware in roadmap Phase 3b before it is treated as a shipped
constitution rule 4 claim ("< 256MB RSS combined at idle" is about the
full control plane at idle; this benchmark measures one CRUD-heavy
in-process workload, not the daemon's idle footprint — a different thing
entirely, don't conflate the two numbers later).

## What this measures

`cmd/bench-resource-model` (`go run ./cmd/bench-resource-model`) creates
10,011 resources through `core/store/sqlite` — 1 Organization, 10
Projects, 200 Applications, 9,800 Components — each Component chained to
the previous one via a `depends-on` Relationship, so the `edges` table
grows realistically alongside `resources`, not just `resources` in
isolation. Every Create goes through the full T011-T014 path: optimistic
concurrency bookkeeping, graph-edge sync, transactional audit-log write,
post-commit event publish (no subscriber attached for this run).

This satisfies spec 001 acceptance criterion 5 ("combined test binary
demonstrates < 50MB RSS handling 10k resources in SQLite") as a
dev-machine directional result.

## Methodology

Process memory is the OS-reported figure, not a Go-runtime proxy:
`/proc/self/status` `VmRSS` on Linux (the actual edge-1/T470 target
platform, ADR 0001) with a Windows fallback via
`(Get-Process -Id $PID).WorkingSet64` (no new Go dependency added for a
one-off benchmark tool — see `cmd/bench-resource-model/main.go`'s
`processMemory()`). The same binary runs unmodified on both this dev
machine and a real T470 in Phase 3b. Go's own `runtime.MemStats.Sys`/
`HeapAlloc` are also reported for reference but are not the headline
number — they undercount true process RSS in a Go binary (they exclude
the Go runtime's own non-heap overhead, the SQLite driver's C-derived
allocations transpiled into Go, and OS-level binary/stack pages).

## Results (2026-08-16, dev machine)

Two runs, `go run ./cmd/bench-resource-model`:

| Run | Resources | Elapsed | Go `Sys` | Go `HeapAlloc` | Process memory (WorkingSet64) |
|---|---|---|---|---|---|
| 1 | 10,011 | 9.16s | 16.1 MB | 0.6 MB | **20.6 MB** |
| 2 | 10,011 | 8.83s | 16.1 MB | 0.7 MB | **22.6 MB** |

Well under the 50MB target as a dev-machine directional result. ~9s for
10k sequential `Create` calls (~1.1ms/resource) is unoptimized but
untroubling at this scale — spec 001 never claimed a throughput target,
only the RSS ceiling.

## Known limitations of this run (don't over-read the numbers)

- **Not T470 hardware.** The entire point of the honesty rule this file
  opens with. Revalidate in Phase 3b.
- **No concurrent load.** `conn.go` serializes all writes
  (`SetMaxOpenConns(1)`) by design at this scale; this benchmark is
  single-goroutine sequential Creates only, matching that design, not
  stress-testing it.
- **Create-heavy, not steady-state.** This measures growth to 10k
  resources, not an idle daemon holding 10k resources already loaded —
  the constitution rule 4 idle-RSS claim is a different measurement this
  benchmark does not make.
- **WAL file left on disk until process exit** (`t.TempDir()`-equivalent
  temp dir, cleaned up via `defer os.RemoveAll`) — disk usage isn't
  reported here; not a spec 001 concern.
