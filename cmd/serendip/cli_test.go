package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/api/catalog"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

type tokenAuthn map[string]identity.Principal

func (a tokenAuthn) Authenticate(_ context.Context, token string) (identity.Principal, error) {
	if p, ok := a[token]; ok {
		return p, nil
	}
	return identity.Principal{}, fmt.Errorf("%w: unknown", identity.ErrInvalidCredential)
}

// fakeIssuer is an identity provider's discovery, token and revocation
// endpoints, as ThunderID answers them.
type fakeIssuer struct {
	srv     *httptest.Server
	mu      sync.Mutex
	revoked []string
	grants  []string
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	f := &fakeIssuer{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{
				"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/oauth2/authorize",
				"token_endpoint": f.srv.URL + "/oauth2/token", "revocation_endpoint": f.srv.URL + "/oauth2/revoke",
			})
		case "/oauth2/token":
			r.ParseForm()
			f.grants = append(f.grants, r.PostForm.Get("grant_type"))
			switch {
			case r.PostForm.Get("grant_type") == "authorization_code" && r.PostForm.Get("code") == "c1" &&
				r.PostForm.Get("code_verifier") != "" && r.PostForm.Get("client_id") == "serendip" &&
				r.PostForm.Get("redirect_uri") == "http://127.0.0.1:8250/callback":
				json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "refresh_token": "rt-1", "expires_in": 3600})
			case r.PostForm.Get("grant_type") == tokenExchangeGrant && r.PostForm.Get("client_id") == "serendip-mcp" &&
				(r.PostForm.Get("subject_token") == "at-1" || r.PostForm.Get("subject_token") == "at-2") && !slices.Contains(f.revoked, "rt-1"):
				// The person's token, exchanged for the assistant client.
				json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-ai", "expires_in": 300,
					"issued_token_type": "urn:ietf:params:oauth:token-type:access_token"})
			case r.PostForm.Get("grant_type") == "refresh_token" && r.PostForm.Get("refresh_token") == "rt-1":
				json.NewEncoder(w).Encode(map[string]any{"access_token": "at-2", "refresh_token": "rt-2", "expires_in": 3600})
			default:
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "no"})
			}
		case "/oauth2/revoke":
			r.ParseForm()
			f.revoked = append(f.revoked, r.PostForm.Get("token"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// world is an in-process serendipd API with a little data, and a record of
// the requests it received.
type world struct {
	t        *testing.T
	api      *httptest.Server
	issuer   *fakeIssuer
	db       *sql.DB
	dir      string
	envID    string
	appID    string
	mu       sync.Mutex
	requests []string // "METHOD path client"
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "cli.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, issuer: newFakeIssuer(t), dir: t.TempDir(), db: db}
	org, _ := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization).Create(ctx,
		resource.Resource[resource.OrganizationSpec, resource.OrganizationStatus]{Metadata: resource.Metadata{Name: "org"}})
	proj, _ := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](db, resource.KindProject).Create(ctx,
		resource.Resource[resource.ProjectSpec, resource.ProjectStatus]{Metadata: resource.Metadata{Name: "proj",
			OwnerRefs: []resource.OwnerRef{{Kind: resource.KindOrganization, ID: org.Metadata.ID}}}})
	env, _ := sqlite.NewStore[resource.EnvironmentSpec, resource.EnvironmentStatus](db, resource.KindEnvironment).Create(ctx,
		resource.Resource[resource.EnvironmentSpec, resource.EnvironmentStatus]{Metadata: resource.Metadata{Name: "env",
			OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: proj.Metadata.ID}}}})
	app, err := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, resource.KindApplication).Create(ctx,
		resource.Resource[resource.ApplicationSpec, resource.ApplicationStatus]{Metadata: resource.Metadata{Name: "shop",
			OwnerRefs: []resource.OwnerRef{{Kind: resource.KindEnvironment, ID: env.Metadata.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	w.envID, w.appID = env.Metadata.ID, app.Metadata.ID
	if _, err := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment).Create(ctx,
		resource.RoleAssignment{Metadata: resource.Metadata{Name: "owner"}, Spec: resource.RoleAssignmentSpec{
			Principal: resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "sub-owner"}, RoleDefinitionID: policy.OwnerRoleID, Scope: resource.RootScope,
		}}); err != nil {
		t.Fatal(err)
	}
	owner := identity.Principal{Subject: "sub-owner", Name: "owner"}
	assistant := owner
	assistant.Via = "serendip-mcp"
	authn := tokenAuthn{"at-1": owner, "at-2": owner, "tok-ai": assistant}
	srv := api.NewServer(db, event.NewInProcessBus(), map[string]string{"key-ops": "ops"}, authn, &reconcile.SecretReconciler{}, nil, nil,
		api.WithResourceMetadata(api.ResourceMetadata{
			Issuer: func() string { return w.issuer.srv.URL }, CLIClient: "serendip", AssistantClient: "serendip-mcp",
		}))
	w.api = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		w.requests = append(w.requests, r.Method+" "+r.URL.Path+" "+r.Header.Get("X-Serendip-Client"))
		w.mu.Unlock()
		srv.Handler.ServeHTTP(rw, r)
	}))
	t.Cleanup(w.api.Close)
	return w
}

