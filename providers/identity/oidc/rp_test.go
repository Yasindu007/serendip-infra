package oidc_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/providers/identity/oidc"
)

// rpServer is the provider's back channel: discovery naming the public
// issuer, plus token, revocation and end-session endpoints.
type rpServer struct {
	is  *testIssuer
	srv *httptest.Server

	mu          sync.Mutex
	issuer      string // issuer advertised by discovery
	noRevoke    bool
	noEndSess   bool
	tokenStatus int
	tokenBody   map[string]any
	revokeCode  int
	lastForm    url.Values
	lastPath    string
}

func newRPServer(t *testing.T, is *testIssuer) *rpServer {
	t.Helper()
	s := &rpServer{is: is, issuer: is.url(), tokenStatus: http.StatusOK, revokeCode: http.StatusOK,
		tokenBody: map[string]any{"access_token": "AT", "refresh_token": "RT2", "id_token": "IDT", "expires_in": 3600, "token_type": "Bearer"}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *rpServer) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pub := s.is.url()
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		doc := map[string]string{"issuer": s.issuer, "authorization_endpoint": pub + "/oauth2/authorize",
			"token_endpoint": pub + "/oauth2/token", "jwks_uri": pub + "/jwks"}
		if !s.noRevoke {
			doc["revocation_endpoint"] = pub + "/oauth2/revoke"
		}
		if !s.noEndSess {
			doc["end_session_endpoint"] = pub + "/oauth2/logout?ui=1"
		}
		json.NewEncoder(w).Encode(doc)
	case "/oauth2/token":
		r.ParseForm()
		s.lastForm, s.lastPath = r.PostForm, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.tokenStatus)
		json.NewEncoder(w).Encode(s.tokenBody)
	case "/oauth2/revoke":
		r.ParseForm()
		s.lastForm, s.lastPath = r.PostForm, r.URL.Path
		w.WriteHeader(s.revokeCode)
	default:
		http.NotFound(w, r)
	}
}

func (s *rpServer) set(f func(s *rpServer)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

func newRP(t *testing.T, is *testIssuer, s *rpServer, now time.Time) *oidc.RelyingParty {
	t.Helper()
	v := newVerifier(t, oidc.Config{Issuer: is.url(), Audience: "serendip", Now: func() time.Time { return now }})
	rp, err := oidc.NewRelyingParty(oidc.RPConfig{
		Issuer: is.url(), BackChannelURL: s.srv.URL, ClientID: "serendip",
		RedirectURL: "http://localhost:8080/auth/callback", PostLogoutRedirectURL: "http://localhost:8080/",
		Verifier: v, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRelyingParty: %v", err)
	}
	return rp
}

func TestRelyingParty_AuthCodeURL(t *testing.T) {
	is := newIssuer(t)
	s := newRPServer(t, is)
	rp := newRP(t, is, s, time.Now())

	got, err := rp.AuthCodeURL(context.Background(), "st", "no", "ch")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(got)
	if want := is.url() + "/oauth2/authorize"; u.Scheme+"://"+u.Host+u.Path != want {
		t.Errorf("authorize endpoint = %s, want the public %s", u.Scheme+"://"+u.Host+u.Path, want)
	}
	q := u.Query()
	for k, want := range map[string]string{"response_type": "code", "client_id": "serendip", "redirect_uri": "http://localhost:8080/auth/callback",
		"scope": "openid", "state": "st", "nonce": "no", "code_challenge": "ch", "code_challenge_method": "S256"} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestRelyingParty_AuthCodeURLRequiresDefences(t *testing.T) {
	is := newIssuer(t)
	rp := newRP(t, is, newRPServer(t, is), time.Now())
	for _, args := range [][3]string{{"", "n", "c"}, {"s", "", "c"}, {"s", "n", ""}} {
		if _, err := rp.AuthCodeURL(context.Background(), args[0], args[1], args[2]); err == nil {
			t.Errorf("AuthCodeURL%q: no error", args)
		}
	}
}

func TestRelyingParty_ExchangeAndRefresh(t *testing.T) {
	is := newIssuer(t)
	s := newRPServer(t, is)
	now := time.Unix(1_800_000_000, 0)
	rp := newRP(t, is, s, now)
	ctx := context.Background()

	tok, err := rp.Exchange(ctx, "the-code", "the-verifier")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "AT" || tok.RefreshToken != "RT2" || tok.IDToken != "IDT" || !tok.Expiry.Equal(now.Add(time.Hour)) {
		t.Errorf("tokens = %+v", tok)
	}
	if s.lastPath != "/oauth2/token" {
		t.Errorf("token call went to %q on the back channel", s.lastPath)
	}
	for k, want := range map[string]string{"grant_type": "authorization_code", "code": "the-code", "code_verifier": "the-verifier",
		"redirect_uri": "http://localhost:8080/auth/callback", "client_id": "serendip"} {
		if s.lastForm.Get(k) != want {
			t.Errorf("form %s = %q, want %q", k, s.lastForm.Get(k), want)
		}
	}

	if _, err := rp.Refresh(ctx, "RT1"); err != nil {
		t.Fatal(err)
	}
	if s.lastForm.Get("grant_type") != "refresh_token" || s.lastForm.Get("refresh_token") != "RT1" || s.lastForm.Get("client_id") != "serendip" {
		t.Errorf("refresh form = %v", s.lastForm)
	}
}

func TestRelyingParty_TokenErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   map[string]any
		want   error
	}{
		{"grant refused", http.StatusBadRequest, map[string]any{"error": "invalid_grant", "error_description": "code the-code verifier the-verifier"}, identity.ErrInvalidCredential},
		{"client refused", http.StatusUnauthorized, map[string]any{"error": "invalid_client"}, identity.ErrInvalidCredential},
		{"provider error", http.StatusBadGateway, map[string]any{}, reconcile.ErrProviderNotReady},
		{"rate limited", http.StatusTooManyRequests, map[string]any{}, reconcile.ErrProviderNotReady},
		{"timed out", http.StatusRequestTimeout, map[string]any{}, reconcile.ErrProviderNotReady},
		{"200 without token", http.StatusOK, map[string]any{"token_type": "Bearer"}, identity.ErrInvalidCredential},
		{"wrong token type", http.StatusOK, map[string]any{"access_token": "AT", "token_type": "mac"}, identity.ErrInvalidCredential},
		{"hostile error code", http.StatusBadRequest, map[string]any{"error": "x\"the-code\""}, identity.ErrInvalidCredential},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			is := newIssuer(t)
			s := newRPServer(t, is)
			s.set(func(s *rpServer) { s.tokenStatus, s.tokenBody = tc.status, tc.body })
			rp := newRP(t, is, s, time.Now())
			_, err := rp.Exchange(context.Background(), "the-code", "the-verifier")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if msg := err.Error(); strings.Contains(msg, "the-code") || strings.Contains(msg, "the-verifier") {
				t.Errorf("error leaks request values: %q", msg)
			}
		})
	}
}

