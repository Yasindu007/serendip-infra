package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

const testOrigin = "http://localhost:8080"

// clock is a settable time source shared by the console and its sessions.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

// fakeLogin is an identity.LoginClient whose provider is simulated.
type fakeLogin struct {
	clk *clock

	mu        sync.Mutex
	challenge string
	nonce     string
	revoked   []string
	idSubject string

	refreshes    atomic.Int32
	refreshErr   error
	refreshDelay time.Duration
	exchangeErr  error
}

func (f *fakeLogin) AuthCodeURL(_ context.Context, state, nonce, challenge string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.challenge, f.nonce = challenge, nonce
	return "http://idp.test/authorize?state=" + url.QueryEscape(state), nil
}

func (f *fakeLogin) Exchange(_ context.Context, code, verifier string) (identity.Tokens, error) {
	if f.exchangeErr != nil {
		return identity.Tokens{}, f.exchangeErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
		return identity.Tokens{}, fmt.Errorf("%w: PKCE mismatch", identity.ErrInvalidCredential)
	}
	return identity.Tokens{AccessToken: "at-" + code, RefreshToken: "rt-0", IDToken: "idt-" + code, Expiry: f.clk.Now().Add(time.Hour)}, nil
}

func (f *fakeLogin) Refresh(_ context.Context, rt string) (identity.Tokens, error) {
	n := f.refreshes.Add(1)
	time.Sleep(f.refreshDelay)
	if f.refreshErr != nil {
		return identity.Tokens{}, f.refreshErr
	}
	return identity.Tokens{AccessToken: "at-refreshed", RefreshToken: fmt.Sprintf("rt-%d", n), Expiry: f.clk.Now().Add(time.Hour)}, nil
}

func (f *fakeLogin) Revoke(_ context.Context, rt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, rt)
	return nil
}

func (f *fakeLogin) EndSessionURL(_ context.Context, hint string) (string, error) {
	return "http://idp.test/logout?id_token_hint=" + url.QueryEscape(hint), nil
}

func (f *fakeLogin) VerifyIDToken(_ context.Context, raw, nonce string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if nonce == "" || nonce != f.nonce {
		return "", fmt.Errorf("%w: nonce mismatch", identity.ErrInvalidCredential)
	}
	if f.idSubject != "" {
		return f.idSubject, nil
	}
	return "sub-1", nil
}

// consoleAuthn accepts the fake provider's access tokens.
type consoleAuthn struct{}

func (consoleAuthn) Authenticate(_ context.Context, token string) (identity.Principal, error) {
	if strings.HasPrefix(token, "at-") {
		return identity.Principal{Subject: "sub-1", Name: "admin"}, nil
	}
	return identity.Principal{}, identity.ErrInvalidCredential
}

func newConsole(t *testing.T) (http.Handler, *clock, *fakeLogin, *api.Sessions) {
	t.Helper()
	return newConsoleWith(t, consoleAuthn{})
}

func newConsoleWith(t *testing.T, authn identity.Authenticator) (http.Handler, *clock, *fakeLogin, *api.Sessions) {
	t.Helper()
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	fl := &fakeLogin{clk: clk}
	origin, err := api.ParseOrigin(testOrigin, false)
	if err != nil {
		t.Fatal(err)
	}
	sessions := api.NewSessions(api.SessionConfig{Now: clk.Now})
	shell := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>shell</html>")) })
	c := &api.Console{Handler: shell, Origin: origin, Login: func() identity.LoginClient { return fl }, Sessions: sessions, Now: clk.Now}
	srv := api.NewServer(newTestDB(t), event.NewInProcessBus(), map[string]string{"key-alice": "alice"}, authn, nil, nil, c)
	return srv.Handler, clk, fl, sessions
}

