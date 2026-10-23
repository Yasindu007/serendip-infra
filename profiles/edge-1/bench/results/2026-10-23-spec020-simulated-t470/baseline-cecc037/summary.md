> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-10-23 19:41 +0530

## Machine

- Host: windows/amd64, AMD Ryzen 7 7840HS with Radeon 780M Graphics
- Node: bench-t470-control-plane (Debian GNU/Linux 12 (bookworm), kernel 6.6.87.2-microsoft-standard-WSL2, containerd://2.1.3, kubelet v1.34.0)
- Node capacity reported by Kubernetes: 4 CPU, 8133876Ki memory
- Simulated: 2.0 CPUs of quota on cpuset 0-3. kind node limited to 4 threads and 2.0 CPUs of quota to approximate an i5-6300U (2c/4t); baseline: spec 021's final code (cecc037), run on the same host the same day
- Not simulated: 16-32 GB RAM (only measured, not limited)
- Not simulated: SATA/NVMe disk speed
- Settle 180s, then 12 samples every 10s

### Platform at idle (no workloads)

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 813.3 | 822.2 | 173.7 | 203.4 |
| Secrets (OpenBao) | 18.4 | 18.4 | 9.2 | 11.8 |
| Certificates (cert-manager) | 64.7 | 66.2 | 3.5 | 5.5 |
| Application platform: control plane | 56.9 | 58.2 | 7.9 | 11.4 |
| Application platform: data plane | 6.7 | 6.7 | 0.1 | 0.4 |
| Gateway (kgateway) | 52.6 | 56.1 | 8.0 | 8.6 |
| Identity (ThunderID) | 12.1 | 12.1 | 0.5 | 1.2 |
| Node overhead outside pods (kubelet, container runtime, system) | 534.8 | 542.0 | 83.5 | 139.3 |
| **Node total** | 1559.5 | 1573.8 | 286.4 | 352.0 |

Container images on disk: 2199 MiB. (Node filesystem used: 77537 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 558.9 | 86.1 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 103.4 | 48.0 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 66.7 | 23.5 |
| kgateway-system/kgateway-7b8984bc55-7v9dk | Gateway (kgateway) | 34.5 | 2.2 |
| cert-manager/cert-manager-bc44b5799-flrxd | Certificates (cert-manager) | 27.3 | 1.5 |
| cert-manager/cert-manager-cainjector-784846b495-g5hhh | Certificates (cert-manager) | 24.2 | 1.4 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-c7nzv | Application platform: control plane | 23.6 | 6.9 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.3 | 8.5 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.4 | 9.2 |
| kgateway-system/serendip-external-657d66d6c7-gp2d9 | Gateway (kgateway) | 18.1 | 5.8 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 34.4 | 34.4 |
| CPU (millicores) | 0.4 | 1.5 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 34.4 MiB: **PASS**
- CPU 0.02% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured

### With 3 light Components deployed

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 798.0 | 803.1 | 171.2 | 237.9 |
| Secrets (OpenBao) | 18.7 | 18.7 | 7.6 | 9.3 |
| Certificates (cert-manager) | 61.6 | 61.6 | 2.8 | 3.8 |
| Application platform: control plane | 63.8 | 66.7 | 7.9 | 10.5 |
| Application platform: data plane | 7.3 | 7.3 | 0.0 | 0.3 |
| Gateway (kgateway) | 55.4 | 56.6 | 7.6 | 12.1 |
| Identity (ThunderID) | 12.0 | 12.0 | 0.4 | 1.0 |
| Workloads | 13.7 | 13.7 | 0.0 | 0.0 |
| Node overhead outside pods (kubelet, container runtime, system) | 601.1 | 604.4 | 80.6 | 286.3 |
| **Node total** | 1631.7 | 1640.1 | 278.1 | 523.3 |

Container images on disk: 2250 MiB. (Node filesystem used: 77589 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 545.1 | 87.6 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 103.4 | 46.7 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 63.9 | 21.0 |
| kgateway-system/kgateway-7b8984bc55-7v9dk | Gateway (kgateway) | 37.1 | 2.0 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-c7nzv | Application platform: control plane | 29.1 | 7.0 |
| cert-manager/cert-manager-bc44b5799-flrxd | Certificates (cert-manager) | 25.5 | 1.1 |
| cert-manager/cert-manager-cainjector-784846b495-g5hhh | Certificates (cert-manager) | 24.0 | 1.1 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.9 | 8.6 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.7 | 7.6 |
| kgateway-system/serendip-external-657d66d6c7-gp2d9 | Gateway (kgateway) | 18.4 | 5.6 |

### serendipd with 3 web-console tabs polling

150 requests (1.30/s), 0 errors.

| | No tabs | 3 tabs |
|---|---|---|
| RSS mean (MiB) | 36.0 | 35.9 |
| CPU mean (millicores) | 1.4 | 1.6 |

- CPU 0.08% of 2.0 cores (budget < 5%, no request errors): **PASS**
- RSS 35.9 MiB (budget < 256 MiB): **PASS**


### 16 GB headroom

Budget = 16 GB − 2 GB host OS = 14336 MiB. Projected = platform 1594 MiB + 10 × 8.1 MiB per Component = 1675 MiB (12% of budget).

**Verdict: fits 16 GB comfortably.**

### Timings

- Cold start (fresh cluster, includes image pulls): 579s
- Warm restart (everything installed): 39s
- Component create → Serving: 16s, 24s, 24s (includes the image pull; status is re-checked about 2, 6, 14, 22 and 30s after create)
