package api_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/template"
)

// Spec 027 T006: Templates and their versions (plan decisions 1, 5 and 10).

func templateDoc(name, project string) map[string]any {
	meta := map[string]any{"name": name}
	if project != "" {
		meta["ownerRefs"] = []resource.OwnerRef{{Kind: resource.KindProject, ID: project}}
	}
	return map[string]any{"metadata": meta, "spec": resource.TemplateSpec{DisplayName: name}}
}

// versionDoc is web-server 1.0's parameters and body under label.
func versionDoc(label string) map[string]any {
	b, _ := template.LookupBuiltIn(template.WebServerID)
	s := b.Versions[0].Spec
	return map[string]any{"metadata": map[string]any{"name": label},
		"spec": map[string]any{"parameters": s.Parameters, "body": s.Body}}
}

type listed struct {
	Metadata resource.Metadata       `json:"metadata"`
	Status   resource.TemplateStatus `json:"status"`
	BuiltIn  bool                    `json:"builtIn"`
	Latest   bool                    `json:"latest"`
}

func (w *authzWorld) listed(token, path string) []listed {
	w.t.Helper()
	got := w.do(token, http.MethodGet, path, nil)
	if got.Code != http.StatusOK {
		w.t.Fatalf("GET %s as %s: %d %s", path, people[token].Name, got.Code, got.Body)
	}
	var out []listed
	if err := json.Unmarshal(got.Body.Bytes(), &out); err != nil {
		w.t.Fatal(err)
	}
	return out
}

func listedNames(items []listed) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Metadata.Name)
	}
	return out
}

func statusReady(s resource.Status) bool {
	return len(s.Conditions) == 1 && s.Conditions[0].Type == "Ready" && s.Conditions[0].Status == resource.ConditionTrue
}

func TestTemplates_ScopeDecidesWhoSeesAndWrites(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) {
		w.grant("tok-contrib", policy.ContributorRoleID, "projA")
		w.grant("tok-reader", policy.ReaderRoleID, "projA")
	})
	starters := []string{"web-server", "service-from-source", "web-and-api"}

	mine := w.mkAs("tok-contrib", "/v1/templates", templateDoc("team-web", w.id["projA"]))
	// A Template at "/" needs write at "/".
	w.expect("tok-contrib", http.MethodPost, "/v1/templates", templateDoc("everyone", ""), http.StatusForbidden)
	w.mkAs("key-ci", "/v1/templates", templateDoc("everyone", ""))
	w.mkAs("key-ci", "/v1/templates", templateDoc("b-only", w.id["projB"]))
	// Owned by an Application: refused by validation, once allowed.
	w.expect("tok-contrib", http.MethodPost, "/v1/templates", map[string]any{
		"metadata": map[string]any{"name": "x", "ownerRefs": []resource.OwnerRef{{Kind: resource.KindApplication, ID: w.id["app"]}}}, "spec": map[string]any{},
	}, http.StatusBadRequest)
	w.expect("tok-contrib", http.MethodPost, "/v1/templates", templateDoc("", w.id["projA"]), http.StatusBadRequest)

	// The built-ins first, then what the caller may read.
	if got, want := listedNames(w.listed("tok-nobody", "/v1/templates")), starters; !slices.Equal(got, want) {
		t.Errorf("nobody lists %v, want %v", got, want)
	}
	if got, want := listedNames(w.listed("tok-reader", "/v1/templates")), append(slices.Clone(starters), "team-web"); !slices.Equal(got, want) {
		t.Errorf("reader lists %v, want %v", got, want)
	}
	if got, want := listedNames(w.listed("key-ci", "/v1/templates")), append(slices.Clone(starters), "team-web", "everyone", "b-only"); !slices.Equal(got, want) {
		t.Errorf("owner lists %v, want %v", got, want)
	}
	// A filter by owner leaves the built-ins out.
	if got := listedNames(w.listed("key-ci", "/v1/templates?ownerId="+w.id["projA"])); !slices.Equal(got, []string{"team-web"}) {
		t.Errorf("by owner: %v", got)
	}

	w.expect("tok-nobody", http.MethodGet, "/v1/templates/"+mine, nil, http.StatusForbidden)
	w.expect("tok-nobody", http.MethodGet, "/v1/templates/"+mine+"/versions", nil, http.StatusForbidden)
	got := w.listed("tok-reader", "/v1/templates")
	if !statusReady(got[3].Status.Status) || got[3].BuiltIn {
		t.Errorf("stored template: %+v, want Ready and not built in", got[3])
	}

	// Writing needs write at its scope; a Reader may only read.
	doc := w.current("/v1/templates/"+mine, "")
	doc["spec"].(map[string]any)["description"] = "edited"
	w.expect("tok-reader", http.MethodPut, "/v1/templates/"+mine, doc, http.StatusForbidden)
	w.expect("tok-contrib", http.MethodPut, "/v1/templates/"+mine, doc, http.StatusOK)
	w.expect("tok-reader", http.MethodPost, "/v1/templates/"+mine+"/versions", versionDoc("1.0"), http.StatusForbidden)
	w.expect("tok-contrib", http.MethodPost, "/v1/templates/"+mine+"/versions", versionDoc("1.0"), http.StatusCreated)
	w.expect("tok-reader", http.MethodGet, "/v1/templates/"+mine+"/versions/1.0", nil, http.StatusOK)
	w.expect("tok-reader", http.MethodDelete, "/v1/templates/"+mine+"/versions/1.0", nil, http.StatusForbidden)
	w.expect("tok-nobody", http.MethodDelete, "/v1/templates/"+mine+"/versions/1.0", nil, http.StatusForbidden)
	w.expect("tok-nobody", http.MethodDelete, "/v1/templates/"+mine+"/versions/9.9", nil, http.StatusForbidden)
}

