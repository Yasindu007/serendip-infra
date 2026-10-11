> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-11 14:46 +0530

## Machine

- Host: windows/amd64, AMD Ryzen 7 7840HS with Radeon 780M Graphics
- Node: bench-t470-control-plane (Debian GNU/Linux 12 (bookworm), kernel 6.6.87.2-microsoft-standard-WSL2, containerd://2.1.3, kubelet v1.34.0)
- Node capacity reported by Kubernetes: 4 CPU, 8133868Ki memory
- Simulated: 2.0 CPUs of quota on cpuset 0-3. kind node limited to 4 threads (cpuset 0-3) and 2.0 CPUs of quota to approximate an i5-6300U (2c/4t) against Ryzen 7 7840HS Zen 4 threads; calibration approximate
- Not simulated: 16-32 GB RAM (Docker VM has 7.8 GB
- Not simulated: memory is measured, not limited)
- Not simulated: SATA/NVMe disk speed (no reliable I/O throttling on Docker Desktop/WSL2)
- Not simulated: Docker VM overhead is outside the node totals
- Settle 180s, then 12 samples every 10s

### Platform at idle (no workloads)

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 799.9 | 814.6 | 149.1 | 165.2 |
| Secrets (OpenBao) | 17.8 | 17.9 | 10.5 | 11.7 |
| Certificates (cert-manager) | 65.3 | 72.5 | 2.6 | 3.6 |
| Application platform: control plane | 53.9 | 54.3 | 4.6 | 6.0 |
| Application platform: data plane | 6.4 | 6.5 | 0.0 | 0.1 |
| Gateway (kgateway) | 51.9 | 54.4 | 6.4 | 8.8 |
| **Node total** | 1407.0 | 1433.0 | 228.9 | 275.7 |

Node disk used: 61247 MiB (container images: 1985 MiB)

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 25.0 | 25.5 |
| CPU (millicores) | 0.0 | 0.0 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 25.0 MiB: **PASS**
- CPU 0.00% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 752.6 | 790.3 | 147.9 | 167.9 |
| Secrets (OpenBao) | 18.5 | 18.6 | 10.0 | 10.9 |
| Certificates (cert-manager) | 62.0 | 62.0 | 2.5 | 3.4 |
| Application platform: control plane | 61.3 | 65.3 | 5.4 | 6.0 |
| Application platform: data plane | 7.5 | 7.6 | 0.0 | 0.2 |
| Gateway (kgateway) | 54.6 | 56.9 | 6.9 | 8.8 |
| Workloads | 13.6 | 13.6 | 0.0 | 0.0 |
| **Node total** | 1417.1 | 1458.4 | 229.0 | 251.6 |

Node disk used: 61301 MiB (container images: 2035 MiB)

### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1432 MiB + 10 × 3.4 MiB per Component = 1466 MiB (10% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 203s
- Warm restart (everything installed): 33s
- Component create → Serving: 30s, 30s, 30s (includes up to one 30s resync)
