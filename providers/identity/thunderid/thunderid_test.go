package thunderid

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// fakeAPI is a tiny Kubernetes API: GET/POST/apply-PATCH on exact paths,
// plus a canned response for the Service proxy.
type fakeAPI struct {
	mu        sync.Mutex
	objects   map[string]kubernetes.Object
	creates   int
	applies   map[string][]kubernetes.Object
	readiness int // status the proxied /health/readiness answers
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Contains(r.URL.Path, "/proxy/") {
		w.WriteHeader(f.readiness)
		return
	}
	switch r.Method {
	case http.MethodGet:
		obj, ok := f.objects[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(obj)
	case http.MethodPost:
		var obj kubernetes.Object
		json.NewDecoder(r.Body).Decode(&obj)
		f.objects[r.URL.Path+"/"+obj.Metadata.Name] = obj
		f.creates++
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(obj)
	case http.MethodPatch:
		var obj kubernetes.Object
		json.NewDecoder(r.Body).Decode(&obj)
		f.objects[r.URL.Path] = obj
		f.applies[r.URL.Path] = append(f.applies[r.URL.Path], obj)
		json.NewEncoder(w).Encode(obj)
	}
}

func newFake(t *testing.T) (*fakeAPI, *kubernetes.Client) {
	t.Helper()
	f := &fakeAPI{objects: map[string]kubernetes.Object{}, applies: map[string][]kubernetes.Object{}, readiness: http.StatusOK}
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	client, err := kubernetes.NewClient(kubernetes.Config{Host: srv.URL, CAData: caPEM})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return f, client
}

// lookup walks a decoded JSON document.
func lookup(t *testing.T, doc any, path ...string) any {
	t.Helper()
	cur := doc
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("at %q: not an object in %v", p, path)
		}
		cur = m[p]
	}
	return cur
}

func TestValues(t *testing.T) {
	const issuer = "http://identity.localhost:30080"
	raw, err := values(issuer, ConsoleRedirects{Callback: "http://localhost:8080/auth/callback", PostLogout: "http://localhost:8080/"})
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("values are not valid JSON/YAML: %v", err)
	}
	checks := []struct {
		path []string
		want any
	}{
		{[]string{"deployment", "replicaCount"}, 1.0},
		{[]string{"deployment", "image", "tag"}, ImageTag},
		{[]string{"deployment", "image", "pullPolicy"}, "IfNotPresent"},
		{[]string{"deployment", "resources", "limits", "memory"}, "256Mi"},
		{[]string{"deployment", "resources", "requests", "memory"}, "64Mi"},
		{[]string{"hpa", "enabled"}, false},
		{[]string{"ingress", "enabled"}, false},
		{[]string{"configuration", "server", "httpOnly"}, true},
		{[]string{"configuration", "server", "publicUrl"}, issuer},
		{[]string{"setup", "admin", "username"}, AdminUsername},
		{[]string{"setup", "admin", "passwordRef", "name"}, AdminSecretName},
		{[]string{"setup", "admin", "passwordRef", "key"}, AdminSecretKey},
	}
	for _, c := range checks {
		if got := lookup(t, v, c.path...); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %v, want %v", strings.Join(c.path, "."), got, c.want)
		}
	}
	for _, db := range []string{"config", "entity", "runtime_persistent", "runtime_transient"} {
		if got := lookup(t, v, "configuration", "database", db, "type"); got != "sqlite" {
			t.Errorf("database %s type = %v, want sqlite", db, got)
		}
	}

	scripts := lookup(t, v, "bootstrap", "scripts").(map[string]any)
	if len(scripts) != 2 || scripts[bootstrapFile] == nil || scripts[delegateBootstrapFile] == nil {
		t.Fatalf("bootstrap scripts = %v, want exactly %s and %s", scripts, bootstrapFile, delegateBootstrapFile)
	}
	if bootstrapFile >= delegateBootstrapFile {
		t.Errorf("%s must sort before %s: bootstrap files are imported in name order", bootstrapFile, delegateBootstrapFile)
	}
	var delegate any
	if err := json.Unmarshal([]byte(scripts[delegateBootstrapFile].(string)), &delegate); err != nil {
		t.Fatalf("delegate bootstrap file is not valid JSON/YAML: %v", err)
	}
	dcfg := lookup(t, delegate, "inboundAuthConfig").([]any)[0].(map[string]any)["config"]
	for _, c := range []struct {
		path []string
		want any
	}{
		{[]string{"clientId"}, DelegateClientID},
		{[]string{"publicClient"}, true},
		{[]string{"tokenEndpointAuthMethod"}, "none"},
		{[]string{"grantTypes"}, []any{"authorization_code", tokenExchangeGrant}},
		{[]string{"token", "accessToken", "userConfig", "validityPeriod"}, float64(DelegatedTokenLifetime)},
	} {
		if got := lookup(t, dcfg, c.path...); !reflect.DeepEqual(got, c.want) {
			t.Errorf("delegate client %s = %v, want %v", strings.Join(c.path, "."), got, c.want)
		}
	}
	var app any
	if err := json.Unmarshal([]byte(scripts[bootstrapFile].(string)), &app); err != nil {
		t.Fatalf("bootstrap file is not valid JSON/YAML: %v", err)
	}
	if got := lookup(t, app, "resource_type"); got != "application" {
		t.Errorf("resource_type = %v", got)
	}
	if got := lookup(t, app, "signOutFlowId"); got != defaultSignOutFlowID {
		t.Errorf("signOutFlowId = %v, want the shipped sign-out flow", got)
	}
	cfg := lookup(t, app, "inboundAuthConfig").([]any)[0].(map[string]any)["config"]
	appChecks := []struct {
		path []string
		want any
	}{
		{[]string{"clientId"}, ClientID},
		{[]string{"publicClient"}, true},
		{[]string{"pkceRequired"}, true},
		{[]string{"tokenEndpointAuthMethod"}, "none"},
		{[]string{"redirectUris"}, []any{LoopbackRedirectURI, "http://localhost:8080/auth/callback"}},
		{[]string{"postLogoutRedirectUris"}, []any{LoopbackRedirectURI, "http://localhost:8080/"}},
		{[]string{"grantTypes"}, []any{"authorization_code", "refresh_token"}},
		{[]string{"token", "accessToken", "userConfig", "attributes"}, []any{"username", "email", "name", "groups"}},
	}
	for _, c := range appChecks {
		if got := lookup(t, cfg, c.path...); !reflect.DeepEqual(got, c.want) {
			t.Errorf("client %s = %v, want %v", strings.Join(c.path, "."), got, c.want)
		}
	}
}

