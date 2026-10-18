package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
	"github.com/serendip-infra/serendip-infra/providers/identity/oidc"
)

// TestAgentSeam follows a real signed agent token across every layer spec
// 018 added, as serendipd wires them: an agent issuer's discovery and keys,
// the issuer router, the agent verifier, the middleware (actor, sponsor),
// policy (role, guardrails) and the audit row. The layers' own tests use
// fakes on each side.
func TestAgentSeam(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	var issuer string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "jwks_uri": issuer + "/oauth2/jwks"})
		case "/oauth2/jwks":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{
				"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
				"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(idp.Close)
	issuer = idp.URL
	sign := func(claims map[string]any) string {
		h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "at+jwt"})
		p, _ := json.Marshal(claims)
		in := b64(h) + "." + b64(p)
		sum := sha256.Sum256([]byte(in))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		return in + "." + b64(sig)
	}
	now := time.Now()
	token := func(extra map[string]any) string {
		c := map[string]any{"iss": issuer, "sub": "agent-1", "aud": "https://serendip.local/api", "client_id": "c1",
			"grant_type": "client_credentials", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
			// A group claim an agent must never benefit from.
			"groups": []string{"Administrators"}}
		for k, v := range extra {
			c[k] = v
		}
		return sign(c)
	}

	issuers, err := parseAgentIssuers("am-dev="+issuer+"|https://serendip.local/api", "")
	if err != nil {
		t.Fatal(err)
	}
	agents, err := agentVerifiers(t.Context(), issuers)
	if err != nil {
		t.Fatal(err)
	}
	router := oidc.NewIssuerRouter(nil, agents)

	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "seam.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	mk := func(kind resource.Kind, name string, owner *resource.OwnerRef) string {
		t.Helper()
		meta := resource.Metadata{Name: name}
		if owner != nil {
			meta.OwnerRefs = []resource.OwnerRef{*owner}
		}
		var id string
		var err error
		switch kind {
		case resource.KindOrganization:
			r, e := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, kind).Create(ctx, resource.Resource[resource.OrganizationSpec, resource.OrganizationStatus]{Metadata: meta})
			id, err = r.Metadata.ID, e
		case resource.KindProject:
			r, e := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](db, kind).Create(ctx, resource.Resource[resource.ProjectSpec, resource.ProjectStatus]{Metadata: meta})
			id, err = r.Metadata.ID, e
		case resource.KindEnvironment:
			r, e := sqlite.NewStore[resource.EnvironmentSpec, resource.EnvironmentStatus](db, kind).Create(ctx, resource.Resource[resource.EnvironmentSpec, resource.EnvironmentStatus]{Metadata: meta})
			id, err = r.Metadata.ID, e
		case resource.KindApplication:
			r, e := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, kind).Create(ctx, resource.Resource[resource.ApplicationSpec, resource.ApplicationStatus]{Metadata: meta})
			id, err = r.Metadata.ID, e
		}
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return id
	}
	org := mk(resource.KindOrganization, "org", nil)
	proj := mk(resource.KindProject, "proj", &resource.OwnerRef{Kind: resource.KindOrganization, ID: org})
	env := mk(resource.KindEnvironment, "env", &resource.OwnerRef{Kind: resource.KindProject, ID: proj})
	app := mk(resource.KindApplication, "app", &resource.OwnerRef{Kind: resource.KindEnvironment, ID: env})
	assign := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment)
	for _, a := range []resource.RoleAssignmentSpec{
		{Principal: resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "am-dev/agent-1", Sponsor: &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "sub-sponsor"}},
			RoleDefinitionID: policy.ContributorRoleID, Scope: resource.Scope{Kind: resource.KindProject, ID: proj}},
		{Principal: resource.PrincipalRef{Type: resource.PrincipalGroup, ID: "Administrators"}, RoleDefinitionID: policy.OwnerRoleID, Scope: resource.RootScope},
	} {
		if _, err := assign.Create(ctx, resource.RoleAssignment{Metadata: resource.Metadata{Name: "seam"}, Spec: a}); err != nil {
			t.Fatal(err)
		}
	}
	h := api.NewServer(db, event.NewInProcessBus(), nil, router, nil, nil, nil).Handler

	do := func(tok, method, path string, body any) *httptest.ResponseRecorder {
		var rd *strings.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		} else {
			rd = strings.NewReader("")
		}
		r := httptest.NewRequest(method, path, rd)
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	tok := token(nil)
	if w := do(tok, http.MethodGet, "/v1/applications/"+app, nil); w.Code != http.StatusOK {
		t.Fatalf("agent read in its project: %d %s", w.Code, w.Body)
	}
	if w := do(tok, http.MethodGet, "/v1/organizations/"+org, nil); w.Code != http.StatusForbidden {
		t.Errorf("agent read above its project: %d, want 403 (the Administrators group claim must not apply)", w.Code)
	}
	created := do(tok, http.MethodPost, "/v1/applications", map[string]any{"metadata": map[string]any{"name": "by-agent",
		"ownerRefs": []any{map[string]any{"kind": "Environment", "id": env}}}})
	if created.Code != http.StatusCreated {
		t.Fatalf("agent create: %d %s", created.Code, created.Body)
	}
	del := do(tok, http.MethodDelete, "/v1/applications/"+app, nil)
	if del.Code != http.StatusForbidden || !strings.Contains(del.Body.String(), api.ProblemNeedsApproval) {
		t.Errorf("agent delete: %d %s, want needs-approval", del.Code, del.Body)
	}
	var actor, subject, sponsor string
	if err := db.QueryRow(`SELECT actor, actor_subject, actor_sponsor FROM audit_log WHERE kind = 'Application' AND actor LIKE 'agent:%' AND policy_decision LIKE 'allow:%'`).Scan(&actor, &subject, &sponsor); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if actor != "agent:am-dev/agent-1" || subject != "am-dev/agent-1" || sponsor != "sub-sponsor" {
		t.Errorf("audit = %q %q %q", actor, subject, sponsor)
	}

	// The same issuer's other tokens are refused: a person's login token,
	// one for another audience, and one that lives too long.
	for name, bad := range map[string]string{
		"a person's token": token(map[string]any{"grant_type": "authorization_code"}),
		"another audience": token(map[string]any{"aud": "serendip"}),
		"lives a day":      token(map[string]any{"exp": now.Add(24 * time.Hour).Unix()}),
	} {
		if w := do(bad, http.MethodGet, "/v1/applications/"+app, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, w.Code)
		}
	}
	// With agents only, a token claiming another issuer is refused too.
	if w := do(token(map[string]any{"iss": "https://elsewhere.example"}), http.MethodGet, "/v1/applications/"+app, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("unknown issuer: %d, want 401", w.Code)
	}
}

func TestIsAgentIssuer(t *testing.T) {
	issuers := []agentIssuer{{alias: "a", issuer: "https://a.example/"}}
	for issuer, want := range map[string]bool{"https://a.example": true, "https://a.example/": true, "https://b.example": false} {
		if got := isAgentIssuer(issuers, issuer); got != want {
			t.Errorf("isAgentIssuer(%q) = %v, want %v", issuer, got, want)
		}
	}
}
