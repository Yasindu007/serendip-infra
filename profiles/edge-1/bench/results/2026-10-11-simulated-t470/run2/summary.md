> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-11 16:06 +0530

## Machine

- Host: windows/amd64, AMD Ryzen 7 7840HS with Radeon 780M Graphics
- Node: bench-t470-control-plane (Debian GNU/Linux 12 (bookworm), kernel 6.6.87.2-microsoft-standard-WSL2, containerd://2.1.3, kubelet v1.34.0)
- Node capacity reported by Kubernetes: 4 CPU, 8133868Ki memory
- Simulated: 2.0 CPUs of quota on cpuset 0-3. kind node limited to 4 threads (cpuset 0-3) and 2.0 CPUs of quota to approximate an i5-6300U (2c/4t) against Ryzen 7 7840HS Zen 4 threads, calibration approximate
- Not simulated: 16-32 GB RAM (Docker VM has 7.8 GB, memory measured not limited)
- Not simulated: SATA/NVMe disk speed (no reliable I/O throttling on Docker Desktop/WSL2)
- Not simulated: Docker VM overhead (outside the node totals)
- Settle 180s, then 12 samples every 10s

### Platform at idle (no workloads)

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 815.8 | 825.7 | 180.3 | 218.4 |
| Secrets (OpenBao) | 17.8 | 17.8 | 10.8 | 14.5 |
| Certificates (cert-manager) | 65.3 | 70.0 | 6.9 | 25.6 |
| Application platform: control plane | 54.7 | 55.4 | 11.2 | 14.0 |
| Application platform: data plane | 6.6 | 6.7 | 0.1 | 0.3 |
| Gateway (kgateway) | 52.4 | 56.3 | 7.6 | 9.8 |
| Node overhead outside pods (kubelet, container runtime, system) | 300.2 | 303.9 | 66.7 | 164.9 |
| **Node total** | 1312.8 | 1334.3 | 283.5 | 415.4 |

Container images on disk: 1985 MiB. (Node filesystem used: 61247 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 576.5 | 89.6 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 89.1 | 49.2 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 68.0 | 23.4 |
| kgateway-system/kgateway-7b8984bc55-vbw94 | Gateway (kgateway) | 34.8 | 2.4 |
| cert-manager/cert-manager-bc44b5799-2495s | Certificates (cert-manager) | 27.8 | 4.9 |
| cert-manager/cert-manager-cainjector-784846b495-gzz9l | Certificates (cert-manager) | 24.5 | 1.4 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-t5hlj | Application platform: control plane | 21.7 | 10.3 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 21.7 | 9.2 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 17.8 | 10.8 |
| kgateway-system/serendip-external-657d66d6c7-t7988 | Gateway (kgateway) | 17.7 | 5.2 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 23.4 | 23.4 |
| CPU (millicores) | 0.0 | 0.0 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 23.4 MiB: **PASS**
- CPU 0.00% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 759.9 | 797.8 | 162.0 | 187.8 |
| Secrets (OpenBao) | 18.3 | 18.3 | 10.6 | 12.0 |
| Certificates (cert-manager) | 58.0 | 58.3 | 2.8 | 4.7 |
| Application platform: control plane | 62.7 | 63.9 | 11.3 | 13.4 |
| Application platform: data plane | 7.2 | 7.3 | 0.0 | 0.2 |
| Gateway (kgateway) | 55.0 | 57.5 | 7.4 | 8.8 |
| Workloads | 13.5 | 13.5 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 337.6 | 388.3 | 67.3 | 90.0 |
| **Node total** | 1312.3 | 1348.8 | 261.4 | 297.3 |

Container images on disk: 2035 MiB. (Node filesystem used: 61302 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 523.1 | 81.1 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 90.6 | 44.3 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 63.6 | 21.3 |
| kgateway-system/kgateway-7b8984bc55-vbw94 | Gateway (kgateway) | 37.1 | 1.9 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-t5hlj | Application platform: control plane | 28.4 | 10.3 |
| cert-manager/cert-manager-cainjector-784846b495-gzz9l | Certificates (cert-manager) | 24.2 | 1.3 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.2 | 8.7 |
| cert-manager/cert-manager-bc44b5799-2495s | Certificates (cert-manager) | 21.9 | 1.1 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.3 | 10.6 |
| kgateway-system/serendip-external-657d66d6c7-t7988 | Gateway (kgateway) | 17.9 | 5.4 |

### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1336 MiB + 10 × 8.4 MiB per Component = 1420 MiB (10% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 204s
- Warm restart (everything installed): 37s
- Component create → Serving: 30s, 30s, 30s (includes up to one 30s resync)
