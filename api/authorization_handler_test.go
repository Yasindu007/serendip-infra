package api_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

const authzPath = "/v1/authorization/"

func assignment(pt resource.PrincipalType, id, role string, at resource.Scope) map[string]any {
	return map[string]any{"metadata": map[string]any{}, "spec": map[string]any{
		"principal": map[string]any{"type": pt, "id": id}, "roleDefinitionId": role, "scope": at,
	}}
}

func (w *authzWorld) assign(token string, pt resource.PrincipalType, id, role, at string, want int) string {
	w.t.Helper()
	scope := resource.RootScope
	if at != "/" {
		scope = w.scope(at)
	}
	got := w.do(token, "POST", authzPath+"roleAssignments", assignment(pt, id, role, scope))
	if got.Code != want {
		w.t.Errorf("%s assigns %s to %s at %s: %d, want %d (%s)", people[token].Name, role, id, at, got.Code, want, strings.TrimSpace(got.Body.String()))
		return ""
	}
	var created resource.RoleAssignment
	_ = json.Unmarshal(got.Body.Bytes(), &created)
	return created.Metadata.ID
}

// Acceptance criterion 2, second half: a Contributor can't assign roles.
func TestAuthzAPI_ContributorCantAssign(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-contrib", policy.ContributorRoleID, "prod") })
	w.assign("tok-contrib", resource.PrincipalPerson, "sub-reader", policy.ReaderRoleID, "prod", http.StatusForbidden)
	w.assign("tok-contrib", resource.PrincipalPerson, "sub-reader", policy.ReaderRoleID, "/", http.StatusForbidden)
}

// Assigning takes effect on the next request, and removing it undoes it.
func TestAuthzAPI_AssignmentsTakeEffect(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-owner", policy.OwnerRoleID, "/") })
	web := "/v1/components/" + w.id["web"]
	w.expect("tok-reader", "GET", web, nil, http.StatusForbidden)
	id := w.assign("tok-owner", resource.PrincipalPerson, "sub-reader", policy.ReaderRoleID, "projA", http.StatusCreated)
	w.expect("tok-reader", "GET", web, nil, http.StatusOK)
	// The same grant twice is a conflict, as in Azure.
	w.assign("tok-owner", resource.PrincipalPerson, "sub-reader", policy.ReaderRoleID, "projA", http.StatusConflict)
	w.expect("tok-owner", "DELETE", authzPath+"roleAssignments/"+id, nil, http.StatusNoContent)
	w.expect("tok-reader", "GET", web, nil, http.StatusForbidden)
	// Scopes must exist, so nothing is granted on a typo.
	got := w.do("tok-owner", "POST", authzPath+"roleAssignments", assignment(resource.PrincipalPerson, "sub-reader", policy.ReaderRoleID,
		resource.Scope{Kind: resource.KindProject, ID: "no-such-project"}))
	if got.Code != http.StatusBadRequest {
		t.Errorf("assignment at a missing scope: %d %s, want 400", got.Code, got.Body)
	}
	w.assign("tok-owner", resource.PrincipalPerson, "sub-reader", "no-such-role", "projA", http.StatusBadRequest)
}

// Acceptance criterion 5: a role assigned to a group applies to whoever
// carries the group.
func TestAuthzAPI_GroupAssignment(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-owner", policy.OwnerRoleID, "/") })
	w.expect("tok-member", "GET", "/v1/components/"+w.id["web"], nil, http.StatusForbidden)
	w.assign("tok-owner", resource.PrincipalGroup, "platform", policy.ReaderRoleID, "projA", http.StatusCreated)
	w.expect("tok-member", "GET", "/v1/components/"+w.id["web"], nil, http.StatusOK)
	w.expect("tok-reader", "GET", "/v1/components/"+w.id["web"], nil, http.StatusForbidden)
}