// req builds a request to the console's own host.
func req(method, target string, body []byte) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Host = "localhost:8080"
	r.RemoteAddr = "127.0.0.1:50000"
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func cookieFrom(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// startLogin runs /auth/login and returns the state and pre-login cookie.
func startLogin(t *testing.T, h http.Handler, ret string) (string, *http.Cookie) {
	t.Helper()
	w := serve(h, req("GET", "/auth/login?return="+url.QueryEscape(ret), nil))
	if w.Code != http.StatusFound {
		t.Fatalf("login: status %d %s", w.Code, w.Body)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	ck := cookieFrom(w, "serendip_login")
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/auth" {
		t.Fatalf("pre-login cookie = %+v", ck)
	}
	return loc.Query().Get("state"), ck
}

func callback(h http.Handler, state, code string, ck *http.Cookie) *httptest.ResponseRecorder {
	r := req("GET", "/auth/callback?code="+code+"&state="+url.QueryEscape(state), nil)
	if ck != nil {
		r.AddCookie(ck)
	}
	return serve(h, r)
}

// signIn performs a whole login and returns the session cookie.
func signIn(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	state, ck := startLogin(t, h, "/applications")
	w := callback(h, state, "c1", ck)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/applications" {
		t.Fatalf("callback: %d to %q", w.Code, w.Header().Get("Location"))
	}
	sc := cookieFrom(w, api.SessionCookie)
	if sc == nil || !sc.HttpOnly || sc.SameSite != http.SameSiteStrictMode || sc.Path != "/" || sc.Domain != "" {
		t.Fatalf("session cookie = %+v", sc)
	}
	return sc
}

// keepActive advances the clock by d in steps short of the idle timeout,
// using the session at each step, as an open console tab would.
func keepActive(h http.Handler, clk *clock, sc *http.Cookie, d time.Duration) {
	const step = 20 * time.Minute
	for d > 0 {
		s := min(step, d)
		clk.Advance(s)
		d -= s
		apiCall(h, "GET", "/v1/session", sc, nil)
	}
}

// apiCall is a console fetch: session cookie plus the CSRF header, and the
// page's Origin on unsafe methods.
func apiCall(h http.Handler, method, path string, sc *http.Cookie, body []byte) *httptest.ResponseRecorder {
	r := req(method, path, body)
	r.AddCookie(sc)
	r.Header.Set(api.CSRFHeader, "1")
	if method != "GET" {
		r.Header.Set("Origin", testOrigin)
	}
	return serve(h, r)
}

func TestConsole_LoginFlowAndSession(t *testing.T) {
	h, _, _, _ := newConsole(t)
	sc := signIn(t, h)

	w := apiCall(h, "GET", "/v1/session", sc, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/v1/session: %d %s", w.Code, w.Body)
	}
	var info map[string]any
	json.Unmarshal(w.Body.Bytes(), &info)
	if info["actor"] != "person:admin" || info["method"] != "session" || info["subject"] != "sub-1" || info["expiresAt"] == nil {
		t.Errorf("session info = %v", info)
	}
	for _, leak := range []string{"at-", "rt-", "idt-", sc.Value} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("/v1/session leaks %q", leak)
		}
	}

	// A second login gets a different session ID (no fixation).
	if sc2 := signIn(t, h); sc2.Value == sc.Value {
		t.Error("two logins share a session ID")
	}
}

