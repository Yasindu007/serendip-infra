> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-11-06 13:10 +0530

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
| Kubernetes system | 814.9 | 841.5 | 164.9 | 203.9 |
| Secrets (OpenBao) | 18.5 | 18.5 | 11.7 | 12.5 |
| Certificates (cert-manager) | 63.5 | 67.7 | 2.8 | 4.9 |
| Application platform: control plane | 55.8 | 57.2 | 7.6 | 9.1 |
| Application platform: data plane | 6.6 | 6.7 | 0.0 | 0.3 |
| Gateway (kgateway) | 52.6 | 57.0 | 5.9 | 7.5 |
| Identity (ThunderID) | 12.6 | 12.6 | 0.4 | 1.8 |
| Node overhead outside pods (kubelet, container runtime, system) | 644.6 | 646.7 | 65.2 | 96.3 |
| **Node total** | 1669.2 | 1700.0 | 258.4 | 331.3 |

Container images on disk: 2199 MiB. (Node filesystem used: 78382 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 543.0 | 81.9 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 98.5 | 44.1 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 75.2 | 24.6 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 35.1 | 8.3 |
| kgateway-system/kgateway-7b8984bc55-swzr5 | Gateway (kgateway) | 34.8 | 2.1 |
| cert-manager/cert-manager-bc44b5799-9hr9q | Certificates (cert-manager) | 26.5 | 1.1 |
| cert-manager/cert-manager-cainjector-784846b495-v98m6 | Certificates (cert-manager) | 24.3 | 1.2 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-lw8n7 | Application platform: control plane | 22.7 | 6.7 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.5 | 11.7 |
| kgateway-system/serendip-external-657d66d6c7-dczcm | Gateway (kgateway) | 17.8 | 3.8 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 29.2 | 29.7 |
| CPU (millicores) | 0.0 | 0.0 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 29.2 MiB: **PASS**
- CPU 0.00% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 852.0 | 865.9 | 153.3 | 165.7 |
| Secrets (OpenBao) | 18.9 | 18.9 | 7.0 | 8.5 |
| Certificates (cert-manager) | 62.6 | 62.8 | 2.4 | 3.4 |
| Application platform: control plane | 65.5 | 65.8 | 8.6 | 10.1 |
| Application platform: data plane | 7.4 | 7.6 | 0.0 | 0.2 |
| Gateway (kgateway) | 53.0 | 53.0 | 5.2 | 6.2 |
| Identity (ThunderID) | 12.4 | 12.4 | 0.2 | 0.3 |
| Workloads | 13.7 | 13.7 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 669.5 | 673.0 | 58.0 | 124.5 |
| **Node total** | 1755.1 | 1770.9 | 234.8 | 313.6 |

Container images on disk: 2250 MiB. (Node filesystem used: 78435 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 571.9 | 80.5 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 105.9 | 40.1 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 74.8 | 20.2 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 36.2 | 7.6 |
| kgateway-system/kgateway-7b8984bc55-swzr5 | Gateway (kgateway) | 35.0 | 1.9 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-lw8n7 | Application platform: control plane | 31.2 | 7.8 |
| cert-manager/cert-manager-bc44b5799-9hr9q | Certificates (cert-manager) | 26.0 | 0.9 |
| cert-manager/cert-manager-cainjector-784846b495-v98m6 | Certificates (cert-manager) | 24.2 | 1.1 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.9 | 7.0 |
| kgateway-system/serendip-external-657d66d6c7-dczcm | Gateway (kgateway) | 18.0 | 3.3 |

### serendipd with 3 web-console tabs polling

144 requests (1.26/s), 0 errors.

| | No tabs | 3 tabs |
|---|---|---|
| RSS mean (MiB) | 31.8 | 31.9 |
| CPU mean (millicores) | 1.8 | 3.6 |

- CPU 0.18% of 2.0 cores (budget < 5%, no request errors): **PASS**
- RSS 31.9 MiB (budget < 256 MiB): **PASS**


### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1698 MiB + 10 × 8.3 MiB per Component = 1781 MiB (12% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 285s
- Warm restart (everything installed): 31s
- Component create → Serving: 34s, 34s, 34s (includes the image pull; status is re-checked about 2, 6, 14, 22 and 30s after create)
