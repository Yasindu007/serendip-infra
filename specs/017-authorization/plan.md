# Plan 017: Authorization (Azure-style roles at scopes)

Status: Approved 2026-10-18

Spec: `spec.md` (approved 2026-10-18). Azure facts come from a research
pass over Microsoft Learn on 2026-10-18, summarised under "Azure reference"
below. ThunderID facts come from an oss-researcher pass, under "Upstream
facts". Items marked **(spike)** are settled by T001 on a live install
before code depends on them.

## Approach in one paragraph

A new `core/policy` package evaluates every `/v1` request the way Azure
Resource Manager does:
1. resolve the target to a **scope**: the resource and its owner chain up
   to `/`, through the existing ownership edges;
2. collect the **role assignments** at that scope and above, for the
   caller and the caller's groups;
3. take the **union** of each role's `actions − notActions`, and
   separately `dataActions − notDataActions`;
4. allow the request when the route's **action** is covered.

Role definitions (built-in roles in code, custom roles in the store) and
role assignments are new resource kinds, served by a dedicated handler
that enforces Azure's rules (`assignableScopes`, constrained delegation,
no deleting an assigned custom role) plus ours (the last Owner at `/`).

An authorization middleware sits after `AuthMiddleware`. It maps each
route to an action through one table, so an unmapped route fails closed.
Lists are filtered per item. Every decision can explain itself: the
granting assignment is written to `policy_decision`, and denied mutations
are audited.

The console gains an Access control page and permission-aware controls,
driven by `GET /v1/authorization/permissions`.

## Decisions

