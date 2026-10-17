package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
)

// RPConfig configures a RelyingParty. Issuer, ClientID, RedirectURL and
// Verifier are required.
type RPConfig struct {
	// Issuer is the public issuer URL: what tokens carry and where the
	// browser is sent.
	Issuer string
	// BackChannelURL, when set, replaces Issuer as the base of every
	// server-side call (discovery, token, revocation) -- e.g. a
	// cluster-internal route to the same server. Endpoints discovery
	// returns under Issuer are rewritten onto it; the browser-facing ones
	// (authorize, end session) are not.
	BackChannelURL string
	// ClientID is the public client this server logs people in through.
	ClientID string
	// RedirectURL is the registered callback the provider returns codes to.
	RedirectURL string
	// PostLogoutRedirectURL is the registered page the provider returns the
	// browser to after ending its session. Empty means none is passed.
	PostLogoutRedirectURL string
	// Scope is the authorization request's scope. Defaults to "openid".
	Scope string
	// Verifier checks ID tokens; it must be configured for the same issuer
	// with ClientID as its audience, so both share one key cache.
	Verifier *Verifier
	// HTTPClient makes the back-channel calls. Defaults to a client with a
	// 10 s timeout.
	HTTPClient *http.Client
	// Now is the clock; defaults to time.Now. For tests.
	Now func() time.Time
}

// RelyingParty is an identity.LoginClient for any OIDC provider that
// publishes discovery: authorization code + PKCE (S256) for a public
// client. Safe for concurrent use.
type RelyingParty struct {
	cfg RPConfig

	mu  sync.Mutex
	doc *discovery // cached once read successfully
}

var _ identity.LoginClient = (*RelyingParty)(nil)

type discovery struct {
	Issuer           string `json:"issuer"`
	AuthorizationURL string `json:"authorization_endpoint"`
	TokenURL         string `json:"token_endpoint"`
	RevocationURL    string `json:"revocation_endpoint"`
	EndSessionURL    string `json:"end_session_endpoint"`
}

// NewRelyingParty validates cfg and applies defaults. It does no I/O.
func NewRelyingParty(cfg RPConfig) (*RelyingParty, error) {
	cfg.Issuer = strings.TrimSpace(cfg.Issuer)
	switch {
	case cfg.Issuer == "":
		return nil, errors.New("oidc: issuer is required")
	case cfg.ClientID == "":
		return nil, errors.New("oidc: client ID is required")
	case cfg.RedirectURL == "":
		return nil, errors.New("oidc: redirect URL is required")
	case cfg.Verifier == nil:
		return nil, errors.New("oidc: verifier is required")
	}
	if cfg.Scope == "" {
		cfg.Scope = "openid"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &RelyingParty{cfg: cfg}, nil
}

// AuthCodeURL implements identity.LoginClient.
func (rp *RelyingParty) AuthCodeURL(ctx context.Context, state, nonce, codeChallenge string) (string, error) {
	if state == "" || nonce == "" || codeChallenge == "" {
		// Each is a replay or injection defence; a caller that lost one
		// must fail loudly rather than log someone in without it.
		return "", errors.New("oidc: state, nonce and code challenge are all required")
	}
	doc, err := rp.discover(ctx)
	if err != nil {
		return "", err
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {rp.cfg.ClientID},
		"redirect_uri":          {rp.cfg.RedirectURL},
		"scope":                 {rp.cfg.Scope},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return withQuery(doc.AuthorizationURL, q)
}

// Exchange implements identity.LoginClient.
func (rp *RelyingParty) Exchange(ctx context.Context, code, codeVerifier string) (identity.Tokens, error) {
	return rp.token(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {rp.cfg.RedirectURL},
		"code_verifier": {codeVerifier},
	})
}

// Refresh implements identity.LoginClient.
func (rp *RelyingParty) Refresh(ctx context.Context, refreshToken string) (identity.Tokens, error) {
	return rp.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
}

// Revoke implements identity.LoginClient (RFC 7009).
func (rp *RelyingParty) Revoke(ctx context.Context, refreshToken string) error {
	doc, err := rp.discover(ctx)
	if err != nil {
		return err
	}
	if doc.RevocationURL == "" {
		return identity.ErrUnsupported
	}
	resp, err := rp.postForm(ctx, rp.backChannel(doc.RevocationURL), url.Values{
		"token":           {refreshToken},
		"token_type_hint": {"refresh_token"},
		"client_id":       {rp.cfg.ClientID},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case transient(resp.StatusCode):
		return fmt.Errorf("%w: revocation: %s", reconcile.ErrProviderNotReady, resp.Status)
	default:
		return invalid("revocation: " + resp.Status)
	}
}

// EndSessionURL implements identity.LoginClient.
func (rp *RelyingParty) EndSessionURL(ctx context.Context, idTokenHint string) (string, error) {
	doc, err := rp.discover(ctx)
	if err != nil {
		return "", err
	}
	if doc.EndSessionURL == "" {
		return "", identity.ErrUnsupported
	}
	q := url.Values{"client_id": {rp.cfg.ClientID}}
	if idTokenHint != "" {
		q.Set("id_token_hint", idTokenHint)
	}
	if rp.cfg.PostLogoutRedirectURL != "" {
		q.Set("post_logout_redirect_uri", rp.cfg.PostLogoutRedirectURL)
	}
	return withQuery(doc.EndSessionURL, q)
}

// VerifyIDToken implements identity.LoginClient.
func (rp *RelyingParty) VerifyIDToken(ctx context.Context, raw, nonce string) (string, error) {
	if nonce == "" {
		// The login flow always sends one, so an empty nonce here is a
		// caller bug that would silently disable replay protection.
		return "", invalid("no nonce to check")
	}
	return rp.cfg.Verifier.verifyIDToken(ctx, raw, nonce)
}

// tokenResponse is RFC 6749 section 5.1 plus the OIDC id_token.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error"`
}

// maxExpiresIn bounds a provider-supplied lifetime (one year) so a
// nonsense value can't overflow the expiry arithmetic.
const maxExpiresIn = 365 * 24 * 3600

func (rp *RelyingParty) token(ctx context.Context, form url.Values) (identity.Tokens, error) {
	doc, err := rp.discover(ctx)
	if err != nil {
		return identity.Tokens{}, err
	}
	form.Set("client_id", rp.cfg.ClientID)
	resp, err := rp.postForm(ctx, rp.backChannel(doc.TokenURL), form)
	if err != nil {
		return identity.Tokens{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocSize))
	if err != nil {
		return identity.Tokens{}, fmt.Errorf("%w: token endpoint: %w", reconcile.ErrProviderNotReady, err)
	}
	var tr tokenResponse
	_ = json.Unmarshal(body, &tr) // an unparseable body is judged by status below
	switch {
	case resp.StatusCode == http.StatusOK && tr.AccessToken != "":
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized:
		// RFC 6749 5.2: the grant itself was refused. Only the error code
		// is kept -- error_description may echo request values.
		return identity.Tokens{}, invalid("token endpoint: " + oauthErrorCode(tr.Error))
	case transient(resp.StatusCode):
		return identity.Tokens{}, fmt.Errorf("%w: token endpoint: %s", reconcile.ErrProviderNotReady, resp.Status)
	default:
		return identity.Tokens{}, invalid("token endpoint: " + resp.Status)
	}
	if tr.TokenType != "" && !strings.EqualFold(tr.TokenType, "bearer") {
		return identity.Tokens{}, invalid(fmt.Sprintf("token type %q", oauthErrorCode(tr.TokenType)))
	}
	expiresIn := min(max(tr.ExpiresIn, 0), maxExpiresIn)
	return identity.Tokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
		Expiry:       rp.cfg.Now().Add(time.Duration(expiresIn) * time.Second),
	}, nil
}

