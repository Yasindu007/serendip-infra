| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 785.8 | 789.0 | PASS | 169.4 | 155.9 | PASS |
| Secrets (OpenBao) | 18.0 | 17.8 | PASS | 10.4 | 9.7 | PASS |
| Certificates (cert-manager) | 64.8 | 62.0 | PASS | 2.6 | 2.7 | PASS |
| Application platform: control plane | 55.3 | 54.4 | PASS | 7.8 | 4.4 | PASS |
| Application platform: data plane | 6.4 | 8.0 | PASS | 0.0 | 0.0 | PASS |
| Gateway (kgateway) | 54.0 | 52.4 | PASS | 7.2 | 6.8 | PASS |
| Identity (ThunderID) | — | 10.8 | new | — | 0.2 | new |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. "new" groups exist only in run B and are not part of the verdict. Overall: **PASS**
