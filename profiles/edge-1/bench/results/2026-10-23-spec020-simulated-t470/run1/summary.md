> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-23 18:42 +0530

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
| Kubernetes system | 859.1 | 869.8 | 151.0 | 181.6 |
| Secrets (OpenBao) | 18.6 | 18.7 | 9.1 | 11.3 |
| Certificates (cert-manager) | 62.6 | 62.6 | 3.0 | 4.4 |
| Application platform: control plane | 54.9 | 55.1 | 7.8 | 8.9 |
| Application platform: data plane | 6.5 | 6.6 | 0.0 | 0.2 |
| Gateway (kgateway) | 52.5 | 52.5 | 7.5 | 9.9 |
| Identity (ThunderID) | 12.1 | 12.1 | 0.3 | 0.6 |
| Node overhead outside pods (kubelet, container runtime, system) | 636.8 | 647.1 | 68.9 | 110.7 |
| **Node total** | 1703.0 | 1721.5 | 247.7 | 293.5 |

Container images on disk: 2199 MiB. (Node filesystem used: 77534 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 586.8 | 74.8 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 107.6 | 41.4 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 73.4 | 19.4 |
| kgateway-system/kgateway-7b8984bc55-wvh8m | Gateway (kgateway) | 34.6 | 1.9 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 29.0 | 8.6 |
| cert-manager/cert-manager-bc44b5799-c56fz | Certificates (cert-manager) | 26.2 | 1.1 |
| cert-manager/cert-manager-cainjector-784846b495-v7cp9 | Certificates (cert-manager) | 24.0 | 1.4 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-g2c62 | Application platform: control plane | 22.3 | 7.0 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.6 | 9.1 |
| kgateway-system/serendip-external-657d66d6c7-7phxw | Gateway (kgateway) | 17.9 | 5.6 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 35.6 | 36.4 |
| CPU (millicores) | 0.0 | 0.0 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 35.6 MiB: **PASS**
- CPU 0.00% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 857.7 | 858.1 | 164.5 | 190.0 |
| Secrets (OpenBao) | 18.9 | 18.9 | 10.7 | 12.1 |
| Certificates (cert-manager) | 62.2 | 62.2 | 3.2 | 4.4 |
| Application platform: control plane | 65.9 | 66.5 | 9.4 | 11.4 |
| Application platform: data plane | 7.4 | 7.6 | 0.0 | 0.3 |
| Gateway (kgateway) | 53.9 | 54.0 | 8.3 | 9.9 |
| Identity (ThunderID) | 12.0 | 12.0 | 0.3 | 0.7 |
| Workloads | 13.6 | 13.6 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 651.6 | 652.8 | 67.5 | 101.0 |
| **Node total** | 1743.3 | 1744.9 | 263.9 | 299.5 |

Container images on disk: 2250 MiB. (Node filesystem used: 77585 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 583.3 | 87.0 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 109.1 | 43.3 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 72.9 | 19.4 |
| kgateway-system/kgateway-7b8984bc55-wvh8m | Gateway (kgateway) | 35.8 | 2.2 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-g2c62 | Application platform: control plane | 31.7 | 8.5 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 29.6 | 8.5 |
| cert-manager/cert-manager-bc44b5799-c56fz | Certificates (cert-manager) | 26.0 | 1.1 |
| cert-manager/cert-manager-cainjector-784846b495-v7cp9 | Certificates (cert-manager) | 24.0 | 1.6 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.9 | 10.7 |
| kgateway-system/serendip-external-657d66d6c7-7phxw | Gateway (kgateway) | 18.2 | 6.1 |

### serendipd with 3 web-console tabs polling

150 requests (1.30/s), 0 errors.

| | No tabs | 3 tabs |
|---|---|---|
| RSS mean (MiB) | 37.5 | 37.2 |
| CPU mean (millicores) | 1.1 | 2.3 |

- CPU 0.12% of 2.0 cores (budget < 5%, no request errors): **PASS**
- RSS 37.2 MiB (budget < 256 MiB): **PASS**


### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1739 MiB + 10 × 9.1 MiB per Component = 1829 MiB (13% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 774s
- Warm restart (everything installed): 47s
- Component create → Serving: 70s, 70s, 70s (includes the image pull; status is re-checked about 2, 6, 14, 22 and 30s after create)
