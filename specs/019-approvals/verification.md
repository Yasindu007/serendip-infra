# Spec 019 — verification record (T011)

Observed directly on 2026-10-20 unless marked otherwise. The acceptance
criteria the unit and end-to-end tests already cover are listed at the end.

## Setup

- **Serendip:**
  - **Cluster:** kind `serendip-t018` (kindest/node v1.34.0) with the spec
    017/018 stack: storage, OpenBao, kgateway, OpenChoreo, and ThunderID.
  - **`serendipd`:** built from `784e326` with the console embedded, on
    `:8088` (Agent Manager holds 8080), with a fresh store and one API key,
    `verifier`. The key was Owner at `/`, plus Secrets Officer to write
    test values.
  - **OpenBao credentials:** this cluster's OpenBao was initialised by spec
    018 T001. The matching `openbao-init.json` was found by its timestamp
    (the `openbao-data` PVC was created 2026-10-18 12:22:56Z, the file was
    written at 17:53 local) and passed with
    `SERENDIP_OPENBAO_CREDENTIALS_PATH`. OpenBao unsealed with it.
  - **Agent issuer:**
    `SERENDIP_AGENT_ISSUERS=am-dev=http://default-idp.amp.localhost:8080|https://serendip.local/api`.
- **Agent Manager:** `amp/v1.0.0`'s k3d cluster `amp-local`, restarted
  from spec 018.
  - **The agent:** `serendip-ext` (AgentID
    `01a14f26-cb2a-753e-89da-54e4993b7ad5`). Its credential was
    regenerated through Agent Manager's API (`POST …/agents/serendip-ext/identities`
    with `{"environment": "default"}`), with the chart-default
    `amp-api-client` and the scopes `amp:agent:*` and `amp:agent-identity:*`
    by exact name.
  - **Its token:** `iss` http://default-idp.amp.localhost:8080, `sub` the
    AgentID, `aud` https://serendip.local/api, `grant_type`
    client_credentials, 3600 s.
  - **In Serendip:** Contributor on the test Project, sponsored by the
    ThunderID administrator.
- **Data:** an Organization, a Project, an Environment, and three
  Applications:
  - `t019-app` with three Components and Secrets;
  - `t019-cascade` with a Component and a Secret;
  - `t019-stale` with a Component.
- **Drivers (scratch, session-local):**
  - a stdlib Python API driver;
  - a stdlib headless-Chrome driver over the DevTools protocol, signing in
    through ThunderID's real login page as `admin`;
  - an OpenBao probe that runs `bao kv metadata get` in the `openbao-0`
    pod and prints only "present" or "absent". It was checked against
    three written values (all present) and a path never written (absent).

  No token, password or secret value was printed.

## Results

| # | Check | Result |
|---|---|---|
| 1 | Secret DELETE erases its value | **PASS** |
| 2 | Cascade erases a Secret's value and tears down the workload | **PASS** |
| 3 | OpenBao down during a delete, across a `serendipd` restart | **PASS** |
| 4 | An agent's delete waits for a person | **PASS** |
| 5 | A person approves the agent's request in the console | **PASS** |
| 6 | Reject | **PASS** |
| 7 | Stale target | **PASS** |
| 8 | Audit trail of an agent's approved delete | **PASS** |
| 9 | Console: delete a Component and a Secret, Approvals page | **PASS** |
| 10 | The edge-1 harness, through the new delete path, on the simulated T470 | **PASS** (re-run) |

### 1. Secret DELETE erases its value (criterion 15)

- **Before:** OpenBao `present`.
- **The request:** `DELETE /v1/secrets/{id}` returned 204 with an
  `Serendip-Approval-Id`.
- **The Approval:** `Executed`, `settled`, decided by policy.
  - `Executed` True (Deleted), `CleanedUp` True (Done) and `Verified` True
    (Gone).
  - The Secret ref is `cleanedUp`.
- **After:** OpenBao `absent`, and the record is 404.

### 2. Cascade (criteria 8, 15)

