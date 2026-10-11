# Tasks: Spec 013 — edge-1 benchmark harness + simulated T470 run

Ordered. `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). No `core/`, `api/` or `providers/` change is planned. If one
becomes necessary, it needs `contract-guardian` before commit (acceptance
criterion 8).

Dependency sketch: T001 ∥ T002 ∥ T003 → T004 → T005 → T006 → T007 → T008 →
T009.

## T001 [P] — Summary API parsing, grouping, statistics

- **Do**: In `cmd/bench-edge1/summary.go`:
  - Types for the kubelet Summary API: node CPU/memory/fs, plus per-pod
    namespace, name, containers, CPU `usageNanoCores` and memory
    `workingSetBytes`.
  - `groupFor(namespace)` using the provider packages' namespace constants
    (plan.md decision 5). Unmatched namespaces go to "other", by name.
  - Aggregation of one sample into per-group totals, and of N samples into
    mean/max per group.
  - Unit conversions: millicores, MiB.
- **Files**: `cmd/bench-edge1/summary.go`, `cmd/bench-edge1/summary_test.go`.
- **Verify**: `go test ./cmd/bench-edge1/...`.
  - A recorded-fixture Summary JSON parses.
  - Grouping maps each pinned namespace correctly, `dp-*` maps to
    workloads, and an unknown namespace lands in "other" by name.
  - Mean/max over samples, and nanocores → millicores, are exact.

## T002 [P] — Process stats for `serendipd` (Linux + Windows)

- **Do**: `processStats(pid) (rssBytes, cpuTime time.Duration, err)`:
  - Linux, behind a build tag: `/proc/<pid>/status` `VmRSS` and
    `/proc/<pid>/stat` utime+stime ÷ `CLK_TCK`.
  - Windows: `powershell Get-Process -Id <pid>`, using `WorkingSet64` and
    `TotalProcessorTime.TotalMilliseconds`.

  CPU% over a window is derived from two readings.
- **Files**: `cmd/bench-edge1/proc_linux.go`, `proc_windows.go`,
  `proc_test.go`.
- **Verify**: `go test ./cmd/bench-edge1/...`. Reading the test process's
  own PID returns RSS > 0 and a non-decreasing CPU time across a short busy
  loop. The Linux parsing is also tested against fixture text, so it's
  covered on Windows CI too.

## T003 [P] — Report rendering: JSON + markdown, headroom, tolerance

- **Do**:
  - The `result.json` schema: machine profile, simulation constraints,
    every sample, aggregates, timings, rule-4 line, and headroom.
  - `summary.md` rendering, carrying the simulation banner whenever
    constraints are set.
  - `headroom(platformBytes, perComponentBytes)` per plan.md decision 7
    (verdicts: comfortable / tight / points-to-32GB).
  - `compareRuns(a, b)` per decision 8.
- **Files**: `cmd/bench-edge1/report.go`, `cmd/bench-edge1/report_test.go`.
- **Verify**: `go test ./cmd/bench-edge1/...`.
  - Headroom thresholds hit exactly at the 50% and 75% boundaries.
  - `compareRuns` flags a +11% memory drift on a ≥ 50 MB group, and a
    +11 MB drift on a small group, but not +9%.
  - Rendered markdown always contains the banner text when simulated, and
    never claims "verified".

## T004 — `serendipd` lifecycle, readiness, workload, run sequence

- **Do**:
  - `serendipd.go`: start the binary with env (fresh DB, kubeconfig,
    API key), capture its log, wait for "listening on" with a timeout,
    stop, restart.
  - Readiness: list pods in all namespaces through `providers/kubernetes.Client`.
    Every pod must be Ready, or Succeeded for Job pods.
  - Workload: create one Application and 3 exposed nginx Components through
    `serendipd`'s HTTP API, wait for `Reachable=Serving` (recording
    time-to-Serving each), and cascade-delete them at the end.
  - `main.go`: flags (`--serendipd`, `--kubeconfig`, `--out`,
    `--settle`, `--samples`, `--interval`, `--sim-cpus`, `--sim-cpuset`,
    `--sim-note`), the machine profile (`runtime`, host CPU model via OS,
    node info from the API), and the full sequence in plan.md decision 3.
- **Files**: `cmd/bench-edge1/serendipd.go`, `cmd/bench-edge1/main.go`,
  `cmd/bench-edge1/serendipd_test.go`.
- **Verify**: `go build ./...`, `go vet ./...`. A unit test of the
  log-line wait, using a fake binary (a tiny test helper process printing
  "listening on" after a delay), covers timeout and success.
  `go run ./cmd/bench-edge1 --help` lists every flag.

## T005 — kind T470 setup script + bench README

- **Do**:
  - `kind-t470.yaml`: node image v1.34.0, the 30080 port mapping.
  - `kind-t470-setup.sh`: create the cluster, `docker update --cpus=2
    --cpuset-cpus=0-3 <node>`, print the applied limits read back from
    `docker inspect`, write the kubeconfig to a given path. A separate
    teardown command does `kind delete`.
  - `README.md`: what is measured, how to run it on kind (simulation) and
    on a real K3s node (the T470: no setup script, point
    `--kubeconfig`/`--serendipd` at the host), how to read results, and
    the honesty rules.
- **Files**: `profiles/edge-1/bench/kind-t470.yaml`, `kind-t470-setup.sh`,
  `README.md`.
- **Verify**: Running the setup script creates the cluster, and
  `docker inspect` shows `NanoCpus=2000000000` and `CpusetCpus=0-3` on the
  node. Tear it down; the unrelated `urlshortener` cluster is untouched.

## T006 — Simulated T470 run 1

- **Do**:
  1. Build `serendipd` and `bench-edge1`.
  2. Run the setup script, then the harness with
     `--sim-cpus 2 --sim-cpuset 0-3`, writing to
     `profiles/edge-1/bench/results/2026-10-11-simulated-t470/run1/`.
  3. Watch for installs timing out under throttling. If they do, record it
     as a finding (plan.md risk 5) and don't change timeouts.
  4. Tear down.
- **Files**: results output only.
- **Verify**: `result.json` and `summary.md` exist, with every Scope-IN
  measurement present:
  - per-group mean and max;
  - node totals and disk;
  - `serendipd` RSS/CPU and the rule-4 line;
  - cold start and warm restart times;
  - 3 time-to-Serving values;
  - workload delta;
  - headroom verdict;
  - machine profile with constraints.

  The cluster is deleted afterward.

## T007 — Simulated T470 run 2 + repeatability check

- **Do**: Repeat T006 exactly on a fresh cluster, into `run2/`. Run
  `bench-edge1 compare run1 run2` (a subcommand built on T003's
  `compareRuns`) and save its output.
- **Files**: results output only.
- **Verify**: Both runs are recorded. The comparison output exists and
  reports per-group pass/fail against plan.md decision 8. Any failure is
  kept and explained, not rerun away (acceptance criterion 3).

## T008 [A] — Results write-up + docs sync

- **Do**:
  - Write `profiles/edge-1/bench/002-edge1-footprint.md`:
    - simulation banner;
    - machine profile;
    - simulated vs not simulated;
    - tables from both runs and the repeatability result;
    - rule-4 pass/fail;
    - the 16 GB verdict with its arithmetic;
    - ADR 0005 / ADR 0011 claims each marked consistent, inconsistent, or
      not yet decidable;
    - findings worth carrying into spec 014 and later, such as the cost of
      Traefik idling, any throttling timeouts, the time-to-Serving lag.
  - Link it from ADR 0005 and ADR 0011, and point
    `application-platform.md` / `gateway.md` footprint mentions to it.
  - Update `profiles/README.md` and `docs/PROGRESS.md`.
- **Files**: the write-up, the ADRs, the docs above.
- **Verify**:
  - Every number in the write-up appears in a `result.json`.
  - No doc says "verified" for a footprint.
  - The banner is present at the top of the write-up.

## T009 — Final gate

- **Do**: `go test ./...`, `go vet ./...`, `gofmt -l .`,
  `check-imports.sh`. A `go-reviewer` pass on `cmd/bench-edge1`, for
  portability and correctness of the measurement math. Confirm no
  `core/`/`api/`/`providers/` diff exists (otherwise run `contract-guardian`).
  Re-read spec.md's acceptance criteria against the outputs.
- **Files**: none (gate).
- **Verify**: All green, `go-reviewer` PASS, all 8 criteria individually
  confirmed.
