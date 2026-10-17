| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 785.8 | 806.0 | PASS | 169.4 | 144.5 | PASS |
| Secrets (OpenBao) | 18.0 | 17.8 | PASS | 10.4 | 8.9 | PASS |
| Certificates (cert-manager) | 64.8 | 64.2 | PASS | 2.6 | 2.6 | PASS |
| Application platform: control plane | 55.3 | 56.5 | PASS | 7.8 | 6.3 | PASS |
| Application platform: data plane | 6.4 | 7.7 | PASS | 0.0 | 0.0 | PASS |
| Gateway (kgateway) | 54.0 | 53.5 | PASS | 7.2 | 6.1 | PASS |
| Identity (ThunderID) | — | 10.6 | new | — | 0.2 | new |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. "new" groups exist only in run B and are not part of the verdict. Overall: **PASS**
