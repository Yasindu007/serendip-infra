// Package policy decides whether a principal may perform an action on a
// resource (specs/017-authorization). It implements Azure RBAC's model:
//
//   - a role definition lists actions, notActions, dataActions and
//     notDataActions;
//   - a role assignment grants one role to one principal at one scope ("/"
//     or a resource), and applies to everything that scope owns;
//   - a principal's effective permissions at a resource are the union, over
//     every assignment to it or its groups at the resource or any ancestor,
//     of each role's actions minus its notActions (and separately its data
//     actions minus its notDataActions). notActions subtracts within its own
//     role; it never denies what another role grants.
//
// Control-plane actions never grant data actions: a role with actions "*"
// (Owner) can't read a Secret's value without a role that grants the data
// action, exactly as in Azure.
//
// Decisions are allow or deny; Decision.Effect also has NeedsApproval,
// which the approvals spec (019) uses. Nothing in this package knows any
// product: principals, scopes and actions are Serendip's own.
package policy
