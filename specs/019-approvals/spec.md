# Spec 019: Approvals, the exception path of authorization

Status: Approved 2026-10-19, reworked on top of spec 017 (Authorization)
and spec 018 (agent identities). Amended 2026-10-19 during planning: a
console Secrets page, the store refusing a delete that carries no
approval, and Secret values erased from the approval's record (which
also fixes cascades leaving Secret values behind).

## Problem

Constitution rule 5: "Destructive operations (delete, scale-to-zero, node
drain, volume ops) always require an explicit approval object, even from
humans via API." Today no Approval object exists:

- A principal whose role allows a delete deletes immediately, and the only
  record is the audit row.
- An agent whose role allows a delete gets 403
  `urn:serendip:problem:needs-approval` (spec 018), and the detail says
  "approvals aren't available yet". So an agent can't delete anything,
  even with a person's sign-off.
- The console has no delete at all (spec 016 kept it out until approvals
  existed).

The user's direction (2026-10-18): **roles decide; an approval is the
exception.** Asking a person to approve every delete would be a hassle in a
company, and Azure's model is already accepted. So:

- Every destructive action produces an Approval object (constitution 5).
- When the actor's role allows the action, policy approves it **instantly**.
- Only `NeedsApproval` decisions wait for a person. Today those are agents'
  deletes (spec 018).
- A principal whose role denies the action can't even request it.

`ai-operations.md`'s path, **Action → Policy check → Provider → Execution →
Verification → Audit**, gets its first real piece here.

## Scope — IN

### The `Approval` resource kind

An operational kind, like `Incident`.

- **What it asks for (spec):**
  - an action. The only verb in this spec is `delete`, with a target
    `{kind, id}` and `cascade` true or false. The verb field is designed to
    take scale-to-zero, drain and volume operations later;
  - a reason, in free text. It's required on a request that will wait for
    a person, and optional when policy approves it.
- **What the server records (status), never taken from the client:**
  - the requester (principal and audit actor) and, for an agent, its
    sponsor;
  - when it was requested;
  - the target's name, its `resourceVersion` and its owner chain at that
    moment;
  - the blast radius: every resource the delete would remove (the target
    and, with cascade, its descendants), with kind, name and ID, taken from
    the resource graph;
  - the policy decision at request time (`allow` or `needsApproval`, with
    its evidence);
  - the decision: who decided (a person, an API key or **policy**), when,
    and an optional comment;
  - the outcome of execution and its verification.
- **Phases:** `Pending` → `Approved`, `Rejected` or `Expired`. An approved
  request then becomes `Executed` or `Failed`. Conditions follow
  constitution 3 (type, status, reason, message).
- **Expiry:** a pending approval expires 24 hours after the request
  (constant, revisited in plan.md if needed).
- **Immutability:** a decided approval can't change, and approvals can't be
  deleted through the API. The trail is the point.

### Two ways in, one record

1. **`DELETE /v1/{kind}/{id}` keeps working for those allowed.** When
   policy returns `Allow`:
   - the server writes an Approval decided by **policy**, with the
     evidence (role, assignment, scope), in the same store transaction as
     the delete;
   - the delete then behaves as today: cascade and workload teardown by the
     existing reconcilers. A Secret's value, its own or a cascaded one's,
     is erased afterwards from the approval's record (see "Execution");
   - the response carries the approval's ID (header
     `Serendip-Approval-Id`) and the approval is `Executed`.

   Existing callers (the bench harness, tests, scripts) need no change.
   This applies to every DELETE route, role assignments and custom role
   definitions included, so no delete happens without an Approval.
2. **`POST /v1/approvals` requests an action explicitly.** It's decided by
   the same policy check as the DELETE:
   - `Allow`: approved by policy, then executed by the approval
     reconciler.
   - `NeedsApproval`: `Pending`, waiting for a person. 201 with the
     approval.
   - `Deny`: 403, as today, and nothing is recorded but the audit row.
   - At most one pending request exists per target and action; asking
     again returns the existing one (200).

   `DELETE` on a `NeedsApproval` decision still answers 403
   `needs-approval`, now with a detail telling the caller to
   `POST /v1/approvals` with a reason. Nothing pending is created without
   a reason.

