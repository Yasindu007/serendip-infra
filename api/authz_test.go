package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// authzWorld is an ownership tree, people with roles in it, and a server:
//
//	org ── projA ── prod ── app ── web, secret
//	             └─ dev ── devapp ── devweb
//	    └─ projB ── appB
type authzWorld struct {
	t  *testing.T
	db *sql.DB
	h  http.Handler
	id map[string]string // name -> resource ID
}

// people maps each test token to its person.
var people = map[string]identity.Principal{
	"tok-reader":  {Subject: "sub-reader", Name: "reader"},
	"tok-contrib": {Subject: "sub-contrib", Name: "contrib"},
	"tok-owner":   {Subject: "sub-owner", Name: "owner"},
	"tok-mixed":   {Subject: "sub-mixed", Name: "mixed"},
	"tok-nobody":  {Subject: "sub-nobody", Name: "nobody"},
	"tok-admin":   {Subject: "sub-admin", Name: "admin"},
	"tok-member":  {Subject: "sub-member", Name: "member", Groups: []string{"platform"}},
}

func newAuthzWorld(t *testing.T, grants func(w *authzWorld)) *authzWorld {
	t.Helper()
	ctx := context.Background()
	w := &authzWorld{t: t, db: newTestDB(t), id: map[string]string{}}
	mk := func(name string, kind resource.Kind, owner string) {
		t.Helper()
		var refs []resource.OwnerRef
		if owner != "" {
			refs = []resource.OwnerRef{{Kind: kindOf(owner), ID: w.id[owner]}}
		}
		meta := resource.Metadata{Name: name, OwnerRefs: refs}
		var id string
		var err error
		switch kind {
		case resource.KindOrganization:
			id, err = create(ctx, sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](w.db, kind), meta)
		case resource.KindProject:
			id, err = create(ctx, sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](w.db, kind), meta)
		case resource.KindEnvironment:
			id, err = create(ctx, sqlite.NewStore[resource.EnvironmentSpec, resource.EnvironmentStatus](w.db, kind), meta)
		case resource.KindApplication:
			id, err = create(ctx, sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](w.db, kind), meta)
		case resource.KindComponent:
			id, err = create(ctx, sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](w.db, kind), meta)
		case resource.KindSecret:
			s := sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](w.db, kind)
			var sec resource.Secret
			sec, err = s.Create(ctx, resource.Secret{Metadata: meta, Spec: resource.SecretSpec{Path: "app/" + name}})
			id = sec.Metadata.ID
		}
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		w.id[name] = id
	}
	mk("org", resource.KindOrganization, "")
	mk("projA", resource.KindProject, "org")
	mk("projB", resource.KindProject, "org")
	mk("prod", resource.KindEnvironment, "projA")
	mk("dev", resource.KindEnvironment, "projA")
	mk("app", resource.KindApplication, "prod")
	mk("devapp", resource.KindApplication, "dev")
	mk("appB", resource.KindApplication, "projB")
	mk("web", resource.KindComponent, "app")
	mk("devweb", resource.KindComponent, "devapp")
	mk("secret", resource.KindSecret, "app")
	grants(w)

	authn := &fakeAuthn{tokens: people}
	bus := event.NewInProcessBus()
	secrets := sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](w.db, resource.KindSecret).SetBus(bus)
	rec := reconcile.NewSecretReconciler(secrets, newFakeSecretsProvider())
	w.h = api.NewServer(w.db, bus, map[string]string{"key-ci": "ci"}, authn, rec, nil, nil).Handler
	return w
}

func create[S, St any](ctx context.Context, s *sqlite.Store[S, St], meta resource.Metadata) (string, error) {
	r, err := s.Create(ctx, resource.Resource[S, St]{Metadata: meta})
	return r.Metadata.ID, err
}

func kindOf(name string) resource.Kind {
	switch {
	case name == "org":
		return resource.KindOrganization
	case strings.HasPrefix(name, "proj"):
		return resource.KindProject
	case name == "prod" || name == "dev":
		return resource.KindEnvironment
	case strings.HasSuffix(name, "web"):
		return resource.KindComponent
	case name == "secret":
		return resource.KindSecret
	}
	return resource.KindApplication
}

func (w *authzWorld) scope(name string) resource.Scope {
	return resource.Scope{Kind: kindOf(name), ID: w.id[name]}
}

