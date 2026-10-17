> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-17 20:37 +0530

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
| Kubernetes system | 789.0 | 819.0 | 155.9 | 170.0 |
| Secrets (OpenBao) | 17.8 | 17.9 | 9.7 | 12.1 |
| Certificates (cert-manager) | 62.0 | 62.3 | 2.7 | 3.5 |
| Application platform: control plane | 54.4 | 54.5 | 4.4 | 6.4 |
| Application platform: data plane | 8.0 | 8.1 | 0.0 | 0.2 |
| Gateway (kgateway) | 52.4 | 52.5 | 6.8 | 8.3 |
| Identity (ThunderID) | 10.8 | 11.3 | 0.2 | 0.6 |
| Node overhead outside pods (kubelet, container runtime, system) | 361.7 | 364.0 | 59.3 | 72.9 |
| **Node total** | 1356.0 | 1389.1 | 239.1 | 262.2 |

Container images on disk: 2199 MiB. (Node filesystem used: 61782 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 548.3 | 77.8 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 92.1 | 42.8 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 64.7 | 21.3 |
| kgateway-system/kgateway-7b8984bc55-dqkbz | Gateway (kgateway) | 34.7 | 1.8 |
| cert-manager/cert-manager-bc44b5799-x8nkg | Certificates (cert-manager) | 25.9 | 1.1 |
| cert-manager/cert-manager-cainjector-784846b495-kdcfc | Certificates (cert-manager) | 23.8 | 1.1 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.3 | 7.5 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-ffk5z | Application platform: control plane | 22.3 | 3.6 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 17.8 | 9.7 |
| kgateway-system/serendip-external-657d66d6c7-tcd9k | Gateway (kgateway) | 17.8 | 5.0 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 26.3 | 26.3 |
| CPU (millicores) | 0.0 | 0.0 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 26.3 MiB: **PASS**
- CPU 0.00% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 803.2 | 805.8 | 161.9 | 175.9 |
| Secrets (OpenBao) | 18.4 | 18.5 | 8.9 | 12.9 |
| Certificates (cert-manager) | 61.3 | 61.3 | 2.6 | 4.2 |
| Application platform: control plane | 62.4 | 62.9 | 5.0 | 5.9 |
| Application platform: data plane | 8.6 | 8.8 | 0.0 | 0.4 |
| Gateway (kgateway) | 54.0 | 54.0 | 6.9 | 9.0 |
| Identity (ThunderID) | 10.7 | 10.7 | 0.3 | 0.4 |
| Workloads | 14.5 | 14.5 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 445.3 | 448.8 | 64.9 | 105.5 |
| **Node total** | 1478.4 | 1484.0 | 250.5 | 300.8 |

Container images on disk: 2250 MiB. (Node filesystem used: 61838 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 560.3 | 82.0 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 95.5 | 43.8 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 63.0 | 22.0 |
| kgateway-system/kgateway-7b8984bc55-dqkbz | Gateway (kgateway) | 36.0 | 1.9 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-ffk5z | Application platform: control plane | 29.3 | 4.1 |
| cert-manager/cert-manager-bc44b5799-x8nkg | Certificates (cert-manager) | 25.8 | 1.0 |
| cert-manager/cert-manager-cainjector-784846b495-kdcfc | Certificates (cert-manager) | 23.7 | 1.2 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.5 | 7.5 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.4 | 8.9 |
| kgateway-system/serendip-external-657d66d6c7-tcd9k | Gateway (kgateway) | 18.0 | 5.0 |

### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1382 MiB + 10 × 8.4 MiB per Component = 1467 MiB (10% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 180s
- Warm restart (everything installed): 37s
- Component create → Serving: 16s, 32s, 32s (includes the image pull; status is re-checked about 2, 6, 14, 22 and 30s after create)
