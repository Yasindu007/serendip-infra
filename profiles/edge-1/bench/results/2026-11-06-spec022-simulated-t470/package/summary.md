> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-11-09 13:25 +0530

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
| Kubernetes system | 832.3 | 834.5 | 136.7 | 161.0 |
| Secrets (OpenBao) | 18.2 | 18.2 | 8.7 | 10.7 |
| Certificates (cert-manager) | 63.6 | 63.6 | 2.6 | 4.1 |
| Application platform: control plane | 55.6 | 56.9 | 7.1 | 9.0 |
| Application platform: data plane | 6.4 | 6.5 | 0.0 | 0.1 |
| Gateway (kgateway) | 54.2 | 57.5 | 4.9 | 5.8 |
| Identity (ThunderID) | 12.2 | 12.2 | 0.3 | 0.5 |
| Node overhead outside pods (kubelet, container runtime, system) | 571.7 | 575.2 | 56.1 | 100.4 |
| **Node total** | 1614.0 | 1618.2 | 216.4 | 281.5 |

Container images on disk: 2199 MiB. (Node filesystem used: 78397 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 585.6 | 68.3 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 99.3 | 37.1 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 63.2 | 17.7 |
| kgateway-system/kgateway-7b8984bc55-rgcx7 | Gateway (kgateway) | 36.1 | 1.7 |
| cert-manager/cert-manager-bc44b5799-mjpmx | Certificates (cert-manager) | 26.0 | 1.0 |
| cert-manager/cert-manager-cainjector-784846b495-fqkhh | Certificates (cert-manager) | 25.2 | 1.1 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 22.7 | 7.6 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-6gshv | Application platform: control plane | 22.2 | 6.2 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.2 | 8.7 |
| kgateway-system/serendip-external-657d66d6c7-z6jt6 | Gateway (kgateway) | 18.1 | 3.3 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 29.6 | 29.9 |
| CPU (millicores) | 0.0 | 0.0 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 29.6 MiB: **PASS**
- CPU 0.00% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured


### 16 GB headroom

**Verdict: not computed: no light-workload data.**

### Timings

- Cold start (fresh cluster, includes image pulls): 680s
- Warm restart (everything installed): 31s
- Component create → Serving:  (includes the image pull; status is re-checked about 2, 6, 14, 22 and 30s after create)

## Package: openchoreo-workflow-plane

Install (create → Ready, with image pulls): 1m46s

### Installed, idle (60 samples)

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 892.8 | 924.7 | 142.9 | 198.0 |
| Secrets (OpenBao) | 18.4 | 18.4 | 8.8 | 10.9 |
| Certificates (cert-manager) | 68.6 | 69.2 | 2.7 | 3.5 |
| Application platform: control plane | 55.9 | 56.0 | 7.6 | 8.9 |
| Application platform: data plane | 6.2 | 6.2 | 0.0 | 0.2 |
| Gateway (kgateway) | 53.8 | 53.8 | 4.9 | 6.0 |
| Identity (ThunderID) | 12.1 | 12.1 | 0.2 | 0.9 |
| Builds (workflow plane) | 27.3 | 28.0 | 1.9 | 3.5 |
| Node overhead outside pods (kubelet, container runtime, system) | 613.2 | 662.9 | 60.0 | 140.0 |
| **Node total** | 1748.4 | 1788.1 | 229.1 | 309.5 |

Container images on disk: 2332 MiB. (Node filesystem used: 78533 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 636.6 | 72.8 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 106.3 | 38.6 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 64.9 | 17.8 |
| kgateway-system/kgateway-7b8984bc55-rgcx7 | Gateway (kgateway) | 35.6 | 1.7 |
| cert-manager/cert-manager-cainjector-784846b495-fqkhh | Certificates (cert-manager) | 30.5 | 1.3 |
| cert-manager/cert-manager-bc44b5799-mjpmx | Certificates (cert-manager) | 25.8 | 1.0 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 23.0 | 7.7 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-6gshv | Application platform: control plane | 22.3 | 6.6 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.4 | 8.8 |
| kgateway-system/serendip-external-657d66d6c7-z6jt6 | Gateway (kgateway) | 18.2 | 3.2 |

serendipd with the package idle: 31.0 MiB RSS, 0.44 mcores mean (without the package, same run: 29.6 MiB, 0.00 mcores). Resync on the fingerprint path: **+0.44 mcores**.

### Build: first (cold node image cache)

Succeeded, commit `0b8ae2b15ca1`, 3m49s seen by the harness (3m30s running). 68 samples.

| | Mean | Max |
|---|---|---|
| Builds group memory (MiB) | 100.2 | 276.1 |
| Builds group CPU (mcores) | 82.4 | 583.6 |
| Node memory (MiB) | 2111.8 | 2605.2 |
| Node CPU (mcores) | 770.7 | 2002.8 |
| serendipd CPU (mcores) | 1.36 | 13.86 |

### Build: second (warm node image cache)

Succeeded, commit `0b8ae2b15ca1`, 2m31s seen by the harness (2m16s running). 45 samples.

| | Mean | Max |
|---|---|---|
| Builds group memory (MiB) | 112.6 | 218.5 |
| Builds group CPU (mcores) | 99.4 | 609.9 |
| Node memory (MiB) | 2301.4 | 2742.9 |
| Node CPU (mcores) | 997.8 | 2002.6 |
| serendipd CPU (mcores) | 1.05 | 9.21 |

### Auto-build on (332 s)

AutoBuild condition: Watching. serendipd 0.38 mcores mean (-0.06 against package idle). Builds group after the builds: 33.7 MiB, 2.0 mcores.

### One full Helm run (what each resync would cost without the fingerprint)

5s wall, helm's own CPU 2.31 s, node CPU 235 mcores mean / 235 max over 2 samples.

### Stated cost (Requirements)

The node's use beyond the platform phase above (1614 MiB, 216 mcores mean), never below the package's own pods.

| | Idle memory | Idle CPU | Peak memory | Peak CPU |
|---|---|---|---|---|
| **Stated (node)** | 453 MiB | 19 mcores | 1129 MiB | 1787 mcores |
| Package's own pods | 34 MiB | 3 mcores | 277 MiB | 610 mcores |
