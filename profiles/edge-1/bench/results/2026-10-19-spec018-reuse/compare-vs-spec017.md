| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 786.4 | 766.1 | PASS | 158.9 | 154.0 | PASS |
| Secrets (OpenBao) | 18.1 | 18.4 | PASS | 11.3 | 7.4 | PASS |
| Certificates (cert-manager) | 64.4 | 63.9 | PASS | 2.9 | 2.7 | PASS |
| Application platform: control plane | 54.4 | 56.0 | PASS | 7.3 | 5.0 | PASS |
| Application platform: data plane | 6.5 | 6.6 | PASS | 0.1 | 0.1 | PASS |
| Gateway (kgateway) | 51.9 | 52.9 | PASS | 7.0 | 5.8 | PASS |
| Identity (ThunderID) | 11.6 | 12.3 | PASS | 0.3 | 0.3 | PASS |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. "new" groups exist only in run B and are not part of the verdict. Overall: **PASS**