func TestConsole_AuditActorIsThePerson(t *testing.T) {
	db := newTestDB(t)
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	fl := &fakeLogin{clk: clk}
	origin, _ := api.ParseOrigin(testOrigin, false)
	c := &api.Console{Handler: http.NotFoundHandler(), Origin: origin, Login: func() identity.LoginClient { return fl },
		Sessions: api.NewSessions(api.SessionConfig{Now: clk.Now}), Now: clk.Now}
	h := api.NewServer(db, event.NewInProcessBus(), nil, consoleAuthn{}, nil, nil, c).Handler
	sc := signIn(t, h)

	body, _ := json.Marshal(resource.Application{Metadata: resource.Metadata{Name: "storefront"}})
	w := apiCall(h, "POST", "/v1/applications", sc, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var actor string
	if err := db.QueryRowContext(context.Background(), "SELECT actor FROM audit_log ORDER BY at DESC LIMIT 1").Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if actor != "person:admin" {
		t.Errorf("audit actor = %q, want person:admin", actor)
	}
}

func TestConsole_CallbackRejections(t *testing.T) {
	cases := []struct {
		name   string
		run    func(t *testing.T, h http.Handler, fl *fakeLogin, clk *clock) *httptest.ResponseRecorder
		reason string
	}{
		{"state mismatch", func(t *testing.T, h http.Handler, _ *fakeLogin, _ *clock) *httptest.ResponseRecorder {
			_, ck := startLogin(t, h, "/")
			return callback(h, "forged", "c1", ck)
		}, "invalid_request"},
		{"missing pre-login cookie", func(t *testing.T, h http.Handler, _ *fakeLogin, _ *clock) *httptest.ResponseRecorder {
			state, _ := startLogin(t, h, "/")
			return callback(h, state, "c1", nil)
		}, "invalid_request"},
		{"replayed callback", func(t *testing.T, h http.Handler, _ *fakeLogin, _ *clock) *httptest.ResponseRecorder {
			state, ck := startLogin(t, h, "/")
			if w := callback(h, state, "c1", ck); w.Code != http.StatusSeeOther || cookieFrom(w, api.SessionCookie) == nil {
				t.Fatalf("first callback failed: %d", w.Code)
			}
			return callback(h, state, "c1", ck)
		}, "expired"},
		{"expired login", func(t *testing.T, h http.Handler, _ *fakeLogin, clk *clock) *httptest.ResponseRecorder {
			state, ck := startLogin(t, h, "/")
			clk.Advance(11 * time.Minute)
			return callback(h, state, "c1", ck)
		}, "expired"},
		{"provider error", func(t *testing.T, h http.Handler, _ *fakeLogin, _ *clock) *httptest.ResponseRecorder {
			state, ck := startLogin(t, h, "/")
			r := req("GET", "/auth/callback?error=access_denied&error_description=%3Cscript%3E&state="+url.QueryEscape(state), nil)
			r.AddCookie(ck)
			return serve(h, r)
		}, "denied"},
		{"wrong nonce", func(t *testing.T, h http.Handler, fl *fakeLogin, _ *clock) *httptest.ResponseRecorder {
			state, ck := startLogin(t, h, "/")
			fl.mu.Lock()
			fl.nonce = "someone-elses"
			fl.mu.Unlock()
			return callback(h, state, "c1", ck)
		}, "invalid_request"},
		{"subject mismatch", func(t *testing.T, h http.Handler, fl *fakeLogin, _ *clock) *httptest.ResponseRecorder {
			state, ck := startLogin(t, h, "/")
			fl.mu.Lock()
			fl.idSubject = "sub-2"
			fl.mu.Unlock()
			return callback(h, state, "c1", ck)
		}, "invalid_request"},
		{"code refused", func(t *testing.T, h http.Handler, fl *fakeLogin, _ *clock) *httptest.ResponseRecorder {
			state, ck := startLogin(t, h, "/")
			fl.exchangeErr = fmt.Errorf("%w: invalid_grant", identity.ErrInvalidCredential)
			return callback(h, state, "c1", ck)
		}, "invalid_request"},
		{"provider down", func(t *testing.T, h http.Handler, fl *fakeLogin, _ *clock) *httptest.ResponseRecorder {
			state, ck := startLogin(t, h, "/")
			fl.exchangeErr = reconcile.ErrProviderNotReady
			return callback(h, state, "c1", ck)
		}, "provider_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, clk, fl, sessions := newConsole(t)
			before := sessions.Len()
			w := tc.run(t, h, fl, clk)
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/signin?error="+tc.reason {
				t.Errorf("got %d to %q, want 303 to /signin?error=%s", w.Code, w.Header().Get("Location"), tc.reason)
			}
			if tc.name != "replayed callback" && sessions.Len() != before {
				t.Error("a session was created")
			}
			if sc := cookieFrom(w, api.SessionCookie); sc != nil && sc.Value != "" {
				t.Error("a session cookie was set")
			}
		})
	}
}

func TestConsole_ReturnPathIsNeverAnOpenRedirect(t *testing.T) {
	cases := map[string]string{
		"/applications/42?tab=conditions": "/applications/42?tab=conditions",
		"":                                "/",
		"//evil.example":                  "/",
		"/\\evil.example":                 "/",
		"https://evil.example/":           "/",
		"javascript:alert(1)":             "/",
		"/%2F%2Fevil.example":             "/",
		"/ok\r\nSet-Cookie: x=y":          "/",
		"applications":                    "/",
		"/%5Cevil.example":                "/",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			h, _, _, _ := newConsole(t)
			state, ck := startLogin(t, h, in)
			w := callback(h, state, "c1", ck)
			if got := w.Header().Get("Location"); got != want {
				t.Errorf("return %q redirected to %q, want %q", in, got, want)
			}
		})
	}
}

