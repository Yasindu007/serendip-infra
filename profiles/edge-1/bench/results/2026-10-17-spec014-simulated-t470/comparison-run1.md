| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 775.5 | 785.8 | PASS | 154.4 | 169.4 | PASS |
| Secrets (OpenBao) | 18.0 | 18.0 | PASS | 9.8 | 10.4 | PASS |
| Certificates (cert-manager) | 65.8 | 64.8 | PASS | 2.4 | 2.6 | PASS |
| Application platform: control plane | 58.0 | 55.3 | PASS | 4.9 | 7.8 | PASS |
| Application platform: data plane | 6.7 | 6.4 | PASS | 0.0 | 0.0 | PASS |
| Gateway (kgateway) | 51.7 | 54.0 | PASS | 7.3 | 7.2 | PASS |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. Overall: **PASS**
