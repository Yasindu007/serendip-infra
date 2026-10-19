# Plan 019: Approvals, the exception path of authorization

Status: Approved 2026-10-19. Spec: `spec.md` (approved 2026-10-19,
amended the same day: a Secrets page, a store that refuses unrecorded
deletes, and Secret values erased from the approval's record).

## Approach in one paragraph

An `Approval` is an ordinary resource row (kind `Approval`) in the
existing store. Every delete records one **in the same transaction as the
delete**, and the store **refuses** a delete that carries none. The
authorizer puts an *approval intent* on the request context, as it already
does for the policy decision. `core/store/sqlite`'s `Delete` then:
- checks the target hasn't changed;
- deletes, cascade included;
- writes or completes the Approval, listing every removed resource with
  its last spec.

That list is a durable to-do list: after the commit, a new
`ApprovalReconciler` runs each removed kind's clean-up (today, erasing a
Secret's value through the Secrets capability) and retries until it
succeeds. It marks the approval `Verified` only when every record is gone
and every clean-up is done. This replaces Secret delete's erase-first
order, and it fixes cascades that left Secret values behind.

`POST /v1/approvals` uses the same checks as a DELETE, and a
`NeedsApproval` answer becomes a `Pending` Approval. The same reconciler
executes approved requests and expires overdue ones. Deciding is a route
whose checks are the target's delete actions, so only someone who could
delete the target directly can approve. One more rule,
`core/policy.MayDecide`, refuses agents, `system:` actors and the
requester.

The console gains Delete with blast radius, a Secrets page and an
Approvals page. No new dependency, no migration, no new role.

## Decisions

1. **An Approval is a resource row, not a new table.**
   - It uses `sqlite.NewStore[ApprovalSpec, ApprovalStatus]`, so it gets
     audit rows, events and `resourceVersion` for free.
   - **No `ownerRefs`:** a cascade must never delete the record of its own
     approval. The target is in `spec.action.target`. The owner chain is
     copied into `status` for read authorization (decision 9).
   - **Indexed reads, not a new table** (amended 2026-10-19, T007a).
     Approvals only grow: every delete adds one, and none are ever deleted,
     with retention a later spec. So no read may scan them all:
     - **Migration `0006`** adds:
       - `(kind, created_at, id)` for newest-first paging;
       - partial expression indexes, for `kind = 'Approval'` only, on the
         phase, on the target ID, and on unsettled approvals.
     - **One store query** (`sqlite.ListApprovals`) serves the list and
       the duplicate check: filters, newest first, a limit and an
       `after` cursor.
     - **The reconciler's resync** lists only unsettled approvals.
       `status.settled` is set once nothing more will happen: verified
       after execution, or rejected, expired or failed.
     - **The API pages:** `GET /v1/approvals?limit=&after=` (default 100,
       at most 500). A full page names the next cursor in
       `Serendip-Next-After`.
     - **Tests pin the query plans** to the indexes. T009 measures 20,000
       approvals on the simulated T470.
   - **Not in `resourcePaths`,** so the generic CRUD routes (PUT, DELETE)
     never exist for approvals. It has its own handler and five routes.

2. **The Approval and the delete commit together, through the context.**
   - **`core/store`** gains `ApprovalIntent` and `WithApproval(ctx,
     intent)` / `ApprovalFrom(ctx)`. This is the pattern
     `audit.WithPolicyDecision` already uses for the same reason: every
     delete path already passes the request context down to
     `store.Delete`.
   - **The intent:**
     - `Approval`: the record to write. It's new for a direct DELETE
       (phase `Executed`, decided by policy), or the existing one for the
       reconciler (decision 7).
     - `ExpectedVersion`: the target's `resourceVersion`. 0 means don't
       check.
     - `ExpectedRadius`: the blast radius that was approved. nil means
       don't check.
   - **`sqlite.Store.Delete`,** inside its transaction:
     1. with no intent in the context: returns the new
        `store.ErrNoApproval` (decision 3);
     2. compares the target's version, and the cascade's actual refs with
        `ExpectedRadius`, returning the new `store.ErrStale` on a
        mismatch. This happens before anything is removed;
     3. does the delete as today;
     4. inserts the Approval, or updates the existing one to `Executed`.
        Its blast radius is every row actually removed: kind, ID, name,
        and the row's **last spec** (`json.RawMessage`), read from the
        same rows the transaction already reads for its audit snapshots.
        The Approval gets its own audit row.

     A failed delete rolls back the Approval too, so a 404 or 409 leaves
     no record.
   - **Kind-agnostic:** the store doesn't know what a Secret is. It
     records each removed row's spec, and clean-up interprets it
     (decision 7).
   - **Rejected:**
     - **a separate `ApprovalStore.Record` after the delete:** not
       atomic. A crash in between leaves a delete without its Approval,
       which is exactly what rule 5 forbids;
     - **changing every handler:** four delete paths, and easy to miss a
       future one.

3. **The store refuses a delete without an approval (fail closed).**
   - **The rule:** `sqlite.Store.Delete` returns `store.ErrNoApproval`
     when the context carries no intent. That applies to every kind and
     every caller. There is **no bypass switch**, since a bypass would
     reopen the hole.
   - **The production callers,** five today, all carry an intent:
     - the generic handler, and the role definition and role assignment
       handlers, through the authorizer (decision 4);
     - startup's revocation of removed API keys (decision 5);
     - the approval reconciler (decision 7).

     `SecretReconciler.DeleteWithValue` is removed (decision 7).
   - **Tests:** the `core/store/storetest` helper `Approved(ctx, kind, id)`
     returns a context carrying a real intent. The roughly ten test files
     that call `Delete` directly use it. A new test checks that `Delete`
     without an intent fails and leaves the row in place.
   - **A stored approval must carry what was reviewed** (contract-guardian
     finding F1, T003): with `Existing`, a zero `ExpectedVersion` or a nil
     `ExpectedRadius` is refused with `ErrNoApproval`, so the stale check
     can't be skipped by omission. Only a new, policy-approved Approval
     may leave them empty.
   - **The API answer:** `ErrNoApproval` that ever reaches the API is a
     wiring bug, so `MapStoreError` turns it into a 500 with no internal
     text, and it's logged.

4. **The authorizer attaches the intent to every DELETE route.** This is
   in `api/authz.go`'s `wrap`, after all checks pass.
   - **The record:** requester (actor, principal, subject, sponsor), the
     joined `Allow` evidence as the request decision, and decided by
     `policy` with the same evidence.
   - **The response:** the Approval ID is generated up front. A response
     writer wrapper adds `Serendip-Approval-Id` only to a 2xx answer.
   - **The test:** `deleteactions_test.go` is extended so every
     registered DELETE route, when it succeeds, leaves exactly one
     `Executed` Approval naming its target. Decision 3 makes a missed
     route fail anyway; this test says which route.
   - **The agent's DELETE** is still 403 `needs-approval`. Its detail now
     reads: "…needs an approval: POST /v1/approvals with
     `{action: {verb: delete, target: {kind, id}, cascade}, reason}`".

5. **Non-API deletes.**
   - **Startup's API-key revocation** (`api/server.go` bootstrap) attaches
     an intent decided by `system`, with reason "API key removed from
     configuration".
   - **Break-glass** only creates, so nothing changes there.
   - Since decision 3, there is no other way to delete.

6. **`POST /v1/approvals` is decided like the DELETE it stands for.**
   - **The rule:** `targetApprovalBody`. The checks are the target kind's
     delete at the target, plus each descendant's delete when `cascade`
     is true. That's the same code as today's DELETE cascade checks,
     extracted into `deleteChecks`.
   - **`allowNeedsApproval`:** a new rule flag. `wrap` lets
     `NeedsApproval` through for that route only, and marks it in the
     context. `Deny` is still 403.
   - **The handler:**
     - It supports only kinds in `resourcePaths` (400 otherwise; access
       management is deleted with DELETE).
     - The target must exist (404).
     - The reason is required when any check said `NeedsApproval` (400).
     - It snapshots the target's name, version, owner chain and blast
       radius (`graph.Descendants` plus names).
     - With every check `Allow`: it creates the Approval `Approved` by
       policy (201), and the reconciler executes it.
     - With any `NeedsApproval`: `Pending`, expiring in 24 h (201).
   - **Duplicates:** a `Pending` or `Approved`-not-yet-executed approval
     for the same verb, target and cascade is returned instead (200). The
     check runs under a handler mutex, which is enough for one
     `serendipd` per store.

7. **The `ApprovalReconciler`: execution, clean-up, verification and
   expiry** (`core/reconcile/approval.go`).
   - **Its dependencies:**
     - the approval store;
     - `deleters map[resource.Kind]DeleteFunc`: each served kind's
       `store.Delete`, now including Secret's;
     - `cleaners map[resource.Kind]CleanupFunc`: today only Secret, which
       is `SecretReconciler.EraseValue`;
     - an existence lookup;
     - a clock.

     All are built in `cmd/serendipd`.
   - **Per approval:**
     - **`Pending`:** past `expiresAt`, it becomes `Expired`. Otherwise it
       requeues for the time left. The 30 s resync bounds the lateness.
     - **`Approved`:** it calls the kind's delete with the intent, which
       carries the expected version and radius, and a policy decision of
       `approved: <approval> by <approver>; requested by <requester>`.
       - The store does the stale check inside the transaction, so
         nothing needs pre-checking: nothing irreversible happens before
         it.
       - `ErrStale` (or a target that's gone) → `Failed`, reason
         `StaleTarget`. Any other error is returned, so the engine
         retries it.
     - **`Executed`, not yet `Verified`:**
       - For each blast-radius ref whose kind has a cleaner and that
         isn't yet marked cleaned: call it with the ref and its last
         spec, and on success mark the ref `cleanedUp` (an `Update` per
         ref, so progress survives a crash).
       - A cleaner error, such as the provider not ready, is
         returned or requeued, as `SecretReconciler` already does with
         `ErrProviderNotReady`.
       - Once every ref is cleaned up, confirm every ref is gone from
         the store, then set `Verified` true. A leftover ref (which
         shouldn't happen) sets `Verified` false naming it.
     - **`Rejected`, `Expired`, `Failed`, and `Executed` with
       `Verified`:** nothing. That's idempotent (constitution 3).
   - **Clean-up must be idempotent:** erasing an already-erased value
     succeeds. The Secrets provider's `Delete` treats "not found" as done,
     which T004 checks for the OpenBao provider. That's what makes a
     retry after a crash between the erase and the `cleanedUp` update
     safe.
   - **Secret delete changes:**
     - `SecretHandler.Delete` and `SecretReconciler.DeleteWithValue` go
       away. A Secret DELETE is the generic store delete like every other
       kind, and the value is erased by clean-up within seconds.
     - The order is now: record gone, then value erased. In between, the
       value is unreachable: access is decided per Secret record, and
       Secret IDs are server-chosen and never reused.
     - `EraseValue(ctx, ref, lastSpec)` rebuilds the `resource.Secret`
       (ID and path) the provider needs from the recorded spec.
   - **The cascade fix:** a cascade that removes Secrets lists them in
     the blast radius like any other ref, so their values are erased too.
     Before this spec they were left in the provider for good.
   - **The actor is the engine's `system:reconciler`, not the
     requester.** The requester didn't perform the delete; the approval
     did. Who asked and who approved are both named in `policy_decision`
     and in the Approval itself. (Amended at T005: the plan said
     `system:approvals` with interface `approval`, but
     `core/reconcile/CLAUDE.md` has the engine, not each reconciler, set
     the audit identity.)

8. **Deciding** (`POST /v1/approvals/{id}/approve|reject`).
   - **The rule:** `targetApprovalDecide`. It loads the approval; the
     checks are its target's delete, plus, for a cascade, the
     descendants' deletes **as they are now**. A missing approval is
     decided at `/`, like a missing role assignment. So the approver must
     hold the authority at decision time, with no new action or role.
   - **`policy.MayDecide(approver, requester Principal) error`:**
     - refuses `agent` and `system` principal types;
     - refuses the same principal as the requester.

     Wrong type is 403 "approver guardrail"; the requester is 403
     "separation of duties". Both are audited through `RecordDenied`.
   - **Not `Pending`:** 409. A concurrent decision loses on
     `resourceVersion` (`ErrConflict` → 409).
   - **The decision record:** approver (actor, principal, subject), time,
     comment (at most 1 KiB), and the joined `Allow` evidence. An
     approved request moves to `Approved` and the reconciler picks it up
     from the update event.

9. **Reading approvals.**
   - **`GET /v1/approvals/{id}`** (`targetApprovalRead`): read on the
     **first surviving** scope in `[target, recorded owner chain…]`,
     else `/`. The kind's read action applies. The owner chain is read
     from the graph (`Ancestors`) at request time and stored, because once
     the target is gone the engine's ownership cache can't build its
     chain.
   - **`GET /v1/approvals`** (`?phase=`, `?targetKind=&targetId=`,
     `?requester=`): it lists everything, then filters item by item
     through the same scope resolution. The route-table test covers all
     five routes.
   - **Hidden fields:** a blast-radius ref's last spec is **not
     returned** by the API (`json:"-"` on the API view). It's internal
     clean-up data. For Secrets it's only a path, but other kinds' specs
     could carry more later.

10. **Phases and conditions.**
    - **Phase:** `status.phase`, one of `Pending`, `Approved`,
      `Rejected`, `Expired`, `Executed`, `Failed`.
    - **Conditions:**
      - `Decided`: True or False, reason `ApprovedByPolicy`,
        `ApprovedByPerson`, `Rejected` or `Expired`;
      - `Executed`: reason `Deleted`, `StaleTarget` or `DeleteFailed`;
      - `CleanedUp`: reason `NothingToClean`, `Pending` or `Done`, with
        the count left;
      - `Verified`.
    - **Phase transitions** are checked in one function,
      `resource.ApprovalTransition(from, to)`, so neither the handler nor
      the reconciler can move a decided approval.
    - **Status is never taken from a client:** the handler builds the
      whole status.

11. **The console.**
    - **A Secrets page:**
      - `/secrets` is a list of the Secrets the caller may read, from the
        already-filtered `GET /v1/secrets`, with name, path, owner (linked
        to the Application or Component page) and status, plus a
        navigation entry;
      - `/secrets/:id` is a detail page: metadata, owner, conditions, and
        Delete;
      - **no values:** nothing calls `/v1/secrets/{id}/value`. Showing or
        editing values is a data permission and its own spec;
      - an Application page also links to its Secrets (a filtered list).
    - **Delete on the Application, Component and Secret pages,** shown
      when `GET /v1/authorization/permissions` allows the delete there.
      The dialog lists what will go, from
      `GET /v1/graph/{kind}/{id}/children` walked down, with cascade for
      an Application. For a Secret, or a cascade containing Secrets, it
      says "its value is erased from the secrets store". It shows the API
      equivalent (`ApiEquivalent`). On success it shows the Approval ID
      and links to it.
    - **An Approvals page** (navigation entry with a pending count):
      - pending requests first: action, target, requester, an agent's
        sponsor, reason, blast radius, and time left before expiry;
      - Approve and Reject behind a confirmation, with an optional
        comment. They're offered only when the caller may delete the
        target (the permissions API at the target scope) and isn't the
        requester. The server decides anyway;
      - decided requests show who decided (a person or "policy"), the
        outcome, and the conditions, including clean-up progress.

12. **Docs and the ADR.**
    - **ADR 0016** "Approvals: a record for every destructive action,
      decided by policy unless it needs a person". It records decisions
      2, 3 and 7: atomic through the context, fail-closed, and the
      approval as the clean-up record.
    - **`docs/architecture/approvals.md`.**
    - **Updates:**
      - `authorization.md` (agents section, audit);
      - `ai-operations.md` (the path's policy and approval steps now
        exist);
      - `secrets.md` (the delete order and the cascade fix);
      - `resource-model.md` (the Approval kind; delete needs an approval).
    - **`api/CLAUDE.md`:** "no destructive operation without an
      Approval", the intent and the header.

## Interfaces and types

```go
// core/resource/approval.go
const KindApproval Kind = "Approval"
type ApprovalVerb string            // "delete"
type ApprovalPhase string           // Pending, Approved, Rejected, Expired, Executed, Failed
type ApprovalAction struct { Verb ApprovalVerb; Target Scope; Cascade bool }
type ApprovalSpec   struct { Action ApprovalAction; Reason string }
type ApprovalActor  struct { Actor string; Principal PrincipalRef; Subject, Sponsor string }
type ApprovalRef    struct {
    Kind Kind; ID, Name string
    LastSpec  json.RawMessage // clean-up input; never returned by the API
    CleanedUp bool            // set once its kind's clean-up succeeded
}
type ApprovalDecision struct {
    ByPolicy bool; By *ApprovalActor; At time.Time; Comment, Evidence string
}
type ApprovalStatus struct {
    Status                      // conditions
    Phase ApprovalPhase
    RequestedBy ApprovalActor; RequestedAt, ExpiresAt time.Time
    RequestDecision string      // "allow: …" or "needsApproval: …"
    TargetName string; TargetVersion int64; OwnerChain []Scope
    BlastRadius []ApprovalRef   // target first, then descendants
    Decision *ApprovalDecision
    ExecutedAt time.Time
}
type Approval = Resource[ApprovalSpec, ApprovalStatus]
func ApprovalTransition(from, to ApprovalPhase) error
const ApprovalTTL = 24 * time.Hour

// core/store
var ErrStale      = errors.New("store: target changed since the approval was requested")
var ErrNoApproval = errors.New("store: delete refused: no approval")
type ApprovalIntent struct {
    Approval        resource.Approval // new (ID set, phase Executed) or existing
    Existing        bool
    ExpectedVersion int64             // 0: don't check
    ExpectedRadius  []resource.Scope  // nil: don't check
}
func WithApproval(ctx context.Context, in ApprovalIntent) context.Context
func ApprovalFrom(ctx context.Context) (ApprovalIntent, bool)

// core/store/storetest (test helper)
func Approved(ctx context.Context, kind resource.Kind, id string) context.Context

// core/policy
func MayDecide(approver, requester Principal) error // ErrApproverGuardrail, ErrSelfApproval

// core/reconcile
type DeleteFunc  func(ctx context.Context, id string, cascade bool) error
type CleanupFunc func(ctx context.Context, ref resource.ApprovalRef) error
func NewApprovalReconciler(approvals store.Store[...], deleters map[resource.Kind]DeleteFunc,
    cleaners map[resource.Kind]CleanupFunc,
    exists func(ctx context.Context, s resource.Scope) (bool, error), now func() time.Time) *ApprovalReconciler
func (r *SecretReconciler) EraseValue(ctx context.Context, ref resource.ApprovalRef) error
```

The exact field names are settled in T001. `PrincipalRef` reuses spec 017's
principal type and ID shape.

## Files touched

- **core:**
  - `core/resource/approval.go` (new) and its test, `validate.go`;
  - `core/store/store.go`, `core/store/storetest/` (new helper);
  - `core/store/sqlite/store.go` (`Delete`: refusal, stale check, removed
    refs with names and specs), `approval.go` (new: the in-transaction
    insert and update), and tests;
  - the roughly ten existing test files calling `Delete`, which move to
    `storetest.Approved`;
  - `core/policy/approvals.go` (new) and its test;
  - `core/reconcile/approval.go` (new) and its tests;
  - `core/reconcile/secrets.go` (`EraseValue`; `DeleteWithValue`
    removed) and its tests.
- **providers:** `providers/secrets/openbao` only if T004 finds that
  `Delete` of a missing value isn't already a success.
- **api:**
  - `api/authz.go` (rules, `deleteChecks`, intent, `allowNeedsApproval`,
    approval scopes);
  - `api/approval_handler.go` (new) and its tests;
  - `api/secret_handler.go` (the `Delete` override removed);
  - `api/problem.go` (detail text, `ErrNoApproval` → 500);
  - `api/server.go` (bootstrap intent, registration);
  - `deleteactions_test.go`, `authz_routes_test.go`.
- **cmd:** `cmd/serendipd/wiring.go` and `main.go` (the store, the
  deleters and cleaners maps, the reconciler).
- **ui:**
  - `ui/src/api/types.ts` and `client.ts`;
  - `ui/src/screens/Secrets.tsx` and `Approvals.tsx` (new);
  - `Applications.tsx`, `Components.tsx`, `common.tsx`, `App.tsx`
    (routes);
  - `ui/src/components/` (the delete dialog), `Shell.tsx` (navigation);
  - tests.
- **docs:**
  - `docs/decisions/0016-*.md`;
  - `docs/architecture/approvals.md`, `authorization.md`,
    `ai-operations.md`, `secrets.md`, `resource-model.md`;
  - `api/CLAUDE.md`, `docs/PROGRESS.md`;
  - `specs/019-approvals/verification.md`.
- **bench:**
  - `profiles/edge-1/bench/results/2026-10-xx-spec019-approvals/`;
  - `api/authz_bench_test.go` (DELETE with approval).

## Risks

- **Secret values are erased asynchronously.** A 204 on a Secret DELETE
  means the record is gone and the value will be erased within seconds,
  or as soon as the provider is reachable. The approval's `CleanedUp`
  and `Verified` conditions show when it's done. The value can't be
  reached in the meantime (decision 7). A client that must know it's
  erased reads the approval.
- **Ordering inside clean-up.** A crash after the provider erase but
  before `cleanedUp` is saved means the erase runs again. That's safe
  only because the erase is idempotent, which T004 checks against
  OpenBao.
- **Unbounded approval rows.** Every delete adds one (and the bench
  harness deletes per run). Each carries its removed rows' specs. For
  edge-1's kinds that's about 2–4 KiB a delete, more for a large
  cascade. Reads stay bounded by the indexes and paging (decision 1);
  disk use grows until the retention spec. The audit log keeps every
  approval's snapshot, so archiving later loses nothing.
- **Paging before filtering.** A page is cut before the per-item read
  check. A caller who may read few approvals can get short or empty pages
  and follows `Serendip-Next-After`, as Azure's paged lists do.
- **A strict blast-radius match.** A child added to an Application after
  the request makes its approved cascade `Failed` (`StaleTarget`). That's
  the intended safety, but a busy target may need a fresh request.
- **The approver's authority is live, not recorded.** An approver whose
  role is removed before they decide gets 403. That's correct and
  intended.
- **Sponsor collusion:** a sponsor can approve their own agent's request.
  The spec allows it; separation from the sponsor is a future approval
  policy.
- **The fail-closed store changes every test that deletes.** That's
  mechanical (the `storetest.Approved` helper), but it touches about ten
  files outside this spec's own code.
- **The 403 `needs-approval` detail changes.** Spec 018's verification
  matched on the problem type, not the text. T005 checks it.
- **Performance:** one extra row insert and audit row per delete. The
  target is under 1 ms p99 extra on the simulated T470. `serendipd` idle
  memory is expected to change negligibly, and T009 measures both.

## Explicitly NOT done

Everything in spec "Scope — OUT", plus:
- reading or editing Secret values in the console;
- `POST /v1/approvals` for access-management kinds, which use DELETE;
- clean-up for kinds other than Secret. Volumes and workloads keep their
  existing reconciler teardown; the `cleaners` map is where a future kind
  would plug in.
