package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

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