### Deciding

`POST /v1/approvals/{id}/approve` and `/reject`, with an optional comment.

**Who may decide** (one function in `core/policy`):
- a person or an API key, never an agent or a `system:` actor;
- never the requester (separation of duties). Since a person's own deletes
  are approved by policy, this only matters for agents today;
- and only someone whose roles **Allow the action themselves**, at decision
  time: delete at the target, and with cascade at every descendant, by its
  own kind. An approver can approve only what they could have done
  directly, so approving never widens anyone's authority. No new role is
  needed.

An agent's sponsor is a natural approver, but isn't required to be the
one.

A refused decision is 403 and audited. A decision on an approval that's no
longer `Pending` is 409.

### Execution, reconciled (constitution 3)

The approval reconciler carries out an approved action that isn't yet
executed:
- **Stale target:** if the target changed since the request (a different
  `resourceVersion`) or is already gone, the approval ends `Failed` with a
  stale-target reason and nothing is deleted. Nobody deletes what nobody
  reviewed.
- **The delete itself:** today's store delete, so cascade and teardown
  behave exactly as before.
- **Verification:** the target, and with cascade every resource in the
  blast radius, is gone from the store before the approval is marked
  `Executed`. Workload teardown keeps being reported by the existing
  reconcilers.
- **Clean-up from the record:** the approval records every resource it
  removed, with what's needed to clean up after it. For a Secret, that's
  where its value lives.
  - After the delete commits, the reconciler erases each removed
    Secret's value through the Secrets capability, and retries until it
    succeeds.
  - The approval is `Verified` only when every record is gone **and**
    every value is erased.
  - This replaces today's erase-then-delete order. Nothing irreversible
    happens before the stale check, and a crash resumes from the record.
  - It also fixes an existing gap: a cascade that removes Secrets
    (deleting an Application or Project) never erased their values.
- **No delete without an approval, enforced by the store:** a store
  delete that carries no approval is refused with an error, so a future
  delete path that forgets one fails loudly instead of deleting
  unrecorded. There's no bypass; tests use real approvals.
- **Expiry:** the same reconciler moves overdue `Pending` approvals to
  `Expired`.

### Reading approvals

- `GET /v1/approvals` (filters: phase, target, requester) and
  `GET /v1/approvals/{id}`.
- **Authorization:** reading an approval needs read on its target. Once the
  target is gone, it's decided at the nearest surviving owner in the
  recorded owner chain, or at `/`. Lists keep only what the caller may
  read, like every other list. The route-table test covers the new routes.

### Audit (constitution 8)

- The request, the decision and the execution each have an audit row with
  their actor.
- The delete's own row carries, in `policy_decision`, the approval ID and
  who approved it: `approved: <approval> by policy (allow: <role>
  (<assignment>) at <scope>)`, or `approved: <approval> by <approver>`.
- The 403 `needs-approval` audit row from spec 018 stays.

### The console (interface parity, constitution 7)

- **A Secrets page:** a list of the Secrets the caller may read (name,
  path, owner, status) and a detail page. It never shows or edits values;
  that's a separate data permission and its own spec.
- **Delete** on Application, Component and Secret detail pages, shown only
  when `GET /v1/authorization/permissions` allows it. A dialog shows the
  blast radius (from the graph API), with the API request it sends. It
  uses the existing DELETE, so the console shows the resulting approval ID.
