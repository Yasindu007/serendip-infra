package thunderid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// fakeDirectory serves the management endpoints Directory reads, with the
// shapes observed on 1.0.1 (specs/018-agent-manager-poc plan.md T001
// finding 4), behind fakeThunder's admin login.
type fakeDirectory struct {
	login *fakeThunder

	mu      sync.Mutex
	users   []map[string]any
	groups  []map[string]any
	member  map[string][]string // user ID -> group names
	logins  int
	reject  int // answer this many management calls 401 first
	pages   []string
	revoked bool // every token is refused
	// expiresIn, when set, is added to the token response.
	expiresIn int
	// noTotal leaves totalResults out of list responses.
	noTotal bool
}

func (f *fakeDirectory) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/oauth2/token":
		f.mu.Lock()
		f.logins++
		expiresIn := f.expiresIn
		f.mu.Unlock()
		rec := httptest.NewRecorder()
		f.login.serve(rec, r)
		var tok map[string]any
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &tok) != nil {
			w.WriteHeader(rec.Code)
			w.Write(rec.Body.Bytes())
			return
		}
		if expiresIn != 0 {
			tok["expires_in"] = expiresIn
		}
		json.NewEncoder(w).Encode(tok)
		return
	case strings.HasPrefix(r.URL.Path, "/oauth2/") || r.URL.Path == "/flow/execute":
		f.login.serve(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer mgmt-token" || f.revoked || f.reject > 0 {
		if f.reject > 0 {
			f.reject--
		}
		http.Error(w, `{"code":"AUTH-4010"}`, http.StatusUnauthorized)
		return
	}
	f.pages = append(f.pages, r.URL.RequestURI())
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	page := func(all []map[string]any) []map[string]any {
		if offset >= len(all) {
			return []map[string]any{}
		}
		return all[offset:min(offset+limit, len(all))]
	}
	list := func(key string, all []map[string]any) map[string]any {
		doc := map[string]any{"startIndex": offset + 1, key: page(all)}
		if !f.noTotal {
			doc["totalResults"] = len(all)
		}
		return doc
	}
	switch {
	case r.URL.Path == "/users":
		json.NewEncoder(w).Encode(list("users", f.users))
	case r.URL.Path == "/groups":
		json.NewEncoder(w).Encode(list("groups", f.groups))
	case strings.HasPrefix(r.URL.Path, "/users/") && strings.HasSuffix(r.URL.Path, "/groups"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/users/"), "/groups")
		names, ok := f.member[id]
		if !ok {
			http.Error(w, `{"code":"USR-1003"}`, http.StatusNotFound)
			return
		}
		var gs []map[string]any
		for _, n := range names {
			gs = append(gs, map[string]any{"id": "g-" + n, "name": n, "ouId": defaultOUID})
		}
		json.NewEncoder(w).Encode(map[string]any{"totalResults": len(gs), "groups": page(gs)})
	default:
		http.NotFound(w, r)
	}
}

func user(id, username, name, email string) map[string]any {
	return map[string]any{"id": id, "ouId": defaultOUID, "type": "Person", "attributes": map[string]any{
		"username": username, "name": name, "email": email, "sub": username, // attributes.sub is the username, not the subject
	}}
}

// newTestDirectory returns a Directory whose tunnel is a plain connection
// to a fake ThunderID, and whose admin password comes from a fake
// Kubernetes Secret.
func newTestDirectory(t *testing.T, f *fakeDirectory, withSecret bool) *Directory {
	t.Helper()
	login, _ := newFakeThunder(t, nil, nil)
	f.login = login
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	kube, client := newFake(t)
	if withSecret {
		data, _ := json.Marshal(map[string][]byte{AdminSecretKey: []byte(login.password)})
		kube.objects[adminSecretPath] = kubernetes.Object{APIVersion: "v1", Kind: "Secret", Data: data}
	}
	d := NewDirectory(client, "http://identity.localhost:30080")
	addr := srv.Listener.Addr().String()
	d.hc = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}}}
	return d
}

func TestDirectory_People(t *testing.T) {
	f := &fakeDirectory{users: []map[string]any{
		user("u1", "admin", "Administrator", "admin@example.com"),
		user("u2", "t018-reader", "Reader Person", "reader@example.com"),
		user("u3", "dev", "Developer", "DEV@Corp.example"),
	}}
	d := newTestDirectory(t, f, true)
	tests := []struct {
		query string
		want  []identity.DirectoryEntry
	}{
		{"", []identity.DirectoryEntry{{ID: "u1", Name: "admin"}, {ID: "u2", Name: "t018-reader"}, {ID: "u3", Name: "dev"}}},
		{"READ", []identity.DirectoryEntry{{ID: "u2", Name: "t018-reader"}}},
		{"corp.example", []identity.DirectoryEntry{{ID: "u3", Name: "dev"}}},
		{"administrator", []identity.DirectoryEntry{{ID: "u1", Name: "admin"}}},
		{"nobody", nil},
	}
	for _, tt := range tests {
		res, err := d.People(t.Context(), tt.query)
		if err != nil {
			t.Fatalf("People(%q) = %v", tt.query, err)
		}
		if !reflect.DeepEqual(res.Items, tt.want) || res.Truncated {
			t.Errorf("People(%q) = %+v, want %+v, not truncated", tt.query, res, tt.want)
		}
	}
	if f.logins != 1 {
		t.Errorf("admin logins = %d, want 1 (the token is cached)", f.logins)
	}
}

