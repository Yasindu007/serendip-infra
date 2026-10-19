package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// approvalsOf returns the Approvals naming target.
func approvalsOf(t *testing.T, w *authzWorld, target resource.Scope) []resource.Approval {
	t.Helper()
	all, err := sqlite.NewStore[resource.ApprovalSpec, resource.ApprovalStatus](w.db, resource.KindApproval).
		List(t.Context(), store.ListOptions{Kind: resource.KindApproval})
	if err != nil {
		t.Fatal(err)
	}
	var out []resource.Approval
	for _, a := range all {
		if a.Spec.Action.Target == target {
			out = append(out, a)
		}
	}
	return out
}

// Every DELETE route records exactly one Approval, decided by policy, and
// names it in the response (spec 019 acceptance criterion 1).
func TestDelete_EveryRouteRecordsAPolicyApproval(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-contrib", policy.ContributorRoleID, "projA") })

	// A custom role and an assignment to delete, made by the Owner key.
	role := w.do("key-ci", http.MethodPost, "/v1/authorization/roleDefinitions", resource.RoleDefinition{
		Metadata: resource.Metadata{Name: "viewer"},
		Spec: resource.RoleDefinitionSpec{RoleName: "Viewer", Permissions: []resource.Permission{{Actions: []string{"*/read"}}},
			AssignableScopes: []resource.Scope{w.scope("projA")}},
	})
	var roleDoc resource.RoleDefinition
	if role.Code != http.StatusCreated || json.Unmarshal(role.Body.Bytes(), &roleDoc) != nil {
		t.Fatalf("create role: %d %s", role.Code, role.Body)
	}
	assignmentID := grant(t, w.db, resource.PrincipalPerson, "sub-reader", policy.ReaderRoleID, w.scope("projB"))

	tests := []struct {
		name, token, path string
		target            resource.Scope
		cascade           bool
		radius            int
	}{
		{name: "component", token: "tok-contrib", path: "/v1/components/" + w.id["web"], target: w.scope("web"), radius: 1},
		{name: "secret", token: "tok-contrib", path: "/v1/secrets/" + w.id["secret"], target: w.scope("secret"), radius: 1},
		{name: "application with cascade", token: "tok-contrib", path: "/v1/applications/" + w.id["devapp"] + "?cascade=true", target: w.scope("devapp"), cascade: true, radius: 2},
		{name: "role assignment", token: "key-ci", path: "/v1/authorization/roleAssignments/" + assignmentID, target: resource.Scope{Kind: resource.KindRoleAssignment, ID: assignmentID}, radius: 1},
		{name: "role definition", token: "key-ci", path: "/v1/authorization/roleDefinitions/" + roleDoc.Metadata.ID, target: resource.Scope{Kind: resource.KindRoleDefinition, ID: roleDoc.Metadata.ID}, radius: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := w.do(tt.token, http.MethodDelete, tt.path, nil)
			if got.Code != http.StatusNoContent {
				t.Fatalf("DELETE: %d %s", got.Code, got.Body)
			}
			list := approvalsOf(t, w, tt.target)
			if len(list) != 1 {
				t.Fatalf("approvals for %s = %d, want 1", tt.target, len(list))
			}
			a := list[0]
			if got.Header().Get("Serendip-Approval-Id") != a.Metadata.ID {
				t.Errorf("header = %q, want %q", got.Header().Get("Serendip-Approval-Id"), a.Metadata.ID)
			}
			st := a.Status
			if st.Phase != resource.ApprovalExecuted || st.Decision == nil || !st.Decision.ByPolicy || st.Decision.Evidence == "" ||
				st.RequestDecision != st.Decision.Evidence || a.Spec.Action.Cascade != tt.cascade || len(st.BlastRadius) != tt.radius {
				t.Errorf("approval = %+v / %+v, want Executed, decided by policy with evidence, cascade %v, %d removed", a.Spec, st, tt.cascade, tt.radius)
			}
			if st.RequestedBy.Principal == nil || st.RequestedBy.Actor == "" {
				t.Errorf("requestedBy = %+v, want the caller", st.RequestedBy)
			}
		})
	}
}

// A delete that fails records nothing and names nothing.
func TestDelete_FailedDeleteHasNoApproval(t *testing.T) {
	w := newAuthzWorld(t, func(*authzWorld) {})
	for _, tt := range []struct {
		path   string
		target resource.Scope
		want   int
	}{
		{path: "/v1/components/missing", target: resource.Scope{Kind: resource.KindComponent, ID: "missing"}, want: http.StatusNotFound},
		{path: "/v1/applications/" + w.id["app"], target: w.scope("app"), want: http.StatusConflict}, // has children, no cascade
	} {
		got := w.do("key-ci", http.MethodDelete, tt.path, nil)
		if got.Code != tt.want {
			t.Errorf("DELETE %s: %d, want %d (%s)", tt.path, got.Code, tt.want, got.Body)
		}
		if h := got.Header().Get("Serendip-Approval-Id"); h != "" {
			t.Errorf("DELETE %s named approval %q", tt.path, h)
		}
		if n := len(approvalsOf(t, w, tt.target)); n != 0 {
			t.Errorf("DELETE %s recorded %d approvals", tt.path, n)
		}
	}
}

// Start-up's removal of an unconfigured key's assignment is on the record
// too, decided by configuration.
func TestDelete_RemovedAPIKeyAssignmentHasAnApproval(t *testing.T) {
	db := newTestDB(t)
	api.NewServer(db, event.NewInProcessBus(), map[string]string{"k1": "ci", "k2": "deploy"}, nil, nil, nil, nil)
	api.NewServer(db, event.NewInProcessBus(), map[string]string{"k1": "ci"}, nil, nil, nil, nil)

	all, err := sqlite.NewStore[resource.ApprovalSpec, resource.ApprovalStatus](db, resource.KindApproval).
		List(t.Context(), store.ListOptions{Kind: resource.KindApproval})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("approvals = %d, want 1", len(all))
	}
	a := all[0]
	if a.Spec.Action.Target.Kind != resource.KindRoleAssignment || a.Status.Phase != resource.ApprovalExecuted ||
		a.Spec.Reason != "API key deploy removed from configuration" || a.Status.Decision == nil || !a.Status.Decision.ByPolicy {
		t.Errorf("approval = %+v / %+v", a.Spec, a.Status)
	}
}
