package oidc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

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
	// GroupsClaim names the access-token claim listing the person's group
	// names. Defaults to "groups". A token without it has no groups.
	GroupsClaim string
	// Kind is what an accepted token becomes. A person (the default) may
	// not present a client_credentials token: a machine is never a person.
	// An agent's token must say it is a client_credentials one, must carry
	// iat, must live at most MaxLifetime, and needs a short, printable sub;
	// its groups are ignored, since group names belong to their issuer
	// (specs/018-agent-manager-poc plan.md decisions 6 and 7).
	Kind identity.Kind
	// IssuerAlias names an agent issuer in principals and audit records.
	// Required for an agent issuer: lower-case letters, digits and "-", at
	// most 32.
	IssuerAlias string
	// MaxLifetime bounds an agent token's exp - iat, since the issuer can't
	// revoke tokens. Defaults to 1 h; at most 24 h.
	MaxLifetime time.Duration
	// DelegateClients are clients a person's token is exchanged for when
	// they act through an AI assistant (RFC 8693, spec 021 plan decision
	// 1). A token issued to one becomes the person with Via set to the
	// client, so the agent guardrails apply. It must come from a token
	// exchange, carry the client as its audience, carry iat and live at
	// most MaxDelegatedLifetime. A token exchanged for any other client is
	// refused. People's issuers only.
	DelegateClients []string
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
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	switch cfg.Kind {
	case identity.KindPerson:
		if cfg.IssuerAlias != "" || cfg.MaxLifetime != 0 {
			return nil, errors.New("oidc: an issuer alias and a maximum lifetime are for agent issuers")
		}
		for _, c := range cfg.DelegateClients {
			if c == "" || c == cfg.Audience || strings.ContainsFunc(c, func(r rune) bool { return r == '/' || unicode.IsSpace(r) || unicode.IsControl(r) }) {
				return nil, fmt.Errorf("oidc: delegate client %q: want a client ID other than the audience, with no spaces or slashes", c)
			}
		}
	case identity.KindAgent:
		if !ValidIssuerAlias(cfg.IssuerAlias) {
			return nil, fmt.Errorf("oidc: agent issuer alias %q: want 1-32 of a-z, 0-9 and -", cfg.IssuerAlias)
		}
		if cfg.MaxLifetime == 0 {
			cfg.MaxLifetime = DefaultAgentTokenLifetime
		}
		if cfg.MaxLifetime < 0 || cfg.MaxLifetime > MaxAgentTokenLifetime {
			return nil, fmt.Errorf("oidc: agent token lifetime %s: want at most %s", cfg.MaxLifetime, MaxAgentTokenLifetime)
		}
		if len(cfg.DelegateClients) > 0 {
			return nil, errors.New("oidc: delegate clients are for people's issuers")
		}
	default:
		return nil, fmt.Errorf("oidc: unknown principal kind %d", cfg.Kind)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Verifier{cfg: cfg}, nil
}

// Issuer is the issuer every accepted token carries.
func (v *Verifier) Issuer() string { return v.cfg.Issuer }

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

// verifyIDToken verifies an ID token issued to the configured audience:
// signature, iss, aud, exp/nbf/iat and, when nonce is non-empty, an exactly
// matching "nonce" claim. Unlike Authenticate it doesn't require client_id,
// which ID tokens don't carry. It returns the subject.
func (v *Verifier) verifyIDToken(ctx context.Context, raw, nonce string) (string, error) {
	t, err := parseToken(raw)
	if err != nil {
		return "", invalid(err.Error())
	}
	alg, ok := algorithms[t.header.Alg]
	if !ok {
		return "", invalid(fmt.Sprintf("algorithm %q not accepted", t.header.Alg))
	}
	if err := v.verify(ctx, t, alg); err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(t.payload))
	dec.UseNumber()
	var c claims
	if err := dec.Decode(&c); err != nil {
		return "", invalid("claims: " + err.Error())
	}
	if err := v.checkTimes(c); err != nil {
		return "", err
	}
	if c.Iss != v.cfg.Issuer {
		return "", invalid(fmt.Sprintf("issuer %q", c.Iss))
	}
	if !audienceContains(c.Aud, v.cfg.Audience) {
		return "", invalid("audience")
	}
	if c.Sub == "" {
		return "", invalid("no sub")
	}
	if nonce != "" && c.Nonce != nonce {
		return "", invalid("nonce mismatch")
	}
	return c.Sub, nil
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
		return fmt.Errorf("%w: %w", reconcile.ErrProviderNotReady, err)
	}
	return nil
}

