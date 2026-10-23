| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 813.3 | 859.1 | PASS | 173.7 | 151.0 | PASS |
| Secrets (OpenBao) | 18.4 | 18.6 | PASS | 9.2 | 9.1 | PASS |
| Certificates (cert-manager) | 64.7 | 62.6 | PASS | 3.5 | 3.0 | PASS |
| Application platform: control plane | 56.9 | 54.9 | PASS | 7.9 | 7.8 | PASS |
| Application platform: data plane | 6.7 | 6.5 | PASS | 0.1 | 0.0 | PASS |
| Gateway (kgateway) | 52.6 | 52.5 | PASS | 8.0 | 7.5 | PASS |
| Identity (ThunderID) | 12.1 | 12.1 | PASS | 0.5 | 0.3 | PASS |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. "new" groups exist only in run B and are not part of the verdict. Overall: **PASS**
