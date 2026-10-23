# Authorization

Who may do what, where. Spec 017 (`specs/017-authorization/`), ADR 0014.
Serendip copies **Azure RBAC**: roles at scopes decide authority, the
industry-standard model that most operators already know. Approvals (spec
019, `approvals.md`) are the exception path, not the default.

## The model

```
role definition  = what may be done (actions, notActions, dataActions, notDataActions)
role assignment  = principal + role + scope
scope            = "/" or one resource; applies to everything that resource owns
```

- **Scopes inherit down the ownership tree.** An assignment at a Project
  applies to its Environments, Applications, Components and Secrets, and
  to the Deployments in its Environments (spec 020: an Environment is the
  resource group you deploy into, ADR 0019). The
  tree is the resources' `ownerRefs` ("owns" edges in the graph).
  A scope references a resource by `{kind, id}`, not a path, so renaming
  or moving never orphans an assignment.
- **Access is additive.** A principal's permissions at a resource are the
  union of every role assigned to it, or to its groups, at that resource
  or any ancestor. There are no deny assignments. `notActions` subtracts
  within its own role only; a role assigned lower down never takes access
  away.
- **Control plane and data plane are separate.** `actions` cover managing
  resources (`serendip/{resourceType}/{read|write|delete}`). `dataActions`
  cover what a resource holds; today that's a Secret's value
  (`serendip/secrets/value/{read|write}`). Owner has `*` actions and no
  data actions, so it can't read a Secret's value without a Secrets role.
- **Wildcards:** one `*` per pattern, matching any run of characters
  including `/`; case-insensitive (`*/read`, `serendip/components/*`).

### Principals

| Type | ID | From |
|---|---|---|
| `person` | the identity provider's subject (`sub`) | OIDC access token, or the console session |
| `group` | the group's name | the access token's `groups` claim |
| `apiKey` | the key's actor name | `SERENDIP_API_KEYS` |
| `agent` | `<issuer alias>/<sub>` | a token from a trusted agent issuer (spec 018, `identity.md`); must carry a person sponsor |

A person acting through an AI assistant (spec 021) is not a fifth type: it
is a `person` principal with `Via` set to the assistant's client
(`serendip-mcp`). Their own assignments and groups apply, plus the agent
guardrails below. A role assignment can never carry `Via` (400): roles are
assigned to the person, never to their assistant.

