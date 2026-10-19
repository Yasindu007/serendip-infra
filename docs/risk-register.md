# Risk and performance register

Known risks, performance costs and trade-offs, kept in one place so they
aren't lost inside a spec's plan or a benchmark summary. Each entry says
what it is, what it costs or could cost, what holds it today, and **when
to look at it again**.

- **Add an entry** whenever a spec, a benchmark or a review finds
  something that is accepted rather than fixed, or fixed in a way worth
  remembering.
- **Close an entry** by setting its status, keeping the entry: the
  history is the point.
- Specs' own plan.md Risks sections still hold detail; an entry here
  links to them.

Status values: **Open** (live, accepted for now), **Watch** (fine today,
with a named trigger to revisit), **Decided** (a trade-off chosen on
purpose), **Closed** (fixed).

## Index

| ID | Title | Kind | Status | Found in |
|---|---|---|---|---|
| RP-001 | Every write syncs to disk (`synchronous = FULL`) | Performance vs durability | Decided | Spec 019 T009 |
| RP-002 | Recording an Approval adds to every DELETE | Performance | Decided | Spec 019 T009 |
| RP-003 | Approvals only grow | Capacity | Open | Spec 019 T007a |
| RP-004 | The identity-provider tunnel rides a legacy Kubernetes protocol | Compatibility, security | Watch | Spec 018 |
| RP-005 | A deleted Secret's value is erased after its record | Consistency | Decided | Spec 019 T004 |
| RP-006 | Approval pages can come back short | Behaviour | Decided | Spec 019 T007a |
| RP-007 | An agent's sponsor can approve its requests | Security | Open | Spec 019 plan |
| RP-008 | No measurement on a real T470 yet | Performance evidence | Open | Spec 019 T009 |
| RP-009 | Approval list cost scales with page size | Performance | Watch | Spec 019 T009 |
| RP-010 | Timestamps from before fixed-width writes can misorder | Correctness | Watch | Spec 019 T007a |
| RP-011 | A delete path could skip its Approval | Correctness | Closed | Spec 019 planning |
| RP-012 | An index over every kind taxed every write | Performance | Closed | Spec 019 T009 |
| RP-013 | An applied migration was edited | Process | Closed | Spec 019 T009 |

---

## RP-001 — Every write syncs to disk (`synchronous = FULL`)

- **Kind:** performance vs durability. **Status:** Decided (2026-10-20,
  the user: keep `FULL`).
- **What:** the store runs SQLite in WAL mode with `synchronous = FULL`
  (checked on a live connection: `synchronous = 2`). Every commit is
  forced to disk before the API answers.
- **Cost:** that sync is most of each write's time: about 1–3 ms per
  DELETE on the dev NVMe, 3–7 ms on the simulated T470's virtual disk
  (`profiles/edge-1/bench/results/2026-10-19-spec019-approvals/`).
- **The alternative and why not:** `synchronous = NORMAL` syncs only at
  checkpoints, so writes would get much faster. A process crash would
  still lose nothing, and the database is never corrupted. But on a power
  loss or an OS crash, every commit since the last checkpoint (minutes,
  on a quiet install) is lost, after its effects outside the database
  already happened:
  - a deleted Application comes back, and the reconciler redeploys it;
  - its Secrets come back as records whose values were already erased;
  - a revoked role assignment comes back;
  - the audit log loses the record of what actually happened
    (constitution 8).

  Control-plane stores sync every write for this reason (etcd does).
- **Revisit when:** writes become a measured bottleneck on the reference
  T470 (RP-008). Changing it needs an ADR, measured on the T470, and
  should consider middle grounds (`FULL` only for destructive and
  access-management writes; more frequent checkpoints).

## RP-002 — Recording an Approval adds to every DELETE

- **Kind:** performance. **Status:** Decided (2026-10-20, the user:
  accept; spec 019 T009's target amended).
- **What:** every delete writes its Approval, the Approval's audit row and
  its index entries in the delete's own transaction (spec 019 plan
  decision 2), so a delete never commits without its record.
- **Cost:** about **+0.6 ms mean and +3 ms p99** per DELETE on the dev
  NVMe (paired runs against the commit before spec 019). It's commit
  I/O, not CPU: a profile puts about 75% of a DELETE in SQLite's commit
  and about 3% in recording the Approval. The p99 roughly doubles, which
  fits checkpoints coming about twice as often (inferred). On the
  simulated T470, disk noise was larger than the effect.
- **Why accepted:** deletes are rare and human-paced, and this record is
  what constitution 5 requires. Shrinking it (no audit snapshot of the
  Approval) would weaken constitution 8.
- **Revisit when:** a workload deletes in bulk, or RP-001 is revisited
  (the two move together).

## RP-003 — Approvals only grow

