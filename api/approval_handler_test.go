package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// approvalsWorld is the authz world with an agent that may delete in projA
// (sponsored by the owner), a Contributor in projA, a Reader in projB, and
// an Owner at "/"; readers of app and devapp.
func approvalsWorld(t *testing.T) *authzWorld {
	t.Helper()
	withAgentTokens(t)
	return newAuthzWorld(t, func(w *authzWorld) {
		grantAgent(t, w, "am-dev/agent-1", policy.ContributorRoleID, "projA", "sub-owner")
		grantAgent(t, w, "am-dev/agent-2", policy.ContributorRoleID, "projA", "sub-owner")
		w.grant("tok-owner", policy.OwnerRoleID, "/")
		w.grant("tok-contrib", policy.ContributorRoleID, "projA")
		w.grant("tok-reader", policy.ReaderRoleID, "projB")
		w.grant("tok-mixed", policy.ContributorRoleID, "web")
		w.grant("tok-admin", policy.ReaderRoleID, "app")
		w.grant("tok-member", policy.ReaderRoleID, "devapp")
	})
}

func deleteRequest(target resource.Scope, cascade bool, reason string) map[string]any {
	return map[string]any{
		"action": map[string]any{"verb": "delete", "target": map[string]any{"kind": target.Kind, "id": target.ID}, "cascade": cascade},
		"reason": reason,
	}
}

func decodeApproval(t *testing.T, body []byte) resource.Approval {
	t.Helper()
	var a resource.Approval
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("decode approval: %v (%s)", err, body)
	}
	return a
}

// requestOK asks for an approval and returns it, failing unless the answer
// is want.
func (w *authzWorld) requestApproval(token string, body any, want int) resource.Approval {
	w.t.Helper()
	got := w.do(token, http.MethodPost, "/v1/approvals", body)
	if got.Code != want {
		w.t.Fatalf("%s POST /v1/approvals: %d, want %d (%s)", token, got.Code, want, got.Body)
	}
	if want >= 300 {
		return resource.Approval{}
	}
	return decodeApproval(w.t, got.Body.Bytes())
}

func TestApprovals_Request(t *testing.T) {
	w := approvalsWorld(t)
	web := w.scope("web")

	// Denied can't request (acceptance criterion 2): nothing is recorded.
	w.requestApproval("tok-reader", deleteRequest(web, false, "x"), http.StatusForbidden)
	if n := len(approvalsOf(t, w, web)); n != 0 {
		t.Fatalf("a refused request recorded %d approvals", n)
	}

	// An agent waits for a person, and must say why (criterion 3).
	w.requestApproval("tok-agent", deleteRequest(web, false, ""), http.StatusBadRequest)
	before := time.Now()
	a := w.requestApproval("tok-agent", deleteRequest(web, false, "retired"), http.StatusCreated)
	st := a.Status
	if st.Phase != resource.ApprovalPending || st.Decision != nil {
		t.Errorf("agent request: phase %q decision %+v, want Pending and undecided", st.Phase, st.Decision)
	}
	if exp := st.ExpiresAt.Sub(before); exp < 23*time.Hour || exp > 25*time.Hour {
		t.Errorf("expiresAt %v is %v away, want about 24h", st.ExpiresAt, exp)
	}
	if p := st.RequestedBy.Principal; p == nil || p.Type != resource.PrincipalAgent || p.ID != "am-dev/agent-1" || st.RequestedBy.Sponsor != "sub-owner" {
		t.Errorf("requestedBy = %+v, want the agent with its sponsor", st.RequestedBy)
	}
	if st.TargetName != "web" || st.TargetVersion == 0 || len(st.BlastRadius) != 1 || st.BlastRadius[0].Name != "web" ||
		!strings.HasPrefix(st.RequestDecision, "needsApproval: serendip/components/delete") {
		t.Errorf("status = %+v, want the target as reviewed and the needsApproval decision", st)
	}
	if len(st.OwnerChain) == 0 || st.OwnerChain[0] != w.scope("app") {
		t.Errorf("ownerChain = %v, want the Application first", st.OwnerChain)
	}
	w.expect("tok-owner", http.MethodGet, "/v1/components/"+w.id["web"], nil, http.StatusOK)

	// The same request again returns the open one (criterion 9).
	again := w.requestApproval("tok-agent", deleteRequest(web, false, "again"), http.StatusOK)
	if again.Metadata.ID != a.Metadata.ID {
		t.Errorf("a repeated request made %s, want the open %s", again.Metadata.ID, a.Metadata.ID)
	}

	// A person whose role allows it is approved by policy at once.
	devweb := w.requestApproval("tok-contrib", deleteRequest(w.scope("devweb"), false, ""), http.StatusCreated)
	if devweb.Status.Phase != resource.ApprovalApproved || devweb.Status.Decision == nil || !devweb.Status.Decision.ByPolicy ||
		!devweb.Status.ExpiresAt.IsZero() {
		t.Errorf("contributor request = %+v, want Approved by policy, no expiry", devweb.Status)
	}

	// A cascade lists everything it would remove, with names.
	app := w.requestApproval("tok-agent", deleteRequest(w.scope("app"), true, "retire the app"), http.StatusCreated)
	names := map[string]bool{}
	for _, r := range app.Status.BlastRadius {
		names[r.Name] = true
	}
	if len(app.Status.BlastRadius) != 3 || !names["app"] || !names["web"] || !names["secret"] || app.Status.BlastRadius[0].Name != "app" {
		t.Errorf("cascade blast radius = %+v, want app first, then web and secret", app.Status.BlastRadius)
	}

	// What can't be requested.
	w.requestApproval("key-ci", deleteRequest(resource.Scope{Kind: resource.KindRoleAssignment, ID: "x"}, false, "x"), http.StatusBadRequest)
	w.requestApproval("key-ci", deleteRequest(resource.Scope{Kind: resource.KindComponent, ID: "missing"}, false, "x"), http.StatusNotFound)
	w.requestApproval("key-ci", map[string]any{"action": map[string]any{"verb": "scale", "target": map[string]any{"kind": "Component", "id": w.id["web"]}}}, http.StatusBadRequest)

	// Status comes from the server, never the client.
	body := deleteRequest(w.scope("devweb"), true, "cleanup")
	body["status"] = map[string]any{"phase": "Executed"}
	forged := w.requestApproval("tok-agent", body, http.StatusCreated)
	if forged.Status.Phase != resource.ApprovalPending {
		t.Errorf("a client-sent status was kept: phase %q", forged.Status.Phase)
	}
}

