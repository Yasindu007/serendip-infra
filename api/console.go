package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
)

// Cookie and header names (specs/016-ui-mvp plan.md decisions 3 and 6).
const (
	SessionCookie = "serendip_session"
	loginCookie   = "serendip_login"
	// CSRFHeader must accompany every cookie-authenticated request. A
	// cross-site page can't add a custom header without a CORS preflight,
	// which this server never approves.
	CSRFHeader = "X-Serendip-Csrf"
)

// Login-flow limits (plan.md decisions 3, 5 and 9).
const (
	maxPendingLogins = 256
	pendingLoginTTL  = 10 * time.Minute
	// refreshMargin: a session's access token is renewed when it has less
	// than this left, so a request never runs on a token about to lapse.
	refreshMargin = time.Minute
	// providerCallTimeout bounds every call to the identity provider made
	// on a browser's behalf.
	providerCallTimeout = 10 * time.Second
	revokeTimeout       = 3 * time.Second
	maxReturnPath       = 512
)

// Console wires the web console and its browser login into the server
// (ADR 0013). A nil *Console means neither exists: the API alone, as before.
type Console struct {
	// Handler serves the built console (cmd/serendipd/console).
	Handler http.Handler
	// Origin is the console's public origin, from ParseOrigin. Browsers are
	// redirected to it, cookies are scoped to it, and Origin headers must
	// equal it.
	Origin *url.URL
	// Login returns the identity provider's login client, or nil while the
	// provider isn't ready yet.
	Login func() identity.LoginClient
	// Sessions holds the logged-in browsers.
	Sessions *Sessions
	// Now is the clock; defaults to time.Now. For tests.
	Now func() time.Time

	// authn verifies access tokens after an exchange or refresh; set by
	// NewServer from its authn argument.
	authn identity.Authenticator

	pendingMu sync.Mutex
	pending   map[string]pendingLogin

	limiter *rateLimiter
}

// pendingLogin is what /auth/login remembers until its callback.
type pendingLogin struct {
	verifier string
	nonce    string
	ret      string
	created  time.Time
}

