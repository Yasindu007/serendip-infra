# Plan: Spec 013 — edge-1 benchmark harness + simulated T470 run

Status: Draft — human approval required before implementation

No upstream product research is needed. Everything measured is reachable
through standard Kubernetes APIs and the OS, and every component's version
is already pinned in the repo (`VERSIONS.md` files, `agent/bootstrap`).

## Facts about the machines involved

| | Reference T470 (target) | This dev machine (simulation host) |
|---|---|---|
| CPU | Intel i5-6300U: 2 cores / 4 threads, Skylake, 2.4–3.0 GHz | AMD Ryzen 7 7840HS: 8 cores / 16 threads, Zen 4 |
| What Kubernetes sees | 4 threads, the whole machine | Docker Desktop VM: **4 vCPUs**, cgroup v2 |
| RAM | 16–32 GB | Docker VM: **7.8 GB** |
| Disk | SATA/NVMe SSD | NVMe through the WSL2 VM |
| Kubernetes | K3s v1.36.4 (pinned, ADR 0001) | kind, node image v1.34.0 |

## Resolved design decisions

### 1. Simulation constraints: throttle CPU, measure memory, don't fake disk

The setup script creates the kind node, then applies
`docker update --cpus=2 --cpuset-cpus=0-3` before anything is installed.

- **CPU, simulated.**
  - 4 schedulable threads match the T470's 2c/4t, and kubelet still sees 4
    CPUs, as on a T470.
  - A 2.0-CPU quota across them approximates a Skylake 2c/4t having roughly
    half the throughput of four Zen 4 threads.
  - **The calibration is rough and stated as such.** It is a public-benchmark
    ballpark, not a measurement, so the results label it "approximate CPU
    class".
  - CPU-bound numbers (startup time, CPU%) get the most uncertainty.
    Memory numbers barely depend on it.
- **Memory: measured, not limited.**
  - A 16 GB limit can't be applied inside a 7.8 GB VM, so the harness
    reports what the stack *uses*.
  - Usage barely depends on how much RAM is available until there's
    pressure. The 16 GB headroom question (decision 7) is then answered
    arithmetically from usage.
  - A deliberately tight limit as a stress point is **not** run here: a
    node OOM-kill would destabilize the cluster mid-benchmark and measure
    the failure, not the footprint.
- **Disk: not simulated.** Docker Desktop on WSL2 doesn't reliably apply
  block-I/O throttling (`--device-read-bps` needs a real block device
  path). Cold-start time is therefore labeled "dev-machine disk"; image
  pulls dominate it anyway (decision 6).

The limits are applied by a kind-specific setup script, kept separate from
the harness (acceptance criterion 2).

### 2. Measurement source: the kubelet Summary API, read through the API server

- The source is `GET /api/v1/nodes/{node}/proxy/stats/summary`, standard on
  every conformant kubelet, kind and K3s alike. It returns, per pod and per
  container:
  - `memory.workingSetBytes`, the metric the kubelet itself uses for
    eviction, so the honest "in use" figure;
  - `cpu.usageNanoCores`.
- It also returns node totals and filesystem usage (`node.fs`,
  `runtime.imageFs`).
- **Why it beats the alternatives:**
  - No extra component. metrics-server would add its own footprint and
    distort the measurement.
  - No `docker exec`/`crictl` (kind-specific; spec 012's ad-hoc method).
  - It works unchanged on the real T470's K3s node.
- **Access:** the harness reads it with the existing
  `providers/kubernetes.Client` (`Get` with a path), using the same
  kubeconfig `serendipd` uses. It needs `nodes/proxy` permission, which the
  admin kubeconfigs for kind and K3s both have.
- **Known limit, stated in results:** on kind, "node" totals are the kind
  node container, not the Docker VM. On K3s they're the host.

### 3. Harness form: a Go program, `cmd/bench-edge1`, plus a README and a setup script

- A Go program mirrors the precedent of `cmd/bench-resource-model`. It
  needs no new dependency and is unit-testable.
- The same binary runs on Windows (this simulation) and Linux (the real
  T470) unchanged; a shell script would need two versions.
- **What the program does:**
  1. Starts `serendipd` itself, from a given binary path, with a fresh
     database and given kubeconfig/env.
  2. Times a **cold start** to the "listening on" log line.
  3. Waits until every pod in every namespace is Ready (Jobs: Complete).
  4. Waits a settle period.
  5. Samples platform idle.
  6. Deploys the light workload through `serendipd`'s own API.
  7. Waits for `Serving`, settles, and samples again.
  8. Stops and restarts `serendipd` and times a **warm restart**.
  9. Deletes the workload.
  10. Writes the results.
- **Measuring `serendipd`'s own process** (it runs on the host, outside the
  cluster), using no dependencies, following spec 001's pattern:
  - **Linux:** RSS from `/proc/<pid>/status` `VmRSS`; CPU from `/proc/<pid>/stat`
    utime+stime deltas over the sampling window.
  - **Windows:** `Get-Process -Id <pid>` through PowerShell, using
    `WorkingSet64` and deltas of `TotalProcessorTime`.