A person's groups come from **their current access token** at every
request. A group change applies when their next token is issued (their
next sign-in, or the console's session refresh), as in Azure.

### Built-in roles

Fixed IDs, the same on every install (`core/policy/builtins.go`):

| Role | Actions | Data actions |
|---|---|---|
| Owner | `*` | none |
| Contributor | `*` except `serendip/authorization/*/write` and `serendip/authorization/*/delete` | none |
| Reader | `*/read` except `serendip/components/logs/read` | none |
| User Access Administrator | `*/read`, `serendip/authorization/*`; no logs | none |
| Role Based Access Control Administrator | `*/read`, role assignments write and delete; no logs | none |
| Secrets Officer | `serendip/secrets/*` | `serendip/secrets/value/*` |
| Secrets User | `serendip/secrets/read` | `serendip/secrets/value/read` |
| Log Reader | `*/read`, including logs | none |

**Logs are their own action** (`serendip/components/logs/read`, spec 020):
they can carry sensitive data, so the read-only and access-management
roles leave them out. Log Reader adds them; Owner and Contributor have
them.

Built-in roles can't be changed or deleted (400). **Custom roles**
(`POST /v1/authorization/roleDefinitions`):
- They must name assignable scopes, never `/`.
- Creating one needs `roleDefinitions/write` at every scope it's assignable
  at, and deleting one needs delete at all of them.
- Names are unique, built-in ones included, compared without case (409).
- One that's still assigned can't be deleted (409).

**Role assignments** (`POST /v1/authorization/roleAssignments`): the scope
must exist (400), an identical principal, role and scope is refused (409),
and the server chooses the ID. `GET .../roleAssignments?scope=` lists those
that apply there, made there or inherited, and needs read at that scope.

### Constrained delegation

An assignment may carry `delegation: {roleDefinitionIds, principalTypes}`,
like Azure's RBAC Administrator with a condition. A caller whose grant to
assign roles comes only from such assignments:
- may grant only the listed roles, to the listed principal types;
- may not grant to themselves or to a group they're in;
- when granting a role that can itself assign roles, must pass on a
  delegation within their own, so a constrained delegate can't mint a
  wider administrator.

An Owner or User Access Administrator without a delegation is
unrestricted, as in Azure.

## Enforcement (`api/authz.go`)

- **Every route needs a rule.** Every `/v1` route is registered through an
  authorizing mux. A route missing from `routeTable()` panics at startup,
  and a test enumerates every registered route, so nothing can be added
  unchecked.
- **The target scope depends on the request:**
  - read, update and delete are decided at the resource itself;
  - create is decided at each `ownerRef` in the body, or `/`;
  - an update that adds or removes an owner also needs write at each
    owner gained or lost, and at `/` when it drops them all;
  - `?cascade=true` needs delete on every descendant, by its own kind;
  - Secret values use the data actions.
- **The body is checked as the handler reads it.** Owners and scopes are
  read from it with the same decoder the handler uses, and a body the
  check can't read is refused (400/413), never handed on.
- **Platform-wide routes** (`GET /v1/readiness`, a capability's default
  provider, `GET /v1/platform`, `GET /v1/packages`) are decided at `/`.
- **Packages** (spec 020): creating or updating a `ProviderInstance` is
  decided at `/`, whatever owner the body names, and the handler refuses
  any owner, so removing one (decided at the instance) inherits from `/`
  alone. A Project's Contributor can't install a package for everyone.
- **Deployments: read the source, write the target** (spec 020, ADR 0019).
  - **Deploying or changing** a Deployment needs
    `serendip/deployments/write` at its Environment, by the route's rule.
    The handler also checks read on the Component it runs.
  - **Promoting** needs read on the source Deployment (the route's rule),
    and the handler checks `serendip/deployments/write` at the target
    Environment.
  - **Declaring a dependency** needs read on the Component called.
  - **Runtime state** (`…/runtime`, `…/events`) needs
    `serendip/components/runtime/read`; **logs** need their own
    `serendip/components/logs/read`, at the Deployment.
  - **These handler checks** are refused, and audited, exactly like the
    route's own (below).
- **Lists and graph results** keep only what the caller may read.
- **A refusal** is 403 naming the missing action and scope.
- **Authenticated only:** `GET /v1/session`,
  `GET /v1/authorization/permissions` and reading role definitions.
  `GET /v1/authorization/principals` is a directory of people and groups,
  so it also requires being able to assign roles somewhere.

## The evaluation (`core/policy`)

`Engine.Decide(principal, action, scope)`:
1. Builds the scope chain: the target, its ancestors nearest first, then
   `/`.
2. Collects the assignments to the principal or any of its groups on that
   chain.
3. Allows if any of their roles' permission blocks grants the action
   (actions minus notActions, or data actions minus notDataActions).
4. The evidence is the nearest such assignment.

Keeping it fast and current:
- **The assignment index** is in memory. Change events mark it stale, and
  a 30 s tick catches writes from other processes (break-glass).
- **Ownership is cached** per resource (its direct owners). A change event
  drops only that resource's entry, and a generation counter stops a
  lookup that raced a change from storing stale data.
- **Measured cost** (`profiles/edge-1/bench/results/2026-10-18-spec017-authz/`):
  about +10 µs per request (p99 +36 µs) and +0.24 ms p99 per 100-item list
  on the simulated T470. Idle memory: +0.7 MiB on the dev machine, +1.8 MiB
  on the simulated T470 (target < 5 MiB).
- **Assignment changes** hold `Engine.LockAssignments` across the check and
  the write, so two concurrent removals can't both pass the last-Owner
  check.

## Agents (spec 018)

Constitution 5 holds AI to more than roles do. For a principal of type
`agent`, `core/policy` (`agents.go`) adds the following. People and API
keys are unaffected.

**Delegated principals (spec 021, ADR 0018).** The same guardrails apply to
a person acting through an AI assistant (`Via` set). One predicate,
`Principal.guarded()` in `engine.go`, is true for an agent or a `Via`
person, and `Decide`, `EffectivePermissions`, `checkDelegated` and
`GrantedAnywhere` all use it. The person's own roles decide first (so the
assistant is capped at them), then:
- access management is denied whatever their roles say, and the
  permissions API doesn't claim it;
- every delete their roles allow returns `NeedsApproval`;
- the person's groups do apply, unlike an agent's. The sponsor and
  group-assignment rules below concern agents only.

The same person acting directly (no `Via`) is unaffected, so Owner keeps
`DELETE` and access management in the console and the CLI.

- **Never authority over access.**
  - **At evaluation:** `Decide` denies an agent `roleAssignments/write`
    and `/delete`, and `roleDefinitions/write` and `/delete`, whatever
    its roles say.
  - **For delegation:** `checkDelegated` refuses an agent caller
    outright.
  - **At assignment:** `CheckAssign` refuses to give an agent a role whose
    permissions grant any of those four actions, wildcards included
    (`AgentMayHold`). That means Owner, User Access Administrator, RBAC
    Administrator and such custom roles; the refusal is a 400 "agent
    guardrail". Agents can hold Contributor, Reader, Secrets Officer and
    Secrets User.
  - **Elsewhere:** `GrantedAnywhere` and `EffectivePermissions` agree, so
    an agent can't read the directory, and the permissions API doesn't
    claim rights it lacks.
- **Deletes need an approval.** Any `…/delete` action an agent's roles
  allow, control or data plane, cascade included, returns `NeedsApproval`.
  - The API answers 403 with problem type
    `urn:serendip:problem:needs-approval` and title "approval required",
    and nothing changes.
  - The refusal is audited as `needsApproval: <action> at <scope>, allowed
    by <role> (<assignment>) at <scope>: an agent's delete needs an
    approval`.
  - The detail names the way forward: `POST /v1/approvals` with the
    action and a reason creates a `Pending` approval, naming the agent's
    sponsor. A person who may delete the target approves it, and the
    approval reconciler deletes it (`approvals.md`). An agent can never
    approve, its own request or anyone's.
  - A test pins every DELETE route to an action ending in `/delete`.
- **One sponsor per agent.** Every assignment for an agent names the same
  person sponsor. A different one is refused, after the caller's own
  authority is checked, so nothing about the agent leaks to a caller who
  couldn't assign anyway.
  - The engine indexes sponsors at reload (`SponsorOf`).
- **No groups.** Agent tokens' group claims are never read, so a group
  assignment never applies to an agent.
- **The role view** carries `assignableToAgents`, worked out by
  `AgentMayHold`, so clients offer only the roles the server accepts.
- **Measured cost** (spec 018 T010): an agent request costs the same as a
  person's on Linux, and adds +56 µs p99 on the simulated T470.

## Audit

Every mutation's audit row carries the subject of the person behind it
(`actor_subject`) and the decision that allowed it (`policy_decision`:
`allow: <role> (<assignment>) at <scope>`). A refused mutation leaves its
own row with no before or after (`deny: missing <action> at <scope>`).
That includes a check a handler makes beyond its route's rule, such as
promote's write at the target Environment (`auditRefusal`, spec 020 T016:
found missing on a real cluster). Refused reads are logged, not audited,
as in Azure.