// transient reports whether a status means "try again later" rather than
// "this credential is refused": a server error, a timeout or rate limiting.
// Ending a session on one of these would sign people out during an outage.
func transient(status int) bool {
	return status >= 500 || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests
}

// oauthErrorCode keeps an OAuth error code only if it looks like one
// (RFC 6749 restricts it to printable ASCII without quotes or backslash),
// so arbitrary provider text never reaches logs or responses.
func oauthErrorCode(s string) string {
	if s == "" || len(s) > 64 {
		return "unspecified"
	}
	for _, r := range s {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return "unspecified"
		}
	}
	return s
}

// postForm sends a back-channel form POST. Transport failures are the
// provider being unreachable, never the credential being bad.
func (rp *RelyingParty) postForm(ctx context.Context, endpoint string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", reconcile.ErrProviderNotReady, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := rp.cfg.HTTPClient.Do(req)
	if err != nil {
		// *url.Error carries the URL, never the body, so no secret leaks.
		return nil, fmt.Errorf("%w: %w", reconcile.ErrProviderNotReady, err)
	}
	return resp, nil
}

// discover reads and caches the discovery document. A failure isn't
// cached, so the next call retries.
func (rp *RelyingParty) discover(ctx context.Context) (*discovery, error) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if rp.doc != nil {
		return rp.doc, nil
	}
	base := rp.cfg.Issuer
	if rp.cfg.BackChannelURL != "" {
		base = rp.cfg.BackChannelURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: discovery: %w", reconcile.ErrProviderNotReady, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := rp.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: discovery: %w", reconcile.ErrProviderNotReady, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: discovery: %s", reconcile.ErrProviderNotReady, resp.Status)
	}
	var doc discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDocSize)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: discovery: %w", reconcile.ErrProviderNotReady, err)
	}
	if doc.Issuer != rp.cfg.Issuer {
		return nil, fmt.Errorf("%w: discovery: issuer %q, want %q", reconcile.ErrProviderNotReady, doc.Issuer, rp.cfg.Issuer)
	}
	if doc.AuthorizationURL == "" || doc.TokenURL == "" {
		return nil, fmt.Errorf("%w: discovery: missing authorization or token endpoint", reconcile.ErrProviderNotReady)
	}
	rp.doc = &doc
	return rp.doc, nil
}

// backChannel rewrites an endpoint under the public issuer onto
// BackChannelURL. Endpoints elsewhere are left alone.
func (rp *RelyingParty) backChannel(endpoint string) string {
	if rp.cfg.BackChannelURL == "" {
		return endpoint
	}
	issuer := strings.TrimSuffix(rp.cfg.Issuer, "/")
	if rest, ok := strings.CutPrefix(endpoint, issuer); ok && (rest == "" || rest[0] == '/') {
		return strings.TrimSuffix(rp.cfg.BackChannelURL, "/") + rest
	}
	return endpoint
}

func withQuery(endpoint string, q url.Values) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("%w: endpoint %q: %w", reconcile.ErrProviderNotReady, endpoint, err)
	}
	existing := u.Query()
	maps.Copy(existing, q)
	u.RawQuery = existing.Encode()
	return u.String(), nil
}