func TestTemplates_BuiltInsAreReadableAndFixed(t *testing.T) {
	w := newAuthzWorld(t, func(*authzWorld) {})
	id := template.WebServerID
	got := w.do("tok-nobody", http.MethodGet, "/v1/templates/"+id, nil)
	var one listed
	if err := json.Unmarshal(got.Body.Bytes(), &one); got.Code != http.StatusOK || err != nil || !one.BuiltIn || one.Metadata.Name != "web-server" || !statusReady(one.Status.Status) {
		t.Fatalf("get a built-in as nobody: %d %s", got.Code, got.Body)
	}
	vs := w.listed("tok-nobody", "/v1/templates/"+id+"/versions")
	if len(vs) != 1 || vs[0].Metadata.Name != "1.0" || !vs[0].Latest || !vs[0].BuiltIn {
		t.Errorf("built-in versions: %+v", vs)
	}
	w.expect("tok-nobody", http.MethodGet, "/v1/templates/"+id+"/versions/1.0", nil, http.StatusOK)
	w.expect("tok-nobody", http.MethodGet, "/v1/templates/"+id+"/versions/2.0", nil, http.StatusNotFound)

	// Changing one is refused: 403 without a role, 400 for an Owner.
	doc := map[string]any{"metadata": map[string]any{"name": "web-server"}, "spec": map[string]any{"description": "mine now"}}
	w.expect("tok-nobody", http.MethodPut, "/v1/templates/"+id, doc, http.StatusForbidden)
	w.expect("key-ci", http.MethodPut, "/v1/templates/"+id, doc, http.StatusBadRequest)
	w.expect("key-ci", http.MethodDelete, "/v1/templates/"+id, nil, http.StatusBadRequest)
	w.expect("key-ci", http.MethodPost, "/v1/templates/"+id+"/versions", versionDoc("2.0"), http.StatusBadRequest)
	w.expect("key-ci", http.MethodDelete, "/v1/templates/"+id+"/versions/1.0", nil, http.StatusBadRequest)
	// A stored Template can't take a built-in's ID, or any ID: the server
	// chooses it.
	taken := templateDoc("impostor", "")
	taken["metadata"].(map[string]any)["id"] = id
	if got := w.mkAs("key-ci", "/v1/templates", taken); got == id || got == "" {
		t.Errorf("created with ID %q", got)
	}
	w.expect("tok-nobody", http.MethodGet, "/v1/templates/"+id, nil, http.StatusOK)
}