func TestConsole_CookieRequestsNeedCSRFChecks(t *testing.T) {
	h, _, _, _ := newConsole(t)
	sc := signIn(t, h)
	body, _ := json.Marshal(resource.Application{Metadata: resource.Metadata{Name: "x"}})

	cases := []struct {
		name   string
		method string
		header bool
		origin string
		want   int
	}{
		{"read with header", "GET", true, "", http.StatusOK},
		{"read without header", "GET", false, "", http.StatusForbidden},
		{"write, right origin", "POST", true, testOrigin, http.StatusCreated},
		{"write, no origin", "POST", true, "", http.StatusForbidden},
		{"write, other origin", "POST", true, "http://evil.example", http.StatusForbidden},
		{"write, lookalike origin", "POST", true, testOrigin + ".evil.example", http.StatusForbidden},
		{"write, no header", "POST", false, testOrigin, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r *http.Request
			if tc.method == "GET" {
				r = req("GET", "/v1/applications", nil)
			} else {
				r = req("POST", "/v1/applications", body)
			}
			r.AddCookie(sc)
			if tc.header {
				r.Header.Set(api.CSRFHeader, "1")
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := serve(h, r)
			if w.Code != tc.want {
				t.Errorf("status %d, want %d (%s)", w.Code, tc.want, w.Body)
			}
			for k := range w.Header() {
				if strings.HasPrefix(strings.ToLower(k), "access-control-") {
					t.Errorf("CORS header %s sent", k)
				}
			}
		})
	}

	// A preflight is never approved.
	r := req("OPTIONS", "/v1/applications", nil)
	r.Header.Set("Origin", "http://evil.example")
	r.Header.Set("Access-Control-Request-Method", "POST")
	w := serve(h, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("preflight approved")
	}
}

func TestConsole_BearerAndCookieTogetherIsRefused(t *testing.T) {
	h, _, _, _ := newConsole(t)
	sc := signIn(t, h)
	r := req("GET", "/v1/applications", nil)
	r.AddCookie(sc)
	r.Header.Set(api.CSRFHeader, "1")
	r.Header.Set("Authorization", "Bearer key-alice")
	if w := serve(h, r); w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

func TestConsole_UnknownSessionIsUnauthorized(t *testing.T) {
	h, _, _, _ := newConsole(t)
	w := apiCall(h, "GET", "/v1/applications", &http.Cookie{Name: api.SessionCookie, Value: "attacker-chosen"}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
}

func TestConsole_APIKeysAndBearerUnchanged(t *testing.T) {
	h, _, _, _ := newConsole(t)
	for _, tok := range []string{"key-alice", "at-direct"} {
		r := req("GET", "/v1/applications", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		if w := serve(h, r); w.Code != http.StatusOK {
			t.Errorf("bearer %s: status %d", tok, w.Code)
		}
	}
}

func TestConsole_ConcurrentRequestsRefreshOnce(t *testing.T) {
	h, clk, fl, _ := newConsole(t)
	sc := signIn(t, h)
	keepActive(h, clk, sc, 40*time.Minute)
	if n := fl.refreshes.Load(); n != 0 {
		t.Fatalf("refreshed %d times before the token was near expiry", n)
	}
	fl.refreshDelay = 50 * time.Millisecond
	clk.Advance(19*time.Minute + 30*time.Second) // inside the refresh margin, not idle

	var wg sync.WaitGroup
	codes := make(chan int, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- apiCall(h, "GET", "/v1/applications", sc, nil).Code
		}()
	}
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != http.StatusOK {
			t.Errorf("a request got %d", c)
		}
	}
	if n := fl.refreshes.Load(); n != 1 {
		t.Errorf("%d refreshes, want exactly 1", n)
	}
}

func TestConsole_RefreshOutcomes(t *testing.T) {
	t.Run("refused ends the session", func(t *testing.T) {
		h, clk, fl, sessions := newConsole(t)
		sc := signIn(t, h)
		keepActive(h, clk, sc, 40*time.Minute)
		fl.refreshErr = fmt.Errorf("%w: invalid_grant", identity.ErrInvalidCredential)
		clk.Advance(20 * time.Minute)
		if w := apiCall(h, "GET", "/v1/applications", sc, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("status %d, want 401", w.Code)
		}
		if sessions.Len() != 0 {
			t.Error("session survived a refused refresh")
		}
	})
	t.Run("provider down keeps the session", func(t *testing.T) {
		h, clk, fl, sessions := newConsole(t)
		sc := signIn(t, h)
		keepActive(h, clk, sc, 40*time.Minute)
		fl.refreshErr = fmt.Errorf("%w: unreachable", reconcile.ErrProviderNotReady)
		clk.Advance(20 * time.Minute)
		if w := apiCall(h, "GET", "/v1/applications", sc, nil); w.Code != http.StatusServiceUnavailable {
			t.Errorf("status %d, want 503", w.Code)
		}
		if sessions.Len() != 1 {
			t.Error("an outage ended the session")
		}
		fl.refreshErr = nil
		if w := apiCall(h, "GET", "/v1/applications", sc, nil); w.Code != http.StatusOK {
			t.Errorf("after recovery: status %d", w.Code)
		}
	})
}

func TestConsole_SessionExpiry(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		h, clk, _, _ := newConsole(t)
		sc := signIn(t, h)
		clk.Advance(29 * time.Minute)
		if w := apiCall(h, "GET", "/v1/session", sc, nil); w.Code != http.StatusOK {
			t.Fatalf("within idle window: %d", w.Code)
		}
		clk.Advance(31 * time.Minute)
		if w := apiCall(h, "GET", "/v1/session", sc, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("after 31 idle minutes: %d, want 401", w.Code)
		}
	})
	t.Run("absolute", func(t *testing.T) {
		h, clk, _, _ := newConsole(t)
		sc := signIn(t, h)
		for range 12 * 3 { // active every 20 minutes for 12 hours
			clk.Advance(20 * time.Minute)
			apiCall(h, "GET", "/v1/session", sc, nil)
		}
		if w := apiCall(h, "GET", "/v1/session", sc, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("after 12 active hours: %d, want 401", w.Code)
		}
	})
}

