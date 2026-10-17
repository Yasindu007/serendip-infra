package oidc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
)

// Config configures a Verifier. Issuer and Audience are required.
type Config struct {
	// Issuer is the exact "iss" every accepted token carries, and where
	// discovery is read from when JWKSURL is empty.
	Issuer string
	// Audience must appear in a token's "aud" (string or array).
	Audience string
	// JWKSURL, when set, is fetched instead of discovering "jwks_uri" from
	// the issuer -- e.g. a cluster-internal route to the same keys. The
	// "iss" check still applies; only where keys come from changes.
	JWKSURL string
	// HTTPClient fetches discovery and keys. Defaults to a client with a
	// 10 s timeout.
	HTTPClient *http.Client
	// Leeway tolerates clock skew on exp/nbf/iat. Defaults to 30 s.
	Leeway time.Duration
	// RefreshInterval is the least time between two key fetches, so tokens
	// with made-up key IDs can't make the verifier hammer the provider.
	// Defaults to 30 s.
	RefreshInterval time.Duration
	// Now is the clock; defaults to time.Now. For tests.
	Now func() time.Time
}

// Verifier is an identity.Authenticator for JWT access tokens. Safe for
// concurrent use.
type Verifier struct {
	cfg Config

	fetchMu sync.Mutex // serializes key fetches

	mu          sync.Mutex
	keys        []jwk
	lastAttempt time.Time // last key fetch, successful or not
}

var _ identity.Authenticator = (*Verifier)(nil)

// NewVerifier validates cfg and applies defaults. It does no I/O.
func NewVerifier(cfg Config) (*Verifier, error) {
	cfg.Issuer = strings.TrimSpace(cfg.Issuer)
	if cfg.Issuer == "" {
		return nil, errors.New("oidc: issuer is required")
	}
	if cfg.Audience == "" {
		return nil, errors.New("oidc: audience is required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = 30 * time.Second
	}
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = 30 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Verifier{cfg: cfg}, nil
}

// Warm fetches the issuer's keys once, regardless of RefreshInterval, and
// reports whether that worked. Setup calls it so "ready" means tokens can
// actually be verified.
func (v *Verifier) Warm(ctx context.Context) error {
	v.fetchMu.Lock()
	defer v.fetchMu.Unlock()
	return v.fetch(ctx)
}

// Authenticate verifies a JWT access token: its signature against the
// issuer's published keys, then iss, aud, exp/nbf/iat and the presence of
// client_id and sub. Every rejection wraps identity.ErrInvalidCredential.
// When the right key isn't cached and can't be fetched, it returns
// reconcile.ErrProviderNotReady: the token may well be valid.
func (v *Verifier) Authenticate(ctx context.Context, raw string) (identity.Principal, error) {
	t, err := parseToken(raw)
	if err != nil {
		return identity.Principal{}, invalid(err.Error())
	}
	alg, ok := algorithms[t.header.Alg]
	if !ok {
		return identity.Principal{}, invalid(fmt.Sprintf("algorithm %q not accepted", t.header.Alg))
	}
	if err := v.verify(ctx, t, alg); err != nil {
		return identity.Principal{}, err
	}
	return v.checkClaims(t.payload)
}

// verify finds the key t names and checks its signature, fetching keys
// once if the key isn't known yet (a rotation, or nothing cached).
func (v *Verifier) verify(ctx context.Context, t token, alg algorithm) error {
	candidates := v.candidates(t.header.Kid, alg)
	if len(candidates) == 0 {
		if err := v.refresh(ctx); err != nil {
			return err
		}
		candidates = v.candidates(t.header.Kid, alg)
		if len(candidates) == 0 {
			return invalid(fmt.Sprintf("no %s key with kid %q", alg.kty, t.header.Kid))
		}
	}
	var lastErr error
	for _, k := range candidates {
		if lastErr = verifySignature(t, alg, k); lastErr == nil {
			return nil
		}
	}
	return invalid("signature: " + lastErr.Error())
}

// candidates returns the cached keys that could have signed a token with
// this kid and algorithm: the key with that kid, or -- for a token with no
// kid -- every key of the right type and curve.
func (v *Verifier) candidates(kid string, alg algorithm) []jwk {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []jwk
	for _, k := range v.keys {
		if kid != "" && k.kid != kid {
			continue
		}
		if k.kty != alg.kty || (alg.curve != "" && k.crv != alg.curve) {
			continue
		}
		out = append(out, k)
	}
	return out
}

// refresh fetches keys unless the last attempt was under RefreshInterval
// ago. With nothing cached, any failure to fetch means "not ready".
func (v *Verifier) refresh(ctx context.Context) error {
	v.fetchMu.Lock()
	defer v.fetchMu.Unlock()
	v.mu.Lock()
	recent := !v.lastAttempt.IsZero() && v.cfg.Now().Sub(v.lastAttempt) < v.cfg.RefreshInterval
	empty := len(v.keys) == 0
	v.mu.Unlock()
	if recent {
		if empty {
			return fmt.Errorf("%w: identity provider keys unavailable", reconcile.ErrProviderNotReady)
		}
		return nil // keys are fresh; the kid is simply unknown
	}
	if err := v.fetch(ctx); err != nil {
		return fmt.Errorf("%w: %v", reconcile.ErrProviderNotReady, err)
	}
	return nil
}

// fetch replaces the cached keys with the issuer's current JWKS. A failed
// fetch keeps the old keys, so verification survives a provider outage.
// Callers hold fetchMu.
func (v *Verifier) fetch(ctx context.Context) error {
	v.mu.Lock()
	v.lastAttempt = v.cfg.Now()
	v.mu.Unlock()

	url := v.cfg.JWKSURL
	if url == "" {
		var err error
		if url, err = v.discoverJWKS(ctx); err != nil {
			return err
		}
	}
	body, err := v.get(ctx, url)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	keys, err := parseJWKS(body)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return errors.New("JWKS has no usable signing keys")
	}
	v.mu.Lock()
	v.keys = keys
	v.mu.Unlock()
	return nil
}

