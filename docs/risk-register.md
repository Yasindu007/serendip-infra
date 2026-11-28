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
| RP-020 | The catalogue's parity test checks routes, not parameter names | Correctness (low) | Accepted | Spec 021 T002 |
| RP-021 | An AI assistant keeps access up to 5 minutes after its person signs out | Security (low) | Accepted | Spec 021 T004 |
| RP-022 | A fresh install's `serendip-mcp` bootstrap file isn't verified yet | Correctness | Open | Spec 021 T004 |
| RP-023 | The MCP protocol and its Go SDK change every month or two | Maintenance | Accepted | Spec 021 (ADR 0017) |
| RP-024 | Without a configured console origin, the resource metadata names the request's Host | Security (low) | Accepted | Spec 021 T005 review |
| RP-025 | An exchanged assistant token can be exchanged again after its person signs out | Security (medium) | Open | Spec 021 T012 security review |
| RP-026 | Tokens other ThunderID clients receive may be exchangeable into Serendip access | Security (low) | Open | Spec 021 T012 security review |
| RP-027 | Tokens aren't bound to the Serendip server they were issued for | Security (medium) | Mitigated | Spec 021 T012 security review |
| RP-028 | Migration 0008 re-parents Applications and changes what Environment-scope assignments cover, without audit rows | Audit | Decided | Spec 020 T003 |
| RP-029 | Release listing scans every release, and a component-type change re-releases every Component | Performance | Accepted | Spec 020 T005 review |
| RP-030 | Deployment writes scan every Deployment; platform failures wait for a resync | Performance, resilience | Accepted | Spec 020 T006 review |
| RP-031 | Log reads aren't audited; following logs holds connections open | Security, performance | Accepted | Spec 020 T007 |
| RP-032 | A Deployment's unchanged configuration isn't checked again | Correctness | Accepted | Spec 020 T008 review |
| RP-033 | Two simultaneous package installs could both pass the one-per-capability check | Correctness | Closed (spec 022 T003) | Spec 020 T010 |
| RP-034 | The console couldn't state a package's measured cost (stated since spec 022); follows reconnect; the Deploy tab lists every Deployment | Honesty, performance | Accepted (cost part closed, spec 022) | Spec 020 T012 |
| RP-035 | The first deploy into a new Environment takes about 70 s to serve | Performance | Accepted | Spec 020 T014 |
| RP-036 | Right after a restart, Applications, Components and Deployments read not-ready for a few seconds | Honesty, noise | Accepted | Spec 020 T015 |
| RP-037 | Only the builds package declares its resource requirements in code (spec 022); older providers don't | Honesty, documentation | Mitigated (spec 022) | Spec 020 T016 review |
| RP-038 | Build steps run privileged, without user namespaces | Security | Open | Spec 022 T001 |
| RP-039 | The workflow plane's Argo CRDs outlive its removal (the per-run download ended in T013) | Footprint, network | Decided | Spec 022 T001 |
| RP-040 | A repository's own contents can't be checked before its build | Security | Open | Spec 022 T002 |
| RP-041 | The build registry listens on the node's network without authentication, and its tags can be overwritten | Security (medium) | Open | Spec 022 T005, T015 |
| RP-042 | A followed build log polls the cluster while it waits for a step | Performance | Accepted | Spec 022 |
| RP-043 | Auto-build's head read runs on the reconcile loop, so slow repositories can stall it | Performance, availability | Accepted | Spec 022 T007, T015 |
| RP-044 | A credential's use is checked only when the Secret or the repository changes, which holds only while the build step can't see the credential | Security | Accepted | Spec 022 T008 review |
| RP-045 | A package's Helm release left locked is recovered by undoing the unfinished operation | Operability | Accepted | Spec 022 T013 |
| RP-046 | A package's cost hides outside its own pods: Argo's full CRDs held kube-apiserver ~550 MiB higher | Footprint | Mitigated | Spec 022 T013 |
| RP-047 | A followed build log's first lines can be a few short of `tailLines` | Correctness (low) | Accepted | Spec 022 T015 |
| RP-048 | A source Component keeps running an earlier source's image until its current source builds | Correctness | Decided | Spec 022 T015 |
| RP-049 | A build's own steps reach the whole cluster network | Security (medium) | Open | Spec 022 T015 |
| RP-050 | A build's credential copy is a plain Secret on the node while its run lasts | Security (low) | Accepted | Spec 022 T015 |
| RP-051 | Auto-build reads any HTTPS address a Component names, internal ones included | Security (low) | Accepted | Spec 022 T015 |
| RP-052 | Build steps have no CPU or memory limits | Footprint, availability | Open | Spec 022 T015 |
| RP-053 | Logs written while the log store is unreachable are dropped | Correctness, operability | Open | Spec 023 T001 |
| RP-054 | The observability adapters answer unauthenticated queries; only NetworkPolicies close them | Security (medium) | Open | Spec 023 T001 |
| RP-055 | OpenObserve's log search slows as stored logs grow (0.24 s to 0.53 s mean, 3.1 s worst, in 2.5 h) | Performance | Accepted | Spec 023 T003 |
| RP-056 | Serendip's plain Prometheus must keep matching what the metrics module's adapter queries | Operability, correctness | Accepted | Spec 023 T003 |
| RP-057 | ThunderID's Service-proxy calls drop a kubeconfig's bearer token | Correctness (latent) | Watch | Spec 023 T005 |
| RP-058 | Stored-log pages rely on the store returning tied timestamps in a stable order; a tie group over 1,000 lines is cut | Correctness | Accepted | Spec 023 T006 |
| RP-059 | Stored logs are scoped by OpenChoreo object UIDs, so a re-created platform object hides its predecessor's logs | Correctness | Accepted | Spec 023 T006 |
| RP-060 | Fluent Bit's read-position file stays on the node after the package is removed | Operability | Accepted | Spec 023 T006 |
| RP-061 | The package's reported disk use lags, and leaves out the logs store's newest data and metadata | Honesty of a reported number | Accepted | Spec 023 T006 |
| RP-062 | OpenObserve's SSRF guard is off, so its in-cluster alert webhook can be created | Security | Accepted | Spec 023 T012 |
| RP-063 | The observability package's disk growth is a 2-hour slope under a synthetic load, extrapolated to a day | Honesty of a reported number | Accepted | Spec 023 T012 |
| RP-064 | Fluent Bit's monitoring port (:2020, host network) answered any pod on the node: counters, no log content | Security (information) | Fixed (T014) | Spec 023 T013 |
| RP-065 | Serendip carries a copy of the logs chart's Fluent Bit config, and pod metadata now comes from the API server (not re-measured) | Maintenance; performance | Accepted | Spec 023 T014 |
| RP-066 | The observability plane chart's `observabilityalertrules` CRD outlives the package's removal | Footprint | Decided | Spec 023 T001 |
| RP-067 | Stored logs keep what pods print, secrets included, for 3 days | Data exposure | Accepted | Spec 023 plan |
| RP-068 | A noisy Component can fill the logs volume before retention acts | Availability, capacity | Accepted | Spec 023 plan |
| RP-069 | The Telegram bot token is the whole credential, and sits in the API URL's path | Security | Accepted | Spec 024 plan |
| RP-070 | Alert content leaves the machine for third parties (Telegram, the mail relay, webhook receivers) | Data exposure | Accepted | Spec 024 plan |
| RP-071 | Resolved Alerts are removed after 30 days by system retention | Audit, data retention | Accepted | Spec 024 T005 |
| RP-072 | Rules are polled: a condition shorter than the interval can be missed, and a firing is seen up to one interval late | Correctness | Accepted | Spec 024 plan |
| RP-073 | A `logMatch` threshold is capped at 999 | Correctness (limit) | Accepted | Spec 024 plan |
| RP-074 | The heartbeat proves Serendip can alert, not that a message reached a person; it can't send headers, so Gatus needs a relay | Operability | Accepted | Spec 024 plan; T013 |
| RP-075 | Alerting authorization: checks made before the write, and wide custom roles | Security | Accepted | Spec 024 T003 |
| RP-076 | The sender's "cluster's own ranges" are learned from nodes and ServiceCIDRs, refreshed every 5 minutes | Security (SSRF) | Accepted (Cilium's pool ranges merged, spec 025 T008) | Spec 024 T006 |
| RP-077 | Alert condition trade-offs: early restarts missed, the built-in rule's Alerts removed with the package, one unmeasured series | Correctness; footprint | Accepted | Spec 024 T006 |
| RP-078 | Alerting API trade-offs: heartbeat status lag, save-time address check, in-memory Alert filtering | Audit volume; correctness; footprint | Accepted | Spec 024 T007 |
| RP-079 | The API drops unknown fields in a resource document without saying so | Correctness | Mitigated (metadata refused, T012) | Spec 024 T010 |
| RP-080 | 200 alert rules load the observability backends (+149 to +234 mcores on the node), and their status writes add about 37,000 audit rows a day | Footprint; audit volume | Accepted (load); Mitigated (writes: hourly refresh, T012) | Spec 024 T011 |
| RP-081 | The L7 proxy that gives request metrics retries 5xx and rewrites paths by default | Correctness (application behaviour) | Mitigated (both off; on drift the L7 policies are removed, T004) | Spec 025 T001 |
| RP-082 | Request visibility copies each Component's NetworkPolicy; a shape it can't copy faithfully leaves that Component without request metrics | Correctness (coverage); security (isolation) | Accepted | Spec 025 T004 |
| RP-083 | A Cilium bootstrap fetches the chart and images from the internet at first boot, and the node is NotReady until they arrive | Availability (install); footprint (first boot) | Accepted | Spec 025 T005 |
| RP-084 | Connection records: de-duplicated within a page only, proxied ports recorded at the source only, and Fluent Bit's config depends on the logs chart's layout | Correctness (counts); upgrade | Accepted | Spec 025 T006 |
| RP-085 | Request metrics from Hubble's histogram: coarse percentiles, extrapolated counts, a DNS lookup that fails on clusters without Cilium | Correctness (precision); log noise | Accepted | Spec 025 T006 |
| RP-086 | Trace identity leans on the store's key flattening; removing traces waits for the store and rolls Components | Security (span identity); availability; performance | Accepted | Spec 025 T007 |
| RP-087 | Flows and traces read the caller's readable Deployments in the Environment, capped at 20 | Correctness (names past the cap); performance (lookups per read) | Accepted | Spec 025 T009 |
| RP-088 | Traces share the logs store: about 730 MiB a day at 10 traced requests a second, kept 3 days, in the same 5 GiB as logs | Footprint (disk); availability (logs) | Accepted | Spec 025 T013 |
| RP-089 | Cilium costs about +450 to +500 MiB and +80 to +130 mcores on the edge-1 node (simulated); edge-1 keeps it as the default | Footprint | Accepted | Spec 025 T013 |
| RP-090 | Request visibility's audit rows wait in memory until the store takes them, and a policy someone edited is deleted, then re-applied | Audit (durability); correctness (metrics gap) | Accepted | Spec 025 T014 |
| RP-091 | Cilium's node metrics ports are closed to pods by a Serendip policy keyed on two namespace names; anything else reaching the node's other ports isn't covered | Security (traffic visibility) | Mitigated | Spec 025 T014 |
| RP-092 | The trace collector accepts spans from any pod, with only a memory limit: one Component can fill the shared logs store | Availability (logs, traces); footprint (disk) | Accepted | Spec 025 T014 |
| RP-093 | Hubble's request metrics carry source and destination pods, so their series grow with pod pairs; not counted at the bench | Footprint (Prometheus memory, disk) | Watch | Spec 025 plan; T015 |
| RP-094 | OpenBao stayed sealed after its pod restarts, until `serendipd` restarted; it's now unsealed again with the stored key | Availability (secrets, secret variables) | Closed | Spec 026 T009; fixed T012 |
| RP-095 | Deleting a Component leaves its Deployments behind, waiting for it | Behaviour (clean-up) | Open | Spec 026 T009 |
| RP-096 | Secret variables rest in the cluster as plain Secrets, encrypted at rest only on agent K3s installs, with the key on the same disk | Security | Accepted | Spec 026 plan; T005 |
| RP-097 | Secret references aren't part of a release, so a rollback keeps today's references | Behaviour | Decided | Spec 026 plan decision 5 |
| RP-098 | A rotation rolls every Deployment using the Secret, and rotating any one of a Deployment's Secrets rolls it | Availability (single replicas) | Accepted | Spec 026 plan |
| RP-099 | Every Deployment rolls once on the upgrade to spec 026; a downgrade drops secret variables from running pods | Availability (upgrade, downgrade) | Accepted | Spec 026 T001, T009, T010 |
| RP-100 | Values read to deliver secret variables aren't audited | Audit | Accepted | Spec 026 spec (out of scope) |
| RP-101 | Delivering secret variables: two cluster GETs per Deployment per pass, an apply 404 read as pending, and best-effort clearing of value buffers | Performance; correctness; security (memory) | Accepted | Spec 026 T003, T004 |
| RP-102 | A secret variable may share a name with a variable the platform injects (spec 025's trace variables); it isn't refused | Correctness | Accepted | Spec 026 T002 |
| RP-103 | The encryption-at-rest status is read at `/`, so a Project-only user's console shows no banner | Usability | Decided | Spec 026 T006 (plan decision 9) |

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

## RP-020 — The catalogue's parity test checks routes, not parameters

- **Kind:** correctness, low. **Status:** Accepted, 2026-10-21.
  **Found:** spec 021 T002 review.
- **What:** `api/catalog` describes each operation's query parameters and
  body by hand. The parity test proves every route has a CLI command and
  an MCP tool (or a reason not to), but not that their parameter names
  match what the handler reads. Handlers ignore unknown query parameters,
  so a misspelt filter would silently filter nothing.
- **Found that way:** the review caught two missing filters
  (`principalType`/`principalId` on role assignments, `label.*` on lists)
  and a required `type` on directory search. All three are fixed.
- **Mitigations:** spec 021 T009's end-to-end tests drive the CLI and MCP
  against a real `serendipd`, which exercises each operation's request.
  A new route's catalogue entry is reviewed with its handler.
- **Revisit:** if a parameter mismatch reaches a user, or when the API
  gains an OpenAPI description the catalogue could be checked against.

## RP-021 — An AI assistant outlives its person's sign-out by minutes

- **Kind:** security, low. **Status:** Accepted, 2026-10-21.
  **Found:** spec 021 T004 contract review.
- **What:** a person's assistant acts with a token exchanged for the
  `serendip-mcp` client. Signing out revokes the person's refresh token,
  and ThunderID then refuses further exchanges (spec 021 T001). But an
  exchanged token already issued is a JWT that `serendipd` verifies
  locally, so it stays valid until it expires.
- **Bound:** the client issues 300-second tokens
  (`thunderid.DelegatedTokenLifetime`), and `serendipd` refuses any
  delegated token living over 1 hour (`oidc.MaxDelegatedLifetime`),
  whatever the client is configured to issue. The operator tool never
  edits an existing `serendip-mcp` client, so an operator who raises its
  lifetime gets up to that hour.
- **Why accepted:** the same holds for every JWT access token Serendip
  verifies, people's included (1 hour). The assistant's window is
  shorter, and it can never do more than the person, under agent
  guardrails.
- **Revisit:** if Serendip gains token introspection or a revocation list,
  or an incident needs an assistant cut off at once. Until then, removing
  the person's role assignments cuts it off immediately.

## RP-022 — A fresh install's `serendip-mcp` bootstrap file isn't verified

- **Kind:** correctness. **Status:** Open, 2026-10-21. **Found:** spec 021
  T004.
- **What:** fresh installs declare `serendip-mcp` in a second ThunderID
  bootstrap file, `71-serendip-mcp-client.yaml`. ThunderID imports
  bootstrap files once, in name order (`VERSIONS.md`). The document's
  shape was accepted live through the management API, but a fresh install
  importing it hasn't been run.
- **Impact if wrong:** a fresh install has no `serendip-mcp` client.
  `serendip mcp` can't exchange tokens, and says so; nothing else breaks.
  The operator tool `cmd/serendip-identity-redirects` creates it.
- **Close:** on the next fresh identity install (a new kind cluster, or the
  real T470 run), check that ThunderID lists the client.

## RP-023 — The MCP protocol and its Go SDK change often

- **Kind:** maintenance. **Status:** Accepted, 2026-10-22. **Found:**
  spec 021 (ADR 0017).
- **What:** MCP's 2026-07-28 revision removed the handshake and sessions,
  and clients still speak 2025 versions. The official Go SDK
  (`modelcontextprotocol/go-sdk` v1.8.0) ships a minor version every one
  to two months, and brings 9 modules.
- **Why accepted:** the user chose less to maintain (2026-10-20). The SDK
  tracks protocol revisions; a hand-written layer would make each one
  ours.
- **Mitigations:**
  - **Contained:** the SDK is confined to `mcp/`, and
    `check-imports.sh` enforces it.
  - **Pinned:** recorded-exchange tests cover both protocol generations,
    and Claude Code 2.1.304's actual requests.
- **Revisit:** if an SDK upgrade breaks a client, the SDK stalls, or its
  dependencies grow (ADR 0017's "Revisit").

## RP-024 — Resource metadata can reflect the request's Host

- **Kind:** security, low. **Status:** Accepted, 2026-10-22. **Found:**
  spec 021 T005 review.
- **What:** `/.well-known/oauth-protected-resource` names the resource,
  and `/mcp`'s 401 names the document's URL. With a console origin
  configured (the normal `serendipd` setup), both use it. Without one,
  they're built from the request's `Host` header.
- **Impact:** a client sending a forged `Host` gets its own forged value
  back. The answer is `Cache-Control: no-store`, so a shared cache can't
  serve it to anyone else. The issuer and client IDs never come from the
  request.
- **Revisit:** if `serendipd` runs behind a cache that ignores
  `no-store`, or without a console origin in production.

## RP-025 — An exchanged token can be exchanged again after sign-out

- **Kind:** security, medium. **Status:** Open, 2026-10-22. **Found:**
  spec 021 T012 security review; verified live on ThunderID 1.0.1.
- **What:** after a person signs out (their refresh token is revoked),
  ThunderID refuses to exchange their access token. But it still accepts
  an **already-exchanged** `serendip-mcp` token as the subject of a new
  exchange. Anyone holding a live delegated token can keep minting fresh
  5-minute tokens after the sign-out, so RP-021's "within 5 minutes" holds
  only for a holder who doesn't do that.
- **Bounds:**
  - **A live delegated token is needed:** one lives only in `serendip
    mcp`'s memory and on its requests to `serendipd`. `serendip mcp`
    never chains: it always exchanges the stored sign-in.
  - **It must be renewed within 5 minutes,** every time, or it dies.
  - **It's always the person, with agent guardrails:** no access
    management, every delete through an approval.
  - **Removing the person's role assignments** stops it immediately.
- **Fix to pursue:** ThunderID's `oauth.token_exchange.token_family:
  inherit` puts exchanged tokens in the subject token's family, so a
  sign-out revokes them. The ThunderID 1.0.1 chart's `deployment.yaml`
  template doesn't expose that setting. Getting it means an upstream chart
  change, or overriding the whole configuration file, which is fragile.
  Until then, Serendip can't tell a chained token from a first one.
- **Revisit:** when the chart exposes the setting, or when Serendip gains
  token introspection.

## RP-026 — Other ThunderID clients' tokens may be exchangeable

- **Kind:** security, low. **Status:** Open, 2026-10-22. **Found:**
  spec 021 T012 security review.
- **What:** `serendip-mcp` is a public client with the token-exchange
  grant, and ThunderID decides whose tokens it accepts as subjects. A
  token ThunderID's own Console received was refused (verified live: it
  has a resource audience, and an exchange with that resource would carry
  that audience, which `serendipd` refuses). A token another
  relying-party app in the same ThunderID received for itself, carrying
  its own client ID as audience, may be exchangeable. That app, or anyone
  who compromises it, could then act in Serendip as that person, under
  agent guardrails.
- **Today:** Serendip's ThunderID has only the `serendip`, `serendip-mcp`
  and Console clients. Its dynamic client registration is enabled in the
  shipped configuration (`oauth.dcr.enabled: true, insecure: false`);
  whether registering needs authentication is to be confirmed.
- **Fix to pursue:** confirm the cross-client exchange with a second
  test app. If it works, restrict which subject tokens `serendip-mcp`
  accepts, if ThunderID allows it. Otherwise keep Serendip's identity
  provider free of other relying parties, and confirm DCR requires
  authentication.

## RP-027 — Tokens aren't bound to the server they were issued for

- **Kind:** security, medium. **Status:** Mitigated, 2026-10-22.
  **Found:** spec 021 T012 security review.
- **What:** the CLI learns which identity provider to sign in with from
  the server it's pointed at. A hostile server could name a real
  provider. The person signs in on the genuine page, and the CLI then
  sends the real tokens to the hostile server, which could replay them to
  the real `serendipd`: the audience is the shared client `serendip`, not
  the server.
- **Mitigation (spec 021 T012):**
  - **The first sign-in to a server is confirmed:** it shows the
    provider, asks, remembers the answer per server, and asks again if
    the provider changes. Without anyone to answer, it refuses unless
    `--yes` is given.
  - **The server's RFC 9728 `resource` must be the server itself.**
  - **A stored sign-in is only ever sent to the server it was made for.**
- **Full fix:** bind tokens to the server with RFC 8707 (`resource=`).
  ThunderID needs a registered resource server for that (spec 021 T001
  finding 1); then `serendipd` checks the audience is its own.

## RP-028 — Migration 0008 changes ownership without audit rows

- **Kind:** audit. **Status:** Decided, 2026-10-22. **Found:** spec 020
  T003 contract review.
- **What:** migration 0008 (Azure-style environments, spec 020 plan
  decision 2) makes changes no person or reconciler made:
  - **It moves each Application** from its Environment up to the
    Environment's Project (owner references, graph edges, a version
    bump).
  - **It creates a Deployment per Component** in that Environment.
  - **Role assignments at an Environment** then cover deployments into
    it, not the Applications that were under it. That changes what a
    person decided when they made the assignment.
- **No audit row is written,** as with 0006 (RP-016). This goes further
  than 0006, and RP-016 said to revisit "when a migration needs to change
  anything a person decided".
- **Why decided this way:**
  - **The change is fixed SQL in the repository,** applied once per
    database, in the migration's transaction.
  - **Every affected role assignment, and every Application it couldn't
    move,** is written to the permanent `migration_notes` table. It's
    logged when the migration is applied (`sqlite.logMigrationNotes`), so
    the record survives the log.
  - **Writing audit rows from SQL** would mean building each resource's
    before and after documents in SQL, which is easy to get subtly wrong.
- **Revisit:** if an operator needs migration changes in the audit trail
  itself, migrations should gain a Go step that writes proper audit
  rows.

## RP-029 — Release listing scans every release; a type change re-releases everything

- **Kind:** performance. **Status:** Accepted, 2026-10-23. **Found:**
  spec 020 T005 reviews.
- **What:**
  - **Listing a Component's releases reads every release in the
    namespace,** and filters by owner in `serendipd`. Releases the
    platform made before spec 020 carry no labels, so a label selector
    would miss them, and they're valid rollback targets. Each release
    embeds the whole component type spec. The legacy-binding cleanup also
    lists every binding on each Application reconcile.
  - **A release is named after its whole content,** the component type's
    spec included. So an OpenChoreo upgrade that changes the component
    type, or a Serendip change to the release format, gives every
    Component a new release on its next reconcile. That's intended (the
    release is a different thing), but it's a burst of writes.
- **Why accepted:** edge-1 runs tens of Components with a few releases
  each, so a list is small. Release creation is idempotent and rate-free.
- **Revisit:** when a list costs measurable time on the T470, or
  releases exceed a few hundred. Options are labelling legacy releases
  once, pruning old releases, or a release index.

## RP-030 — Deployment writes scan every Deployment; platform failures wait for a resync

- **Kind:** performance and resilience. **Status:** Accepted, 2026-10-23.
  **Found:** spec 020 T006 review.
- **What:**
  - **Each Deployment create, update or promote lists every Deployment.**
    The pipeline rule needs the release this Component runs in every
    other Environment. Writes are serialised by one mutex in the
    handler, which also makes "one Deployment per Component per
    Environment" hold under concurrent requests on one control plane.
  - **A failed `Deploy` that isn't "provider not ready"** becomes an
    `EnsureFailed` condition and waits for the next resync instead of
    backing off. This matches the Application and Component reconcilers.
  - **A pipeline naming a since-deleted Environment** keeps failing in
    the Project reconciler until someone edits it. The API checks the
    pipeline only when it's written.
- **Why accepted:** edge-1 has tens of Deployments, so the list is small
  and the mutex is never contended in practice. Consistent failure
  handling across the platform reconcilers is worth more than one being
  different.
  - **Component writes and deletes** (spec 020 T009) are serialised
    the same way, by a process-local lock across their checks. That
    keeps a dependency from being added to a Component being deleted,
    or to a port being removed.
- **Revisit:**
  - **Scan and mutex:** when Deployments reach the hundreds, or when
    more than one `serendipd` writes. Options are an index by Component,
    or a unique constraint in the store.
  - **Failures:** together with the other platform reconcilers, if
    resync latency turns out to hurt.
  - **Deleted Environments:** remove them from pipelines when they are
    deleted.

## RP-031 — Log reads aren't audited; following logs holds connections open

- **Kind:** security and performance. **Status:** Accepted, 2026-10-23.
  **Found:** spec 020 T007.
- **What:**
  - **Reading a Deployment's logs leaves no audit row.** Logs can hold
    sensitive data, which is why they have their own permission
    (`serendip/components/logs/read`, outside Reader). Who read them
    isn't recorded, the same as Secret value reads
    (`docs/architecture/secrets.md`, "reads don't").
  - **Each followed stream holds open:**
    - a client connection;
    - one platform stream per instance (up to 10);
    - a few goroutines.

    That's capped at 3 per caller and 20 per server, for at most 10
    minutes, ending after 5 idle.
- **Why accepted:**
  - The constitution audits mutations. Auditing reads needs a
    `core/audit` extension, which is already deferred for Secrets.
  - The caps bound the cost on a T470: at most 200 platform streams.
- **Also accepted:**
  - **A stalled client** keeps its stream and its place until the
    stream's write deadline, 10.5 minutes at most.
  - **A stream that has started** isn't stopped when the caller's role
    is removed or their session ends. It runs out within 10 minutes.
  - **More than 10 instances:** only the newest 10 are read.
- **Revisit:**
  - **Audit:** add read auditing for logs and Secret values together,
    when `core/audit` gains reads.
  - **Caps:** if the console's reconnects make 20 streams too few for
    a team.

## RP-032 — A Deployment's unchanged configuration isn't checked again

- **Kind:** correctness. **Status:** Accepted, 2026-10-23. **Found:**
  spec 020 T008 review.
- **What:** configuration overrides are checked against the platform's
  schema only when they change.
  - A Deployment stored before T008, or under an older component type
    whose schema has since tightened, keeps configuration the current
    schema would refuse. Promoting it, or changing its release, doesn't
    re-check it.
  - Numbers are checked as float64, so integer bounds beyond 2^53 are
    approximate.
- **Why accepted:** re-checking on every write would make promotion and
  rollback depend on the platform answering. The platform still refuses
  invalid configuration when the reconciler deploys it, and the
  Deployment's Ready condition shows that.
- **Revisit:** if a component type upgrade changes its schema
  incompatibly, re-validate stored Deployments when the type changes.

## RP-033 — Two simultaneous package installs could both pass the one-per-capability check

- **Kind:** correctness. **Status:** Fixed, 2026-10-25 (spec 022 T003).
  Accepted 2026-10-23. **Found:** spec 020 T010.
- **What:** the API checks a capability has no package before storing a
  new ProviderInstance. Two requests at the same moment could both pass,
  leaving two packages for one capability. Their installs would compete.
- **Why accepted:**
  - Installing a package is an Owner's rare, deliberate act, made from
    one console screen.
  - A second instance is visible in the API and can be removed through
    an Approval.
- **Revisit:** with the first real package (spec 022). A store-level
  unique constraint on capability would close it.
- **Fixed** by spec 022 T003:
  - **Migration 0009** adds `resources.unique_key`, with
    `UNIQUE (kind, unique_key)`.
  - **The ProviderInstance store keys each new row** with
    `capabilityId/environmentId` (`resource.ProviderInstanceKey`).
  - **A racing install gets 409.** In a test, 20 racing installs left one
    row; without the key, 2–3 got through.
  - **Existing duplicates:** only the oldest is keyed, and each other one
    gets a migration note. The reconciler still marks it `Duplicate`.
  - **Update keeps a keyed row's key in step with its spec.** A change
    onto a taken key gets `ErrDuplicate`. A store without a key function
    leaves keys alone.
  - **What's left:** an unkeyed duplicate stays unkeyed until it's
    removed. So, like before, it could still be edited to collide.

## RP-034 — The console couldn't state a package's measured cost (stated since spec 022); follows reconnect; the Deploy tab lists every Deployment

- **Kind:** honesty and performance. **Status:** Accepted, 2026-10-23;
  the cost part closed 2026-11-09 (spec 022). **Found:** spec 020 T012.
- **What:**
  - **Package cost.** Spec 020 asks the console to say what a package
    costs before it's installed. `GET /v1/packages` carries no measured
    memory or CPU, and neither package has an installer yet, so the
    Packages page reads "Not measured yet: it can't be installed in this
    version". The observability package's description says it's heavy
    on a 16 GB machine. An available package without a stated cost reads
    "Not stated by this server", never a guessed number.
  - **Log following reconnects.** The server ends a followed stream
    after 10 minutes (RP-031). The console reconnects on its own, asking
    only for lines since the last one, so a follow lasts until the
    person stops it or leaves the page. It keeps running while the tab
    is hidden. Each reconnect is authorised again, so a removed role
    takes effect within 10 minutes.
  - **The Deploy and Runtime tabs** read `GET /v1/deployments` every 5
    seconds and keep the Component's own. The API has no filter by
    Component.
- **Why accepted:**
  - No package can be installed in this build, so no one is asked to
    decide without a number.
  - A follow is one of the person's 3 streams, and they started it.
  - Deployments number a few per Component on the target machines, and
    the list is filtered by what the caller may read.
- **Revisit:**
  - **Cost:** before spec 022 or 023 ships an installer, the catalogue
    must carry the measured idle memory and CPU, and the Packages page
    must show them.
  - **Follow:** stop following when the tab is hidden, if streams turn
    out to be held open for hours.
  - **Listing:** add a `component` filter to `GET /v1/deployments` if
    the list grows large.
- **Update, 2026-11-09 (spec 022): the cost part is closed.**
  - **The catalogue carries a package's measured cost**
    (`core/provider.Requirements`, spec 022 T002), and
    `Catalogue.Validate` refuses an installable package without it.
  - **`GET /v1/packages`** returns `requirements` (`null` when not
    measured), `installable` and `notInstallable` (T008).
  - **The Packages page** shows idle and peak cost, where it was
    measured and the fit note, before the install button. An unmeasured
    package reads "Not measured" with no number, and can't be installed
    (T011).
  - **The builds package's numbers** come from T013's run
    (`profiles/edge-1/bench/results/2026-11-06-spec022-simulated-t470/`).
  - **Still accepted, unchanged:** the follow reconnect, and the Deploy
    tab's full list. Spec 022's Builds tab follows the same way.

## RP-035 — The first deploy into a new Environment takes about 70 s to serve

- **Kind:** performance. **Status:** Accepted, 2026-10-23. **Found:**
  spec 020 T014 (`profiles/edge-1/bench/results/2026-10-23-spec020-simulated-t470/`).
- **What:** on the simulated T470, a Component created and deployed into
  a new Environment served after 70 s. Before spec 020, an auto-deployed
  Component served after 16 to 24 s on the same host.
  - The Component is released in under a second. Its Deployment then
    reads Progressing for about 67 s.
  - The platform does more on a first deploy into an Environment: it
    makes the Environment's data-plane namespace and the Application's
    binding to it.
  - The engine re-checks a converging Deployment fast for one resync
    interval (30 s), then only on the 30 s resync. Up to about 30 s of
    the 70 can be that wait.
- **Since measured on a real cluster** (spec 020 T015, `serendip-t018`):
  first deploys into the new staging and production Environments served
  in 18 s and 24 s. The 70 s is the simulated T470's.
- **Why accepted:** memory and CPU don't regress. A first deploy into a
  new Environment is rare. Later deploys and promotions into an existing
  Environment weren't measured, and don't create a namespace.
- **Revisit:**
  - **Measure** a second Deployment into the same Environment, and a
    promotion, to split the platform's time from the engine's wait.
  - **If the wait dominates,** let a converging Deployment keep its fast
    re-checks longer (a `core/reconcile` change, so a reviewed one).

## RP-036 — Right after a restart, Applications, Components and Deployments read not-ready for a few seconds

- **Kind:** honesty and noise. **Status:** Accepted, 2026-10-23.
  **Found:** spec 020 T015 (finding F2).
- **What:** for about 15 s after `serendipd` starts, while the
  application platform capability re-checks the installed stack:
  - each Application reads `ProviderNotReady`;
  - its Components read `WaitingForApplication`;
  - their Deployments read `WaitingForComponent`.

  Nothing changed on the platform, and the workloads keep running. On
  `serendip-t018` it lasted 14 s.
- **Why accepted:** while the platform can't be read, "can't confirm
  right now" is the honest state, and it clears without anyone acting.
  T016 made the Deployment's message carry the Component's own reason
  ("it isn't ready (WaitingForApplication)") instead of claiming it has
  no release.
- **Revisit:** if people take the flicker for an outage, keep the last
  observed state with a "not re-checked since the restart" note instead
  of flipping to not-ready.

## RP-037 — Only the builds package declares its resource requirements in code (spec 022); older providers don't

- **Kind:** honesty and documentation. **Status:** Mitigated, 2026-11-09
  (spec 022). Accepted 2026-10-23. **Found:** spec 020 T016 contract
  review.
- **What:** `providers/CLAUDE.md` and `provider-model.md` say a provider
  declares its capabilities, resource requirements (RAM and CPU on
  reference hardware), health check and upgrade behaviour.
  - **No provider declares RAM and CPU in code.** That includes the
    OpenChoreo provider spec 020 extended. Their measured footprints live
    in the benchmark results
    (`profiles/edge-1/bench/results/`, `002-edge1-footprint.md`) instead.
  - **True of every provider since before spec 020.** It isn't new with
    this spec.
- **Why accepted:** the measured numbers exist and are what's honest to
  quote (constitution 6). A declared number nothing measures would be
  worse.
- **Revisit:** when the provider registry gains a requirements field.
  Spec 022's packages need it anyway, to state a package's cost before
  installing it (RP-034). Fill it from the benchmark results then.
- **Update, 2026-11-09 (spec 022): mitigated.**
  - **The registry gained the field:** `core/provider.Requirements`
    (idle and peak memory and CPU, where and when it was measured, and a
    fit note), carried by each catalogue `Package` (T002).
  - **The builds package declares it,** from T013's measurement, in
    `providers/build/openchoreo/requirements.go`: idle 453 MiB and 19
    mcores, peak 1129 MiB and 1787 mcores, stated at the node level
    (RP-046).
  - **Enforced:** a package without `Requirements` can't be installed.
  - **What's left:** the always-on providers (K3s, OpenChoreo's
    platform, OpenBao, Thunder, the gateway, storage) still don't
    declare theirs; that was out of spec 022's scope. Their numbers stay
    in the benchmark results.
- **Revisit:** when a spec next changes one of those providers, or
  before the observability package (spec 023) gets an installer, which
  must declare its own.

## RP-038 — Build steps run privileged, without user namespaces

- **Kind:** security. **Status:** Open, 2026-10-24. **Found:** spec 022
  T001 spike.
- **What:** `podman` needs a privileged container. Upstream's templates
  pair that with `hostUsers: false`, so root in the build is unprivileged
  on the host. On kind that fails for every pod: kind's own OCI hook
  (`/kind/bin/mount-product-files.sh`) can't run inside a user namespace.
  So Serendip's build step runs `privileged` without it.
  - **What could happen:** a malicious Dockerfile or build dependency could
    escape to the node as root.
- **What holds it today:**
  - only Serendip's own `serendip-dockerfile` template runs; no
    person-supplied workflows;
  - builds run in their own namespace (`workflows-serendip-builds`);
  - triggering a build needs `serendip/builds/write`.
- **Revisit:** on the first K3s run (spec 022 T013). If user namespaces
  work there, set `hostUsers: false` wherever it works. Detail:
  `specs/022-builds/plan.md`, "Spike findings" item 3.

## RP-039 — The workflow plane's Argo CRDs outlive its removal, and every Helm run downloads them

- **Kind:** footprint, network. **Status:** Decided, 2026-10-24.
  **Found:** spec 022 T001 spike.
- **What:** the workflow-plane chart's Argo subchart installs its eight
  `argoproj.io` CRDs through a pre-install/pre-upgrade hook Job
  (`crds.full=true`), not as Helm-owned objects.
  - **They survive `helm uninstall`.** Removing the builds package
    leaves the definitions behind. Nothing runs from them.
  - **Every install and upgrade runs the Job,** which downloads the full
    CRDs from GitHub. That's part of the 19–24 s a no-change `upgrade
    --install` costs, and makes each such run depend on github.com.
- **Why decided:** deleting `argoproj.io` CRDs could break a person's own
  Argo installation. Leaving definitions matches the Gateway API stance.
  The resync fingerprint (spec 022 decision 8) means Helm, and so the
  download, only runs when something changed.
- **Revisit:** if the chart starts owning its CRDs, or if offline installs
  become a goal (`crds.full=false` ships minimal CRDs inside the chart).
- **Update, 2026-11-09 (T013, RP-046):** the installer now sets
  `argo-workflows.crds.full=false` and `crds.keep=true`.
  - **The CRDs are the chart's minimal set,** owned by Helm. No Job runs
    and nothing downloads from GitHub, so a full Helm run fell from 23 s
    to 5 s.
  - **They still outlive removal** (`helm.sh/resource-policy: keep`), so
    the decision above stands, and a reinstall adopts them.
  - **New:** full CRDs left by an earlier install, or by a person's own
    Argo, carry no Helm ownership, so the install refuses them
    ("exists and cannot be imported"). Before, the Job would have
    overwritten them. Delete them by hand when no Workflows are left
    (`providers/build/openchoreo/VERSIONS.md`).

## RP-040 — A repository's own contents can't be checked before its build

- **Kind:** security. **Status:** Open, 2026-10-24. **Found:** spec 022
  T002 review.
- **What:** `ValidateComponentSource` keeps a Component's `repository`,
  `branch`, `path` and `dockerfile` shell-inert. `path` and `dockerfile`
  can't start with `-` or `/`, and have no `.` or `..` segment. But a
  symlink inside the cloned repository can still point outside the build
  context, and nothing can see that before the clone.
  - **What could happen:** a build reads a file outside its context, from
    the workspace or the privileged build container (RP-038).
- **What holds it today:**
  - `podman build` resolves `COPY` and `ADD` sources within the context;
  - the workspace volume holds only that run's checkout;
  - triggering a build needs `serendip/builds/write`.
- **Spec 022 T005 (2026-11-01):** the build step now refuses a `path`
  whose real directory, or a `dockerfile` whose real file, is outside the
  checkout (`pwd -P` and `realpath`, checked in the `podman-runner:v1.2`
  image against symlinks to `/` and `/etc/passwd`). The scripts quote
  every value; what keeps a value from being read as an option is the
  validation (an HTTPS or SSH URL, a full commit ID, and no leading `-`).
  Symlinks *inside* the context are still `podman`'s to resolve.
- **Revisit:** if build methods other than a Dockerfile arrive.

## RP-041 — The build registry listens on the node's network without authentication

- **Kind:** security. **Status:** Open, 2026-11-01. **Found:** spec 022
  T005.
- **What:** the builds package's `registry:2` runs with `hostNetwork` on
  port 5000, so workloads can pull `localhost:5000/...` with no node
  configuration (T001 item 2). It listens on every node address, with no
  authentication and plain HTTP. Builds push to it through its Service,
  which reaches the node's address, so it can't listen on `localhost`
  alone.
  - **What could happen:** anyone who can reach the node on port 5000 (on
    a laptop, the local network) can pull images built from source, or
    push one under a name a workload will pull next.
- **What holds it today:** nothing in Serendip. A host firewall closing
  port 5000 to everything but the node itself closes it.
- **Revisit:** in spec 022 T015's security review. Candidates: a
  NetworkPolicy can't cover `hostNetwork` pods, so registry `htpasswd`
  auth with a pull Secret, or the containerd mirror (multi-node) with the
  registry back on the pod network.
- **Update, 2026-11-09 (T015 security review): wider than the LAN, and
  tags can be overwritten.**
  - **Who can push:** not only the LAN. Any pod in the cluster, and any
    build's own `RUN` steps (RP-049), reach the node's port 5000.
  - **What they can replace:** image names are predictable
    (`serendip/cmp-` and 10 hex characters of the Component ID's hash),
    and tags are the commit's first 12 characters, public for a public
    repository. Workloads pull by tag. A push over an existing tag runs
    the attacker's image at the victim's next pull (a new node, image
    garbage collection, an eviction).
  - **Fixes, in order:** record the pushed digest (`podman push
    --digestfile`) and deploy `repository@sha256:...`, which ends the
    overwrite; then registry `htpasswd` auth with a pull Secret, which
    ends anonymous push and pull. Both change the build workflow and the
    deploy path, and need a live run: a follow-up spec, not T015.
  - **Until then:** a host firewall closing port 5000 to the LAN; nothing
    yet stops an in-cluster pod. A single-team laptop (the target) is the
    case where that's tolerable.

## RP-042 — A followed build log polls the cluster while it waits for a step

- **Kind:** performance. **Status:** Accepted, 2026-11-01. **Found:** spec
  022 T006 (go-reviewer).
- **What:** `BuildLogs` with follow streams the steps one after another.
  - **While a step's pod doesn't exist yet,** or its container hasn't
    started, it polls every 2 s.
  - **Each poll costs up to three API reads:** a pod list, the
    `WorkflowRun` and the Argo Workflow.
  - **The cost is per follower.** Each open console tab or `--follow`
    adds its own polling.
  - **While a step is running,** it's one ordinary streamed request.
- **Why accepted:** the wait is short. It's the gap between steps, or an
  image pull. And followers are few on one machine. A watch would cost a
  held connection per follower anyway.
- **Revisit:** in T013's footprint run, if a followed build shows on the
  API server or `serendipd`. Candidates: back off from 2 s to 10 s, or
  share one poll between followers of the same Build.

## RP-043 — Auto-build's head read runs on the reconcile loop

- **Kind:** performance. **Status:** Accepted, 2026-11-06. **Found:** spec
  022 T007.
- **What:** the Component reconciler reads a branch's head inline: one
  HTTPS `GET` of `info/refs` per auto-build Component, at most every
  5 minutes.
  - **The engine is one goroutine,** so a slow repository holds up every
    other reconcile for as long as the read takes. That's at most the
    10 s reconcile timeout (the provider's own cap is 30 s).
  - **When it was last read is kept in memory.** After a restart, each
    auto-build Component is read once on its first pass, early.
- **Why accepted:** a reachable host answers in well under a second
  (T001 item 7: one 2.5 KB response), and a laptop has a handful of
  auto-build Components. Moving the read off the loop would add a worker
  and a hand-off for a cost nobody has measured yet.
- **Revisit:** in T013's poller measurement, or when a slow host shows up
  as resync lag. Candidates: a shorter head-read timeout than the
  reconcile's, or one background poller that enqueues the Component when
  the head moves.
- **Update, 2026-11-09 (T015 security review): it's also an
  availability risk.** Someone with Component write can point many
  auto-build Components at an address that never answers. Each holds the
  one engine goroutine for the 10 s reconcile timeout every 5 minutes, so
  about 30 of them keep it blocked all the time. **Fix, for the
  follow-up:** a 2-3 s head-read timeout, and the background poller
  above. Accepted for now: writing Components already needs a role at
  their Application, and an install is one team's.

## RP-044 — A credential's use is checked only when the Secret or repository changes

- **Kind:** security. **Status:** Accepted, 2026-11-06. **Found:** spec
  022 T008 (go-reviewer).
- **What:** naming a Component's `source.credentialSecret` needs
  `serendip/secrets/value/read` at that Secret (`api/build_handler.go`,
  `sourceChecker`).
  - **When it's checked:** when the credential is newly named, or the
    repository changes. Those are the two ways to send the credential to
    a host the caller chooses.
  - **When it isn't:** someone with Component write, but not the value,
    may still change the branch, path, Dockerfile or auto-build of a
    Component that already has a credential. That picks which of the
    repository's code a build runs, with that credential cloning it.
- **Why accepted:** the credential never reaches the code the build runs.
  - **The Git Secret is mounted only in the checkout step**
    (`providers/build/openchoreo/workflow.go`). Git reads it through a
    `GIT_ASKPASS` script in that container's own `/tmp`, so it isn't
    written into `.git/config`.
  - **The build step**, which runs the Dockerfile, mounts only the
    workspace (the checked-out source) and its own storage.
  - **So a Dockerfile can't print the credential** into the build logs.
  - **The checks wouldn't stop the code anyway.** Anyone who can push to
    the configured branch already changes what's built, without a
    Serendip edit.
  - **Escaping the privileged build pod** is RP-038's risk, for any build
    of code the caller controls.
- **The condition this rests on:** the credential stays out of the build
  step, and out of the workspace (`/mnt/vol`) the two steps share. Today
  the checkout writes only the source there. The remote in `.git/config`
  is the plain URL. **Revisit at once** if the build step ever mounts the
  Git Secret, gets the credential in its environment, or shares a
  container or `/tmp` with the checkout. The same goes if the checkout
  writes a credential helper, a `.netrc`, or a URL with the credential in
  it into the workspace. Also revisit if a build method other than
  the Dockerfile workflow is added. Then the check must cover every
  source field.

## RP-045 — A package's Helm release left locked is recovered by undoing the unfinished operation

- **Kind:** operability. **Status:** Accepted, 2026-11-06. **Found:** spec
  022 T013 (the package footprint run).
- **What T013 found:** the builds package couldn't be installed at all.
  - **The cause:** the engine caps each reconcile at 10 s, and the
    installer ran Helm (`--wait`, up to 10 min) inside it.
  - **What happened:** Helm was killed after ~8 s, which left the release
    `pending-install`. Every retry then failed with "another operation
    (install/upgrade/rollback) is in progress": 163 failed runs in 20
    minutes.
  - **Why the tests missed it:** T012's acceptance tests use fake
    installers.
- **The fix:**
  - **`core/reconcile` runs each package's install and removal as a
    background job** (`packagejob.go`), at most one per package, with
    its own 30-minute bound. A reconcile waits 3 s for the job and
    otherwise reads Installing and comes back. A removal's clean-up
    answers `ErrPackageBusy` until its job finishes.
  - **The builds installer recovers a locked release:** a pending first
    install is uninstalled, and a pending upgrade or rollback is rolled
    back. Then it installs again.
- **What's accepted:**
  - **The lock it recovers is assumed not to be live.** Serendip runs one
    job per package, so a lock it finds was left by a `serendipd` that
    died mid-Helm. If a person runs `helm` on that release at the same
    moment, their operation is undone.
  - **A job doesn't survive a restart.** Its Helm process dies with
    `serendipd`, and the next start recovers the release as above.
  - **A job isn't cancelled when its instance is removed.** The removal
    waits for the install to finish, up to its 30-minute bound.
  - **An installed package keeps reading Ready while a job runs.** The
    reconciler can't tell a resync's slow check from a reinstall after a
    spec edit, so during a multi-minute reinstall Ready=True and
    `Applied` are the previous install's until the job finishes.
  - **A locked release is recognised by Helm's English error text**
    ("another operation ... is in progress"). If a Helm version rewords
    it, the error is returned as-is and the package stays failed until
    someone recovers the release by hand. Check the text when bumping
    Helm.
- **Revisit:**
  - when a second package ships an installer (spec 023);
  - if people report managing these releases with `helm` themselves;
  - if one of the engine's reconcile goroutines is ever allowed to run
    longer than its timeout.

## RP-046 — A package's cost hides outside its own pods: Argo's full CRDs held kube-apiserver ~550 MiB higher

- **Kind:** footprint. **Status:** Mitigated, 2026-11-09. **Found:** spec
  022 T013 (the package footprint run).
- **What T013 found:** the first package run counted only the package's
  namespaces, and stated 34 MiB idle. But the node grew ~950 MiB.
  - **kube-apiserver:** 544 → 1125 MiB, and it stayed there.
  - **cert-manager's cainjector:** 25 → 83 MiB.
  - **The cause:** Argo's eight CRDs with the chart's default
    `crds.full=true` are 4.66 MiB of OpenAPI schema. That's more than every
    other API group in the cluster combined (kgateway 0.9 MiB, OpenChoreo's
    36 CRDs 0.36 MiB). The apiserver builds models from them; the
    cainjector watches every CRD.
- **The fix:**
  - **The builds installer uses the minimal CRDs** (`crds.full=false`,
    852 KB, with `x-kubernetes-preserve-unknown-fields`). kube-apiserver
    then idled at 637 MiB (586 without the package), and the cainjector
    at 30 MiB. The stated idle cost fell from 954 to 453 MiB.
  - **The harness states cost at the node level:** the node's use beyond
    the same run's platform phase, never below the package's own pods. So
    a cost like this can't hide again. The pods' own figure is kept as a
    breakdown.
- **What's accepted:**
  - **No server-side validation of Argo objects.** The minimal CRDs
    accept any `Workflow` body. Serendip writes every Workflow itself
    (through OpenChoreo's `WorkflowRun`), and Argo's controller still
    rejects a malformed one when it runs it.
  - **The node-level figure includes noise.** It takes in whatever else
    the node did during the run, plus ~320 MiB of runtime memory and file
    cache after builds pull their images, part of which the kernel can
    reclaim. It's stated anyway, rounded up, because that's what the node
    held.
  - **A build saturates the T470's two cores** for 2–4 minutes
    (~2000 mcores, the quota). The stated peak CPU says so. Nothing
    throttles builds below that yet.
- **Revisit:**
  - when the next package ships an installer (spec 023): check its CRD
    sizes, and measure it the same way;
  - if a build's CPU starves workloads in real use (a CPU limit on the
    build step);
  - on a real T470 (RP-008).

## RP-047 — A followed build log's first lines can be a few short of `tailLines`

- **Kind:** correctness (low). **Status:** Accepted, 2026-11-09.
  **Found:** spec 022 T014 (U3), fixed in part in T015.
- **What:** Argo writes a few lines of its own (`argo=true`) into a
  build step's main container. Serendip filters them out, but Kubernetes
  has already cut the tail. With `tailLines=5`, a step returned 2 lines.
- **The fix (T015):** a recent read asks Kubernetes for `tailLines + 20`
  and keeps the last `tailLines` of the step's own lines, so it returns
  the number asked for.
- **What's accepted:** a follow can't be trimmed the same way: it can't
  know where the backlog ends and the live lines begin. It asks for
  exactly `tailLines`, so its first lines can be a few short. New lines
  are all passed on.
- **Revisit:** if a client relies on an exact backlog when following.

## RP-048 — A source Component keeps running an earlier source's image until its current source builds

- **Kind:** correctness. **Status:** Decided, 2026-11-09 (spec 022 T015;
  T014 finding F2).
- **What T014 found:** a source Component ran only the newest successful
  build of its *current* source (plan decision 7). Once `source` changed
  and that build failed, its `status.image` emptied, it read "workload is
  not deployed", and its Deployments read `WaitingForComponent`. Yet the
  platform kept serving the earlier release.
- **The decision:** until the current source has a successful build, the
  Component runs the newest successful build of any earlier source, and
  says so. A new `Built` condition is on every source Component: True
  (`Built`) for a build of the current source, else False
  (`WaitingForBuild`), naming the build still running and why the
  current source isn't built. With no successful build at all, it reads
  `WaitingForBuild` and runs nothing, as before.
- **The cost:** Ready can read True while the current source isn't built;
  `Built` is where that shows. And a change to other fields in the same
  edit as the source (a port, an environment variable) rolls out with the
  earlier image, as it would on an image Component. `Built`'s message
  names the latest build's phase, so the Component is written once per
  phase change of that build (a few writes per build), not only when the
  image changes.
- **Revisit:** if people want a source change to stop the Component until
  it builds; that would be an opt-in, not the default.

## RP-049 — A build's own steps reach the whole cluster network

- **Kind:** security (medium). **Status:** Open, 2026-11-09. **Found:**
  spec 022 T015 security review.
- **What:** nothing limits where build pods in
  `workflows-serendip-builds` connect. A Dockerfile's `RUN curl ...` can
  reach in-cluster Services (PostgreSQL, OpenBao, internal APIs), the
  node's own ports, and the registry (RP-041).
- **Why not fixed in T015:** a policy that allows DNS, the registry and
  the internet but not the pod and Service ranges needs those ranges per
  install, and has to be checked live against Cilium. RP-038's escape is
  the larger risk on the same pods.
- **Fix:** a NetworkPolicy (or CiliumNetworkPolicy) on the build pods,
  applied by the installer.
- **Revisit:** with RP-038 and RP-041, in a follow-up builds-hardening
  spec.

## RP-050 — A build's credential copy is a plain Secret on the node while its run lasts

- **Kind:** security (low). **Status:** Accepted, 2026-11-09. **Found:**
  spec 022 T015 security review (plan decision 3 said it would be
  recorded; it wasn't until now).
- **What:** a private repository's credential is copied into Secret
  `<run>-git` in `workflows-serendip-builds` before its run starts, for
  the checkout to mount. While it exists, cluster admins, controllers
  with Secret read (Argo, OpenChoreo), anyone who can create pods in that
  namespace, and a build that escapes its pod (RP-038) can read it.
- **What holds:** it's never in a status, a log or the audit log (T014
  searched for it), and the build step doesn't mount it (RP-044's
  condition). Since T015 it's deleted as soon as the build reconciler
  sees the run end or expire, as well as with the Build, a cancel and the
  package's removal (T014 F3). Before, it lasted until the Build or the
  package was removed. That first delete is best-effort: if it fails, the
  build is still recorded as finished (it's logged), and the copy then
  lasts until the Build or the package goes, as before.
- **Revisit:** if builds move to short-lived per-run tokens (a Git host's
  app tokens), or the secrets capability can mount values itself.

## RP-051 — Auto-build reads any HTTPS address a Component names, internal ones included

- **Kind:** security (low). **Status:** Accepted, 2026-11-09. **Found:**
  spec 022 T015 security review.
- **What:** a Component's repository may be an IP address, `localhost`
  or any port, so whoever writes Components can make `serendipd` send a
  `GET` to an internal address.
- **What contains it:** HTTPS only, with certificates checked against the
  system's roots (self-signed internal endpoints, the kube-apiserver
  included, fail); a fixed path (`/info/refs?service=git-upload-pack`);
  the answer capped at 8 MiB, under a 30 s timeout and the reconcile's
  10 s; errors only in `serendipd`'s log, with the status saying only
  that it couldn't reach. Redirects stay HTTPS, and Go drops the
  credential on a redirect to another host.
- **Fix if wanted:** a dialer hook refusing loopback, link-local and
  private addresses unless an admin allows them. A self-hosted Git server
  on the LAN is a real case, so they can't simply be refused.
- **Revisit:** if Component write is ever given to people who shouldn't
  probe the network `serendipd` sits on.

## RP-052 — Build steps have no CPU or memory limits

- **Kind:** footprint, availability. **Status:** Open, 2026-11-09.
  **Found:** spec 022 T015 security review (T013 saw the CPU side: a
  build saturates the T470's two cores, RP-046).
- **What:** the `serendip-dockerfile` workflow sets only the workspace's
  storage. A Dockerfile can use all of the node's memory and CPU, and on
  one laptop that's the control plane's too.
- **Why not fixed in T015:** a limit has to be measured. Too low, and
  ordinary builds (a `go build`, an `npm install`) fail or crawl.
- **Fix:** requests and limits on both steps, sized from a measured run on
  the simulated T470, and stated in the package's cost.
- **Revisit:** in a follow-up builds-hardening spec, or as soon as a build
  starves a workload.

## RP-053 — Logs written while the log store is unreachable are dropped

- **Kind:** correctness, operability. **Status:** Open, 2026-11-12.
  **Found:** spec 023 T001.
- **What:** Fluent Bit (the observability package's collector) retries a
  failed flush a few times and then drops the batch ("cannot be
  retried"). Lines written while the store can't be reached never arrive.
  - The spike lost them twice:
    1. at install, when the plane chart's own NetworkPolicy blocked the
       `hostNetwork` collector;
    2. after a Docker restart changed the node's IP and the pinned rule
       stopped matching.
  - The pods' own logs are untouched, so recent lines are still readable
    on the Runtime tab while the pods live.
- **Mitigation (plan 023):** the node rule is built from the live `Node`
  addresses and checked on every resync, so an IP change is repaired
  within one resync. The readiness checks cover Fluent Bit's delivery.
- **Possible fix:** Fluent Bit's filesystem buffering with a larger retry
  limit, so batches survive a store outage. It costs disk and must be
  measured.
- **Revisit:** in spec 023 T012 (measure buffering), or if stored logs are
  ever used as an audit source.

## RP-054 — The observability adapters answer unauthenticated queries

- **Kind:** security, medium. **Status:** Open, 2026-11-12.
  **Found:** spec 023 T001.
- **What:** OpenChoreo's logs and metrics adapters, which Serendip queries
  (plan 023 decision 4, amended), have no authentication. Anything that
  can reach them reads every Component's logs, scoped by any IDs it
  sends; an omitted ID widens the scope to the whole namespace.
- **Mitigation:**
  - **NetworkPolicies:** the plane chart's policy, plus Serendip's,
    admit only pods in the observability namespace, and the nodes to
    OpenObserve's ingest port. Checked live on kind: a workload pod and
    a `hostNetwork` pod both time out.
  - **Serendip's own scoping:** it reaches the adapters only through the
    API server's Service proxy, with its own credentials. It always sends
    all three IDs, and drops any returned line that isn't the target's.
- **Residual:**
  - **Enforcement depends on the CNI.** kindnet admitted the API server's
    proxy traffic while blocking `hostNetwork` pods; K3s must be checked
    (T013, and RP-018's open item).
  - **Policies match by label,** so a relabel in a future chart re-opens
    them. The installer's checks verify that each selector matches a pod.
- **Revisit:** on every OpenChoreo or module upgrade (`VERSIONS.md`), and
  if the Observer's authorization becomes usable with Serendip's
  identity.
- **Spec 025 T006:** connection records are read the same way: SQL on
  the logs store's search API, over the port-forward, with the admin
  credentials, scoped by Deployment names Serendip reads from the
  cluster (never caller text).
- **Spec 025 T007:** traces too, scoped by the identity columns the
  collector attests (RP-086). The tracing module's adapter is off, so it
  adds no unauthenticated endpoint; its collector's OTLP ports take spans
  from any pod (RP-092).

## RP-055 — OpenObserve's log search slows as stored logs grow

- **Kind:** performance. **Status:** Accepted, 2026-11-14.
  **Found:** spec 023 T003.
- **What:** on the simulated T470, OpenObserve's mean search time went
  from 0.24 s in the first 10 minutes under load to 0.53 s over 2.5 hours
  (worst 3.1 s), with about 100 MiB stored. OpenSearch averaged 0.13 s and
  SigNoz 0.05 s on the same load (ADR 0021).
- **Why accepted:** it is the lightest store by far, and half a second is
  fine for a log tab. Retention is 3 days, which bounds the data searched.
- **Mitigation:** searches are always scoped to one Component and a time
  window (plan 023 decision 6), and the default window is short.
- **Revisit:** if T013's run on the reference laptop shows searches over
  2 s, or if retention grows. Options then: a narrower default window, or
  OpenObserve's own tuning (a larger `ZO_MEMORY_CACHE_MAX_SIZE`).
- **Spec 024 T011 (2026-11-16):** with 200 alert rules evaluating beside
  the same load, the alert queries plateaued over 2.5 hours (mean 255 to
  205 ms, p95 1.1 to 0.8 s, worst 4.9 s). They're `limit = 1` counts over
  5 or 15 minutes. That plateau stands for the 3 days of data the plan
  asked for (the user's choice of method). The load driver's own searches
  were slower with the rules competing: mean 0.90 s, worst 5.1 s. That's
  still under this entry's 2 s revisit line on average, but the worst
  case is over it. The rules' load on OpenObserve is RP-080.

## RP-056 — Serendip's plain Prometheus must keep matching the metrics adapter

- **Kind:** operability, correctness. **Status:** Accepted, 2026-11-14.
  **Found:** spec 023 T003.
- **What:** ADR 0021 replaces the metrics module's kube-prometheus-stack
  with plain Prometheus, configured by Serendip to look like it to the
  module's adapter:
  - the Service name and port the adapter's address fixes;
  - the scrape job names;
  - kube-state-metrics' metric and label allowlists.
  An upgrade that changes the adapter's queries or its address breaks
  metrics with no install error: queries return empty series.
- **Why accepted:** it saves about 450 MiB at idle and leaves nothing in
  kube-apiserver after removal (the module's 10 CRDs held about 390 MiB
  there until a restart).
- **Mitigation:**
  - `VERSIONS.md` lists what to re-check on every module upgrade.
  - The installer's readiness check runs a real `resource` query for a
    known pod and fails if its series are empty (T005).
- **Revisit:** on every metrics module upgrade, or if a later alerts spec
  needs the operator anyway.

## RP-057 — ThunderID's Service-proxy calls drop a kubeconfig's bearer token

- **Kind:** correctness (latent). **Status:** Watch, 2026-11-14.
  **Found:** spec 023 T005.
- **What:** `kubernetes.Client.HTTPClient()` returns the client's
  TLS-configured `http.Client`, but the bearer token is added per request
  inside the client's own calls. `providers/identity/thunderid`
  (`ready.go`, `clients.go`) sends Service-proxy requests through
  `HTTPClient()` directly, so with a token-based kubeconfig they would
  answer 401.
- **Why it holds today:** K3s and kind kubeconfigs authenticate with
  client certificates, which the transport carries.
- **Mitigation:** new proxy callers use `Client.PostRaw` or the other
  path-taking calls, which send the token (the observability installer
  does).
- **Revisit:** before supporting a token-based kubeconfig (a managed
  cluster, a service-account kubeconfig), or the next time ThunderID's
  provider is touched. The fix is to move those calls onto the client's
  own request path.

## RP-058 — Stored-log pages and tied timestamps

- **Kind:** correctness. **Status:** Accepted, 2026-11-14.
  **Found:** spec 023 T006.
- **What:** stored-log searches page by time
  (`providers/observability/openchoreo/provider.go`, `search`). The cursor
  holds a time and how many lines at that time were already returned.
  The logs adapter sorts by timestamp with no tiebreak, and the stores
  keep microseconds.
  - **Lines that share a microsecond** are counted in the order the store
    returns them. If the store returns tied lines in a different order
    on the next query, a page boundary inside the tie can repeat one line
    and skip another.
  - **A tie group larger than one answer (1,000 lines)** is passed on as
    far as it fits, then skipped, and the page says `truncated`.
- **Why accepted:** a page boundary has to fall inside a tie for either
  to matter, and a thousand lines in one microsecond from one Deployment
  doesn't happen with ordinary logging. Lines from different targets are
  never confused: ties keep the targets' order.
- **Mitigation:** a source that answered in full is never trusted below
  its oldest line; the next round starts there. Tests cover ties across
  targets and page sizes from 1 up, with the end bound inclusive and
  exclusive.
- **Revisit:** if the adapter gains a tiebreak or a cursor of its own
  (T001 found none), use it.

## RP-059 — A re-created platform object hides its predecessor's logs

- **Kind:** correctness. **Status:** Accepted, 2026-11-14.
  **Found:** spec 023 T006.
- **What:** the adapters scope by the UIDs of the OpenChoreo Component,
  Project and Environment (spike finding 5). Serendip resolves them from
  the live objects (`scope.go`). If one is deleted and re-created under
  the same name, by hand or by a reconciler repairing it, it gets a new
  UID, and lines stored under the old one aren't found any more. They
  age out with retention (3 days).
  - **For up to a minute after a re-creation,** resolved UIDs are
    cached, so searches still use the old UID and miss the new object's
    lines. That's narrower, never wider.
- **Why accepted:** Serendip never re-creates these objects in normal
  use, and widening the scope to find them would break the rule that a
  query never reads beyond its targets.
- **Revisit:** if a repair path that re-creates the objects is added.

## RP-060 — Fluent Bit's read-position file stays on the node

- **Kind:** operability. **Status:** Accepted, 2026-11-14.
  **Found:** spec 023 T006, live on `serendip-t018`.
- **What:** the logs module's Fluent Bit keeps how far it has read each
  container log in `/var/lib/fluent-bit/db` on the node (a hostPath).
  Uninstall deletes the namespace, but not that directory. On
  `serendip-t018` it dated from T001's install two days earlier.
  - **On a reinstall,** Fluent Bit resumes where it stopped, so lines
    written while the package was installed before aren't stored again
    (the old store went with the namespace), and only new lines arrive.
  - **On a node that never had the package,** `Read_from_Head` reads
    every container log there (43,507 records in T001).
- **Why accepted:** it's a few kilobytes, and removing it needs a
  privileged pod on each node, which the package shouldn't need.
  Resuming also spares a reinstalled package re-reading every log on
  the node.
- **Revisit:** if a reinstall should bring back earlier logs, or the
  node agent (`serendip-agent`) gains a clean-up step for packages.

## RP-061 — Reported disk use lags, and leaves out the newest data

- **Kind:** honesty of a reported number. **Status:** Accepted,
  2026-11-14. **Found:** spec 023 T006, live on `serendip-t018`.
- **What:** `GET /v1/packages` shows the package's current disk use
  (`usage.go`). The kubelet doesn't measure local-path volumes (T002),
  so each store is asked:
  - **Prometheus** reports its blocks and WAL, which match its volume;
  - **OpenObserve** reports its streams' stored and index sizes. It
    updates them every 10 minutes, and they leave out its write-ahead
    log (the newest minutes of lines) and its metadata database (about
    5 MiB). On a fresh install with the bench's loggers running, they
    read 0 for the first 8 minutes; at 13 minutes they read 0.49 MiB,
    exactly the stored files, while 7.3 MiB was on the volume (5.2 MiB
    of it the metadata database, 1.4 MiB the write-ahead log).
- **Why accepted:** the number is a guide next to a stated budget; the
  volumes' sizes and retention bound the real use. Measuring the volume
  itself needs a `du` from a privileged pod or an exec into the store,
  which the product shouldn't need.
- **Mitigation:** the console (T011) labels it as reported by the
  stores. T012 compares it with a `du` after 2 hours under load.
- **Revisit:** if a store exposes its on-disk size, or local-path is
  replaced by a CSI provisioner whose volumes the kubelet measures.

## RP-062 — OpenObserve's SSRF guard is off

- **Kind:** security. **Status:** Accepted, 2026-11-14. **Found:** spec
  023 T012, on a fresh `bench-t470`.
- **What:** OpenObserve v0.92.2 refuses alert destinations at private
  addresses (its SSRF guard). The logs module's setup Job creates one,
  OpenChoreo's webhook at `http://logs-adapter:9098`, a ClusterIP, so
  on a fresh store the Job fails and the install never becomes Ready.
  `serendip-t018` didn't show it, because its destination already
  existed from the first install. The installer now sets
  `ZO_SSRF_ALLOW_LOOPBACK=true`, as the module's newer releases do. At
  v0.92.2 that skips the create-time check entirely, not only for
  loopback (checked live: the destination was then created).
- **Why accepted:** only Serendip (with the admin Secret, kept in the
  cluster) and the chart's Job can reach OpenObserve's API: the plane's
  NetworkPolicies admit only pods in its namespace, plus the node rule
  to the ingest port. No person or AI creates OpenObserve destinations;
  alerts are spec 024's, through Serendip's API. OpenObserve has no
  allow-list at v0.92.2 to admit only `logs-adapter`.
- **Mitigation:** the policies above; spec 024 must validate any
  destination a person sets before it reaches OpenObserve.
- **Revisit:** when OpenObserve has an allow-list of hosts or CIDRs (a
  chart PR mentioned `ZO_SSRF_ALLOWED_HOSTS`; not in v0.92.2), or when
  spec 024 adds notification channels.
- **Revisited, spec 024 (2026-11-16):** the second condition is met, and
  no person-typed destination ever reaches OpenObserve. `serendipd`
  evaluates rules and sends every notification itself (ADR 0022), so
  OpenObserve has no alert destination but the logs module's own. The
  mitigation is now `core/notify`'s address check at every dial (plan
  024 decision 5, RP-076). The guard stays off for that one webhook.

## RP-063 — The stated disk growth is a 2-hour slope, extrapolated

- **Kind:** honesty of a reported number. **Status:** Accepted,
  2026-11-14. **Found:** spec 023 T012 (go-reviewer).
- **What:** the observability package states 496 MiB a day: the
  volumes' growth over the benchmark's 2-hour disk phase, scaled to 24
  hours, under T002's synthetic load (five loggers, 10 lines a second
  each). A fresh store's first hours aren't steady state: the 31-minute
  load phase before it scaled to 727 MiB a day, as the logs store
  compacted its first data. Neither phase reaches the 3-day retention,
  so the ~1.5 GiB level the note states is growth × retention, not a
  measurement. A real cluster's logging rate can be far higher or lower.
