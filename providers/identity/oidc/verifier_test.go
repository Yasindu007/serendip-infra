package oidc_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/providers/identity/oidc"
)

// testIssuer is a minimal OIDC issuer: discovery, a JWKS, and signing
// helpers for tokens it (or an attacker) produces.
type testIssuer struct {
	t      *testing.T
	srv    *httptest.Server
	rsa    *rsa.PrivateKey
	ec     *ecdsa.PrivateKey
	ed     ed25519.PrivateKey
	hits   atomic.Int32 // JWKS fetches
	down   atomic.Bool  // answer 503 to everything
	mu     sync.Mutex
	extras []map[string]string // extra JWKS entries (rotation tests)
}

func newIssuer(t *testing.T) *testIssuer {
	t.Helper()
	is := &testIssuer{t: t}
	var err error
	if is.rsa, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatal(err)
	}
	if is.ec, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		t.Fatal(err)
	}
	if _, is.ed, err = ed25519.GenerateKey(rand.Reader); err != nil {
		t.Fatal(err)
	}
	is.srv = httptest.NewServer(http.HandlerFunc(is.serve))
	t.Cleanup(is.srv.Close)
	return is
}

func (is *testIssuer) url() string { return is.srv.URL }

func (is *testIssuer) serve(w http.ResponseWriter, r *http.Request) {
	if is.down.Load() {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		json.NewEncoder(w).Encode(map[string]string{"issuer": is.url(), "jwks_uri": is.url() + "/jwks"})
	case "/jwks":
		is.hits.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"keys": is.jwks()})
	default:
		http.NotFound(w, r)
	}
}

func (is *testIssuer) jwks() []map[string]string {
	ecx, ecy := is.ec.PublicKey.X.FillBytes(make([]byte, 32)), is.ec.PublicKey.Y.FillBytes(make([]byte, 32))
	keys := []map[string]string{
		{"kty": "RSA", "kid": "rsa1", "alg": "RS256", "use": "sig", "n": enc(is.rsa.N.Bytes()), "e": enc(big.NewInt(int64(is.rsa.E)).Bytes())},
		// The same RSA key again with no alg pinned, so PSS can use it.
		{"kty": "RSA", "kid": "rsa-any", "n": enc(is.rsa.N.Bytes()), "e": enc(big.NewInt(int64(is.rsa.E)).Bytes())},
		{"kty": "EC", "kid": "ec1", "crv": "P-256", "x": enc(ecx), "y": enc(ecy)},
		{"kty": "OKP", "kid": "ed1", "crv": "Ed25519", "x": enc(is.ed.Public().(ed25519.PublicKey))},
		// Skipped, not fatal: an encryption key and an unknown type.
		{"kty": "RSA", "kid": "enc1", "use": "enc", "n": enc(is.rsa.N.Bytes()), "e": "AQAB"},
		{"kty": "oct", "kid": "sym1", "k": "c2VjcmV0"},
	}
	is.mu.Lock()
	defer is.mu.Unlock()
	return append(keys, is.extras...)
}

func enc(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// sign produces a JWS for claims, signed per alg with the issuer's key of
// that type (kid as given, even when it doesn't match -- for attack cases).
func (is *testIssuer) sign(alg, kid string, claims map[string]any) string {
	is.t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid, "typ": "at+jwt"})
	p, _ := json.Marshal(claims)
	input := enc(h) + "." + enc(p)
	return input + "." + enc(signWith(is.t, alg, input, is.rsa, is.ec, is.ed))
}