func TestDeployerEnsure(t *testing.T) {
	f, client := newFake(t)
	var gotArgs []string
	var valuesSeen []byte
	d := &Deployer{client: client, run: func(_ context.Context, args ...string) ([]byte, error) {
		gotArgs = args
		i := slices.Index(args, "-f")
		if i < 0 {
			t.Fatal("no -f values file")
		}
		var err error
		if valuesSeen, err = os.ReadFile(args[i+1]); err != nil {
			t.Fatalf("values file unreadable during helm run: %v", err)
		}
		return nil, nil
	}}
	if err := d.Ensure(t.Context(), "http://identity.localhost:30080"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	want := []string{"upgrade", "--install", Release, Chart, "--version", ChartVersion, "--namespace", Namespace}
	if !reflect.DeepEqual(gotArgs[:len(want)], want) {
		t.Errorf("helm args = %v, want prefix %v", gotArgs, want)
	}
	for _, a := range []string{"--wait", "--timeout"} {
		if !slices.Contains(gotArgs, a) {
			t.Errorf("helm args %v lack %s", gotArgs, a)
		}
	}
	if !strings.Contains(string(valuesSeen), `"publicUrl": "http://identity.localhost:30080"`) {
		t.Errorf("values file passed to helm lacks the issuer:\n%s", valuesSeen)
	}
	i := slices.Index(gotArgs, "-f")
	if _, err := os.Stat(gotArgs[i+1]); !os.IsNotExist(err) {
		t.Errorf("values file %s left behind after Ensure", gotArgs[i+1])
	}
	if _, ok := f.objects["/api/v1/namespaces/"+Namespace]; !ok {
		t.Error("namespace not applied before install")
	}
	if _, ok := f.objects[adminSecretPath]; !ok {
		t.Error("admin secret not created before install")
	}
}

func TestEnsureAdminSecret_CreatesOnceNeverUpdates(t *testing.T) {
	f, client := newFake(t)
	if err := EnsureAdminSecret(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	first := f.objects[adminSecretPath]
	var data map[string][]byte
	if err := json.Unmarshal(first.Data, &data); err != nil {
		t.Fatalf("secret data: %v", err)
	}
	if len(data[AdminSecretKey]) < 32 {
		t.Errorf("password %q too short", data[AdminSecretKey])
	}
	for range 3 {
		if err := EnsureAdminSecret(t.Context(), client); err != nil {
			t.Fatal(err)
		}
	}
	if f.creates != 1 {
		t.Errorf("creates = %d, want 1", f.creates)
	}
	if string(f.objects[adminSecretPath].Data) != string(first.Data) {
		t.Error("existing admin secret was changed")
	}
	if len(f.applies[adminSecretPath]) != 0 {
		t.Error("admin secret was server-side applied; it must only ever be created")
	}
}

// A missing Secret on a cluster that already holds an install is not
// recreated: a new random password would not be the stored admin's.
func TestEnsureAdminSecret_NotRecreatedForExistingInstall(t *testing.T) {
	f, client := newFake(t)
	f.objects[certsPVCPath] = kubernetes.Object{APIVersion: "v1", Kind: "PersistentVolumeClaim"}
	if err := EnsureAdminSecret(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	if f.creates != 0 {
		t.Errorf("creates = %d, want 0 (existing install)", f.creates)
	}
}

func TestEnsureRoute(t *testing.T) {
	f, client := newFake(t)
	ep := reconcile.GatewayEndpoint{Name: "serendip-external", Namespace: "kgateway-system", Host: "localhost", Port: 30080}
	if err := EnsureRoute(t.Context(), client, ep, "http://identity.localhost:30080"); err != nil {
		t.Fatal(err)
	}
	applied := f.applies[routePath]
	if len(applied) != 1 {
		t.Fatalf("applies to %s = %d, want 1", routePath, len(applied))
	}
	var spec struct {
		ParentRefs []map[string]string `json:"parentRefs"`
		Hostnames  []string            `json:"hostnames"`
		Rules      []struct {
			Matches []struct {
				Path struct{ Type, Value string } `json:"path"`
			} `json:"matches"`
			BackendRefs []struct {
				Name string `json:"name"`
				Port int    `json:"port"`
			} `json:"backendRefs"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(applied[0].Spec, &spec); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec.ParentRefs, []map[string]string{{"name": ep.Name, "namespace": ep.Namespace}}) {
		t.Errorf("parentRefs = %v", spec.ParentRefs)
	}
	if !reflect.DeepEqual(spec.Hostnames, []string{"identity.localhost"}) {
		t.Errorf("hostnames = %v", spec.Hostnames)
	}
	if len(spec.Rules) != 1 || len(spec.Rules[0].BackendRefs) != 1 ||
		spec.Rules[0].BackendRefs[0].Name != ServiceName || spec.Rules[0].BackendRefs[0].Port != ServicePort {
		t.Fatalf("rules = %+v", spec.Rules)
	}
	var exact, prefixes []string
	for _, m := range spec.Rules[0].Matches {
		switch m.Path.Type {
		case "Exact":
			exact = append(exact, m.Path.Value)
		case "PathPrefix":
			prefixes = append(prefixes, m.Path.Value)
		default:
			t.Errorf("match %v has type %q", m, m.Path.Type)
		}
	}
	wantExact := []string{
		"/oauth2/authorize", "/oauth2/token", "/oauth2/jwks", "/oauth2/userinfo", "/oauth2/revoke",
		"/oauth2/auth/callback", "/oauth2/logout", "/oauth2/logout/callback",
		"/flow/execute", "/flow/meta", "/design/resolve",
	}
	if !reflect.DeepEqual(exact, wantExact) {
		t.Errorf("exact paths = %v, want exactly %v", exact, wantExact)
	}
	if want := []string{"/.well-known", "/gate"}; !reflect.DeepEqual(prefixes, want) {
		t.Errorf("prefixes = %v, want exactly %v", prefixes, want)
	}
	// Nothing sensitive may be reachable, by either kind of match.
	routed := func(path string) bool {
		if slices.Contains(exact, path) {
			return true
		}
		return slices.ContainsFunc(prefixes, func(p string) bool { return path == p || strings.HasPrefix(path, p+"/") })
	}
	for _, forbidden := range []string{
		"/", "/console", "/mcp", "/health/readiness", "/applications", "/users", "/groups", "/agents",
		"/resource-servers", "/flows", "/design/themes", "/auth",
		"/oauth2/introspect", "/oauth2/dcr/register", "/oauth2/par", "/oauth2/bc-authorize",
	} {
		if routed(forbidden) {
			t.Errorf("route exposes %s", forbidden)
		}
	}
}

func TestCheckReady(t *testing.T) {
	f, client := newFake(t)
	if err := CheckReady(t.Context(), client); err != nil {
		t.Errorf("ready: %v", err)
	}
	f.readiness = http.StatusServiceUnavailable
	if err := CheckReady(t.Context(), client); err == nil {
		t.Error("503: want an error")
	}
}

func TestIssuer(t *testing.T) {
	ep := reconcile.GatewayEndpoint{Host: "localhost", Port: 30080}
	tests := []struct {
		override string
		want     string
		wantErr  bool
	}{
		{"", "http://identity.localhost:30080", false},
		{"https://login.example.com", "https://login.example.com", false},
		{"https://login.example.com/", "https://login.example.com", false},
		{"http://10.0.0.5.nip.io:30080", "http://10.0.0.5.nip.io:30080", false},
		{"ftp://x", "", true},
		{"identity.localhost", "", true},
		{"https://login.example.com/?x=1", "", true},
		{"https://user:pw@login.example.com", "", true},
	}
	for _, tt := range tests {
		got, err := Issuer(ep, tt.override)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("Issuer(%q) = (%q, %v), want (%q, err=%v)", tt.override, got, err, tt.want, tt.wantErr)
		}
	}
	if got, _ := Issuer(reconcile.GatewayEndpoint{Host: "10.0.0.5.nip.io", Port: 31000}, ""); got != "http://identity.10.0.0.5.nip.io:31000" {
		t.Errorf("default issuer = %q", got)
	}
}