Every delete also records an Approval (`approvals.md`, spec 019). The
request, the decision and the execution each have their own row. A direct
`DELETE` keeps the `allow: …` evidence. A delete that an approved request
runs carries `approved: <approval> by <approver or policy>; requested by
<requester>`. A refused approval decision is `deny: policy: forbidden: …`,
and a request that waits is `needsApproval: …`.

## Bootstrap, lockout and recovery

- **Bootstrap administrator:** on start, the identity provider's bootstrap
  administrator (ThunderID's fixed subject, kept in its provider) gets
  Owner and Secrets Officer at `/`, each role only if it has never held
  it. So an upgrade gets a working administrator, a restart never
  overrules a removal, and an interrupted start finishes.
- **API keys:**
  - A key in `SERENDIP_API_KEYS` that has never had an assignment becomes
    Owner at `/`, so a key the operator revoked stays revoked.
  - A key removed from config loses its assignments at the next start,
    except the last Owner at `/`.
- **The last Owner at `/`** can't be removed (409, audited).
- **Break-glass:** `serendipd break-glass grant-owner --subject <sub>` (or
  `--username`, resolved among people who have signed in) restores an
  Owner, like Azure's "elevate access".
  - It needs host access to the store file, never the API.
  - It's audited with interface `break-glass` and prints a warning.
  - A running `serendipd` applies it within 30 s.

## The console

- **Access control (IAM)** is a page per scope (Overview, Application,
  Component), laid out like Azure's blade:
  - **Check access** works in the browser, from the assignments and the
    person's groups as of their last sign-in.
  - **Role assignments** shows those made here and those inherited from
    above, marked with their source. Assignments can be added, and removed
    with a confirmation. Removing one revokes access and destroys nothing;
    like every delete it records an Approval, decided by policy. An
    inherited assignment is removed where it was made, not here.
  - **Roles** lists every role.
- **Controls follow `GET /v1/authorization/permissions`:**
  - create actions are hidden when not allowed;
  - edits are disabled with the reason (`Guarded`);
  - while access is still loading, controls stay disabled.
- **Delete** (spec 019) on the Application, Component and Secret pages is
  shown when the permissions API allows it. It sends the ordinary `DELETE`,
  which records an Approval decided by policy, and shows its ID. The
  Approvals page lists requests waiting for a person, who approves or
  rejects them if they may delete the target (`approvals.md`).

## Secrets and authorization

A Secret's value is stored by its provider under the **Secret's ID and
path**, never at a path alone. Access is decided per Secret, so two
Secrets must never share a value; a shared path would let a Secret in one
project reach another's (spec 017 T011). Paths are validated and can't
change after creation, and Secret IDs are always the server's.

## Known limits

Recorded in `specs/017-authorization/plan.md` Risks:
- **Ownership staleness:** ownership written by another process is picked
  up within 30 s.
- **The audit log:** `EverAssigned` relies on role-assignment audit rows
  being kept.
- **Re-created resources:** assignments at the scope of a deleted
  resource remain, and a resource re-created with the same ID would
  inherit them.
- **Directory reads are for callers who can assign roles somewhere,**
  even at a single project. Such a caller can read the whole directory,
  including any person's groups, the same as with `/principals`. Spec 018
  made the directory live (`identity.md`, ADR 0015).