func TestApprovals_Decide(t *testing.T) {
	w := approvalsWorld(t)
	pending := w.requestApproval("tok-agent", deleteRequest(w.scope("web"), false, "retired"), http.StatusCreated)
	path := "/v1/approvals/" + pending.Metadata.ID

	// Who may decide (criterion 5): never the requesting agent, nor any
	// agent, nor someone not allowed the delete themselves.
	for _, tok := range []string{"tok-agent", "tok-agent-bare", "tok-reader", "tok-nobody"} {
		w.expect(tok, http.MethodPost, path+"/approve", nil, http.StatusForbidden)
		w.expect(tok, http.MethodPost, path+"/reject", nil, http.StatusForbidden)
	}
	var decision string
	if err := w.db.QueryRow(`SELECT policy_decision FROM audit_log WHERE actor = 'agent:am-dev/agent-1' AND resource_id = ? ORDER BY at DESC LIMIT 1`, pending.Metadata.ID).Scan(&decision); err != nil ||
		!strings.Contains(decision, "only a person or an API key") {
		t.Errorf("the agent's refused decision was audited as %q (%v)", decision, err)
	}

	// A person allowed the delete approves it.
	got := w.do("tok-contrib", http.MethodPost, path+"/approve", map[string]any{"comment": "checked"})
	if got.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", got.Code, got.Body)
	}
	a := decodeApproval(t, got.Body.Bytes())
	d := a.Status.Decision
	if a.Status.Phase != resource.ApprovalApproved || d == nil || d.ByPolicy || d.By == nil || d.By.Actor != "person:contrib" ||
		d.Comment != "checked" || !strings.HasPrefix(d.Evidence, "allow: ") || !a.Status.ExpiresAt.IsZero() {
		t.Errorf("approved = %+v / %+v", a.Status, d)
	}

	// Decided is final (criterion 6).
	w.expect("tok-owner", http.MethodPost, path+"/approve", nil, http.StatusConflict)
	w.expect("tok-owner", http.MethodPost, path+"/reject", nil, http.StatusConflict)

	// Reject is final too.
	other := w.requestApproval("tok-agent", deleteRequest(w.scope("devweb"), false, "unused"), http.StatusCreated)
	w.expect("tok-owner", http.MethodPost, "/v1/approvals/"+other.Metadata.ID+"/reject", nil, http.StatusOK)
	w.expect("tok-owner", http.MethodPost, "/v1/approvals/"+other.Metadata.ID+"/approve", nil, http.StatusConflict)

	// A cascade needs delete on everything below, as it is now: a
	// Contributor on web alone can't approve deleting its Application.
	cascade := w.requestApproval("tok-agent", deleteRequest(w.scope("app"), true, "retire"), http.StatusCreated)
	w.expect("tok-mixed", http.MethodPost, "/v1/approvals/"+cascade.Metadata.ID+"/approve", nil, http.StatusForbidden)

	w.expect("tok-owner", http.MethodPost, "/v1/approvals/missing/approve", nil, http.StatusNotFound)
	w.expect("tok-contrib", http.MethodPost, "/v1/approvals/missing/approve", nil, http.StatusForbidden)
}

