# Spec 013: edge-1 benchmark harness + simulated T470 run

Status: Draft — human review required before plan.md

## Problem

Constitution rule 4 makes the ThinkPad T470 (i5-6300U, 2 cores / 4
threads, 16–32 GB RAM, SATA/NVMe SSD) the reference hardware. It sets a
hard budget for Serendip's own control plane: core + api + agent under
256 MB RSS combined and under 5% CPU at idle. Rule 9 requires benchmarks
to live in the repo under `profiles/*/bench/` and to run on reference
hardware before profile defaults change.

Three things now make this urgent:

- **The edge-1 stack has grown a lot, and none of it has been measured
  systematically.** Since the only benchmark so far (spec 001's 10k-resource
  CRUD smoke test), it has gained K3s defaults, local-path, OpenBao,
  cert-manager, OpenChoreo's control and data planes, and kgateway. Every
  number in the docs is a one-off reading taken by hand during a spec's
  verification. They were taken differently each time, mostly as a single
  kind-node total. Specs 010–012 each recorded "not the T470 benchmark".
- **The roadmap depends on the answer.** `docs/PRD.md` makes the
  dev-machine POC the gate before buying the first T470. Whether the stack
  plausibly fits a T470, and whether 16 GB is enough or 32 GB is needed, is
  a purchase decision no current data can inform.
- **ADR 0005 (OpenChoreo) and ADR 0011 (kgateway) carry footprint-based
  fallback conditions**, and nothing evaluates them.

A real T470 doesn't exist yet. So this spec builds two things:

1. A **reusable, repeatable benchmark harness** that measures the same
   things the same way every time, on any machine. It will run unchanged
   on the real T470 when one is bought.
2. **One run on a dev-machine node constrained to approximate the T470.**
   It is clearly labeled as a simulation that informs the purchase
   decision and does **not** satisfy the rule-9 reference-hardware gate
   (rule 6: no capability's footprint is reported as verified on the
   strength of a simulation).

## Scope — IN

- **A benchmark harness under `profiles/edge-1/bench/`.** It is runnable
  on any machine with the same host prerequisites `serendipd` already
  needs, and produces a machine-readable result file plus a human-readable
  summary. It measures, for the full edge-1 stack as `serendipd` deploys
  it:
  - **Idle memory and CPU per component**, grouped by capability and
    platform piece: Serendip's own control plane; K3s/kubelet system pods;
    storage; secrets; cert-manager; the application platform's control
    plane and data plane; the gateway. Measured after a defined settle
    period, sampled over a window rather than read once.
  - **`serendipd`'s own idle RSS and CPU**, reported against rule 4's
    control-plane budget as an explicit pass/fail line. Today the budget
    covers `serendipd` only; `serendip-agent` isn't a long-running daemon
    yet, and the harness says so rather than inventing a number.
  - **Whole-node totals**: memory in use, CPU at idle, and disk used by
    images and data.
  - **Timings**: a cold first `serendipd` start (all installs) and a warm
    restart (everything already installed).
  - **A light workload delta**: footprint with a small number of deployed,
    exposed Components versus none, so each workload's cost is visible,
    not just the platform's.
- **A machine profile recorded with every result.** CPU model, core and
  thread count, any applied CPU or memory limits, total RAM, OS/kernel,
  container runtime, Kubernetes version, and pinned component versions.
  Results are never ambiguous about where they came from.
- **A simulated-T470 run on this dev machine.** A kind node constrained to
  approximate the T470's CPU class. The run records exactly which
  constraints were and were not possible, for example memory or disk
  limits that Docker Desktop can't apply here, instead of claiming a
  fidelity it doesn't have.
- **Recorded results**: a results file under `profiles/edge-1/bench/` in
  the style of `001-resource-model.md`, with the simulation labeled at the
  top. It includes an explicit answer to the purchase question (does the
  measured idle footprint leave headroom on 16 GB, or does it point to
  32 GB), and evaluates the existing footprint claims and fallback
  conditions in ADR 0005, ADR 0011, and rule 4 against the numbers.
- **Docs sync.** Replace the scattered one-off footprint numbers in
  `docs/architecture/*.md`, the ADRs, and `PROGRESS.md` with a pointer to
  the recorded benchmark, keeping each one's "not reference hardware"
  label.

## Scope — OUT (explicitly)

- **Satisfying the rule-9 / ADR 0005 reference-hardware gate.** Only a run
  on a real T470 does that. This spec makes that run a single command
  later; it doesn't fake it.
