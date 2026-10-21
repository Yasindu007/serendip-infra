package thunderid

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeThunder simulates the endpoints UpdateClient uses, with the
// shapes observed on 1.0.1 (spec 016 T001).
type fakeThunder struct {
	t        *testing.T
	password string

	mu   sync.Mutex
	app  map[string]any
	puts int
	put  map[string]any
	// noDelegate leaves the serendip-mcp client out of the application
	// list; created collects what was POSTed.
	noDelegate bool
	created    []map[string]any
	// listStatus and createStatus, when set, fail those calls.
	listStatus, createStatus int
	touched                  []string // other applications' paths requested
}

func newFakeThunder(t *testing.T, redirects, postLogout []any) (*fakeThunder, *httptest.Server) {
	f := &fakeThunder{t: t, password: "pw-secret", app: map[string]any{
		"id": clientAppID, "name": "Serendip", "type": "browser", "ouId": defaultOUID, "loginConsent": map[string]any{"validityPeriod": 0},
		"inboundAuthConfig": []any{map[string]any{"type": "oauth2", "config": map[string]any{
			"clientId": ClientID, "redirectUris": redirects, "postLogoutRedirectUris": postLogout,
			"grantTypes": []any{"authorization_code", "refresh_token"}, "pkceRequired": true, "publicClient": true,
			"token": map[string]any{"accessToken": map[string]any{"userConfig": map[string]any{
				"validityPeriod": 3600, "attributes": []any{"username", "email", "name", "groups"},
			}}},
		}}},
	}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeThunder) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/oauth2/authorize" && r.URL.Query().Get("client_id") == ClientID:
		if f.hasURI("redirectUris", r.URL.Query().Get("redirect_uri")) {
			http.Redirect(w, r, "http://identity.localhost:30080/gate/signin?executionId=p&authId=p", http.StatusFound)
		} else {
			http.Redirect(w, r, "http://identity.localhost:30080/gate/error?errorCode=invalid_request", http.StatusFound)
		}
	case r.URL.Path == "/oauth2/logout":
		if f.hasURI("postLogoutRedirectUris", r.URL.Query().Get("post_logout_redirect_uri")) {
			http.Redirect(w, r, "http://identity.localhost:30080/gate/signout?logoutId=p", http.StatusFound)
		} else {
			http.Error(w, "invalid post_logout_redirect_uri", http.StatusBadRequest)
		}
	case r.URL.Path == "/oauth2/authorize":
		q := r.URL.Query()
		if q.Get("client_id") != consoleClientID || q.Get("resource") != systemResource || q.Get("scope") != systemScope {
			http.Error(w, "invalid_target", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "http://identity.localhost:30080/gate/signin?executionId=ex1&authId=au1", http.StatusFound)
	case r.URL.Path == "/flow/execute":
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["challengeToken"]; !ok {
			json.NewEncoder(w).Encode(map[string]any{"flowStatus": "INCOMPLETE", "challengeToken": "ct1"})
			return
		}
		inputs, _ := body["inputs"].(map[string]any)
		if inputs["username"] != AdminUsername || inputs["password"] != f.password {
			json.NewEncoder(w).Encode(map[string]any{"flowStatus": "ERROR"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"flowStatus": "COMPLETE", "assertion": "as1"})
	case r.URL.Path == "/oauth2/auth/callback":
		json.NewEncoder(w).Encode(map[string]string{"redirect_uri": "http://identity.localhost:30080/console?code=code1&state=serendip"})
	case r.URL.Path == "/oauth2/token":
		r.ParseForm()
		if r.PostForm.Get("code") != "code1" || r.PostForm.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "mgmt-token", "token_type": "Bearer"})
	case r.URL.Path == "/applications":
		if r.Header.Get("Authorization") != "Bearer mgmt-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet && f.listStatus != 0 {
			http.Error(w, "list failed", f.listStatus)
			return
		}
		if r.Method == http.MethodPost && f.createStatus != 0 {
			http.Error(w, "create failed", f.createStatus)
			return
		}
		if r.Method == http.MethodPost {
			var doc map[string]any
			json.NewDecoder(r.Body).Decode(&doc)
			f.created = append(f.created, doc)
			f.noDelegate = false
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(doc)
			return
		}
		apps := []any{map[string]any{"id": clientAppID, "clientId": ClientID}}
		if !f.noDelegate {
			apps = append(apps, map[string]any{"id": "other-id", "clientId": DelegateClientID})
		}
		json.NewEncoder(w).Encode(map[string]any{"totalResults": len(apps), "applications": apps})
	case r.URL.Path == "/applications/"+clientAppID:
		if r.Header.Get("Authorization") != "Bearer mgmt-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPut {
			var doc map[string]any
			json.NewDecoder(r.Body).Decode(&doc)
			f.puts++
			f.put, f.app = doc, doc
		}
		json.NewEncoder(w).Encode(f.app)
	default:
		if strings.HasPrefix(r.URL.Path, "/applications/") {
			f.touched = append(f.touched, r.Method+" "+r.URL.Path)
		}
		http.NotFound(w, r)
	}
}

