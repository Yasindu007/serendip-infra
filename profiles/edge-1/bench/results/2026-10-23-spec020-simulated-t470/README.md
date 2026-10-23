# Spec 020 T014: edge-1 footprint on the simulated T470

> **SIMULATION — does not satisfy the reference-hardware gate.**

Two runs on the same host, the same day, each on a fresh `bench-t470` kind
cluster (2.0 CPUs of quota on cpuset 0-3, default harness settings: 180 s
settle, 12 samples every 10 s, 3 Components, 3 console tabs).

| Run | Code | Harness workload |
|---|---|---|
| `baseline-cecc037/` | spec 021's final commit, the last before spec 020 | an Application and 3 exposed Components (auto-deployed) |
| `run1/` | spec 020 through T013 | a Project, an Environment, an Application in the Project, 3 exposed Components and a Deployment of each |

`serendip-t018` was running beside both. Its gateway holds host port
30080, so `bench-t470`'s gateway was mapped to 30081 (`KIND_CONFIG`). The
harness never uses the host port.

## Verdict: no regression (PASS)

`compare-vs-baseline-cecc037.md`: every group is within tolerance.

| | Baseline | Spec 020 | |
|---|---|---|---|
| Kubernetes system (MiB) | 813.3 | 859.1 | PASS (+5.6%) |
| Serendip's groups (OpenBao, cert-manager, OpenChoreo, kgateway, ThunderID) | | | all PASS |
| `serendipd` idle RSS (MiB) | 34.4 | 35.6 | +1.2 |
| `serendipd` with 3 console tabs, RSS / CPU | 35.9 MiB / 1.6 mcores | 37.2 MiB / 2.3 mcores | |
| 16 GB headroom | 12% | 13% | comfortable |

**Rule 4:** `serendipd` at 35.6 MiB idle and 0.12% CPU with three tabs
polling: **PASS** (budget 256 MiB, 5%).

## Against spec 019's run (2026-10-20)

`compare-vs-spec019.md` fails on Kubernetes system (741.6 → 859.1 MiB).
That's the host, not spec 020: today's baseline, running code without
spec 020, measures 813.3 MiB on the same group. Every control-plane pod
(API server, etcd, controller manager, scheduler) grew alike with
unchanged CPU. Node overhead outside pods grew too, and cold starts were
slower all day (spec 019 300 s; today 579 s and 774 s). kind also failed
once to boot a fresh node. A comparison across days on this machine
isn't a fair one, which is why the baseline was run.

`serendipd` went from 27.4 MiB (spec 019) to 34.4 MiB (baseline) to 35.6
MiB (spec 020). Most of that came before spec 020; spec 020's own share
is about 1.2 MiB.

## What did change: create → Serving

| | Baseline | Spec 020 |
|---|---|---|
| Component create → Serving | 16 s, 24 s, 24 s | 70 s, 70 s, 70 s |

The run's audit log (kept outside the repository) shows where the time
goes:

- Each Component was released within 0.3 s of being created.
- The harness created each Deployment 2 s after that.
- Each Deployment read Progressing at once, and stayed Progressing until
  about 67 s after it was created. Then it became Ready with its endpoint
  in the same write.

That is the first deploy into a brand-new Environment: the platform makes
the Environment's data-plane namespace and the Application's binding to it
before the workload can start. The fast re-checks (about 2, 6, 14, 22 and
30 s) only last one resync interval (30 s). A Deployment still
progressing after that waits for the next 30 s resync. So up to about 30
s of this comes from the engine's schedule, not the platform. Recorded as
RP-035, with what to measure next.

The 30 s endpoint gap considered beforehand (Ready before reachable,
with no fast re-check) didn't happen here: Ready and the endpoint arrived
together.

## Files

- `run1/`, `baseline-cecc037/`: each run's `result.json` and `summary.md`.
- `compare-vs-baseline-cecc037.md`: the verdict.
- `compare-vs-spec019.md`: the cross-day comparison, explained above.