- **`serendip-agent`:** reported as "not a long-running daemon yet, not
  measured", not as a made-up number. The rule-4 line covers `serendipd`
  only, and says so.
- **Outputs:** `result.json` (every sample plus the machine profile) and
  `summary.md` (the tables), both written to an output directory.

### 4. Settling and sampling

- **Ready:** every pod Ready (or Succeeded, for Jobs) **and** the
  application platform's and gateway's own readiness already implied by
  `serendipd` logging "listening on" (its startup blocks on them).
- **Settle period:** 3 minutes by default (flag), so startup spikes, image
  GC and controller resyncs pass before sampling.
- **Sampling:** 12 samples, 10 s apart (2 minutes), by default. The kubelet
  computes `usageNanoCores` over its own housekeeping interval, so faster
  sampling only resamples the same value.
- **Reported per group:** memory mean and max, CPU mean and max.
- **CPU units:** millicores (1000 = one full thread), plus percent of node
  capacity. For the node, capacity is the 2.0-CPU quota on the simulation,
  stated explicitly, and all 4 threads on a real T470.

### 5. Component grouping: by namespace, from constants the code already pins

| Group | Namespaces |
|---|---|
| Serendip control plane | the `serendipd` host process (not in-cluster) |
| Kubernetes system | `kube-system`, `local-path-storage` (kind's provisioner), `kube-node-lease`, `kube-public` |
| Secrets (OpenBao) | `serendip-system` |
| Certificates | `cert-manager` |
| Application platform (control plane) | `openchoreo-control-plane` |
| Application platform (data plane) | `openchoreo-data-plane` |
| Gateway | `kgateway-system` |
| Workloads | namespaces prefixed `dp-` (OpenChoreo cell namespaces) |
| Other | anything unmatched, listed by name, so drift is visible rather than hidden |

- The namespaces come from constants in the provider packages
  (`ControlPlaneNamespace`, `kgateway.Namespace`, ...). The harness imports
  those constants, so a rename can't silently miscount.
- Storage (local-path on K3s) runs in `kube-system` on K3s, so on K3s it
  falls under Kubernetes system. The results note this.
- Per-pod detail is included in `result.json` under each group, so any
  number can be traced to pods.

### 6. Timings

- **Cold start:** from a freshly created cluster to "listening on". It
  includes image pulls, so it is network-bound, and is labeled that way.
- **Warm restart:** `serendipd` stopped and started against the
  already-installed cluster. All Helm upgrades are no-ops and the Gateway
  API download is skipped. This is the CPU/API-bound number, most
  comparable to a real T470.
- **Workload time-to-Serving:** per light-workload Component, measured from
  the create call to `Reachable=Serving`. Like spec 012's verification, it
  includes up to one 30 s resync lag, and is reported with that caveat.
  Spec 014 targets exactly this lag.

### 7. Light workload and the 16 GB headroom rule

- **Workload:** 3 Components in one Application, each `nginx:1.27-alpine`
  with one exposed port. That's small, real, and goes through the gateway.
- **Workload delta:** (workload sample − platform sample), reported
  per-Component.
- **"Reasonable headroom" on 16 GB**, defined up front, not fitted to the
  result:
  - Budget = 16 GB − 2 GB for the host OS (a headless Ubuntu server plus
    K3s's own host-side processes) = 14 GB.
  - Projected = platform idle working set + 10 × per-Component delta.
    Ten small apps is a modest edge-1 load.
  - **Fits comfortably** if projected ≤ 50% of the budget (7 GB),
    **tight** if ≤ 75% (10.5 GB), otherwise **points to 32 GB**.
  - Both the inputs and the arithmetic are printed.

### 8. Repeatability tolerance

- Two complete back-to-back runs, each on a fresh cluster. Both are
  recorded.
- **Memory:** each group of ≥ 50 MB must agree within **±10%** between
  runs. Smaller groups are compared absolutely, within ±10 MB, since
  percentages are meaningless for tiny numbers.
- **CPU at idle:** compared absolutely, within **±25 millicores** per
  group, because idle CPU is small and spiky.
- **Timings:** reported, not compared. Cold start depends on network pulls.
- A run that breaks tolerance is reported as such, not discarded.

### 9. Where results go, and how claims are evaluated

- The two-run raw output goes under
  `profiles/edge-1/bench/results/2026-10-11-simulated-t470/`.
- `profiles/edge-1/bench/002-edge1-footprint.md` records them, styled
  after `001-resource-model.md`:
  - the simulation banner;
  - the machine profile;
  - what was and wasn't simulated;
  - the tables;
  - the rule-4 line;
  - the 16 GB verdict;
  - the evaluation of ADR 0005 / ADR 0011 claims (consistent, inconsistent,
    or not yet decidable).
- `profiles/edge-1/bench/README.md` covers how to run the harness, on kind
  (simulation) and on a real K3s node (the future T470 run).
- ADR 0005 and ADR 0011 get a link to the result, and their scattered
  one-off numbers point to it.

## Files touched

- **New `cmd/bench-edge1/`:**
  - `main.go`: flags and the run sequence.
  - `summary.go`: Summary API types, grouping, statistics.
  - `proc_linux.go` / `proc_windows.go` (build tags): process RSS/CPU.
  - `serendipd.go`: start/stop/restart, the "listening on" wait, API calls.
  - `report.go`: JSON and markdown output.
  - Tests for grouping, statistics, Summary parsing, headroom math,
    tolerance comparison, and markdown rendering.
- **New `profiles/edge-1/bench/`:**
  - `README.md`;
  - `kind-t470.yaml` (kind config with the 30080 mapping);
  - `kind-t470-setup.sh` (create the cluster, apply the `docker update`
    limits, record them);
  - `002-edge1-footprint.md`;
  - `results/2026-10-11-simulated-t470/{run1,run2}/{result.json,summary.md}`.
- **Docs:**
  - ADR 0005 and ADR 0011: link to results.
  - `application-platform.md`, `gateway.md`: replace one-off numbers with a
    pointer.
  - `profiles/README.md`: note that bench results may be simulations if
    labeled.
  - `docs/PROGRESS.md`.
- **No `core/`, `api/`, `providers/` or `serendipd` code change.** The
  harness only imports provider *constants* for namespace grouping, plus
  `providers/kubernetes.Client`, the way `cmd/serendipd` already does.
- **No new Go dependency.**

## Risks

1. **CPU calibration is approximate** (decision 1), and the results say so
   prominently. Memory, the purchase-relevant number, is largely
   insensitive to it.
2. **Docker Desktop VM overhead** (WSL2 kernel, Docker daemon) is invisible
   to the Summary API, so the node totals understate what a whole machine
   would use. On the real T470, K3s's node totals include the host.
   The 2 GB host-OS reserve in decision 7 is meant to cover this.
3. **Cold-start numbers are network-dependent**, since image pulls dominate
   them, so they are labeled and not tolerance-checked.
4. **`kubectl`-style mutations.** The harness doesn't use kubectl or helm.
   The setup script uses `kind`/`docker` only. Cluster teardown is `kind
   delete`. The kube-context guard hook isn't triggered.
5. **Throttled installs may hit timeouts.** Helm `--wait` timeouts (5–10 min)
   and the gateway's 2-minute programmed wait could trip under a 2-CPU
   quota. If that happens, it is a real finding about T470 fitness,
   recorded as such, and not "fixed" by raising timeouts inside this spec.
   Timeout tuning belongs to spec 014.

## Explicitly NOT done

Everything in spec.md's Scope-OUT, plus:

- No memory-limited stress run (decision 1).
- No disk throttling (decision 1).
- No metrics-server, Prometheus, or any in-cluster agent (decision 2).
- No changes to `serendipd`, providers, or timeouts, even if the throttled
  run surfaces problems (risk 5). Findings are recorded for spec 014 and
  later specs.

## Corrections from the runs (T006–T009)

- **A pilot run before runs 1–2.** It exposed three report gaps, all fixed
  before the counted runs:
  - about 400 MiB of node memory outside any pod wasn't shown, so a
    "node overhead outside pods" row was added;
  - per-pod numbers were missing from `result.json` (decision 5 promised
    them), so they were added;
  - the node-total workload delta was swamped by kube-system GC noise, so
    the per-Component cost is now the workload pods plus positive growth in
    platform groups, excluding Kubernetes system.

  The pilot is kept in `results/.../pilot/` for transparency.
- **Run 2's first attempt failed for a non-CPU reason.** A transient quay.io
  image-pull `EOF` outlasted OpenBao's 90 s readiness wait and `serendipd`
  exited. This is recorded as a finding for spec 014 and kept in
  `run2-attempt1-failed/`.
- **That failure also exposed a harness hang**: the deferred stop waited on a
  one-shot exit signal the start-up wait had already consumed. Fixed with a
  closed `done` channel, with a regression test. Run 2 was then repeated on a
  fresh cluster.
- **go-reviewer findings fixed:**
  - a `serendipd` crash mid-run now aborts sampling instead of reading as
    zero usage; rule 4 reports NOT EVALUATED without process data;
  - partial results are written on any abort, and the light workload is
    always deleted;
  - the 16 GB verdict is withheld without workload data;
  - Components are timed concurrently;
  - empty IDs fail fast;
  - negative node overhead is flagged;
  - zero node-CPU samples are skipped.

  The recorded runs hit none of these paths. Summaries were re-rendered with
  the final renderer (`bench-edge1 render`).
- **Repeatability (decision 8): memory PASS in every group. CPU FAIL in one
  group** (Kubernetes system, 25.9 vs 25 mcores). It is kept as recorded, and
  the explanation is in `002-edge1-footprint.md`.