- **Why accepted:** a multi-day run per package is out of proportion
  for v1. The figure only informs the person installing, and the
  package's own volumes (5 GiB logs, 3 GiB metrics, Prometheus capped at
  2 GB) bound the real use whatever the rate. `GET /v1/packages` reports
  the measured current use beside the stated figure.
- **Revisit:** in the full test pass after v1, with a run past the
  retention window on reference hardware.

## RP-064 — Fluent Bit's monitoring port answers any pod on the node

- **Kind:** security (information exposure). **Status:** Fixed in spec
  023 T014 (2026-11-14); accepted in T013 until then. **Found:** spec
  023 T001 (noted in plan.md), checked on `serendip-t018` in T013
  (criterion 9).
- **Fix (T014, the user's choice of option 1):** Fluent Bit runs off the
  host network (`fluent-bit.hostNetwork=false`, `dnsPolicy=ClusterFirst`),
  so the plane chart's policy, which admits only pods in the namespace,
  covers `:2020`. That needed Serendip's own copy of the chart's config
  (RP-065). The node rule `serendip-allow-node-to-openobserve` is gone:
  Fluent Bit's flushes now come from a pod in the namespace. **Checked on
  `serendip-t018`:** from a workload pod, `:2020` timed out by Service and
  by pod IP, and the node's address refused it; the DaemonSet's probes
  pass; a Component's lines were searchable through the API about a
  second after they were written, scoped by its UID.
  - **Not handled:** an install from before T014 would keep the node rule
    until the namespace is removed. Spec 023 was never released, so no
    such install exists outside the test clusters, whose namespace was
    removed first.

The text below is the T013 record.
- **What:** the logs module's Fluent Bit runs with `hostNetwork`, so the
  plane's NetworkPolicies don't apply to it, and its HTTP server listens
  on `0.0.0.0:2020`. From a workload pod in another Project,
  `fluent-bit.openchoreo-observability-plane:2020` answered
  `/api/v1/metrics` (records and bytes per input, filter and output),
  `/api/v2/metrics` (the same as Prometheus text, with the node's host
  name), `/api/v1/uptime` and the version. No log lines, labels or
  pod names. Every other part of the plane (OpenObserve, Prometheus,
  the Observer, both adapters, kube-state-metrics) timed out, by
  Service and by pod IP.
- **Why accepted:** it isn't cheap to close. The DaemonSet's own liveness
  and readiness probes use that port, and on the host network the
  kubelet probes the node's address, so turning `HTTP_Server` off or
  listening on `127.0.0.1` would break them. What leaks is how much the
  node logs, which a workload can roughly see from its own node anyway.
- **Mitigation:** none beyond the content being counters only.
- **Revisit:** if the chart lets the probes be exec-based or the server
  bind to a node-local address; or on a multi-tenant edge profile where
  a tenant's log volume is itself sensitive.

## RP-065 — Serendip carries the logs chart's Fluent Bit config

- **Kind:** maintenance; performance (not re-measured). **Status:**
  Accepted, 2026-11-14. **Found:** spec 023 T014, fixing RP-064.
- **What:**
  - **The copy:** the logs module 0.8.0 hard-codes the kubernetes
    filter's `Use_Kubelet true`, with `Kubelet_Host` at its default of
    127.0.0.1, so off the host network it can't reach the kubelet. Serendip
    embeds the chart's render of its Fluent Bit ConfigMap verbatim
    (`providers/observability/openchoreo/fluent-bit/`), edits two lines
    (`Use_Kubelet false`, `tls.verify On`), applies it as
    `serendip-fluent-bit`, and points the chart at it. Every chart upgrade
    must re-render it; the tests fail if an edited line moved, and
    `Install` refuses rather than the binary panicking at start.
  - **The API server instead of the kubelet:** each Fluent Bit pod GETs a
    pod's metadata from kube-apiserver the first time it tails that pod's
    log, then caches it (no watch). On a single-node edge that is a small
    burst after a restart or a large rollout. T012's footprint
    (`requirements.go`) was measured with the kubelet lookup and wasn't
    re-run.
  - **Drift:** an edited or deleted copy is re-applied within a resync and
    Fluent Bit's pods are restarted (checked live: about 10 s). A
    deliberate change rolls the DaemonSet through a config-hash pod
    annotation.
- **Why accepted:** the alternatives were the kubelet on the node's
  address (also a copy, plus repeating the chart's four credential `env`
  entries, since Helm replaces the list) or leaving RP-064 open.
- **Revisit:** when the logs module makes the kubelet host or
  `Use_Kubelet` configurable (then drop the copy); and in the post-v1
  full test pass, re-run the T012 package benchmark to confirm the
  footprint.

## RP-066 — The plane chart's alert-rule CRD outlives the package's removal

- **Kind:** footprint. **Status:** Decided, 2026-11-15. **Found:** spec
  023 T001 (plan "Spike findings" 16).
- **What:** uninstalling the observability package deletes the
  `ObservabilityPlane`, the three Helm releases and the namespace, but
  Helm leaves the plane chart's `observabilityalertrules` CRD. The
  Prometheus Operator's 10 CRDs, which kept kube-apiserver's memory up
  after removal (plan 023 decision 3), aren't installed at all (plain Prometheus, ADR 0021).
- **Why decided:** the CRD is small, and deleting a CRD deletes every
  object of its kind, which a person's own use could depend on. Same
  reasoning as RP-039's Argo CRDs.
- **Revisit:** if spec 024 (alerts) creates objects of this kind, so
  removal must say what it leaves; or if a re-install with a newer chart
  finds the old CRD's schema in the way.
- **Revisited, spec 024 (2026-11-16):** unchanged. Spec 024 makes no
  `observabilityalertrules` objects (ADR 0022), so the CRD stays empty.

## RP-067 — Stored logs keep what pods print, secrets included

- **Kind:** data exposure. **Status:** Accepted, 2026-11-15. **Found:**
  spec 023 plan (Risks).
- **What:** with the observability package installed, everything a
  container prints is kept in OpenObserve for 3 days, including any
  secret a workload or a build step prints. Live pod logs already showed
  it, but only while the pod lived.
- **Mitigation:**
  - **Stored logs need `serendip/components/logs/read`,** the same action
    as live logs, which the read-only and access-management roles leave
    out (`authorization.md`). An Application's search reads only the
    Deployments the caller may read.
  - **Bounded:** 3-day retention, and removing the package deletes the
    store's volume.
  - **The store itself** is reachable only from its namespace
    (NetworkPolicy; RP-054), and its admin credentials live only in a
    cluster Secret.
- **Residual:** no redaction. A secret printed once is searchable for 3
  days by anyone holding Log Reader on that Deployment, and in the
  volume's files on the node.
- **Revisit:** if a profile needs redaction or a shorter retention for
  some Projects, or when Fluent Bit filters become configurable per
  Project.

## RP-068 — A noisy Component can fill the logs volume before retention acts

- **Kind:** availability, capacity. **Status:** Accepted, 2026-11-15.
  **Found:** spec 023 plan (Risks).
- **What:** retention drops logs older than 3 days, not by size. A
  Component logging far beyond the benchmark's load (about 496 MiB a day
  for five loggers, RP-063) could fill OpenObserve's 5 Gi volume first.
  Then new logs stop being stored (RP-053's dropped lines) until
  compaction frees space. Prometheus has a size cap (2 GB of its 3 Gi)
  as well as its 3 days.
- **Mitigation:** the volumes are bounded, so the node's disk isn't
  filled; the package reports current use (`usage` on
  `GET /v1/packages`, the Packages page) against the stated budget.
- **Residual:** the reported use lags (RP-061). The "no alert" part is
  closed by spec 024 T006: the package's built-in `observability-disk`
  rule warns at 85% of the budget, to the platform's default channels.
- **Revisit:** if a size-based cap for the logs store becomes settable
  in the logs module. (The spec 024 trigger is done: the built-in
  `observability-disk` rule, live-checked in T010.)

## RP-069 — The Telegram bot token is the whole credential

- **Kind:** security. **Status:** Accepted, 2026-11-16. **Found:** spec
  024 plan (Risks); built in T004.
- **What:** anyone holding a bot's token can read the bot's chats
  (`getUpdates`) and send as it. Telegram's Bot API takes the token in
  the URL path (`/bot<token>/sendMessage`), so any error or log that
  carries the URL carries the token.
- **Mitigation:**
  - it's a Secret (`botTokenSecretId`), read at send time and never
    cached; responses show only `secretSet`;
  - errors are cleaned down to the host, and Go's `url.Error` is
    unwrapped (`core/notify/telegram.go`), so no Alert, event or log
    holds it. T010 searched the store, audit rows, events and
    `serendipd`'s log for the live token and found none;
  - naming it on a channel needs the Secret's value read;
  - "Find chat" (`POST /v1/secrets/{id}/telegramchats`) answers chats
    only, never the token, and needs the same value read.
- **Residual:** Telegram itself, and the Secrets provider, hold it.
  BotFather's `/revoke` is the only rotation.
- **Revisit:** if Telegram offers scoped or expiring bot credentials, or
  when reading replies (account linking) needs the bot to read more.

## RP-070 — Alert content leaves the machine

- **Kind:** data exposure. **Status:** Accepted, 2026-11-16. **Found:**
  spec 024 plan (Risks).
- **What:** every notification sends the rule's summary, the Project,
  Application, Component and Environment names, the value and threshold,
  and a console link to a third party: Telegram's servers, the SMTP relay
  (Gmail, Brevo) and the mail providers after it, or a webhook receiver
  and whatever it forwards to.
- **Mitigation:**
  - **never log lines:** a `logMatch` Alert records the count;
  - the console says so on the channel form and the Notifications
    page (`LeavesNotice`);
  - channel, SMTP and heartbeat writes are left off MCP, so people,
    not AI agents, decide where content goes;
  - the console link needs a sign-in to show anything.
- **Residual:** a summary is free text a person writes; it can name
  anything.
- **Revisit:** if a profile needs on-premises-only delivery (a LAN
  webhook with `allowPrivateDestination` already does it), or a redacted
  body mode.

## RP-072 — Rules are polled

- **Kind:** correctness. **Status:** Accepted, 2026-11-16. **Found:**
  spec 024 plan (Risks).
- **What:** each rule is evaluated on its interval (1–60 minutes) over
  its window, not on every data point. So:
  - a condition shorter than the interval can fall between two
    evaluations and be missed (a `metric` rule's `for` reads every point
    in its window, which narrows this; a restart counter's max − min
    still sees restarts inside the window);
  - a firing is seen up to one interval late, 1 minute at best, plus the
    data's own lag (RP-061 for disk).
- **Mitigation:** the 1-minute floor, windows that cover the interval,
  and next runs counted from the due time (T011's 66 s drift fixed).
- **Revisit:** if a use needs sub-minute alerting. That needs push from
  the backend, which ADR 0022 turned down for this install.

## RP-073 — A `logMatch` threshold is capped at 999

- **Kind:** correctness (limit). **Status:** Accepted, 2026-11-16.
  **Found:** spec 024 plan (Risks).
- **What:** the logs adapter pages at 1,000 lines. T001 found its answer
  carries `total`, but if `total` were ever absent the fallback counts a
  page of `above + 1`, which can't go past 1,000. So `above` is capped at
  999 (`resource.MaxCountAbove`), and "more than N lines" can't count
  higher.
- **Revisit:** if the adapter's `total` is confirmed stable across
  releases (the fallback could go), or a rule needs higher counts.

## RP-074 — The heartbeat proves Serendip can alert, not that a person was told

- **Kind:** operability. **Status:** Accepted, 2026-11-16. **Found:**
  spec 024 plan (Risks); the header limit in T013.
- **What:**
  - the heartbeat beats while the evaluator, the delivery loop and the
    store work. A revoked Telegram token, a full mailbox or a broken
    relay still lets it beat; those show as `failed` deliveries on the
    Alert, not as a missing heartbeat;
  - it sends a plain `GET` or empty `POST` and can't set headers. Gatus's
    `external-endpoints` push needs `Authorization: Bearer`, so Gatus
    (plan 024's suggestion for `small-3` and `standard-5`) works only
    through a relay that adds it.
- **Mitigation:** a channel's test send and `lastTestResult`; deliveries'
  recorded errors; healthchecks.io and Uptime Kuma take the token in the
  URL (`docs/guides/alert-integrations.md`).
- **Residual:** nothing alerts on failed deliveries themselves.
- **Revisit:** if a monitor people want needs a header (an optional
  header from a Secret would be small), or with a "deliveries failing"
  rule.

## RP-075 — Alerting authorization: checks made before the write, and wide custom roles

- **Kind:** security. **Status:** Accepted, 2026-11-15.
  **Found:** spec 024 T003 (go-reviewer).
- **What:**
  - A rule's channels and a channel's Secrets are checked against the
    stored channel before the rule or channel is written, not in the
    same transaction. If a channel's owner or `default` flag changes in
    that window, a rule could name a channel its writer couldn't read a
    moment later.
  - A platform default channel can be named by anyone who may write a
    rule (decision 9), so a caller can tell a platform default channel's
    ID from any other ID they can't read.
  - Only the built-in Contributor role leaves out
    `serendip/notificationchannels/allowPrivateDestination/action`. A
    custom role granting `serendip/notificationchannels/*` (or `*`)
    includes it, as Azure's wildcards do.
- **Mitigation:** the window is one request's length, and changing a
  channel's owner or default flag needs write on it. Channel IDs are
  random. Custom roles are made by access administrators, who see their
  actions.
- **Residual:** as above.
- **Revisit:** if channels become shared across Projects other than
  through platform defaults, or if a custom-role editor should warn about
  wildcards covering Owner-only actions.

## RP-071 — Resolved Alerts are removed after 30 days by system retention

- **Kind:** audit, data retention. **Status:** Accepted, 2026-11-15.
  **Found:** spec 024 plan decision 11; built in T005.
- **What:** `serendipd` removes resolved Alerts that resolved more than 30
  days ago, at most 500 a pass, hourly (`sqlite.PurgeResolvedAlerts`).
  It's system retention of operational records, not a person's delete
  (spec 019's RP-003 left that open), but still a delete: each pass
  records one Approval per rule, decided by policy and Executed in the
  same transaction, naming the removed Alerts (constitution 5, ADR 0016;
  plan decision 11's "no Approval" was amended in T005 review). Azure
  Monitor keeps fired alerts for 30 days the same way.
- **Mitigation:** the purge can remove nothing but resolved Alerts, which
  nothing depends on and no route deletes. Each removed Alert gets an
  audit row under `system:alerts` naming its Approval. A backlog is
  purged in batches (up to 20 × 500 a run), so it can't outgrow the
  hourly pass.
- **Residual:** a removed Alert's history (its deliveries) is gone; the
  Approval keeps its ID and name. The purge scans Alerts' JSON status (no
  index on `resolvedAt`), which is fine at this scale.
- **Revisit:** if a profile needs longer retention, or an export before
  removal.

## RP-076 — The sender's "cluster's own ranges" are learned from nodes and ServiceCIDRs

- **Kind:** security (SSRF). **Status:** Accepted, 2026-11-15.
  **Found:** spec 024 T006.
- **What:** `core/notify` refuses the cluster's own ranges whatever a
  channel allows. `serendipd` learns them from the cluster
  (`kubernetes.Client.ClusterRanges`): each node's `spec.podCIDRs` and
  addresses, and the ServiceCIDR API's ranges, re-read every 5 minutes
  (30 s after a failed read). So:
  - pod ranges are the per-node slices, not the cluster-wide pool, which
    the API doesn't expose. A CNI that allocates pod addresses without
    `podCIDRs` (Cilium's cluster-pool IPAM, spec 025) leaves them out;
  - a node that joins is unknown for up to 5 minutes;
  - before the first read, a private address is refused even where a
    channel allows one (the check fails closed, T006 review).
- **Mitigation:** these ranges are private addresses, refused anyway
  unless a channel has `allowPrivateDestination`, an Owner-only action
  with a reason (T003). A failed or empty read never clears ranges a good
  read set; a partial read adds what it found to the last ranges.
- **Residual:** a channel allowed private destinations could reach a pod
  address outside the nodes' `podCIDRs` while that's so.
- **Spec 025 T008:** the networking capability's `PodRanges` (Cilium's
  cluster-pool ranges when IPAM isn't `kubernetes`) are merged into every
  read. Until the network layer's first read (seconds after start), only
  the nodes' and Service ranges are known; the check still fails closed.
  A CNI Serendip can't ask (`external`) still leaves the residual above.
- **Revisit:** an `external` CNI with its own IPAM, if one is supported.

## RP-077 — Alert condition trade-offs found in T006

- **Kind:** correctness; footprint. **Status:** Accepted, 2026-11-15.
  **Found:** spec 024 T001 and T006.
- **What:**
  - **Restarts** are `max − min` of kube-state-metrics' counter over the
    window (T001: `increase()` overstated 6 restarts as 7.08). A pod's
    restarts before its first scrape aren't counted (1 per pod in the
    spike), and a restart is seen ~40 s to ~2 minutes late. An empty
    answer is 0 only when the pods' restart series exist (a second
    `count` query); otherwise it's no data, so a stopped exporter can't
    resolve a firing Alert. A Deployment scaled to 0 reads `NoData`.
  - **The built-in disk rule is owned by its package**, so removing the
    package cascades (the console now sends `cascade=true`, and its
    confirmation lists the rule). The rule's Alerts go with it, so a
    firing disk Alert gets no "resolved" message when the package is
    removed; other rules' Alerts resolve with `packageRemoved` as
    planned.
  - **One more series per container**
    (`kube_pod_container_status_restarts_total` in `metricAllowlist`),
    next to ~9,600 head series. The package's measured footprint
    predates it.
- **Mitigation:** a crash loop keeps counting, so the first missed
  restart only delays a firing. A disk alert for a package being removed
  is moot: its data is deleted. T011's benchmark measures the package
  with the new series.
- **Residual:** as above.
- **Revisit:** if T011's numbers move the package's stated cost, or a
  restart alert must catch a single restart.

## RP-078 — Alerting API trade-offs found in T007

- **Kind:** audit volume; correctness; footprint. **Status:** Accepted,
  2026-11-15. **Found:** spec 024 T007.
- **What:**
  - **The heartbeat's status is written only when its result changes,
    or every 10 minutes while it's steady** (`HeartbeatStatusEvery`).
    Every store write is an audit row, and a beat a minute would add
    1,440 rows a day. So `lastSent` can lag the real last send by up to
    10 minutes. The outside monitor, not `lastSent`, is the alarm.
  - **The address check at save refuses only a refused address.** A name
    that doesn't resolve yet (DNS down, a host not created yet) is saved,
    and every send checks again. A name re-pointed at a private address
    after it's saved is refused at the send (tested at test send: 422).
  - **Status is the server's on rules, channels and SMTP servers**
    (`WithServerStatus`): a PUT's `status` is ignored. Before this, the
    generic PUT stored a client's status, so writing a rule could set its
    silence without `silence/action`. The other generic kinds still
    store a body's status (spec 002's behaviour), which isn't
    authorization-relevant for them today.
  - **A rule PUT can answer 409** when the evaluator flushed the rule's
    status (at most once a minute) between the client's read and its
    write. The client re-reads and retries, as for any conflict.
  - **The channel list reads each named Secret** for `secretSet` (one
    store read per Secret per channel). That's fine at this size (a few
    dozen channels). Revisit if lists get slow.
  - **Alert lists filter in memory**: every Alert is read, the request's
    own filters (state, severity, Component, Environment; firing for the
    summary) are applied, and only the survivors are authorized one by
    one, then capped at 500. Retention keeps resolved Alerts for 30 days
    (RP-071), and 200 rules fold flaps into one Alert each. The console's
    bell polls the summary, which authorizes only firing Alerts.
  - **Test sends run two at a time**; a third answers 429 with
    `Retry-After`. Each holds a connection for up to 40 s.
  - **The heartbeat retries a failed send within a minute**, and while
    it's paused re-checks every 15 s, so its first beat after Serendip
    recovers isn't held back an interval.
- **Mitigation:** as above. A test send and a heartbeat test always
  write, so each has its audit row.
- **Residual:** `lastSent` granularity; one extra query per Secret on
  channel lists.
- **Revisit:** if a console view needs `lastSent` to the minute, or Alert
  lists pass about 10,000 rows.

## RP-079 — The API drops unknown fields in a resource document

- **Kind:** correctness. **Status:** Mitigated in spec 024 T012 (unknown
  metadata fields refused). **Found:** spec 024 T010.
- **What:** `decodeBody` ignores fields it doesn't know. A typo'd or
  invented field is dropped and the request succeeds. In T010, channels
  created with `metadata.ownerId` (what the catalogue's description
  said, F3) were stored without an owner, so they became platform
  channels, and nothing said so. The description is fixed; the silent
  drop isn't.
- **Mitigation:**
  - the description now names `ownerRefs` and `resourceVersion`;
  - **since T012, `decodeBody` refuses unknown `metadata` fields** for
    every client and every route that takes a resource document: 400
    "unknown metadata fields", naming them and listing the known ones.
    Keys still match case-insensitively, as `encoding/json` does. Store
    reads stay tolerant (the check is in `api/`, not `core/resource`);
  - `TestResourceBody_UnknownMetadataRefused`;
  - the console builds its documents from typed forms.
- **Why metadata only:** metadata decides ownership and so
  authorization, and its shape hasn't changed since spec 001. Refusing
  unknown `spec` or top-level fields everywhere would break clients that
  send back a response's view-only fields (a channel's `secretSet`,
  `platformChannel`). That's an API-wide contract change, not a spec 024
  fix.