func (w *authzWorld) grant(token, role, at string) {
	w.t.Helper()
	scope := resource.RootScope
	if at != "/" {
		scope = w.scope(at)
	}
	grant(w.t, w.db, resource.PrincipalPerson, people[token].Subject, role, scope)
}

func (w *authzWorld) do(token, method, path string, body any) *httptest.ResponseRecorder {
	w.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, req)
	return rec
}

func (w *authzWorld) expect(token, method, path string, body any, want int) {
	w.t.Helper()
	if got := w.do(token, method, path, body); got.Code != want {
		w.t.Errorf("%s %s %s: %d, want %d (%s)", people[token].Name, method, path, got.Code, want, strings.TrimSpace(got.Body.String()))
	}
}

func (w *authzWorld) names(token, path string) []string {
	w.t.Helper()
	got := w.do(token, http.MethodGet, path, nil)
	if got.Code != http.StatusOK {
		w.t.Fatalf("list %s: %d %s", path, got.Code, got.Body)
	}
	var items []struct {
		Metadata resource.Metadata `json:"metadata"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &items); err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for _, it := range items {
		out = append(out, it.Metadata.Name)
	}
	slices.Sort(out)
	return out
}

// current fetches a resource as the Owner API key, to PUT back with its
// resource version, optionally under a new owner.
func (w *authzWorld) current(path, owner string) map[string]any {
	w.t.Helper()
	got := w.do("key-ci", http.MethodGet, path, nil)
	var doc map[string]any
	if err := json.Unmarshal(got.Body.Bytes(), &doc); err != nil || got.Code != http.StatusOK {
		w.t.Fatalf("get %s: %d %s", path, got.Code, got.Body)
	}
	if owner != "" {
		doc["metadata"].(map[string]any)["ownerRefs"] = []resource.OwnerRef{{Kind: kindOf(owner), ID: w.id[owner]}}
	}
	return doc
}

// put updates the named resource as token, optionally moving it.
func (w *authzWorld) put(token, path, name, owner string, want int) {
	w.t.Helper()
	full := path + "/" + w.id[name]
	w.expect(token, http.MethodPut, full, w.current(full, owner), want)
}

func component(name string, owner resource.Scope) map[string]any {
	return map[string]any{"metadata": map[string]any{"name": name, "ownerRefs": []resource.OwnerRef{{Kind: owner.Kind, ID: owner.ID}}}, "spec": map[string]any{}}
}

// Acceptance criterion 1.
func TestAuthz_Reader(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-reader", policy.ReaderRoleID, "projA") })
	if got, want := w.names("tok-reader", "/v1/applications"), []string{"app", "devapp"}; !slices.Equal(got, want) {
		t.Errorf("applications listed = %v, want %v (nothing from projB)", got, want)
	}
	if got, want := w.names("tok-reader", "/v1/components"), []string{"devweb", "web"}; !slices.Equal(got, want) {
		t.Errorf("components listed = %v, want %v", got, want)
	}
	w.expect("tok-reader", "GET", "/v1/components/"+w.id["web"], nil, http.StatusOK)
	w.expect("tok-reader", "GET", "/v1/applications/"+w.id["appB"], nil, http.StatusForbidden)
	w.expect("tok-reader", "POST", "/v1/components", component("new", w.scope("app")), http.StatusForbidden)
	w.put("tok-reader", "/v1/components", "web", "", http.StatusForbidden)
	w.expect("tok-reader", "DELETE", "/v1/components/"+w.id["web"], nil, http.StatusForbidden)
	// Someone with no role sees empty lists, not errors.
	if got := w.names("tok-nobody", "/v1/applications"); len(got) != 0 {
		t.Errorf("nobody listed %v", got)
	}
}

// Acceptance criterion 2 (the role-assignment half is T006's endpoints).
func TestAuthz_Contributor(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-contrib", policy.ContributorRoleID, "prod") })
	w.put("tok-contrib", "/v1/components", "web", "", http.StatusOK)
	w.expect("tok-contrib", "POST", "/v1/components", component("api", w.scope("app")), http.StatusCreated)
	w.put("tok-contrib", "/v1/components", "devweb", "", http.StatusForbidden)
	w.expect("tok-contrib", "DELETE", "/v1/components/"+w.id["devweb"], nil, http.StatusForbidden)
	w.expect("tok-contrib", "DELETE", "/v1/components/"+w.id["web"], nil, http.StatusNoContent)
	// Creating with no owner happens at "/", where they have nothing.
	w.expect("tok-contrib", "POST", "/v1/organizations", map[string]any{"metadata": map[string]any{"name": "rogue"}, "spec": map[string]any{}}, http.StatusForbidden)
}

// Moving a resource under a parent the caller can't write is refused.
func TestAuthz_MoveNeedsWriteAtTheNewParent(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-contrib", policy.ContributorRoleID, "prod") })
	w.put("tok-contrib", "/v1/components", "web", "devapp", http.StatusForbidden)
	// Moving within what they control is fine.
	w.put("tok-contrib", "/v1/components", "web", "prod", http.StatusOK)
}

// Acceptance criterion 3.
func TestAuthz_SecretValuesNeedADataRole(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) {
		w.grant("tok-owner", policy.OwnerRoleID, "/")
		w.grant("tok-mixed", policy.SecretsUserRoleID, "secret")
	})
	value := "/v1/secrets/" + w.id["secret"] + "/value"
	w.expect("tok-owner", "POST", value, map[string]any{"data": map[string]any{"k": "v"}}, http.StatusForbidden)
	w.expect("tok-owner", "GET", value, nil, http.StatusForbidden)
	w.expect("tok-owner", "POST", "/v1/secrets", map[string]any{"metadata": map[string]any{"name": "other"}, "spec": map[string]any{"path": "x/y"}}, http.StatusCreated)
	w.expect("tok-mixed", "GET", "/v1/secrets/"+w.id["secret"], nil, http.StatusOK)
	w.expect("tok-mixed", "POST", value, map[string]any{"data": map[string]any{"k": "v"}}, http.StatusForbidden)
	w.expect("tok-mixed", "DELETE", "/v1/secrets/"+w.id["secret"], nil, http.StatusForbidden)
	// The migrated API key is Owner at "/", which has no data actions.
	w.expect("key-ci", "POST", value, map[string]any{"data": map[string]any{"k": "v"}}, http.StatusForbidden)
}

// Acceptance criterion 4.
func TestAuthz_InheritanceAndUnion(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) {
		w.grant("tok-mixed", policy.ContributorRoleID, "projA")
		w.grant("tok-mixed", policy.ReaderRoleID, "app")
	})
	// Contributor at the project still applies under the lower Reader.
	w.put("tok-mixed", "/v1/components", "web", "", http.StatusOK)
	w.put("tok-mixed", "/v1/environments", "dev", "", http.StatusOK)
	w.put("tok-mixed", "/v1/applications", "appB", "", http.StatusForbidden)
}

func TestAuthz_PlatformRoutes(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-reader", policy.ReaderRoleID, "projA") })
	// Platform-wide reads are decided at "/".
	w.expect("tok-reader", "GET", "/v1/readiness", nil, http.StatusForbidden)
	w.expect("key-ci", "GET", "/v1/readiness", nil, http.StatusOK)
	// Graph reads need read on the named resource, and hide what the
	// caller can't read.
	w.expect("tok-reader", "GET", "/v1/graph/Project/"+w.id["projB"]+"/children", nil, http.StatusForbidden)
	w.expect("tok-reader", "GET", "/v1/graph/Unknown/x/children", nil, http.StatusNotFound)
	got := w.do("key-ci", "GET", "/v1/graph/Organization/"+w.id["org"]+"/children", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), w.id["projB"]) {
		t.Fatalf("Owner graph children: %d %s", got.Code, got.Body)
	}
	got = w.do("tok-reader", "GET", "/v1/graph/Project/"+w.id["projA"]+"/children", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), w.id["prod"]) {
		t.Errorf("Reader graph children of projA: %d %s", got.Code, got.Body)
	}
}

// The 403 names the missing action and scope; the allowed mutation's
// audit row names the deciding assignment; the refused mutation leaves a
// row with no before or after (plan.md decision 8, acceptance criterion
// 10).
func TestAuthz_DecisionRecords(t *testing.T) {
	var contribAssignment string
	w := newAuthzWorld(t, func(w *authzWorld) {
		contribAssignment = grant(w.t, w.db, resource.PrincipalPerson, "sub-contrib", policy.ContributorRoleID, w.scope("prod"))
	})
	denied := w.do("tok-contrib", "DELETE", "/v1/components/"+w.id["devweb"], nil)
	var p api.Problem
	if err := json.Unmarshal(denied.Body.Bytes(), &p); err != nil || denied.Code != http.StatusForbidden {
		t.Fatalf("denied: %d %s", denied.Code, denied.Body)
	}
	if want := fmt.Sprintf("missing serendip/components/delete at Component/%s", w.id["devweb"]); p.Detail != want {
		t.Errorf("403 detail = %q, want %q", p.Detail, want)
	}
	w.put("tok-contrib", "/v1/components", "web", "", http.StatusOK)
	// Refused reads aren't audited.
	w.expect("tok-contrib", "GET", "/v1/components/"+w.id["devweb"], nil, http.StatusForbidden)

	rows, err := w.db.Query(`SELECT resource_id, actor, COALESCE(actor_subject,''), before IS NULL AND after IS NULL, COALESCE(policy_decision,''), COALESCE(request_id,'')
		FROM audit_log WHERE kind = 'Component' AND actor = 'person:contrib' ORDER BY at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct {
		id, actor, subject string
		empty              bool
		decision, request  string
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.actor, &r.subject, &r.empty, &r.decision, &r.request); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("audit rows = %+v, want the refused delete and the allowed update", got)
	}
	refused, allowed := got[0], got[1]
	if refused.id != w.id["devweb"] || !refused.empty || refused.subject != "sub-contrib" || refused.request == "" ||
		refused.decision != fmt.Sprintf("deny: missing serendip/components/delete at Component/%s", w.id["devweb"]) {
		t.Errorf("refused row = %+v", refused)
	}
	if want := fmt.Sprintf("allow: Contributor (%s) at Environment/%s", contribAssignment, w.id["prod"]); allowed.id != w.id["web"] || allowed.empty || allowed.decision != want {
		t.Errorf("allowed row = %+v, want decision %q", allowed, want)
	}
}