func TestRelyingParty_Unreachable(t *testing.T) {
	is := newIssuer(t)
	s := newRPServer(t, is)
	rp := newRP(t, is, s, time.Now())
	s.srv.Close()
	if _, err := rp.Exchange(context.Background(), "c", "v"); !errors.Is(err, reconcile.ErrProviderNotReady) {
		t.Errorf("err = %v, want not ready", err)
	}
}

func TestRelyingParty_DiscoveryFailureNotCached(t *testing.T) {
	is := newIssuer(t)
	s := newRPServer(t, is)
	s.set(func(s *rpServer) { s.issuer = "http://someone-else" })
	rp := newRP(t, is, s, time.Now())
	ctx := context.Background()
	if _, err := rp.AuthCodeURL(ctx, "s", "n", "c"); !errors.Is(err, reconcile.ErrProviderNotReady) {
		t.Fatalf("issuer mismatch: err = %v, want not ready", err)
	}
	s.set(func(s *rpServer) { s.issuer = is.url() })
	if _, err := rp.AuthCodeURL(ctx, "s", "n", "c"); err != nil {
		t.Errorf("after fix: %v", err)
	}
}

func TestRelyingParty_Revoke(t *testing.T) {
	is := newIssuer(t)
	s := newRPServer(t, is)
	rp := newRP(t, is, s, time.Now())
	ctx := context.Background()

	if err := rp.Revoke(ctx, "RT"); err != nil {
		t.Fatal(err)
	}
	if s.lastPath != "/oauth2/revoke" || s.lastForm.Get("token") != "RT" || s.lastForm.Get("client_id") != "serendip" || s.lastForm.Get("token_type_hint") != "refresh_token" {
		t.Errorf("revoke call %s %v", s.lastPath, s.lastForm)
	}
	s.set(func(s *rpServer) { s.revokeCode = http.StatusBadRequest })
	if err := rp.Revoke(ctx, "RT"); !errors.Is(err, identity.ErrInvalidCredential) {
		t.Errorf("rejected revoke: err = %v, want invalid credential", err)
	}
	s.set(func(s *rpServer) { s.revokeCode = http.StatusServiceUnavailable })
	if err := rp.Revoke(ctx, "RT"); !errors.Is(err, reconcile.ErrProviderNotReady) {
		t.Errorf("provider down: err = %v, want not ready", err)
	}

	is2 := newIssuer(t)
	s2 := newRPServer(t, is2)
	s2.set(func(s *rpServer) { s.noRevoke = true })
	if err := newRP(t, is2, s2, time.Now()).Revoke(ctx, "RT"); !errors.Is(err, identity.ErrUnsupported) {
		t.Errorf("no endpoint: err = %v, want unsupported", err)
	}
}