// hasURI is called with f.mu held.
func (f *fakeThunder) hasURI(key, uri string) bool {
	cfg := f.app["inboundAuthConfig"].([]any)[0].(map[string]any)["config"].(map[string]any)
	list, _ := cfg[key].([]any)
	return slices.Contains(list, any(uri))
}

func (f *fakeThunder) config() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.app["inboundAuthConfig"].([]any)[0].(map[string]any)["config"].(map[string]any)
}

const (
	testCallback   = "http://localhost:8080/auth/callback"
	testPostLogout = "http://localhost:8080/"
)

func run(t *testing.T, srv *httptest.Server, password string) error {
	t.Helper()
	return ensureClient(context.Background(), srv.URL, noRedirects(srv.Client()), "http://identity.localhost:30080", password, testCallback, testPostLogout)
}

// userConfig is the client's access-token attribute settings.
func userConfig(cfg map[string]any) map[string]any {
	return cfg["token"].(map[string]any)["accessToken"].(map[string]any)["userConfig"].(map[string]any)
}

func TestEnsureClient_AddsGroupsToAnOlderClient(t *testing.T) {
	f, srv := newFakeThunder(t, []any{testCallback}, []any{testPostLogout})
	userConfig(f.config())["attributes"] = []any{"username", "email", "name", "nickname"}
	if err := run(t, srv, "pw-secret"); err != nil {
		t.Fatal(err)
	}
	if f.puts != 1 {
		t.Fatalf("%d PUTs, want 1", f.puts)
	}
	uc := userConfig(f.config())
	if got, want := uc["attributes"].([]any), []any{"username", "email", "name", "nickname", "groups"}; !slices.Equal(got, want) {
		t.Errorf("attributes = %v, want %v (added, nothing removed)", got, want)
	}
	if uc["validityPeriod"] == nil {
		t.Error("PUT lost the token validity period")
	}
	// A second pass is a no-op.
	if err := run(t, srv, "pw-secret"); err != nil || f.puts != 1 {
		t.Errorf("second pass: err = %v, puts = %d; want no write", err, f.puts)
	}
}

func TestEnsureClient_WithoutConsoleURIsOnlyAddsAttributes(t *testing.T) {
	f, srv := newFakeThunder(t, []any{LoopbackRedirectURI}, []any{LoopbackRedirectURI})
	userConfig(f.config())["attributes"] = []any{"username"}
	if err := ensureClient(context.Background(), srv.URL, noRedirects(srv.Client()), "http://identity.localhost:30080", "pw-secret", "", ""); err != nil {
		t.Fatal(err)
	}
	cfg := f.config()
	if got := cfg["redirectUris"].([]any); !slices.Equal(got, []any{LoopbackRedirectURI}) {
		t.Errorf("redirectUris changed: %v", got)
	}
	if got := userConfig(cfg)["attributes"].([]any); !slices.Contains(got, any("groups")) {
		t.Errorf("attributes = %v, want groups added", got)
	}
}

func TestEnsureClient_RefusesAClientWithoutTokenConfig(t *testing.T) {
	f, srv := newFakeThunder(t, []any{testCallback}, []any{testPostLogout})
	delete(f.config(), "token")
	if err := run(t, srv, "pw-secret"); err == nil || f.puts != 0 {
		t.Errorf("err = %v, puts = %d; want an error and no write", err, f.puts)
	}
}

func TestEnsureClient_AddsMissingURIsInOnePut(t *testing.T) {
	f, srv := newFakeThunder(t, []any{LoopbackRedirectURI, "http://someone-else/cb"}, []any{LoopbackRedirectURI})
	if err := run(t, srv, "pw-secret"); err != nil {
		t.Fatal(err)
	}
	if f.puts != 1 {
		t.Fatalf("%d PUTs, want 1", f.puts)
	}
	cfg := f.config()
	got := cfg["redirectUris"].([]any)
	for _, want := range []any{LoopbackRedirectURI, "http://someone-else/cb", testCallback} {
		if !slices.Contains(got, want) {
			t.Errorf("redirectUris %v lacks %v", got, want)
		}
	}
	if !slices.Contains(cfg["postLogoutRedirectUris"].([]any), any(testPostLogout)) {
		t.Errorf("postLogoutRedirectUris = %v", cfg["postLogoutRedirectUris"])
	}
	// The whole document went back: nothing else lost or changed.
	if cfg["clientId"] != ClientID || cfg["pkceRequired"] != true || f.put["name"] != "Serendip" || f.put["loginConsent"] == nil {
		t.Errorf("PUT document lost fields: %v", f.put)
	}
}

func TestEnsureClient_NoDriftMakesNoWrites(t *testing.T) {
	f, srv := newFakeThunder(t, []any{LoopbackRedirectURI, testCallback}, []any{LoopbackRedirectURI, testPostLogout})
	if err := run(t, srv, "pw-secret"); err != nil {
		t.Fatal(err)
	}
	if err := run(t, srv, "pw-secret"); err != nil {
		t.Fatal(err)
	}
	if f.puts != 0 {
		t.Errorf("%d PUTs on a client already up to date, want 0", f.puts)
	}
}