func TestConsole_Logout(t *testing.T) {
	h, _, fl, sessions := newConsole(t)
	sc := signIn(t, h)

	r := req("POST", "/auth/logout", nil)
	r.AddCookie(sc)
	if w := serve(h, r); w.Code != http.StatusForbidden {
		t.Errorf("cross-site logout: status %d, want 403", w.Code)
	}

	w := apiCall(h, "POST", "/auth/logout", sc, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	var out map[string]string
	json.Unmarshal(w.Body.Bytes(), &out)
	if !strings.HasPrefix(out["location"], "http://idp.test/logout") || strings.Contains(out["location"], "idt-") {
		t.Errorf("location = %q, want the provider's logout without the ID token", out["location"])
	}
	if c := cookieFrom(w, api.SessionCookie); c == nil || c.MaxAge >= 0 {
		t.Errorf("session cookie not cleared: %+v", c)
	}
	if sessions.Len() != 0 {
		t.Error("session still held")
	}
	if len(fl.revoked) != 1 || fl.revoked[0] != "rt-0" {
		t.Errorf("revoked = %v", fl.revoked)
	}
	if w := apiCall(h, "GET", "/v1/session", sc, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("after logout: %d, want 401", w.Code)
	}
}

func TestConsole_PublicShellAndHeaders(t *testing.T) {
	h, _, _, _ := newConsole(t)
	for _, path := range []string{"/", "/applications/42"} {
		w := serve(h, req("GET", path, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "shell") {
			t.Errorf("GET %s without a cookie: %d", path, w.Code)
		}
		csp := w.Header().Get("Content-Security-Policy")
		for _, want := range []string{"script-src 'self'", "connect-src 'self'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("CSP %q lacks %q", csp, want)
			}
		}
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
			t.Errorf("CSP allows unsafe sources: %q", csp)
		}
		for k, v := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY"} {
			if w.Header().Get(k) != v {
				t.Errorf("%s = %q, want %q", k, w.Header().Get(k), v)
			}
		}
	}
	if w := serve(h, req("GET", "/auth/login", nil)); w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("/auth Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
	r := req("GET", "/v1/applications", nil)
	r.Header.Set("Authorization", "Bearer key-alice")
	if w := serve(h, r); w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("/v1 Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
}

func TestConsole_OtherHostIsRedirectedToTheOrigin(t *testing.T) {
	h, _, _, _ := newConsole(t)
	r := req("GET", "/applications?x=1", nil)
	r.Host = "127.0.0.1:8080"
	w := serve(h, r)
	if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != testOrigin+"/applications?x=1" {
		t.Errorf("got %d to %q", w.Code, w.Header().Get("Location"))
	}
	// The API itself still answers on any host (API keys via 127.0.0.1).
	r = req("GET", "/v1/applications", nil)
	r.Host = "127.0.0.1:8080"
	r.Header.Set("Authorization", "Bearer key-alice")
	if w := serve(h, r); w.Code != http.StatusOK {
		t.Errorf("API on another host: %d", w.Code)
	}
}

func TestConsole_BodyLimit(t *testing.T) {
	h, _, _, _ := newConsole(t)
	big := append([]byte(`{"metadata":{"name":"`), bytes.Repeat([]byte("a"), api.MaxBodyBytes)...)
	big = append(big, []byte(`"}}`)...)

	r := req("POST", "/v1/applications", big)
	r.Header.Set("Authorization", "Bearer key-alice")
	if w := serve(h, r); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("declared length: status %d, want 413", w.Code)
	}
	r = req("POST", "/v1/applications", big)
	r.ContentLength = -1 // chunked: only the reader can tell
	r.Header.Set("Authorization", "Bearer key-alice")
	if w := serve(h, r); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("chunked: status %d, want 413", w.Code)
	}
}

