package policy_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

var agent = policy.Principal{Type: resource.PrincipalAgent, ID: "am-dev/agent-1"}

func agentRef(id, sponsor string) resource.PrincipalRef {
	return resource.PrincipalRef{Type: resource.PrincipalAgent, ID: id, Sponsor: &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: sponsor}}
}

// grantAgent writes an assignment straight to the store, bypassing
// CheckAssign, as a pre-existing or hand-made row would be.
func (w *world) grantAgent(id, sponsor, roleID string, at resource.Scope) string {
	w.t.Helper()
	w.counter++
	created, err := w.assign.Create(w.ctx, resource.RoleAssignment{
		Metadata: resource.Metadata{Name: fmt.Sprintf("ra-%d", w.counter)},
		Spec:     resource.RoleAssignmentSpec{Principal: agentRef(id, sponsor), RoleDefinitionID: roleID, Scope: at},
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return created.Metadata.ID
}

func TestAgentMayHold(t *testing.T) {
	want := map[string]bool{
		policy.OwnerRoleID:                   false,
		policy.ContributorRoleID:             true,
		policy.ReaderRoleID:                  true,
		policy.UserAccessAdministratorRoleID: false,
		policy.RBACAdministratorRoleID:       false,
		policy.SecretsOfficerRoleID:          true,
		policy.SecretsUserRoleID:             true,
	}
	roles := policy.BuiltInRoles()
	if len(roles) != len(want) {
		t.Fatalf("%d built-in roles, the table covers %d", len(roles), len(want))
	}
	for id, role := range roles {
		if got := policy.AgentMayHold(role); got != want[id] {
			t.Errorf("AgentMayHold(%s) = %v, want %v", role.RoleName, got, want[id])
		}
	}
	custom := []struct {
		name  string
		perms []resource.Permission
		want  bool
	}{
		{"restart only", []resource.Permission{{Actions: []string{"serendip/components/read", "serendip/components/action"}}}, true},
		{"everything", []resource.Permission{{Actions: []string{"*"}}}, false},
		{"authorization wildcard", []resource.Permission{{Actions: []string{"serendip/authorization/*"}}}, false},
		{"write wildcard", []resource.Permission{{Actions: []string{"*/write"}}}, false},
		{"delete role definitions", []resource.Permission{{Actions: []string{"serendip/authorization/roleDefinitions/delete"}}}, false},
		{"case doesn't hide it", []resource.Permission{{Actions: []string{"SERENDIP/AUTHORIZATION/ROLEASSIGNMENTS/WRITE"}}}, false},
		{"excluded by notActions", []resource.Permission{{Actions: []string{"*"}, NotActions: []string{"serendip/authorization/*"}}}, true},
		{"a second block grants it", []resource.Permission{
			{Actions: []string{"*"}, NotActions: []string{"serendip/authorization/*"}},
			{Actions: []string{"serendip/authorization/roleAssignments/write"}},
		}, false},
		{"data actions only", []resource.Permission{{DataActions: []string{"*"}}}, true},
	}
	for _, c := range custom {
		if got := policy.AgentMayHold(resource.RoleDefinitionSpec{RoleName: c.name, Permissions: c.perms}); got != c.want {
			t.Errorf("AgentMayHold(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAgentDecisions(t *testing.T) {
	w := newWorld(t)
	contrib := w.grantAgent(agent.ID, "sub-alice", policy.ContributorRoleID, projA)
	// Rows a guardrail-blind past could have left: Decide still holds.
	w.grantAgent(agent.ID, "sub-alice", policy.OwnerRoleID, projB)

	decide := func(action string, at resource.Scope) policy.Decision {
		t.Helper()
		d, err := w.engine.Decide(w.ctx, agent, policy.Action{Name: action}, at)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := decide("serendip/components/write", web); d.Effect != policy.Allow {
		t.Errorf("write in scope: %v", d.Effect)
	}
	d := decide("serendip/components/delete", web)
	if d.Effect != policy.NeedsApproval || d.Evidence == nil || d.Evidence.AssignmentID != contrib {
		t.Errorf("delete in scope: %+v, want needsApproval with Contributor's evidence", d)
	}
	if got := d.Describe(policy.Action{Name: "serendip/components/delete"}, web); !strings.HasPrefix(got, "needsApproval: serendip/components/delete at Component/web, allowed by Contributor") {
		t.Errorf("described as %q", got)
	}
	if d := decide("serendip/components/delete", appB); d.Effect != policy.NeedsApproval {
		t.Errorf("delete under the Owner row: %v, want needsApproval", d.Effect)
	}
	if d := decide("serendip/components/delete", dev); d.Effect != policy.NeedsApproval {
		t.Errorf("delete inherited from projA: %v", d.Effect)
	}
	if d := decide("serendip/components/delete", projB); d.Effect != policy.NeedsApproval {
		t.Errorf("delete at projB: %v", d.Effect)
	}
	if d := decide("serendip/components/write", resource.RootScope); d.Effect != policy.Deny {
		t.Errorf("outside every scope: %v", d.Effect)
	}
	for _, a := range []string{policy.ActionRoleAssignmentsWrite, policy.ActionRoleAssignmentsDelete, policy.ActionRoleDefinitionsWrite, policy.ActionRoleDefinitionsDelete} {
		if d := decide(a, projB); d.Effect != policy.Deny || d.Reason == "" {
			t.Errorf("%s under Owner: %+v, want deny with a reason", a, d)
		}
	}
	ok, err := w.engine.GrantedAnywhere(w.ctx, agent, policy.Action{Name: policy.ActionRoleAssignmentsWrite})
	if err != nil || ok {
		t.Errorf("GrantedAnywhere(roleAssignments/write) = %v, %v; want false", ok, err)
	}
	// A person with the same role gets neither guardrail.
	w.grant(alice, policy.ContributorRoleID, projA, nil)
	w.expect(check{alice, "serendip/components/delete", false, web, true})
}

func TestAgentAssignments(t *testing.T) {
	w := newWorld(t)
	w.grant(bob, policy.OwnerRoleID, resource.RootScope, nil)
	assign := func(role string, ref resource.PrincipalRef) error {
		return w.engine.CheckAssign(w.ctx, bob, resource.RoleAssignmentSpec{Principal: ref, RoleDefinitionID: role, Scope: projA})
	}
	for _, role := range []string{policy.OwnerRoleID, policy.UserAccessAdministratorRoleID, policy.RBACAdministratorRoleID,
		w.customRole(resource.RoleDefinitionSpec{RoleName: "sneaky", AssignableScopes: []resource.Scope{org},
			Permissions: []resource.Permission{{Actions: []string{"*/write"}}}})} {
		if err := assign(role, agentRef(agent.ID, "sub-alice")); !errors.Is(err, policy.ErrAgentGuardrail) || !errors.Is(err, resource.ErrValidation) {
			t.Errorf("assign %s to an agent: %v, want ErrAgentGuardrail", role, err)
		}
	}
	for _, role := range []string{policy.ContributorRoleID, policy.ReaderRoleID, policy.SecretsUserRoleID} {
		if err := assign(role, agentRef(agent.ID, "sub-alice")); err != nil {
			t.Errorf("assign %s to an agent: %v", role, err)
		}
	}
	// The same role to a person is fine: the guardrail is for agents.
	if err := assign(policy.OwnerRoleID, resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "sub-carol"}); err != nil {
		t.Errorf("Owner to a person: %v", err)
	}

	// One sponsor per agent.
	w.grantAgent(agent.ID, "sub-alice", policy.ReaderRoleID, projA)
	if err := assign(policy.ReaderRoleID, agentRef(agent.ID, "sub-bob")); !errors.Is(err, policy.ErrAgentGuardrail) {
		t.Errorf("a second sponsor: %v, want ErrAgentGuardrail", err)
	}
	if err := assign(policy.ReaderRoleID, agentRef(agent.ID, "sub-alice")); err != nil {
		t.Errorf("the same sponsor again: %v", err)
	}
	if s, ok, err := w.engine.SponsorOf(w.ctx, agent.ID); err != nil || !ok || s.ID != "sub-alice" {
		t.Errorf("SponsorOf = %+v, %v, %v", s, ok, err)
	}
	// Another agent's sponsor is its own business.
	if err := assign(policy.ReaderRoleID, agentRef("am-dev/agent-2", "sub-bob")); err != nil {
		t.Errorf("another agent, another sponsor: %v", err)
	}

	// An agent can't assign or unassign, whatever it holds.
	w.grantAgent("am-dev/admin-bot", "sub-alice", policy.OwnerRoleID, resource.RootScope)
	bot := policy.Principal{Type: resource.PrincipalAgent, ID: "am-dev/admin-bot"}
	if err := w.engine.CheckAssign(w.ctx, bot, resource.RoleAssignmentSpec{
		Principal: resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "sub-carol"}, RoleDefinitionID: policy.ReaderRoleID, Scope: projA,
	}); !errors.Is(err, policy.ErrForbidden) {
		t.Errorf("an agent assigning: %v, want ErrForbidden", err)
	}
	carol := w.grant(policy.Principal{Type: resource.PrincipalPerson, ID: "sub-carol"}, policy.ReaderRoleID, projA, nil)
	if err := w.engine.CheckUnassign(w.ctx, bot, carol); !errors.Is(err, policy.ErrForbidden) {
		t.Errorf("an agent unassigning: %v, want ErrForbidden", err)
	}
}

func TestAgentDecisions_Edges(t *testing.T) {
	w := newWorld(t)
	noDelete := w.customRole(resource.RoleDefinitionSpec{RoleName: "no delete", AssignableScopes: []resource.Scope{org},
		Permissions: []resource.Permission{{Actions: []string{"*"}, NotActions: []string{"*/delete", "serendip/authorization/*"}}}})
	deleteOnly := w.customRole(resource.RoleDefinitionSpec{RoleName: "delete only", AssignableScopes: []resource.Scope{org},
		Permissions: []resource.Permission{{Actions: []string{"serendip/components/delete"}}}})
	w.grantAgent("am-dev/no-delete", "sub-alice", noDelete, projA)
	w.grantAgent("am-dev/delete-only", "sub-alice", deleteOnly, projA)
	decide := func(id, action string, data bool) policy.Effect {
		t.Helper()
		d, err := w.engine.Decide(w.ctx, policy.Principal{Type: resource.PrincipalAgent, ID: id}, policy.Action{Name: action, Data: data}, web)
		if err != nil {
			t.Fatal(err)
		}
		return d.Effect
	}
	tests := []struct {
		id, action string
		data       bool
		want       policy.Effect
	}{
		// A role that doesn't allow the delete denies it: approval is only
		// for what a role allows.
		{"am-dev/no-delete", "serendip/components/delete", false, policy.Deny},
		{"am-dev/no-delete", "serendip/components/write", false, policy.Allow},
		{"am-dev/delete-only", "serendip/components/delete", false, policy.NeedsApproval},
		// Deleting doesn't imply reading: lists and graphs only show what
		// Decide allows to be read.
		{"am-dev/delete-only", "serendip/components/read", false, policy.Deny},
	}
	for _, tt := range tests {
		if got := decide(tt.id, tt.action, tt.data); got != tt.want {
			t.Errorf("%s %s: %v, want %v", tt.id, tt.action, got, tt.want)
		}
	}

	// A data-plane delete needs an approval as well.
	w.grantAgent("am-dev/values", "sub-alice", w.customRole(resource.RoleDefinitionSpec{RoleName: "values", AssignableScopes: []resource.Scope{org},
		Permissions: []resource.Permission{{DataActions: []string{"serendip/secrets/value/*"}}}}), projA)
	if got := decide("am-dev/values", "serendip/secrets/value/delete", true); got != policy.NeedsApproval {
		t.Errorf("data-plane delete: %v, want needsApproval", got)
	}
	if got := decide("am-dev/values", "serendip/secrets/value/read", true); got != policy.Allow {
		t.Errorf("data-plane read: %v, want allow", got)
	}
}

func TestEffectivePermissionsAgreeForAgents(t *testing.T) {
	w := newWorld(t)
	// Owner can only reach an agent through a row written past the
	// guardrails; its effective permissions still mustn't claim access
	// management.
	w.grantAgent(agent.ID, "sub-alice", policy.OwnerRoleID, projA)
	perms, err := w.engine.EffectivePermissions(w.ctx, agent, web)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Allows(perms, policy.Action{Name: policy.ActionRoleAssignmentsWrite}) {
		t.Error("an agent's effective permissions claim role assignment rights")
	}
	if !policy.Allows(perms, policy.Action{Name: "serendip/components/write"}) {
		t.Error("an agent's other permissions went missing")
	}
	// A person's are untouched, and the built-in role isn't modified.
	w.grant(alice, policy.OwnerRoleID, projA, nil)
	if perms, _ := w.engine.EffectivePermissions(w.ctx, alice, web); !policy.Allows(perms, policy.Action{Name: policy.ActionRoleAssignmentsWrite}) {
		t.Error("a person's Owner lost role assignment rights")
	}
}
