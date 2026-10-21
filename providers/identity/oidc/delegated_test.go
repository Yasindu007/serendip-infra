package oidc_test

import (
	"errors"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/providers/identity/oidc"
)

const exchange = "urn:ietf:params:oauth:grant-type:token-exchange"

// exchanged is a token ThunderID issues when a person's token is exchanged
// for the delegate client (spec 021 T001 finding 1).
func (is *testIssuer) exchanged(now time.Time, overrides map[string]any) map[string]any {
	c := is.claims(now, map[string]any{
		"aud": "serendip-mcp", "client_id": "serendip-mcp", "grant_type": exchange, "exp": now.Add(5 * time.Minute).Unix(),
	})
	for k, v := range overrides {
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
	}
	return c
}

func TestVerifier_Delegated(t *testing.T) {
	is := newIssuer(t)
	now := time.Now()
	v := newVerifier(t, oidc.Config{Issuer: is.url(), Audience: "serendip", DelegateClients: []string{"serendip-mcp"}, Now: func() time.Time { return now }})

	p, err := v.Authenticate(t.Context(), is.sign("RS256", "rsa1", is.exchanged(now, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != identity.KindPerson || p.Subject != "0190-0030" || p.Name != "admin" || p.Via != "serendip-mcp" {
		t.Errorf("principal = %+v, want the person via serendip-mcp", p)
	}
	if got := identity.Actor(p); got != "agent:serendip-mcp/0190-0030" {
		t.Errorf("actor = %q", got)
	}

	// The person's own sign-in is still the person, with no Via.
	p, err = v.Authenticate(t.Context(), is.sign("RS256", "rsa1", is.claims(now, map[string]any{"grant_type": "authorization_code"})))
	if err != nil || p.Via != "" {
		t.Errorf("the person's own token: %+v, %v", p, err)
	}

	refused := []struct {
		name   string
		claims map[string]any
	}{
		{"a delegate client's token from a direct sign-in", is.exchanged(now, map[string]any{"grant_type": "authorization_code"})},
		{"a delegate client's token with no grant type", is.exchanged(now, map[string]any{"grant_type": nil})},
		{"an exchange for a client that isn't a delegate", is.exchanged(now, map[string]any{"client_id": "other", "aud": "serendip"})},
		{"an exchange for the person's own client", is.claims(now, map[string]any{"grant_type": exchange})},
		{"a delegate token for the person's audience", is.exchanged(now, map[string]any{"aud": "serendip"})},
		{"a delegate token for another audience", is.exchanged(now, map[string]any{"aud": "elsewhere"})},
		{"a delegate token without iat", is.exchanged(now, map[string]any{"iat": nil})},
		{"a delegate token living too long", is.exchanged(now, map[string]any{"exp": now.Add(2 * time.Hour).Unix()})},
		{"a delegate client used for client credentials", is.exchanged(now, map[string]any{"grant_type": "client_credentials"})},
		{"a delegate token for the person's audience among others", is.exchanged(now, map[string]any{"aud": []any{"serendip", "other"}})},
		{"another spelling of the exchange grant", is.exchanged(now, map[string]any{"grant_type": "urn:ietf:params:oauth:grant-type:Token-Exchange"})},
		{"the exchange grant with a trailing slash", is.exchanged(now, map[string]any{"grant_type": exchange + "/"})},
		{"a delegate token issued after it expires", is.exchanged(now, map[string]any{"iat": now.Unix(), "exp": now.Add(-time.Minute).Unix()})},
		{"a delegate token one second past the cap and leeway", is.exchanged(now, map[string]any{
			"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(-time.Minute + oidc.MaxDelegatedLifetime + 31*time.Second).Unix()})},
		{"an exchanged token with no client", is.claims(now, map[string]any{"grant_type": exchange, "client_id": nil})},
	}
	for _, tt := range refused {
		if p, err := v.Authenticate(t.Context(), is.sign("RS256", "rsa1", tt.claims)); !errors.Is(err, identity.ErrInvalidCredential) {
			t.Errorf("%s: %+v, %v; want ErrInvalidCredential", tt.name, p, err)
		}
	}

	// Accepted shapes worth pinning: an audience array naming the delegate,
	// and a lifetime exactly at the cap.
	for name, c := range map[string]map[string]any{
		"an audience array with the delegate": is.exchanged(now, map[string]any{"aud": []any{"serendip-mcp", "serendip"}}),
		"a lifetime exactly at the cap": is.exchanged(now, map[string]any{
			"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(-time.Minute + oidc.MaxDelegatedLifetime).Unix()}),
	} {
		if p, err := v.Authenticate(t.Context(), is.sign("RS256", "rsa1", c)); err != nil || p.Via != "serendip-mcp" {
			t.Errorf("%s: %+v, %v; want delegated", name, p, err)
		}
	}
	// A grant type that only looks like an exchange is just a person's
	// token from a non-delegate client.
	if p, err := v.Authenticate(t.Context(), is.sign("RS256", "rsa1", is.claims(now, map[string]any{"grant_type": "Token-Exchange"}))); err != nil || p.Via != "" {
		t.Errorf("an odd grant type from the person's client: %+v, %v; want the person", p, err)
	}

	// Without delegate clients, an exchanged token is never accepted.
	plain := newVerifier(t, oidc.Config{Issuer: is.url(), Audience: "serendip", Now: func() time.Time { return now }})
	for _, c := range []map[string]any{is.exchanged(now, nil), is.claims(now, map[string]any{"grant_type": exchange})} {
		if _, err := plain.Authenticate(t.Context(), is.sign("RS256", "rsa1", c)); !errors.Is(err, identity.ErrInvalidCredential) {
			t.Errorf("an exchanged token without delegates: %v, want ErrInvalidCredential", err)
		}
	}
}

func TestNewVerifier_DelegateConfig(t *testing.T) {
	base := oidc.Config{Issuer: "https://idp.example", Audience: "serendip"}
	tests := []struct {
		name string
		cfg  func(c oidc.Config) oidc.Config
		ok   bool
	}{
		{"a delegate", func(c oidc.Config) oidc.Config { c.DelegateClients = []string{"serendip-mcp"}; return c }, true},
		{"the audience as a delegate", func(c oidc.Config) oidc.Config { c.DelegateClients = []string{"serendip"}; return c }, false},
		{"an empty delegate", func(c oidc.Config) oidc.Config { c.DelegateClients = []string{""}; return c }, false},
		{"a delegate with a slash", func(c oidc.Config) oidc.Config { c.DelegateClients = []string{"a/b"}; return c }, false},
		{"delegates on an agent issuer", func(c oidc.Config) oidc.Config {
			c.Kind, c.IssuerAlias, c.DelegateClients = identity.KindAgent, "am-dev", []string{"x"}
			return c
		}, false},
	}
	for _, tt := range tests {
		if _, err := oidc.NewVerifier(tt.cfg(base)); (err == nil) != tt.ok {
			t.Errorf("%s: err = %v, want ok=%v", tt.name, err, tt.ok)
		}
	}
}
