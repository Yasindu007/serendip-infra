# Spec 019 approvals: performance (T009)

The targets are from `specs/019-approvals/tasks.md` T009 and T007a.

**Verdict:**
- **Approval reads with 20,000 approvals:** PASS.
- **Idle memory:** PASS.
- **DELETE overhead:** **FAIL** against the "< 1 ms p99" target. Recording each delete's Approval adds about **+0.6 ms mean and +3 ms p99** on the Windows NVMe. The cost is disk I/O, not CPU (see "Why"). On the simulated T470, disk noise is larger than the difference, so the result there is inconclusive.
- **Decided 2026-10-20:** the user accepted the cost (option 1 below), and the target became "+≤ 1 ms mean added; p99 reported". `synchronous` stays `FULL`. See `docs/risk-register.md` RP-001 and RP-002.
- **Not yet run:** the edge-1 harness on the simulated T470 kind node needs the cluster. It runs with T011.

## Setup

- **Machine:** AMD Ryzen 7 7840HS, 31 GiB, Windows 11, Go (see `go-version.txt`).
- **Simulated T470:** the same test binary (`GOOS=linux`, `CGO_ENABLED=0`) runs in a container with the T470 simulation's CPU limits (`docker run --cpus 2 --cpuset-cpus 0-3`, `kindest/node:v1.34.0`), as spec 017 did. No cluster is needed.
- **Baseline:** `e18a226`, the last commit before spec 019's code. `api/delete_bench_test.go` uses only APIs that existed then, so the same file runs on both.
- **HEAD and baseline are run alternately,** four rounds each, so both see the same disk conditions.

## DELETE (`BenchmarkDelete`, 2000 deletes per run)

`DELETE /v1/components/{id}` goes through the full server: authentication, authorization and the store.

### Windows NVMe (`delete-windows.txt`)

| Round | Mean base → HEAD | p99 base → HEAD | Added mean | Added p99 |
|---|---|---|---|---|
| 1 | 1.09 → 1.60 ms | 2.5 → 5.5 ms | +0.51 ms | +2.9 ms |
| 2 | 2.67 → 3.29 ms | 5.5 → 8.7 ms | +0.62 ms | +3.2 ms |
| 3 | 2.62 → 3.20 ms | 5.4 → 8.4 ms | +0.58 ms | +3.0 ms |
| 4 | 1.17 → 1.88 ms | 2.6 → 6.6 ms | +0.71 ms | +4.0 ms |
| **Median** | | | **+0.60 ms** | **+3.1 ms** |

Allocations go from 279 to 459 per delete, and from 13.4 to 26.3 KB.

### Simulated T470 (`delete-simulated-t470.txt`)

Per-round mean differences (HEAD − base) are −1.8, +1.0, +0.7 and −1.4 ms. p99 differences are −4.3, +4.1, −4.7 and −3.3 ms. Docker's virtual disk varies by more than the effect, so this run can't tell the two builds apart.

`delete-simulated-t470-before-0007.txt` is the first run, made before migration `0007` replaced `0006`'s all-kinds paging index. That run is why `0007` exists: an index over every kind made every write anywhere in Serendip pay for approvals' paging.

### Why

A CPU profile of the DELETE (Windows, 3000 deletes) shows the time split as follows:
- about **75%** in SQLite's commit (`sqlite3PagerCommitPhaseOne`, which writes and syncs the WAL);
- about **3%** in `recordApproval` itself.

The Approval is written in the delete's own transaction, as plan decision 2 requires so that a delete never commits without it. That transaction writes these extra pages: the Approval row, its audit row, and its five index entries. The p99 roughly doubles, which fits SQLite's WAL checkpoints (each delete now writes about twice as many pages, so checkpoints, the slow requests, come about twice as often). That is our reading, not measured directly.

**Options:**
1. **Accept the cost and amend the target** to "+≤ 1 ms mean added; p99 reported". Deletes are rare and human-paced, and this record is what constitution 5 requires.
2. **Shrink the record,** for example by not snapshotting the Approval in its own audit row. Not recommended, since it breaks constitution 8's uniform trail.
3. **`PRAGMA synchronous=NORMAL`** (WAL mode). This removes the per-commit sync for every write, not just deletes. On power loss or an OS crash, the last commits can be lost; a process crash loses nothing, and the database is never corrupted. That is a platform-wide durability decision that needs its own ADR, not spec 019.

## Approval reads with 20,000 approvals (`approvals-simulated-t470.txt`)

The setup is 19,950 executed and settled approvals plus 50 pending ones, seeded in the shape the store writes. Results are from the simulated T470, 3 runs × 3 s. The target is p99 under 50 ms.

| Request | p50 | p99 (range of 3 runs) | Verdict |
|---|---|---|---|
| `GET /v1/approvals`, first page of 100, Owner | 6.8–7.6 ms | 13.8–19.8 ms | PASS |
| `GET /v1/approvals`, first page of 100, Reader at the Project | 6.7–7.3 ms | 13.6–22.3 ms | PASS |
| `GET /v1/approvals?phase=Pending` | 3.4–3.5 ms | 6.5–10.1 ms | PASS |
| `POST /v1/approvals`, including the duplicate check | 0.23–0.26 ms | 0.77–1.2 ms | PASS |
| Reconciler resync list (unsettled approvals) | 0.75–0.78 ms | 2.0–3.3 ms | PASS |

The page of 100 dominates the list cost: each approval in the page is parsed and gets a read decision. The cost doesn't grow with the total number of approvals; the query-plan tests in `core/store/sqlite/approval_test.go` pin every approval query to its index.

## Idle memory (`idle-windows.txt`)

The method is spec 017's: a fresh store, no cluster, one API key, four authenticated reads, then 30 s idle, then three samples 5 s apart, three runs per build. Each line gives private bytes, then the working set, in MiB.

| Build | Private bytes (median) | Growth |
|---|---|---|
| Before spec 019 (`e18a226`) | 15.7 MiB | |
| Spec 019 | 15.9 MiB | **+0.2 MiB** |

**Rule 4: PASS.** `serendipd` stays far below 256 MiB.