- **The request:** `DELETE /v1/applications/{t019-cascade}?cascade=true`
  returned 204.
- **The Approval:** it removed `Application t019-cascade`, `Component
  t019-cascade-web` and `Secret t019-secret-cascade`, with the Secret
  `cleanedUp`, then reached `Verified` and `settled`.
- **After:**
  - The Secret's value went from present to absent in OpenBao.
  - The Component's workload pod (`cmp-<sha256(id)[:10]>`) was gone 4 s
    later.

### 3. OpenBao outage and restart (criterion 15)

1. **OpenBao down:** `kubectl scale statefulset openbao --replicas=0`.
2. **Then the delete:** `DELETE /v1/secrets/{t019-secret-outage}` returned
   204, and the record is 404 at once.
   - The Approval stayed `Executed`, **not settled, not verified**, with
     `CleanedUp` False, Pending, "1 left to clean up; retrying". No provider
     error text was shown.
3. **`serendipd` stopped,** OpenBao scaled back to 1, and `serendipd`
   started again. Its start-up unseals OpenBao.
4. **The Approval resumed from the record:** `CleanedUp` True, then
   `Verified` True, `settled`. OpenBao: `absent`.

### 4. An agent's delete waits for a person (criteria 3, 5, 9)

- **The agent's DELETE:** `DELETE /v1/components/{t019-web-agent}` as the
  agent returned **403**
  `urn:serendip:problem:needs-approval`.
  - The detail names `POST /v1/approvals`.
  - There's no `Serendip-Approval-Id`.
