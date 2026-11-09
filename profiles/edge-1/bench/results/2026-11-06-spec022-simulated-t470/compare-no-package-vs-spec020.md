| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 859.1 | 814.9 | PASS | 151.0 | 164.9 | PASS |
| Secrets (OpenBao) | 18.6 | 18.5 | PASS | 9.1 | 11.7 | PASS |
| Certificates (cert-manager) | 62.6 | 63.5 | PASS | 3.0 | 2.8 | PASS |
| Application platform: control plane | 54.9 | 55.8 | PASS | 7.8 | 7.6 | PASS |
| Application platform: data plane | 6.5 | 6.6 | PASS | 0.0 | 0.0 | PASS |
| Gateway (kgateway) | 52.5 | 52.6 | PASS | 7.5 | 5.9 | PASS |
| Identity (ThunderID) | 12.1 | 12.6 | PASS | 0.3 | 0.4 | PASS |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. "new" groups exist only in run B and are not part of the verdict. Overall: **PASS**
