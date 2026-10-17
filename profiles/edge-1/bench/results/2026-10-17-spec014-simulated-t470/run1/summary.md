> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-17 11:19 +0530

## Machine

- Host: windows/amd64, AMD Ryzen 7 7840HS with Radeon 780M Graphics
- Node: bench-t470-control-plane (Debian GNU/Linux 12 (bookworm), kernel 6.6.87.2-microsoft-standard-WSL2, containerd://2.1.3, kubelet v1.34.0)
- Node capacity reported by Kubernetes: 4 CPU, 8133876Ki memory
- Simulated: 2.0 CPUs of quota on cpuset 0-3. kind node limited to 4 threads and 2.0 CPUs of quota to approximate an i5-6300U (2c/4t)
- Not simulated: 16-32 GB RAM (only measured, not limited)
- Not simulated: SATA/NVMe disk speed
- Settle 180s, then 12 samples every 10s

### Platform at idle (no workloads)

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 785.8 | 786.5 | 169.4 | 196.3 |
| Secrets (OpenBao) | 18.0 | 18.0 | 10.4 | 13.0 |
| Certificates (cert-manager) | 64.8 | 67.4 | 2.6 | 3.2 |
| Application platform: control plane | 55.3 | 57.5 | 7.8 | 9.6 |
| Application platform: data plane | 6.4 | 6.4 | 0.0 | 0.2 |
| Gateway (kgateway) | 54.0 | 60.1 | 7.2 | 8.3 |
| Node overhead outside pods (kubelet, container runtime, system) | 512.4 | 543.2 | 63.2 | 87.5 |
| **Node total** | 1496.6 | 1536.3 | 260.7 | 313.2 |

Container images on disk: 1985 MiB. (Node filesystem used: 61243 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 551.6 | 86.6 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 87.8 | 45.3 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 63.6 | 21.8 |
| kgateway-system/kgateway-7b8984bc55-pczkb | Gateway (kgateway) | 36.2 | 2.0 |
| cert-manager/cert-manager-bc44b5799-n7b5l | Certificates (cert-manager) | 27.6 | 1.1 |
| cert-manager/cert-manager-cainjector-784846b495-dpm4t | Certificates (cert-manager) | 24.0 | 1.1 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.1 | 8.7 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-67xxr | Application platform: control plane | 21.7 | 6.9 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.0 | 10.4 |
| kgateway-system/serendip-external-657d66d6c7-kj2d5 | Gateway (kgateway) | 17.7 | 5.2 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 34.8 | 34.8 |
| CPU (millicores) | 0.0 | 0.0 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 34.8 MiB: **PASS**
- CPU 0.00% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 801.0 | 814.2 | 182.0 | 200.7 |
| Secrets (OpenBao) | 18.1 | 18.1 | 9.8 | 12.1 |
| Certificates (cert-manager) | 62.3 | 62.4 | 3.0 | 3.8 |
| Application platform: control plane | 62.6 | 63.2 | 9.3 | 10.3 |
| Application platform: data plane | 7.3 | 7.6 | 0.1 | 0.5 |
| Gateway (kgateway) | 55.1 | 58.3 | 7.7 | 10.3 |
| Workloads | 13.7 | 13.7 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 537.7 | 541.4 | 74.7 | 107.9 |
| **Node total** | 1557.9 | 1577.0 | 286.4 | 318.3 |

Container images on disk: 2035 MiB. (Node filesystem used: 61299 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 558.7 | 90.8 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 93.8 | 49.1 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 64.7 | 24.3 |
| kgateway-system/kgateway-7b8984bc55-pczkb | Gateway (kgateway) | 37.0 | 2.1 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-67xxr | Application platform: control plane | 28.8 | 8.3 |
| cert-manager/cert-manager-bc44b5799-n7b5l | Certificates (cert-manager) | 26.0 | 1.2 |
| cert-manager/cert-manager-cainjector-784846b495-dpm4t | Certificates (cert-manager) | 23.9 | 1.2 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.6 | 9.4 |
| kgateway-system/serendip-external-657d66d6c7-kj2d5 | Gateway (kgateway) | 18.1 | 5.5 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.1 | 9.8 |

### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1531 MiB + 10 × 7.7 MiB per Component = 1609 MiB (11% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 182s
- Warm restart (everything installed): 36s
- Component create → Serving: 30s, 30s, 30s (includes up to one 30s resync)
