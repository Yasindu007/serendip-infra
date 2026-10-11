> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-11 15:03 +0530

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
| Kubernetes system | 775.5 | 803.6 | 154.4 | 173.9 |
| Secrets (OpenBao) | 18.0 | 18.2 | 9.8 | 11.1 |
| Certificates (cert-manager) | 65.8 | 69.5 | 2.4 | 3.1 |
| Application platform: control plane | 58.0 | 60.7 | 4.9 | 6.2 |
| Application platform: data plane | 6.7 | 6.8 | 0.0 | 0.1 |
| Gateway (kgateway) | 51.7 | 51.8 | 7.3 | 9.1 |
| Node overhead outside pods (kubelet, container runtime, system) | 300.3 | 303.8 | 56.0 | 75.7 |
| **Node total** | 1275.9 | 1310.8 | 234.8 | 274.3 |

Container images on disk: 1985 MiB. (Node filesystem used: 61247 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 537.8 | 76.7 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 87.4 | 43.4 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 68.4 | 20.7 |
| kgateway-system/kgateway-7b8984bc55-l8n8d | Gateway (kgateway) | 34.1 | 1.9 |
| cert-manager/cert-manager-bc44b5799-l7gnv | Certificates (cert-manager) | 28.2 | 1.1 |
| cert-manager/cert-manager-cainjector-784846b495-7km7t | Certificates (cert-manager) | 24.4 | 0.9 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-zd6t6 | Application platform: control plane | 21.6 | 4.0 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 21.4 | 7.3 |
| openchoreo-control-plane/openchoreo-api-75494997df-vxghb | Application platform: control plane | 18.2 | 0.2 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.0 | 9.8 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 25.3 | 25.3 |
| CPU (millicores) | 0.0 | 0.0 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 25.3 MiB: **PASS**
- CPU 0.00% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 770.9 | 786.8 | 165.2 | 182.3 |
| Secrets (OpenBao) | 18.5 | 18.5 | 9.8 | 10.7 |
| Certificates (cert-manager) | 61.5 | 61.5 | 2.5 | 3.1 |
| Application platform: control plane | 61.2 | 61.5 | 5.8 | 7.4 |
| Application platform: data plane | 7.9 | 8.2 | 0.1 | 0.2 |
| Gateway (kgateway) | 54.9 | 58.9 | 7.8 | 10.1 |
| Workloads | 13.5 | 13.5 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 324.4 | 332.3 | 71.5 | 93.8 |
| **Node total** | 1312.9 | 1335.9 | 262.7 | 299.2 |

Container images on disk: 2035 MiB. (Node filesystem used: 61303 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 532.4 | 82.0 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 92.4 | 46.0 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 63.6 | 23.0 |
| kgateway-system/kgateway-7b8984bc55-l8n8d | Gateway (kgateway) | 37.0 | 2.1 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-zd6t6 | Application platform: control plane | 27.6 | 4.8 |
| cert-manager/cert-manager-bc44b5799-l7gnv | Certificates (cert-manager) | 25.6 | 1.0 |
| cert-manager/cert-manager-cainjector-784846b495-7km7t | Certificates (cert-manager) | 23.7 | 1.1 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.1 | 8.1 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.5 | 9.8 |
| kgateway-system/serendip-external-657d66d6c7-7dskt | Gateway (kgateway) | 17.9 | 5.7 |

### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1301 MiB + 10 × 7.2 MiB per Component = 1373 MiB (10% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 200s
- Warm restart (everything installed): 37s
- Component create → Serving: 30s, 30s, 30s (includes up to one 30s resync)
