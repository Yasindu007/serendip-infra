# Spec 017: Authorization (Azure-style roles at scopes)

Status: Approved 2026-10-18

## Problem

Serendip authenticates callers (API keys, OIDC bearer tokens, console
sessions) but doesn't authorize them. Any signed-in person, and any API
key, can do anything: read every Secret value, delete any resource, change
any capability's provider. Spec 016 shipped the console with this gap
stated plainly. It blocks three things:

- **Real use by a team.** A Viewer must not be able to delete, and a
  developer shouldn't see production Secrets.
- **Spec 019 (Approvals).** "Who may approve", and whether an action needs
  approval at all, should come from roles. Approval is the exception, not
  a step on every change.
- **AI agents and MCP.** An agent must hold narrow, auditable permissions
  and never more (constitution 5).

The user's direction (2026-10-18): **roles decide authority**, modelled as
closely as practical on **Azure RBAC**, because it's industry-accepted.

## The model: Azure RBAC, mapped onto Serendip

The research (Microsoft Learn, 2026-10-18) is summarised in plan.md.
This spec takes Azure's model directly unless it says otherwise.

| Azure | Serendip |
|---|---|
| Security principal: user, group, service principal, managed identity | **Principal:** a person (identity-provider `sub`), a **group** (from the identity provider's group claim), an **API key** (a service principal), an **agent** (reserved, below) |
| Scope: management group → subscription → resource group → resource | **Scope path:** `/` (platform) → Organization → Project → Environment → Application → Component, following ownership. A resource with no owner sits directly under `/`. Infrastructure kinds (Cluster, Node, Site, Capability, Provider) live under `/`. |
| Role definition: `actions`, `notActions`, `dataActions`, `notDataActions`, `assignableScopes` | The same five fields, with stable IDs. Action strings are `serendip/{resourceType}/{read\|write\|delete\|action}` (e.g. `serendip/components/delete`), with at most one `*` wildcard per string, case-insensitive. |
| Role assignment: principal + role + scope, with a description | The same triple plus a description (the justification). Assignments are records attached to their scope. |
| Control plane vs data plane | **Data actions** cover what a resource contains, not the resource itself. In this spec that means reading or writing a **Secret's value**. Owner and Contributor get **no data actions**, exactly as in Azure: managing a Secret doesn't mean reading it. |
| Evaluation: union of the principal's and its groups' assignments at the scope and above; `notActions` subtracts and is not a deny | The same algorithm. Access is additive down the tree, and a lower scope can't remove an inherited permission. |

**Built-in roles**, mirroring Azure's names and meanings:

| Role | Can do | Can't do |
|---|---|---|
| **Owner** | everything on resources (`*`) at the scope, including assigning roles | read or write Secret values (no data actions) |
| **Contributor** | create, update and delete resources at the scope | assign roles (`serendip/authorization/*/write` is excluded) or touch Secret values |
| **Reader** | read resources at the scope, including Secret metadata | any change, or Secret values |
| **User Access Administrator** | read everything, and manage role assignments | change resources |
| **Role Based Access Control Administrator** | create and remove role assignments, limited to an allow-list of roles and principal types set on its own assignment (Azure's "constrained delegation") | assign roles outside that list, or assign itself more |
| **Secrets Officer** | everything on Secrets at the scope, including values (data actions) | anything outside Secrets |
| **Secrets User** | read Secret values (data action) | change or delete Secrets |

**Agents (reserved).** An `agent` principal type exists in the model, and
every agent must name a human **sponsor**, as Microsoft Entra Agent ID
requires. Agents receive roles like any principal. Recognising agent
tokens (for example from WSO2's agent manager, which issues agent
identities through ThunderID) is a later spec. The research found no
approval feature there, so approval stays in Serendip (spec 019).

## Scope — IN

- **Role definitions:**
  - the built-in roles above, read-only, with stable IDs;
  - **custom roles** through the API, with Azure's rules: `assignableScopes`
    is required, can't be `/` or contain wildcards, and the role can't be
    deleted while it's assigned.
- **Role assignments:**
  - created and removed through the API, listed by scope and by principal;
  - each records who created it and why;
  - deleting a principal (an API key removed from config) deletes its
    assignments. Azure leaves orphans behind; we don't.
- **The policy decision point** (the first `core/policy`), consulted on
  every `/v1` request:
  - It maps the route to an action and the target to its scope path.
  - It evaluates the caller's effective permissions.
  - It returns **allow** or **deny**.
  - The interface already has a third answer, **needs approval**, which
    spec 019 uses. Nothing returns it in this spec.
- **Enforcement:**
  - **A denied request gets 403 problem+json** naming the missing action
    and the scope. That's safe to show, since the caller already knows
    the resource ID it asked for.
  - **Lists show only what the caller may read.** Unreadable items are
    left out rather than causing an error, as in Azure.
  - **Secret values require the data action.**
  - **Every `/v1` route has an action**, including reads, the graph,
    readiness and `GET /v1/session`. A route without one fails closed: a
    test enumerates the routes.
- **Bootstrap and lockout safety:**
  - **First start:** the identity provider's bootstrap administrator gets
    Owner and Secrets Officer at `/`.
  - **Static API keys:** today they have full access. Each key keeps an
    Owner assignment at `/`, recorded and logged as such, so scripts keep
    working; operators narrow it afterwards.
  - **The last Owner at `/` can't be removed.** With several Owners,
    any of them can be removed as long as at least one remains. Only the
    final one is protected: with no Owner left, nobody can repair
    access.
  - **Break-glass, like Azure's "elevate access"** (an Entra Global
    Administrator making themselves User Access Administrator):
    - Someone with root on a control-plane host can run a local command
      that grants a named person Owner at `/`.
    - It works only through local host access, never over the API.
    - It is audited with its own interface name, and `serendipd` logs it
      loudly.
    - Recovery-only: it plays no part in normal requests.
- **Groups:** a person's groups come from the identity provider's group
  claim, taken flat with no nested resolution, as research suggests for
  OIDC. Whether ThunderID 1.0.1 emits that claim, and how, is verified in
  plan.md.
- **Who am I and what may I do:** `GET /v1/authorization/permissions?scope=…`
  returns the caller's effective actions at a scope (Azure's
  `permissions` API). The console uses it to hide or disable controls,
  for convenience only, since the server enforces.
