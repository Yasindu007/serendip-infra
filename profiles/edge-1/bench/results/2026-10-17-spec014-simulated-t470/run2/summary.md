> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-17 11:35 +0530

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
| Kubernetes system | 790.6 | 812.2 | 173.5 | 215.4 |
| Secrets (OpenBao) | 18.0 | 18.1 | 10.3 | 12.2 |
| Certificates (cert-manager) | 64.2 | 70.3 | 2.8 | 3.8 |
| Application platform: control plane | 54.7 | 55.7 | 6.5 | 8.1 |
| Application platform: data plane | 7.4 | 7.5 | 0.0 | 0.3 |
| Gateway (kgateway) | 50.5 | 50.5 | 7.1 | 7.9 |
| Node overhead outside pods (kubelet, container runtime, system) | 369.5 | 372.4 | 89.8 | 340.3 |
| **Node total** | 1354.8 | 1377.9 | 289.9 | 534.7 |

Container images on disk: 1985 MiB. (Node filesystem used: 61243 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 559.6 | 85.8 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 86.4 | 47.9 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 63.0 | 23.2 |
| kgateway-system/kgateway-7b8984bc55-sd7vh | Gateway (kgateway) | 32.8 | 1.9 |
| cert-manager/cert-manager-bc44b5799-zl5xt | Certificates (cert-manager) | 26.6 | 1.1 |
| cert-manager/cert-manager-cainjector-784846b495-jhrtn | Certificates (cert-manager) | 24.4 | 1.2 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-fcx5n | Application platform: control plane | 21.9 | 5.6 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 21.4 | 9.3 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.0 | 10.3 |
| kgateway-system/serendip-external-657d66d6c7-c98ff | Gateway (kgateway) | 17.6 | 5.2 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 26.3 | 26.4 |
| CPU (millicores) | 0.4 | 3.1 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 26.3 MiB: **PASS**
- CPU 0.02% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 800.2 | 805.5 | 206.1 | 365.0 |
| Secrets (OpenBao) | 18.5 | 18.7 | 11.4 | 14.7 |
| Certificates (cert-manager) | 62.1 | 62.2 | 3.2 | 4.5 |
| Application platform: control plane | 62.4 | 63.0 | 8.0 | 10.4 |
| Application platform: data plane | 8.1 | 8.2 | 0.1 | 1.4 |
| Gateway (kgateway) | 53.5 | 58.1 | 8.0 | 11.1 |
| Workloads | 13.6 | 13.6 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 438.9 | 442.3 | 54.4 | 83.1 |
| **Node total** | 1457.3 | 1464.4 | 291.3 | 483.5 |

Container images on disk: 2035 MiB. (Node filesystem used: 61299 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 562.7 | 109.3 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 91.5 | 52.4 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 63.1 | 26.8 |
| kgateway-system/kgateway-7b8984bc55-sd7vh | Gateway (kgateway) | 35.6 | 2.1 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-fcx5n | Application platform: control plane | 29.0 | 6.9 |
| cert-manager/cert-manager-bc44b5799-zl5xt | Certificates (cert-manager) | 25.7 | 1.5 |
| cert-manager/cert-manager-cainjector-784846b495-jhrtn | Certificates (cert-manager) | 24.1 | 1.2 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.1 | 9.6 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.5 | 11.4 |
| kgateway-system/serendip-external-657d66d6c7-c98ff | Gateway (kgateway) | 17.9 | 5.9 |

### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1381 MiB + 10 × 8.5 MiB per Component = 1467 MiB (10% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 169s
- Warm restart (everything installed): 38s
- Component create → Serving: 32s, 32s, 32s (includes up to one 30s resync)
