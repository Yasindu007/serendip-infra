| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 775.5 | 790.6 | PASS | 154.4 | 173.5 | PASS |
| Secrets (OpenBao) | 18.0 | 18.0 | PASS | 9.8 | 10.3 | PASS |
| Certificates (cert-manager) | 65.8 | 64.2 | PASS | 2.4 | 2.8 | PASS |
| Application platform: control plane | 58.0 | 54.7 | PASS | 4.9 | 6.5 | PASS |
| Application platform: data plane | 6.7 | 7.4 | PASS | 0.0 | 0.0 | PASS |
| Gateway (kgateway) | 51.7 | 50.5 | PASS | 7.3 | 7.1 | PASS |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. Overall: **PASS**
