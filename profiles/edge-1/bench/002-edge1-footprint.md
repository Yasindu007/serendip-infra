# Benchmark: edge-1 stack idle footprint (spec 013)

> **SIMULATION — does not satisfy the reference-hardware gate.** These runs
> used a kind node on the dev machine, constrained to approximate a
> ThinkPad T470's CPU class. No footprint here may be reported as verified
> (constitution rule 6). The rule-9 gate, and ADR 0005's named benchmark
> gate, stay open until the same harness runs on a real T470 (see
> `README.md`).

Measured 2026-10-11 with `cmd/bench-edge1`. Raw data:
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

## Findings carried forward

1. **Status lag dominates time-to-`Serving`** (always exactly 30 s). Spec 014
   (reconciliation hardening) targets this with faster re-checks while
   something is progressing.
2. **A transient image-pull failure kills `serendipd`'s startup.** Run 2's
   first attempt failed when the node's pull of the OpenBao image from
   quay.io hit a network `EOF`. The image pulled fine on retry 16 s later,
   but by then OpenBao's 90 s readiness wait had expired and `serendipd`
   exited (`run2-attempt1-failed/serendipd-failure.txt`). Startup should
   retry provider setup instead of exiting on the first slow pull. That's a
   spec 014 candidate. This was not a CPU-throttling effect: the same
   install finished in about 200 s on every other attempt.
3. **The API server is the biggest lever on footprint.** Fewer CRDs (for
   example not installing unused OpenChoreo or Gateway API kinds) would help
   more than trimming any Serendip component. This needs measurement on K3s
   before acting.
4. **Traefik's idle cost on K3s is unmeasured** (finding 3's caveat). It is
   the first thing the real T470 run should quantify.
5. **The harness had a hang**, fixed during this spec: after `serendipd`
   exited early, the harness's deferred stop waited forever on a one-shot
   signal. It now uses a closed channel and has a regression test.