func signWith(t *testing.T, alg, input string, rk *rsa.PrivateKey, ek *ecdsa.PrivateKey, edk ed25519.PrivateKey) []byte {
	t.Helper()
	sum := func(h crypto.Hash) []byte { hh := h.New(); hh.Write([]byte(input)); return hh.Sum(nil) }
	var sig []byte
	var err error
	switch alg {
	case "RS256":
		sig, err = rsa.SignPKCS1v15(rand.Reader, rk, crypto.SHA256, sum(crypto.SHA256))
	case "PS256":
		sig, err = rsa.SignPSS(rand.Reader, rk, crypto.SHA256, sum(crypto.SHA256), &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	case "ES256":
		var r, s *big.Int
		r, s, err = ecdsa.Sign(rand.Reader, ek, sum(crypto.SHA256))
		if err == nil {
			sig = append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
		}
	case "EdDSA":
		sig = ed25519.Sign(edk, []byte(input))
	case "HS256":
		// The classic confusion attack: HMAC keyed with the public key.
		m := hmac.New(sha256.New, rk.N.Bytes())
		m.Write([]byte(input))
		sig = m.Sum(nil)
	case "none":
		sig = nil
	default:
		t.Fatalf("test can't sign %s", alg)
	}
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func (is *testIssuer) claims(now time.Time, overrides map[string]any) map[string]any {
	c := map[string]any{
		"iss": is.url(), "sub": "0190-0030", "aud": "serendip", "client_id": "serendip",
		"username": "admin", "email": "admin@example.com",
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Hour).Unix(),
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

func newVerifier(t *testing.T, cfg oidc.Config) *oidc.Verifier {
	t.Helper()
	v, err := oidc.NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func TestVerifier_Authenticate(t *testing.T) {
	is := newIssuer(t)
	now := time.Now()
	v := newVerifier(t, oidc.Config{Issuer: is.url(), Audience: "serendip", Now: func() time.Time { return now }})
	if err := v.Warm(t.Context()); err != nil {
		t.Fatalf("Warm: %v", err)
	}

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	forged := func() string {
		h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "rsa1"})
		p, _ := json.Marshal(is.claims(now, nil))
		in := enc(h) + "." + enc(p)
		return in + "." + enc(signWith(t, "RS256", in, other, nil, nil))
	}
	tampered := func() string {
		tok := is.sign("RS256", "rsa1", is.claims(now, nil))
		parts := strings.Split(tok, ".")
		p, _ := json.Marshal(is.claims(now, map[string]any{"username": "root"}))
		return parts[0] + "." + enc(p) + "." + parts[2]
	}

	tests := []struct {
		name     string
		token    string
		wantName string // "" means rejected
	}{
		{"RS256", is.sign("RS256", "rsa1", is.claims(now, nil)), "admin"},
		{"PS256", is.sign("PS256", "rsa-any", is.claims(now, nil)), "admin"},
		{"ES256", is.sign("ES256", "ec1", is.claims(now, nil)), "admin"},
		{"EdDSA", is.sign("EdDSA", "ed1", is.claims(now, nil)), "admin"},
		{"no kid: tries keys of the right type", is.sign("ES256", "", is.claims(now, nil)), "admin"},
		{"aud as array", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"aud": []string{"other", "serendip"}})), "admin"},
		{"name falls back to preferred_username", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"username": nil, "preferred_username": "pu"})), "pu"},
		{"name falls back to email", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"username": nil})), "admin@example.com"},
		{"name falls back to sub", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"username": nil, "email": nil})), "0190-0030"},
		{"control characters dropped from the name", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"username": "ad\nmin\u0007"})), "admin"},
		{"name capped at 128 runes", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"username": strings.Repeat("a", 300)})), strings.Repeat("a", 128)},
		{"expired within leeway", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"exp": now.Add(-10 * time.Second).Unix()})), "admin"},
		{"nbf within leeway", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"nbf": now.Add(10 * time.Second).Unix()})), "admin"},

		{"expired", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"exp": now.Add(-time.Minute).Unix()})), ""},
		{"no exp", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"exp": nil})), ""},
		{"nbf in the future", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"nbf": now.Add(time.Minute).Unix()})), ""},
		{"iat in the future", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"iat": now.Add(time.Minute).Unix()})), ""},
		{"wrong issuer", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"iss": "http://evil.example"})), ""},
		{"wrong audience", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"aud": "other"})), ""},
		{"no client_id (an ID token)", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"client_id": nil})), ""},
		{"no sub", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"sub": nil})), ""},
		{"sub of only control characters, no other name", is.sign("RS256", "rsa1", is.claims(now, map[string]any{"sub": "\u0007", "username": nil, "email": nil})), ""},
		{"signed by a different key", forged(), ""},
		{"tampered payload", tampered(), ""},
		{"alg none", is.sign("none", "rsa1", is.claims(now, nil)), ""},
		{"HS256 keyed with the public key", is.sign("HS256", "rsa1", is.claims(now, nil)), ""},
		{"PS256 with a key pinned to RS256", is.sign("PS256", "rsa1", is.claims(now, nil)), ""},
		{"RS256 naming an EC key", is.sign("RS256", "ec1", is.claims(now, nil)), ""},
		{"ES256 naming an RSA key", is.sign("ES256", "rsa1", is.claims(now, nil)), ""},
		{"encryption key is not a signing key", is.sign("RS256", "enc1", is.claims(now, nil)), ""},
		{"garbage", "not-a-token", ""},
		{"two segments", "a.b", ""},
		{"bad base64", "!!.!!.!!", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := v.Authenticate(t.Context(), tt.token)
			if tt.wantName == "" {
				if !errors.Is(err, identity.ErrInvalidCredential) {
					t.Fatalf("Authenticate = (%+v, %v), want ErrInvalidCredential", p, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Authenticate: %v", err)
			}
			if p.Name != tt.wantName || p.Subject != "0190-0030" {
				t.Errorf("Principal = %+v, want Name %q, Subject 0190-0030", p, tt.wantName)
			}
		})
	}
}

