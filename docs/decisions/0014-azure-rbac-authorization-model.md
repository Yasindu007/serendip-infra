# ADR 0014: Authorization follows Azure RBAC

Status: Accepted
Date: 2026-10-18

## Context

Until spec 017, every authenticated caller could do everything.
Serendip needs real authorization before it has more than one user,
before AI agents act on it (spec 018), and before approvals (spec 019).

Constraints:
- The user's direction: roles dictate authority. Approvals are the
  exception, not something every action asks for.
- Authority must follow the resource tree (Organization, Project,
  Environment, Application, Component), so access to a project means its
  contents.
- Secret values must be separately controllable from managing Secrets.
- It must run cheaply on a T470 (constitution 4) with no new dependency
  (stack rules).
- Every interface uses the same API (constitution 7), so authorization
  sits in the API, not in the console.

## Options

1. **A home-grown role list** (admin, editor, viewer) per resource.
   - Pro: simple.
   - Con: every team has to learn it, it has no answer for delegation or
     data-plane separation, and its edge cases would be rediscovered one
     at a time.
2. **A policy language** (OPA/Rego, Cedar or an ABAC engine).
   - Pro: expressive.
   - Con: a new dependency and a language to learn. It's also harder to
     show a person what they can do, and slower to evaluate on weak
     hardware.
3. **Kubernetes RBAC semantics** (Roles and ClusterRoles bound by
   namespace).
   - Pro: familiar to cluster operators.
   - Con: Serendip's scopes are a resource tree, not namespaces, and there's
     no data-plane split or constrained delegation.
4. **Azure RBAC's model, copied closely** (role definitions, role
   assignments at hierarchical scopes, additive evaluation with
   notActions, dataActions, constrained delegation, built-in roles).
   - Pro: industry-accepted and widely understood. It has answers for
     inheritance, data versus control plane, delegation and lockout,
     already battle-tested, and evaluation is a small, fast union.
   - Con: no deny assignments or ABAC conditions in this version; we copy
     the parts that fit.

## Decision

Option 4. Serendip copies Azure RBAC:
- the same concepts, evaluation and built-in roles (Owner, Contributor,
  Reader, User Access Administrator, RBAC Administrator, and Key Vault's
  Secrets Officer and Secrets User);
- constrained delegation without the ABAC language;
- Azure's API shapes (`roleDefinitions`, `roleAssignments`,
  `permissions`);
- an IAM-blade-style console page.

Where Serendip differs, it's written down:
- **Scopes** are resource references (`{kind, id}`), not path strings.
- **Recovery** is by local break-glass rather than tenant elevation.
- **Groups** come from the identity provider's token rather than a live
  directory lookup.
- **No minting wider administrators:** a delegate granting a role that
  can itself assign roles must pass on a delegation within their own.

The model and its enforcement are described in
`docs/architecture/authorization.md`; the decisions and their amendments
are in `specs/017-authorization/plan.md`.

## Consequences

- **Fail closed:** every `/v1` route must have a rule in `api/authz.go`'s
  route table, or `serendipd` won't start.
- **Cost:** about 10 µs per request and 0.24 ms per 100-item list on the
  simulated T470 (p99), and +0.7 MiB idle on the dev machine (+1.8 MiB on the
  simulated T470). This needs the in-memory assignment
  index and an ownership cache, both invalidated by change events.
- **Existing installs:** existing API keys become Owner at `/` once; the
  bootstrap administrator gets Owner and Secrets Officer.
- **Deferred:** deny assignments, ABAC conditions and PIM (eligible,
  time-bound roles) are out of scope. PIM-style elevation belongs with
  approvals (spec 019).
- **Live directory lookups**, such as someone's current groups, were
  deferred to spec 018, which added them for the pickers and Check access
  (ADR 0015). Evaluation still reads groups from the token.