func TestConsole_LoginRateLimit(t *testing.T) {
	h, _, _, _ := newConsole(t)
	var last *httptest.ResponseRecorder
	for range 21 {
		last = serve(h, req("GET", "/auth/login", nil))
	}
	if last.Code != http.StatusTooManyRequests || last.Header().Get("Retry-After") == "" {
		t.Errorf("21st login: %d, Retry-After %q; want 429 with Retry-After", last.Code, last.Header().Get("Retry-After"))
	}
	other := req("GET", "/auth/login", nil)
	other.RemoteAddr = "127.0.0.2:1"
	if w := serve(h, other); w.Code != http.StatusFound {
		t.Errorf("another address: %d, want its own budget", w.Code)
	}
}

func TestConsole_PendingLoginsAreCapped(t *testing.T) {
	h, clk, _, _ := newConsole(t)
	first, firstCk := startLogin(t, h, "/")
	for i := range 300 {
		clk.Advance(time.Millisecond)
		r := req("GET", "/auth/login", nil)
		r.RemoteAddr = fmt.Sprintf("10.0.%d.%d:1", i/250, i%250) // stay under the per-address limit
		serve(h, r)
	}
	if w := callback(h, first, "c1", firstCk); w.Header().Get("Location") != "/signin?error=expired" {
		t.Errorf("oldest pending login survived the cap: %q", w.Header().Get("Location"))
	}
}

func TestConsole_LogsNeverContainSecrets(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	h, clk, fl, _ := newConsole(t)
	sc := signIn(t, h)
	keepActive(h, clk, sc, 40*time.Minute)
	fl.refreshErr = errors.New("boom")
	clk.Advance(20 * time.Minute)
	apiCall(h, "GET", "/v1/applications", sc, nil)
	apiCall(h, "POST", "/auth/logout", sc, nil)

	for _, secret := range []string{sc.Value, "at-c1", "rt-0", "idt-c1"} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("log contains %q:\n%s", secret, buf.String())
		}
	}
	if !strings.Contains(buf.String(), "person:admin signed in") {
		t.Errorf("sign-in not logged:\n%s", buf.String())
	}
}

