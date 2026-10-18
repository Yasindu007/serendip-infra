> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-19 00:08 +0530

## Machine

- Host: windows/amd64, AMD Ryzen 7 7840HS with Radeon 780M Graphics
- Node: serendip-t012-control-plane (Debian GNU/Linux 12 (bookworm), kernel 6.6.87.2-microsoft-standard-WSL2, containerd://2.1.3, kubelet v1.34.0)
- Node capacity reported by Kubernetes: 4 CPU, 8133872Ki memory
- Simulated: 2.0 CPUs of quota on cpuset 0-3. kind node limited to 4 threads and 2.0 CPUs of quota to approximate an i5-6300U (2c/4t); spec 018 T012 baseline for the Agent Manager comparison
- Not simulated: 16-32 GB RAM (only measured, not limited)
- Not simulated: SATA/NVMe disk speed
- Settle 180s, then 12 samples every 10s

### Platform at idle (no workloads)

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 766.1 | 777.5 | 154.0 | 181.3 |
| Secrets (OpenBao) | 18.4 | 18.6 | 7.4 | 9.6 |
| Certificates (cert-manager) | 63.9 | 64.3 | 2.7 | 3.2 |
| Application platform: control plane | 56.0 | 61.4 | 5.0 | 5.8 |
| Application platform: data plane | 6.6 | 6.7 | 0.1 | 0.4 |
| Gateway (kgateway) | 52.9 | 54.3 | 5.8 | 7.2 |
| Identity (ThunderID) | 12.3 | 12.3 | 0.3 | 0.9 |
| Node overhead outside pods (kubelet, container runtime, system) | 342.8 | 344.2 | 62.8 | 81.5 |
| **Node total** | 1319.1 | 1335.8 | 238.1 | 285.9 |

Container images on disk: 2199 MiB. (Node filesystem used: 77246 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-serendip-t012-control-plane | Kubernetes system | 524.4 | 77.0 |
| kube-system/etcd-serendip-t012-control-plane | Kubernetes system | 92.4 | 42.6 |
| kube-system/kube-controller-manager-serendip-t012-control-plane | Kubernetes system | 65.6 | 19.7 |
| kgateway-system/kgateway-7b8984bc55-2gkbw | Gateway (kgateway) | 35.1 | 2.0 |
| cert-manager/cert-manager-bc44b5799-p9j9z | Certificates (cert-manager) | 26.7 | 1.1 |
| cert-manager/cert-manager-cainjector-784846b495-tz6vq | Certificates (cert-manager) | 25.1 | 1.0 |
| kube-system/kube-scheduler-serendip-t012-control-plane | Kubernetes system | 22.5 | 7.9 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-ggp5x | Application platform: control plane | 22.3 | 4.1 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.4 | 7.4 |
| kgateway-system/serendip-external-657d66d6c7-vmz69 | Gateway (kgateway) | 17.8 | 3.8 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 31.5 | 35.0 |
| CPU (millicores) | 0.0 | 0.0 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 31.5 MiB: **PASS**
- CPU 0.00% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 784.2 | 787.1 | 144.5 | 169.1 |
| Secrets (OpenBao) | 18.7 | 18.7 | 11.0 | 12.4 |
| Certificates (cert-manager) | 63.2 | 63.2 | 2.3 | 3.0 |
| Application platform: control plane | 63.9 | 66.1 | 4.9 | 5.6 |
| Application platform: data plane | 7.4 | 7.6 | 0.1 | 0.3 |
| Gateway (kgateway) | 53.3 | 53.3 | 5.2 | 6.5 |
| Identity (ThunderID) | 12.2 | 12.3 | 0.2 | 0.4 |
| Workloads | 13.7 | 13.7 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 375.3 | 381.5 | 62.2 | 101.2 |
| **Node total** | 1391.7 | 1398.1 | 230.4 | 274.4 |

Container images on disk: 2250 MiB. (Node filesystem used: 77298 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-serendip-t012-control-plane | Kubernetes system | 538.0 | 73.4 |
| kube-system/etcd-serendip-t012-control-plane | Kubernetes system | 97.6 | 39.6 |
| kube-system/kube-controller-manager-serendip-t012-control-plane | Kubernetes system | 64.4 | 18.0 |
| kgateway-system/kgateway-7b8984bc55-2gkbw | Gateway (kgateway) | 35.2 | 1.7 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-ggp5x | Application platform: control plane | 29.7 | 4.2 |
| cert-manager/cert-manager-bc44b5799-p9j9z | Certificates (cert-manager) | 26.1 | 0.9 |
| cert-manager/cert-manager-cainjector-784846b495-tz6vq | Certificates (cert-manager) | 24.9 | 1.0 |
| kube-system/kube-scheduler-serendip-t012-control-plane | Kubernetes system | 22.6 | 7.5 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.7 | 11.0 |
| kgateway-system/serendip-external-657d66d6c7-vmz69 | Gateway (kgateway) | 18.1 | 3.5 |

### serendipd with 3 web-console tabs polling

144 requests (1.26/s), 0 errors.

| | No tabs | 3 tabs |
|---|---|---|
| RSS mean (MiB) | 35.1 | 35.3 |
| CPU mean (millicores) | 0.1 | 1.0 |

- CPU 0.05% of 2.0 cores (budget < 5%, no request errors): **PASS**
- RSS 35.3 MiB (budget < 256 MiB): **PASS**


### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1351 MiB + 10 × 7.7 MiB per Component = 1427 MiB (10% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 305s
- Warm restart (everything installed): 38s
- Component create → Serving: 30s, 30s, 30s (includes the image pull; status is re-checked about 2, 6, 14, 22 and 30s after create)
