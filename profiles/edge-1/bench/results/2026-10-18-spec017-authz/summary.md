# Spec 017 authorization: performance (T009)

Targets are from `specs/017-authorization/plan.md` decision 12.

**Verdict: PASS on the dev machine and on the simulated T470** (see
the end). The first measurement **failed** the
list target, by about 4.4×. The fix is described below, and both runs
are recorded.

## Setup

- **Machine:** AMD Ryzen 7 7840HS, 31 GiB, Windows 11. Linux numbers come
  from WSL2 Ubuntu (kernel 6.6, 4 vCPU), running the same test binary
  cross-compiled with `GOOS=linux`. Go 1.25.0.
- **Benchmark:** `api/authz_bench_test.go` `BenchmarkAuthorization`. It
  compares the same handlers with authorization (through `NewServer`) and
  without it (a plain mux behind the same authentication):
  - for one Component (`GET /v1/components/{id}`);
  - for a list of 100 (`GET /v1/components?ownerId=…`), filtered item by
    item.
- **Data:** the caller is Reader at the Project, so every decision walks
  the full chain: Component, Application, Environment, Project,
  Organization. A sibling project carries 20 other assignments that every
  decision has to skip.
- **Percentiles:** p50 and p99 come from per-request timings. On Windows
  the timer quantizes those to about 1 ms, so the Windows runs give means
  only. **The percentile verdicts use the Linux runs.**

## Results

### Linux (WSL2), 3 runs × 3 s (`bench-linux-wsl2.txt`)

| Request | p50 without → with | p99 without → with | p99 added | Target | Verdict |
|---|---|---|---|---|---|
| One Component | 28 → 38 µs | 159–170 → 219–238 µs | 74 µs (medians 163 → 237) | < 100 µs | PASS |
| 100-item list | 796–841 µs → 1.04–1.09 ms | 2.16–2.30 → 2.62–3.59 ms | 0.72 ms (medians 2.26 → 2.98) | < 2 ms | PASS |

The single-request p99 has less margin than the mean (+12 µs) suggests.
The tail is mostly GC and scheduler noise on 4 vCPU, so it should be
re-checked on the simulated T470.

### Windows, 6 runs × 2 s, medians (`bench-windows-dev.txt`)

| Request | Without | With | Added | Allocations added |
|---|---|---|---|---|
| One Component | 34.8 µs | 42.9 µs | **8.1 µs** | +38 |
| 100-item list | 767 µs | 1041 µs | **0.27 ms** | +1223 (about 12 per item) |

### Idle memory (`serendipd`, Windows)

These figures compare `0d82624`, the last commit before spec 017's code,
with the current build. Both were started the same way: a fresh store, no
cluster, one API key, four authenticated reads, then 30 s idle, then three
samples 5 s apart. Each build was run three times; medians are shown.

| Build | Private bytes | Working set |
|---|---|---|
| Before spec 017 | 15.1 MiB | 12.8 MiB |
| Spec 017 | 15.8 MiB | 13.4 MiB |
| **Growth** | **+0.7 MiB** | **+0.6 MiB** |

Raw samples: `rss-windows.txt`. The target is < 5 MiB: **PASS**. The spec 017 binary also embeds the
built console, which the older one served as "not built", so the true
authorization cost is slightly lower still. The binary grew from 19.1 MB
to 20.2 MB.

## The failed first run, and the fix

The first version walked a resource's ownership chain with one SQLite
query per level, for every decision. Its per-request cache only
remembered whole chains by their starting resource, so 100 sibling
Components still cost about 500 queries. These figures are quoted from
that run's console output; it wasn't saved to a file:

| Windows, first version | Without | With | Added | Target |
|---|---|---|---|---|
| One Component | 36 µs | 137 µs | ~100 µs (mean) | < 100 µs (p99): would fail |
| 100-item list | 0.81 ms | 9.6 ms | **~8.8 ms** | < 2 ms: **FAIL** |

The fix is in `core/policy` (`Engine.Ancestors`):

- **What's cached:** each resource's *direct* owners, kept across
  requests. A chain is assembled from those, so siblings share every
  level above them.
- **Invalidation:** a change event for a resource drops only that
  resource's entry, the only owner list that can have changed.
  Reconcilers' status updates therefore don't empty the cache.
- **The race:** a generation counter stops a lookup that raced a change
  from storing what it read before the change.
- **The periodic 30 s reload** clears the cache as well, which covers
  writes from outside the process.
- **Test:** `TestOwnershipCacheFollowsMoves` checks that moving an
  Application out of a Project takes effect on the next decision.

## Simulated T470 (`bench-simulated-t470.txt`, `../2026-10-18-spec017-simulated-t470/`)

Plan decision 12 asks for every target to be re-measured on the
simulated T470:

- **Per-request and list latency.** The same test binary
  (`GOOS=linux`) ran in a container with the T470 simulation's exact CPU
  limits (`docker run --cpus 2 --cpuset-cpus 0-3`, the `kindest/node`
  image). That is what `kind-t470-setup.sh` applies to the kind node.
  The benchmark needs no cluster. This was added in T011, after the
  contract review noted that only idle memory had been re-measured on
  the T470. Results, 3 runs × 3 s, medians:

  | Request | p99 without → with | p99 added | Mean added | Target | Verdict |
  |---|---|---|---|---|---|
  | One Component | 159 → 195 µs | 36 µs | ~10 µs | < 100 µs | PASS |
  | 100-item list | 2.34 → 2.58 ms | 0.24 ms | ~0.21 ms | < 2 ms | PASS |

- **Idle memory:** the T470 footprint run (T010's cluster) had
  `serendipd` at 36.3 MiB and 0.01% CPU idle, up from 34.5 MiB in spec
  016: **+1.8 MiB** against the < 5 MiB target, **PASS**. It includes
  the console's own growth since spec 016. Three polling console tabs ran
  at 1.32 requests/s with 0 errors.