- **Residual:** an unknown `spec` field is still dropped silently. A
  dropped spec field can't change who owns a resource; at worst a
  setting is left at its default.
- **Revisit:** a versioned API (`v2`) or a `strict=true` query option, if
  a dropped spec field causes a real mistake.

## RP-080 — 200 alert rules load the observability backends, and their status writes fill the audit log

- **Kind:** footprint; audit volume. **Status:** the backend load is
  Accepted; the status writes are Mitigated (the user chose option a,
  2026-11-16; done in spec 024 T012). Recorded 2026-11-16. **Found:** spec 024 T011 (the
  simulated T470, `profiles/edge-1/bench/results/2026-11-15-spec024-simulated-t470/`).
- **What:**
  - **Backend load.** With 200 rules at 1 minute and spec 023's log
    load running:
    - `serendipd` itself used +4.2 mcores and +5.5 MiB;
    - the node used +149 to +234 mcores mean (peak 1.2 cores) and about
      +177 MiB;
    - OpenObserve averaged 179 mcores and 375 MiB, against 40 mcores and
      264 MiB with the same load and no rules (spec 023).

    That's 7–12% of a 2-core T470. The run's cadence was 66 s, which is
    fixed now, so about 11% more queries are expected than were
    measured.
  - **Status writes.** About 26 AlertRule writes a minute:
    - up to 20 of them are the 10-minute `lastEvaluated` refresh
      (`alertStaleEvaluated`), which every rule gets even when nothing
      changed;
    - the rest are values that changed, flushed once a minute.

    Each write is an audit row of about 2 KiB (a snapshot of the rule).
    That's about 37,000 rows and 75 MiB a day, more than 25 GiB a year,
    and the audit log has no retention yet (RP-003).