// fetch replaces the cached keys with the issuer's current JWKS. A failed
// fetch keeps the old keys, so verification survives a provider outage.
// Callers hold fetchMu.
func (v *Verifier) fetch(ctx context.Context) error {
	// The fetch serves every waiting request, not just the caller, so the
	// caller going away must not abort it (and start the refresh lockout);
	// the HTTP client's own timeout still bounds it.
	ctx = context.WithoutCancel(ctx)
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
	GrantType         string          `json:"grant_type"`
	Username          string          `json:"username"`
	PreferredUsername string          `json:"preferred_username"`
	Email             string          `json:"email"`
	Nonce             string          `json:"nonce"`
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
	// A delegate client's token is meant for that client: the exchange sets
	// its audience to it (spec 021 T001 finding 1).
	delegate := slices.Contains(v.cfg.DelegateClients, c.ClientID)
	aud := v.cfg.Audience
	if delegate {
		aud = c.ClientID
	}
	if !audienceContains(c.Aud, aud) {
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
	if err := v.checkTimes(c); err != nil {
		return identity.Principal{}, err
	}
	if v.cfg.Kind == identity.KindAgent {
		return v.agent(c)
	}
	if c.GrantType == clientCredentials {
		return identity.Principal{}, invalid("a client_credentials token is never a person")
	}
	via, err := v.delegation(c, delegate)
	if err != nil {
		return identity.Principal{}, err
	}
	groups, err := v.groups(payload)
	if err != nil {
		return identity.Principal{}, err
	}
	return identity.Principal{Subject: c.Sub, Name: displayName(c), Groups: groups, Via: via}, nil
}

const tokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange"

// MaxDelegatedLifetime bounds a delegated token's exp - iat. Logging out
// stops new exchanges, so this is how long an assistant can outlive its
// person's session at most.
const MaxDelegatedLifetime = time.Hour

// delegation checks how a person's token was issued, and returns the
// client they act through: empty for the person themselves. Only a token
// exchange for a delegate client delegates; any other exchanged token is
// refused, and a delegate client's token must come from an exchange (a
// person signing in to it directly is not acting through an assistant).
func (v *Verifier) delegation(c claims, delegate bool) (string, error) {
	exchanged := c.GrantType == tokenExchange
	switch {
	case !delegate && !exchanged:
		return "", nil
	case !delegate:
		return "", invalid(fmt.Sprintf("a token exchanged for client %q, which isn't a delegate", c.ClientID))
	case !exchanged:
		return "", invalid(fmt.Sprintf("a token for delegate client %q must come from a token exchange, not %q", c.ClientID, c.GrantType))
	}
	iat, ok := unixTime(c.Iat)
	if !ok {
		return "", invalid("a delegated token needs iat")
	}
	exp, _ := unixTime(c.Exp) // checkTimes required it
	if life := exp.Sub(iat); life < 0 || life > MaxDelegatedLifetime+v.cfg.Leeway {
		return "", invalid(fmt.Sprintf("a delegated token lives %s, not within the %s maximum", life, MaxDelegatedLifetime))
	}
	return c.ClientID, nil
}

const clientCredentials = "client_credentials"

// maxAgentSubject bounds an agent's subject, which becomes its principal
// ID and lands verbatim in audit rows.
const maxAgentSubject = 256

// agent finishes checking an agent issuer's token: issued to a machine
// (grant_type client_credentials, required: without it a person's token
// from the same issuer would pass), with iat, within MaxLifetime, and a
// printable subject. Groups are never read.
func (v *Verifier) agent(c claims) (identity.Principal, error) {
	if c.GrantType != clientCredentials {
		return identity.Principal{}, invalid(fmt.Sprintf("an agent token must be client_credentials, not %q", c.GrantType))
	}
	if len(c.Sub) > maxAgentSubject || strings.IndexFunc(c.Sub, unicode.IsControl) >= 0 {
		return identity.Principal{}, invalid("an agent's sub must be printable and at most 256 bytes")
	}
	iat, ok := unixTime(c.Iat)
	if !ok {
		return identity.Principal{}, invalid("an agent token needs iat")
	}
	exp, _ := unixTime(c.Exp) // checkTimes required it
	if life := exp.Sub(iat); life < 0 || life > v.cfg.MaxLifetime+v.cfg.Leeway {
		return identity.Principal{}, invalid(fmt.Sprintf("lives %s, not within the %s maximum", life, v.cfg.MaxLifetime))
	}
	return identity.Principal{
		Kind:    identity.KindAgent,
		Issuer:  v.cfg.IssuerAlias,
		Subject: c.Sub,
		Name:    displayName(c),
	}, nil
}

// Agent token lifetimes (specs/018-agent-manager-poc plan.md decision 6).
const (
	DefaultAgentTokenLifetime = time.Hour
	MaxAgentTokenLifetime     = 24 * time.Hour
)

// ValidIssuerAlias reports whether s can name an agent issuer.
func ValidIssuerAlias(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// Bounds on the groups claim. A token past them is rejected rather than
// trimmed, so which groups apply never depends on an arbitrary cut.
const (
	maxGroups     = 100
	maxGroupRunes = 128
)

// groups reads the configured groups claim: absent or null means none; it
// must otherwise be an array of non-empty strings without control
// characters, within the bounds above. Duplicates are dropped.
func (v *Verifier) groups(payload []byte) ([]string, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(payload, &all); err != nil {
		return nil, invalid("claims: " + err.Error())
	}
	raw, ok := all[v.cfg.GroupsClaim]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, invalid("malformed " + v.cfg.GroupsClaim + " claim")
	}
	if len(names) > maxGroups {
		return nil, invalid(fmt.Sprintf("more than %d groups", maxGroups))
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" || utf8.RuneCountInString(n) > maxGroupRunes || strings.IndexFunc(n, unicode.IsControl) >= 0 {
			return nil, invalid("malformed group name")
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// checkTimes checks exp (required) and nbf/iat (optional) against the clock
// with the configured leeway.
func (v *Verifier) checkTimes(c claims) error {
	now := v.cfg.Now()
	exp, ok := unixTime(c.Exp)
	if !ok {
		return invalid("missing or malformed exp")
	}
	if !now.Before(exp.Add(v.cfg.Leeway)) {
		return invalid("expired")
	}
	for name, n := range map[string]*json.Number{"nbf": c.Nbf, "iat": c.Iat} {
		if n == nil {
			continue
		}
		at, ok := unixTime(n)
		if !ok {
			return invalid("malformed " + name)
		}
		if at.After(now.Add(v.cfg.Leeway)) {
			return invalid(name + " is in the future")
		}
	}
	return nil
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

// maxUnixSeconds bounds claim dates (about year 36812): anything larger, or
// negative, is malformed rather than converted, since float-to-int64
// conversion of out-of-range values is implementation-defined.
const maxUnixSeconds = 1 << 40

func unixTime(n *json.Number) (time.Time, bool) {
	if n == nil {
		return time.Time{}, false
	}
	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > maxUnixSeconds {
		return time.Time{}, false
	}
	sec := int64(f)
	return time.Unix(sec, int64((f-float64(sec))*1e9)), true
}

func invalid(reason string) error {
	return fmt.Errorf("%w: %s", identity.ErrInvalidCredential, reason)
}