- **An Approvals page:**
  - pending requests first, each with its action, target, requester (and an
    agent's sponsor), reason and blast radius;
  - approve and reject behind a confirmation, offered only to someone who
    may decide;
  - decided requests show who decided (a person or policy) and the
    outcome.
- Built with the existing design system and its consistency rules.

### Docs

- A new `docs/architecture/approvals.md`, linked from `authorization.md`
  and `ai-operations.md`.
- `authorization.md`'s agent section: "Spec 019 turns it into…" becomes the
  real flow.
- The `api/CLAUDE.md` rule: no destructive operation without an Approval.
- The resource model.

## Scope — OUT (explicitly)

- **Other destructive verbs:** scale-to-zero, node drain and volume
  operations. None exists in the API yet.
- **Approval policies for people**, such as "deletes in production need a
  second person", quorum, or multiple approvers. The engine's
  `NeedsApproval` is the hook for them later; today only agents' deletes
  produce it.
- **Pre-approved runbooks** and AI severity-based autonomy
  (`ai-operations.md`'s matrix).
- **Cancelling a request** by its requester. It expires instead.
- **Notifications** (email, chat) when an approval is waiting.
- **An audit read API** (spec 016's gap 2, its own spec).
- **Retention or clean-up of old approvals.**
- **Approvals for non-destructive changes** (creates, updates).
- **Changes in Agent Manager.** The POC agent is driven from outside, as in
  spec 018.

## Affected resources and capabilities

- `core/resource`: the `Approval` kind.
- `core/policy`: who may decide (`MayDecide`), and the blast-radius actions
  reused from the cascade check.
- `core/reconcile`: the approval reconciler (execution, verification,
  expiry).
- `core/store`: the Approval and the delete written in one transaction,
  and a delete without an approval refused.
- `core/reconcile`: Secret delete no longer erases the value itself; the
  approval reconciler erases it from the record.
- `api`: the approval routes and their rules, the approval on every DELETE,
  the new `needs-approval` detail.
- The audit store: `policy_decision` carries the approval.
- `ui`: Delete with blast radius, the Secrets page, the Approvals page.

## Acceptance criteria

1. **Policy approves instantly.** A person with Contributor deletes a
   Component with `DELETE`. It's gone at once, as today; the response names
   an Approval that is `Executed`, decided by policy, with the evidence and
   the blast radius. The same holds for every DELETE route.
2. **Denied can't request.** A Reader gets 403 from both `DELETE` and
   `POST /v1/approvals`, and no Approval exists.
3. **An agent waits for a person.** An agent with Contributor gets 403
   `needs-approval` from `DELETE`, whose detail points to
   `POST /v1/approvals`. Its request with a reason is `Pending`, showing the
   requester, the sponsor and the blast radius, and nothing is deleted.
4. **Approve and execute.** A person with delete at the target approves.
   The reconciler deletes the Component, its workload is torn down, and the
   approval ends `Executed` with verification.
5. **Who may decide.** The requesting agent, any other agent, a `system:`
   actor, and a person without delete at the target (or at a descendant,
   for cascade) get 403 on approve and reject. Each refusal is audited.
6. **Reject and expire.** A rejected or expired approval never deletes
   anything, and deciding it again is 409.
7. **Stale target.** Changing the target after the request makes execution
   fail with a stale-target reason, and nothing is deleted.
8. **Cascade.** An approved cascade delete of an Application removes it and
   its Components, exactly the recorded blast radius.
9. **No duplicates, no deletion.** Requesting the same target twice returns
   the same pending approval. Approvals can't be deleted through the API,
   or changed after the decision.
10. **Audit.** The request, the decision and the execution each have audit
    rows with the right actor, and the delete's row carries the approval and
    approver in `policy_decision`.
11. **Reading.** A caller sees only approvals whose target (or surviving
    owner) they may read, including after the target is gone.
12. **Console.** A person deletes a Component from the console and sees the
    approval. They find a Secret on the Secrets page and delete it the
    same way. On the Approvals page, they approve an agent's pending
    request and watch it execute. The API-equivalent text matches the
    requests sent.
13. **Real agent.** On a real cluster, the spec 018 agent token requests a
    delete, a person approves it in the console, and it executes.
14. **Nothing else breaks.** The bench harness, all existing tests and a
    real-cluster run pass unchanged. Constitution 4 still holds
    (`serendipd` idle memory measured).
15. **Secret values are erased.** Deleting a Secret, and cascade-deleting
    an Application that owns one, removes the record at once and the
    value from the Secrets provider shortly after. The approval is
    `Verified` only then. With the provider unreachable, the approval
    stays unverified and the erase is retried until it succeeds, across a
    `serendipd` restart.
16. **No unrecorded delete.** A store delete without an approval is
    refused, and nothing is deleted.