- **Kind:** capacity. **Status:** Open, until the retention spec.
- **What:** every delete adds an Approval of about 2–4 KiB (more for a
  large cascade: each removed resource's last spec is kept for clean-up).
  None are deleted; that's spec 019's rule.
- **Held by:** reads are bounded by indexes and paging (migrations 0006
  and 0007; T009 measured 20,000 approvals at under 22 ms p99), so
  growth costs disk, not speed.
- **Revisit when:** the retention spec is written. The audit log already
  keeps every Approval's snapshot, so archiving loses nothing. Rough
  scale: 50 deletes a day is about 18,000 approvals and 35–70 MiB a year.

## RP-004 — The identity-provider tunnel rides a legacy Kubernetes protocol

- **Kind:** compatibility and security. **Status:** Watch.
- **What:** `serendipd` reaches ThunderID's management API through its own
  in-process port-forward (ADR 0015, `providers/kubernetes`). It uses the
  kubelet's older WebSocket mode, `v4.channel.k8s.io`, not KEP-4006's
  newer SPDY-over-WebSocket tunnel, which would need a SPDY/3.1
  implementation (too heavy for stdlib-only).
- **Risks:**
  - **Upstream removal:** if a future Kubernetes stops passing the older
    mode through the API server, the channel stops working. Verified on
    kind 1.34 and on our K3s pin, v1.36.4+k3s1.
  - **Security-relevant custom code:** a hand-written WebSocket client
    carries ThunderID's full administrator (`system`) token. It checks the
    handshake, bounds frame sizes, refuses a bearer token over plain http
    to a non-loopback host, and never returns pod internals in errors.
    Reviewed in spec 018 T013.
  - **Cost:** about 7 ms per directory read on a warm tunnel; about 39 ms
    when a new one opens (idle tunnels close at 30 s).
- **If it breaks:** only the live directory degrades. Lookups fall back to
  the people seen at sign-in (`source: signedIn`); sign-in and every other
  route are unaffected.
- **Revisit when:**
  - the K3s pin moves (re-run the spec 018 T001 port-forward check first);
  - Kubernetes deprecates the older WebSocket port-forward mode;
  - ThunderID offers fine-grained management permissions, which should
    replace the full administrator token;
  - `serendipd` moves into the cluster, where the Service's address
    replaces the tunnel.

## RP-005 — A deleted Secret's value is erased after its record

- **Kind:** consistency. **Status:** Decided (spec 019 plan decision 7).
- **What:** the Secret's record is deleted first; the approval reconciler
  erases its value from the Secrets provider afterwards, from what the
  Approval recorded, and retries until it succeeds (including across an
  outage and a restart, tested in T008).
- **Window:** seconds normally; as long as the provider is down. In
  between, the value can't be reached: access is decided per Secret
  record, and Secret IDs are never reused. The Approval shows
  `CleanedUp` and `Verified` when it's done.
- **This fixed:** cascades that removed Secrets used to leave their values
  in the provider for good.
- **Revisit when:** a Secrets provider can't make erasure idempotent (a
  repeat erase must succeed).

## RP-006 — Approval pages can come back short

- **Kind:** behaviour. **Status:** Decided (spec 019 plan decision 1).
- **What:** `GET /v1/approvals` cuts a page before the per-approval read
  check, so a caller who may read few approvals can get short or empty
  pages and must follow `Serendip-Next-After`, as Azure's paged lists do.
- **Revisit when:** a client finds it confusing in practice; a
  scope-aware query could filter before cutting.

## RP-007 — An agent's sponsor can approve its requests

- **Kind:** security. **Status:** Open.
- **What:** the requester can never decide their own request, but an
  agent's sponsor (the person accountable for it) may approve the agent's
  request. Spec 019 allows it.
- **Revisit when:** approval policies for people arrive (spec 019's
  "Scope — OUT"): "not the sponsor" is a natural rule there.

## RP-008 — No measurement on a real T470 yet

- **Kind:** performance evidence. **Status:** Open.
- **What:** every T470 figure so far comes from a simulation: a kind node
  or container with the T470's CPU limits, on the dev machine's Docker
  virtual disk. CPU limits are faithful; disk behaviour isn't, and spec
  019 showed disk dominates writes (RP-001, RP-002).
- **Revisit when:** real reference hardware is available; re-run the
  spec 013 harness and the write benchmarks there before changing any
  durability or profile default.

## RP-009 — Approval list cost scales with page size

- **Kind:** performance. **Status:** Watch.
- **What:** each approval on a page is parsed and gets a read decision
  (one to four existence checks, then a policy decision). A page of 100 is
  7 ms p50 and up to 22 ms p99 on the simulated T470; the total number of
  approvals doesn't matter.
- **Revisit when:** the console needs larger pages, or a faster list;
  caching the existence checks per request would be the first step.

## RP-010 — Timestamps from before fixed-width writes can misorder

- **Kind:** correctness. **Status:** Watch.
- **What:** timestamps are stored as text. Before spec 019 T007a they were
  written with trimmed fractions, which can sort out of order when two are
  less than a microsecond apart. They're now written at a fixed width;
  older rows weren't rewritten.
- **Impact:** at most, two rows written within the same microsecond before
  the change list in the wrong order. Paging approvals is only affected
  for approvals from that window.
- **Revisit when:** ordering bugs appear; a migration could rewrite old
  timestamps.

## RP-011 — A delete path could skip its Approval (Closed)

- **Found:** spec 019 planning: the store wrote an Approval only when the
  request carried one.
- **Fixed:** the store refuses any delete without an approval
  (`store.ErrNoApproval`, T003), with no bypass; a route test also names
  any DELETE route that couldn't attach one.

## RP-012 — An index over every kind taxed every write (Closed)

- **Found:** spec 019 T009. Migration 0006's newest-first index covered
  every kind, so every write anywhere in Serendip maintained it for the
  sake of approvals' paging.
- **Fixed:** migration 0007 replaced it with an approvals-only index.

## RP-013 — An applied migration was edited (Closed)

- **Found:** spec 019 T009, before commit: a fix was first made by editing
  migration 0006 after it was committed. A database that had applied it
  would never have received the change, and its approval queries would
  have failed on the missing index.
- **Fixed:** 0006 restored unchanged; the fix moved to 0007, with a test
  of the migrated schema.
- **Rule:** a committed migration is never edited; fixes go in a new one.
