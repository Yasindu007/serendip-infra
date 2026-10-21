package policy_test

import (
	"errors"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestMayDecide(t *testing.T) {
	alice := &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "alice"}
	agent := &resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "am/a1", Sponsor: alice}
	tests := []struct {
		name      string
		approver  policy.Principal
		requester *resource.PrincipalRef
		want      error
	}{
		{name: "person decides an agent's request", approver: policy.Principal{Type: resource.PrincipalPerson, ID: "bob"}, requester: agent},
		{name: "the agent's sponsor decides", approver: policy.Principal{Type: resource.PrincipalPerson, ID: "alice"}, requester: agent},
		{name: "API key decides", approver: policy.Principal{Type: resource.PrincipalAPIKey, ID: "ops"}, requester: agent},
		{name: "server-side request", approver: policy.Principal{Type: resource.PrincipalPerson, ID: "bob"}, requester: nil},
		{name: "a person with the same ID as an API key requester", approver: policy.Principal{Type: resource.PrincipalPerson, ID: "ops"}, requester: &resource.PrincipalRef{Type: resource.PrincipalAPIKey, ID: "ops"}},
		{name: "the requesting person", approver: policy.Principal{Type: resource.PrincipalPerson, ID: "alice"}, requester: alice, want: policy.ErrSelfApproval},
		{name: "a person decides what their assistant asked for", approver: policy.Principal{Type: resource.PrincipalPerson, ID: "alice"},
			requester: &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "alice", Via: "serendip-mcp"}},
		{name: "an assistant never decides", approver: policy.Principal{Type: resource.PrincipalPerson, ID: "alice", Via: "serendip-mcp"}, requester: agent, want: policy.ErrApproverGuardrail},
		{name: "an assistant never decides its own request", approver: policy.Principal{Type: resource.PrincipalPerson, ID: "alice", Via: "serendip-mcp"},
			requester: &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "alice", Via: "serendip-mcp"}, want: policy.ErrApproverGuardrail},
		{name: "the requesting API key", approver: policy.Principal{Type: resource.PrincipalAPIKey, ID: "ops"}, requester: &resource.PrincipalRef{Type: resource.PrincipalAPIKey, ID: "ops"}, want: policy.ErrSelfApproval},
		{name: "the requesting agent", approver: policy.Principal{Type: resource.PrincipalAgent, ID: "am/a1"}, requester: agent, want: policy.ErrApproverGuardrail},
		{name: "another agent", approver: policy.Principal{Type: resource.PrincipalAgent, ID: "am/a2"}, requester: agent, want: policy.ErrApproverGuardrail},
		{name: "a group", approver: policy.Principal{Type: resource.PrincipalGroup, ID: "ops"}, requester: agent, want: policy.ErrApproverGuardrail},
		{name: "a system actor", approver: policy.Principal{Type: "system", ID: "system:reconciler"}, requester: agent, want: policy.ErrApproverGuardrail},
		{name: "no type", approver: policy.Principal{ID: "x"}, requester: agent, want: policy.ErrApproverGuardrail},
		{name: "no ID", approver: policy.Principal{Type: resource.PrincipalPerson}, requester: agent, want: policy.ErrApproverGuardrail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := policy.MayDecide(tt.approver, tt.requester)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("MayDecide() = %v, want %v", err, tt.want)
			}
			if err != nil && !errors.Is(err, policy.ErrForbidden) {
				t.Errorf("MayDecide() error doesn't wrap ErrForbidden: %v", err)
			}
		})
	}
}