- **Optimizing or trimming the footprint.** No component is removed,
  resized, or replaced here; for example Traefik stays enabled, and
  OpenChoreo's and kgateway's resource requests are untouched. Findings
  feed later specs. Measuring and changing in the same spec would muddy
  what was measured.
- **Benchmarking small-3 or standard-5**, multi-node behavior, or HA
  failover timing.
- **Load or throughput testing** of workloads or the API (requests per
  second, latency under load). Idle footprint plus a light-workload delta
  only.
- **The observability backend comparison** (OpenObserve vs SigNoz vs
  OpenSearch) that `capability-model.md` defers to on-hardware
  benchmarking. None of them is installed yet.
- **Continuous or CI benchmarking.** The harness is run by hand. Wiring it
  into CI, and tracking regressions over time, is later work.
- **Measuring on a real K3s node** instead of kind. The simulation uses
  kind, the established verification path. The harness itself must not
  assume kind, so the real T470 run uses K3s.

## Acceptance criteria

1. **One command runs the whole benchmark.** A documented single command
   under `profiles/edge-1/bench/` runs end to end against a given cluster.
   It produces a machine-readable result file and a human-readable summary
   containing every measurement in Scope-IN and the full machine profile.
2. **The harness is portable.** It makes no kind-specific assumption in
   how it measures components. Anything kind-specific (creating the
   constrained node) is a separate, clearly named setup step, so the same
   harness can run against a real K3s node. Plan.md describes how it will
   reach that node.
3. **Repeatability.** Two back-to-back simulated-T470 runs agree within a
   tolerance that plan.md defines and justifies, for example idle memory
   per group within ±10%. Both runs are recorded, not just the better one.
4. **The simulation is honest about its constraints.** The recorded result
   states which T470 characteristics were simulated (and how) and which
   were not. Every summary carries the label "simulation — does not satisfy
   the reference-hardware gate". No doc, ADR, or Condition reports a
   footprint as verified on the strength of it.
5. **Rule 4 is checked explicitly.** The result contains a pass/fail line
   for `serendipd`'s idle RSS and CPU against the 256 MB / 5% control-plane
   budget, measured in the constrained environment.
6. **The purchase question is answered.** The results file states, with the
   numbers behind it, whether the measured idle footprint plus the
   light-workload delta leaves reasonable headroom on a 16 GB machine.
   Plan.md defines "reasonable".
7. **Existing claims are evaluated.** ADR 0005's and ADR 0011's footprint
   statements and fallback conditions are each marked as consistent,
   inconsistent, or not yet decidable against the simulated numbers, and
   the ADRs link to the result.
8. **No product code changes are needed.** If the harness needs a hook in
   `serendipd` or a provider (for example a readiness signal it can't get
   otherwise), that change goes through the normal `contract-guardian`
   review and is called out explicitly. `go vet`, `gofmt -l .`,
   `check-imports.sh`, and `go test ./...` stay clean either way.

## Open questions (resolve during plan review)

- **How to approximate the T470 on Docker Desktop.**
  - CPU: limit the kind node to 2 or 4 CPUs (2c/4t), and possibly a CPU
    quota to approximate the i5-6300U's lower per-core performance.
    Calibrate against a published single-thread score, or keep it to a
    core-count limit only and say so.
  - Memory: Docker Desktop's VM here has about 7.8 GB, so a 16 GB T470
    can't be reproduced. Measure usage rather than impose the limit, and
    decide whether to also run a deliberately tight memory limit as a
    stress point.
  - Disk: is any I/O throttling possible here?
- **Where measurements come from without adding cluster weight.** For
  example, the container runtime's own per-container stats on the node
  (as the spec 012 run did) versus installing metrics-server, which adds
  footprint and would distort the measurement. Also how to read
  `serendipd`'s own process RSS portably on Windows and Linux; spec 001's
  benchmark already has a pattern for this.
- **Harness form.** A Go program under `cmd/` like `cmd/bench-resource-model`
  (portable, testable, no new dependency), a shell script, or both. Weigh
  against the T470 run happening on Linux and this one on Windows.
- **Settle period and sampling window.** How long after "everything
  Ready" before sampling, and how many samples over what window, so
  startup spikes don't distort idle numbers. Also how CPU% is normalized
  (per core vs per node).
- **Component grouping.** How each pod or container is attributed to a
  capability or platform group, by namespace or labels, without
  hard-coding names that drift between versions.
- **The light-workload definition.** How many Components, which image, and
  whether they're exposed through the gateway, so the delta is
  representative but cheap.
