# Benchmark: edge-1 stack idle footprint (spec 013)

> **SIMULATION — does not satisfy the reference-hardware gate.** These runs
> used a kind node on the dev machine, constrained to approximate a
> ThinkPad T470's CPU class. No footprint here may be reported as verified
> (constitution rule 6). The rule-9 gate, and ADR 0005's named benchmark
> gate, stay open until the same harness runs on a real T470 (see
> `README.md`).

Measured 2026-10-11 with `cmd/bench-edge1`; re-run 2026-10-17 after spec
014 (see "Spec 014 re-run" below). Raw data:
`results/2026-10-11-simulated-t470/` (`run1/`, `run2/`, `comparison.md`;
`pilot/` and `run2-attempt1-failed/` are kept for transparency).

## What was simulated, and what wasn't

| T470 trait | This run |
|---|---|
| 2 cores / 4 threads (i5-6300U) | **Simulated, approximately.** The kind node was pinned to 4 threads (`cpuset 0-3`) with a 2.0-CPU quota, on a Ryzen 7 7840HS (Zen 4). The ratio is a rough public-benchmark ballpark, not a calibration. |
| 16–32 GB RAM | **Not simulated.** Docker Desktop's VM has 7.8 GB. Memory is *measured*, not limited, and the 16 GB question is answered from usage. |
| SATA/NVMe disk speed | **Not simulated.** There is no reliable I/O throttling on Docker Desktop/WSL2. |
| Host OS overhead | **Outside the numbers.** On kind the "node" is a container, so the Docker VM's own overhead isn't counted. The 16 GB budget reserves 2 GB for the host OS. |
| K3s | **Not used.** kind (Kubernetes 1.34, etcd) ran instead of K3s 1.36 (embedded datastore, control plane in one process). See "kind vs K3s" below. |

## Results: platform at idle (no workloads)

Means over 12 samples, 10 s apart, after a 3-minute settle. Memory is the
kubelet's `workingSetBytes`.

| Group | Run 1 memory (MiB) | Run 2 memory (MiB) | Run 1 CPU (mcores) | Run 2 CPU (mcores) |
|---|---|---|---|---|
| Kubernetes system | 775.5 | 815.8 | 154.4 | 180.3 |
| Secrets (OpenBao) | 18.0 | 17.8 | 9.8 | 10.8 |
| Certificates (cert-manager) | 65.8 | 65.3 | 2.4 | 6.9 |
| Application platform: control plane (OpenChoreo) | 58.0 | 54.7 | 4.9 | 11.2 |
| Application platform: data plane (OpenChoreo) | 6.7 | 6.6 | 0.0 | 0.1 |
| Gateway (kgateway) | 51.7 | 52.4 | 7.3 | 7.6 |
| Node overhead outside pods (kubelet, containerd, system) | 300.3 | 300.2 | 56.0 | 66.7 |
| **Node total** | **1275.9** | **1312.8** | **234.8** | **283.5** |
| `serendipd` (host process) | 25.3 | 23.4 | ~0 | ~0 |

Container images on disk: about 2.0 GB.

### Where the memory goes

- **The Kubernetes API server is the single largest consumer: 538 MiB in
  run 1 and 577 MiB in run 2, roughly 42–44% of the node.** Its memory
  scales with the number of CRDs and objects it serves. This stack installs
  about 60 CRDs (OpenChoreo, kgateway, Gateway API, cert-manager). etcd adds
  about 90 MiB and the controller-manager about 65–68 MiB.
- **Everything Serendip itself deploys is small**: OpenBao about 18 MiB,
  cert-manager about 65 MiB, OpenChoreo about 62 MiB (both planes), kgateway
  about 52 MiB, plus `serendipd` about 24 MiB. That's **about 220 MiB** of the
  roughly 1.3 GiB total.
- **Node overhead outside pods is about 300 MiB**: kubelet, containerd, and the
  node's system processes.

### Light workload

With 3 exposed `nginx:1.27-alpine` Components deployed, each costs **about
7–8 MiB**: its own pod (about 4.5 MiB) plus its share of growth in the
gateway and the OpenChoreo controllers (7.2 MiB in run 1, 8.4 MiB in run 2).

## Constitution rule 4: PASS

`serendipd` idles at **23–25 MiB RSS** against the 256 MiB control-plane
budget, and its idle CPU is too small to register. Two caveats:

- Windows charges process CPU time in roughly 15 ms quanta, so brief resync
  bursts don't show up. The Linux `/proc` reading on a real T470 is precise.
- The budget covers `serendipd` only. `serendip-agent` isn't a long-running
  daemon yet.

## The 16 GB question: fits comfortably

The rule, fixed in the plan before measuring: budget = 16 GB − 2 GB host OS
= 14,336 MiB. The projection is the idle platform plus 10 light Components:

- Run 1: 1,301 + 10 × 7.2 = **1,373 MiB (10% of budget)**
- Run 2: 1,336 + 10 × 8.4 = **1,420 MiB (10% of budget)**

**Verdict, both runs: fits 16 GB comfortably.**

Ten nginx containers are a very light load. Real applications are bigger,
so here is the same budget against heavier apps. At 256 MiB per app, about
**22 apps** still fit within the "comfortable" 50% line, and about **36**
within the "tight" 75% line. On this evidence a 16 GB T470 is enough for
edge-1 as deployed today. 32 GB would only matter for notably heavier
workloads, or once Backstage, Thunder, observability, or the Build Plane
are enabled (none are yet).

## Timings

| | Run 1 | Run 2 |
|---|---|---|
| Cold start: fresh cluster to `serendipd` listening (includes image pulls, so network-bound) | 200 s | 204 s |
| Warm restart: everything already installed | 37 s | 37 s |
| Component create to `Serving` | 30 s × 3 | 30 s × 3 |

Create to `Serving` is exactly one resync interval every time. The work
itself finishes well inside it, so the 30 s is entirely the reconcile
engine's status lag (finding 1).

## Repeatability

`comparison.md`: **memory PASS in every group.** Every group agreed within
the planned ±10% (or ±10 MiB), mostly within 3 MiB.

**CPU: one FAIL.** Kubernetes system idled at 154.4 mcores in run 1 and 180.3
in run 2, a difference of 25.9 against a ±25 mcore tolerance. That group is
the API server, etcd, and controller-manager. Their idle CPU tracks
background watch, lease, and resync traffic, so it moves run to run. Run 2's
settle was also stretched from 3 to about 18 minutes, most likely by the
host sleeping. The result is kept as a FAIL and not re-run away: idle
control-plane CPU on this stack is reproducible to about ±30 mcores, not
±25. Every other group passed.

## Evaluating existing claims

| Claim | Against these numbers |
|---|---|
| ADR 0005: OpenChoreo's "control plane alone wants ~2 CPU/4GB RAM minimum", "8GB/16GB min/recommended overall" | **Inconsistent, in the favorable direction, for the configuration Serendip deploys.** The control plane measured about 55–58 MiB and the data plane about 7 MiB, with Backstage, security (Thunder), the gateway block, and observability disabled. The estimate described a much fuller install. The whole edge-1 stack idles at about 1.3 GiB. The ADR's named gate (a real number on reference hardware) is **still open**. This is a simulation. |
| ADR 0005 fallback trigger "the T470 benchmark fails outright" | **Not triggered on this evidence**, but not decidable until the real T470 run. |
| ADR 0011: kgateway fallback if its "idle footprint is disproportionate for edge-1" | **Not triggered.** About 52 MiB (controller plus proxy) is about 4% of the node. That's consistent with ADR 0011's own earlier dev-machine reading (about 70 MiB, unthrottled). |
| Constitution rule 4: control plane < 256 MiB RSS, < 5% CPU idle | **Consistent** (`serendipd` 23–25 MiB). Formally decided only on reference hardware. |

## kind vs K3s: why the real T470 numbers will differ

- On K3s, the API server, scheduler, and controller-manager run inside the
  single `k3s` process, and the datastore is embedded rather than separate
  etcd. They show up under "node overhead", not "Kubernetes system".
- The total should be similar or somewhat lower (no separate etcd). The
  breakdown will look different.
- K3s also runs **Traefik and ServiceLB**, which kind doesn't. Their idle
  cost, a candidate for trimming since Serendip doesn't use Traefik (ADR
  0011), **can only be measured on K3s**.

## Spec 014 re-run (2026-10-17)