func (w *world) sent() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.requests)
}

type result struct {
	code           int
	stdout, stderr string
}

// cli runs the CLI against the world, with extra environment variables.
func (w *world) cli(env map[string]string, stdin string, args ...string) result {
	w.t.Helper()
	return w.cliWith(env, strings.NewReader(stdin), loginOptions{}, args...)
}

func (w *world) cliWith(env map[string]string, stdin io.Reader, lo loginOptions, args ...string) result {
	w.t.Helper()
	getenv := func(k string) string {
		switch k {
		case "SERENDIP_CONFIG_DIR":
			return w.dir
		case "SERENDIP_SERVER":
			return w.api.URL
		}
		return env[k]
	}
	var out, errOut bytes.Buffer
	if lo.out == nil {
		lo.out = &errOut
	}
	if lo.in == nil {
		lo.in = stdin
	}
	if lo.timeout == 0 {
		lo.timeout = 10 * time.Second
	}
	code := runWith(context.Background(), args, env2(getenv, stdin, &out, &errOut, lo))
	return result{code, out.String(), errOut.String()}
}

func env2(getenv func(string) string, stdin io.Reader, out, errOut io.Writer, lo loginOptions) env {
	return env{getenv: getenv, stdin: stdin, stdout: out, stderr: errOut, login: lo}
}

var ops = map[string]string{"SERENDIP_API_KEY": "key-ops"}

