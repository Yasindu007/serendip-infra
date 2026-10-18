package oidc_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/providers/identity/oidc"
)

const agentAud = "https://serendip.local/api"

// agentClaims is the shape of a real Agent Manager agent token
// (specs/018-agent-manager-poc plan.md T002 finding 5).
func (is *testIssuer) agentClaims(now time.Time, overrides map[string]any) map[string]any {
	c := map[string]any{
		"iss": is.url(), "sub": "01a14f26-cb2a-753e-89da-54e4993b7ad5", "aud": agentAud,
		"client_id": "MEmGX1ZI6acmZcGFUTR3AQ", "grant_type": "client_credentials", "jti": "01a14f26-d9ad",
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Hour).Unix(), "scope": "read",
	}
	for k, v := range overrides {
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
	}
	return c
}

// notReady is a human verifier still being set up.
type notReady struct{}

func (notReady) Authenticate(context.Context, string) (identity.Principal, error) {
	return identity.Principal{}, reconcile.ErrProviderNotReady
}

func TestVerifier_Agent(t *testing.T) {
	is := newIssuer(t)
	now := time.Now()
	v := newVerifier(t, oidc.Config{Issuer: is.url(), Audience: agentAud, Kind: identity.KindAgent, IssuerAlias: "am-dev", Now: func() time.Time { return now }})

	p, err := v.Authenticate(t.Context(), is.sign("RS256", "rsa1", is.agentClaims(now, nil)))
	if err != nil {
		t.Fatalf("real agent token rejected: %v", err)
	}
	want := identity.Principal{Kind: identity.KindAgent, Issuer: "am-dev", Subject: "01a14f26-cb2a-753e-89da-54e4993b7ad5", Name: "01a14f26-cb2a-753e-89da-54e4993b7ad5"}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("principal = %+v, want %+v", p, want)
	}
	if got := identity.Actor(p); got != "agent:am-dev/01a14f26-cb2a-753e-89da-54e4993b7ad5" {
		t.Errorf("actor = %q", got)
	}

	tests := []struct {
		name      string
		overrides map[string]any
		ok        bool
	}{
		{"groups are ignored, not trusted", map[string]any{"groups": []string{"Administrators"}}, true},
		{"exactly the maximum lifetime", map[string]any{"exp": now.Add(time.Hour).Unix()}, true},
		{"the maximum plus the leeway", map[string]any{"exp": now.Add(time.Hour + 30*time.Second).Unix()}, true},
		{"no grant_type claim: could be a person's", map[string]any{"grant_type": nil}, false},
		{"past the maximum plus the leeway", map[string]any{"exp": now.Add(time.Hour + 31*time.Second).Unix()}, false},
		{"expires before it was issued", map[string]any{"iat": now.Add(20 * time.Second).Unix(), "exp": now.Add(10 * time.Second).Unix()}, false},
		{"a subject with a newline", map[string]any{"sub": "agent\nforged-line"}, false},
		{"an over-long subject", map[string]any{"sub": strings.Repeat("a", 257)}, false},
		{"a person's login token", map[string]any{"grant_type": "authorization_code"}, false},
		{"a refreshed person's token", map[string]any{"grant_type": "refresh_token"}, false},
		{"no iat", map[string]any{"iat": nil}, false},
		{"lives too long", map[string]any{"exp": now.Add(2 * time.Hour).Unix()}, false},
		{"wrong audience", map[string]any{"aud": "serendip"}, false},
		{"expired", map[string]any{"iat": now.Add(-2 * time.Hour).Unix(), "exp": now.Add(-time.Hour).Unix()}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := v.Authenticate(t.Context(), is.sign("RS256", "rsa1", is.agentClaims(now, tt.overrides)))
			switch {
			case tt.ok && err != nil:
				t.Errorf("rejected: %v", err)
			case tt.ok && (p.Kind != identity.KindAgent || p.Groups != nil):
				t.Errorf("principal = %+v, want an agent with no groups", p)
			case !tt.ok && !errors.Is(err, identity.ErrInvalidCredential):
				t.Errorf("err = %v, want ErrInvalidCredential", err)
			}
		})
	}
}

func TestVerifier_PersonRefusesMachineTokens(t *testing.T) {
	is := newIssuer(t)
	now := time.Now()
	v := newVerifier(t, oidc.Config{Issuer: is.url(), Audience: "serendip", Now: func() time.Time { return now }})
	if _, err := v.Authenticate(t.Context(), is.sign("RS256", "rsa1", is.claims(now, map[string]any{"grant_type": "client_credentials"}))); !errors.Is(err, identity.ErrInvalidCredential) {
		t.Errorf("client_credentials token as a person: %v, want ErrInvalidCredential", err)
	}
	p, err := v.Authenticate(t.Context(), is.sign("RS256", "rsa1", is.claims(now, map[string]any{"grant_type": "authorization_code"})))
	if err != nil || p.Kind != identity.KindPerson {
		t.Errorf("login token: %+v, %v; want a person", p, err)
	}
}

