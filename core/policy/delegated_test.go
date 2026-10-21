package policy_test

import (
	"testing"

	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// A person acting through an AI assistant (spec 021): their own roles
// decide, never more, and an agent's guardrails apply on top.
func TestDelegatedDecisions(t *testing.T) {
	w := newWorld(t)
	contrib := w.grant(alice, policy.ContributorRoleID, projA, nil)
	w.grant(alice, policy.OwnerRoleID, projB, nil)
	viaAI := alice
	viaAI.Via = "serendip-mcp"

	decide := func(p policy.Principal, action string, at resource.Scope) policy.Decision {
		t.Helper()
		d, err := w.engine.Decide(w.ctx, p, policy.Action{Name: action}, at)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := decide(viaAI, "serendip/components/write", web); d.Effect != policy.Allow || d.Evidence == nil || d.Evidence.AssignmentID != contrib {
		t.Errorf("write with the person's role: %+v", d)
	}
	if d := decide(viaAI, "serendip/components/delete", web); d.Effect != policy.NeedsApproval {
		t.Errorf("delete through the assistant: %v, want needsApproval", d.Effect)
	}
	if d := decide(alice, "serendip/components/delete", web); d.Effect != policy.Allow {
		t.Errorf("the person's own delete: %v, want allow", d.Effect)
	}
	if d := decide(viaAI, "serendip/components/write", resource.RootScope); d.Effect != policy.Deny {
		t.Errorf("beyond the person's roles: %v, want deny", d.Effect)
	}
	for _, a := range []string{policy.ActionRoleAssignmentsWrite, policy.ActionRoleAssignmentsDelete, policy.ActionRoleDefinitionsWrite, policy.ActionRoleDefinitionsDelete} {
		if d := decide(viaAI, a, projB); d.Effect != policy.Deny || d.Reason == "" {
			t.Errorf("%s with the person's Owner role: %+v, want deny with a reason", a, d)
		}
		if d := decide(alice, a, projB); d.Effect != policy.Allow {
			t.Errorf("%s by the person themselves: %v, want allow", a, d.Effect)
		}
	}
	if ok, err := w.engine.GrantedAnywhere(w.ctx, viaAI, policy.Action{Name: policy.ActionRoleAssignmentsWrite}); err != nil || ok {
		t.Errorf("GrantedAnywhere(roleAssignments/write) = %v, %v; want false", ok, err)
	}
	perms, err := w.engine.EffectivePermissions(w.ctx, viaAI, appB)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Allows(perms, policy.Action{Name: policy.ActionRoleAssignmentsWrite}) {
		t.Error("the assistant's effective permissions claim role assignment rights")
	}
	if !policy.Allows(perms, policy.Action{Name: "serendip/components/write"}) {
		t.Error("the assistant lost the person's other permissions")
	}
}

func TestDelegatedCantAssign(t *testing.T) {
	w := newWorld(t)
	w.grant(alice, policy.OwnerRoleID, resource.RootScope, nil)
	viaAI := alice
	viaAI.Via = "serendip-mcp"
	spec := resource.RoleAssignmentSpec{Principal: resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "sub-bob"},
		RoleDefinitionID: policy.ReaderRoleID, Scope: projA}
	if err := w.engine.CheckAssign(w.ctx, viaAI, spec); err == nil {
		t.Error("an assistant assigned a role")
	}
	if err := w.engine.CheckAssign(w.ctx, alice, spec); err != nil {
		t.Errorf("the person themselves: %v", err)
	}
}

func TestRolesAreNeverAssignedToAnAssistant(t *testing.T) {
	err := resource.ValidateRoleAssignment(resource.RoleAssignmentSpec{
		Principal:        resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "sub-alice", Via: "serendip-mcp"},
		RoleDefinitionID: policy.ReaderRoleID, Scope: resource.RootScope,
	})
	if err == nil {
		t.Error("an assignment naming an assistant was valid")
	}
}
