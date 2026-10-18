package policy

import (
	"context"
	"fmt"
	"strings"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// Agent guardrails (specs/018-agent-manager-poc plan.md decision 9).
// Constitution 5 holds AI to more than roles do: an agent never holds
// authority over access, and its destructive actions wait for approval.

// Actions that change who can do what. No agent may perform them,
// whatever role it holds.
const (
	ActionRoleDefinitionsWrite  = "serendip/authorization/roleDefinitions/write"
	ActionRoleDefinitionsDelete = "serendip/authorization/roleDefinitions/delete"
)

var accessManagementActions = []string{
	ActionRoleAssignmentsWrite, ActionRoleAssignmentsDelete,
	ActionRoleDefinitionsWrite, ActionRoleDefinitionsDelete,
}

// ErrAgentGuardrail is wrapped by every refusal of an assignment that
// would break an agent guardrail; it is a validation error, since no caller
// could make such an assignment valid.
var ErrAgentGuardrail = fmt.Errorf("%w: agent guardrail", resource.ErrValidation)

// AgentMayHold reports whether an agent may be assigned role: whether none
// of its (non-data) permission blocks grants a change to role assignments
// or role definitions, wildcards included.
func AgentMayHold(role resource.RoleDefinitionSpec) bool {
	for _, a := range accessManagementActions {
		if Allows(role.Permissions, Action{Name: a}) {
			return false
		}
	}
	return true
}

// managesAccess reports whether a is one of the actions no agent may take.
func managesAccess(a Action) bool {
	if a.Data {
		return false
	}
	for _, m := range accessManagementActions {
		if strings.EqualFold(a.Name, m) {
			return true
		}
	}
	return false
}

// isDelete reports whether a deletes something: any action, control or
// data plane, ending in "/delete" (the API names every delete that way, a
// test there checks it). Until approvals exist (spec 019), an agent's
// delete always needs one.
func isDelete(a Action) bool {
	return strings.HasSuffix(strings.ToLower(a.Name), "/delete")
}

// checkAgentAssignment applies the guardrails to an assignment for an
// agent: a role it may hold, and the sponsor its other assignments name.
func (e *Engine) checkAgentAssignment(ctx context.Context, spec resource.RoleAssignmentSpec, role resource.RoleDefinitionSpec) error {
	if spec.Principal.Type != resource.PrincipalAgent {
		return nil
	}
	if err := e.refresh(ctx); err != nil {
		return err
	}
	if !AgentMayHold(role) {
		return fmt.Errorf("%w: an agent can't hold %q, which can change role assignments or role definitions", ErrAgentGuardrail, role.RoleName)
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if s, ok := e.sponsors[spec.Principal.ID]; ok && spec.Principal.Sponsor != nil && (spec.Principal.Sponsor.Type != s.Type || spec.Principal.Sponsor.ID != s.ID) {
		return fmt.Errorf("%w: agent %s already has a different sponsor; every assignment for an agent names the same one",
			ErrAgentGuardrail, spec.Principal.ID)
	}
	return nil
}