func TestRelyingParty_EndSessionURL(t *testing.T) {
	is := newIssuer(t)
	s := newRPServer(t, is)
	rp := newRP(t, is, s, time.Now())
	got, err := rp.EndSessionURL(context.Background(), "IDT")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(got)
	if u.Host != strings.TrimPrefix(is.url(), "http://") || u.Path != "/oauth2/logout" {
		t.Errorf("end session = %s, want the public issuer's /oauth2/logout", got)
	}
	q := u.Query()
	if q.Get("ui") != "1" || q.Get("id_token_hint") != "IDT" || q.Get("client_id") != "serendip" || q.Get("post_logout_redirect_uri") != "http://localhost:8080/" {
		t.Errorf("query = %v", q)
	}

	is2 := newIssuer(t)
	s2 := newRPServer(t, is2)
	s2.set(func(s *rpServer) { s.noEndSess = true })
	if _, err := newRP(t, is2, s2, time.Now()).EndSessionURL(context.Background(), "IDT"); !errors.Is(err, identity.ErrUnsupported) {
		t.Errorf("no endpoint: err = %v, want unsupported", err)
	}
}

func TestRelyingParty_VerifyIDToken(t *testing.T) {
	is := newIssuer(t)
	s := newRPServer(t, is)
	now := time.Now()
	rp := newRP(t, is, s, now)
	id := func(over map[string]any) map[string]any {
		c := is.claims(now, map[string]any{"client_id": nil, "nonce": "n-1"})
		for k, v := range over {
			if v == nil {
				delete(c, k)
			} else {
				c[k] = v
			}
		}
		return c
	}
	cases := []struct {
		name  string
		token string
		nonce string
		ok    bool
	}{
		{"valid", is.sign("RS256", "rsa1", id(nil)), "n-1", true},
		{"valid EC", is.sign("ES256", "ec1", id(nil)), "n-1", true},
		{"wrong nonce", is.sign("RS256", "rsa1", id(nil)), "n-2", false},
		{"missing nonce", is.sign("RS256", "rsa1", id(map[string]any{"nonce": nil})), "n-1", false},
		{"other audience", is.sign("RS256", "rsa1", id(map[string]any{"aud": "other"})), "n-1", false},
		{"other issuer", is.sign("RS256", "rsa1", id(map[string]any{"iss": "http://evil"})), "n-1", false},
		{"expired", is.sign("RS256", "rsa1", id(map[string]any{"exp": now.Add(-time.Hour).Unix()})), "n-1", false},
		{"no sub", is.sign("RS256", "rsa1", id(map[string]any{"sub": nil})), "n-1", false},
		{"alg none", is.sign("none", "rsa1", id(nil)), "n-1", false},
		{"hmac confusion", is.sign("HS256", "rsa1", id(nil)), "n-1", false},
		{"unknown kid", is.sign("RS256", "nope", id(nil)), "n-1", false},
		{"garbage", "a.b.c", "n-1", false},
		{"empty expected nonce", is.sign("RS256", "rsa1", id(map[string]any{"nonce": nil})), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub, err := rp.VerifyIDToken(context.Background(), tc.token, tc.nonce)
			if tc.ok {
				if err != nil || sub != "0190-0030" {
					t.Errorf("sub, err = %q, %v", sub, err)
				}
				return
			}
			if !errors.Is(err, identity.ErrInvalidCredential) {
				t.Errorf("err = %v, want invalid credential", err)
			}
		})
	}
}

func TestNewRelyingParty_Validates(t *testing.T) {
	v, _ := oidc.NewVerifier(oidc.Config{Issuer: "http://i", Audience: "a"})
	base := oidc.RPConfig{Issuer: "http://i", ClientID: "c", RedirectURL: "http://r/cb", Verifier: v}
	for name, mut := range map[string]func(*oidc.RPConfig){
		"issuer":   func(c *oidc.RPConfig) { c.Issuer = " " },
		"client":   func(c *oidc.RPConfig) { c.ClientID = "" },
		"redirect": func(c *oidc.RPConfig) { c.RedirectURL = "" },
		"verifier": func(c *oidc.RPConfig) { c.Verifier = nil },
	} {
		cfg := base
		mut(&cfg)
		if _, err := oidc.NewRelyingParty(cfg); err == nil {
			t.Errorf("missing %s: no error", name)
		}
	}
	if _, err := oidc.NewRelyingParty(base); err != nil {
		t.Errorf("valid config: %v", err)
	}
}
