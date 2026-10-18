> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-18 07:11 +0530

## Machine

- Host: windows/amd64, AMD Ryzen 7 7840HS with Radeon 780M Graphics
- Node: bench-t470-control-plane (Debian GNU/Linux 12 (bookworm), kernel 6.6.87.2-microsoft-standard-WSL2, containerd://2.1.3, kubelet v1.34.0)
- Node capacity reported by Kubernetes: 4 CPU, 8133872Ki memory
- Simulated: 2.0 CPUs of quota on cpuset 0-3. kind node limited to 4 threads and 2.0 CPUs of quota to approximate an i5-6300U (2c/4t)
- Not simulated: 16-32 GB RAM (only measured, not limited)
- Not simulated: SATA/NVMe disk speed
- Settle 180s, then 12 samples every 10s

### Platform at idle (no workloads)

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 803.0 | 829.9 | 166.3 | 198.1 |
| Secrets (OpenBao) | 19.7 | 20.0 | 9.9 | 11.6 |
| Certificates (cert-manager) | 63.0 | 63.7 | 2.7 | 4.6 |
| Application platform: control plane | 55.9 | 60.0 | 8.2 | 9.0 |
| Application platform: data plane | 6.7 | 6.9 | 0.0 | 0.2 |
| Gateway (kgateway) | 53.8 | 53.9 | 6.0 | 7.1 |
| Identity (ThunderID) | 11.6 | 11.7 | 0.2 | 0.7 |
| Node overhead outside pods (kubelet, container runtime, system) | 596.5 | 620.6 | 64.8 | 131.8 |
| **Node total** | 1610.2 | 1635.3 | 258.1 | 311.3 |

Container images on disk: 2199 MiB. (Node filesystem used: 61860 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 534.7 | 83.6 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 103.9 | 45.5 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 73.5 | 22.4 |
| kgateway-system/kgateway-7b8984bc55-rxkmx | Gateway (kgateway) | 35.9 | 2.0 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 28.1 | 8.4 |
| cert-manager/cert-manager-bc44b5799-78nsc | Certificates (cert-manager) | 26.0 | 1.1 |
| cert-manager/cert-manager-cainjector-784846b495-4hmzz | Certificates (cert-manager) | 24.2 | 1.2 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-fwz2g | Application platform: control plane | 21.9 | 7.4 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 19.7 | 9.9 |
| kgateway-system/serendip-external-657d66d6c7-hfnrv | Gateway (kgateway) | 17.9 | 4.0 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 34.5 | 34.9 |
| CPU (millicores) | 0.0 | 0.0 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 34.5 MiB: **PASS**
- CPU 0.00% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 846.5 | 860.1 | 173.4 | 202.5 |
| Secrets (OpenBao) | 20.2 | 20.2 | 10.0 | 13.3 |
| Certificates (cert-manager) | 61.9 | 61.9 | 2.9 | 3.8 |
| Application platform: control plane | 63.1 | 66.4 | 8.7 | 9.6 |
| Application platform: data plane | 7.3 | 7.4 | 0.1 | 0.4 |
| Gateway (kgateway) | 55.2 | 55.6 | 5.9 | 6.9 |
| Identity (ThunderID) | 11.4 | 11.4 | 0.3 | 0.5 |
| Workloads | 13.7 | 13.7 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 615.7 | 618.3 | 71.9 | 116.1 |
| **Node total** | 1695.1 | 1708.4 | 273.1 | 345.5 |

Container images on disk: 2250 MiB. (Node filesystem used: 61916 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 571.3 | 88.7 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 107.4 | 46.2 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 73.9 | 23.0 |
| kgateway-system/kgateway-7b8984bc55-rxkmx | Gateway (kgateway) | 37.1 | 2.0 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 29.6 | 8.8 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-fwz2g | Application platform: control plane | 29.2 | 7.8 |
| cert-manager/cert-manager-bc44b5799-78nsc | Certificates (cert-manager) | 25.6 | 1.3 |
| cert-manager/cert-manager-cainjector-784846b495-4hmzz | Certificates (cert-manager) | 24.2 | 1.1 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 20.2 | 10.0 |
| kgateway-system/serendip-external-657d66d6c7-hfnrv | Gateway (kgateway) | 18.1 | 3.9 |

### serendipd with 3 web-console tabs polling

165 requests (1.28/s), 0 errors.

| | No tabs | 3 tabs |
|---|---|---|
| RSS mean (MiB) | 35.9 | 35.9 |
| CPU mean (millicores) | 0.4 | 0.7 |

- CPU 0.04% of 2.0 cores (budget < 5%, no request errors): **PASS**
- RSS 35.9 MiB (budget < 256 MiB): **PASS**


### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1645 MiB + 10 × 7.8 MiB per Component = 1722 MiB (12% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 325s
- Warm restart (everything installed): 48s
- Component create → Serving: 32s, 32s, 32s (includes the image pull; status is re-checked about 2, 6, 14, 22 and 30s after create)