func TestEnsureClient_ErrorsNeverLeakSecrets(t *testing.T) {
	_, srv := newFakeThunder(t, nil, nil)
	err := run(t, srv, "wrong-password-xyz")
	if err == nil {
		t.Fatal("wrong password: no error")
	}
	for _, secret := range []string{"wrong-password-xyz", "pw-secret", "mgmt-token", "code1", "as1"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q leaks %q", err, secret)
		}
	}
}

func TestEnsureClient_RefusesAnotherClient(t *testing.T) {
	f, srv := newFakeThunder(t, nil, nil)
	f.config()["clientId"] = "not-serendip"
	if err := run(t, srv, "pw-secret"); err == nil || f.puts != 0 {
		t.Errorf("err = %v, puts = %d; want an error and no write", err, f.puts)
	}
}

func TestNoRedirects(t *testing.T) {
	srv := httptest.NewServer(http.RedirectHandler("/elsewhere", http.StatusFound))
	defer srv.Close()
	resp, err := noRedirects(srv.Client()).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status %d, want the 302 itself", resp.StatusCode)
	}
	if u, _ := url.Parse(resp.Header.Get("Location")); u.Path != "/elsewhere" {
		t.Errorf("Location = %q", resp.Header.Get("Location"))
	}
}

func TestRedirectsAccepted(t *testing.T) {
	cases := []struct {
		name       string
		redirects  []any
		postLogout []any
		want       bool
	}{
		{"both registered", []any{testCallback}, []any{testPostLogout}, true},
		{"callback missing", []any{LoopbackRedirectURI}, []any{testPostLogout}, false},
		{"post-logout missing", []any{testCallback}, []any{LoopbackRedirectURI}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeThunder(t, tc.redirects, tc.postLogout)
			got, err := redirectsAccepted(context.Background(), srv.URL, noRedirects(srv.Client()), testCallback, testPostLogout)
			if err != nil || got != tc.want {
				t.Errorf("got %v, %v; want %v", got, err, tc.want)
			}
			if f.puts != 0 {
				t.Error("a read-only check wrote to the client")
			}
		})
	}
}

func TestEnsureClient_CreatesTheDelegateClientOnce(t *testing.T) {
	f, srv := newFakeThunder(t, []any{LoopbackRedirectURI}, []any{LoopbackRedirectURI})
	f.noDelegate = true
	for range 2 {
		if err := run(t, srv, f.password); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.created) != 1 {
		t.Fatalf("created %d applications, want exactly 1", len(f.created))
	}
	doc := f.created[0]
	if _, ok := doc["id"]; ok {
		t.Error("the created application carries a client-chosen id")
	}
	if _, ok := doc["resource_type"]; ok {
		t.Error("the created application carries the bootstrap file's resource_type")
	}
	cfg := doc["inboundAuthConfig"].([]any)[0].(map[string]any)["config"].(map[string]any)
	if cfg["clientId"] != DelegateClientID || cfg["publicClient"] != true || cfg["tokenEndpointAuthMethod"] != "none" {
		t.Errorf("delegate client = %v", cfg)
	}
	if grants := cfg["grantTypes"].([]any); !slices.Contains(grants, any(tokenExchangeGrant)) {
		t.Errorf("grant types = %v, want token exchange", grants)
	}
	life := cfg["token"].(map[string]any)["accessToken"].(map[string]any)["userConfig"].(map[string]any)["validityPeriod"]
	if life != float64(DelegatedTokenLifetime) {
		t.Errorf("exchanged token lifetime = %v, want %d", life, DelegatedTokenLifetime)
	}
}

func TestEnsureClient_DelegateClientFailures(t *testing.T) {
	tests := []struct {
		name               string
		list, create       int
		wantErr, wantFixed bool
	}{
		{name: "the list fails", list: http.StatusInternalServerError, wantErr: true, wantFixed: true},
		{name: "creating fails", create: http.StatusBadRequest, wantErr: true, wantFixed: true},
		{name: "it was created meanwhile", create: http.StatusConflict, wantFixed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// An older client missing "groups", so the serendip client needs a fix.
			f, srv := newFakeThunder(t, []any{LoopbackRedirectURI}, []any{LoopbackRedirectURI})
			uc := userConfig(f.config())
			f.mu.Lock()
			uc["attributes"] = []any{"username"}
			f.noDelegate, f.listStatus, f.createStatus = true, tt.list, tt.create
			f.mu.Unlock()
			err := run(t, srv, f.password)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), f.password) {
				t.Error("the error carries the password")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if fixed := f.puts == 1; fixed != tt.wantFixed {
				t.Errorf("serendip client fixed = %v, want %v", fixed, tt.wantFixed)
			}
		})
	}
}

func TestEnsureClient_LeavesAnExistingDelegateClientAlone(t *testing.T) {
	f, srv := newFakeThunder(t, []any{LoopbackRedirectURI}, []any{LoopbackRedirectURI})
	if err := run(t, srv, f.password); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.created) != 0 || len(f.touched) != 0 {
		t.Errorf("created %v and touched %v; an existing serendip-mcp must be left as it is", f.created, f.touched)
	}
}