After spec 014 (reconciliation hardening: early re-checks, server-side
apply, degraded startup, foundations loop), the harness was re-run twice
under the same simulated-T470 constraints, each on a fresh kind cluster with
the Docker VM otherwise empty. Raw data: `results/2026-10-17-spec014-simulated-t470/`
(`run1/`, `run2/`; `comparison-run1.md` and `comparison-run2.md` compare each
against spec 013's run 1, `comparison-run1-vs-run2.md` the two re-runs).

| Group | Spec 013 run 1 | Spec 014 run 1 | Spec 014 run 2 |
|---|---|---|---|
| Kubernetes system | 775.5 | 785.8 | 790.6 |
| Secrets (OpenBao) | 18.0 | 18.0 | 18.0 |
| Certificates (cert-manager) | 65.8 | 64.8 | 64.2 |
| Application platform: control plane | 58.0 | 55.3 | 54.7 |
| Application platform: data plane | 6.7 | 6.4 | 7.4 |
| Gateway (kgateway) | 51.7 | 54.0 | 50.5 |
| Node overhead outside pods | 300.3 | 512.4 | 369.5 |
| **Node total** | **1275.9** | **1496.6** | **1354.8** |
| `serendipd` RSS (host process) | 25.3 | 34.8 | 26.3 |

Idle memory, MiB. Timings: cold start 182 s / 169 s, warm restart 36 s /
38 s, create → `Serving` 30 s × 3 / 32 s × 3.

- **Every pod group PASSes against spec 013's run 1, memory and CPU, in
  both re-runs**, and the two re-runs PASS against each other. Server-side
  apply and the new background loops cost nothing measurable in the cluster.
- **Rule 4: PASS** in both (`serendipd` 34.8 and 26.3 MiB RSS, idle CPU
  ~0). Run 1's higher RSS is within normal Go heap variation; it is still
  about 14% of the budget.
- **Node overhead outside pods moved** (300 → 512 / 370 MiB). That bucket is
  the node total minus every pod: kubelet, containerd and kernel memory
  inside the kind container, including page cache charged to it. No
  Serendip process runs there (`serendipd` is a host process), and it varies
  between two runs of the same code by 140 MiB, so it's noise in kind's
  accounting, not a regression. It's the least trustworthy number on kind
  and the one the real K3s run will replace.
- **16 GB verdict unchanged:** 11% / 10% of budget.
- **Create → `Serving` is still 30 s here, and that's explained, not a
  regression.** In both runs each fresh cluster pulled `nginx:1.27-alpine`
  first. The pull took 12–14 s (node events), which put the workload's
  readiness just past the engine's third early re-check (about 14 s after
  create; re-checks land at about 2, 6, 14 and 30 s). The next check is the
  30 s one. Re-probed on the same throttled node with the image cached,
  one Component reached `Serving` in 15.2 s and three created together in
  14.9–15.2 s. That's the same as spec 014's T010 kind measurement (14.2 s).

## Findings carried forward

1. **Time-to-`Serving` has a cliff between 14 s and 30 s.** Spec 014
   replaced the flat 30 s resync lag with early re-checks backing off
   2 → 4 → 8 → 16 s. Anything that becomes ready between about 14 s and
   30 s after create (here, any workload whose image isn't cached yet) is
   still reported at 30 s. A lower backoff cap while a resource is
   converging (for example 8 s, giving checks at 2, 6, 14, 22, 30 s) would
   halve the worst case for a few extra provider reads. That's a follow-up
   decision, not part of spec 014.
2. ~~**A transient image-pull failure kills `serendipd`'s startup.**~~
   **Fixed by spec 014 (degraded startup).** Provider setup now retries in
   the background (5 s → 2 m) instead of exiting. In spec 013, run 2's first
   attempt failed when the node's pull of the OpenBao image from quay.io hit
   a network `EOF`. OpenBao's 90 s readiness wait expired and `serendipd`
   exited (`run2-attempt1-failed/serendipd-failure.txt`). Spec 014's T010
   saw the same slow pull simply retry.
3. **The API server is the biggest lever on footprint.** Fewer CRDs (for
   example not installing unused OpenChoreo or Gateway API kinds) would help
   more than trimming any Serendip component. This needs measurement on K3s
   before acting.
4. **Traefik's idle cost on K3s is unmeasured** (finding 3's caveat). It is
   the first thing the real T470 run should quantify.
5. **The harness had a hang**, fixed during this spec: after `serendipd`
   exited early, the harness's deferred stop waited forever on a one-shot
   signal. It now uses a closed channel and has a regression test.