// ParseOrigin validates the console's public origin. It must be an
// absolute http(s) URL with no path, query or fragment. Plain http is
// accepted only for localhost (127.0.0.1, ::1, localhost) unless
// allowInsecure is set: a browser sends cookies over plain http to any
// host, and nothing else protects them until HTTPS exists.
func ParseOrigin(raw string, allowInsecure bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(raw), "/"))
	if err != nil {
		return nil, fmt.Errorf("public URL %q: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("public URL %q: must be http(s)://host[:port]", raw)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, fmt.Errorf("public URL %q: must not have a path, query or fragment", raw)
	}
	if u.Scheme == "http" && !allowInsecure && !isLoopback(u.Hostname()) {
		return nil, fmt.Errorf("public URL %q: plain http is only allowed for localhost (set SERENDIP_ALLOW_INSECURE_ORIGIN=1 to accept the risk)", raw)
	}
	// Browsers send Origin with a lowercase host and no default port.
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" && !(u.Scheme == "http" && port == "80") && !(u.Scheme == "https" && port == "443") {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return &url.URL{Scheme: u.Scheme, Host: host}, nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Console) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Console) secure() bool { return c.Origin.Scheme == "https" }

func (c *Console) init(authn identity.Authenticator) {
	c.authn = authn
	c.pending = map[string]pendingLogin{}
	c.limiter = newRateLimiter(10, 20, c.now)
	if c.Login == nil {
		c.Login = func() identity.LoginClient { return nil }
	}
}

// register mounts the console and its login routes on top (the outer mux,
// outside AuthMiddleware).
func (c *Console) register(top *http.ServeMux) {
	top.Handle("GET /auth/login", c.browserRoute(c.limited(http.HandlerFunc(c.login))))
	top.Handle("GET /auth/callback", c.browserRoute(c.limited(http.HandlerFunc(c.callback))))
	top.Handle("POST /auth/logout", c.browserRoute(http.HandlerFunc(c.logout)))
	top.Handle("/", c.browserRoute(c.Handler))
}

// browserRoute applies what every browser-facing route needs: the canonical
// host, security headers, and no caching of /auth responses.
func (c *Console) browserRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Host, c.Origin.Host) && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			// e.g. 127.0.0.1 vs localhost: cookies set under one host
			// aren't sent to the other, which would half-log people in.
			http.Redirect(w, r, c.Origin.String()+r.URL.RequestURI(), http.StatusTemporaryRedirect)
			return
		}
		setSecurityHeaders(w.Header())
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (c *Console) limited(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wait, ok := c.limiter.allow(clientIP(r)); !ok {
			w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
			WriteProblem(w, http.StatusTooManyRequests, "too many requests", "too many login attempts from this address; retry later", audit.RequestIDFrom(r.Context()))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// signinError sends the browser to the console's sign-in page with a fixed
// reason code; nothing from the request is reflected.
func (c *Console) signinError(w http.ResponseWriter, r *http.Request, reason string) {
	http.Redirect(w, r, "/signin?error="+reason, http.StatusSeeOther)
}

func (c *Console) login(w http.ResponseWriter, r *http.Request) {
	lc := c.Login()
	if lc == nil {
		c.signinError(w, r, "provider_unavailable")
		return
	}
	state, err1 := newSecret()
	nonce, err2 := newSecret()
	verifier, err3 := newSecret()
	if err := errors.Join(err1, err2, err3); err != nil {
		WriteProblem(w, http.StatusInternalServerError, "internal error", "could not start a login", audit.RequestIDFrom(r.Context()))
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	ctx, cancel := context.WithTimeout(r.Context(), providerCallTimeout)
	defer cancel()
	target, err := lc.AuthCodeURL(ctx, state, nonce, base64.RawURLEncoding.EncodeToString(sum[:]))
	if err != nil {
		log.Printf("api: login start failed (request %s): %v", audit.RequestIDFrom(r.Context()), err)
		c.signinError(w, r, "provider_unavailable")
		return
	}
	c.putPending(state, pendingLogin{verifier: verifier, nonce: nonce, ret: safeReturn(r.URL.Query().Get("return")), created: c.now()})
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Value: state, Path: "/auth", MaxAge: int(pendingLoginTTL.Seconds()),
		HttpOnly: true, Secure: c.secure(), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, target, http.StatusFound)
}

func (c *Console) callback(w http.ResponseWriter, r *http.Request) {
	reqID := audit.RequestIDFrom(r.Context())
	// The pre-login cookie is single-use whatever happens next.
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Value: "", Path: "/auth", MaxAge: -1, HttpOnly: true, Secure: c.secure(), SameSite: http.SameSiteLaxMode})

	q := r.URL.Query()
	state := q.Get("state")
	ck, err := r.Cookie(loginCookie)
	if state == "" || err != nil || subtle.ConstantTimeCompare([]byte(ck.Value), []byte(state)) != 1 {
		// No pending entry is consumed: the state may belong to a login
		// another tab of this browser is still finishing.
		c.signinError(w, r, "invalid_request")
		return
	}
	p, ok := c.takePending(state)
	if !ok {
		c.signinError(w, r, "expired")
		return
	}
	if q.Get("error") != "" {
		c.signinError(w, r, "denied")
		return
	}
	code := q.Get("code")
	if code == "" {
		c.signinError(w, r, "invalid_request")
		return
	}
	lc := c.Login()
	if lc == nil || c.authn == nil {
		c.signinError(w, r, "provider_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), providerCallTimeout)
	defer cancel()
	tokens, err := lc.Exchange(ctx, code, p.verifier)
	if err != nil {
		log.Printf("api: login code exchange failed (request %s): %v", reqID, err)
		c.signinError(w, r, reasonFor(err))
		return
	}
	sub, err := lc.VerifyIDToken(ctx, tokens.IDToken, p.nonce)
	if err != nil {
		log.Printf("api: login ID token rejected (request %s): %v", reqID, err)
		c.signinError(w, r, reasonFor(err))
		return
	}
	principal, err := c.authn.Authenticate(ctx, tokens.AccessToken)
	if err != nil {
		log.Printf("api: login access token rejected (request %s): %v", reqID, err)
		c.signinError(w, r, reasonFor(err))
		return
	}
	if principal.Subject != sub {
		log.Printf("api: login rejected (request %s): ID token and access token name different subjects", reqID)
		c.signinError(w, r, "invalid_request")
		return
	}
	tokens.Expiry = c.expiryOf(tokens)
	if old, err := r.Cookie(SessionCookie); err == nil && old.Value != "" {
		// The same browser logging in again: its previous session ends.
		c.Sessions.remove(old.Value)
	}
	id, err := c.Sessions.create(principal, tokens)
	if err != nil {
		WriteProblem(w, http.StatusInternalServerError, "internal error", "could not create a session", reqID)
		return
	}
	http.SetCookie(w, c.sessionCookie(id))
	log.Printf("api: %s signed in to the console (request %s)", identity.Actor(principal), reqID)
	http.Redirect(w, r, p.ret, http.StatusSeeOther)
}

func reasonFor(err error) string {
	if errors.Is(err, reconcile.ErrProviderNotReady) {
		return "provider_unavailable"
	}
	return "invalid_request"
}

// fallbackTokenLifetime is assumed when a provider gives no usable access
// token lifetime, so a session isn't refreshed on every request.
const fallbackTokenLifetime = 5 * time.Minute

func (c *Console) expiryOf(t identity.Tokens) time.Time {
	if min := c.now().Add(refreshMargin); !t.Expiry.After(min) {
		return c.now().Add(fallbackTokenLifetime)
	}
	return t.Expiry
}

func (c *Console) sessionCookie(id string) *http.Cookie {
	return &http.Cookie{Name: SessionCookie, Value: id, Path: "/", HttpOnly: true, Secure: c.secure(), SameSite: http.SameSiteStrictMode}
}

// logout ends the session here, revokes its refresh token at the provider
// (best effort), and answers where the browser should go to end its
// provider session. A POST with the CSRF checks, so a cross-site page can't
// log people out.
func (c *Console) logout(w http.ResponseWriter, r *http.Request) {
	reqID := audit.RequestIDFrom(r.Context())
	if !c.csrfOK(w, r) {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: c.secure(), SameSite: http.SameSiteStrictMode})
	location := "/"
	ck, err := r.Cookie(SessionCookie)
	if err == nil {
		if sess := c.Sessions.remove(ck.Value); sess != nil {
			location = c.endProviderSession(r.Context(), sess, reqID)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"location": location})
}

func (c *Console) endProviderSession(ctx context.Context, sess *session, reqID string) string {
	// The session is already gone from the table; finish the provider side
	// even if the browser stops waiting, so the refresh token is revoked.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), providerCallTimeout)
	defer cancel()
	if err := sess.acquire(ctx); err != nil {
		return "/"
	}
	tokens, principal := sess.tokens, sess.principal
	sess.release()
	lc := c.Login()
	if lc == nil {
		return "/"
	}
	if tokens.RefreshToken != "" {
		rctx, rcancel := context.WithTimeout(ctx, revokeTimeout)
		err := lc.Revoke(rctx, tokens.RefreshToken)
		rcancel()
		if err != nil && !errors.Is(err, identity.ErrUnsupported) {
			log.Printf("api: refresh token revocation failed on logout (request %s): %v", reqID, err)
		}
	}
	log.Printf("api: %s signed out of the console (request %s)", identity.Actor(principal), reqID)
	// No id_token_hint: the provider accepts client_id alone, and this
	// keeps the ID token out of a URL the browser navigates to (and the
	// provider's access logs).
	loc, err := lc.EndSessionURL(ctx, "")
	if err != nil {
		return "/"
	}
	return loc
}

// csrfOK enforces plan.md decision 6 on a cookie-authenticated request,
// writing a 403 and returning false when it fails.
func (c *Console) csrfOK(w http.ResponseWriter, r *http.Request) bool {
	ok := r.Header.Get(CSRFHeader) == "1"
	if ok && r.Method != http.MethodGet && r.Method != http.MethodHead {
		ok = r.Header.Get("Origin") == c.Origin.String()
	}
	if !ok {
		WriteProblem(w, http.StatusForbidden, "cross-site request rejected",
			"cookie-authenticated requests need the "+CSRFHeader+" header and, to change anything, a matching Origin", audit.RequestIDFrom(r.Context()))
	}
	return ok
}

// sessionContextKey carries the request's session (if it used one) from
// AuthMiddleware to GET /v1/session.
type sessionContextKey struct{}

// authenticateCookie resolves a cookie-authenticated request to its person.
// On failure it has written the response and returns ok == false.
func (c *Console) authenticateCookie(w http.ResponseWriter, r *http.Request, id string) (p identity.Principal, ctx context.Context, ok bool) {
	reqID := audit.RequestIDFrom(r.Context())
	if !c.csrfOK(w, r) {
		return identity.Principal{}, nil, false
	}
	sess := c.Sessions.lookup(id)
	if sess == nil {
		WriteProblem(w, http.StatusUnauthorized, "unauthorized", "the session has ended; sign in again", reqID)
		return identity.Principal{}, nil, false
	}
	p, err := c.ensureFresh(r.Context(), sess)
	switch {
	case err == nil:
	case errors.Is(err, errSessionEnded):
		c.Sessions.removeSession(sess)
		log.Printf("api: console session ended by the identity provider (request %s)", reqID)
		WriteProblem(w, http.StatusUnauthorized, "unauthorized", "the session has ended; sign in again", reqID)
		return identity.Principal{}, nil, false
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		WriteProblem(w, http.StatusServiceUnavailable, "identity provider not ready", "the session couldn't be renewed in time; retry", reqID)
		return identity.Principal{}, nil, false
	default:
		log.Printf("api: console session refresh failed (request %s): %v", reqID, err)
		WriteProblem(w, http.StatusServiceUnavailable, "identity provider not ready", "the session can't be renewed right now; retry later", reqID)
		return identity.Principal{}, nil, false
	}
	return p, context.WithValue(r.Context(), sessionContextKey{}, sess), true
}

// ensureFresh returns sess's principal, first renewing its access token if
// that is about to lapse. The session's lock makes this run once at a time:
// the identity provider rotates refresh tokens and revokes the whole family
// when an old one is replayed (T001 spike finding 3), so two concurrent
// refreshes would end the session.
func (c *Console) ensureFresh(ctx context.Context, sess *session) (identity.Principal, error) {
	if err := sess.acquire(ctx); err != nil {
		return identity.Principal{}, err
	}
	defer sess.release()
	if c.now().Add(refreshMargin).Before(sess.tokens.Expiry) {
		return sess.principal, nil
	}
	lc := c.Login()
	if lc == nil || c.authn == nil {
		return identity.Principal{}, reconcile.ErrProviderNotReady
	}
	if sess.tokens.RefreshToken == "" {
		return identity.Principal{}, errSessionEnded
	}
	// Not the request's context: once the provider has rotated the refresh
	// token, losing the reply would lose the session.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), providerCallTimeout)
	defer cancel()
	t, err := lc.Refresh(rctx, sess.tokens.RefreshToken)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredential) {
			return identity.Principal{}, errSessionEnded
		}
		return identity.Principal{}, err
	}
	// The provider has already retired the old refresh token: keep the new
	// one before anything else can fail, or the next attempt would replay
	// the old one and get the whole family revoked.
	if t.RefreshToken != "" {
		sess.tokens.RefreshToken = t.RefreshToken
	}
	if t.IDToken != "" {
		sess.tokens.IDToken = t.IDToken
	}
	p, err := c.authn.Authenticate(rctx, t.AccessToken)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredential) {
			return identity.Principal{}, errSessionEnded
		}
		return identity.Principal{}, err
	}
	if p.Subject != sess.principal.Subject {
		return identity.Principal{}, errSessionEnded
	}
	sess.principal = p
	sess.tokens.AccessToken, sess.tokens.Expiry = t.AccessToken, c.expiryOf(t)
	return p, nil
}

