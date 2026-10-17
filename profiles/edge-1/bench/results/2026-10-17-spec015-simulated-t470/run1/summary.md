> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-17 20:22 +0530

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
| Kubernetes system | 806.0 | 818.5 | 144.5 | 166.7 |
| Secrets (OpenBao) | 17.8 | 18.0 | 8.9 | 9.6 |
| Certificates (cert-manager) | 64.2 | 67.7 | 2.6 | 3.9 |
| Application platform: control plane | 56.5 | 57.2 | 6.3 | 7.9 |
| Application platform: data plane | 7.7 | 7.9 | 0.0 | 0.2 |
| Gateway (kgateway) | 53.5 | 53.5 | 6.1 | 7.3 |
| Identity (ThunderID) | 10.6 | 10.7 | 0.2 | 0.4 |
| Node overhead outside pods (kubelet, container runtime, system) | 436.9 | 454.6 | 54.7 | 69.4 |
| **Node total** | 1453.2 | 1480.9 | 223.3 | 238.6 |

Container images on disk: 2199 MiB. (Node filesystem used: 61782 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 563.7 | 71.5 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 93.6 | 40.1 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 65.3 | 19.1 |
| kgateway-system/kgateway-7b8984bc55-dcdwt | Gateway (kgateway) | 35.6 | 1.8 |
| cert-manager/cert-manager-bc44b5799-nrqdv | Certificates (cert-manager) | 27.3 | 1.0 |
| cert-manager/cert-manager-cainjector-784846b495-hpg9d | Certificates (cert-manager) | 23.9 | 1.0 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-d559n | Application platform: control plane | 22.4 | 5.5 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.3 | 7.5 |
| kgateway-system/serendip-external-657d66d6c7-tss8g | Gateway (kgateway) | 17.9 | 4.2 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 17.8 | 8.9 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 26.2 | 26.2 |
| CPU (millicores) | 0.0 | 0.0 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 26.2 MiB: **PASS**
- CPU 0.00% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 801.4 | 804.0 | 152.4 | 165.0 |
| Secrets (OpenBao) | 18.4 | 18.4 | 10.4 | 12.0 |
| Certificates (cert-manager) | 61.2 | 61.3 | 2.5 | 3.3 |
| Application platform: control plane | 63.6 | 64.2 | 7.0 | 7.6 |
| Application platform: data plane | 8.3 | 8.4 | 0.0 | 0.1 |
| Gateway (kgateway) | 54.1 | 54.1 | 6.3 | 7.5 |
| Identity (ThunderID) | 10.6 | 10.6 | 0.2 | 0.5 |
| Workloads | 13.6 | 13.6 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 479.3 | 482.8 | 58.8 | 71.9 |
| **Node total** | 1510.5 | 1515.7 | 237.6 | 251.5 |

Container images on disk: 2250 MiB. (Node filesystem used: 61838 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 556.4 | 76.6 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 97.8 | 42.2 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 63.6 | 20.1 |
| kgateway-system/kgateway-7b8984bc55-dcdwt | Gateway (kgateway) | 35.9 | 1.8 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-d559n | Application platform: control plane | 28.8 | 6.2 |
| cert-manager/cert-manager-bc44b5799-nrqdv | Certificates (cert-manager) | 25.4 | 0.9 |
| cert-manager/cert-manager-cainjector-784846b495-hpg9d | Certificates (cert-manager) | 23.7 | 1.0 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.5 | 7.7 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.4 | 10.4 |
| kgateway-system/serendip-external-657d66d6c7-tss8g | Gateway (kgateway) | 18.2 | 4.5 |

### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1479 MiB + 10 × 7.5 MiB per Component = 1554 MiB (11% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 203s
- Warm restart (everything installed): 31s
- Component create → Serving: 24s, 32s, 32s (includes the image pull; status is re-checked about 2, 6, 14, 22 and 30s after create)