- **Its request:**
  - `POST /v1/approvals` without a reason returned **400** ("reason:
    required, since this request waits for a person").
  - With a reason it returned **201 Pending**: the sponsor is the
    administrator, it expires 24 h later, and the blast radius is
    `t019-web-agent`.
  - The same request again returned **200** with the same Approval.
- **Approving its own request:** **403**, "only a person or an API key
  may approve or reject".
- **The workload** was still running.

### 5. Approved in the console (criteria 4, 12, 13)

- **The rail:** "Approvals (3 waiting)".
- **The list row:** "Delete component t019-web-agent", the agent with
  "Sponsor: 01900000-…0030", the reason, and "Waiting for a decision".
- **Approve:** it was enabled for `admin`. The confirmation took a comment
  and showed
  `POST /v1/approvals/{id}/approve` with `{"comment": "t019: checked with the team"}`.
- **After:** the status reached **Done**, "Decided by" `admin`, and the
  comment is shown. The workload pod count fell to 0.

### 6. Reject (criterion 6)

- **The rejection:** `POST /v1/approvals/{id}/reject` returned 200,
  `Rejected`, `settled`.
- **Approving afterwards:** **409**, "the approval is Rejected; only a
  pending approval can be decided".
- **The target:** the Component is still there (200), and its pod is
  still running.

### 7. Stale target (criterion 7)

- **The request:** the agent requested a cascade delete of `t019-stale`.
  The Application was then changed (`PUT`, version 5 to 6), and a person
  approved.
- **The outcome:** `Failed`, `settled`, `Executed` False, `StaleTarget`,
  "Application … is at version 6, the approval reviewed 5".
- **The targets:** the Application and its Component are still there,
  and the pod is still running.

### 8. The audit trail (criterion 10)

`audit_log` rows for the agent's approved request and its target, in
order:

| Actor | Row | `policy_decision` |
|---|---|---|
| `agent:am-dev/01a14f26-…` | Component (refused DELETE) | `needsApproval: serendip/components/delete at Component/…, allowed by Contributor…` |
| `agent:am-dev/01a14f26-…` | Approval (the request) | `needsApproval: …` |
| `agent:am-dev/01a14f26-…` | Approval (refused self-approval) | `deny: policy: forbidden: only a person or an API key may approve or reject` |
| `person:admin` | Approval (the decision) | `allow: Owner (…) at /` |
| `system:reconciler` | Component (the delete) | `approved: <approval> by person:admin; requested by agent:am-dev/01a14f26-…` |
| `system:reconciler` | Approval (executed, then verified) | `approved: …`, then none |

### 9. Console (criterion 12; T010's browser walk-through)

Signed in through ThunderID as `admin`.

- **Component page:** Delete component was enabled. The confirmation
  listed `Component | t019-web-person | <id>` and showed
  `DELETE /v1/components/<id>`, exactly.
  - Confirming opened `/approvals/<id>`, which reached **Done**, decided
    by "Policy: the requester's roles allow it", listing what it removed.
  - The API showed the Approval `Executed` and `settled`, requested by
    `person:admin`. The record was 404 and the pod count 0.
- **Secrets page:** the row read `t019-secret-console | t019/console |
  t019-app | Value stored`.
  - The detail page's Delete said "value is erased from the secrets
    store shortly after the delete".
  - The Approval page reached **Done**, and OpenBao: `absent`.
  - **The browser made no request to any `/value` path** (Resource
    Timing).
- **Rail:** Overview, Applications, Secrets, Approvals (with the waiting
  count), and "Volumes (not available yet)".

### 10. Harness (simulated T470)

- **First attempt:** interrupted. The host slept or was starved during
  the workload step (a 2 h 17 m gap), then Claude Code stopped the
  harness because the host was low on memory, before `result.json`. No
  verdict was drawn from it.
- **The re-run:** on a fresh `bench-t470` cluster, with only that
  cluster running (`serendip-t018` and Agent Manager stopped), and
  Windows kept awake for the run. Results:
  `profiles/edge-1/bench/results/2026-10-20-spec019-simulated-t470/run1/`
  (the summary opens with "SIMULATION").

| Measure | Result |
|---|---|
| Whole edge-1 stack at idle | **1.29 GiB** node total, 238 mcores mean |
| `serendipd` | 27.4 MiB RSS, 0% CPU: **rule 4 PASS** |
| `serendipd` with 3 console tabs polling | 29.3 MiB, 0.08% CPU, 0 errors: **PASS** |
| With 3 light Components | 1.47 GiB node total |
| 16 GB headroom | 1398 MiB projected (10% of the budget): **fits comfortably** |
| Cold start / warm restart / Component to Serving | 300 s / 33 s / 32 s each |
| Compared with spec 018 (`2026-10-19-spec018-reuse`) | every group within tolerance: **PASS** (`compare-vs-spec018.md`) |

- **Delete path:** the harness deleted its workload through the new
  path: a policy-approved DELETE, recorded as an Approval.
- **Agent Manager** isn't part of edge-1, the lowest profile, which must
  fit a T470. It runs beside Serendip, idling at about 5 GiB (spec 018).

## Findings (added to `docs/risk-register.md`)

- **RP-014:** writing a value to a brand-new Secret answered **409**,
  though the value *was* written to OpenBao. `SecretReconciler.WriteValue`
  writes the value, then updates the Secret's status, and that update
  lost a race with the reconciler's first status update. This predates
  spec 019 (spec 009); retrying is harmless.
- **RP-015:** the console shows an agent's sponsor as the person's subject
  ID, not their name.
- **Environment (PROGRESS leftovers):** starting Agent Manager's k3d
  containers by hand (`docker start`) gave the server a new IP, and K3s
  crash-looped ("failed to find interface with specified node ip"). The
  fix was to give the containers back their original addresses. `k3d` isn't
  installed on the host; the quick start runs it from its own container.

## Covered by tests, not repeated here

- **Criteria 1, 2, 9 and 11** (the policy approval on every DELETE route;
  Readers refused; duplicates; reading by target or surviving owner):
  `api/approvals_delete_test.go`, `api/approval_handler_test.go`.
- **Criterion 16** (a delete without an approval is refused):
  `core/store/sqlite/approval_test.go`.
- **Expiry** (criterion 6, with a fake clock): `core/reconcile/approval_test.go`.
- **End to end with the engine** (T008): `api/approval_acceptance_test.go`.
