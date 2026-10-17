| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 806.0 | 789.0 | PASS | 144.5 | 155.9 | PASS |
| Secrets (OpenBao) | 17.8 | 17.8 | PASS | 8.9 | 9.7 | PASS |
| Certificates (cert-manager) | 64.2 | 62.0 | PASS | 2.6 | 2.7 | PASS |
| Application platform: control plane | 56.5 | 54.4 | PASS | 6.3 | 4.4 | PASS |
| Application platform: data plane | 7.7 | 8.0 | PASS | 0.0 | 0.0 | PASS |
| Gateway (kgateway) | 53.5 | 52.4 | PASS | 6.1 | 6.8 | PASS |
| Identity (ThunderID) | 10.6 | 10.8 | PASS | 0.2 | 0.2 | PASS |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. "new" groups exist only in run B and are not part of the verdict. Overall: **PASS**
