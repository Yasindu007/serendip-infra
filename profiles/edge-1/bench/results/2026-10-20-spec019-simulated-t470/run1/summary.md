> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-20 05:50 +0530

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
| Kubernetes system | 741.6 | 778.1 | 151.5 | 189.3 |
| Secrets (OpenBao) | 18.0 | 18.1 | 10.8 | 13.5 |
| Certificates (cert-manager) | 64.0 | 64.8 | 2.5 | 3.6 |
| Application platform: control plane | 56.2 | 59.6 | 7.0 | 8.1 |
| Application platform: data plane | 6.3 | 6.4 | 0.0 | 0.2 |
| Gateway (kgateway) | 53.4 | 53.4 | 8.7 | 10.3 |
| Identity (ThunderID) | 12.1 | 12.1 | 0.3 | 0.4 |
| Node overhead outside pods (kubelet, container runtime, system) | 341.3 | 374.6 | 57.5 | 109.5 |
| **Node total** | 1292.8 | 1333.0 | 238.2 | 281.4 |

Container images on disk: 2199 MiB. (Node filesystem used: 77265 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 508.0 | 75.7 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 86.4 | 41.3 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 64.1 | 20.0 |
| kgateway-system/kgateway-7b8984bc55-bwfvl | Gateway (kgateway) | 35.5 | 2.0 |
| cert-manager/cert-manager-bc44b5799-x4dsq | Certificates (cert-manager) | 26.1 | 0.9 |
| cert-manager/cert-manager-cainjector-784846b495-8wnb8 | Certificates (cert-manager) | 25.2 | 1.1 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-7x4sv | Application platform: control plane | 22.6 | 6.2 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 21.6 | 7.7 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.0 | 10.8 |
| kgateway-system/serendip-external-657d66d6c7-4b7qm | Gateway (kgateway) | 17.8 | 6.7 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 27.4 | 27.9 |
| CPU (millicores) | 0.0 | 0.0 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 27.4 MiB: **PASS**
- CPU 0.00% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 778.5 | 785.6 | 150.4 | 179.9 |
| Secrets (OpenBao) | 18.4 | 18.6 | 8.6 | 9.5 |
| Certificates (cert-manager) | 63.2 | 63.4 | 2.4 | 3.4 |
| Application platform: control plane | 62.9 | 64.1 | 7.3 | 8.2 |
| Application platform: data plane | 7.4 | 7.6 | 0.1 | 0.3 |
| Gateway (kgateway) | 54.4 | 60.4 | 8.4 | 8.8 |
| Identity (ThunderID) | 11.9 | 11.9 | 0.2 | 0.5 |
| Workloads | 14.1 | 14.1 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 460.1 | 467.6 | 63.4 | 99.8 |
| **Node total** | 1470.8 | 1481.4 | 240.7 | 306.2 |

Container images on disk: 2250 MiB. (Node filesystem used: 77317 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 541.0 | 75.5 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 89.4 | 41.1 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 64.3 | 19.6 |
| kgateway-system/kgateway-7b8984bc55-bwfvl | Gateway (kgateway) | 36.4 | 1.9 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-7x4sv | Application platform: control plane | 28.7 | 6.5 |
| cert-manager/cert-manager-bc44b5799-x4dsq | Certificates (cert-manager) | 25.9 | 1.0 |
| cert-manager/cert-manager-cainjector-784846b495-8wnb8 | Certificates (cert-manager) | 25.0 | 0.9 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.7 | 7.7 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.4 | 8.6 |
| kgateway-system/serendip-external-657d66d6c7-4b7qm | Gateway (kgateway) | 18.1 | 6.5 |

### serendipd with 3 web-console tabs polling

144 requests (1.26/s), 0 errors.

| | No tabs | 3 tabs |
|---|---|---|
| RSS mean (MiB) | 28.8 | 29.3 |
| CPU mean (millicores) | 0.7 | 1.5 |

- CPU 0.08% of 2.0 cores (budget < 5%, no request errors): **PASS**
- RSS 29.3 MiB (budget < 256 MiB): **PASS**


### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1320 MiB + 10 × 7.8 MiB per Component = 1398 MiB (10% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 300s
- Warm restart (everything installed): 33s
- Component create → Serving: 32s, 32s, 32s (includes the image pull; status is re-checked about 2, 6, 14, 22 and 30s after create)
