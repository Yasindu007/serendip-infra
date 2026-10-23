| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 741.6 | 859.1 | FAIL | 151.5 | 151.0 | PASS |
| Secrets (OpenBao) | 18.0 | 18.6 | PASS | 10.8 | 9.1 | PASS |
| Certificates (cert-manager) | 64.0 | 62.6 | PASS | 2.5 | 3.0 | PASS |
| Application platform: control plane | 56.2 | 54.9 | PASS | 7.0 | 7.8 | PASS |
| Application platform: data plane | 6.3 | 6.5 | PASS | 0.0 | 0.0 | PASS |
| Gateway (kgateway) | 53.4 | 52.5 | PASS | 8.7 | 7.5 | PASS |
| Identity (ThunderID) | 12.1 | 12.1 | PASS | 0.3 | 0.3 | PASS |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. "new" groups exist only in run B and are not part of the verdict. Overall: **FAIL**
