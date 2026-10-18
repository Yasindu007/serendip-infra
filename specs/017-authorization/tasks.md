# Tasks: Spec 017 — Authorization (Azure-style roles at scopes)

Ordered. `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). Run `contract-guardian` before committing anything under
`core/` or `providers/`, and `go-reviewer` after each non-trivial Go task.

Dependency sketch: T001 → T002 → (T003 ∥ T004) → T005 → T006 → T007 →
(T008 ∥ T009) → T010 → T011 → T012.

## T001 — Spike: ThunderID subjects and groups on a live install — DONE 2026-10-18 (plan.md "Spike findings"; all four confirmed)

- **Do**: On a kind cluster with the spec 016 stack, confirm the upstream
  facts in plan.md empirically:
  1. The bootstrap admin's `sub` is `01900000-0000-7000-8000-000000000030`
     on two fresh installs.
  2. With `groups` in the `serendip` client's token attributes (bootstrap
     on a fresh install), access tokens carry `groups` as names. Check the
     shape for a user in 0 groups (claim omitted), in `Administrators`, and
     in a nested group.
  3. Create a group and add a user through `/groups` over a port-forward,
     and confirm the next login's token reflects it.
  4. Adding the attribute to an *existing* install's client through the
     management API (the generalised operator tool) takes effect without a
     reinstall.
- **Files**: `specs/017-authorization/plan.md` ("Spike findings";
  decisions 7 and 9 amended in place).
- **Verify**: every item has observed evidence. Delete the scratch
  cluster.

## T002 — `core/resource` kinds + `core/graph.Ancestors` — DONE 2026-10-18 (contract-guardian PASS)

- **Do**:
  - Add the `RoleDefinition` and `RoleAssignment` kinds, specs and
    statuses, with validation:
    - action-string syntax;
    - at most one wildcard per string;
    - `assignableScopes` required for custom roles and never `/` or a
      wildcard;
    - a principal type in {person, group, apiKey, agent}, and an agent
      requires a sponsor.
  - Add `graph.Ancestors`, the owner chain nearest-first.
- **Files**: `core/resource/authorization.go`, `core/graph/edges.go`,
  `core/graph/graph.go`, and tests.
- **Verify**:
  - table tests for every validation rule;
  - Ancestors over a 5-level chain, an unowned resource, and a cycle
    guard;
  - `go test ./core/...`; `check-imports.sh`; contract-guardian PASS.

## T003 [P] — `core/policy`: built-in roles, index, evaluation — DONE 2026-10-18 (contract-guardian PASS, go-reviewer PASS; ~4 µs per decision)

- **Do**:
  - Add the built-in roles from spec.md with stable UUIDs.
  - Add `Index`: load, event-update, and a periodic rebuild every 30 s.
  - Add `Decide` and `EffectiveActions`, implementing Azure's algorithm
    (the union over the principal and its groups at the scope and its
    ancestors; `actions − notActions` and data actions kept separate;
    wildcard matching), returning the evidence.
  - Add the delegation check (decision 10) and the last-Owner check.
- **Files**: `core/policy/*` and tests.
- **Verify**: `go test ./core/policy/...` with table tests for:
  - the Reader, Contributor, Owner and Secrets roles against
    control-plane and data actions;
  - inheritance and union, and that `notActions` doesn't deny;
  - wildcards (`*/read`, `serendip/secrets/*`);
  - groups;
  - the delegation allow-list, including the self-assignment refusal;
  - the last Owner at `/`;
  - an evaluation benchmark against the plan's target.

## T004 [P] — Identity: subject and groups on the principal — DONE 2026-10-18 (contract-guardian PASS, go-reviewer PASS)

- **Do**:
  - Carry `sub` and groups (the claim from T001) into
    `identity.Principal`.
  - Record the subject in audit rows.
  - Add the known-principals registry: a migration, upserted on every
    authenticated request at most once a minute per principal.
  - **ThunderID:**
    - the bootstrap `serendip` client gains `groups` in its token
      attributes (fresh installs);
    - `cmd/serendip-identity-redirects` is generalised to also ensure that
      attribute on existing installs (plan.md decision 9), reusing the
      same tested management-API code.
- **Files**: `core/identity`, `providers/identity/oidc/verifier.go`,
  `core/store/sqlite` (migration), the audit record,
  `providers/identity/thunderid/{values.go,clients.go}`,
  `cmd/serendip-identity-redirects`, and tests.
- **Verify**: verifier table tests (groups present, absent, malformed;
  capped size); a migration test; `go test ./...`.

## T005 — `api`: route table, authorization middleware, list filtering, 403 — DONE 2026-10-18

- **Notes:** routes are authorized where they're registered (an
  authorizing mux and the `Mux` interface), since a middleware in front
  of the mux can't see the matched pattern or `{id}`. A PUT that moves a
  resource under an owner outside its current ancestry also needs write
  there. Graph results are filtered like lists. The API-key migration
  (each key with no assignment becomes Owner at `/`) landed here, in
  `NewServer`, so every existing test keeps its behaviour. T007 keeps the
  bootstrap administrator, removed-key cleanup and break-glass.

- **Do**:
  - Write the complete route-to-action table (decision 2).
  - Add the middleware after `AuthMiddleware`, resolving the target scope
    per verb (decision 3).
  - Add list filtering and the 403 problem shape.
  - Fill `policy_decision` and audit denied mutations (decision 8).
  - Migrate existing tests' API keys to Owner at `/`.
- **Files**: `api/authz.go`, `api/server.go`, `api/middleware.go`,
  `api/handler.go`, `api/secret_handler.go`, and tests.
- **Verify**: `go test ./api/...` including:
  - **fail closed:** a test enumerating every registered route finds none
    without a mapping;
  - AC 1–4 as unit tests;
  - the `policy_decision` contents;
  - denied-mutation audit rows;
  - all existing tests unchanged in behaviour.

## T006 — `api`: authorization endpoints — DONE 2026-10-18

- **Notes (decisions made while building):**
  - Reading role definitions and `permissions` needs only authentication
    (plan.md decision 2, amended). `principals` is a directory of people
    and groups, so it also needs `roleAssignments/write` at some scope:
    the people who use the picker, including a project-scoped
    administrator.
  - Creating a custom role needs `roleDefinitions/write` at every
    assignable scope; deleting one, delete at all of them (Azure's rule).
    There is no update yet.
  - Listing assignments with `?scope=` needs read there and returns those
    made there and inherited from above (`inherited: true`); without it,
    each is shown only where the caller may read assignments.
  - Assignment scopes must exist; duplicates are 409; removing the last
    Owner at `/` is 409 (the caller may, the state forbids it); delegation
    refusals are 403 and audited as refused mutations.

- **Do**:
  - `GET`/`POST /v1/authorization/roleDefinitions` (custom roles;
    built-ins listed read-only);
  - `GET`/`POST`/`DELETE /v1/authorization/roleAssignments` (filter by
    scope and by principal);
  - `GET /v1/authorization/permissions?scope=`;
  - `GET /v1/authorization/principals` (the registry).
  - Enforce Azure's rules and decision 10's delegation.
- **Files**: `api/authorization_handler.go` and tests.
- **Verify**: `go test ./api/...` with AC 6 (constrained delegation), AC
  7 (custom roles, refusing deletion while assigned) and AC 8's
  last-Owner refusal.

## T007 — `cmd/serendipd`: bootstrap, API-key migration, break-glass — DONE 2026-10-18

- **Notes:** the API-key rules (migration, and removed-key cleanup that
  keeps the last Owner at `/`) live together in `api/server.go`
  `migrateAPIKeys`, run by `NewServer`. The bootstrap administrator and
  `serendipd break-glass grant-owner` are in `cmd/serendipd`. The
  bootstrap condition is amended (plan.md decision 7). Break-glass refuses
  a missing store file, an unknown or shared username, and is idempotent.
  `api.PolicyRebuildInterval` is a variable so tests can shorten it.

- **Do**:
  - The bootstrap administrator gets Owner and Secrets Officer (decision
    7, per T001).
  - API keys get Owner at `/` if they have no assignment yet; a removed
    key's assignments are removed, unless that would leave no Owner at
    `/`.
  - Add the `serendipd break-glass grant-owner` subcommand.
- **Files**: `cmd/serendipd/{main.go,wiring.go,breakglass.go}` and tests.
- **Verify**: tests for:
  - **bootstrap:** a fresh store gets the admin's assignments; a second
    start changes nothing;
  - **key migration:** a removed key loses its assignments unless it's
    the last Owner;
  - **break-glass:** writes the assignment with an audit row marked
    `break-glass`, and a running server picks it up within 30 s.

## T008 [P] — Console: Access control, Check access, permission-aware controls — DONE 2026-10-18

- **Notes (deviations from decision 11, for review):**
  - **Removing an assignment:** first shipped without it (DESIGN.md's "no
    delete" rule); the user then decided it's allowed, since it's guarded
    by RBAC. It's confirmed first with the `DELETE` shown, offered only
    with `roleAssignments/delete` at the scope, and, as in Azure, only
    where the assignment was made (inherited ones are removed at their
    source). The exception is written into `ui/CLAUDE.md` and DESIGN.md.
  - **A page per resource, Azure's blade:** "Access control (IAM)" at
    `/access?scope=…`, linked from Overview, Application and Component
    pages, with Azure's views in order: Check access, Role assignments
    (with Add), Roles. The user asked to keep it close to Azure, whose
    IAM is a per-resource blade.
  - **Check access is computed in the browser** from the assignments at
    the scope, the role definitions and the person's groups at their last
    sign-in. The API has no "someone else's effective permissions"
    endpoint; the page says where group membership comes from.

- **Do**: plan.md decision 11, using only the existing design system.
- **Files**: `ui/src/{api,screens,components}` and tests.
- **Verify**:
  - `pnpm test`:
    - a Reader sees no create, edit or delete controls;
    - adding an assignment sends exactly the request shown;
    - inherited assignments are labelled with their source.
  - typecheck, the bundle budget, the contrast check, and the
    no-raw-HTML grep.

## T009 [P] [A] — Benchmarks — DONE 2026-10-18 (dev machine, and the simulated T470 alongside T010)

- **Notes:** the first run failed the list target (+8.8 ms); the fix is
  the ownership cache in `core/policy`. Results and raw files:
  `profiles/edge-1/bench/results/2026-10-18-spec017-authz/`. p99 is
  measured on Linux (WSL2, a cross-compiled test binary) because the
  Windows timer is too coarse. The simulated-T470 re-run is deferred to
  T010, which needs the same kind cluster.

- **Do**: Add an `api` benchmark for single-resource and 100-item list
  requests with authorization, extend the bench harness if needed, and
  run it on the simulated T470.
- **Files**: `api/authz_bench_test.go`, `profiles/edge-1/bench/` (a write-up
  and results).
- **Verify**: the numbers are recorded against decision 12's targets. A
  missed target is a FAIL, never hidden.

## T010 — Real-cluster verification (kind) — DONE 2026-10-18 (`verification.md`)

- **Notes:** one caveat (AC 11). The Add role assignment form wasn't
  clicked through by a person in a real browser. The same request was
  sent through the API on the cluster and the person's access changed,
  and a UI test proves the form sends exactly that request. AC 2, 6 and 7
  are covered by tests, not repeated on the cluster.

- **Do**: On a fresh install:
  - sign in as the bootstrap admin (Owner);
  - create a second ThunderID user, plus a group if T001 allows;
  - assign Reader at an Application;
  - sign in as that user in a browser and check AC 1, 3, 5 and 11;
  - check the break-glass on the real database;
  - re-run the spec 016 checks (CSP, no tokens in the browser).
- **Files**: `specs/017-authorization/verification.md`.
- **Verify**: every acceptance criterion has observed evidence or an
  explicit "not verified, because …".

## T011 — Final reviews — DONE 2026-10-18

- **contract-guardian:** blocked on two honesty points, both fixed.
  - The simulated-T470 latency was never measured; it's now measured
    under the T470's CPU limits (+36 µs p99, +0.24 ms per list; PASS).
  - The AC 11 caveat is now recorded here, under T010.
  - Also done: the last-Owner refusal is audited, and plan decision 11
    is amended.
- **go-reviewer:** PASS. Done from its notes:
  - no internal error text in 5xx responses;
  - one scope-chain walker;
  - per-scope decisions when listing assignments;
  - fail-closed and concurrent last-Owner tests (plus a test-fixture race
    the new test exposed).
- **/security-review:** one HIGH finding, confirmed by an independent
  false-positive check: Secret path aliasing across scopes. Fixed (see
  plan.md Risks) and covered by tests. Two below-the-bar notes are
  recorded in plan.md Risks.

- **Do**: Run `contract-guardian`, `go-reviewer` and `/security-review`
  over the spec's commits.
- **Verify**: all PASS, or their blocking findings are fixed and
  re-reviewed.

## T012 [A] — Docs + PROGRESS — DONE 2026-10-18 (docs-keeper: drift found and fixed in place)

- **Do**:
  - `docs/architecture/authorization.md` (the model, the Azure mapping,
    the built-in roles, the evaluation, break-glass);
  - ADR 0014 (adopting Azure's model);
  - `api/CLAUDE.md`: every new route needs a route-table entry;
  - `identity.md` (subject, groups);
  - `docs/PROGRESS.md`.
- **Verify**: docs-keeper reports no drift.
