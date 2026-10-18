# Spec 018 agents and directory: performance (T010)

Targets are from `specs/018-agent-manager-poc/tasks.md` T010 and spec.md
AC 11:
- an agent caller adds under 100 µs p99 per request over a person with
  the same role;
- `serendipd` still passes constitution rule 4.

**Verdict: PASS.**

## Setup

- **Machine:** AMD Ryzen 7 7840HS, 31 GiB, Windows 11. Go 1.25.0.
- **Linux:** WSL2 Ubuntu (kernel 6.6.87, 4 vCPU), running `GOOS=linux`,
  `CGO_ENABLED=0` test binaries, as in spec 017.
- **Simulated T470:** the same binaries in a `kindest/node:v1.34.0`
  container with `--cpus 2 --cpuset-cpus 0-3`, the limits
  `kind-t470-setup.sh` applies. Neither benchmark needs a cluster.
- **The benchmarks:**
  - `providers/identity/oidc` `BenchmarkTokens`: real RS256 tokens. It
    compares a person's token checked by the human verifier alone (the
    spec 017 path) with a person's token and an agent's token through the
    issuer router.
  - `api` `BenchmarkAgentRequests`: the full server (`NewServer`). A
    person and an agent hold the same Reader role at the Project, so
    every decision walks the five-level chain.
    - The requests: one Component, and a 100-item list.
    - The agent path adds the sponsor lookup and the guardrail checks.
- **Percentiles** come from per-request timings, as in spec 017.

## Results

### Token verification (`bench-linux-wsl2.txt`, `bench-simulated-t470.txt`; 3 runs × 3 s, medians)

| Path | Linux p50 / p99 | T470 p50 / p99 | Allocs |
|---|---|---|---|
| Person, human verifier only | 56 / 243 µs | 58 / 318 µs | 85 |
| Person, through the router | 60 / 247 µs | 63 / 322 µs | 103 |
| Agent, through the router | 58 / 221 µs | 61 / 288 µs | 72 |

- **The router adds about 4 µs at p50.** It decodes the token's payload
  once to read `iss`.
- **An agent's token costs no more than a person's.**
- **Time is dominated by the RSA signature check.**

### Whole requests, person vs agent with the same role

| Request | Where | Person mean / p50 / p99 | Agent mean / p50 / p99 | p99 added | Target | Verdict |
|---|---|---|---|---|---|---|
| One Component | Linux, 8 runs × 4 s (`bench-linux-wsl2-get-x8.txt`) | 84.6 / 56 / 435 µs | 85.2 / 57.5 / 424 µs | none measurable | < 100 µs | PASS |
| One Component | Simulated T470, 5 runs × 3 s | 76.7 / 54 / 376 µs | 89.2 / 59 / 432 µs | 56 µs | < 100 µs | PASS |
| 100-item list | Linux, 3 runs × 3 s | 1.87 / 1.64 / 4.52 ms | 2.12 / 1.73 / 6.37 ms | tail noise | n/a | n/a |
| 100-item list | Simulated T470, 5 runs × 3 s | 1.99 / 1.67 / 6.44 ms | 2.05 / 1.75 / 5.94 ms | none (mean +0.06 ms) | n/a | n/a |

- **An agent's request allocates 3 more objects** (157 → 160) and about
  80 B.
- **The first Linux run looked like it might miss.** With 3 runs, the
  single-request p99 medians were 380 → 490 µs: +110 µs, just over the
  target. The p99s ranged 370–457 µs for the person and 413–533 µs for
  the agent, on 4 vCPU with Docker Desktop and two kind clusters running.
  Eight runs of 4 s show no difference: 435 against 424 µs. The
  simulated T470 confirms it at +56 µs.
- **The cost is small because the sponsor is read from the policy index.**
  An earlier draft scanned every assignment per request. go-reviewer's
  T007 finding moved the sponsor into an index the engine builds at
  reload.

### The directory over the port-forward (`directory-latency.txt`)

Measured through `serendipd` on kind `serendip-t018`, end to end with
curl, using the API key (Owner at `/`):

| Request | n | Median | Max |
|---|---|---|---|
| `GET /v1/authorization/directory?type=person&q=adm` | 20 | 6.9 ms | 13.1 ms |
| `GET /v1/authorization/principals/person/{id}/groups` | 20 | 6.5 ms | 12.0 ms |
| The first search after 40 s idle (a new tunnel) | 1 | 38.6 ms | n/a |

- **The admin token is cached,** so only the first call after startup or
  after it expires pays for the login: about 300 ms (T001).
- **Idle tunnels close after 30 s** (`IdleConnTimeout`). Reopening one
  costs about 30 ms: the WebSocket upgrade plus the Service and pod
  lookups.

### Idle memory (`mem-windows.txt`, `mem-cluster.txt`)

**The method** is spec 017's. The two builds are `b993ef3` (the end of
spec 017) and this commit, both embedding the same console build. Each
run starts with a fresh store and one API key, makes four authenticated
reads, then idles. Three samples are taken 5 s apart.

| Build | No cluster: private / working set (3 runs, medians) | On kind, directory wired, tunnel idle (1 run) |
|---|---|---|
| Spec 017 | 16.2 / 14.1 MiB | 21.2 / 20.6 MiB |
| Spec 018 | 16.4 / 14.2 MiB | 21.3 / 20.8 MiB |
| **Growth** | **+0.2 / +0.1 MiB** | **+0.1 / +0.2 MiB** |

- **The first spec 017 run's working set was 24.4 MiB,** an outlier
  before Windows trimmed it. The medians use all three runs.
- **The binary grew from 20.19 MB to 20.43 MB.**
- **Rule 4's line is under 256 MiB for core, api and agent together.**
  `serendipd` was at 36.3 MiB on the simulated T470 in spec 017, and
  this adds a fraction of a MiB: **PASS**.
- **A fresh simulated-T470 footprint run** is part of T011's cluster
  verification.