func TestConsole_ProviderNotReady(t *testing.T) {
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	origin, _ := api.ParseOrigin(testOrigin, false)
	c := &api.Console{Handler: http.NotFoundHandler(), Origin: origin, Sessions: api.NewSessions(api.SessionConfig{Now: clk.Now}), Now: clk.Now}
	h := api.NewServer(newTestDB(t), event.NewInProcessBus(), map[string]string{"key-alice": "alice"}, nil, nil, nil, c).Handler
	w := serve(h, req("GET", "/auth/login", nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/signin?error=provider_unavailable" {
		t.Errorf("got %d to %q", w.Code, w.Header().Get("Location"))
	}
	r := req("GET", "/v1/applications", nil)
	r.Header.Set("Authorization", "Bearer key-alice")
	if w := serve(h, r); w.Code != http.StatusOK {
		t.Errorf("API key while identity isn't ready: %d", w.Code)
	}
}

func TestParseOrigin(t *testing.T) {
	cases := []struct {
		in       string
		insecure bool
		want     string
	}{
		{"http://localhost:8080", false, "http://localhost:8080"},
		{"http://localhost:8080/", false, "http://localhost:8080"},
		{"http://127.0.0.1:8080", false, "http://127.0.0.1:8080"},
		{"http://[::1]:8080", false, "http://[::1]:8080"},
		{"https://serendip.example", false, "https://serendip.example"},
		{"http://192.168.1.5:8080", false, ""},
		{"http://192.168.1.5:8080", true, "http://192.168.1.5:8080"},
		{"http://localhost:8080/console", false, ""},
		{"http://localhost:8080?x=1", false, ""},
		{"ftp://localhost", false, ""},
		{"localhost:8080", false, ""},
		{"http://user@localhost:8080", false, ""},
		{"HTTP://LocalHost:8080", false, "http://localhost:8080"},
		{"https://Serendip.Example:443", false, "https://serendip.example"},
		{"http://localhost:80", false, "http://localhost"},
	}
	for _, tc := range cases {
		got, err := api.ParseOrigin(tc.in, tc.insecure)
		if tc.want == "" {
			if err == nil {
				t.Errorf("ParseOrigin(%q, %v) = %v, want an error", tc.in, tc.insecure, got)
			}
			continue
		}
		if err != nil || got.String() != tc.want {
			t.Errorf("ParseOrigin(%q, %v) = %v, %v; want %s", tc.in, tc.insecure, got, err, tc.want)
		}
	}
}

// flakyAuthn rejects the first refreshed access token as "not ready", as a
// JWKS fetch timeout would, then accepts.
type flakyAuthn struct{ fails atomic.Int32 }

func (f *flakyAuthn) Authenticate(ctx context.Context, token string) (identity.Principal, error) {
	if token == "at-refreshed" && f.fails.Add(1) == 1 {
		return identity.Principal{}, reconcile.ErrProviderNotReady
	}
	return consoleAuthn{}.Authenticate(ctx, token)
}

// recordingLogin remembers which refresh tokens were presented.
type recordingLogin struct {
	*fakeLogin
	mu        sync.Mutex
	presented []string
}

func (r *recordingLogin) Refresh(ctx context.Context, rt string) (identity.Tokens, error) {
	r.mu.Lock()
	r.presented = append(r.presented, rt)
	r.mu.Unlock()
	return r.fakeLogin.Refresh(ctx, rt)
}

func TestConsole_RotatedRefreshTokenSurvivesAFailedVerification(t *testing.T) {
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	rl := &recordingLogin{fakeLogin: &fakeLogin{clk: clk}}
	origin, _ := api.ParseOrigin(testOrigin, false)
	c := &api.Console{Handler: http.NotFoundHandler(), Origin: origin, Login: func() identity.LoginClient { return rl },
		Sessions: api.NewSessions(api.SessionConfig{Now: clk.Now}), Now: clk.Now}
	h := api.NewServer(newTestDB(t), event.NewInProcessBus(), nil, &flakyAuthn{}, nil, nil, c).Handler
	sc := signIn(t, h)
	keepActive(h, clk, sc, 40*time.Minute)
	clk.Advance(20 * time.Minute)

	if w := apiCall(h, "GET", "/v1/session", sc, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("first refresh: %d, want 503 (verification not ready)", w.Code)
	}
	if w := apiCall(h, "GET", "/v1/session", sc, nil); w.Code != http.StatusOK {
		t.Fatalf("second refresh: %d, want 200", w.Code)
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if len(rl.presented) != 2 || rl.presented[0] != "rt-0" || rl.presented[1] != "rt-1" {
		t.Errorf("refresh tokens presented = %v, want [rt-0 rt-1] (never the retired one again)", rl.presented)
	}
}