// Acceptance criterion 6: constrained delegation.
func TestAuthzAPI_ConstrainedDelegation(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) {
		s := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](w.db, resource.KindRoleAssignment)
		if _, err := s.Create(t.Context(), resource.RoleAssignment{Metadata: resource.Metadata{Name: "delegate"}, Spec: resource.RoleAssignmentSpec{
			Principal: resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "sub-admin"}, RoleDefinitionID: policy.RBACAdministratorRoleID, Scope: w.scope("projA"),
			Delegation: &resource.Delegation{RoleDefinitionIDs: []string{policy.ReaderRoleID, policy.ContributorRoleID}, PrincipalTypes: []resource.PrincipalType{resource.PrincipalPerson}},
		}}); err != nil {
			t.Fatal(err)
		}
	})
	person := resource.PrincipalPerson
	id := w.assign("tok-admin", person, "sub-reader", policy.ReaderRoleID, "app", http.StatusCreated)
	w.assign("tok-admin", person, "sub-contrib", policy.ContributorRoleID, "prod", http.StatusCreated)
	w.assign("tok-admin", person, "sub-reader", policy.OwnerRoleID, "app", http.StatusForbidden)
	w.assign("tok-admin", resource.PrincipalGroup, "platform", policy.ReaderRoleID, "app", http.StatusForbidden)
	w.assign("tok-admin", person, "sub-admin", policy.ContributorRoleID, "app", http.StatusForbidden)
	w.assign("tok-admin", person, "sub-reader", policy.ReaderRoleID, "projB", http.StatusForbidden)
	// Removing what the delegation covers is allowed too.
	w.expect("tok-admin", "DELETE", authzPath+"roleAssignments/"+id, nil, http.StatusNoContent)

	var refused int
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE kind = 'RoleAssignment' AND actor = 'person:admin' AND policy_decision LIKE 'deny:%'`).Scan(&refused); err != nil || refused != 4 {
		t.Errorf("%d refused assignment changes audited (%v), want 4", refused, err)
	}
}

// Acceptance criterion 7: a custom role is created, assigned and enforced,
// and can't be deleted while assigned.
func TestAuthzAPI_CustomRoles(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-owner", policy.OwnerRoleID, "/") })
	role := map[string]any{"metadata": map[string]any{}, "spec": map[string]any{
		"roleName":         "Component operator",
		"permissions":      []resource.Permission{{Actions: []string{"serendip/components/*", "*/read"}, NotActions: []string{"serendip/components/delete"}}},
		"assignableScopes": []resource.Scope{w.scope("projA")},
	}}
	got := w.do("tok-owner", "POST", authzPath+"roleDefinitions", role)
	if got.Code != http.StatusCreated {
		t.Fatalf("create role: %d %s", got.Code, got.Body)
	}
	var created resource.RoleDefinition
	_ = json.Unmarshal(got.Body.Bytes(), &created)
	roleID := created.Metadata.ID

	w.expect("tok-owner", "POST", authzPath+"roleDefinitions", role, http.StatusConflict) // same name
	w.assign("tok-owner", resource.PrincipalPerson, "sub-mixed", roleID, "projB", http.StatusBadRequest)
	id := w.assign("tok-owner", resource.PrincipalPerson, "sub-mixed", roleID, "app", http.StatusCreated)
	w.put("tok-mixed", "/v1/components", "web", "", http.StatusOK)
	w.expect("tok-mixed", "DELETE", "/v1/components/"+w.id["web"], nil, http.StatusForbidden)

	w.expect("tok-owner", "DELETE", authzPath+"roleDefinitions/"+roleID, nil, http.StatusConflict)
	w.expect("tok-owner", "DELETE", authzPath+"roleAssignments/"+id, nil, http.StatusNoContent)
	w.expect("tok-owner", "DELETE", authzPath+"roleDefinitions/"+roleID, nil, http.StatusNoContent)
	w.expect("tok-owner", "DELETE", authzPath+"roleDefinitions/"+policy.ReaderRoleID, nil, http.StatusBadRequest)

	// Creating a role needs write wherever it's assignable.
	w.assign("tok-owner", resource.PrincipalPerson, "sub-contrib", policy.UserAccessAdministratorRoleID, "projB", http.StatusCreated)
	w.expect("tok-contrib", "POST", authzPath+"roleDefinitions", role, http.StatusForbidden)
	onB := map[string]any{"metadata": map[string]any{}, "spec": map[string]any{
		"roleName": "B reader", "permissions": []resource.Permission{{Actions: []string{"*/read"}}}, "assignableScopes": []resource.Scope{w.scope("projB")},
	}}
	w.expect("tok-contrib", "POST", authzPath+"roleDefinitions", onB, http.StatusCreated)
	// A custom role may not be assignable at "/".
	rootRole := map[string]any{"metadata": map[string]any{}, "spec": map[string]any{
		"roleName": "Everywhere", "permissions": []resource.Permission{{Actions: []string{"*/read"}}}, "assignableScopes": []resource.Scope{resource.RootScope},
	}}
	w.expect("tok-owner", "POST", authzPath+"roleDefinitions", rootRole, http.StatusBadRequest)
}

// Acceptance criterion 8: the last Owner at "/" can't be removed; one of
// several can.
func TestAuthzAPI_LastOwner(t *testing.T) {
	var ownerID string
	w := newAuthzWorld(t, func(w *authzWorld) {
		ownerID = grant(w.t, w.db, resource.PrincipalPerson, "sub-owner", policy.OwnerRoleID, resource.RootScope)
	})
	// The migrated API key is a second Owner at "/", so one can go.
	var keyAssignment string
	if err := w.db.QueryRow(`SELECT id FROM resources WHERE kind = 'RoleAssignment' AND json_extract(spec, '$.principal.id') = 'ci'`).Scan(&keyAssignment); err != nil {
		t.Fatal(err)
	}
	w.expect("tok-owner", "DELETE", authzPath+"roleAssignments/"+keyAssignment, nil, http.StatusNoContent)
	got := w.do("tok-owner", "DELETE", authzPath+"roleAssignments/"+ownerID, nil)
	if got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "last Owner") {
		t.Errorf("removing the last Owner: %d %s, want 409", got.Code, got.Body)
	}
	w.expect("tok-owner", "DELETE", authzPath+"roleAssignments/no-such-id", nil, http.StatusNotFound)
	w.expect("tok-reader", "DELETE", authzPath+"roleAssignments/"+ownerID, nil, http.StatusForbidden)
}

// Listing at a scope shows what applies there, inherited ones marked;
// without a scope, only assignments the caller may read.
func TestAuthzAPI_ListAssignments(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) {
		w.grant("tok-reader", policy.ReaderRoleID, "projA")
		w.grant("tok-contrib", policy.ContributorRoleID, "app")
		w.grant("tok-mixed", policy.ReaderRoleID, "projB")
	})
	type view struct {
		Spec      resource.RoleAssignmentSpec `json:"spec"`
		Inherited bool                        `json:"inherited"`
	}
	list := func(token, query string) []view {
		t.Helper()
		got := w.do(token, "GET", authzPath+"roleAssignments"+query, nil)
		if got.Code != http.StatusOK {
			t.Fatalf("list %s: %d %s", query, got.Code, got.Body)
		}
		var out []view
		_ = json.Unmarshal(got.Body.Bytes(), &out)
		return out
	}
	at := list("tok-reader", "?scope=Application/"+w.id["app"])
	var who []string
	for _, v := range at {
		who = append(who, v.Spec.Principal.ID)
		if v.Spec.Principal.ID == "sub-reader" && !v.Inherited || v.Spec.Principal.ID == "sub-contrib" && v.Inherited {
			t.Errorf("inherited marker wrong: %+v", v)
		}
	}
	for _, want := range []string{"sub-reader", "sub-contrib", "ci"} {
		if !slices.Contains(who, want) {
			t.Errorf("assignments at the application lack %s: %v", want, who)
		}
	}
	if slices.Contains(who, "sub-mixed") {
		t.Errorf("an assignment on projB is listed at projA's application: %v", who)
	}
	w.expect("tok-reader", "GET", authzPath+"roleAssignments?scope=Project/"+w.id["projB"], nil, http.StatusForbidden)
	for _, v := range list("tok-reader", "") {
		if v.Spec.Principal.ID == "sub-mixed" || v.Spec.Principal.ID == "ci" {
			t.Errorf("reader sees an assignment outside projA: %+v", v)
		}
	}
	if got := list("tok-reader", "?principalId=sub-contrib"); len(got) != 1 {
		t.Errorf("filter by principal: %+v", got)
	}
	w.expect("tok-reader", "GET", authzPath+"roleAssignments?scope=bogus", nil, http.StatusBadRequest)
}

func TestAuthzAPI_PermissionsDefinitionsPrincipals(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) {
		w.grant("tok-reader", policy.ReaderRoleID, "projA")
		w.grant("tok-admin", policy.RBACAdministratorRoleID, "projA")
	})
	var perms struct {
		Scope       string                `json:"scope"`
		Permissions []resource.Permission `json:"permissions"`
	}
	got := w.do("tok-reader", "GET", authzPath+"permissions?scope=Component/"+w.id["web"], nil)
	if err := json.Unmarshal(got.Body.Bytes(), &perms); err != nil || got.Code != http.StatusOK ||
		len(perms.Permissions) != 1 || !slices.Equal(perms.Permissions[0].Actions, []string{"*/read"}) {
		t.Errorf("reader's permissions at web: %d %s", got.Code, got.Body)
	}
	got = w.do("tok-reader", "GET", authzPath+"permissions?scope=/", nil)
	_ = json.Unmarshal(got.Body.Bytes(), &perms)
	if perms.Scope != "/" || len(perms.Permissions) != 0 {
		t.Errorf("reader's permissions at /: %s", got.Body)
	}

	var defs []struct {
		Metadata resource.Metadata `json:"metadata"`
		BuiltIn  bool              `json:"builtIn"`
	}
	got = w.do("tok-nobody", "GET", authzPath+"roleDefinitions", nil)
	if err := json.Unmarshal(got.Body.Bytes(), &defs); err != nil || len(defs) != len(policy.BuiltInRoles()) || !defs[0].BuiltIn {
		t.Errorf("role definitions: %d %s", got.Code, got.Body)
	}
	w.expect("tok-nobody", "GET", authzPath+"roleDefinitions/"+policy.OwnerRoleID, nil, http.StatusOK)

	w.expect("tok-reader", "GET", "/v1/components/"+w.id["web"], nil, http.StatusOK) // seen once
	// The principals directory is for those who can assign somewhere: a
	// project-scoped administrator yes, a Reader no.
	var principals []sqlite.KnownPrincipal
	got = w.do("tok-admin", "GET", authzPath+"principals?type=person", nil)
	if err := json.Unmarshal(got.Body.Bytes(), &principals); err != nil || got.Code != http.StatusOK || len(principals) == 0 {
		t.Errorf("principals: %d %s", got.Code, got.Body)
	}
	w.expect("tok-reader", "GET", authzPath+"principals", nil, http.StatusForbidden)
	w.expect("tok-admin", "GET", authzPath+"principals?type=robot", nil, http.StatusBadRequest)
}

// The server chooses IDs: a client can't create a role whose ID shadows a
// built-in's, and an unknown assignment ID tells a non-Owner nothing.
func TestAuthzAPI_ServerChosenIDs(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-owner", policy.OwnerRoleID, "/") })
	role := map[string]any{"metadata": map[string]any{"id": policy.ReaderRoleID}, "spec": map[string]any{
		"roleName": "Squatter", "permissions": []resource.Permission{{Actions: []string{"*"}}}, "assignableScopes": []resource.Scope{w.scope("projA")},
	}}
	got := w.do("tok-owner", "POST", authzPath+"roleDefinitions", role)
	var created resource.RoleDefinition
	if err := json.Unmarshal(got.Body.Bytes(), &created); err != nil || got.Code != http.StatusCreated || created.Metadata.ID == policy.ReaderRoleID {
		t.Errorf("role with a built-in's ID: %d %s", got.Code, got.Body)
	}
	w.expect("tok-reader", "DELETE", authzPath+"roleAssignments/no-such-id", nil, http.StatusForbidden)
	w.expect("tok-owner", "GET", authzPath+"roleAssignments/no-such-id", nil, http.StatusNotFound)
}

// Two Owners removing each other's root Owner assignment at the same time
// must leave exactly one: the check and the write happen under one lock.
func TestAuthzAPI_ConcurrentRemovalsKeepAnOwner(t *testing.T) {
	var ownerA, ownerB string
	w := newAuthzWorld(t, func(w *authzWorld) {
		ownerA = grant(w.t, w.db, resource.PrincipalPerson, "sub-owner", policy.OwnerRoleID, resource.RootScope)
		ownerB = grant(w.t, w.db, resource.PrincipalPerson, "sub-admin", policy.OwnerRoleID, resource.RootScope)
	})
	// The migrated API key is a third root Owner; remove it first so two
	// remain.
	var key string
	if err := w.db.QueryRow(`SELECT id FROM resources WHERE kind = 'RoleAssignment' AND json_extract(spec, '$.principal.id') = 'ci'`).Scan(&key); err != nil {
		t.Fatal(err)
	}
	w.expect("tok-owner", "DELETE", authzPath+"roleAssignments/"+key, nil, http.StatusNoContent)

	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, c := range []struct{ token, target string }{{"tok-owner", ownerB}, {"tok-admin", ownerA}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- w.do(c.token, "DELETE", authzPath+"roleAssignments/"+c.target, nil).Code
		}()
	}
	wg.Wait()
	close(codes)
	var got []int
	for c := range codes {
		got = append(got, c)
	}
	slices.Sort(got)
	// One removal wins; the other is refused, either as the last Owner
	// (409) or because its caller just lost Owner (403).
	if got[0] != http.StatusNoContent || (got[1] != http.StatusConflict && got[1] != http.StatusForbidden) {
		t.Errorf("concurrent removals answered %v, want one 204 and one refusal", got)
	}
	var owners int
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM resources WHERE kind = 'RoleAssignment' AND json_extract(spec, '$.roleDefinitionId') = ? AND json_extract(spec, '$.scope.kind') IS NULL`, policy.OwnerRoleID).Scan(&owners); err != nil || owners != 1 {
		t.Errorf("%d Owners at / remain (%v), want exactly 1", owners, err)
	}
}
