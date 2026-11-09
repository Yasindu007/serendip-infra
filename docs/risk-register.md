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

## RP-034 — The console can't state a package's measured cost yet; follows reconnect; the Deploy tab lists every Deployment

- **Kind:** honesty and performance. **Status:** Accepted, 2026-10-23.
  **Found:** spec 020 T012.
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

## RP-037 — No provider declares its resource requirements in code

- **Kind:** honesty and documentation. **Status:** Accepted, 2026-10-23.
  **Found:** spec 020 T016 contract review.
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