func TestApprovals_SelfApprovalAndExpiry(t *testing.T) {
	w := approvalsWorld(t)
	store := sqlite.NewStore[resource.ApprovalSpec, resource.ApprovalStatus](w.db, resource.KindApproval)
	seed := func(requester *resource.PrincipalRef, expires time.Time) string {
		t.Helper()
		a, err := store.Create(t.Context(), resource.Approval{
			Metadata: resource.Metadata{Name: "seeded"},
			Spec:     resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: resource.ApprovalVerbDelete, Target: w.scope("web")}, Reason: "seeded"},
			Status: resource.ApprovalStatus{
				Phase: resource.ApprovalPending, ExpiresAt: expires,
				RequestedBy: resource.ApprovalActor{Actor: "person:owner", Principal: requester},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return "/v1/approvals/" + a.Metadata.ID
	}
	// Separation of duties: a requester never decides their own request.
	own := seed(&resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "sub-owner"}, time.Now().Add(time.Hour))
	got := w.do("tok-owner", http.MethodPost, own+"/approve", nil)
	if got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "separation of duties") {
		t.Errorf("self-approval: %d %s", got.Code, got.Body)
	}
	w.expect("tok-contrib", http.MethodPost, own+"/approve", nil, http.StatusOK)

	// An expired request can't be decided, even before the reconciler marks it.
	late := seed(&resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "am-dev/agent-1"}, time.Now().Add(-time.Minute))
	w.expect("tok-owner", http.MethodPost, late+"/approve", nil, http.StatusConflict)
}

func TestApprovals_Reading(t *testing.T) {
	w := approvalsWorld(t)
	pending := w.requestApproval("tok-agent", deleteRequest(w.scope("web"), false, "retired"), http.StatusCreated)
	path := "/v1/approvals/" + pending.Metadata.ID

	// Reading follows the target (criterion 11).
	w.expect("tok-owner", http.MethodGet, path, nil, http.StatusOK)
	w.expect("tok-admin", http.MethodGet, path, nil, http.StatusOK)
	w.expect("tok-reader", http.MethodGet, path, nil, http.StatusForbidden)
	if got := w.names("tok-reader", "/v1/approvals"); len(got) != 0 {
		t.Errorf("projB's reader lists %v, want nothing from projA", got)
	}
	if got := w.names("tok-admin", "/v1/approvals?phase=Pending"); len(got) != 1 {
		t.Errorf("app reader lists %v, want the one pending approval", got)
	}
	if got := w.names("tok-owner", "/v1/approvals?phase=Executed"); len(got) != 0 {
		t.Errorf("phase filter: %v, want none executed yet", got)
	}

	// Once the target is gone, the nearest surviving owner decides.
	del := w.do("tok-contrib", http.MethodDelete, "/v1/components/"+w.id["devweb"], nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete devweb: %d %s", del.Code, del.Body)
	}
	executed := "/v1/approvals/" + del.Header().Get("Serendip-Approval-Id")
	w.expect("tok-member", http.MethodGet, executed, nil, http.StatusOK)
	w.expect("tok-admin", http.MethodGet, executed, nil, http.StatusForbidden)

	// The removed resources' last specs never leave the server.
	secretDel := w.do("tok-contrib", http.MethodDelete, "/v1/secrets/"+w.id["secret"], nil)
	if secretDel.Code != http.StatusNoContent {
		t.Fatalf("delete secret: %d %s", secretDel.Code, secretDel.Body)
	}
	for _, p := range []string{"/v1/approvals", "/v1/approvals/" + secretDel.Header().Get("Serendip-Approval-Id")} {
		got := w.do("tok-owner", http.MethodGet, p, nil)
		if got.Code != http.StatusOK || strings.Contains(got.Body.String(), "lastSpec") || strings.Contains(got.Body.String(), "app/secret") {
			t.Errorf("GET %s: %d, and it must not carry lastSpec: %s", p, got.Code, got.Body)
		}
	}
	if a := approvalsOf(t, w, w.scope("secret")); len(a) != 1 || len(a[0].Status.BlastRadius[0].LastSpec) == 0 {
		t.Errorf("the stored approval lost the secret's last spec: %+v", a)
	}
}

// A decision's body is optional, whether sent sized or chunked.
func TestApprovals_DecideWithoutABody(t *testing.T) {
	w := approvalsWorld(t)
	pending := w.requestApproval("tok-agent", deleteRequest(w.scope("web"), false, "retired"), http.StatusCreated)
	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/"+pending.Metadata.ID+"/approve", strings.NewReader(""))
	req.ContentLength = -1 // chunked: the length isn't known up front
	req.Header.Set("Authorization", "Bearer tok-owner")
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("approve with an empty chunked body: %d %s", rec.Code, rec.Body)
	}
}
