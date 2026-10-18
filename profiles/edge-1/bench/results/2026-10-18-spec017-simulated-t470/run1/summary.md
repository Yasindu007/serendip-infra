> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-18 13:17 +0530

## Machine

- Host: windows/amd64, AMD Ryzen 7 7840HS with Radeon 780M Graphics
- Node: serendip-t017-control-plane (Debian GNU/Linux 12 (bookworm), kernel 6.6.87.2-microsoft-standard-WSL2, containerd://2.1.3, kubelet v1.34.0)
- Node capacity reported by Kubernetes: 4 CPU, 8133872Ki memory
- Simulated: 2.0 CPUs of quota on cpuset 0-3. kind node limited to 4 threads and 2.0 CPUs of quota to approximate an i5-6300U (2c/4t)
- Not simulated: 16-32 GB RAM (only measured, not limited)
- Not simulated: SATA/NVMe disk speed
- Settle 180s, then 12 samples every 10s

### Platform at idle (no workloads)

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 786.4 | 849.6 | 158.9 | 189.5 |
| Secrets (OpenBao) | 18.1 | 18.4 | 11.3 | 12.0 |
| Certificates (cert-manager) | 64.4 | 67.6 | 2.9 | 4.0 |
| Application platform: control plane | 54.4 | 55.7 | 7.3 | 10.9 |
| Application platform: data plane | 6.5 | 6.6 | 0.1 | 0.3 |
| Gateway (kgateway) | 51.9 | 51.9 | 7.0 | 8.9 |
| Identity (ThunderID) | 11.6 | 11.6 | 0.3 | 0.5 |
| Node overhead outside pods (kubelet, container runtime, system) | 342.6 | 351.8 | 59.2 | 95.4 |
| **Node total** | 1335.8 | 1409.2 | 246.9 | 284.3 |

Container images on disk: 2199 MiB. (Node filesystem used: 61907 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-serendip-t017-control-plane | Kubernetes system | 548.8 | 79.4 |
| kube-system/etcd-serendip-t017-control-plane | Kubernetes system | 88.6 | 44.3 |
| kube-system/kube-controller-manager-serendip-t017-control-plane | Kubernetes system | 65.7 | 22.0 |
| kgateway-system/kgateway-7b8984bc55-lbt2k | Gateway (kgateway) | 34.1 | 2.6 |
| cert-manager/cert-manager-bc44b5799-lnvfp | Certificates (cert-manager) | 27.9 | 1.2 |
| cert-manager/cert-manager-cainjector-784846b495-wqwqh | Certificates (cert-manager) | 24.1 | 1.2 |
| kube-system/kube-scheduler-serendip-t017-control-plane | Kubernetes system | 22.1 | 7.3 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-4jfsd | Application platform: control plane | 21.8 | 5.9 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.1 | 11.3 |
| kgateway-system/serendip-external-657d66d6c7-zhlgg | Gateway (kgateway) | 17.8 | 4.5 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 36.3 | 36.8 |
| CPU (millicores) | 0.2 | 2.7 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 36.3 MiB: **PASS**
- CPU 0.01% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 783.5 | 806.5 | 163.8 | 191.6 |
| Secrets (OpenBao) | 18.4 | 18.4 | 9.4 | 13.3 |
| Certificates (cert-manager) | 61.7 | 61.7 | 2.6 | 3.1 |
| Application platform: control plane | 63.5 | 65.6 | 7.4 | 8.3 |
| Application platform: data plane | 7.6 | 7.9 | 0.0 | 0.2 |
| Gateway (kgateway) | 53.4 | 53.5 | 6.5 | 8.5 |
| Identity (ThunderID) | 11.4 | 11.4 | 0.2 | 0.5 |
| Workloads | 13.5 | 13.5 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 378.5 | 380.5 | 101.8 | 514.3 |
| **Node total** | 1391.6 | 1417.8 | 291.8 | 696.4 |

Container images on disk: 2250 MiB. (Node filesystem used: 61962 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-serendip-t017-control-plane | Kubernetes system | 539.6 | 84.3 |
| kube-system/etcd-serendip-t017-control-plane | Kubernetes system | 96.0 | 44.6 |
| kube-system/kube-controller-manager-serendip-t017-control-plane | Kubernetes system | 64.0 | 21.5 |
| kgateway-system/kgateway-7b8984bc55-lbt2k | Gateway (kgateway) | 35.4 | 2.4 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-4jfsd | Application platform: control plane | 30.3 | 6.4 |
| cert-manager/cert-manager-bc44b5799-lnvfp | Certificates (cert-manager) | 25.8 | 1.1 |
| cert-manager/cert-manager-cainjector-784846b495-wqwqh | Certificates (cert-manager) | 24.0 | 1.1 |
| kube-system/kube-scheduler-serendip-t017-control-plane | Kubernetes system | 22.4 | 7.5 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.4 | 9.4 |
| kgateway-system/serendip-external-657d66d6c7-zhlgg | Gateway (kgateway) | 18.0 | 4.1 |

### serendipd with 3 web-console tabs polling

165 requests (1.32/s), 0 errors.

| | No tabs | 3 tabs |
|---|---|---|
| RSS mean (MiB) | 37.8 | 38.2 |
| CPU mean (millicores) | 0.5 | 1.6 |

- CPU 0.08% of 2.0 cores (budget < 5%, no request errors): **PASS**
- RSS 38.2 MiB (budget < 256 MiB): **PASS**


### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1372 MiB + 10 × 8.5 MiB per Component = 1458 MiB (10% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 257s
- Warm restart (everything installed): 37s
- Component create → Serving: 32s, 32s, 32s (includes the image pull; status is re-checked about 2, 6, 14, 22 and 30s after create)
