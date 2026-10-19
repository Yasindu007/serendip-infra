package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// agentTokens are agents as an agent issuer's verifier yields them
// (specs/018-agent-manager-poc plan.md decision 7). The middleware must
// ignore Groups even if a verifier ever filled them.
var agentTokens = map[string]identity.Principal{
	"tok-agent":       {Kind: identity.KindAgent, Issuer: "am-dev", Subject: "agent-1", Name: "agent-1"},
	"tok-agent-bare":  {Kind: identity.KindAgent, Issuer: "am-dev", Subject: "agent-2", Name: "agent-2"},
	"tok-agent-group": {Kind: identity.KindAgent, Issuer: "am-dev", Subject: "agent-3", Name: "agent-3", Groups: []string{"platform"}},
	// Same subject as agent-1 from another issuer: a different agent.
	"tok-agent-other": {Kind: identity.KindAgent, Issuer: "am-prod", Subject: "agent-1", Name: "agent-1"},
}

func withAgentTokens(t *testing.T) {
	for tok, p := range agentTokens {
		people[tok] = p
		t.Cleanup(func() { delete(people, tok) })
	}
}

func grantAgent(t *testing.T, w *authzWorld, agentID, role, at, sponsor string) {
	t.Helper()
	s := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](w.db, resource.KindRoleAssignment)
	_, err := s.Create(context.Background(), resource.RoleAssignment{
		Metadata: resource.Metadata{Name: "agent-grant"},
		Spec: resource.RoleAssignmentSpec{
			Principal:        resource.PrincipalRef{Type: resource.PrincipalAgent, ID: agentID, Sponsor: &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: sponsor}},
			RoleDefinitionID: role,
			Scope:            w.scope(at),
		},
	})
	if err != nil {
		t.Fatalf("grant agent %s: %v", agentID, err)
	}
}

func TestAgents_RolesSponsorAndAudit(t *testing.T) {
	withAgentTokens(t)
	w := newAuthzWorld(t, func(w *authzWorld) {
		grantAgent(t, w, "am-dev/agent-1", policy.ContributorRoleID, "projA", "sub-owner")
		// A group grant an agent must never pick up through a group claim.
		grant(t, w.db, resource.PrincipalGroup, "platform", policy.OwnerRoleID, resource.RootScope)
		w.grant("tok-owner", policy.OwnerRoleID, "/")
		w.grant("tok-admin", policy.RBACAdministratorRoleID, "projA")
	})

	// agent-1 is a Contributor on projA, and nothing elsewhere.
	w.expect("tok-agent", http.MethodGet, "/v1/applications/"+w.id["app"], nil, http.StatusOK)
	w.expect("tok-agent", http.MethodGet, "/v1/applications/"+w.id["appB"], nil, http.StatusForbidden)
	created := w.do("tok-agent", http.MethodPost, "/v1/applications", map[string]any{
		"metadata": map[string]any{"name": "by-agent", "ownerRefs": []any{map[string]any{"kind": "Environment", "id": w.id["prod"]}}},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("agent create in its scope: %d %s", created.Code, created.Body)
	}

	// No assignment: a valid agent token grants nothing.
	w.expect("tok-agent-bare", http.MethodGet, "/v1/applications/"+w.id["app"], nil, http.StatusForbidden)
	// A group claim never applies to an agent.
	w.expect("tok-agent-group", http.MethodGet, "/v1/applications/"+w.id["app"], nil, http.StatusForbidden)
	// The same subject from another agent issuer is another agent.
	w.expect("tok-agent-other", http.MethodGet, "/v1/applications/"+w.id["app"], nil, http.StatusForbidden)

	var actor, subject, sponsor string
	err := w.db.QueryRow(`SELECT actor, COALESCE(actor_subject,''), COALESCE(actor_sponsor,'') FROM audit_log
		WHERE kind = 'Application' AND after IS NOT NULL AND json_extract(after, '$.metadata.name') = 'by-agent'`).Scan(&actor, &subject, &sponsor)
	if err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if actor != "agent:am-dev/agent-1" || subject != "am-dev/agent-1" || sponsor != "sub-owner" {
		t.Errorf("audit = actor %q, subject %q, sponsor %q; want agent:am-dev/agent-1, am-dev/agent-1, sub-owner", actor, subject, sponsor)
	}

	// A refused agent mutation is audited with the agent, too.
	w.expect("tok-agent", http.MethodPost, "/v1/applications", map[string]any{
		"metadata": map[string]any{"name": "outside", "ownerRefs": []any{map[string]any{"kind": "Project", "id": w.id["projB"]}}},
	}, http.StatusForbidden)
	var denied int
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE actor = 'agent:am-dev/agent-1' AND policy_decision LIKE 'deny:%' AND actor_sponsor = 'sub-owner'`).Scan(&denied); err != nil || denied != 1 {
		t.Errorf("denied agent mutations audited = %d (%v), want 1 with the sponsor", denied, err)
	}

	// People's audit rows carry no sponsor.
	w.expect("tok-owner", http.MethodPost, "/v1/organizations", map[string]any{"metadata": map[string]any{"name": "by-person"}}, http.StatusCreated)
	if err := w.db.QueryRow(`SELECT COALESCE(actor_sponsor,'') FROM audit_log WHERE actor = 'person:owner' ORDER BY at DESC LIMIT 1`).Scan(&sponsor); err != nil || sponsor != "" {
		t.Errorf("person's audit sponsor = %q (%v), want none", sponsor, err)
	}

	// Agents are listed as their own type for the principal picker.
	got := w.do("tok-admin", http.MethodGet, authzPath+"principals?type=agent", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"am-dev/agent-1"`) {
		t.Errorf("agents in the principal list: %d %s", got.Code, got.Body)
	}
}