// discoverJWKS reads jwks_uri from the issuer's OIDC discovery document,
// which must name the same issuer (OIDC Discovery 1.0 section 4.3).
func (v *Verifier) discoverJWKS(ctx context.Context) (string, error) {
	body, err := v.get(ctx, strings.TrimSuffix(v.cfg.Issuer, "/")+"/.well-known/openid-configuration")
	if err != nil {
		return "", fmt.Errorf("discovery: %w", err)
	}
	var doc struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("discovery: %w", err)
	}
	if doc.Issuer != v.cfg.Issuer {
		return "", fmt.Errorf("discovery: issuer %q, want %q", doc.Issuer, v.cfg.Issuer)
	}
	if doc.JWKSURI == "" {
		return "", errors.New("discovery: no jwks_uri")
	}
	return doc.JWKSURI, nil
}

// maxDocSize bounds discovery and JWKS responses.
const maxDocSize = 1 << 20

func (v *Verifier) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := v.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDocSize))
}

// claims is what the verifier reads from a token. Numeric dates are
// json.Number so both integer and fractional seconds decode.
type claims struct {
	Iss               string          `json:"iss"`
	Sub               string          `json:"sub"`
	Aud               json.RawMessage `json:"aud"`
	Exp               *json.Number    `json:"exp"`
	Nbf               *json.Number    `json:"nbf"`
	Iat               *json.Number    `json:"iat"`
	ClientID          string          `json:"client_id"`
	Username          string          `json:"username"`
	PreferredUsername string          `json:"preferred_username"`
	Email             string          `json:"email"`
}

func (v *Verifier) checkClaims(payload []byte) (identity.Principal, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var c claims
	if err := dec.Decode(&c); err != nil {
		return identity.Principal{}, invalid("claims: " + err.Error())
	}
	if c.Iss != v.cfg.Issuer {
		return identity.Principal{}, invalid(fmt.Sprintf("issuer %q", c.Iss))
	}
	if !audienceContains(c.Aud, v.cfg.Audience) {
		return identity.Principal{}, invalid("audience")
	}
	// client_id is what separates an access token from an ID token issued
	// to the same client (plan.md decision 7): only the access token is
	// meant for a resource server.
	if c.ClientID == "" {
		return identity.Principal{}, invalid("no client_id (not an access token)")
	}
	if c.Sub == "" || displayName(c) == "" {
		return identity.Principal{}, invalid("no usable sub")
	}
	now := v.cfg.Now()
	exp, ok := unixTime(c.Exp)
	if !ok {
		return identity.Principal{}, invalid("missing or malformed exp")
	}
	if !now.Before(exp.Add(v.cfg.Leeway)) {
		return identity.Principal{}, invalid("expired")
	}
	for name, n := range map[string]*json.Number{"nbf": c.Nbf, "iat": c.Iat} {
		if n == nil {
			continue
		}
		at, ok := unixTime(n)
		if !ok {
			return identity.Principal{}, invalid("malformed " + name)
		}
		if at.After(now.Add(v.cfg.Leeway)) {
			return identity.Principal{}, invalid(name + " is in the future")
		}
	}
	return identity.Principal{Subject: c.Sub, Name: displayName(c)}, nil
}

// maxNameRunes bounds the display name recorded as the audit actor.
const maxNameRunes = 128

// displayName is the human-readable name for the audit actor: the most
// specific of the usual claims, falling back to the opaque subject. Control
// characters are dropped and the length capped, since it lands verbatim in
// audit rows and logs.
func displayName(c claims) string {
	for _, s := range []string{c.Username, c.PreferredUsername, c.Email, c.Sub} {
		if s = clean(s); s != "" {
			return s
		}
	}
	return ""
}

func clean(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		if n == maxNameRunes {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

func audienceContains(raw json.RawMessage, want string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == want
	}
	var many []string
	return json.Unmarshal(raw, &many) == nil && slices.Contains(many, want)
}

func unixTime(n *json.Number) (time.Time, bool) {
	if n == nil {
		return time.Time{}, false
	}
	f, err := n.Float64()
	if err != nil {
		return time.Time{}, false
	}
	sec := int64(f)
	return time.Unix(sec, int64((f-float64(sec))*1e9)), true
}

func invalid(reason string) error {
	return fmt.Errorf("%w: %s", identity.ErrInvalidCredential, reason)
}
