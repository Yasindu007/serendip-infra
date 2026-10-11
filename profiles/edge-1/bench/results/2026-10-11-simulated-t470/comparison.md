| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |
|---|---|---|---|---|---|---|
| Kubernetes system | 775.5 | 815.8 | PASS | 154.4 | 180.3 | FAIL |
| Secrets (OpenBao) | 18.0 | 17.8 | PASS | 9.8 | 10.8 | PASS |
| Certificates (cert-manager) | 65.8 | 65.3 | PASS | 2.4 | 6.9 | PASS |
| Application platform: control plane | 58.0 | 54.7 | PASS | 4.9 | 11.2 | PASS |
| Application platform: data plane | 6.7 | 6.6 | PASS | 0.0 | 0.1 | PASS |
| Gateway (kgateway) | 51.7 | 52.4 | PASS | 7.3 | 7.6 | PASS |

Tolerance: memory ±10% for groups ≥ 50 MiB, else ±10 MiB; CPU ±25 millicores. Overall: **FAIL**