func TestVerifier_EmptySignatureRejected(t *testing.T) {
	is := newIssuer(t)
	v := newVerifier(t, oidc.Config{Issuer: is.url(), Audience: "serendip"})
	tok := is.sign("RS256", "rsa1", is.claims(time.Now(), nil))
	tok = tok[:strings.LastIndex(tok, ".")+1]
	if _, err := v.Authenticate(t.Context(), tok); !errors.Is(err, identity.ErrInvalidCredential) {
		t.Fatalf("err = %v, want ErrInvalidCredential", err)
	}
}

// Provider neutrality (spec.md acceptance 8): a second, unrelated issuer,
// reached by discovery alone, verifies with configuration only -- and
// neither verifier accepts the other's tokens.
func TestVerifier_SecondIssuerConfigurationOnly(t *testing.T) {
	a, b := newIssuer(t), newIssuer(t)
	va := newVerifier(t, oidc.Config{Issuer: a.url(), Audience: "serendip"})
	vb := newVerifier(t, oidc.Config{Issuer: b.url(), Audience: "console"})
	now := time.Now()

	if _, err := vb.Authenticate(t.Context(), b.sign("ES256", "ec1", b.claims(now, map[string]any{"aud": "console"}))); err != nil {
		t.Fatalf("second issuer's own token: %v", err)
	}
	if _, err := va.Authenticate(t.Context(), a.sign("EdDSA", "ed1", a.claims(now, nil))); err != nil {
		t.Fatalf("first issuer's own token: %v", err)
	}
	// b's key signing a token that claims to be from a.
	cross := b.sign("RS256", "rsa1", a.claims(now, nil))
	if _, err := va.Authenticate(t.Context(), cross); !errors.Is(err, identity.ErrInvalidCredential) {
		t.Errorf("cross-issuer token: err = %v, want ErrInvalidCredential", err)
	}
}

// The JWKS override (the cluster-internal route) is used instead of
// discovery; iss is still checked against the configured issuer.
func TestVerifier_JWKSOverrideSkipsDiscovery(t *testing.T) {
	is := newIssuer(t)
	v := newVerifier(t, oidc.Config{Issuer: "http://identity.localhost:30080", Audience: "serendip", JWKSURL: is.url() + "/jwks"})
	now := time.Now()
	ok := is.sign("RS256", "rsa1", is.claims(now, map[string]any{"iss": "http://identity.localhost:30080"}))
	if _, err := v.Authenticate(t.Context(), ok); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := v.Authenticate(t.Context(), is.sign("RS256", "rsa1", is.claims(now, nil))); !errors.Is(err, identity.ErrInvalidCredential) {
		t.Errorf("token with the JWKS host as issuer: err = %v, want ErrInvalidCredential", err)
	}
}

func TestVerifier_DiscoveryIssuerMismatchFails(t *testing.T) {
	is := newIssuer(t)
	v := newVerifier(t, oidc.Config{Issuer: is.url() + "/other", Audience: "serendip"})
	if err := v.Warm(t.Context()); err == nil {
		t.Fatal("Warm succeeded although discovery names a different issuer")
	}
}