// Existing API keys keep their full access, and the migration says so in
// the assignment itself.
func TestAuthz_APIKeysAreMigratedToOwner(t *testing.T) {
	w := newAuthzWorld(t, func(*authzWorld) {})
	w.expect("key-ci", "DELETE", "/v1/components/"+w.id["devweb"], nil, http.StatusNoContent)
	var n int
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM resources WHERE kind = 'RoleAssignment' AND json_extract(spec, '$.principal.id') = 'ci'
		AND json_extract(spec, '$.roleDefinitionId') = ? AND json_extract(spec, '$.description') LIKE 'migrated%'`, policy.OwnerRoleID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d migrated Owner assignments for key ci, want 1", n)
	}
	// A second server over the same database doesn't add another.
	api.NewServer(w.db, event.NewInProcessBus(), map[string]string{"key-ci": "ci"}, nil, nil, nil, nil)
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM resources WHERE kind = 'RoleAssignment' AND json_extract(spec, '$.principal.id') = 'ci'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("after a restart: %d assignments for key ci (%v), want 1", n, err)
	}
}

// A body the check can't read is refused before any handler runs, so its
// owners can't escape the decision. A valid resource followed by trailing
// bytes is the shape json.Decoder and json.Unmarshal disagree on.
func TestAuthz_UnreadableBodiesNeverSkipTheCheck(t *testing.T) {
	w := newAuthzWorld(t, func(*authzWorld) {})
	raw := func(method, path, body string) int {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok-nobody")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		w.h.ServeHTTP(rec, req)
		return rec.Code
	}
	org := `{"metadata":{"name":"sneaky"},"spec":{}}`
	for _, tc := range []struct {
		name, method, path, body string
	}{
		{"create with trailing bytes", "POST", "/v1/organizations", org + ` x`},
		{"create with a second value", "POST", "/v1/organizations", org + `{"metadata":{"name":"two"}}`},
		{"update with trailing bytes", "PUT", "/v1/components/" + w.id["web"], `{"metadata":{"name":"web"},"spec":{}} x`},
		{"not JSON", "POST", "/v1/organizations", `nope`},
	} {
		if code := raw(tc.method, tc.path, tc.body); code == http.StatusCreated || code == http.StatusOK {
			t.Errorf("%s: %d, a role-less caller changed something", tc.name, code)
		}
	}
	if code := raw("POST", "/v1/organizations", org+` x`); code != http.StatusForbidden {
		t.Errorf("valid resource plus trailing bytes: %d, want 403 (decided, not skipped)", code)
	}
	if code := raw("POST", "/v1/organizations", `{"metadata":`); code != http.StatusBadRequest {
		t.Errorf("truncated body: %d, want 400", code)
	}
	if code := raw("POST", "/v1/organizations", `{"metadata":{"name":"`+strings.Repeat("a", 2<<20)+`"}}`); code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: %d, want 413", code)
	}
	var n int
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM resources WHERE kind = 'Organization'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d organizations (%v), want only the fixture's", n, err)
	}
}

// Revoking a migrated key's Owner assignment sticks across a restart.
func TestAuthz_RevokedAPIKeyStaysRevoked(t *testing.T) {
	w := newAuthzWorld(t, func(*authzWorld) {})
	var id string
	if err := w.db.QueryRow(`SELECT id FROM resources WHERE kind = 'RoleAssignment' AND json_extract(spec, '$.principal.id') = 'ci'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	s := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](w.db, resource.KindRoleAssignment)
	if err := s.Delete(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	h := api.NewServer(w.db, event.NewInProcessBus(), map[string]string{"key-ci": "ci"}, nil, nil, nil, nil).Handler
	w.h = h
	w.expect("key-ci", "GET", "/v1/organizations/"+w.id["org"], nil, http.StatusForbidden)
}

// A cascading delete needs delete on everything it would remove, of every
// kind, not only on the resource named.
func TestAuthz_CascadeNeedsDeleteOnEveryDescendant(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) {
		// A custom role that can delete Applications and Components, but
		// nothing on Secrets, held at the application.
		defs := sqlite.NewStore[resource.RoleDefinitionSpec, resource.RoleDefinitionStatus](w.db, resource.KindRoleDefinition)
		role, err := defs.Create(context.Background(), resource.RoleDefinition{Metadata: resource.Metadata{Name: "app-ops"},
			Spec: resource.RoleDefinitionSpec{RoleName: "App ops",
				Permissions:      []resource.Permission{{Actions: []string{"serendip/applications/*", "serendip/components/*"}}},
				AssignableScopes: []resource.Scope{resource.RootScope}}})
		if err != nil {
			w.t.Fatal(err)
		}
		grant(w.t, w.db, resource.PrincipalPerson, "sub-mixed", role.Metadata.ID, w.scope("app"))
		w.grant("tok-contrib", policy.ContributorRoleID, "prod")
	})
	app := "/v1/applications/" + w.id["app"] + "?cascade=true"
	got := w.do("tok-mixed", "DELETE", app, nil)
	if got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "serendip/secrets/delete at Secret/"+w.id["secret"]) {
		t.Errorf("cascade over a secret without secrets/delete: %d %s", got.Code, got.Body)
	}
	w.expect("tok-mixed", "GET", "/v1/components/"+w.id["web"], nil, http.StatusOK)
	// Contributor at prod covers the application, its component and its
	// secret.
	w.expect("tok-contrib", "DELETE", app, nil, http.StatusNoContent)
}

