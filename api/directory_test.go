package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
)

// fakeDirectory is an identity.Directory; err, when set, is what every
// call returns, as an unreachable provider would.
type fakeDirectory struct {
	people, groups identity.DirectoryResult
	member         map[string][]string
	err            error
	queries        []string
}

func (f *fakeDirectory) People(_ context.Context, q string) (identity.DirectoryResult, error) {
	f.queries = append(f.queries, "people:"+q)
	return f.people, f.err
}

func (f *fakeDirectory) Groups(_ context.Context, q string) (identity.DirectoryResult, error) {
	f.queries = append(f.queries, "groups:"+q)
	return f.groups, f.err
}

func (f *fakeDirectory) GroupsOf(_ context.Context, subject string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	g, ok := f.member[subject]
	if !ok {
		return nil, identity.ErrUnknownSubject
	}
	return g, nil
}

// leaky is the kind of text a failed port-forward carries; it must never
// reach a client.
var leaky = fmt.Errorf("%w: port-forward serendip-identity/thunderid-0: netns /var/run/netns/cni-1234", reconcile.ErrProviderNotReady)

type dirTestView struct {
	Source    string `json:"source"`
	Truncated bool   `json:"truncated"`
	Items     []struct {
		Type        string `json:"type"`
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	} `json:"items"`
}

type groupsTestView struct {
	Source string   `json:"source"`
	Groups []string `json:"groups"`
}

func decode[T any](t *testing.T, w *authzWorld, token, path string, want int) T {
	t.Helper()
	got := w.do(token, http.MethodGet, path, nil)
	var v T
	if got.Code != want {
		t.Fatalf("GET %s as %s: %d, want %d (%s)", path, people[token].Name, got.Code, want, got.Body)
	}
	if strings.Contains(got.Body.String(), "netns") {
		t.Errorf("GET %s leaked provider internals: %s", path, got.Body)
	}
	if want == http.StatusOK {
		if err := json.Unmarshal(got.Body.Bytes(), &v); err != nil {
			t.Fatalf("GET %s: %v (%s)", path, err, got.Body)
		}
	}
	return v
}

func grantsForDirectory(w *authzWorld) {
	w.grant("tok-reader", policy.ReaderRoleID, "projA")
	w.grant("tok-admin", policy.RBACAdministratorRoleID, "projA")
}

func TestDirectory_LiveSearch(t *testing.T) {
	dir := &fakeDirectory{
		people: identity.DirectoryResult{Items: []identity.DirectoryEntry{{ID: "sub-new", Name: "never-signed-in"}}, Truncated: true},
		groups: identity.DirectoryResult{Items: []identity.DirectoryEntry{{ID: "t018-readers", Name: "t018-readers"}}},
	}
	w := newAuthzWorld(t, grantsForDirectory, api.WithDirectory(dir))

	v := decode[dirTestView](t, w, "tok-admin", authzPath+"directory?type=person&q=never", http.StatusOK)
	if v.Source != "live" || !v.Truncated || len(v.Items) != 1 || v.Items[0].ID != "sub-new" ||
		v.Items[0].Type != "person" || v.Items[0].DisplayName != "never-signed-in" {
		t.Errorf("person search = %+v", v)
	}
	g := decode[dirTestView](t, w, "tok-admin", authzPath+"directory?type=group&q=readers", http.StatusOK)
	if g.Source != "live" || g.Truncated || len(g.Items) != 1 || g.Items[0].Type != "group" || g.Items[0].ID != "t018-readers" {
		t.Errorf("group search = %+v", g)
	}
	if want := []string{"people:never", "groups:readers"}; !reflect.DeepEqual(dir.queries, want) {
		t.Errorf("queries = %v, want %v", dir.queries, want)
	}
}

func TestDirectory_Gate(t *testing.T) {
	w := newAuthzWorld(t, grantsForDirectory, api.WithDirectory(&fakeDirectory{member: map[string][]string{"sub-member": nil}}))
	for _, path := range []string{"directory?type=person", "principals/person/sub-member/groups"} {
		w.expect("tok-reader", http.MethodGet, authzPath+path, nil, http.StatusForbidden)
		w.expect("tok-nobody", http.MethodGet, authzPath+path, nil, http.StatusForbidden)
		w.expect("tok-admin", http.MethodGet, authzPath+path, nil, http.StatusOK)
	}
	w.expect("tok-admin", http.MethodGet, authzPath+"directory", nil, http.StatusBadRequest)
	w.expect("tok-admin", http.MethodGet, authzPath+"directory?type=apiKey", nil, http.StatusBadRequest)
	w.expect("tok-admin", http.MethodGet, authzPath+"directory?type=person&q="+strings.Repeat("x", 101), nil, http.StatusBadRequest)
}