func TestTemplateVersions_PublishedOnceNewestIsLatest(t *testing.T) {
	w := newAuthzWorld(t, func(*authzWorld) {})
	tid := w.mkAs("key-ci", "/v1/templates", templateDoc("t", ""))
	versions := "/v1/templates/" + tid + "/versions"

	got := w.do("key-ci", http.MethodPost, versions, versionDoc("2.0"))
	var pub struct {
		Metadata resource.Metadata              `json:"metadata"`
		Spec     resource.TemplateVersionSpec   `json:"spec"`
		Status   resource.TemplateVersionStatus `json:"status"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &pub); got.Code != http.StatusCreated || err != nil {
		t.Fatalf("publish 2.0: %d %s", got.Code, got.Body)
	}
	if pub.Spec.Template != tid || pub.Spec.Version != "2.0" || len(pub.Metadata.OwnerRefs) != 1 || pub.Metadata.OwnerRefs[0].ID != tid || !statusReady(pub.Status.Status) {
		t.Errorf("published: %+v", pub)
	}
	// The label is taken, and a version never changes.
	w.expect("key-ci", http.MethodPost, versions, versionDoc("2.0"), http.StatusConflict)
	// No route: the API answers 404 to a method it doesn't serve.
	w.expect("key-ci", http.MethodPut, versions+"/2.0", versionDoc("2.0"), http.StatusNotFound)
	// The newest is the latest, whatever its label (RP-104).
	w.mkAs("key-ci", versions, versionDoc("1.1"))
	vs := w.listed("key-ci", versions)
	if len(vs) != 2 || vs[0].Metadata.Name != "2.0" || vs[0].Latest || vs[1].Metadata.Name != "1.1" || !vs[1].Latest {
		t.Errorf("versions: %+v", vs)
	}

	// A body that can't render is refused at publish.
	bad := versionDoc("3.0")
	bad["spec"].(map[string]any)["body"] = map[string]any{"components": []any{map[string]any{"name": "web", "image": map[string]any{"$param": "nope"}}}}
	w.expect("key-ci", http.MethodPost, versions, bad, http.StatusBadRequest)
	w.expect("key-ci", http.MethodPost, versions, versionDoc("not a label"), http.StatusBadRequest)
	w.expect("key-ci", http.MethodPost, "/v1/templates/01a1dda3-0000-7000-8000-00000000dead/versions", versionDoc("1.0"), http.StatusNotFound)

	// Deleting a version records its own Approval.
	del := w.do("key-ci", http.MethodDelete, versions+"/2.0", nil)
	if del.Code != http.StatusNoContent || del.Header().Get("Serendip-Approval-Id") == "" {
		t.Fatalf("delete 2.0: %d %s", del.Code, del.Body)
	}
	ap := w.do("key-ci", http.MethodGet, "/v1/approvals/"+del.Header().Get("Serendip-Approval-Id"), nil)
	var a resource.Approval
	if err := json.Unmarshal(ap.Body.Bytes(), &a); err != nil || a.Spec.Action.Target != (resource.Scope{Kind: resource.KindTemplateVersion, ID: pub.Metadata.ID}) {
		t.Errorf("approval: %d %s", ap.Code, ap.Body)
	}
	w.expect("key-ci", http.MethodGet, versions+"/2.0", nil, http.StatusNotFound)
	w.expect("key-ci", http.MethodDelete, versions+"/2.0", nil, http.StatusNotFound)
	if vs := w.listed("key-ci", versions); len(vs) != 1 || !vs[0].Latest {
		t.Errorf("after the delete: %+v", vs)
	}

	// An approval request may name a version (an agent's delete asks for
	// one).
	vid := w.mkAs("key-ci", versions, versionDoc("4.0"))
	w.expect("key-ci", http.MethodPost, "/v1/approvals", map[string]any{
		"action": map[string]any{"verb": "delete", "target": map[string]any{"kind": "TemplateVersion", "id": vid}}, "reason": "retire 4.0",
	}, http.StatusCreated)
}

func TestTemplates_DeleteCascadesUnderOneApproval(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) { w.grant("tok-contrib", policy.ContributorRoleID, "projA") })
	tid := w.mkAs("tok-contrib", "/v1/templates", templateDoc("t", w.id["projA"]))
	versions := "/v1/templates/" + tid + "/versions"
	v1 := w.mkAs("tok-contrib", versions, versionDoc("1.0"))
	v2 := w.mkAs("tok-contrib", versions, versionDoc("2.0"))

	w.expect("tok-contrib", http.MethodDelete, "/v1/templates/"+tid, nil, http.StatusConflict)
	del := w.do("tok-contrib", http.MethodDelete, "/v1/templates/"+tid+"?cascade=true", nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("cascade delete: %d %s", del.Code, del.Body)
	}
	ap := w.do("key-ci", http.MethodGet, "/v1/approvals/"+del.Header().Get("Serendip-Approval-Id"), nil)
	var a resource.Approval
	if err := json.Unmarshal(ap.Body.Bytes(), &a); err != nil || !a.Spec.Action.Cascade {
		t.Fatalf("approval: %d %s", ap.Code, ap.Body)
	}
	var radius []string
	for _, ref := range a.Status.BlastRadius {
		radius = append(radius, ref.ID)
	}
	for _, v := range []string{v1, v2} {
		if !slices.Contains(radius, v) {
			t.Errorf("blast radius %v lacks version %s", radius, v)
		}
	}
	if n := len(w.listed("key-ci", "/v1/approvals")); n != 1 {
		t.Errorf("%d approvals, want 1", n)
	}
	w.expect("key-ci", http.MethodGet, versions, nil, http.StatusNotFound)
}