func freePort(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// browser plays the person's browser: it reads the sign-in link and, as the
// identity provider would after sign-in, sends the code to the loopback.
func browser(t *testing.T, listen, code string) func(string) error {
	return func(link string) error {
		u, err := url.Parse(link)
		if err != nil {
			return err
		}
		q := u.Query()
		if q.Get("client_id") != "serendip" || q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != "http://127.0.0.1:8250/callback" {
			t.Errorf("sign-in link = %s", link)
		}
		go func() {
			resp, err := http.Get("http://" + listen + "/callback?" + url.Values{"code": {code}, "state": {q.Get("state")}}.Encode())
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

func TestLoginCommandLogout(t *testing.T) {
	w := newWorld(t)
	listen := freePort(t)
	r := w.cliWith(nil, strings.NewReader(""), loginOptions{listen: listen, openBrowser: browser(t, listen, "c1")}, "login", "--yes")
	if r.code != exitOK {
		t.Fatalf("login: %d %s %s", r.code, r.stdout, r.stderr)
	}
	b, err := os.ReadFile(credentialsPath(w.dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"accessToken": "at-1"`)) {
		t.Errorf("credentials = %s", b)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(credentialsPath(w.dir)); st.Mode().Perm() != 0o600 {
			t.Errorf("credentials mode = %v, want 0600", st.Mode().Perm())
		}
	}
	if strings.Contains(r.stdout+r.stderr, "at-1") || strings.Contains(r.stdout+r.stderr, "rt-1") {
		t.Error("a token was printed")
	}

	r = w.cli(nil, "", "whoami")
	if r.code != exitOK || !strings.Contains(r.stdout, "owner") {
		t.Errorf("whoami: %d %q %q", r.code, r.stdout, r.stderr)
	}
	if got := w.sent(); !slices.Contains(got, "GET /v1/session cli") {
		t.Errorf("requests = %v, want GET /v1/session declared as cli", got)
	}

	r = w.cli(nil, "", "logout")
	if r.code != exitOK {
		t.Fatalf("logout: %d %s", r.code, r.stderr)
	}
	w.issuer.mu.Lock()
	revoked := slices.Clone(w.issuer.revoked)
	w.issuer.mu.Unlock()
	if !slices.Equal(revoked, []string{"rt-1", "at-1"}) {
		t.Errorf("revoked = %v, want the refresh then the access token", revoked)
	}
	if _, err := os.Stat(credentialsPath(w.dir)); !os.IsNotExist(err) {
		t.Error("credentials still on disk after logout")
	}
	if r := w.cli(nil, "", "whoami"); r.code != exitUnauthorized {
		t.Errorf("whoami after logout: %d %s", r.code, r.stderr)
	}
}

// pasteBack answers the paste-back prompt: it waits for the sign-in link,
// then pastes the address the browser would have landed on.
type pasteBack struct {
	pr       *io.PipeReader
	pw       *io.PipeWriter
	code     string
	badState bool
	buf      bytes.Buffer
	once     sync.Once
}

func (p *pasteBack) Write(b []byte) (int, error) {
	p.buf.Write(b)
	for _, f := range strings.Fields(p.buf.String()) {
		if strings.Contains(f, "/oauth2/authorize?") {
			p.once.Do(func() {
				u, _ := url.Parse(f)
				state := u.Query().Get("state")
				if p.badState {
					state = "forged"
				}
				go fmt.Fprintf(p.pw, "http://127.0.0.1:8250/callback?code=%s&state=%s\n", p.code, state)
			})
		}
	}
	return len(b), nil
}

func TestLoginPasteBack(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprintf("forged state %v", bad), func(t *testing.T) {
			w := newWorld(t)
			pr, pw := io.Pipe()
			pb := &pasteBack{pr: pr, pw: pw, code: "c1", badState: bad}
			r := w.cliWith(nil, pr, loginOptions{out: pb, in: bufio.NewReader(pr)}, "login", "--no-browser", "--yes")
			if bad {
				if r.code == exitOK || !strings.Contains(r.stderr, "state mismatch") {
					t.Errorf("forged state: %d %s", r.code, r.stderr)
				}
				return
			}
			if r.code != exitOK {
				t.Fatalf("paste-back login: %d %s", r.code, r.stderr)
			}
			if !strings.Contains(pb.buf.String(), "Paste that whole address") {
				t.Errorf("prompt = %q", pb.buf.String())
			}
		})
	}
}

func TestRefreshRotatesAndSaves(t *testing.T) {
	w := newWorld(t)
	c := &credentials{Server: w.api.URL, Issuer: w.issuer.srv.URL, ClientID: "serendip", TokenURL: w.issuer.srv.URL + "/oauth2/token",
		AccessToken: "old", RefreshToken: "rt-1", Expiry: time.Now().Add(-time.Minute)}
	if err := saveCredentials(w.dir, c); err != nil {
		t.Fatal(err)
	}
	if r := w.cli(nil, "", "whoami"); r.code != exitOK {
		t.Fatalf("whoami with an expired token: %d %s", r.code, r.stderr)
	}
	got, _ := loadCredentials(w.dir)
	if got.AccessToken != "at-2" || got.RefreshToken != "rt-2" || time.Until(got.Expiry) < 30*time.Minute {
		t.Errorf("after refresh: %+v", got)
	}
	// A second command uses the saved token without refreshing again.
	w.cli(nil, "", "whoami")
	w.issuer.mu.Lock()
	defer w.issuer.mu.Unlock()
	if !slices.Equal(w.issuer.grants, []string{"refresh_token"}) {
		t.Errorf("token requests = %v, want one refresh", w.issuer.grants)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "credentials.lock")); !os.IsNotExist(err) {
		t.Error("the refresh lock was left behind")
	}
}

func TestCommands(t *testing.T) {
	w := newWorld(t)
	appFile := filepath.Join(t.TempDir(), "app.json")
	os.WriteFile(appFile, []byte(fmt.Sprintf(`{"metadata":{"name":"cart","ownerRefs":[{"kind":"Environment","id":%q}]}}`, w.envID)), 0o600)

	r := w.cli(ops, "", "applications", "create", "--file", appFile)
	if r.code != exitOK || !strings.Contains(r.stdout, `"name": "cart"`) {
		t.Fatalf("create: %d %s %s", r.code, r.stdout, r.stderr)
	}
	r = w.cli(ops, "", "applications", "list")
	if r.code != exitOK || !strings.Contains(r.stdout, "ID") || !strings.Contains(r.stdout, "shop") || !strings.Contains(r.stdout, "cart") {
		t.Errorf("list table: %d %q", r.code, r.stdout)
	}
	r = w.cli(ops, "", "applications", "list", "--json")
	var list []map[string]any
	if r.code != exitOK || json.Unmarshal([]byte(r.stdout), &list) != nil || len(list) != 2 {
		t.Errorf("list --json: %d %q", r.code, r.stdout)
	}
	if r := w.cli(ops, "", "applications", "get", "no-such-id"); r.code != exitNotFound {
		t.Errorf("get missing: %d %s", r.code, r.stderr)
	}

	before := len(w.sent())
	r = w.cli(ops, "", "--dry-run", "applications", "delete", w.appID, "--cascade")
	if r.code != exitOK || !strings.Contains(r.stdout, "DELETE "+w.api.URL+"/v1/applications/"+w.appID+"?cascade=true") {
		t.Errorf("dry run: %d %q", r.code, r.stdout)
	}
	if len(w.sent()) != before {
		t.Error("a dry run sent a request")
	}

	for _, tt := range []struct {
		args []string
		code int
	}{
		{[]string{"no", "such", "command"}, exitUsage},
		{[]string{"applications", "get"}, exitUsage},
		{[]string{"applications", "get", "a", "b"}, exitUsage},
		{[]string{"applications", "list", "--nope", "x"}, exitUsage},
		{[]string{"approvals", "list", "--limit", "x"}, exitUsage},
		{[]string{"secrets", "value", "set", "s1", "--data", "password=hunter2"}, exitUsage},
	} {
		if r := w.cli(ops, "", tt.args...); r.code != tt.code {
			t.Errorf("%v: %d, want %d (%s)", tt.args, r.code, tt.code, r.stderr)
		}
	}
	if r := w.cli(map[string]string{"SERENDIP_API_KEY": "wrong"}, "", "applications", "list"); r.code != exitUnauthorized {
		t.Errorf("a bad key: %d %s", r.code, r.stderr)
	}
	for _, s := range w.sent() {
		if !strings.HasSuffix(s, " cli") {
			t.Errorf("request %q didn't declare the CLI", s)
		}
	}
	if r := w.cli(ops, "", "applications", "delete", "--help"); r.code != exitOK || !strings.Contains(r.stdout, "--cascade") || !strings.Contains(r.stdout, "DELETE /v1/applications/{id}") {
		t.Errorf("help: %d %q", r.code, r.stdout)
	}
}

func TestSecretValuesHiddenByDefault(t *testing.T) {
	var out, errOut bytes.Buffer
	op := mustOp(t, "secrets_read_value")
	printSuccess(op, response{Status: 200, Header: http.Header{}, Body: []byte(`{"data":{"password":"hunter2"}}`)}, globals{}, &out, &errOut)
	if strings.Contains(out.String(), "hunter2") || !strings.Contains(out.String(), "password") || !strings.Contains(errOut.String(), "--show-value") {
		t.Errorf("hidden: %q %q", out.String(), errOut.String())
	}
	out.Reset()
	printSuccess(op, response{Status: 200, Header: http.Header{}, Body: []byte(`{"data":{"password":"hunter2"}}`)}, globals{showValue: true, json: true}, &out, &errOut)
	if !strings.Contains(out.String(), "hunter2") {
		t.Errorf("--show-value: %q", out.String())
	}
	// A value from a file is fine; the dry run doesn't print it.
	f := filepath.Join(t.TempDir(), "v.json")
	os.WriteFile(f, []byte(`{"password":"hunter2"}`), 0o600)
	w := newWorld(t)
	r := w.cli(ops, "", "--dry-run", "secrets", "value", "set", "s1", "--data", "@"+f)
	if r.code != exitOK || strings.Contains(r.stdout, "hunter2") || !strings.Contains(r.stdout, "POST") {
		t.Errorf("dry-run of a value write: %d %q", r.code, r.stdout)
	}
}

func mustOp(t *testing.T, name string) catalog.Operation {
	t.Helper()
	op, ok := catalog.Lookup(name)
	if !ok {
		t.Fatalf("no operation %s", name)
	}
	return op
}

func TestDeleteThatNeedsApproval(t *testing.T) {
	w := newWorld(t)
	assistant := map[string]string{"SERENDIP_TOKEN": "tok-ai"}
	r := w.cli(assistant, "", "applications", "delete", w.appID)
	if r.code != exitNeedsApproval || !strings.Contains(r.stderr, "serendip approvals request --kind Application --id "+w.appID) {
		t.Errorf("needs approval: %d %s", r.code, r.stderr)
	}
	r = w.cli(assistant, "", "applications", "delete", w.appID, "--cascade", "--reason", "retired")
	if r.code != exitNeedsApproval || !strings.Contains(r.stderr, "filed (Pending)") {
		t.Fatalf("filed: %d %s", r.code, r.stderr)
	}
	r = w.cli(ops, "", "approvals", "list", "--phase", "Pending", "--json")
	var list []resource.Approval
	if r.code != exitOK || json.Unmarshal([]byte(r.stdout), &list) != nil || len(list) != 1 ||
		!list[0].Spec.Action.Cascade || list[0].Spec.Reason != "retired" {
		t.Errorf("pending approvals: %d %s", r.code, r.stdout)
	}
	// The application is still there.
	if r := w.cli(ops, "", "applications", "get", w.appID); r.code != exitOK {
		t.Errorf("application gone before approval: %d", r.code)
	}
}

func TestServerResourceMetadata(t *testing.T) {
	w := newWorld(t)
	m, e, err := discover(context.Background(), newHTTPClient(), w.api.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	if m.CLIClient != "serendip" || m.AssistantClient != "serendip-mcp" || e.Token != w.issuer.srv.URL+"/oauth2/token" {
		t.Errorf("discovered %+v %+v", m, e)
	}
}

func TestLocalhostNamesResolveToLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.Host) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	resp, err := newHTTPClient().Get("http://identity.localhost:" + port + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "identity.localhost:"+port {
		t.Errorf("Host = %q, want the name kept", b)
	}
	for _, h := range []string{"localhost", "a.localhost", "A.LocalHost."} {
		if !isLocalhost(h) {
			t.Errorf("isLocalhost(%q) = false", h)
		}
	}
	for _, h := range []string{"localhost.example", "notlocalhost", "example.com"} {
		if isLocalhost(h) {
			t.Errorf("isLocalhost(%q) = true", h)
		}
	}
}

func TestStoredSignInOnlyGoesToItsServer(t *testing.T) {
	w := newWorld(t)
	other := newWorld(t)
	w.signIn(t)
	// The same configuration directory, pointed at another server.
	getenv := func(k string) string {
		if k == "SERENDIP_CONFIG_DIR" {
			return w.dir
		}
		return ""
	}
	var out, errOut bytes.Buffer
	code := runWith(context.Background(), []string{"--server", other.api.URL, "whoami"}, env{getenv: getenv, stdin: strings.NewReader(""), stdout: &out, stderr: &errOut})
	if code != exitUnauthorized || !strings.Contains(errOut.String(), "signed in to "+w.api.URL) {
		t.Errorf("a sign-in used against another server: %d %s", code, errOut.String())
	}
	for _, s := range other.sent() {
		if strings.Contains(s, "/v1/session") {
			t.Errorf("the other server was asked %q with someone's token", s)
		}
	}
}

func TestPlainHTTPToAnotherMachineIsRefused(t *testing.T) {
	w := newWorld(t)
	if r := w.cli(ops, "", "--server", "http://serendip.example:8080", "readiness"); r.code != exitUsage || !strings.Contains(r.stderr, "plain http") {
		t.Errorf("plain http: %d %s", r.code, r.stderr)
	}
	if r := w.cli(ops, "", "--server", "ftp://serendip.example", "readiness"); r.code != exitUsage {
		t.Errorf("ftp: %d %s", r.code, r.stderr)
	}
	for _, ok := range []string{"https://serendip.example", "http://localhost:8080", "http://127.0.0.1:1", "http://serendip.localhost"} {
		if _, err := checkURL(ok, false); err != nil {
			t.Errorf("checkURL(%s): %v", ok, err)
		}
	}
	if _, err := checkURL("http://serendip.example", true); err != nil {
		t.Errorf("--insecure: %v", err)
	}
}

func TestDiscoverRefusesEndpointsElsewhere(t *testing.T) {
	for _, tt := range []struct{ name, authorize, token string }{
		{"authorize on another origin", "https://evil.example/authorize", ""},
		{"a file authorize URL", "file:///etc/passwd", ""},
		{"token on another origin", "", "http://127.0.0.1:1/token"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var idp *httptest.Server
			idp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				doc := map[string]string{"issuer": idp.URL, "authorization_endpoint": idp.URL + "/authorize", "token_endpoint": idp.URL + "/token"}
				if tt.authorize != "" {
					doc["authorization_endpoint"] = tt.authorize
				}
				if tt.token != "" {
					doc["token_endpoint"] = tt.token
				}
				json.NewEncoder(w).Encode(doc)
			}))
			defer idp.Close()
			var rs *httptest.Server
			rs = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"resource": rs.URL, "authorization_servers": []string{idp.URL}, "serendip_cli_client_id": "serendip"})
			}))
			defer rs.Close()
			if _, _, err := discover(context.Background(), newHTTPClient(), rs.URL, false); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestReasonReachesApprovalsRequest(t *testing.T) {
	w := newWorld(t)
	r := w.cli(map[string]string{"SERENDIP_TOKEN": "tok-ai"}, "", "approvals", "request", "--kind", "Application", "--id", w.appID, "--reason", "retired", "--json")
	if r.code != exitOK || !strings.Contains(r.stdout, `"reason": "retired"`) {
		t.Errorf("approvals request --reason: %d %s %s", r.code, r.stdout, r.stderr)
	}
}

func TestDescribeHandlesEveryListShape(t *testing.T) {
	for _, item := range []map[string]any{
		{"id": "Owner", "roleName": "Owner"},
		{"metadata": map[string]any{"id": "a1", "name": "shop"}},
		{"metadata": map[string]any{"id": "a1", "name": "shop"}, "status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}},
		{"metadata": map[string]any{"id": "ap1"}, "spec": map[string]any{"action": map[string]any{"verb": "delete", "target": map[string]any{"kind": "Component", "id": "c1"}}}, "status": map[string]any{"phase": "Pending"}},
		{},
		{"status": "not an object"},
	} {
		describe(item) // must not panic
	}
	var out bytes.Buffer
	printHuman([]byte(`[{"id":"Owner","roleName":"Owner"},{"metadata":{"id":"a1","name":"shop"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]`), &out)
	if !strings.Contains(out.String(), "Owner") || !strings.Contains(out.String(), "Ready=True") {
		t.Errorf("table = %q", out.String())
	}
}

func TestParseArgs(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "doc.json")
	os.WriteFile(doc, []byte(`{"metadata":{"name":"x"}}`), 0o600)
	tests := []struct {
		op    string
		args  []string
		stdin string
		want  string // JSON of the parsed arguments
		err   string
	}{
		{op: "applications_get", args: []string{"a1"}, want: `{"id":"a1"}`},
		{op: "applications_get", args: []string{"--id", "a1"}, want: `{"id":"a1"}`},
		{op: "applications_delete", args: []string{"a1", "--cascade"}, want: `{"cascade":true,"id":"a1"}`},
		{op: "applications_delete", args: []string{"a1", "--cascade=false"}, want: `{"cascade":false,"id":"a1"}`},
		{op: "approvals_list", args: []string{"--limit", "20"}, want: `{"limit":20}`},
		{op: "applications_create", args: []string{"--file", doc}, want: `{"resource":{"metadata":{"name":"x"}}}`},
		{op: "applications_create", args: []string{"--file=" + doc}, want: `{"resource":{"metadata":{"name":"x"}}}`},
		{op: "applications_create", args: []string{"--file", "-"}, stdin: `{"metadata":{"name":"y"}}`, want: `{"resource":{"metadata":{"name":"y"}}}`},
		{op: "applications_create", args: []string{"--resource", `{"metadata":{"name":"z"}}`}, want: `{"resource":{"metadata":{"name":"z"}}}`},
		{op: "applications_list", args: []string{"--labels", "team=a,tier=web"}, want: `{"labels":{"team":"a","tier":"web"}}`},
		{op: "secrets_write_value", args: []string{"s1", "--data", "-"}, stdin: `{"k":"v"}`, want: `{"data":{"k":"v"},"id":"s1"}`},
		{op: "secrets_write_value", args: []string{"s1", "--data", `{"k":"v"}`}, err: "never the command line"},
		{op: "applications_get", args: []string{}, err: "missing <id>"},
		{op: "applications_get", args: []string{"a", "b"}, err: "unexpected argument"},
		{op: "applications_get", args: []string{"a", "--nope"}, err: "has no --nope"},
		{op: "approvals_list", args: []string{"--limit"}, err: "needs a value"},
		{op: "approvals_list", args: []string{"--limit", "x"}, err: "whole number"},
		{op: "approvals_list", args: []string{"--phase", "Pending", "--phase", "Approved"}, err: "given twice"},
		{op: "applications_list", args: []string{"--labels", "oops"}, err: "name=value"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(append([]string{tt.op}, tt.args...), " "), func(t *testing.T) {
			got, err := parseArgs(mustOp(t, tt.op), tt.args, strings.NewReader(tt.stdin))
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if b, _ := json.Marshal(got); string(b) != tt.want {
				t.Errorf("got %s, want %s", b, tt.want)
			}
		})
	}
}

func TestExitCodes(t *testing.T) {
	op := mustOp(t, "readiness_get")
	for status, want := range map[int]int{401: exitUnauthorized, 403: exitDenied, 404: exitNotFound, 409: exitConflict, 412: exitConflict, 422: exitError, 500: exitServer, 503: exitServer, 302: exitError} {
		var out, errOut bytes.Buffer
		body := []byte(`{"title":"t","detail":"d\u001b[31mred"}`)
		if got := printFailure(context.Background(), nil, op, nil, response{Status: status, Body: body}, globals{}, &out, &errOut); got != want {
			t.Errorf("HTTP %d: exit %d, want %d", status, got, want)
		}
		if strings.Contains(errOut.String(), "\x1b") {
			t.Errorf("HTTP %d: a terminal escape reached the terminal", status)
		}
	}
}

// A forged request to the loopback can't abort the sign-in; only the
// answer with this sign-in's state counts.
func TestLoopbackIgnoresForgedAnswers(t *testing.T) {
	w := newWorld(t)
	listen := freePort(t)
	forgeThenAnswer := func(link string) error {
		u, _ := url.Parse(link)
		go func() {
			for _, q := range []string{"error=access_denied", "state=forged&code=evil", "state=forged&error=x"} {
				if resp, err := http.Get("http://" + listen + "/callback?" + q); err == nil {
					resp.Body.Close()
				}
			}
			resp, err := http.Get("http://" + listen + "/callback?" + url.Values{"code": {"c1"}, "state": {u.Query().Get("state")}}.Encode())
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	if r := w.cliWith(nil, strings.NewReader(""), loginOptions{listen: listen, openBrowser: forgeThenAnswer}, "login", "--yes"); r.code != exitOK {
		t.Fatalf("login after forged answers: %d %s", r.code, r.stderr)
	}
	// The listener is gone afterwards.
	if ln, err := net.Listen("tcp", listen); err != nil {
		t.Errorf("the loopback listener wasn't released: %v", err)
	} else {
		ln.Close()
	}
}

// Concurrent commands with an expired sign-in refresh exactly once: a
// second refresh would replay the spent refresh token.
func TestConcurrentRefreshHappensOnce(t *testing.T) {
	w := newWorld(t)
	if err := saveCredentials(w.dir, &credentials{Server: w.api.URL, Issuer: w.issuer.srv.URL, ClientID: "serendip",
		TokenURL: w.issuer.srv.URL + "/oauth2/token", AccessToken: "old", RefreshToken: "rt-1", Expiry: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tok, err := freshToken(context.Background(), newHTTPClient(), w.dir, w.api.URL); err != nil || tok != "at-2" {
				t.Errorf("freshToken = %q, %v", tok, err)
			}
		}()
	}
	wg.Wait()
	w.issuer.mu.Lock()
	defer w.issuer.mu.Unlock()
	if !slices.Equal(w.issuer.grants, []string{"refresh_token"}) {
		t.Errorf("token requests = %v, want exactly one refresh", w.issuer.grants)
	}
}

func TestLockTakeoverAndUnlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.lock")
	// A stale lock from a process that died is taken over.
	os.WriteFile(path, []byte("dead"), 0o600)
	old := time.Now().Add(-lockStale - time.Minute)
	os.Chtimes(path, old, old)
	unlock, err := lockCredentials(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	// Someone else's lock replaced ours: unlocking must leave it.
	os.WriteFile(path, []byte("someone else"), 0o600)
	unlock()
	if b, _ := os.ReadFile(path); string(b) != "someone else" {
		t.Error("unlock removed another holder's lock")
	}
	// A live lock makes a waiter give up when its context ends.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := lockCredentials(ctx, dir); err == nil {
		t.Error("took a live lock")
	}
}

// The first sign-in to a server confirms the identity provider it names:
// a hostile server could name a real provider and receive a real sign-in.
func TestLoginConfirmsTheIssuer(t *testing.T) {
	w := newWorld(t)
	tokens := func() int {
		w.issuer.mu.Lock()
		defer w.issuer.mu.Unlock()
		return len(w.issuer.grants)
	}
	listen := freePort(t)
	r := w.cliWith(nil, strings.NewReader("n\n"), loginOptions{listen: listen, openBrowser: browser(t, listen, "c1")}, "login")
	if r.code == exitOK || !strings.Contains(r.stderr, "not confirmed") || !strings.Contains(r.stderr, "signs people in with the identity provider "+w.issuer.srv.URL) {
		t.Errorf("declined: %d %s", r.code, r.stderr)
	}
	if r := w.cliWith(nil, strings.NewReader(""), loginOptions{listen: listen, openBrowser: browser(t, listen, "c1")}, "login"); r.code == exitOK {
		t.Errorf("no answer (no terminal) signed in: %s", r.stderr)
	}
	if tokens() != 0 {
		t.Fatal("a sign-in went ahead without confirmation")
	}
	if r := w.cliWith(nil, strings.NewReader("y\n"), loginOptions{listen: listen, openBrowser: browser(t, listen, "c1")}, "login"); r.code != exitOK {
		t.Fatalf("confirmed: %d %s", r.code, r.stderr)
	}
	// Remembered: no question the second time.
	if r := w.cliWith(nil, strings.NewReader(""), loginOptions{listen: listen, openBrowser: browser(t, listen, "c1")}, "login"); r.code != exitOK || strings.Contains(r.stderr, "[y/N]") {
		t.Errorf("second sign-in: %d %s", r.code, r.stderr)
	}
	// A changed identity provider is asked about again.
	os.WriteFile(trustedPath(w.dir), []byte(fmt.Sprintf(`{%q: "https://other-idp.example"}`, w.api.URL)), 0o600)
	if r := w.cliWith(nil, strings.NewReader(""), loginOptions{listen: listen, openBrowser: browser(t, listen, "c1")}, "login"); r.code == exitOK {
		t.Errorf("a changed provider signed in without asking: %s", r.stderr)
	}
}

func TestDiscoverRefusesAnotherResource(t *testing.T) {
	rs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"resource": "https://serendip.example", "authorization_servers": []string{"https://idp.example"}, "serendip_cli_client_id": "serendip"})
	}))
	defer rs.Close()
	if _, _, err := discover(context.Background(), newHTTPClient(), rs.URL, false); err == nil || !strings.Contains(err.Error(), "describes itself") {
		t.Errorf("discover = %v", err)
	}
}
