# Approvals

The record of every destructive action. Spec 019 (`specs/019-approvals/`),
ADR 0016. Constitution rule 5 requires an explicit approval object for
destructive operations, even from humans. Serendip follows ADR 0014's
direction: **roles decide; an approval is the exception.**

- Every delete leaves an `Approval`.
- When the actor's roles Allow the delete, **policy approves it
  instantly**, and the caller sees an ordinary `DELETE`.
- Only a `NeedsApproval` decision waits for a person. Today that is an
  agent's delete (`authorization.md`, Agents).
- A principal whose roles deny the action can't request it.

The only verb is `delete`. The field is built to take scale-to-zero,
drain and volume operations later.

## The `Approval` kind

An operational kind (`resource-model.md`), stored as an ordinary resource
row in the one store, so it gets audit rows, events and `resourceVersion`.
It has no `ownerRefs`, so a cascade never deletes its own approval, and it
is not in `resourcePaths`, so the generic `PUT` and `DELETE` routes don't
exist for it. A decided approval can't change and none can be deleted
through the API.

**Spec (what the caller asks):**
- `action`: `verb` (`delete`), `target` `{kind, id}`, `cascade`.
- `reason`: free text, at most 2 KiB. Required when the request waits for a
  person; optional when policy approves it.

**Status (recorded by the server, never taken from a client):**
- `phase`, `conditions`.
- `requestedBy` (actor, principal, subject and, for an agent, sponsor),
  `requestedAt`, `expiresAt` (only while waiting).
- `requestDecision`: the policy decision at request time (`allow: …` or
  `needsApproval: …`).
