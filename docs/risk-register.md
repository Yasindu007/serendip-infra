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
| RP-014 | Writing a new Secret's value can answer 409 after writing it | Correctness | Open | Spec 019 T011 |
| RP-015 | The console shows an agent's sponsor as a subject ID | Usability | Open | Spec 019 T011 |
| RP-016 | Migration 0006 marks old approvals settled without an audit row | Audit | Decided | Spec 019 T012 |
| RP-017 | The approvals paging cursor can reveal IDs the caller can't read | Data exposure (low) | Open | Spec 019 T012 |
| RP-018 | OpenChoreo's API answered any pod in the cluster, unauthenticated | Security (high) | Closed | Spec 020 research |
| RP-019 | A local AI assistant can read its person's CLI sign-in and act as them | Security (medium) | Accepted | Spec 021 T001 |

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

## RP-014 — Writing a new Secret's value can answer 409 after writing it

- **Kind:** correctness. **Status:** Open. **Found:** spec 019 T011, live;
  the code is from spec 009.
- **What:** `SecretReconciler.WriteValue` writes the value to the Secrets
  provider, then updates the Secret's status (`Reachable`). On a Secret
  created moments earlier, the reconciler's first status update can land
  in between, so the second update loses on `resourceVersion` and the API
  answers **409**. But the value *was* written.
- **Impact:** a client is told the write failed when it succeeded. A
  retry writes the same value again, so nothing is lost; it's a confusing
  answer, not data loss.
- **Fix when touched:** on `ErrConflict`, re-read the Secret and retry
  the status update (the value write itself is already done), or set the
  condition through the reconciler instead.

## RP-015 — The console shows an agent's sponsor as a subject ID

- **Kind:** usability. **Status:** Open. **Found:** spec 019 T011.
- **What:** on the Approvals pages, "Sponsor:" shows the person's subject
  (for example `01900000-…0030`), which is how an Approval records it.
- **Fix when touched:** resolve it through the directory the Access
  control page already uses (`GET /v1/authorization/directory`), falling
  back to the ID.

## RP-016 — Migration 0006 marks old approvals settled without an audit row

- **Kind:** audit (constitution 8). **Status:** Decided. **Found:** spec
  019 T012 (contract review).
- **What:** migration 0006 sets `status.settled` on approvals recorded
  before the field existed (rejected, expired, failed, or executed and
  verified). That's a data fix by the migration itself: no audit row, no
  `resourceVersion` bump.
- **Why accepted:** it touches only the reconciler's internal bookkeeping
  flag, never a decision or what was removed; it runs once per database,
  in the migration's own transaction; and the migration is in the
  repository, so what it did is on record.
- **Revisit when:** a migration needs to change anything a person decided
  or a resource's desired state. That must be audited like any change.

## RP-017 — The approvals paging cursor can reveal IDs the caller can't read

- **Kind:** data exposure, low. **Status:** Open. **Found:** spec 019 T012
  (security review, confidence 6 of 10; below the reporting bar).
- **What:** `GET /v1/approvals` cuts a page, then filters it by read
  access (RP-006). `Serendip-Next-After` is the last ID of the *unfiltered*
  page. With `?limit=1` and a filter (`?requester=`, `?targetId=`,
  `?phase=`), a caller who can read none of the matches can still learn
  that they exist, their IDs, and roughly how many there are by walking
  the cursor.
- **What doesn't leak:** any approval's content (target, reason, blast
  radius). Approval IDs are random.
- **Fix when touched:** an opaque cursor (for example the page's last
  `created_at` and a sequence, not an ID), or filtering before cutting
  with a scope-aware query (RP-006's fix).

## RP-018 — OpenChoreo's API answers any pod in the cluster, unauthenticated

- **Kind:** security, high. **Status:** Closed, 2026-10-20 (fixed before
  spec 020, at the user's request). **Found:** spec 020 research, 2026-10-20 (OpenChoreo 1.3.1,
  verified from its chart and source). It has existed since spec 010.
- **What:** spec 010 installs OpenChoreo's control plane with
  `security.enabled=false`, so `openchoreo-api` accepts requests with no
  authentication and no authorization. It's a ClusterIP Service on port
  8080, and the chart ships no NetworkPolicy by default.
- **Impact:** any pod in the cluster can create, change or delete
  OpenChoreo resources (projects, components, bindings), and so run
  workloads or remove them. That includes a user's workload, or a
  compromised one. It bypasses Serendip's RBAC, approvals and audit
  entirely. Nothing outside the cluster can reach it.
- **Why it wasn't caught:** spec 010 set the flag to avoid the chart's
  placeholder OIDC issuer, and treated the API as unused. Unused isn't the
  same as unreachable.
- **Fixed:** the OpenChoreo provider's platform defaults now apply
  `serendip-deny-openchoreo-api`, a NetworkPolicy denying all ingress to
  the API's pods (`providers/application/openchoreo/defaults.go`).
  - **It's restored by the foundations loop** if deleted or edited.
  - **Verified live on kind** (kindest/node v1.34.0):
    - Before the fix, an anonymous pod listed projects (HTTP 200).
    - After `serendipd` applied the policy, the same request got no
      answer.
    - The kubelet's probes still pass, and a new Component still deployed
      Ready in about 16 s.
  - **Still to verify live:** K3s enforcement, on the first K3s run.
- **Residual risk:** the policy matches only by the pods' label
  (`app.kubernetes.io/component=api-server`). A future chart that
  relabels them would silently re-open the API. `VERSIONS.md` requires a
  re-check on every OpenChoreo upgrade. A foundations-time check that the
  selector matches a pod would be stronger, later.

## RP-019 — A local AI assistant can read its person's CLI sign-in

- **Kind:** security, medium. **Status:** Accepted, 2026-10-20.
  **Found:** spec 021 T001.
- **What:** `serendip mcp` runs as the person's own OS user and exchanges
  the person's CLI token for a delegated one, so the AI's calls carry
  spec 018's agent guardrails. But an AI assistant that can run commands
  as the same user (Claude Code can) could read the person's token file
  and call the API as the person, without the guardrails.
- **Why accepted:** any local design shares this. A separate agent secret
  on disk would be just as readable, and so is the person's browser
  session. The guardrails protect against an assistant's mistakes when
  it uses Serendip's tools, not against a hostile process running as the
  person.
- **Mitigations:**
  - the token file is owner-only (0600 or an owner-only ACL);
  - exchanged tokens last 5 minutes, and logout ends them;
  - audit records which calls were delegated.
- **Revisit:** if Serendip gains an OS-keychain store for the CLI, or when
  hosted assistants (no local file) become the main path.
