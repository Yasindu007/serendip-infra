> **SIMULATION — does not satisfy the reference-hardware gate.**

# edge-1 footprint — 2026-11-09 11:27 +0530

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
| Kubernetes system | 811.8 | 828.9 | 156.8 | 205.5 |
| Secrets (OpenBao) | 18.5 | 18.5 | 9.0 | 10.1 |
| Certificates (cert-manager) | 63.0 | 63.0 | 2.9 | 4.3 |
| Application platform: control plane | 56.2 | 61.0 | 5.1 | 7.5 |
| Application platform: data plane | 6.5 | 6.6 | 0.0 | 0.2 |
| Gateway (kgateway) | 53.6 | 53.6 | 5.5 | 7.8 |
| Identity (ThunderID) | 12.0 | 12.0 | 0.2 | 0.5 |
| Node overhead outside pods (kubelet, container runtime, system) | 574.5 | 581.8 | 71.3 | 111.0 |
| **Node total** | 1596.1 | 1616.0 | 251.0 | 301.0 |

Container images on disk: 2199 MiB. (Node filesystem used: 78384 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 544.1 | 78.1 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 103.0 | 42.0 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 73.3 | 21.8 |
| kgateway-system/kgateway-7b8984bc55-n2pc4 | Gateway (kgateway) | 35.8 | 2.1 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 29.8 | 8.6 |
| cert-manager/cert-manager-bc44b5799-wz8j5 | Certificates (cert-manager) | 26.1 | 1.2 |
| cert-manager/cert-manager-cainjector-784846b495-qw74t | Certificates (cert-manager) | 24.9 | 1.3 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-6ql7g | Application platform: control plane | 22.8 | 4.3 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.5 | 9.0 |
| kgateway-system/serendip-external-657d66d6c7-6zmz7 | Gateway (kgateway) | 17.9 | 3.4 |

### serendipd (host process)

| | Mean | Max |
|---|---|---|
| RSS (MiB) | 28.3 | 29.4 |
| CPU (millicores) | 0.3 | 1.5 |

### Constitution rule 4 (control plane < 256 MiB RSS, < 5% CPU at idle)

- RSS 28.3 MiB: **PASS**
- CPU 0.01% of 2.0 cores: **PASS**
- covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured


### 16 GB headroom

**Verdict: not computed: no light-workload data.**

### Timings

- Cold start (fresh cluster, includes image pulls): 590s
- Warm restart (everything installed): 38s
- Component create → Serving:  (includes the image pull; status is re-checked about 2, 6, 14, 22 and 30s after create)

## Package: openchoreo-workflow-plane

Install (create → Ready, with image pulls): 2m16s

### Installed, idle (60 samples)

| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |
|---|---|---|---|---|
| Kubernetes system | 1427.0 | 1557.5 | 174.8 | 292.1 |
| Secrets (OpenBao) | 18.7 | 18.8 | 8.7 | 10.8 |
| Certificates (cert-manager) | 121.0 | 121.1 | 3.1 | 5.2 |
| Application platform: control plane | 56.4 | 56.7 | 5.9 | 7.5 |
| Application platform: data plane | 6.3 | 6.4 | 0.0 | 0.3 |
| Gateway (kgateway) | 53.9 | 53.9 | 5.5 | 7.1 |
| Identity (ThunderID) | 11.9 | 11.9 | 0.3 | 0.9 |
| Builds (workflow plane) | 27.4 | 27.9 | 2.1 | 2.7 |
| Node overhead outside pods (kubelet, container runtime, system) | 624.7 | 650.1 | 66.1 | 145.9 |
| **Node total** | 2347.3 | 2481.9 | 266.6 | 463.6 |

Container images on disk: 2399 MiB. (Node filesystem used: 78600 MiB; on kind this is the whole Docker VM disk, not Serendip's.)

Largest pods by memory:

| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |
|---|---|---|---|
| kube-system/kube-apiserver-bench-t470-control-plane | Kubernetes system | 1124.9 | 93.0 |
| kube-system/etcd-bench-t470-control-plane | Kubernetes system | 133.3 | 44.2 |
| cert-manager/cert-manager-cainjector-784846b495-qw74t | Certificates (cert-manager) | 82.9 | 1.5 |
| kube-system/kube-controller-manager-bench-t470-control-plane | Kubernetes system | 73.9 | 22.6 |
| kgateway-system/kgateway-7b8984bc55-n2pc4 | Gateway (kgateway) | 36.0 | 2.0 |
| kube-system/kube-scheduler-bench-t470-control-plane | Kubernetes system | 29.5 | 8.3 |
| cert-manager/cert-manager-bc44b5799-wz8j5 | Certificates (cert-manager) | 26.0 | 1.1 |
| openchoreo-control-plane/controller-manager-6b99ffdcd-6ql7g | Application platform: control plane | 23.3 | 4.9 |
| serendip-system/openbao-0 | Secrets (OpenBao) | 18.7 | 8.7 |
| kgateway-system/serendip-external-657d66d6c7-6zmz7 | Gateway (kgateway) | 17.9 | 3.5 |

serendipd with the package idle: 30.2 MiB RSS, 0.81 mcores mean (without the package, same run: 28.3 MiB, 0.28 mcores). Resync on the fingerprint path: **+0.53 mcores**.

### Build: first (cold node image cache)

Succeeded, commit `0b8ae2b15ca1`, 4m10s seen by the harness (3m50s running). 72 samples.

| | Mean | Max |
|---|---|---|
| Builds group memory (MiB) | 117.4 | 312.8 |
| Builds group CPU (mcores) | 136.1 | 1105.5 |
| Node memory (MiB) | 2688.8 | 3268.6 |
| Node CPU (mcores) | 906.8 | 1995.4 |
| serendipd CPU (mcores) | 2.00 | 22.30 |

### Build: second (warm node image cache)

Succeeded, commit `0b8ae2b15ca1`, 3m31s seen by the harness (2m59s running). 61 samples.

| | Mean | Max |
|---|---|---|
| Builds group memory (MiB) | 113.1 | 309.3 |
| Builds group CPU (mcores) | 131.7 | 702.9 |
| Node memory (MiB) | 2812.8 | 3306.2 |
| Node CPU (mcores) | 979.6 | 2002.7 |
| serendipd CPU (mcores) | 1.56 | 17.36 |

### Auto-build on (335 s)

AutoBuild condition: Watching. serendipd 1.03 mcores mean (+0.22 against package idle). Builds group after the builds: 33.5 MiB, 2.2 mcores.

### One full Helm run (what each resync would cost without the fingerprint)

23s wall, helm's own CPU 2.39 s, node CPU 603 mcores mean / 1288 max over 8 samples.

### Stated cost (Requirements)

The node's use beyond the platform phase above (1596 MiB, 251 mcores mean), never below the package's own pods.

| | Idle memory | Idle CPU | Peak memory | Peak CPU |
|---|---|---|---|---|
| **Stated (node)** | 954 MiB | 25 mcores | 1711 MiB | 1752 mcores |
| Package's own pods | 34 MiB | 3 mcores | 313 MiB | 1106 mcores |