- `targetName`, `targetVersion` and `ownerChain` as of the request.
- `blastRadius`: every resource removed (the target first, then a
  cascade's descendants) with kind, ID and name. Before execution it is
  what would go; after, what did go. Each ref also keeps its last spec and
  a `cleanedUp` flag for clean-up. **The last spec is never returned by the
  API.**
- `decision`: by policy, or by whom; when; an optional comment (at most
  1 KiB); the evidence.
- `executedAt`, and `settled` (see Indexes).

### Phases

`Pending`, `Approved`, `Rejected`, `Expired`, `Executed`, `Failed`.

```
Pending ──► Approved ──► Executed
   │            └──────► Failed
   ├──► Rejected
   └──► Expired
```

- One function, `resource.ApprovalTransition`, checks every move, so a
  decided approval can't be moved back or decided again.
- **Terminal:** `Rejected`, `Expired`, `Executed`, `Failed`. An `Executed`
  approval still gets its clean-up and verification (below).
- **Settled** means nothing more will happen: rejected, expired or failed,
  or executed and verified.
- A pending approval expires 24 hours after the request
  (`resource.ApprovalTTL`).

### Conditions

| Type | Status and reason | Set by |
|---|---|---|
| `Decided` | False, `AwaitingDecision` | the request, when it waits for a person |
| | True, `ApprovedByPolicy` | the request, or a direct `DELETE` |
| | True, `ApprovedByPerson` | an approve |
| | True, `Rejected` | a reject |
| | True, `Expired` | the reconciler |
| `Executed` | True, `Deleted` | the store, in the delete's transaction |
| | False, `StaleTarget` | the reconciler: the target changed or is gone |
| | False, `DeleteFailed` | the reconciler: not executable (an unsupported verb, an illegitimate decision, or a record the store refuses), or a retryable failure ("will be retried") |
| `CleanedUp` | True, `NothingToClean` | no clean-up applies to what was removed |
| | True, `Done` | every clean-up is done |
| | False, `Pending` | "N left to clean up; retrying" |
| | False, `ProviderNotReady` | the capability isn't ready; waiting |
| `Verified` | True, `Gone` | every removed record is gone from the store |
| | False, `StillPresent` | something removed is still there (it shouldn't be); names it |

Provider error text is logged, never stored on the approval.

## Two ways in, one record

### `DELETE /v1/{kind}/{id}`

Works as before for anyone allowed. This covers every DELETE route:
generic kinds, Secrets, role assignments and custom role definitions.

1. The authorizer (`api/authz.go`) checks the delete, and with
   `?cascade=true` every descendant by its own kind.
2. On Allow it builds the Approval (requested by the caller, decided by
   policy, with the evidence) and puts it on the context as the *approval
   intent* (`store.WithApproval`).
3. `sqlite.Store.Delete`, in one transaction, deletes (cascade included),
   then writes the Approval as `Executed` listing every row it removed with
   its last spec, and its audit row.
4. The 2xx answer carries `Serendip-Approval-Id`. A failed delete (404,
   409) rolls the Approval back and sends no header.

- **A cascade is pinned to what was checked.** The intent carries the
  checked scopes as the expected blast radius. A child added in between
  makes the store answer 409 (`stale target`) rather than delete what was
  never decided.
- **No approval, no delete.** A `Delete` with no intent returns
  `store.ErrNoApproval` and changes nothing. There is no bypass. An
  intent that doesn't name exactly this delete, or an existing approval
  without its reviewed version and blast radius, is refused the same way.
  Reaching the API, that is a wiring bug and answers 500 with no internal
  text.
- **Start-up** revokes assignments held by API keys no longer in
  configuration through the same store delete, with an intent decided by
  the server ("API key … removed from configuration").
- **An agent's `DELETE`** is 403 `urn:serendip:problem:needs-approval`.
  The detail says to `POST /v1/approvals` with
  `{"action": {"verb": "delete", "target": {"kind", "id"}, "cascade"}, "reason": "..."}`.
  Nothing is recorded but the audit row.

### `POST /v1/approvals`

Requests an action explicitly, decided by the same checks as the
`DELETE` it stands for.

- **Allow** on every check: 201, `Approved` by policy. The reconciler
  executes it.
- **NeedsApproval** on any check: 201, `Pending`, expiring in 24 hours. A
  `reason` is required (400 without one).
- **Deny:** 403, with only the audit row.
- **Duplicates:** if a `Pending` or `Approved` (not yet executed) approval
  exists for the same action, it is returned (200), whoever asks. At most
  one is open per action. The check runs under a handler mutex, enough for
  one `serendipd` per store.
- **Validation:** only kinds the generic API serves can be requested (400
  otherwise; access management is deleted with `DELETE`). The target must
  exist (404).
- The handler snapshots the target's name and version, its owner chain
  and its blast radius (the target, and with cascade its descendants from
  the graph).

## Deciding

`POST /v1/approvals/{id}/approve` and `…/reject`, with an optional
`{"comment"}`.

**Who may decide:**
- A person or an API key. Never an agent or a `system:` actor
  (`policy.ErrApproverGuardrail`, 403 "approver guardrail").
- Never the requester (`policy.ErrSelfApproval`, 403 "separation of
  duties"). An agent's sponsor may decide its agent's request (RP-007).
- Only someone whose roles **Allow the delete themselves**, at decision
  time: at the target, and with cascade at every descendant, by its own
  kind. The same applies to anything in the recorded blast radius that
  still exists. An approver can approve only what they could have done
  directly. No new action or role exists.
- A refusal is 403 and is audited. These checks run before the
  permission checks, so an agent is refused as an agent, not as missing a
  permission.

**Outcomes:**
- Not `Pending`: 409. A concurrent decision loses on `resourceVersion`
  (409).
- Past `expiresAt` but not yet marked `Expired` by the reconciler: 409.
- Approve: phase `Approved`, the reconciler picks it up. Reject:
  `Rejected`, settled.
- The decision records the decider, the time, the comment and the policy
  evidence, and clears `expiresAt`.

## Execution, clean-up and verification

The `ApprovalReconciler` (`core/reconcile/approval.go`) is the only thing
that executes an approved request (constitution 3). It acts as
`system:reconciler`; who asked and who approved are in the delete's
`policy_decision` and in the Approval.

| Phase | The reconciler |
|---|---|
| `Pending` | Past `expiresAt`: `Expired`. Otherwise requeue for the time left. |
| `Approved` | Re-checks that the decision was legitimate (policy, or a person or API key that isn't the requester), then calls the kind's store delete with the intent. |
| `Executed`, not `Verified` | Cleans up, then verifies. |
| `Rejected`, `Expired`, `Failed`, `Executed` and `Verified` | Nothing. |

**Stale checks.** The store compares, inside the delete's transaction and
before removing anything, the target's `resourceVersion` and the cascade's
actual blast radius with what was reviewed. A mismatch (`ErrStale`), a
target already gone (`ErrNotFound`) or a target that now has children
(`ErrHasChildren`) ends the approval `Failed`, reason `StaleTarget`, with
nothing deleted. Nobody deletes what nobody reviewed. Any other error is
retried by the engine, and the approval says so once.

**Clean-up from the record.** For each removed ref whose kind has a
clean-up and that isn't `cleanedUp`, the reconciler runs it with the
ref's last spec and marks the ref (one update per ref, so progress
survives a crash). Today the only clean-up is a Secret's: erase its value
through the Secrets capability (`secrets.md`). It must be idempotent,
since a crash between the erase and the mark repeats it. A failure leaves
`CleanedUp` False and is retried; with the capability not ready, it waits.

**Verification.** Once every clean-up is done, the reconciler checks that
every ref is gone from the store and sets `Verified` True and the approval
`settled`. Its claim covers records and approval-driven clean-up only.
Workload teardown is still reported by each kind's own reconciler.

## Reading approvals

- `GET /v1/approvals/{id}`: needs **read on the target's kind at the
  first scope that still exists** in `[target, recorded owner chain…]`,
  else at `/`. So reading keeps working after the target is gone. A
  missing approval is decided at `/` (`serendip/approvals/read`).
- `GET /v1/approvals`: newest first, filtered by `?phase=`,
  `?targetKind=`, `?targetId=` and `?requester=` (an audit actor), then
  kept only if the caller may read each item.
- **Paging:** `?limit=` (default 100, 1 to 500) and `?after=` (the last ID
  of the previous page). A full page names the next cursor in the header
  `Serendip-Next-After`. A page is cut before the read check, so it can be
  short or empty; follow the cursor (RP-006, RP-017).

## Routes and authorization

| Route | Decided as |
|---|---|
| `POST /v1/approvals` | the delete it asks for: delete at the target, and with cascade at each descendant. `NeedsApproval` is let through for this route only. |
| `GET /v1/approvals` | each item, by its read scope (above) |
| `GET /v1/approvals/{id}` | read at the first surviving scope (above) |
| `POST /v1/approvals/{id}/approve` | `MayDecide`, then the delete's checks, as of now |
| `POST /v1/approvals/{id}/reject` | the same |

When the approval doesn't exist, `serendip/approvals/read` or
`/write` at `/` applies, so a caller can't tell a missing approval from a
forbidden one. The route-table test covers all five routes.

## Audit

Every step has an audit row with its actor (constitution 8). The Approval's
own snapshots are kept in the log, so archiving old approvals later loses
nothing.

| Row | `policy_decision` |
|---|---|
| A direct `DELETE` (the target and the Approval, `Executed`) | `approved: <approval> by policy (allow: <role> (<assignment>) at <scope>)`, the evidence joined with `; ` for a cascade |
| The request (`POST /v1/approvals`) | `allow: …` or `needsApproval: <action> at <scope>, allowed by <role> (<assignment>) at <scope>: …` |
| A refused `DELETE` or request | `deny: missing <action> at <scope>`, or `needsApproval: …` |
| A refused decision | `deny: policy: forbidden: …` |
| A decision | the decider's `allow: …` evidence |
| The delete an approved request runs, and the Approval's `Executed` write | `approved: <approval> by <approver or policy>; requested by <requester>`, actor `system:reconciler` |
| The later clean-up and `Verified` writes | none, actor `system:reconciler` |

## Indexes and migrations

Approvals only grow, so no read scans them all (RP-003).

- **`0006`** adds partial indexes for `kind = 'Approval'` only: the phase,
  the target ID and unsettled approvals, as expression indexes that
  `sqlite.ListApprovals` repeats exactly. It also marks approvals recorded
  before `status.settled` existed (RP-016). Query plans are pinned by a
  test.
- **`0007`** replaces 0006's all-kinds newest-first index with an
  approvals-only one, `(created_at, id)`, since the first taxed every
  write (RP-012). A committed migration is never edited (RP-013).
- `ListApprovals` serves the list and the duplicate check. The
  reconciler's resync lists only unsettled approvals.

## The console

- **Delete** on the Application, Component and Secret pages, shown when
  `GET /v1/authorization/permissions` allows it. The dialog lists the
  blast radius from the graph API, says a Secret's value is erased, shows
  the API request it sends, and afterwards links to the approval. It uses
  the ordinary `DELETE`.
- **Secrets** (`/secrets`, `/secrets/:id`): the Secrets the caller may
  read, with name, path, owner and status, filterable by owner. Values are
  never shown or edited; that is a separate data permission and its own
  spec.
- **Approvals** (`/approvals`, `/approvals/:id`): pending requests first,
  then recent decided ones with "Show older". The navigation entry says
  how many are waiting. A request shows its action, target, requester,
  an agent's sponsor, reason, time left, blast radius and conditions.
  Approve and Reject, with an optional comment behind a confirmation, are
  offered to someone who may delete the target and isn't the requester;
  the server decides anyway. Decided requests show who decided (a person or
  policy), the outcome and clean-up progress.

## Known limits

In `docs/risk-register.md`:
- RP-001, RP-002: every write syncs to disk; a DELETE costs about +0.6 ms
  mean and +3 ms p99 more.
- RP-003: approvals are never deleted (retention is a later spec).
- RP-005: a Secret's value is erased after its record.
- RP-006, RP-017: pages can come back short, and the cursor can reveal IDs
  the caller can't read.
- RP-007: an agent's sponsor can approve its requests.
- RP-015: the console shows a sponsor as a subject ID.
- RP-014: writing a new Secret's value can answer 409 after writing it
  (spec 009 code, found by this spec).
- A strict blast radius: a child added to an Application after the
  request fails an approved cascade as `StaleTarget`; request again.
- An approver's authority is live: a role removed before they decide
  means 403.

Out of scope: other destructive verbs, approval policies for people
(quorum, "a second person"), pre-approved runbooks, cancelling a request,
notifications, an audit read API, retention, and approvals for creates and
updates.

## Settled during T013

- **A direct `DELETE`'s audit rows name their Approval:**
  `approved: <approval> by policy (allow: …)`, as the spec describes.
  Before T013 they carried only the plain `allow: …` evidence; the code
  was brought in line (`api/authz.go`, pinned by
  `TestDelete_EveryRouteRecordsAPolicyApproval`).