func TestDirectory_Truncation(t *testing.T) {
	t.Run("more matches than returned", func(t *testing.T) {
		f := &fakeDirectory{}
		for i := range 60 {
			f.users = append(f.users, user(fmt.Sprint("u", i), fmt.Sprint("person", i), "", ""))
		}
		res, err := newTestDirectory(t, f, true).People(t.Context(), "person")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Items) != maxResults || !res.Truncated {
			t.Errorf("got %d items, truncated %v; want %d, true", len(res.Items), res.Truncated, maxResults)
		}
	})
	t.Run("more entries than scanned", func(t *testing.T) {
		f := &fakeDirectory{}
		for i := range maxScanned + 150 {
			f.groups = append(f.groups, map[string]any{"id": fmt.Sprint("g", i), "name": fmt.Sprint("team-", i)})
		}
		res, err := newTestDirectory(t, f, true).Groups(t.Context(), "no-such-team")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Items) != 0 || !res.Truncated {
			t.Errorf("got %+v; want no items, truncated", res)
		}
		if len(f.pages) != maxScanned/pageSize {
			t.Errorf("read %d pages, want %d", len(f.pages), maxScanned/pageSize)
		}
	})
	t.Run("exactly the last page", func(t *testing.T) {
		f := &fakeDirectory{}
		for i := range pageSize {
			f.groups = append(f.groups, map[string]any{"id": fmt.Sprint("g", i), "name": fmt.Sprint("team-", i)})
		}
		res, err := newTestDirectory(t, f, true).Groups(t.Context(), "team-99")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(res.Items, []identity.DirectoryEntry{{ID: "team-99", Name: "team-99"}}) || res.Truncated {
			t.Errorf("got %+v", res)
		}
		if len(f.pages) != 1 {
			t.Errorf("read %d pages, want 1", len(f.pages))
		}
	})
}

func TestDirectory_GroupsUseNamesAsIDs(t *testing.T) {
	f := &fakeDirectory{groups: []map[string]any{
		{"id": "01900000-0000-7000-8000-000000000040", "name": "Administrators"},
		{"id": "01a1-x", "name": "t018-readers"},
	}}
	res, err := newTestDirectory(t, f, true).Groups(t.Context(), "readers")
	if err != nil {
		t.Fatal(err)
	}
	if want := []identity.DirectoryEntry{{ID: "t018-readers", Name: "t018-readers"}}; !reflect.DeepEqual(res.Items, want) {
		t.Errorf("Groups = %+v, want %+v", res.Items, want)
	}
}

func TestDirectory_GroupsOf(t *testing.T) {
	var many []string
	for i := range 130 {
		many = append(many, fmt.Sprint("g", i))
	}
	f := &fakeDirectory{member: map[string][]string{
		"01900000-0000-7000-8000-000000000030": {"Administrators", "t018-readers"},
		"loner":                                {},
		"busy":                                 many,
	}}
	d := newTestDirectory(t, f, true)
	got, err := d.GroupsOf(t.Context(), "01900000-0000-7000-8000-000000000030")
	if err != nil || !reflect.DeepEqual(got, []string{"Administrators", "t018-readers"}) {
		t.Errorf("GroupsOf(admin) = %v, %v", got, err)
	}
	if got, err := d.GroupsOf(t.Context(), "loner"); err != nil || len(got) != 0 {
		t.Errorf("GroupsOf(loner) = %v, %v; want none", got, err)
	}
	if got, err := d.GroupsOf(t.Context(), "busy"); err != nil || len(got) != 130 {
		t.Errorf("GroupsOf(busy) = %d groups, %v; want 130 across pages", len(got), err)
	}
	before := len(f.pages)
	// A subject is one path segment: crafted ones never reach the provider.
	for _, s := range []string{"", ".", "..", "../groups", "a/b"} {
		if _, err := d.GroupsOf(t.Context(), s); !errors.Is(err, identity.ErrUnknownSubject) {
			t.Errorf("GroupsOf(%q) = %v, want ErrUnknownSubject", s, err)
		}
	}
	if len(f.pages) != before {
		t.Errorf("crafted subjects reached the provider: %v", f.pages[before:])
	}
	if _, err := d.GroupsOf(t.Context(), "no-such-user"); !errors.Is(err, identity.ErrUnknownSubject) {
		t.Errorf("GroupsOf(no-such-user) = %v, want ErrUnknownSubject", err)
	}
}

