| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 785.8 | 790.6 | PASS | 169.4 | 173.5 | PASS |
| Secrets (OpenBao) | 18.0 | 18.0 | PASS | 10.4 | 10.3 | PASS |
| Certificates (cert-manager) | 64.8 | 64.2 | PASS | 2.6 | 2.8 | PASS |
| Application platform: control plane | 55.3 | 54.7 | PASS | 7.8 | 6.5 | PASS |
| Application platform: data plane | 6.4 | 7.4 | PASS | 0.0 | 0.0 | PASS |
| Gateway (kgateway) | 54.0 | 50.5 | PASS | 7.2 | 7.1 | PASS |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. Overall: **PASS**