// Cached keys keep verifying while the provider is down; with nothing
// cached, an unreachable provider means "not ready", not "invalid".
func TestVerifier_Outages(t *testing.T) {
	is := newIssuer(t)
	now := time.Now()
	tok := is.sign("RS256", "rsa1", is.claims(now, nil))

	cold := newVerifier(t, oidc.Config{Issuer: is.url(), Audience: "serendip"})
	is.down.Store(true)
	if _, err := cold.Authenticate(t.Context(), tok); !errors.Is(err, reconcile.ErrProviderNotReady) {
		t.Fatalf("cold verifier, provider down: err = %v, want ErrProviderNotReady", err)
	}
	if _, err := cold.Authenticate(t.Context(), tok); !errors.Is(err, reconcile.ErrProviderNotReady) {
		t.Fatalf("cold verifier, rate-limited retry: err = %v, want ErrProviderNotReady", err)
	}

	is.down.Store(false)
	warm := newVerifier(t, oidc.Config{Issuer: is.url(), Audience: "serendip"})
	if err := warm.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	is.down.Store(true)
	if _, err := warm.Authenticate(t.Context(), tok); err != nil {
		t.Fatalf("warm verifier, provider down: %v", err)
	}
}

// An unknown kid triggers a fetch at most once per RefreshInterval, and a
// rotated-in key is picked up once the interval has passed.
func TestVerifier_UnknownKidRefreshIsRateLimited(t *testing.T) {
	is := newIssuer(t)
	clock := time.Now()
	v := newVerifier(t, oidc.Config{Issuer: is.url(), Audience: "serendip", RefreshInterval: time.Minute,
		Now: func() time.Time { return clock }})
	if err := v.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}

	newKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	h, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": "ec2"})
	p, _ := json.Marshal(is.claims(clock, nil))
	in := enc(h) + "." + enc(p)
	rotated := in + "." + enc(signWith(t, "ES256", in, nil, newKey, nil))

	for range 5 {
		if _, err := v.Authenticate(t.Context(), rotated); !errors.Is(err, identity.ErrInvalidCredential) {
			t.Fatalf("unknown kid: err = %v, want ErrInvalidCredential", err)
		}
	}
	if got := is.hits.Load(); got != 1 {
		t.Fatalf("JWKS fetches = %d after 5 unknown-kid tokens inside the interval, want 1 (the Warm)", got)
	}

	is.mu.Lock()
	is.extras = append(is.extras, map[string]string{"kty": "EC", "kid": "ec2", "crv": "P-256",
		"x": enc(newKey.PublicKey.X.FillBytes(make([]byte, 32))), "y": enc(newKey.PublicKey.Y.FillBytes(make([]byte, 32)))})
	is.mu.Unlock()
	clock = clock.Add(2 * time.Minute)
	if _, err := v.Authenticate(t.Context(), rotated); err != nil {
		t.Fatalf("rotated key after the interval: %v", err)
	}
	if got := is.hits.Load(); got != 2 {
		t.Errorf("JWKS fetches = %d, want 2", got)
	}
}

func TestNewVerifier_RequiresIssuerAndAudience(t *testing.T) {
	if _, err := oidc.NewVerifier(oidc.Config{Audience: "a"}); err == nil {
		t.Error("no issuer: want error")
	}
	if _, err := oidc.NewVerifier(oidc.Config{Issuer: "http://x"}); err == nil {
		t.Error("no audience: want error")
	}
}

// Concurrent verification and refreshes are race-free (run with -race).
func TestVerifier_Concurrent(t *testing.T) {
	is := newIssuer(t)
	v := newVerifier(t, oidc.Config{Issuer: is.url(), Audience: "serendip", RefreshInterval: time.Millisecond})
	now := time.Now()
	good := is.sign("RS256", "rsa1", is.claims(now, nil))
	unknown := is.sign("RS256", "nope", is.claims(now, nil))
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok := good
			if i%2 == 0 {
				tok = unknown
			}
			_, _ = v.Authenticate(context.Background(), tok)
		}()
	}
	wg.Wait()
	if _, err := v.Authenticate(t.Context(), good); err != nil {
		t.Fatalf("after concurrent use: %v", err)
	}
}
