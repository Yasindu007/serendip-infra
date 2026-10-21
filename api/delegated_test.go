package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// A person acting through an AI assistant (spec 021 plan decision 1): the
// verifier yields the person with Via set, from a token exchanged for the
// assistant's client.
func TestDelegated_PersonThroughAnAssistant(t *testing.T) {
	people["tok-owner-ai"] = identity.Principal{Subject: "sub-owner", Name: "owner", Via: "serendip-mcp"}
	t.Cleanup(func() { delete(people, "tok-owner-ai") })
	w := newAuthzWorld(t, func(w *authzWorld) {
		w.grant("tok-owner", policy.OwnerRoleID, "/")
	})

	// The person's roles: reads and writes as the Owner can.
	w.expect("tok-owner-ai", http.MethodGet, "/v1/applications/"+w.id["appB"], nil, http.StatusOK)
	created := w.do("tok-owner-ai", http.MethodPost, "/v1/applications", map[string]any{
		"metadata": map[string]any{"name": "by-assistant", "ownerRefs": []any{map[string]any{"kind": "Environment", "id": w.id["prod"]}}},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("assistant create: %d %s", created.Code, created.Body)
	}
	var actor, subject, sponsor string
	if err := w.db.QueryRow(`SELECT actor, COALESCE(actor_subject,''), COALESCE(actor_sponsor,'') FROM audit_log
		WHERE kind = 'Application' AND after IS NOT NULL AND json_extract(after, '$.metadata.name') = 'by-assistant'`).Scan(&actor, &subject, &sponsor); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if actor != "agent:serendip-mcp/sub-owner" || subject != "sub-owner" || sponsor != "sub-owner" {
		t.Errorf("audit = actor %q, subject %q, sponsor %q; want the assistant, with the person as subject and sponsor", actor, subject, sponsor)
	}

	// A delete the Owner could just do needs a person's approval.
	got := w.do("tok-owner-ai", http.MethodDelete, "/v1/components/"+w.id["web"], nil)
	var prob struct{ Type string }
	_ = json.Unmarshal(got.Body.Bytes(), &prob)
	if got.Code != http.StatusForbidden || prob.Type != api.ProblemNeedsApproval {
		t.Fatalf("assistant delete: %d %s", got.Code, got.Body)
	}
	w.expect("tok-owner", http.MethodGet, "/v1/components/"+w.id["web"], nil, http.StatusOK)

	// The assistant files the request; the person decides it, as a sponsor
	// decides their agent's. The assistant never decides.
	filed := w.do("tok-owner-ai", http.MethodPost, "/v1/approvals", map[string]any{
		"action": map[string]any{"verb": "delete", "target": map[string]any{"kind": "Component", "id": w.id["web"]}},
		"reason": "no longer used",
	})
	if filed.Code != http.StatusCreated {
		t.Fatalf("assistant files a request: %d %s", filed.Code, filed.Body)
	}
	var req resource.Approval
	if err := json.Unmarshal(filed.Body.Bytes(), &req); err != nil {
		t.Fatal(err)
	}
	if p := req.Status.RequestedBy.Principal; p == nil || p.Type != resource.PrincipalPerson || p.ID != "sub-owner" || p.Via != "serendip-mcp" {
		t.Errorf("requester = %+v, want the person via serendip-mcp", p)
	}
	decide := "/v1/approvals/" + req.Metadata.ID + "/approve"
	if got := w.do("tok-owner-ai", http.MethodPost, decide, map[string]any{"comment": "self"}); got.Code != http.StatusForbidden {
		t.Errorf("assistant approves: %d %s, want 403", got.Code, got.Body)
	}
	if got := w.do("tok-owner", http.MethodPost, decide, map[string]any{"comment": "fine"}); got.Code != http.StatusOK {
		t.Errorf("the person approves their assistant's request: %d %s", got.Code, got.Body)
	}

	// Access management is refused, whatever the person's roles say.
	assign := w.do("tok-owner-ai", http.MethodPost, authzPath+"roleAssignments", map[string]any{"spec": map[string]any{
		"principal": map[string]any{"type": "person", "id": "sub-reader"}, "roleDefinitionId": policy.ReaderRoleID,
		"scope": map[string]any{"kind": "Project", "id": w.id["projA"]},
	}})
	if assign.Code != http.StatusForbidden {
		t.Errorf("assistant assigns a role: %d %s, want 403", assign.Code, assign.Body)
	}
	perms := w.do("tok-owner-ai", http.MethodGet, authzPath+"permissions", nil)
	if perms.Code != http.StatusOK || !strings.Contains(perms.Body.String(), policy.ActionRoleAssignmentsWrite) {
		t.Fatalf("permissions: %d %s", perms.Code, perms.Body)
	}
	var view struct {
		Permissions []resource.Permission `json:"permissions"`
	}
	if err := json.Unmarshal(perms.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if policy.Allows(view.Permissions, policy.Action{Name: policy.ActionRoleAssignmentsWrite}) {
		t.Error("the assistant's effective permissions claim role assignment rights")
	}
}