func TestDirectory_GroupsOfPastTheCapIsAnError(t *testing.T) {
	var many []string
	for i := range maxScanned + 1 {
		many = append(many, fmt.Sprint("g", i))
	}
	d := newTestDirectory(t, &fakeDirectory{member: map[string][]string{"busy": many}}, true)
	if got, err := d.GroupsOf(t.Context(), "busy"); !errors.Is(err, reconcile.ErrProviderNotReady) {
		t.Errorf("GroupsOf = %d groups, %v; want ErrProviderNotReady, not a partial list", len(got), err)
	}
}

func TestDirectory_PagesWithoutTotalResults(t *testing.T) {
	f := &fakeDirectory{noTotal: true}
	for i := range 2*pageSize + 5 {
		f.groups = append(f.groups, map[string]any{"id": fmt.Sprint("g", i), "name": fmt.Sprint("team-", i)})
	}
	res, err := newTestDirectory(t, f, true).Groups(t.Context(), "team-204")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Truncated {
		t.Errorf("got %+v; want team-204 found on the third page", res)
	}
	if len(f.pages) != 3 {
		t.Errorf("read %d pages, want 3 (stop at the short page)", len(f.pages))
	}
}

func TestDirectory_NotFoundListIsNotReady(t *testing.T) {
	f := &fakeDirectory{}
	d := newTestDirectory(t, f, true)
	if _, err := d.People(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	// A provider that no longer serves /users (another version, say) is
	// "not ready", never a bare status the caller can't use.
	err := d.get(t.Context(), "/no-such-endpoint", &struct{}{}, false)
	if !errors.Is(err, reconcile.ErrProviderNotReady) {
		t.Errorf("err = %v, want ErrProviderNotReady", err)
	}
}

func TestDirectory_ShortLivedTokenIsStillReused(t *testing.T) {
	f := &fakeDirectory{expiresIn: 30, groups: []map[string]any{{"id": "g", "name": "team"}}}
	d := newTestDirectory(t, f, true)
	for range 3 {
		if _, err := d.Groups(t.Context(), ""); err != nil {
			t.Fatal(err)
		}
	}
	if f.logins != 1 {
		t.Errorf("logins = %d, want 1 (a 30 s token is kept for 15 s)", f.logins)
	}
}

func TestDirectory_CancelledIsReportedAsCancelled(t *testing.T) {
	d := newTestDirectory(t, &fakeDirectory{}, true)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := d.People(ctx, "")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
}

func TestDirectory_TokenRefusedOnceLogsInAgain(t *testing.T) {
	f := &fakeDirectory{groups: []map[string]any{{"id": "g", "name": "team"}}}
	d := newTestDirectory(t, f, true)
	if _, err := d.Groups(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.reject = 1 // e.g. the provider restarted and forgot the session
	f.mu.Unlock()
	if _, err := d.Groups(t.Context(), ""); err != nil {
		t.Fatalf("after one 401: %v, want a fresh login and success", err)
	}
	if f.logins != 2 {
		t.Errorf("logins = %d, want 2", f.logins)
	}
}

func TestDirectory_NotReady(t *testing.T) {
	t.Run("token always refused", func(t *testing.T) {
		f := &fakeDirectory{revoked: true}
		_, err := newTestDirectory(t, f, true).People(t.Context(), "")
		if !errors.Is(err, reconcile.ErrProviderNotReady) {
			t.Errorf("err = %v, want ErrProviderNotReady", err)
		}
		if f.logins != 2 {
			t.Errorf("logins = %d, want 2 (one retry, then give up)", f.logins)
		}
	})
	t.Run("no admin secret", func(t *testing.T) {
		_, err := newTestDirectory(t, &fakeDirectory{}, false).People(t.Context(), "")
		if !errors.Is(err, reconcile.ErrProviderNotReady) {
			t.Errorf("err = %v, want ErrProviderNotReady", err)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		d := newTestDirectory(t, &fakeDirectory{}, true)
		d.hc = &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("kubernetes: service serendip-identity/thunderid-service: no ready pod")
		}}}
		_, err := d.GroupsOf(t.Context(), "u1")
		if !errors.Is(err, reconcile.ErrProviderNotReady) {
			t.Errorf("err = %v, want ErrProviderNotReady", err)
		}
	})
}

func TestDirectory_ErrorsNeverCarryThePassword(t *testing.T) {
	f := &fakeDirectory{revoked: true}
	d := newTestDirectory(t, f, true)
	_, err := d.People(t.Context(), "")
	if err == nil || strings.Contains(err.Error(), f.login.password) || strings.Contains(err.Error(), "mgmt-token") {
		t.Errorf("err = %v; it must exist and carry neither password nor token", err)
	}
}
