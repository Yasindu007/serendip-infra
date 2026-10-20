| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 766.1 | 741.6 | PASS | 154.0 | 151.5 | PASS |
| Secrets (OpenBao) | 18.4 | 18.0 | PASS | 7.4 | 10.8 | PASS |
| Certificates (cert-manager) | 63.9 | 64.0 | PASS | 2.7 | 2.5 | PASS |
| Application platform: control plane | 56.0 | 56.2 | PASS | 5.0 | 7.0 | PASS |
| Application platform: data plane | 6.6 | 6.3 | PASS | 0.1 | 0.0 | PASS |
| Gateway (kgateway) | 52.9 | 53.4 | PASS | 5.8 | 8.7 | PASS |
| Identity (ThunderID) | 12.3 | 12.1 | PASS | 0.3 | 0.3 | PASS |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. "new" groups exist only in run B and are not part of the verdict. Overall: **PASS**