func TestAgents_Guardrails(t *testing.T) {
	withAgentTokens(t)
	w := newAuthzWorld(t, func(w *authzWorld) {
		grantAgent(t, w, "am-dev/agent-1", policy.ContributorRoleID, "projA", "sub-owner")
		w.grant("tok-owner", policy.OwnerRoleID, "/")
	})

	// A delete its role allows needs an approval, and nothing is deleted.
	got := w.do("tok-agent", http.MethodDelete, "/v1/components/"+w.id["web"], nil)
	var prob struct {
		Type, Title, Detail string
	}
	_ = json.Unmarshal(got.Body.Bytes(), &prob)
	if got.Code != http.StatusForbidden || prob.Type != api.ProblemNeedsApproval || !strings.Contains(prob.Detail, "needs an approval") {
		t.Errorf("agent delete: %d %s", got.Code, got.Body)
	}
	// The detail says how to ask (spec 019), and no Approval was recorded.
	if want := `POST /v1/approvals and {"action": {"verb": "delete", "target": {"kind": "Component", "id": "` + w.id["web"] + `"}, "cascade": false}`; !strings.Contains(prob.Detail, want) {
		t.Errorf("needs-approval detail = %q, want it to contain %q", prob.Detail, want)
	}
	if got.Header().Get("Serendip-Approval-Id") != "" {
		t.Errorf("a refused delete named an approval: %q", got.Header().Get("Serendip-Approval-Id"))
	}
	w.expect("tok-owner", http.MethodGet, "/v1/components/"+w.id["web"], nil, http.StatusOK)
	var decision string
	if err := w.db.QueryRow(`SELECT policy_decision FROM audit_log WHERE actor = 'agent:am-dev/agent-1' AND resource_id = ?`, w.id["web"]).Scan(&decision); err != nil ||
		!strings.HasPrefix(decision, "needsApproval: serendip/components/delete") {
		t.Errorf("audited decision = %q (%v)", decision, err)
	}
	// A cascade delete is a delete too.
	got = w.do("tok-agent", http.MethodDelete, "/v1/applications/"+w.id["app"]+"?cascade=true", nil)
	if got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), api.ProblemNeedsApproval) {
		t.Errorf("agent cascade delete: %d %s", got.Code, got.Body)
	}
	// A person with the same role just deletes.
	w.expect("tok-owner", http.MethodDelete, "/v1/components/"+w.id["devweb"], nil, http.StatusNoContent)

	// Access management is refused to agents even if a role allowed it.
	body := map[string]any{"spec": map[string]any{
		"principal": map[string]any{"type": "person", "id": "sub-reader"}, "roleDefinitionId": policy.ReaderRoleID,
		"scope": map[string]any{"kind": "Project", "id": w.id["projA"]},
	}}
	got = w.do("tok-agent", http.MethodPost, authzPath+"roleAssignments", body)
	if got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "an agent can't change role assignments or role definitions") {
		t.Errorf("agent assigning: %d %s", got.Code, got.Body)
	}

	// Roles that manage access can't be given to an agent, by anyone.
	agentBody := func(role, sponsor string) map[string]any {
		return map[string]any{"spec": map[string]any{
			"principal":        map[string]any{"type": "agent", "id": "am-dev/agent-9", "sponsor": map[string]any{"type": "person", "id": sponsor}},
			"roleDefinitionId": role, "scope": map[string]any{"kind": "Project", "id": w.id["projA"]},
		}}
	}
	for _, role := range []string{policy.OwnerRoleID, policy.UserAccessAdministratorRoleID, policy.RBACAdministratorRoleID} {
		w.expect("tok-owner", http.MethodPost, authzPath+"roleAssignments", agentBody(role, "sub-owner"), http.StatusBadRequest)
	}
	w.expect("tok-owner", http.MethodPost, authzPath+"roleAssignments", agentBody(policy.ReaderRoleID, "sub-owner"), http.StatusCreated)
	// ...and every assignment for an agent names the same sponsor.
	w.expect("tok-owner", http.MethodPost, authzPath+"roleAssignments", agentBody(policy.ContributorRoleID, "sub-reader"), http.StatusBadRequest)

	// The role list says which roles an agent may hold.
	var defs []struct {
		Metadata           resource.Metadata `json:"metadata"`
		AssignableToAgents bool              `json:"assignableToAgents"`
	}
	got = w.do("tok-owner", http.MethodGet, authzPath+"roleDefinitions", nil)
	if err := json.Unmarshal(got.Body.Bytes(), &defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if want := policy.AgentMayHold(policy.BuiltInRoles()[d.Metadata.ID]); d.AssignableToAgents != want {
			t.Errorf("%s assignableToAgents = %v, want %v", d.Metadata.Name, d.AssignableToAgents, want)
		}
	}
	// A custom role is judged the same way.
	for name, actions := range map[string][]string{"writer-of-all": {"*/write"}, "restarter": {"serendip/components/read"}} {
		got = w.do("tok-owner", http.MethodPost, authzPath+"roleDefinitions", map[string]any{"spec": map[string]any{
			"roleName": name, "assignableScopes": []any{map[string]any{"kind": "Project", "id": w.id["projA"]}},
			"permissions": []any{map[string]any{"actions": actions}},
		}})
		var created struct {
			AssignableToAgents bool `json:"assignableToAgents"`
		}
		if err := json.Unmarshal(got.Body.Bytes(), &created); err != nil || got.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, got.Code, got.Body)
		}
		if want := name == "restarter"; created.AssignableToAgents != want {
			t.Errorf("custom role %s assignableToAgents = %v, want %v", name, created.AssignableToAgents, want)
		}
	}
}