- **Audit (constitution 8):**
  - every role-assignment and custom-role change is audited;
  - every allowed mutation's audit row records the deciding assignment in
    `policy_decision` (Azure's "evidence");
  - **denied mutations are logged too**, which Azure doesn't do reliably.
- **The console:**
  - an **Access control** page per scope, like Azure's IAM blade: who has
    which role at this scope, inherited assignments marked as such, and
    adding or removing assignments when permitted;
  - a **Check access** view for a principal;
  - controls the caller can't use are hidden or disabled with a reason.
  - The existing design system applies.
- **Docs:** a new `docs/architecture/authorization.md`, ADR 0014 (adopting
  Azure's model and the mapping above), and `api/CLAUDE.md`.

## Scope — OUT (explicitly)

- **Approvals and PIM-style eligible/just-in-time roles.** Spec 019 will
  use Azure PIM's shape: eligible assignments, time-bound activation,
  justification and approvers.
- **Resource locks** (CanNotDelete, ReadOnly). The research rates them
  cheap and high-value, so they're a candidate for spec 019 or right
  after it.
- **Deny assignments.** Azure doesn't let customers create them either.
- **The ABAC condition language,** apart from constrained delegation's
  allow-list.
- **Recognising agent tokens and WSO2 Agent Manager integration.** The
  `agent` principal type is defined here; spec 018 is the proof of concept
  (agreed 2026-10-18). Agent Manager runs **beside** Serendip, from its own
  quick-start cluster. Serendip trusts its ThunderID as a second issuer,
  for agent tokens only, and maps agents to roles. For the POC and a
  performance test, spec 018 will also **try reusing our existing
  ThunderID and OpenChoreo** (the user, 2026-10-18), on a throwaway
  cluster, never as edge-1's default. Making it a default inside edge-1 is
  ruled out for now:
  - its Gateway API, kgateway and OpenChoreo versions conflict with ours;
  - it patches OpenChoreo's authentication and ThunderID's settings;
  - it adds about 6–8 GB of components, which conflicts with constitution
    rule 4.
- **Nested groups;** Teams as a Serendip-managed group directory. Group
  membership comes from the identity provider.
- **Multi-tenant isolation between Organizations** beyond what scopes
  give: one `serendipd` per install.

## Affected resources and capabilities

- `core/resource`: `RoleDefinition` and `RoleAssignment` kinds. Whether
  they are ordinary resources or a separate store is decided in plan.md.
- `core/policy` (new): evaluation, the route-to-action map, scope
  resolution through the ownership graph.
- `api`: enforcement on every route, list filtering, the authorization
  routes, 403 problem+json.
- `core/identity` and `providers/identity/oidc`: the group claim feeding
  the principal.
- The audit store: `policy_decision` filled, denials recorded.
- `ui`: Access control, Check access, permission-aware controls.

## Acceptance criteria

1. **Reader.** A person with only Reader on Project A can list and read
   Project A's Applications and Components. They get 403 on any create,
   update or delete there. They see nothing from Project B, which is
   absent from lists and returns 403 by ID.
2. **Contributor.** A Contributor on Environment `prod` can change and
   delete Components in `prod` but not in `dev`. They can't create a role
   assignment anywhere (403).
3. **Data plane.** An Owner at `/` can create and delete a Secret but gets
   403 reading its value. With Secrets User at that Secret's scope, they
   can read the value but not delete the Secret.
4. **Inheritance and union.** An assignment at a Project applies to its
   Environments, Applications and Components. Contributor at a Project
   plus Reader at one of its Applications is Contributor there; a lower
   scope never removes access.
5. **Groups.** A role assigned to a group applies to a person whose token
   carries that group, and stops applying when the claim no longer
   carries it.
6. **Constrained delegation.** An RBAC Administrator allowed only
   {Reader, Contributor} for Users can grant those two and is refused
   Owner, or assigning to themselves.
7. **Custom roles.** A custom role (for example, "can restart, can't
   delete") is created, assigned and enforced. Deleting it while assigned
   is refused.
8. **Bootstrap and lockout.** A fresh install's bootstrap administrator
   is Owner and Secrets Officer at `/`. Existing API keys keep working as
   Owner at `/`, and the log says so. Removing the last Owner at `/` is
   refused. The local break-glass command restores an Owner when nobody
   can log in, and leaves an audit row with its own interface name.
9. **Fail closed.** A test enumerating every registered `/v1` route finds
   none without an action mapping.
10. **Audit.** Assignment changes, allowed mutations (with the deciding
    assignment) and denied mutations are all in the audit log with
    actor, action and scope.
11. **Console.** A Reader sees no create, edit or delete controls. An
    Owner sees the Access control page, adds a Reader assignment, and that
    person's access changes accordingly. The API-equivalent text matches
    the requests sent.
12. **Footprint.** Every existing test, the benchmark harness and a
    real-cluster run still pass. The authorization check adds a measured,
    small cost per request (target in plan.md). Constitution 4 holds.