1. **Scopes are resource references, not path strings.**
   - An assignment's scope is `/` or `{kind, id}`. Inheritance is computed
     at evaluation time by walking `owns` edges upward
     (`graph.Ancestors`, new).
   - Moving or renaming a resource never orphans an assignment, a known
     annoyance with Azure's path-string scopes.
   - The console still displays a readable path, e.g.
     `/ › acme › storefront › prod › web`.
   - A resource with no owner (today's Applications) sits directly under
     `/`.
2. **Actions and the route table.**
   - Actions are `serendip/{resourceType}/{read|write|delete|action}`,
     where `resourceType` is the API path segment (`components`,
     `secrets`…).
   - Data actions are `serendip/secrets/value/read` and
     `serendip/secrets/value/write`.
   - The authorization API's own actions are
     `serendip/authorization/roleAssignments/{read,write,delete}` and
     `.../roleDefinitions/{read,write,delete}`.
   - Platform-wide reads, evaluated at `/`: `serendip/readiness/read` and
     `serendip/capabilities/*`.
   - `GET /v1/session` and `GET /v1/authorization/permissions` need only
     authentication: anyone may ask who they are and what they can do. The
     table marks them explicitly as `authenticated-only`, not unmapped.
   - **Amended in T006:** reading role definitions is authenticated-only
     too. Role definitions aren't secret, and someone with access to one
     project needs the role names to understand it, which a read action at
     `/` would deny them. `GET /v1/authorization/principals` is a directory
     of people and groups, so its handler additionally requires
     `roleAssignments/write` at some scope: exactly the callers who pick
     whom to assign.
   - **The route table is the single source.** A middleware looks up the
     mux pattern (`r.Pattern`, Go 1.23+) in it. The startup check and a
     unit test both fail if any registered route has no entry, so the
     design fails closed (AC 9).
3. **Target scope per verb:**
   - **Read, update and delete:** the resource itself, which inherits its
     ancestors' assignments.
   - **Create:** the would-be parent from the request's `ownerRefs`, or
     `/` when there is none. In Azure terms, creating in a resource group
     needs write at that group's scope.
   - **Graph routes:** read on the named resource.
   - **Secret value routes:** the data action on the Secret.
4. **List filtering.** A list is fetched as today, then each item is kept
   only if the caller has `read` at its scope. Ancestor walks are memoised
   per request. Lists are small on edge-1; the cost is measured in T009.
5. **Storage.**
   - `RoleAssignment` and custom `RoleDefinition` are ordinary resource
     kinds in the existing SQLite store, with audit and events for free,
     behind a dedicated handler like `SecretHandler`, because the
     validation is specific.
   - Built-in roles live in code with stable UUIDs: Azure gives built-ins
     the same GUID everywhere, and scripts should reference the ID.
   - `core/policy` keeps an in-memory index of assignments and definitions,
     rebuilt from the store on start and updated from the event bus. A
     single process makes this safe, and evaluation needs no SQL apart
     from the memoised ancestor walk.
6. **Principals:**
   - **person** `{type: person, id: <sub>}`. Spec 015 deferred recording
     `sub`; authorization needs a stable ID, so `identity.Principal.Subject`
     becomes the key. The audit actor stays `person:<username>` for
     readability, and the subject is added to the record.
   - **group** `{type: group, id: <name>}`, from the token's group claim
     (decision 9).
   - **apiKey** `{type: apiKey, id: <actor name>}`.
   - **agent:** reserved. It must carry a sponsor (a person principal);
     there's no way to authenticate one until spec 018.
   - **The known-principals registry.** A small table of principals
     `serendipd` has seen (sub, username, groups, last seen), so the
     console can pick people when assigning. That's the equivalent of
     Azure picking from the directory; there's no directory API here,
     because the Service proxy strips the `Authorization` header that
     ThunderID's management API needs.
     It's an observation, not a resource: its writes aren't audited, and
     a failed write is logged without failing the request (T004).
7. **Bootstrap and lockout:**
   - **Bootstrap administrator.** On first start, when no assignment
     exists at all, the bootstrap admin gets Owner and Secrets Officer at
     `/`. Its subject is ThunderID's fixed bootstrap ID
     `01900000-0000-7000-8000-000000000030` (Upstream fact 3), which lives
     in the ThunderID provider, not in `core/`. T001 confirms it survives
     two fresh installs.
   - **Amended in T007:** the condition is, per role, "the bootstrap
     administrator has never held it" (from the audit log), not "no
     assignment exists at all". Per role, so a start interrupted between
     the two grants finishes the second next time. An install upgraded from spec 016 already
     has its API keys' assignments, and its administrator would otherwise
     get 403 on everything. A second start still changes nothing, and an
     operator who removes the administrator's roles isn't overruled.
   - **API keys.** Each key in `SERENDIP_API_KEYS` gets Owner at `/` if it
     has no assignment yet, recorded with the description "migrated from
     unscoped API key (spec 017)".
   - **Removed keys.** When a key disappears from config, its assignments
     are removed at startup, unless that would remove the last Owner at
     `/`. Then they stay, with a warning.
   - **The last Owner at `/`.** Deleting an Owner assignment at `/` is
     refused when it's the only one left. Any other Owner can be removed.
   - **Break-glass:** `serendipd break-glass grant-owner --subject <sub>`
     (or `--username`, resolved through the registry).
     - It opens the store directly. That needs host access to the
       database, never the API.
     - It writes one Owner-at-`/` assignment, audited with the interface
       `break-glass`, and prints a warning.
     - It can run while `serendipd` is running: SQLite WAL allows a second
       writer. `serendipd` picks up the change within 30 s through its
       periodic index rebuild.
8. **The decision record.** `policy.Decide` returns
   `{Effect: allow|deny|needsApproval, Evidence: {assignmentID, roleName,
   scope}}`.
   - **Allowed mutations** write `"allow: <role> (<assignment id>) at
     <scope>"` to `policy_decision`.
   - **Denied mutations** write an audit row with no before or after,
     `policy_decision = "deny: missing <action> at <scope>"`, and the
     request ID. Denied reads aren't audited (as in Azure); they're
     logged.
9. **Groups** (Upstream facts 1–2).
   - **Fresh installs:** the bootstrap `serendip` client adds `groups` to
     its token attributes, so access tokens carry the user's group names.
   - **Reading them:** the provider-neutral verifier reads a configurable
     claim (default `groups`) into `Principal.Groups`, capped at 100
     names, each at most 128 characters.
   - **Existing installs:** bootstrap files import only on first install,
     so they need the attribute added once.
     `cmd/serendip-identity-redirects` (spec 016's option B tool) is
     generalised to also ensure the `groups` attribute. Until then, group
     assignments are stored but match nobody, and the console says why.
   - **Managing groups:** creating groups and adding people stays in
     ThunderID, through its management API over a port-forward, as with
     adding users today.
10. **Constrained delegation** (Azure's RBAC Administrator with a
    condition), without the ABAC language. An assignment may carry
    `delegation: {roleDefinitionIds: [...], principalTypes: [...]}`. When the caller
    creates or removes an assignment, the role and principal type must be
    allowed by some delegation-carrying assignment that grants
    `roleAssignments/write` at that scope.
    - **Nobody may assign a role to themselves** through a delegated
      grant. That's Azure's documented risk: "can assign any role to any
      user… including themselves".
    - An Owner, or a User Access Administrator without delegation, is
      unrestricted, as in Azure.
    - **No minting wider administrators** (added in T006, from review).
      When a delegate grants a role that can itself assign roles, the new
      assignment must carry a delegation within the delegate's own (its
      roles and principal types a subset). Otherwise a constrained
      delegate could create an unconstrained one.
11. **Console:**
    - **An Access control tab** on Overview (scope `/`), Application and
      Component pages. It lists the assignments at this scope and
      inherited ones (with where they come from), and adds or removes
      them through a dialog that shows the API request.
    - **Check access:** pick a principal and see its effective actions
      here.
    - **Permission-aware controls:** buttons the caller can't use are
      hidden (create) or disabled with a reason (edit). They come from
      `GET /v1/authorization/permissions?kind=&id=`, cached per page.
    - Built on the existing components and tokens only (DESIGN.md).
12. **Performance target.** Authorization adds **p99 < 100 µs** per
    single-resource request and **< 2 ms** to a 100-item list on the dev
    machine, measured with an `api` benchmark like spec 016's
    `BenchmarkAuth`. `serendipd` idle RSS grows by **< 5 MiB**. All are
    re-measured on the simulated T470.

## Azure reference (Microsoft Learn, 2026-10-18)

- **Model:** the assignment is (principal, role definition, scope). A role
  definition is `actions`, `notActions`, `dataActions`, `notDataActions`,
  `assignableScopes`. Built-in roles keep stable GUIDs. One wildcard per
  action string, case-insensitive.
- **Scopes:** management group → subscription → resource group →
  resource. Access only adds up downward; a lower scope can't remove it.
  Only deny assignments subtract, and customers can't create them
  (private preview only).
- **Data plane:** an Owner (`actions: *`) gets **no** data actions. In Key
  Vault:
  - Secrets Officer = `vaults/secrets/*` data actions;
  - Secrets User = `getSecret` plus `readMetadata`;
  - Reader = metadata only.
- **Built-ins:**
  - Owner `*`;
  - Contributor `*` minus `Microsoft.Authorization/*/Write` and others;
  - Reader `*/read`;
  - User Access Administrator `Microsoft.Authorization/*`;
  - RBAC Administrator `roleAssignments/write|delete`, meant to be
    constrained;
  - "privileged" means any role with `*`, `*/write`, `*/delete` or
    assignment/definition writes.
- **Evaluation:** deny assignments first; then the union of the
  principal's and its groups' (transitive) assignments at the scope and
  above; `notActions` subtracts, it doesn't deny; then conditions.
- **Custom roles:** `assignableScopes` is required and can't be `/` or a
  wildcard; a role can't be deleted while assigned.
- **PIM (for spec 019):** eligible vs active assignments, time-bound
  activation (1–24 h), justification, an approver who "doesn't have to
  have any roles", settings per role and scope, not inherited.
- **Locks (spec 019 candidate):** CanNotDelete and ReadOnly override even
  Owner, are inherited, and need `Microsoft.Authorization/locks/*`.
- **Agents:** Microsoft Entra Agent ID agent identities are service
  principals that get RBAC roles and need a human **sponsor**.
- **Audit:** the Activity Log records assignment and definition writes,
  sometimes with "evidence" of the granting assignment, for 90 days; it
  doesn't record reads.

## Upstream facts (ThunderID 1.0.1)

From source at tag v1.0.1, read via an oss-researcher pass on 2026-10-18.
T001 confirms them empirically before code depends on them.

1. **Groups in tokens: yes.** Add the string `groups` to the application's
   token attribute allow-list, exactly as the bootstrap `CONSOLE` app does
   (`given_name, family_name, email, groups, name, ouId`). There's no
   separate scope.
   - The `groups` claim is a **flat list of group names**. Nested groups
     are resolved transitively.
   - It's omitted when the user has no groups.
   - `roles` works the same way; we don't use it, since Serendip's roles
     are authoritative.
   - *To confirm in T001:* that the allow-list the flow reads
     (`Assertion.UserAttributes`) is the API's
     `token.accessToken.userConfig.attributes`.
2. **Groups come with the install.** ThunderID creates an
   `Administrators` group containing the bootstrap admin, plus an
   `Administrator` role assigned to that group. Groups are managed through
   `/groups` (create, and add or remove members) with the `system` scope,
   which means a port-forward, because the Service proxy strips
   `Authorization`.
3. **The bootstrap admin's ID is fixed**: `01900000-0000-7000-8000-000000000030`,
   from the bootstrap resources. The token `sub` defaults to that entity
   ID; spec 015's spike observed exactly this `sub`. So the bootstrap
   Owner assignment can be keyed to the subject from the first start.
4. **Machine and agent tokens** (`client_credentials`), for spec 018:
   - `sub` is the application's entity ID, and the token carries a
     `client_id` claim with no `username`;
   - extra claims come from a separate allow-list
     (`token.accessToken.clientConfig.attributes`) and can include
     `groups`;
   - **agent entities additionally carry `name` and `owner` claims**,
     which is how an agent can be told apart, and `owner` maps naturally to
     the sponsor.

## Spike findings (T001, 2026-10-18)

**Setup.** A fresh kind cluster with the spec 016 stack. The bootstrap
`serendip` client's token attributes were `username, email, name, groups`
(T004's change, made early). A scratch driver ran ThunderID's sign-in flow
and the management API over a port-forward. It printed claims only, never
tokens or passwords.

1. **The bootstrap admin's `sub` is fixed:**
   `01900000-0000-7000-8000-000000000030`. This is a second install
   showing the same value as spec 015's spike, and it's the literal ID in
   the bootstrap resources. Decision 7 stands as written.
2. **Groups arrive in the access token as names, transitively.**
   - Admin: `["Administrators"]`.
   - A user in `platform2`, where `eng2` contains `platform2`:
     `["eng2","platform2"]`.
   - A user in no group: **no claim at all**, so absent means empty.
   - **ID tokens never carry `groups`**, even with the attribute in
     `idToken.userAttributes`. Authorization therefore reads groups from
     the **access token**, which is what the verifier already checks.
3. **Groups and members are created through ThunderID's management API**
   (`POST /groups` with `members` of type `user` or `group`): 201 each
   time. A later login reflects them.
4. **Existing installs need no reinstall.** Removing `groups` from the
   client's attributes through the management API (PUT, 200) dropped the
   claim on the next login, and adding it back restored it. That confirms
   the operator tool's approach (decision 9), and that the allow-list the
   flow reads is the API's `token.accessToken.userConfig.attributes`.

## Interfaces and types created

- `core/resource`:
  - `RoleDefinitionSpec`: `roleName`, `description`, `permissions[]`
    (`actions`, `notActions`, `dataActions`, `notDataActions`),
    `assignableScopes[]`. There is no stored `builtIn` field: built-in
    roles live in `core/policy` code, and the validator is told which kind
    it's checking, so a stored flag can't be spoofed. (Amended in T002.)
  - `RoleAssignmentSpec`: `principal{type,id}`, `roleDefinitionId`,
    `scope` (`/` or `{kind,id}`), `description`, `delegation`;
  - their Status types, and the kinds `RoleDefinition` and
    `RoleAssignment`.
- `core/graph`: `Ancestors(ctx, ref) ([]ResourceRef, error)`, the
  owner chain nearest-first.
- `core/policy` (new, no product names):
  - `Principal{Type, ID, Groups}`;
  - `Decide(ctx, principal, action, target) Decision`;
  - `EffectiveActions(ctx, principal, target)`;
  - the built-in role set;
  - `Index`, the in-memory assignments and definitions, rebuilt and
    event-updated;
  - `RouteTable` types, kept pure data in `api`.
- `core/identity`: `Principal` gains `Groups []string`.
- `api`:
  - `authz.go`: the middleware, the route table and list filtering;
  - `authorization_handler.go`: role definitions, role assignments,
    permissions;
  - the 403 problem shape.
- `cmd/serendipd`: the `break-glass` subcommand, bootstrap assignments,
  and API-key migration.

## Files touched

- `core/resource/authorization.go` (new) and tests
- `core/graph/edges.go` (Ancestors) and tests
- `core/policy/*` (new) and tests
- `core/identity/identity.go`; `providers/identity/oidc/verifier.go`
  (subject, groups); `providers/identity/thunderid/{values.go,clients.go}`
  and `cmd/serendip-identity-redirects` (the `groups` attribute)
- `api/{authz.go,authorization_handler.go,server.go,middleware.go,handler.go,secret_handler.go}`
  and tests
- `core/store/sqlite`: a migration for the known-principals table
- `cmd/serendipd/{main.go,wiring.go,breakglass.go}` and tests
- `ui/src/{api,screens,components}` (Access control, Check access,
  permission-aware controls) and tests
- `docs/architecture/authorization.md` (new), ADR 0014, `api/CLAUDE.md`,
  `docs/PROGRESS.md`

## Risks

- **Every existing test calls `/v1` as an API key with no assignments.**
  Test servers get the same API-key migration (Owner at `/`), so existing
  behaviour is unchanged, and new tests exercise narrower roles.
- **Groups may not exist in ThunderID tokens** (decision 9). It's handled
  honestly either way.
- **Subject vs username.** Existing audit rows have usernames only, so
  the subject applies from this spec on, as documented.
- **The console's reads multiply** (permissions per page). The endpoint
  is cached per page, and its cost is measured in T009.
- **Each decision scans every assignment,** with no index by scope (the
  go-reviewer note in T003). Measured at about 4 µs with 101 assignments
  and a five-level chain. An index by scope is the first optimisation if
  T009's list benchmarks need it.
- **Assignment mutations must hold `Engine.LockAssignments`** across the
  check and the store write (T003). Otherwise two concurrent removals of
  different root Owners could both pass the last-Owner check. The API
  handler in T006 does this.
- **Enforcement details settled in T005** (from the reviews):
  - A create or update body is decoded for its owners exactly as the
    handler decodes it, and one the check can't read is refused there
    (400, or 413), never handed on. Handing it on was a bypass: the two
    decoders disagreed on trailing data.
  - `?cascade=true` needs delete on every descendant, by its own kind.
    A descendant with a second owner elsewhere is still covered by
    inheritance from the resource being deleted, as in Azure.
  - A PUT needs write at every owner it adds or removes, and at `/` when
    it removes them all.
  - An API key is migrated to Owner only if it has never had an
    assignment (judged from the audit log), so revoking a key's access
    survives a restart. Revoking a key entirely means removing it from
    `SERENDIP_API_KEYS`.
  - Lists cost one decision per item; measured in T009.
  - The owners and descendants a request is checked against are read
    just before the store's transaction, so a child added in between
    could be cascaded without its own check. The window is small on a
    single-node SQLite store; closing it needs the check inside the
    transaction.
  - Removing a key from `SERENDIP_API_KEYS`, even by mistake (an empty
    or mistyped config), deletes its assignments at the next start,
    narrowed ones included, except the last Owner at `/` (T007). Restoring
    the key doesn't restore them: it was assigned before, so it isn't
    migrated again.
  - `EverAssigned` scans the audit log once per API key at startup, and
    relies on role-assignment audit rows never being pruned. Any future
    audit retention must keep them.
  - A handler mounted on a plain mux (unit tests only) isn't filtered.
    Production mounts every route through the authorizing mux.
  - Each `NewServer` starts the policy index's 30 s tick, stopped by
    `Server.Shutdown`. Tests that never shut down leave it running until
    the process exits.
- **Lockout through key removal** is guarded (decision 7); break-glass is
  the backstop.

## Explicitly NOT done

- approvals, PIM, locks (spec 019);
- deny assignments;
- the ABAC language;
- agent token recognition (spec 018);
- nested groups;
- a Serendip-managed group directory;
- Organization-level multi-tenancy.