// Moving a resource out from under an owner needs write at that owner too;
// dropping every owner needs write at "/".
func TestAuthz_RemovingOwnersIsAMove(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-contrib", policy.ContributorRoleID, "web") })
	full := "/v1/components/" + w.id["web"]
	orphan := w.current(full, "")
	delete(orphan["metadata"].(map[string]any), "ownerRefs")
	w.expect("tok-contrib", "PUT", full, orphan, http.StatusForbidden)
	// Re-parenting to the grandparent leaves the application: refused
	// without write there.
	w.put("tok-contrib", "/v1/components", "web", "prod", http.StatusForbidden)
	// Unchanged owners need nothing more than write on the component.
	w.put("tok-contrib", "/v1/components", "web", "", http.StatusOK)
}

// Duplicate keys resolve the same way for the check and the handler (the
// last one wins in both), so the checked owner is the stored owner.
func TestAuthz_DuplicateOwnerRefsKeys(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-contrib", policy.ContributorRoleID, "prod") })
	body := fmt.Sprintf(`{"metadata":{"name":"dup","ownerRefs":[{"kind":"Application","id":%q}],"ownerRefs":[{"kind":"Application","id":%q}]},"spec":{}}`,
		w.id["app"], w.id["devapp"])
	req := httptest.NewRequest("POST", "/v1/components", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok-contrib")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("create under devapp via a duplicate key: %d %s, want 403", rec.Code, rec.Body)
	}
}