- **Why the backend load is accepted:** 200 rules at 1 minute is the
  worst case the caps allow (plan decision 2), and a real install has far
  fewer. The evaluation latency held a plateau over 2.5 hours (p95 under
  1.2 s, worst 4.9 s, against a 20 s timeout).
- **Mitigation:**
  - the 200-rule cap and the 1-minute floor;
  - at most 2 queries at once;
  - `logMatch` is a `limit = 1` count.
- **The status writes (option a, the user's choice, 2026-11-16):** of
  the options in plan 024 "Findings from T011" 5 (a: refresh hourly; b:
  answer from the evaluator's memory only; c: accept until the retention
  spec), a was chosen. In spec 024 T012:
  - `alertStaleEvaluated` is 1 hour, not 10 minutes. A rule whose value
    holds is written once an hour, so 200 rules make about 3 refresh
    writes a minute instead of 20. Values that changed are still flushed
    once a minute. That's about 3–9 writes a minute in all, roughly
    4,000–13,000 audit rows a day instead of 37,000 (not re-measured);
  - the evaluator keeps each rule's live time (`LastEvaluated`), and
    the API's rule answers (get, list, silence and unsilence) show it
    when it's newer than the stored one (`api.WithRuleEvaluated`). So
    the console, CLI and MCP see a live time;
  - `TestAlerts_SteadyRuleWritesHourly` and
    `TestAlertRules_LiveLastEvaluated` cover it.
- **Residual:** the store's `lastEvaluated` can be up to an hour old,
  and after a restart the API shows that stored time until the rule's
  next evaluation (within one interval). Changed values still write. The
  audit log still has no retention (RP-003).
- **Revisit:**
  - T013's run on the reference laptop;
  - a raised rule cap;
  - the audit retention spec.

## RP-081 — The L7 proxy that gives request metrics retries 5xx and rewrites paths by default

- **Kind:** correctness (application behaviour). **Status:** Mitigated,
  2026-11-20. **Found:** spec 025 T001 (scratch cluster `t025`, Cilium
  1.20.5).
- **What:** Hubble's HTTP metrics need Cilium's Envoy in the path of each
  Component's HTTP port (an L7 policy). Envoy's defaults then change what
  the application sees:
  - **`http-retry-count` 3:** every 5xx is retried up to three times. A
    test server answering 10% 500s looked 100% healthy: the metrics showed
    only 200s, the server got more requests than were sent, and a
    non-idempotent request (a POST that charges a card) could run up to
    four times;
  - **`http-normalize-path` true:** request paths are normalized before
    they reach the application.
- **Mitigation:** Serendip's Cilium values set `http-retry-count: "0"`
  and `http-normalize-path: "false"` (`extraConfig`); with retries off
  the 500s came through (5 of 39). The provider reports drift if either
  is changed back (plan decision 4). Since T004 drift does more than
  report: the visibility pass (`EnsureVisibility`) removes every L7
  policy it owns while either setting differs, so a Cilium upgrade that
  brings the defaults back can't put a retrying proxy in front of a
  Component; request metrics are then unavailable, with the reason.
- **Residual:** Envoy's other defaults still apply to proxied traffic
  only: `http-request-timeout` 3600 s, `http-stream-idle-timeout` 300 s,
  and its connection limits (1024 per cluster). A long-lived stream or
  a very busy Component can behave differently behind the proxy.
- **Revisit:** on every Cilium upgrade (`providers/networking/cilium/VERSIONS.md`),
  and with T013's latency measurement.

## RP-082 — Request visibility copies each Component's NetworkPolicy, and skips what it can't copy

- **Kind:** correctness (coverage); security (isolation). **Status:**
  Accepted, 2026-11-20. **Found:** spec 025 T004.
- **What:** Hubble sees HTTP requests only through a CiliumNetworkPolicy
  with an HTTP rule on the port, and allows are a union, so that policy
  must not admit anyone the platform's NetworkPolicy doesn't. The
  `cilium` provider copies the NetworkPolicy's ingress peers and keeps
  only its HTTP ports (`visibility.go`, `mirror`). It refuses, with a
  reason, any shape it can't copy faithfully: an address-range peer, a
  peer chosen by namespace labels, a rule admitting every source, a
  named port or a port range, an unknown operator or field (decoded
  strictly). Keys carry Cilium's `k8s:` source prefix, so a selector
  can't match labels from another source. OpenChoreo 1.3.1's policies
  (same namespace; the platform's system components in any namespace)
  copy; live on `t025` a refused peer stayed refused.
- **Cost:** a Component whose policy is skipped has no request metrics
  (its flows are still recorded); Facts counts the skips. An HTTP port
  that the platform marks TCP (a Component port that isn't exposed) gets
  no L7 policy at all, because putting the proxy on non-HTTP traffic
  would break it: east-west calls to an internal-only port show
  connection counts only. The other way round, a port the platform marks
  HTTP that carries something else (raw TCP, a protocol upgrade other
  than Envoy handles) breaks behind the proxy; Serendip marks only exposed
  ports HTTP, which the gateway already routes as HTTP.
- **Revisit:** an OpenChoreo upgrade that changes the policy's shape (the
  unit test holds 1.3.1's); a user-authored Network resource (a later
  spec); per-port protocol in the Component model.

## RP-083 — A Cilium bootstrap needs the internet at first boot

- **Kind:** availability (install); footprint (first boot). **Status:**
  Accepted, 2026-11-20. **Found:** spec 025 T005.
- **What:** `serendip-agent bootstrap --cni cilium` writes a K3s
  `HelmChart` naming `https://helm.cilium.io` and chart 1.20.5. K3s's Helm
  controller fetches the chart, and the kubelet pulls Cilium's images from
  quay.io. The node stays NotReady, with nothing else scheduled, until
  Cilium runs. In a `rancher/k3s` container on a home link, with no image
  cache, that took about 7 minutes (CoreDNS got a pod address about a
  minute later). The bootstrap's `--timeout` default went from 5 to 10
  minutes for it.
- **Cost:** no offline (air-gapped) install with Cilium; a slow or
  filtered link fails the bootstrap's wait, though K3s keeps retrying and
  the node goes Ready once the pulls finish. `--cni default` (Flannel,
  bundled in K3s) still installs offline as far as K3s itself does.
- **Why accepted:** K3s itself already comes from `get.k3s.io` and its
  images from Docker Hub; making Cilium alone offline gains nothing until
  the whole install is.
- **Revisit:** an air-gapped install: the chart goes into K3s's static
  charts directory (`/var/lib/rancher/k3s/server/static/charts/`,
  referenced by the HelmChart) and the images into K3s's airgap images
  directory, both shipped by the agent.
- **Not checked:** what `k3s-uninstall.sh` leaves of Cilium on a real host
  (its interfaces, BPF mounts, `/etc/cni/net.d`): only containers were
  used. Check on the reference hardware before calling uninstall clean.

## RP-084 — Connection records are de-duplicated per page, and the collector's config leans on the logs chart

- **Kind:** correctness (counts); upgrade. **Status:** Accepted,
  2026-11-23. **Found:** spec 025 T006 (live on `t025`).
- **What:**
  - **Both ends write a record** of a connection between two cluster
    endpoints (EGRESS at the source, INGRESS at the destination). Reads
    keep the destination's and drop the source's when both are in the
    same page within a minute (`flows.go` `dedupe`). A pair split across
    two pages shows twice.
  - **Through the L7 proxy** (a Component's HTTP port, spec 025's request
    visibility) only the source's EGRESS record exists: Envoy terminates
    the connection, so no INGRESS SYN is seen. The proxy's own upstream
    leg is recorded with the client's identity and no namespace; the Lua
    filter drops it, as the client's record already counts it.
  - **Fluent Bit's config** is the logs chart's render plus Serendip's
    pipeline. Two things depend on the chart's layout at 0.8.0: the chart
    starts Fluent Bit with `--workdir=/fluent-bit/etc`, so its
    `Parsers_File parsers.conf` is the image's file and the flow parser
    goes in its own file, added by absolute path; and Helm replaces the
    `extraVolumes`/`extraVolumeMounts` lists, so the chart's `db` entry is
    restated beside the flow directory (without it the upgrade was
    refused).
- **Volume:** drops and new connections with an end at a Component,
  aggregated per minute; T001 measured about 7.5 MiB a day for three
  Components talking. The stream has the logs' retention (3 days,
  `ZO_COMPACT_DATA_RETENTION_DAYS`) and counts towards the package's
  disk use (spec 024's `packageDisk`). T013 measures it on the bench.
- **Cost:** a page can show a connection twice; a proxied port's counts
  are the client's view only; a chart upgrade that moves either piece
  breaks the flow input (the parsers line is checked at Install, the
  `db` entry isn't).
- **Not signalled by a read:** an empty page doesn't say whether
  records are collected at all (no flow source yet, Fluent Bit down);
  T009's API checks the networking facts' `flows` for that.
- **Why accepted:** a cross-page de-dup needs state the cursor doesn't
  carry, for a rare duplicate; the client's record of a proxied
  connection is the honest one available.
- **Revisit:** on a logs chart upgrade (re-render, compare its default
  `extraVolumes`); if duplicates show in the console, de-dup by the
  minute in SQL.

## RP-085 — Request metrics are as coarse as Hubble's histogram

- **Kind:** correctness (precision); log noise. **Status:** Accepted,
  2026-11-23. **Found:** spec 025 T006.
- **What:**
  - **Percentiles** are interpolated within Hubble's latency buckets
    (5, 10, 25, 50, 100 ms, ...). A server answering in 20 ms plus a
    little shows p50 17.6 ms and p95 24.5 ms: inside the right bucket,
    not exact.
  - **Counts** for `requestErrorRate`'s `minRequests` come from
    `increase()`, which extrapolates to the window's edges, so they're
    not whole numbers.
  - **The `hubble` scrape job** looks up `hubble-metrics.kube-system` by
    DNS every minute. On a cluster without Cilium the name doesn't
    resolve, the job has no targets, and Prometheus logs the failed
    lookup each time.
  - **Requests are counted once,** by the server's side
    (`reporter="server"`): a call to something outside the mesh of L7
    policies (a platform service, the internet) isn't in the traffic
    overlay.
  - **Replaced pods** (spec 025 T012): the request and restart
    conditions now count every pod seen in the window
    (`max_over_time(kube_pod_labels[window])`). Before, only pods alive at
    the evaluation counted, so a rule resolved as soon as a rollout
    replaced the bad pod. A pod that's gone still drops out up to one
    1-minute scrape early, because `increase()` needs two samples.
- **Cost:** latency alerts near a bucket edge fire a little early or
  late; a little log noise on `external` clusters.
- **Why accepted:** Hubble's buckets are its defaults; finer ones cost
  series on every pod pair (the cardinality risk in the plan).
- **Revisit:** at the bench (T013), with the series count; if a latency
  rule is too coarse in practice, set Hubble's `httpV2` buckets.

## RP-086 — Trace identity leans on the store's key flattening; removing traces waits for the store and rolls Components

- **Kind:** security (identity of stored spans); availability (rollouts,
  removal time); performance. **Status:** Accepted, 2026-11-23.
  **Found:** spec 025 T007.
- **What:**
  - **Identity holds because of how OpenObserve names columns.** It
    lowercases a key and turns other characters into `_`, prefixes
    resource attributes with `service_`, and writes a span attribute as a
    column of its own name unless that column is already set (then
    `attr_*`). Seen live: with T001's collector config,
    `OpenChoreo.dev/component-uid` (resource) and
    `service.openchoreo.dev/component-uid` (span) from an unlabelled pod
    both landed on `service_openchoreo_dev_component_uid`. T007's config
    keeps an allow-list of resource keys, deletes `service.*` span
    attributes in any spelling, and drops spans `k8s_attributes` couldn't
    attribute (an unlabelled pod, one too new for its cache, the host
    network) (`testdata/t007-spoof-check.json`). A store upgrade that
    changes the flattening could reopen it.
  - **A stream's deletion is queued, not done:** the store's retention
    job runs it, hourly by default; until then spans written to a
    re-created stream are deleted with the old ones, and searches fail.
    Serendip runs that job every minute (`ZO_COMPACT_DATA_RETENTION_INTERVAL=60`,
    which also applies the logs' retention sooner), and the tracing
    removal waits 90 s after the request. A `serendipd` restart inside
    that wait doesn't wait again (the second request answers 404), so an
    install straight after could lose up to a minute of spans.
  - **Removing tracing rolls traced Components:** the variables join a
    Component's next new release (plan decision 11), but after removal an
    unpinned Deployment's release is recomputed without them on its next
    pass, so each traced Component restarts once. Installing rolls
    nothing.
  - **The observability package's removal takes the collector too:** it
    deletes the namespace both share (T008's catalogue orders them).
  - **Opening a trace scans the retention** (3 days) for its ID: there's
    no time hint in `Trace`.
  - **Listing pages by each trace's first stored span:** a span that
    arrives late with an earlier start moves its trace between pages, so
    it can show twice or be skipped; a trace started before the query's
    start is summarised from its spans since then.
- **Cost:** a regression test is the only guard on the flattening; a
  minute-ly retention job on the store (its cost measured in T013); 90 s
  per tracing removal; a restart per traced Component on removal; an
  open trace's query time grows with the stream (measured in T013).
- **Why accepted:** the flattening is the store's ingestion behaviour,
  and the allow-list leaves almost nothing to spell; the store has no
  status for a stream's deletion to wait on instead; keeping the
  variables on removal would point workloads at a gone endpoint.
- **Revisit:** on every OpenObserve or collector upgrade, resend
  `testdata/t007-spoof-span-otlp.json` from an unlabelled pod and check
  nothing is stored with an identity, and re-time a stream's deletion.
  T013 (simulated T470, ~1.5 h of traces stored): opening a trace took
  163 ms median, a 1-hour list 119 ms. The retention job's cost isn't
  separable from the observability group's (tracing cost: RP-088). Time
  an open again with 3 days stored, on real hardware.

## RP-087 — Flows and traces read the caller's readable Deployments in the Environment, capped at 20

- **Kind:** correctness (naming past the cap); performance. **Status:**
  Accepted, 2026-11-23. **Found:** spec 025 T009.
- **What:**
  - **Flows** name the other end only for a target, a peer or a platform
    service. Peers are the Environment's other Deployments the caller
    may read with observability read, at most 20 (`MaxLogTargets`). Past
    that, a readable end is shown as outside: never a wrong name, but
    less than the caller could see. An end in another Environment is
    always outside, readable or not: for an Owner too. The console says
    "Not shown", and the API and console texts name the three causes
    (T012, which first worded it as "Not visible to you").
  - **Each flows read resolves every peer** (a scope lookup and the
    Component's workloads), even when the page comes back empty, and
    the API checks each Deployment's permission first.
  - **Opening a trace** reads the spans of up to 20 readable Deployments
    in one query pair, each over the whole retention (RP-086). Past 20,
    the rest count as hidden, so the trace says `partial`.
  - **Traffic** refuses an Application with more than 20 Deployments in
    the Environment (422) instead of answering part of it.
- **Cost:** up to 20 extra platform lookups per flows read; a trace's
  two queries grow with the number of scopes in the OR.
- **Why accepted:** 20 Deployments in one Environment is well past
  edge-1's scale, and the cap matches stored logs' (spec 023). Naming
  less is the safe side.
- **Revisit:** at `small-3`, or if an Environment holds more than 20
  Components: resolve peers lazily (only the ends a page has), or page
  the merge. T013 (simulated T470, one peer): a 15-minute flows read
  took 64 ms median and a trace open 163 ms.
- **Spec 025 T014 (go-reviewer):** none of these reads is cached.
  Request metrics are five PromQL range queries a call, and traffic's
  rates are over the whole range asked for (up to the retention). Each
  read is bounded by the caller's query slots and the observability
  read deadline, not by a cache. Cache or coalesce them if the console
  polls them, or if a read past the deadline is seen on the reference
  hardware.

## RP-088 — Traces share the logs store, and their disk use is one slope at one rate

- **Kind:** footprint (disk); availability (logs). **Status:** Accepted,
  2026-11-23. **Found:** spec 025 T013.
- **What:**
  - Traces are kept in the observability package's logs store (plan
    decision 11), for its 3 days, in its 5 GiB volume.
  - At 10 traced requests a second (three spans each), they grew it
    about 730 MiB a day: ~2.2 GiB of the 5 GiB at 3 days, beside about
    1.5 GiB of logs under the bench's log load.
  - The figure is the Cilium run's 1-hour slope with traces (1226 MiB a
    day) less spec 023's 2-hour log-only slope (496 MiB a day): two runs,
    one request rate, and not a steady state (RP-063's caveat).
  - It overstates traces alone: the Cilium run's logs store also took
    Hubble's connection records, and its Prometheus Hubble's request
    series, which spec 023's run had none of (contract-guardian, T013).
  - The tracing package's disk budget names the logs volume, which the
    observability package's budget already counts. Nothing sums budgets
    across packages today. A disk-fit check that does must count that
    volume once.
  - Traces grow with request rate. Twice the traffic would bring logs
    and traces close to the volume's size.
- **Cost:** a busy traced Component can fill the logs store, and logs
  then share the shortfall. The package's built-in disk rule (spec 024)
  warns first.
- **Why accepted:** one store keeps edge-1 to one backend (spec 023's
  choice), and edge-1's traffic is light. The requirements state the
  rate they were measured at.
- **Revisit:** on real hardware (the L470) with a day's run; if traces
  crowd logs out, add head sampling in the collector or a separate
  traces retention.

## RP-089 — Cilium's node cost on edge-1

- **Kind:** footprint. **Status:** Accepted, 2026-11-23. **Found:** spec
  025 T013.
- **What:**
  - On the simulated T470, a node running Cilium (agent, operator,
    Envoy, Hubble export; kube-proxy kept) used +443 MiB and +92 mcores
    more than one running kindnet at platform idle.
  - Under 10 requests a second of HTTP traffic, the difference was
    +461 MiB and +128 mcores, and +496 MiB and +118 mcores with tracing.
  - Cilium's own pods are 236–302 MiB of that. The rest is node overhead
    (BPF maps, the runtime) and kube-apiserver (Cilium's CRDs).
  - The heaviest sample of the whole stack was 3244 MiB, 23% of a
    16 GB machine less 2 GB for the OS.
- **Cost:** about half a GiB and up to 6.5% of a 2-core CPU, always on.
- **Why accepted:** it's what request metrics, flows with drop reasons
  and CNI-enforced policies cost, and the stack still fits a 16 GB T470
  comfortably. So Cilium stays edge-1's default (`serendip-agent
  bootstrap --cni cilium`), and `--cni default` stays for tighter
  machines (ADR 0023).
- **Revisit:**
  - on real hardware (the L470: 2 cores, 8 to 16 GB): if half a GiB is
    too much on an 8 GB machine, recommend `--cni default` for it;
  - if the node is tight, measure kube-proxy-free first (plan decision
    6).

## RP-090 — Request visibility's audit rows wait in memory, and an edited policy is replaced in two steps

- **Kind:** audit (durability); correctness (a metrics gap). **Status:**
  Accepted, 2026-11-23. **Found:** spec 025 T014 (contract-guardian).
- **What:**
  - The visibility pass (`cmd/serendipd/networking.go`) writes or
    deletes CiliumNetworkPolicies first, then their audit row. A row the
    store refuses is kept in memory and retried every pass; past 100 the
    oldest are logged in full and dropped. A crash between a write and
    its row loses the row. At shutdown each try waits 2 s, not 10.
  - A Serendip-owned policy that someone edited (a field Serendip
    doesn't write) is deleted, then applied again, because the client
    has only server-side apply, which can't drop another manager's
    fields. If the apply fails, that Component has no L7 policy until
    the next pass. It's reported in the pass's `Failed`.
- **Cost:** a lost audit row after a crash (the policies themselves are
  re-derived on start from the managed label, so nothing is left
  unowned). One pass without request metrics for one Component. Its
  isolation doesn't change: allows are a union, and the platform's
  NetworkPolicy still applies.
- **Why accepted:** the writes are a system actor's derived state, re-made
  from the platform's NetworkPolicies every pass. They aren't a user's
  change that only the audit row would record. Writing the row first
  would record writes that may then fail.
- **Revisit:** if audit rows must survive a crash for system actors too
  (an intent row before the write); a `PUT` in the Kubernetes client
  (replace in one step).

## RP-091 — Cilium's node metrics ports, closed to pods by one Serendip policy

- **Kind:** security (traffic visibility), medium. **Status:** Mitigated,
  2026-11-23. **Found:** spec 025 T014 (security review).
- **What:** Cilium's agent, operator and Envoy run on the host network
  and serve their metrics on every node's address, with no
  authentication: 9962, 9963, 9964 and Hubble's 9965. Hubble's request
  metrics carry the source and destination pods of every Component's
  calls, with status codes and latency. OpenChoreo's NetworkPolicies are
  ingress-only, so before this any pod could read the whole cluster's
  traffic picture that the flows and traffic reads scope by permission
  (spec criteria 5 and 6).
- **Mitigation:** the `cilium` provider keeps a
  CiliumClusterwideNetworkPolicy, `serendip-node-metrics-guard`
  (`nodeguard.go`), on every visibility pass, even with request
  visibility off. It denies egress to the `host` and `remote-node`
  entities on those four ports from every pod outside `kube-system` and
  `openchoreo-observability-plane` (whose Prometheus scrapes them).
  Builds' pods are covered too. It only denies: `enableDefaultDeny` is
  off, so the pods it selects get no allow-list. Checked live on `t025`:
  - a Component pod and a `default` pod time out on all four ports, and
    still reach the kubelet's 10250;
  - Prometheus's `hubble` job stays `up`.
- **Residual:**
  - The exempt namespaces are names. The observability plane's comes
    from the observability package (`PlaneNamespace`), and a rename there
    must change `metricsReaders`.
  - Anything else in those two namespaces can read the ports.
  - The policy is Serendip's while `serendipd` runs. Someone deleting it,
    or a failed re-apply after an edited guard is deleted, opens the ports
    until the next pass (every interval).
  - Nothing removes the guard if the cluster moves off Cilium. It goes
    with Cilium's CRDs, and until then it's inert.
  - Other host-network endpoints (the kubelet, node exporters) aren't
    part of this guard. Those that scope by caller have their own
    authentication.
- **Revisit:** Hubble metrics over TLS with client certificates
  (`hubble.metrics.tls`), giving Prometheus a certificate, which would
  make the guard unnecessary for 9965; a user-run Prometheus outside the
  exempt namespaces.

## RP-092 — The trace collector takes spans from any pod, with only a memory limit

- **Kind:** availability (logs, traces); footprint (disk). **Status:**
  Accepted, 2026-11-23. **Found:** spec 025 T014 (go-reviewer).
- **What:** the collector's OTLP receivers listen to the whole cluster.
  Identity comes from the connection (RP-086), so a pod can't pass its
  spans off as another Component's. But nothing limits how many it
  sends. `memory_limiter` (160 MiB) protects the collector, not the
  store. One chatty or hostile Component can fill the 5 GiB logs store
  that traces share (RP-088), and its retention job then drops other
  Components' logs and traces early.
- **Why accepted:** at edge-1's scale the Components are the operator's
  own. The observability package reports the store's disk use
  (`usage.go`), so a filling store shows.
- **Revisit:** a `probabilistic_sampler` or per-pod rate limit in the
  collector; a separate traces store or quota when Projects belong to
  different people (multi-tenant).

## RP-093 — Hubble's request metrics grow with pod pairs

- **Kind:** footprint (Prometheus memory and disk). **Status:** Watch,
  2026-11-23. **Found:** spec 025 plan (Risks); recorded at T015.
- **What:** Hubble's `httpV2` metrics are labelled by
  `labelsContext=source_namespace,source_pod,destination_namespace,destination_pod`,
  the labels the reads join to `kube_pod_labels` on (plan decision 4).
  Each pair of talking pods gets its own series, times method, status
  code and, for the latency histogram, every bucket. A rollout adds a
  new set for the new pods while the old ones age out of the head.
- **Measured:** not the series count. T013's observability group, which
  holds Prometheus, grew 17–31 MiB with two Components talking at 10
  requests a second (`profiles/edge-1/bench/results/2026-11-23-spec025-simulated-t470/`);
  the scrape job already keeps only the request, duration, drop and
  flow series (plan decision 7).
- **Why accepted:** at edge-1's scale (a handful of Components, one or
  two pods each) the pairs are few.
- **Revisit:** at `small-3`, or when an Application has more than a few
  dozen talking pods: count `hubble_http_*` head series, and consider
  workload instead of pod labels (the reads would join by the Deployment
  name, as flows do) or fewer histogram buckets (RP-085).

## RP-094 — OpenBao stays sealed after its pod restarts

- **Kind:** availability (secrets, secret variables). **Status:**
  Closed, 2026-11-24, by spec 026 T012 (see "Fixed" below). **Found:**
  spec 026 T009 (criterion 5's capability-down check); older than spec
  026.
- **What:** `serendipd` unseals OpenBao once, in the Secrets setup at
  start (`providers/secrets/openbao/bootstrap.go`). If the OpenBao pod
  restarts later (eviction, OOM, a node restart without `serendipd`'s,
  an upgrade), it comes back sealed and stays that way until `serendipd`
  restarts. Meanwhile value reads and writes fail. A value write answers
  503 "capability not ready". A Deployment whose secret variables change
  can't be given them, so it reports `ProviderNotReady`, and running pods
  keep what they had. Secret variables that don't change aren't affected,
  because values are read only when the version changes.
- **What T009 fixed on the way:** an unreachable or sealed OpenBao used
  to fail as an internal error (500, `SecretReadFailed`). It now counts
  as the capability not being ready (`openbao.ErrUnavailable` maps to
  `ErrProviderNotReady`). `GET /v1/secrets/status` reports `ready:
  false` from OpenBao's health instead of from setup alone.
- **Why it was left open at T009:** re-unsealing on demand uses the
  unseal key `serendipd` already keeps (ADR 0009), but it changes how
  that key is used, so it waited for a decision. The options were to
  re-run the unseal when the health check says sealed, or auto-unseal
  (ADR 0009's later route).
- **Fixed (T012, the user's choice):** the first option.
  - When a Secrets call or the encryption report finds OpenBao sealed,
    the provider reads the stored unseal key, unseals, and retries once
    (`providers/secrets/openbao/unseal.go`).
  - It tries at most once every 10 s, and never initializes an
    uninitialized OpenBao.
  - Live, the Secrets reconciler's own resync unsealed a restarted
    OpenBao within seconds, with no `serendipd` restart.
  - ADR 0009 is amended. What remains is the custody itself (a key file
    on the same disk), ADR 0009's open trigger.

## RP-095 — Deleting a Component leaves its Deployments behind

- **Kind:** behaviour (clean-up). **Status:** Open, 2026-11-24.
  **Found:** spec 026 T009 (criterion 7); older than spec 026.
- **What:** `DELETE /v1/components/{id}` succeeds while Deployments of
  the Component exist, with no 409 and no `cascade`. The platform's own
  clean-up removes the workloads, but the Deployment records stay,
  `Ready=False` with `WaitingForComponent` ("Component … not found"),
  until someone deletes them. An Application delete does ask for
  `cascade=true`.
- **What T009 fixed:** the one leak this caused. The cluster Secret of
  secret variables isn't the platform's, so it was left in the
  Environment's namespace. `DeleteComponent` now deletes it first, in
  every Environment the Component is bound to.
- **Revisit:** with the next spec that touches Deployments: either answer
  409 naming the Deployments (as Secrets do), or delete them by cascade
  as Applications do.

## RP-096 — Secret variables rest in the cluster as plain Secrets

- **Kind:** security. **Status:** Accepted, 2026-11-24. **Found:** spec
  026 plan ("Risks"); the encryption caveats in T005. ADR 0024.
- **What:** to start a container, a secret variable's value has to be in
  the cluster. Serendip writes it into `<component>-secret-env`, a plain
  Kubernetes Secret in the Deployment's data-plane namespace. Anyone
  with `get secrets` there, a controller with Secret read, or a
  cluster-admin kubeconfig can read it. A pod that prints its
  environment puts it in the logs (RP-067). This is the same class as a
  build's credential copy (RP-050), but it lasts as long as the
  Deployment.
- **Encryption at rest:** on new agent K3s installs (`--secrets-encryption`,
  AES-CBC). Its limits:
  - the key is in `/var/lib/rancher/k3s/server/cred/`, on the same disk,
    so it protects a copied datastore, not a stolen disk;
  - `on` means the node's `k3s.io/encryption-config-hash` annotation is
    there: encryption was configured when K3s started. It doesn't prove
    that Secrets written earlier were re-encrypted;
  - whether `k3s secrets-encrypt disable` removes the annotation isn't
    checked, so `on` might outlive a disable;
  - the steps to turn it on for an existing install (`operations.md`)
    haven't been run against a Serendip install yet;
  - kind and external clusters read `unknown`.
- **What holds:** the value is in no Serendip store, audit row, API
  answer (other than the value route), condition, log line or OpenChoreo
  object (T009 searched for it). Naming a Secret needs its value-read
  permission (ADR 0024 decision 2).
- **Revisit:** in spec 036 (workload identity, #382, a v1 GA epic run after
  small-3). It adds an opt-in file mount through OpenBao's CSI provider
  and fetches by the application itself, both of which keep values out
  of the cluster store, as AKS's Key Vault CSI driver and workload
  identity do. Environment variables stay as built, because they can't
  avoid a cluster Secret (AKS's CSI `secretObjects` sync makes one too).
  On the L470, run the enable steps and check the annotation after a
  disable.

## RP-097 — Secret references aren't part of a release

- **Kind:** behaviour. **Status:** Decided, 2026-11-24. **Found:** spec
  026 plan decision 5.
- **What:** `spec.secretEnv` and `overrides.secretEnv` are read from the
  current Component and Deployment at reconcile time, not from the
  `ComponentRelease`. A rollback, or a promotion of an older release,
  keeps today's references. A change to a Component's references
  applies in every Environment it runs in at once (overrides aside).
- **Why decided:** a reference names no value, and a rollback that
  brought back an old password reference would surprise people more
  than one that didn't. Per-Environment differences belong in overrides.
- **Revisit:** if someone needs a reference change staged through the
  pipeline like an image change.

## RP-098 — A rotation rolls every Deployment that uses the Secret

- **Kind:** availability. **Status:** Accepted, 2026-11-24. **Found:**
  spec 026 plan ("Risks").
- **What:** writing a new value changes the version of every Deployment
  that names the Secret, and each one rolls its pods. A Deployment has
  one version for all its Secrets, so rotating any one of them rolls it.
  A single-replica Deployment is briefly down if its new pod isn't ready
  before the old one stops, and on a full node the surge pod can stay
  Pending.
- **Why accepted:** environment variables are fixed at container start,
  so a running pod can't be given a new value. A rolling restart is the
  standard answer.
- **Revisit:** if a value should reach pods without a restart (a
  mounted file, which the application must re-read), or if rotations get
  frequent enough to batch.

## RP-099 — The upgrade to spec 026 rolls every Deployment once; a downgrade drops secret variables

- **Kind:** availability. **Status:** Accepted, 2026-11-24. **Found:**
  spec 026 T001 (a release pins its type), confirmed in T009; the
  downgrade in T010.
- **What:**
  - **Upgrade:** the component type gains the `envFrom` entry and the
    annotation, and the type is part of the release hash. So every
    Component gets a new release, and every Deployment following the
    latest rolls once. A Deployment pinned to an older release doesn't
    roll, and reports `NeedsNewRelease` if it gains secret variables.
  - **Downgrade** to a pre-026 `serendipd`: the type loses
    `serendipSecretEnv`, so Components get new releases without the
    `envFrom` entry. Their pods roll without the secret variables until
    the upgrade comes back, and the cluster Secrets stay behind.
- **Why accepted:** a rolling restart once per upgrade, as with any
  change to the type (spec 012's route did the same). Downgrades aren't
  supported before v1 GA.
- **Revisit:** at v1 GA, with releases and upgrade notes.

## RP-100 — Values read to deliver secret variables aren't audited

- **Kind:** audit. **Status:** Accepted, 2026-11-24. **Found:** spec 026
  spec (out of scope); `secrets.md`'s value-read deferral.
- **What:** `DeploymentReconciler` reads a value whenever a version
  changes, and no audit row records it, as with every value read.
  Naming the Secret is audited: the Component or Deployment write, and a
  denied value-read check.
- **Revisit:** with the append-only read audit `secrets.md` defers.

## RP-101 — The costs and edges of delivering secret variables

- **Kind:** performance; correctness; security (memory). **Status:**
  Accepted, 2026-11-24. **Found:** spec 026 T003, T004.
- **What:**
  - **Two cluster GETs per Deployment with secret variables, every
    pass:** the ProjectReleaseBinding for the namespace, and the Secret
    for its version. Deployments without secret variables cost nothing
    new. T010 measured no change in `serendipd`'s idle memory.
  - **An apply 404 reads as pending** (`SecretEnvPending`), because a
    first deploy's namespace may not exist yet. If the path were ever
    wrong, the Deployment would read pending for good rather than fail.
    The resync bounds the retry cost.
  - **Clearing value buffers is best effort.** Values are cleared after
    use where Serendip holds the bytes, but Go strings, JSON encoding and
    the HTTP client make copies it can't clear.
- **Revisit:** if many Deployments use secret variables (cache the
  namespace, or watch the Secrets), or if `SecretEnvPending` lasts long
  on a real install.

## RP-102 — A secret variable may share a name with a platform-injected variable

- **Kind:** correctness. **Status:** Accepted, 2026-11-24. **Found:**
  spec 026 T002.
- **What:** secret variables are refused when they clash with plain or
  dependency variables, but not with the ones the provider injects:
  spec 025's `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_SERVICE_NAME`. The
  provider leaves those out only when the Component sets them in plain
  `env`. With both present, the Secret's `envFrom` comes after the
  variables' ConfigMap, so by Kubernetes' rule (the last source wins)
  the secret variable should win. Not checked live.
- **Why accepted:** naming a trace variable after a Secret is unlikely,
  and the outcome is the user's own value.
- **Revisit:** if the provider injects more variables. Then have
  `withTraceEnv` skip names in `secretEnv` too, or refuse them.

## RP-103 — The encryption status is read at `/`

- **Kind:** usability. **Status:** Decided, 2026-11-24. **Found:** spec
  026 T006 (plan decision 9).
- **What:** `GET /v1/secrets/status` needs `serendip/secrets/read` at
  `/`, because it describes the whole cluster. A user whose roles are
  all at a Project gets 403, and their console's Secrets page shows no
  encryption banner.
- **Revisit:** if Project-only users need it. The answer holds no
  Project data, so it could be open to any signed-in user.

## RP-104 — A template's "latest" version is the newest created, not the highest

- **Kind:** usability. **Status:** Accepted, 2026-11-28. **Found:** spec
  027 plan (decision 1).
- **What:** version labels are free text and aren't sorted as semver, so
  publishing `1.1` after `2.0` makes `1.1` the latest. Azure has no
  latest at all and makes you name a version.
- **Mitigation:** the console shows each label's creation date.
- **Revisit:** if people publish fixes to old lines, with an explicit
  "latest" pointer on the Template.

## RP-105 — `createdFrom` sits in the spec of user-writable kinds

- **Kind:** security. **Status:** Accepted, 2026-11-28. **Found:** spec
  027 plan (decision 9).
- **What:** provenance is a field on `ApplicationSpec` and
  `ComponentSpec`, set only by the template path. The Application and
  Component handlers refuse it on create and refuse a change on update.
  A future generic write path that skipped them could forge it.
- **Revisit:** if a new write path for these kinds appears; a
  store-level check is the backstop.

## RP-106 — Built-in templates are readable by anyone signed in

- **Kind:** security. **Status:** Accepted, 2026-11-28. **Found:** spec
  027 plan (decision 5).
- **What:** like built-in role definitions, the built-ins are
  `authOnly`, so an agent or user with no role assignment can read them.
  They hold no data, and using one still needs every permission the
  created resources need.
- **Revisit:** if a built-in ever carries install-specific data.

## RP-107 — Resources made from a template don't follow it

- **Kind:** usability. **Status:** Accepted, 2026-11-28. **Found:** spec
  027 plan (spec Out).
- **What:** creation copies the rendered body; a later version or fix
  doesn't reach Applications made earlier, though users may expect it
  to.
- **Mitigation:** the console says so on the create screen, and
  `createdFrom` names the version used.
- **Revisit:** with a re-apply and drift report, if asked for.

## RP-108 — A template create holds SQLite's write lock for the whole batch

- **Kind:** performance. **Status:** Accepted, 2026-11-28 (measured in
  spec 027 T004). **Found:** spec 027 plan (decision 8).
- **What:** up to 20 Components plus their Deployments go in one
  transaction, holding the write lock and the workload lock for the
  batch.
- **Measured:** `BenchmarkAtomically_20Components` (an Application, 20
  Components and 20 Deployments, 41 creates with their edges and audit
  rows) takes **13–14 ms** on the L480 (i5-8250U) and 6 ms on the
  desktop (Ryzen 7 7840HS). That's under the 50 ms limit, so the
  20-Component limit stays.
- **Revisit:** if a template may create more than 20 Components, or the
  benchmark goes over 50 ms on the lowest-spec node.

## RP-109 — An image's form is checked by the reconciler, not yet at write

- **Kind:** risk. **Status:** Open, 2026-11-28. **Found:** spec 027 T003
  (contract-guardian).
- **What:** `resource.ValidateComponentImage` (letters, digits and
  `._/:@+-[]`, at most 512) runs in the Component reconciler and in
  template rendering, but the API's Component create and update don't
  call it yet. A hand-made Component with a malformed image is accepted
  and then shows `InvalidSpec`. A stored Component whose image is outside
  that set (none expected: it covers the OCI reference grammar's
  characters) turns `InvalidSpec` on its next reconcile instead of
  failing on the platform.
- **Revisit:** spec 027 T005/T006, which reuse the pure validators at
  write; close this then.