func TestDirectory_FallsBackToSignedIn(t *testing.T) {
	for name, opts := range map[string][]api.ServerOption{
		"directory unreachable": {api.WithDirectory(&fakeDirectory{err: leaky})},
		"no directory":          nil,
	} {
		t.Run(name, func(t *testing.T) {
			w := newAuthzWorld(t, grantsForDirectory, opts...)
			// Sign-ins put people (and a person's groups) in the registry.
			w.do("tok-member", http.MethodGet, "/v1/session", nil)
			w.do("tok-reader", http.MethodGet, "/v1/session", nil)

			v := decode[dirTestView](t, w, "tok-admin", authzPath+"directory?type=person&q=MEM", http.StatusOK)
			if v.Source != "signedIn" || len(v.Items) != 1 || v.Items[0].ID != "sub-member" || v.Items[0].DisplayName != "member" {
				t.Errorf("person search = %+v", v)
			}
			g := decode[dirTestView](t, w, "tok-admin", authzPath+"directory?type=group", http.StatusOK)
			if g.Source != "signedIn" || len(g.Items) != 1 || g.Items[0].ID != "platform" {
				t.Errorf("group search = %+v", g)
			}
			gv := decode[groupsTestView](t, w, "tok-admin", authzPath+"principals/person/sub-member/groups", http.StatusOK)
			if gv.Source != "signedIn" || !reflect.DeepEqual(gv.Groups, []string{"platform"}) {
				t.Errorf("groups = %+v", gv)
			}
			decode[groupsTestView](t, w, "tok-admin", authzPath+"principals/person/sub-unseen/groups", http.StatusNotFound)
		})
	}
}

func TestDirectory_LiveGroups(t *testing.T) {
	dir := &fakeDirectory{member: map[string][]string{
		"sub-member": {"t018-readers", "platform"},
		"sub-alone":  nil,
	}}
	w := newAuthzWorld(t, grantsForDirectory, api.WithDirectory(dir))
	// The person last signed in with only "platform"; the live answer
	// differs, which is the point.
	w.do("tok-member", http.MethodGet, "/v1/session", nil)
	gv := decode[groupsTestView](t, w, "tok-admin", authzPath+"principals/person/sub-member/groups", http.StatusOK)
	if gv.Source != "live" || !reflect.DeepEqual(gv.Groups, []string{"platform", "t018-readers"}) {
		t.Errorf("groups = %+v, want live and sorted", gv)
	}
	gv = decode[groupsTestView](t, w, "tok-admin", authzPath+"principals/person/sub-alone/groups", http.StatusOK)
	if gv.Groups == nil || len(gv.Groups) != 0 {
		t.Errorf("no groups = %+v, want an empty list", gv)
	}
	// Unknown to the provider is 404, even if the registry remembers them.
	decode[groupsTestView](t, w, "tok-admin", authzPath+"principals/person/sub-gone/groups", http.StatusNotFound)
}

func TestDirectory_ErrorsAreNotLeaked(t *testing.T) {
	w := newAuthzWorld(t, grantsForDirectory, api.WithDirectory(&fakeDirectory{err: errors.Join(leaky, errors.New("token mgmt-secret"))}))
	for _, path := range []string{"directory?type=person", "directory?type=group", "principals/person/sub-x/groups"} {
		got := w.do("tok-admin", http.MethodGet, authzPath+path, nil)
		if strings.Contains(got.Body.String(), "netns") || strings.Contains(got.Body.String(), "mgmt-secret") {
			t.Errorf("%s leaked: %s", path, got.Body)
		}
	}
}

func TestDirectory_Bounds(t *testing.T) {
	var many []identity.DirectoryEntry
	for i := range 70 {
		many = append(many, identity.DirectoryEntry{ID: fmt.Sprint("sub-", i), Name: fmt.Sprint("p", i)})
	}
	w := newAuthzWorld(t, grantsForDirectory, api.WithDirectory(&fakeDirectory{people: identity.DirectoryResult{Items: many}}))
	v := decode[dirTestView](t, w, "tok-admin", authzPath+"directory?type=person", http.StatusOK)
	if len(v.Items) != 50 || !v.Truncated {
		t.Errorf("live answer: %d items, truncated %v; want 50, true (the handler bounds it too)", len(v.Items), v.Truncated)
	}
}

func TestDirectory_FallbackBoundsAndMatchesIDs(t *testing.T) {
	w := newAuthzWorld(t, grantsForDirectory)
	for token := range people {
		w.do(token, http.MethodGet, "/v1/session", nil)
	}
	// Matching is on the ID too, not only the display name.
	v := decode[dirTestView](t, w, "tok-admin", authzPath+"directory?type=person&q=sub-contr", http.StatusOK)
	if len(v.Items) != 1 || v.Items[0].ID != "sub-contrib" {
		t.Errorf("match by ID = %+v", v)
	}
	// More than 50 known people: the fallback answer is bounded.
	for i := range 55 {
		tok := fmt.Sprint("tok-extra-", i)
		people[tok] = identity.Principal{Subject: fmt.Sprint("sub-extra-", i), Name: fmt.Sprint("extra", i)}
		t.Cleanup(func() { delete(people, tok) })
		w.do(tok, http.MethodGet, "/v1/session", nil)
	}
	v = decode[dirTestView](t, w, "tok-admin", authzPath+"directory?type=person", http.StatusOK)
	if v.Source != "signedIn" || len(v.Items) != 50 || !v.Truncated {
		t.Errorf("fallback: %s, %d items, truncated %v; want signedIn, 50, true", v.Source, len(v.Items), v.Truncated)
	}
}
