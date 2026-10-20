# ADR 0016: Approvals: a record for every destructive action, decided by policy unless it needs a person

Status: Accepted
Date: 2026-10-20

## Context

Constitution rule 5: "Destructive operations (delete, scale-to-zero, node
drain, volume ops) always require an explicit approval object, even from
humans via API." Before spec 019 no Approval object existed:
- A principal whose role allowed a delete deleted at once. The only record
  was the audit row.
- An agent whose role allowed a delete got 403 `needs-approval` (spec
  018), with nothing it could do about it.

The user's direction (2026-10-18): **roles decide; an approval is the
exception** (ADR 0014). Asking a person to approve every delete would be
a hassle in a company, and Azure's model is already accepted. So every
destructive action leaves an Approval, and only the ones a role can't
settle wait for a person.

Constraints:
- A delete must never commit without its Approval, and a crash between
  the two must not be possible.
- A future delete path must not be able to forget the Approval.
- Deleting a Secret record used to erase its value first. That order
  destroys something irreversible before any stale check, and a cascade
  that removed Secrets never erased their values at all.
- No new dependency, no new role (constitution 4, ADR 0014).

## Decision

1. **Every delete records an Approval, in the delete's own transaction.**
   - The authorizer puts an *approval intent* on the request context
     (`store.WithApproval`), the way it already carries the policy
     decision (`audit.WithPolicyDecision`). Every delete path already
     passes the context down to `store.Delete`.
   - `sqlite.Store.Delete` writes (or completes) the Approval, listing
     every row it removed, in the same transaction. A delete that fails
     rolls the Approval back with it.
2. **The store fails closed.** A `Delete` whose context carries no intent
   returns `store.ErrNoApproval` and changes nothing. There is no bypass.
   A stored approval must also carry the reviewed `resourceVersion` and
   blast radius, so the stale check can't be skipped by omission.
3. **Policy approves instantly when roles Allow.** The Approval is
   recorded `Executed`, decided by policy, with the evidence. The caller
   sees the same `DELETE` as before, plus the header
   `Serendip-Approval-Id`.
4. **`NeedsApproval` waits for a person.** Today only an agent's deletes
   produce it. `DELETE` answers 403 `needs-approval` and names
   `POST /v1/approvals`; the request, with a reason, is `Pending` for 24
   hours.
5. **Who may decide.**
   - The decider needs the delete themselves, at the target and (with
     cascade) at every descendant, as of decision time. Approving never
     widens anyone's authority, and no new role exists.
   - `policy.MayDecide` adds what roles can't say: never an agent or a
     `system:` actor, never the requester.
6. **The reconciler executes.** The approval reconciler carries out an
   approved request through the store's delete, which checks inside its
   transaction that the target's version and blast radius are what was
   reviewed (`store.ErrStale`). Nothing irreversible happens before that
   check.
7. **The Approval is the clean-up record.** It stores each removed
   resource's last spec. After the delete commits, the reconciler runs the
   kind's clean-up from that record and retries until it succeeds. Today
   that is erasing a Secret's value. The approval is `Verified` only when
   every record is gone and every clean-up is done. A crash resumes from
   the record.
8. **Approvals are read through indexes and pages.** They only grow, so
   no read scans them all: partial indexes (migrations 0006, 0007),
   `sqlite.ListApprovals`, `status.settled` for the reconciler's resync,
   and `GET /v1/approvals?limit=&after=`.
9. **Approvals are immutable once decided and can't be deleted through the
   API.** The trail is the point. They have no `ownerRefs`, so a cascade
   never deletes the record of its own approval.

## Alternatives rejected

| Option | Why not |
|---|---|
| A separate `Record` call after the delete | Not atomic. A crash in between leaves a delete without its Approval, which is what rule 5 forbids. |
| `428 Precondition Required` on every DELETE until the caller holds an approval | It breaks every existing caller (the bench harness, scripts, tests) and asks a person to approve what their role already allows. |
| Changing every delete handler to write the Approval | Four delete paths today, and easy to miss a future one. The context intent plus the fail-closed store covers all of them. |
| Keeping Secret delete's erase-then-delete order | It erases before any stale check, can't be resumed from a record, and doesn't cover cascades. |
| `synchronous = NORMAL` to recover the write cost | A power loss would lose commits whose effects outside the database already happened (RP-001). Kept `FULL`. |

## Consequences

- **Cost per DELETE:** about +0.6 ms mean and +3 ms p99 on the dev NVMe,
  and +0.2 MiB idle memory (spec 019 T009). It is commit I/O, not CPU. The
  user accepted it on 2026-10-20 (RP-001, RP-002).
- **Approvals are unbounded** until a retention spec: about 2-4 KiB per
  delete, more for a large cascade. Reads stay bounded by the indexes
  (RP-003).
- **A Secret's value is erased shortly after its record**, not before. In
  between it can't be reached (access is decided per Secret record and IDs
  are never reused). The approval's `CleanedUp` and `Verified` conditions
  show when it's done (RP-005).
- **Cascades now erase the Secret values they remove.** Before this ADR
  they were left in the provider for good.
- **An agent's sponsor can approve its agent's request** (RP-007).
- **Pages can come back short**, because a page is cut before the
  per-approval read check (RP-006, RP-017).
- **Every test that deletes** carries a real approval
  (`storetest.Approved`). That touched about ten test files.
- **Other destructive verbs** (scale-to-zero, drain, volume operations)
  join by adding a verb, its checks and a deleter; the Approval, the
  decision rules and the reconciler stay as they are.
- **Approval policies for people** ("production deletes need a second
  person") plug into `NeedsApproval`; today nothing produces it for
  people.