func TestNewVerifier_AgentConfig(t *testing.T) {
	base := oidc.Config{Issuer: "https://idp.example", Audience: "a"}
	tests := []struct {
		name string
		cfg  func(c oidc.Config) oidc.Config
		ok   bool
	}{
		{"agent", func(c oidc.Config) oidc.Config { c.Kind, c.IssuerAlias = identity.KindAgent, "am-dev"; return c }, true},
		{"agent without alias", func(c oidc.Config) oidc.Config { c.Kind = identity.KindAgent; return c }, false},
		{"agent with a bad alias", func(c oidc.Config) oidc.Config { c.Kind, c.IssuerAlias = identity.KindAgent, "AM/dev"; return c }, false},
		{"agent lifetime over 24h", func(c oidc.Config) oidc.Config {
			c.Kind, c.IssuerAlias, c.MaxLifetime = identity.KindAgent, "a", 25*time.Hour
			return c
		}, false},
		{"person with an alias", func(c oidc.Config) oidc.Config { c.IssuerAlias = "x"; return c }, false},
		{"person with a lifetime", func(c oidc.Config) oidc.Config { c.MaxLifetime = time.Hour; return c }, false},
	}
	for _, tt := range tests {
		_, err := oidc.NewVerifier(tt.cfg(base))
		if (err == nil) != tt.ok {
			t.Errorf("%s: err = %v, want ok=%v", tt.name, err, tt.ok)
		}
	}
}

func TestIssuerRouter(t *testing.T) {
	people, agents := newIssuer(t), newIssuer(t)
	now := time.Now()
	pv := newVerifier(t, oidc.Config{Issuer: people.url(), Audience: "serendip", Now: func() time.Time { return now }})
	av := newVerifier(t, oidc.Config{Issuer: agents.url(), Audience: agentAud, Kind: identity.KindAgent, IssuerAlias: "am-dev", Now: func() time.Time { return now }})
	r := oidc.NewIssuerRouter(pv, map[string]identity.Authenticator{agents.url(): av})

	p, err := r.Authenticate(t.Context(), people.sign("RS256", "rsa1", people.claims(now, nil)))
	if err != nil || p.Kind != identity.KindPerson || p.Name != "admin" {
		t.Errorf("person token: %+v, %v", p, err)
	}
	a, err := r.Authenticate(t.Context(), agents.sign("RS256", "rsa1", agents.agentClaims(now, nil)))
	if err != nil || a.Kind != identity.KindAgent || a.Issuer != "am-dev" {
		t.Errorf("agent token: %+v, %v", a, err)
	}

	rejected := map[string]string{
		// The agent issuer signs a token claiming the human issuer: routed to
		// the human verifier, whose keys don't match.
		"agent issuer posing as the human issuer": agents.sign("RS256", "rsa1", agents.claims(now, map[string]any{"iss": people.url()})),
		// The human issuer signs a token claiming the agent issuer: routed to
		// the agent verifier, whose keys don't match.
		"human issuer posing as an agent issuer": people.sign("RS256", "rsa1", people.agentClaims(now, map[string]any{"iss": agents.url()})),
		// A person's token from the agent issuer, with the agents' audience.
		"a person's token from the agent issuer": agents.sign("RS256", "rsa1", agents.agentClaims(now, map[string]any{"grant_type": "authorization_code"})),
		// An agent token sent to people with their audience.
		"an agent token with the people's audience": agents.sign("RS256", "rsa1", agents.agentClaims(now, map[string]any{"aud": "serendip"})),
		"unknown issuer": newIssuer(t).sign("RS256", "rsa1", people.claims(now, map[string]any{"iss": "https://elsewhere.example"})),
		"not a token":    "nope",
		"payload not JSON": func() string {
			h := enc([]byte(`{"alg":"RS256","kid":"rsa1"}`))
			return h + "." + enc([]byte("not json")) + "." + enc([]byte("sig"))
		}(),
		"iss not a string": agents.sign("RS256", "rsa1", agents.agentClaims(now, map[string]any{"iss": 42})),
		"no iss at all":    agents.sign("RS256", "rsa1", agents.agentClaims(now, map[string]any{"iss": nil})),
		"over 16 KB":       agents.sign("RS256", "rsa1", agents.agentClaims(now, map[string]any{"pad": strings.Repeat("x", 17<<10)})),
	}
	for name, tok := range rejected {
		if p, err := r.Authenticate(t.Context(), tok); !errors.Is(err, identity.ErrInvalidCredential) {
			t.Errorf("%s: %+v, %v; want ErrInvalidCredential", name, p, err)
		}
	}

	// The human issuer not being ready yet doesn't stop agents, and its
	// "try again" passes through for people's tokens.
	waiting := oidc.NewIssuerRouter(notReady{}, map[string]identity.Authenticator{agents.url(): av})
	if _, err := waiting.Authenticate(t.Context(), people.sign("RS256", "rsa1", people.claims(now, nil))); !errors.Is(err, reconcile.ErrProviderNotReady) {
		t.Errorf("person token while the human issuer is not ready: %v, want ErrProviderNotReady", err)
	}
	if a, err := waiting.Authenticate(t.Context(), agents.sign("RS256", "rsa1", agents.agentClaims(now, nil))); err != nil || a.Kind != identity.KindAgent {
		t.Errorf("agent token while the human issuer is not ready: %+v, %v", a, err)
	}

	// With agents only (no cluster, so no human issuer), other tokens are
	// refused rather than panicking.
	only := oidc.NewIssuerRouter(nil, map[string]identity.Authenticator{agents.url(): av})
	if _, err := only.Authenticate(t.Context(), people.sign("RS256", "rsa1", people.claims(now, nil))); !errors.Is(err, identity.ErrInvalidCredential) {
		t.Errorf("agents-only router, person token: %v", err)
	}
}
