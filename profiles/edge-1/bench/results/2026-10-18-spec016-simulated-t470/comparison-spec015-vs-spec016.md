| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 806.0 | 803.0 | PASS | 144.5 | 166.3 | PASS |
| Secrets (OpenBao) | 17.8 | 19.7 | PASS | 8.9 | 9.9 | PASS |
| Certificates (cert-manager) | 64.2 | 63.0 | PASS | 2.6 | 2.7 | PASS |
| Application platform: control plane | 56.5 | 55.9 | PASS | 6.3 | 8.2 | PASS |
| Application platform: data plane | 7.7 | 6.7 | PASS | 0.0 | 0.0 | PASS |
| Gateway (kgateway) | 53.5 | 53.8 | PASS | 6.1 | 6.0 | PASS |
| Identity (ThunderID) | 10.6 | 11.6 | PASS | 0.2 | 0.2 | PASS |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. "new" groups exist only in run B and are not part of the verdict. Overall: **PASS**
