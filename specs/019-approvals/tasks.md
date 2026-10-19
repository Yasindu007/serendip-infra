# Tasks: Spec 019 — Approvals

Ordered. `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests or docs). Run `contract-guardian` before committing anything under
`core/` or `providers/`, and `go-reviewer` after each non-trivial Go task.

Dependency sketch: T001 → (T002 ∥ T003 ∥ T004) → T005 → T006 → T007 →
T007a → T008 → (T009 ∥ T010) → T011 → T012 → T013 → T014.

## T001 — The `Approval` kind — DONE 2026-10-19 (`go test ./core/resource/...` and check-imports clean; the spec validators live in `approval.go` beside the kind, like `secrets.go` and `authorization.go`, so `validate.go` is unchanged)

- **Do**:
  - Add the `core/resource/approval.go` types (plan "Interfaces"),
    including `ApprovalRef.LastSpec` and `CleanedUp`.
  - Add `ApprovalTransition` (the legal phase moves: Pending→Approved,
    Rejected or Expired; Approved→Executed or Failed; nothing out of the
    terminal phases).
  - Add `ApprovalTTL`, and validation: verb `delete` only, target kind
    and ID required, and a reason of at most 2 KiB.
  - Write table-driven tests.
- **Files**: `core/resource/approval.go`, `approval_test.go`,
  `validate.go`.
- **Verify**: `go test ./core/resource/...`;
  `bash .claude/scripts/check-imports.sh`.

## T002 [P] — `policy.MayDecide` — DONE 2026-10-19 (`go test ./core/policy/...`; it takes the requester as `*resource.PrincipalRef`, nil for a server-side requester, and allows only person and apiKey approvers, so `system` needs no principal type)

- **Do**: `core/policy/approvals.go`. It refuses `agent` and `system`
  approvers (`ErrApproverGuardrail`) and the requester itself
  (`ErrSelfApproval`), comparing type and ID. Write table-driven tests
  covering person, apiKey, agent, system, the same person, and the
  sponsor of the requesting agent (allowed).
- **Files**: `core/policy/approvals.go`, `approvals_test.go`.
- **Verify**: `go test ./core/policy/...`.

## T003 [P] — The store: the intent, fail-closed, removed rows recorded — DONE 2026-10-19 (`go test ./...` green; the production delete callers are exactly the four in `api/`, with the approval reconciler to come in T007. contract-guardian PASS on T001–T004; its finding F1 is fixed here: a stored approval must carry the reviewed version and radius, or `ErrNoApproval`)

- **Do**:
  - Add `store.ErrStale`, `store.ErrNoApproval`, `ApprovalIntent` and
    `WithApproval`/`ApprovalFrom`, and the `core/store/storetest.Approved`
    helper.
  - In `sqlite.Store.Delete`:
    1. refuse without an intent;
    2. check the version, before removing anything;
    3. collect every removed row (`Delete` and `cascadeDeleteChild`) with
       kind, ID, name and last spec, and compare the refs with
       `ExpectedRadius`;
    4. insert the new Approval, or update the existing one to `Executed`,
       with that blast radius and `executedAt`, plus its audit row, all in
       the delete's transaction;
    5. publish the Approval's event after commit.
  - Move every existing test that deletes onto `storetest.Approved`.
- **Files**: `core/store/store.go`, `core/store/storetest/`,
  `core/store/sqlite/store.go`, `approval.go`, `approval_test.go`, and the
  existing `*_test.go` files that call `Delete`.
- **Verify**:
  - `go test ./...` (every package, since the helper change crosses
    packages).
  - The new tests cover:
    - no intent gives `ErrNoApproval`, with the row still there;
    - the new approval committed with the delete;
    - the existing approval updated;
    - a version mismatch gives `ErrStale`, with nothing deleted and no
      approval written;
    - a radius mismatch gives `ErrStale`;
    - a failed delete (404, has children) leaves no approval;
    - a cascade's blast radius lists every removed ref with its name and
      last spec, a Secret child included.
  - `grep -rn "\.Delete(" --include=*.go` shows no production caller
    outside the five in plan decision 3.

## T004 [P] — Secret value erasure from the record — DONE 2026-10-19 (OpenBao `DeleteSecret` already treats 404 as success, pinned by `TestClient_DeleteSecret_404ReturnsNilNotError`, so no provider change was needed. `EraseValue` tests cover a repeat erase and the errors)

- **Do**:
  - Add `SecretReconciler.EraseValue(ctx, ref)`, which rebuilds the
    Secret (ID, path) from `ref.LastSpec` and calls the provider's
    `Delete`. It returns `ErrProviderNotReady` the way `Reconcile` does.
  - Check that the OpenBao provider's `Delete` of a value that is already
    gone succeeds. If it doesn't, make it, so erasure is idempotent.
  - Remove `DeleteWithValue` and `SecretHandler.Delete`'s override (the
    generic Delete takes over).
- **Files**: `core/reconcile/secrets.go`, `secrets_test.go`,
  `api/secret_handler.go`, and `providers/secrets/openbao/*` if needed.
- **Verify**:
  - `go test ./core/reconcile/... ./providers/secrets/... ./api/...`.
  - The tests show `EraseValue` erasing by ID and path, a second
    `EraseValue` succeeding, and a not-ready provider giving
    `ErrProviderNotReady`.
  - The OpenBao test (its existing fake, or a real run if the fake
    doesn't model a missing path) shows a delete of a missing value
    succeeding.
- **Then**: run `contract-guardian` on T001–T004 before committing.

## T005 — Every DELETE records an Approval — DONE 2026-10-19 (`approvals_delete_test.go`, `approvals_routes_test.go`, `agent_test.go`. A 500 from `ResourceHandler` no longer carries internal text. Committed together with T001–T004, since the fail-closed store breaks every API DELETE without this task)

- **Do**: in `api/authz.go`:
  - extract `deleteChecks`;
  - attach the intent in `wrap` for DELETE routes, with the requester and
    the policy evidence;
  - add the 2xx-only `Serendip-Approval-Id` writer.

  Also:
  - change the `needs-approval` detail (plan decision 4);
  - map `ErrNoApproval` to a 500 that is logged and carries no internal
    text;
  - attach the bootstrap revocation intent (plan decision 5).
- **Files**: `api/authz.go`, `api/problem.go`, `api/server.go`,
  `api/deleteactions_test.go`, `api/agent_test.go`.
- **Verify**:
  - `go test ./api/...`.
  - The extended `deleteactions_test` checks that every registered DELETE
    route that succeeds, Secrets and access management included, leaves
    exactly one `Executed`, policy-decided Approval for its target and
    returns its ID in the header.
  - A 404 or 409 DELETE leaves none and has no header.
  - An agent's DELETE is still 403 `needs-approval`, with the new
    detail.
  - A test of bootstrap key removal shows its Approval.

## T006 — Approval routes — DONE 2026-10-19 (`approval_handler_test.go`: request, decide, self-approval and expiry, reading. Deviation: an approval written by a plain DELETE gets its owner chain from the store, read inside the delete transaction, so reading it after the target is gone works too. `MayDecide` runs in the authorizer before the permission checks, so an agent gets "approver guardrail", not a needs-approval answer)

- **Do**:
  - `api/approval_handler.go`: request, get, list (with filters), approve
    and reject.
  - New rules: `targetApprovalBody`, `targetApprovalDecide`,
    `targetApprovalRead`, `allowNeedsApproval`.
  - Approval scope resolution for get and list (plan decision 9). The
    API view never includes `LastSpec`.
  - Call `MayDecide` in decide, with `RecordDenied` on refusal.
  - The duplicate rule, under a mutex.
  - Register the routes and the route-table entries.
- **Files**: `api/approval_handler.go`, `approval_handler_test.go`,
  `api/authz.go`, `api/server.go`, `api/authz_routes_test.go`.
- **Verify**:
  - `go test ./api/...`.
  - The handler tests cover spec acceptance criteria 2, 3, 5, 6 (409
    after a decision), 9 and 11.
  - They also cover: a reason required only for `NeedsApproval`; 400 for
    an access-management target; 404 for a missing target; a client-sent
    status being ignored; and no `lastSpec` in any response.

## T007 — `ApprovalReconciler` and wiring — DONE 2026-10-19 (`approval_test.go`, 7 tests on a real SQLite store. Deviation: one kind-agnostic `DeleteFunc(ctx, kind, id, cascade)` instead of a per-kind map; `cmd/serendipd` serves it with a `json.RawMessage` store per call. Without a cluster, the Secret cleaner answers `ErrProviderNotReady`, so erasure waits rather than being skipped. Reviews of T005–T007: contract-guardian PASS and go-reviewer PASS, findings fixed before commit. The reconciler re-checks who decided (`MayDecide`) before executing. A permanent delete error ends as Failed/DeleteFailed. Conditions carry no provider or store error text; it's logged instead. StillPresent requeues. The approvals list filters on loaded approvals. A decision also needs delete on the recorded blast radius. Approve and reject accept an empty chunked body. Not done: an index for approvals, which stays plan decision 1)

- **Do**:
  - `core/reconcile/approval.go`, per plan decision 7:
    - expire;
    - execute with the intent (the engine's `system:reconciler` actor), with
      stale → `Failed`;
    - clean up each blast-radius ref through `cleaners`, saving
      `cleanedUp` per ref;
    - verify, with the `CleanedUp` and `Verified` conditions.
  - Wire the approval store, the deleters map (every served kind's store
    `Delete`), the cleaners map (Secret → `EraseValue`) and the
    registration in `cmd/serendipd`.
- **Files**: `core/reconcile/approval.go`, `approval_test.go`,
  `cmd/serendipd/wiring.go`, `main.go`.
- **Verify**:
  - `go test ./core/reconcile/... ./cmd/serendipd/...`, using a fake
    clock, fake deleters and fake cleaners.
  - The tests cover:
    - expiry, and requeue before expiry;
    - stale by version, by radius, and because the target is gone;
    - a retried transient delete error;
    - clean-up of two Secret refs where the second fails once: the first
      isn't erased again, the second is retried, and `Verified` is set
      only after both;
    - a cleaner returning `ErrProviderNotReady`, which requeues;
    - idempotence: a second reconcile of a verified approval writes
      nothing.
  - `go build ./...`.
- **Then**: run `contract-guardian` and `go-reviewer` on T005–T007, then
  commit.

## T007a — Indexed approval reads and paging (added 2026-10-19) — DONE 2026-10-19 (`go test ./...`; query plans pinned, and every approval query uses its index with no sort. `INDEXED BY` was needed because the planner otherwise read every approval for the unsettled query. contract-guardian PASS; both of its findings are fixed: 0006 backfills `settled` for approvals recorded earlier, and timestamps are now written at a fixed width, so text order is time order, while older rows still parse)

- **Do** (plan decision 1, amended):
  - Migration `0006`: the `(kind, created_at, id)` index, and partial
    expression indexes for approvals on phase, target ID and unsettled.
  - Add `status.settled`. The handler's reject sets it, and so do the
    reconciler's expire, fail and verified finish.
  - `sqlite.ListApprovals(ctx, db, query)`: phase, target, requester and
    unsettled filters, newest first, a limit and an `after` cursor.
  - The list handler pages with it (`limit`, `after`,
    `Serendip-Next-After`). The duplicate check queries by target, and
    the reconciler's resync lister asks for unsettled approvals only.
- **Files**: `core/store/sqlite/migrations/0006_approval_indexes.sql`,
  `core/store/sqlite/approval.go`, `approval_test.go`,
  `core/resource/approval.go`, `core/reconcile/approval.go`,
  `api/approval_handler.go`, the tests, and `cmd/serendipd/wiring.go`.
- **Verify**:
  - `go test ./...`.
  - `EXPLAIN QUERY PLAN` tests show each approval query using its index,
    not a scan.
  - The paging tests cover: pages in order with no gaps or repeats;
    `Serendip-Next-After` only on a full page; and settled approvals left
    out of the resync list.
- **Then**: run `contract-guardian` (core and a migration) before
  committing.

## T008 [A] — End-to-end acceptance tests

- **Do**: tests in the `api` package against a real SQLite store, with
  the engine running and a fake Secrets provider, for spec acceptance
  criteria 1–11, 15 and 16. They include:
  - approve → executed → cleaned up → verified;
  - cascade matching the recorded radius;
  - a cascade-deleted Application's Secret value erased;
  - erasure retried while the fake provider is down, then succeeding,
    including across an engine restart on the same store;
  - the audit rows: request, decision, and execution carrying the
    approval in `policy_decision`.
- **Files**: `api/approval_acceptance_test.go`.
- **Verify**: `go test ./... -race` (or without `-race` where cgo isn't
  available on Windows, as in earlier specs). Everything is green.

## T009 [P] — Benchmarks

- **Do**:
  - Add a DELETE-with-approval benchmark beside spec 017's authz benches.
  - Run the edge-1 harness on the simulated T470 profile: `serendipd`
    idle memory, and the harness's own clean-up (which now records
    approvals).
  - Compare with `2026-10-19-spec018-reuse`.
- **Files**: `api/authz_bench_test.go`,
  `profiles/edge-1/bench/results/2026-10-xx-spec019-approvals/`.
- **Verify**: the results are committed. The DELETE overhead is under
  1 ms p99. Rule 4 still passes, with `serendipd` under 256 MiB, and the
  idle delta is reported. With 20,000 seeded approvals, the list (first
  page and `?phase=Pending`), the request with its duplicate check, and a
  resync each stay under 50 ms p99 (T007a).

## T010 [P] — Console

- **Do**:
  - Add the types and client calls.
  - **A Secrets page:** `/secrets` list (name, path, owner link, status)
    and `/secrets/:id` detail, with a navigation entry. An Application
    page links to its Secrets. No value calls.
  - **The delete dialog** on the Application, Component and Secret pages,
    gated by the permissions API. It shows the blast radius from the
    graph children, the "value is erased" note for Secrets, and the API
    equivalent.
  - **The Approvals page,** with a navigation entry and a pending count:
    lists, detail with clean-up progress, and approve/reject with
    confirmation and comment.
  - Follow `ui/DESIGN.md` and `ui/design-rules/`.
- **Files**: `ui/src/api/types.ts`, `client.ts`,
  `ui/src/screens/Secrets.tsx`, `Approvals.tsx`, `Applications.tsx`,
  `Components.tsx`, `common.tsx`, `ui/src/App.tsx`,
  `ui/src/components/` (the dialog), `Shell.tsx`, and the tests.
- **Verify**:
  - `pnpm test` and `pnpm build` in `ui/`, then `go build` the embedded
    console.
  - The tests show that no Secrets screen requests a `/value` path.
  - A quick local run (`/run`) shows:
    - a Secret listed and deleted, with its Approval;
    - a Component deleted, with its Approval;
    - a seeded pending Approval approved from the page;
    - approve hidden for the requester;
    - the API-equivalent text matching the requests in the browser's
      network log.

## T011 — Real-cluster verification

- **Do**:
  - Check host RAM. Then `docker start serendip-t018 amp-local` (spec 018
    leftovers) and run `serendipd` against `serendip-t018` with the agent
    issuer configured.
  - Walk spec acceptance criteria 12–16:
    - a person deletes a Component in the console (Approval `Executed`,
      decided by policy, workload gone);
    - a Secret deleted from the Secrets page: the value is gone from
      OpenBao (read through the management path or `bao kv get`, which
      shows not found);
    - a cascade delete of an Application owning a Secret: that value is
      gone from OpenBao too;
    - OpenBao scaled to 0 during a Secret delete: the approval stays
      `CleanedUp` false, then erases and verifies after it's scaled back,
      including across a `serendipd` restart;
    - the spec 018 agent token gets 403 on DELETE, then requests through
      `POST /v1/approvals`;
    - the person approves in the console; the reconciler executes and
      verifies; the workload is torn down;
    - one stale case and one reject case;
    - the bench harness run unchanged.
- **Files**: `specs/019-approvals/verification.md`.
- **Verify**: every criterion has observed evidence (requests, Approval
  JSON, audit rows, `kubectl`/`bao` showing the workload and value gone).
  Stop the containers afterwards.

## T012 — Final reviews

- **Do**: run `contract-guardian`, `go-reviewer` and `/security-review`
  on the whole spec diff. Fix the findings, with a fix commit for each
  group.
- **Files**: as the findings require.
- **Verify**: all three pass, and `go test ./...` and `pnpm test` are
  green after the fixes.

## T013 [A] — Docs and ADR

- **Do**:
  - Write ADR 0016 and `docs/architecture/approvals.md`.
  - Update `authorization.md` (agents section, audit), `ai-operations.md`,
    `secrets.md` (the delete order and the cascade fix) and
    `resource-model.md`.
  - Add the `api/CLAUDE.md` rule.
  - Use `docs-keeper`.
- **Files**: `docs/decisions/0016-approvals.md`, `docs/architecture/*.md`,
  `api/CLAUDE.md`.
- **Verify**: `docs-keeper` reports the docs in sync with the code. Every
  route, phase and condition in the code appears in `approvals.md`.

## T014 — Close the spec

- **Do**: update `docs/PROGRESS.md`: spec 019 complete, what it built,
  the leftovers, and what's next. Mark every task done with its evidence.
- **Files**: `docs/PROGRESS.md`, `specs/019-approvals/tasks.md`.
- **Verify**: `git status` is clean after the closing commit. PROGRESS
  names the next spec candidates.
