# Spec 022 T013: edge-1 footprint on the simulated T470, and the builds package's cost

> **SIMULATION — does not satisfy the reference-hardware gate.**

Every run used a fresh `bench-t470` kind cluster (2.0 CPUs of quota on
cpuset 0-3, 180 s settle, 12 platform samples every 10 s). The host's
gateway port was mapped to 30081 (`KIND_CONFIG`), because `serendip-t018`
was running beside every run and holds 30080.

| Run | Date | What |
|---|---|---|
| `no-package/` | 2026-11-06 | edge-1 without the builds package, against spec 020's run (the regression check) |
| `package-full-crds/` | 2026-11-09 | `bench-edge1 --package openchoreo-workflow-plane`, with the chart's default **full** Argo CRDs. Kept for comparison |
| `package/` | 2026-11-09 | the same run with Argo's **minimal** CRDs. **Its numbers are the package's stated cost** |

The package runs used a `serendipd` built with `go build -overlay` and a
placeholder `Requirements`, because the catalogue refuses to install a
package with no measured cost. Everything else was the working tree.

## Without the package: no regression (PASS)

`compare-no-package-vs-spec020.md` compares against
`2026-10-23-spec020-simulated-t470/run1`, and every group is within
tolerance. Kubernetes system was 859.1 → 814.9 MiB.

- **`serendipd`:** 29.2 MiB RSS idle, 0% CPU. Rule 4: **PASS**.
- **Cold start:** 285 s. **Warm restart:** 31 s.

## With the package

What the package run does, after the platform's idle sample:
1. installs the package through the API (create → Ready);
2. 10 min idle, sampled every 10 s;
3. two builds of `openchoreo/sample-workloads/service-go-greeter`
   (the first with a cold node image cache, the second warm), sampled
   every 3 s;
4. 5.5 min with auto-build on;
5. one full Helm run by itself, to compare with the fingerprint path.

| | Full CRDs | **Minimal CRDs** |
|---|---|---|
| Install (create → Ready) | 2m16s | **1m46s** |
| kube-apiserver at package idle (586 / 544 MiB without the package) | 1125 MiB | **637 MiB** |
| cert-manager cainjector at package idle (25 MiB without) | 83 MiB | **30 MiB** |
| The package's own pods, idle | 27–34 MiB, 2–3 mcores | **27–34 MiB, 2–3 mcores** |
| Build, cold image cache (harness / running) | 4m10s / 3m50s | **3m49s / 3m30s** |
| Build, warm image cache | 3m31s / 2m59s | **2m30s / 2m16s** |
| Node CPU while building | ~2000 mcores (the quota) | **~2000 mcores (the quota)** |
| `serendipd` with the package idle | 30.2 MiB | **31.0 MiB** |
| Resync on the fingerprint path | +0.53 mcores | **+0.44 mcores** |
| Auto-build polling | +0.22 mcores | **−0.06 mcores (noise)** |
| One full Helm run | 23 s, peak 1288 mcores | **5 s** (no CRD download Job) |
| **Stated idle** | 954 MiB, 25 mcores | **453 MiB, 19 mcores** |
| **Stated peak** | 1711 MiB, 1752 mcores | **1129 MiB, 1787 mcores** |

### How the stated cost is derived

The stated figures are **the node's use beyond the same run's platform
phase**, never below the package's own pods. The harness's first rule
counted only the package's namespaces. That gave 34 MiB idle, and missed
that the full CRDs held kube-apiserver ~550 MiB higher. That's RP-046,
and why the package now installs the minimal CRDs.

- **Idle:** the larger of the mean before the builds and the mean after
  them (auto-build on).
- **Peak:** the largest sample during a build.

The ~450 MiB idle figure (minimal CRDs) breaks down as:
- **~35 MiB:** the package's own pods (Argo's controller, the cluster
  agent, the registry);
- **~50 MiB:** kube-apiserver for the CRDs;
- **~320 MiB:** node memory outside pods (the container runtime and file
  cache). It grows once builds have pulled ~384 MiB of build images
  (`imageFsUsed` 2332 → 2716 MiB). Part of this is cache the kernel can
  reclaim, but it's what the node held, so it's stated.

## Fit on a T470

Idle, the builds package costs about 450 MiB of a 16 GB T470: the
platform plus the package stays well under the budget. A build takes
both cores for 2–4 minutes and peaks at about 1.1 GiB on the node.
Everything else on the node slows down while a build runs. This is
`Requirements.Note`, which the console and `GET /v1/packages` show.

## Verified

`GET /v1/packages` on a `serendipd` built from the committed source
returns `idleMemoryMiB: 453, idleCPUMillicores: 19, peakMemoryMiB: 1129,
peakCPUMillicores: 1787`, the measurement place, this folder, and the
note, with `installable: true`.

## Notes on the runs

- **Cold starts were slow** on 2026-11-09: 590 s and 680 s, against 285 s
  on 2026-11-06. A cold start pulls every image, so this is the host's
  network, as on spec 020's day. It isn't part of the package figures,
  which are all taken after the cold start.
- **Two earlier attempts** were lost and aren't kept here. On 2026-11-08
  the lid was closed and Windows entered Modern Standby. Later the same
  day, a PowerShell process query failed (`0xc0000142`). The harness now
  retries that query.
- **Harness fixes from this task:** `sampleBuild` dropped its results;
  the package section never reached `summary.md`; and the stated cost
  now comes from the node level. `bench-edge1 render` recomputes the
  cost, so both package runs here state it the same way.