func (c *Console) putPending(state string, p pendingLogin) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	now := c.now()
	for k, v := range c.pending {
		if now.Sub(v.created) >= pendingLoginTTL {
			delete(c.pending, k)
		}
	}
	for len(c.pending) >= maxPendingLogins {
		var oldestKey string
		var oldest time.Time
		for k, v := range c.pending {
			if oldestKey == "" || v.created.Before(oldest) {
				oldestKey, oldest = k, v.created
			}
		}
		delete(c.pending, oldestKey)
	}
	c.pending[state] = p
}

// takePending removes and returns the pending login for state, if it
// exists and hasn't expired. Single use: a replayed callback finds nothing.
func (c *Console) takePending(state string) (pendingLogin, bool) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	p, ok := c.pending[state]
	delete(c.pending, state)
	if !ok || c.now().Sub(p.created) >= pendingLoginTTL {
		return pendingLogin{}, false
	}
	return p, true
}

// safeReturn accepts only a same-origin path (one leading slash, no
// backslash, no control characters), so the login can't be used as an
// open redirect. Anything else becomes "/".
func safeReturn(s string) string {
	if s == "" || len(s) > maxReturnPath || s[0] != '/' || strings.HasPrefix(s, "//") || strings.ContainsRune(s, '\\') {
		return "/"
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "/"
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	// A decoded path that starts with // would still be read as a host by
	// some clients.
	if strings.HasPrefix(u.Path, "//") || strings.ContainsRune(u.Path, '\\') {
		return "/"
	}
	return s
}

// sessionInfo is GET /v1/session's body. Nothing in it is a credential.
type sessionInfo struct {
	Actor         string     `json:"actor"`
	Method        string     `json:"method"` // "session" or "bearer"
	Subject       string     `json:"subject,omitempty"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	IdleExpiresAt *time.Time `json:"idleExpiresAt,omitempty"`
}

// sessionHandler serves GET /v1/session: who the caller is and, for a
// console session, when it ends.
func (c *Console) sessionHandler(w http.ResponseWriter, r *http.Request) {
	info := sessionInfo{Actor: audit.ActorFrom(r.Context()), Method: "bearer"}
	if c != nil {
		if sess, ok := r.Context().Value(sessionContextKey{}).(*session); ok {
			abs, idle := c.Sessions.expiries(sess)
			info.Method, info.ExpiresAt, info.IdleExpiresAt = "session", &abs, &idle
			if err := sess.acquire(r.Context()); err == nil {
				info.Subject = sess.principal.Subject
				sess.release()
			}
		}
	}
	writeJSON(w, http.StatusOK, info)
}
