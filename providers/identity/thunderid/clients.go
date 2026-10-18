package thunderid

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// The management API is reached as ThunderID's own built-in console client
// with the "system" scope (specs/016-ui-mvp plan.md decision 7, T001 spike
// finding 1). The resource indicator is required: without it authorize
// fails with invalid_target (spec 015 spike finding 10).
const (
	consoleClientID = "CONSOLE"
	systemScope     = "openid system"
	systemResource  = "https://localhost:8090/mcp"
	// credentialsAction is the password step's action ID in the default
	// authentication flow shipped with 1.0.1.
	credentialsAction = "action_001"
	maxManagementBody = 1 << 20
)

// ProxyBaseURL is ThunderID's base URL through the Kubernetes API's Service
// proxy: where serendipd makes its server-side calls (token, revocation,
// management) without depending on the host resolving the issuer's name.
func ProxyBaseURL(client *kubernetes.Client) string {
	return client.ProxyURL(Namespace, ServiceName, ServicePort)
}

// EnsureClientRedirects makes sure the serendip client accepts the
// console's login callback and post-logout page.
//
// It first asks the provider's public endpoints, with no credentials,
// whether both URIs are already accepted. A fresh install registers them
// through the bootstrap file (Deployer.SetConsoleRedirects), so this is the
// normal path, and it needs no privilege at all.
//
// When a URI is missing (an install that predates the console, or a
// changed public URL), it returns an error naming the URIs and the
// documented one-time procedure. It does not try the management API: that
// needs the administrator's token, and the Kubernetes Service proxy strips
// the Authorization header (spec 016 T010), so every attempt would be an
// admin login for nothing, repeated by the foundations loop.
// The update itself is UpdateClient, run by an operator
// through a tunnel that preserves headers (plan.md "T010 finding").
func EnsureClientRedirects(ctx context.Context, client *kubernetes.Client, redirectURI, postLogoutURI string) error {
	base, hc := ProxyBaseURL(client), noRedirects(client.HTTPClient())
	ok, err := redirectsAccepted(ctx, base, hc, redirectURI, postLogoutURI)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return fmt.Errorf("thunderid: the %s client doesn't accept the console's URIs (%s, %s); register them once, "+
		"as docs/architecture/identity.md, section \"Console sessions\", describes", ClientID, redirectURI, postLogoutURI)
}

// redirectsAccepted probes the provider's public authorize and logout
// endpoints, the way a browser would, to learn whether both URIs are
// registered. No credential is sent, and nothing outlives the probe: an
// unfinished authorize or logout simply expires.
func redirectsAccepted(ctx context.Context, base string, hc *http.Client, redirectURI, postLogoutURI string) (bool, error) {
	_, challenge, err := pkcePair()
	if err != nil {
		return false, err
	}
	q := url.Values{
		"response_type": {"code"}, "client_id": {ClientID}, "redirect_uri": {redirectURI}, "scope": {"openid"},
		"state": {"serendip-probe"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	loc, status, err := probe(ctx, hc, base+"/oauth2/authorize?"+q.Encode())
	if err != nil {
		return false, fmt.Errorf("thunderid: check the console callback: %w", err)
	}
	switch {
	case status == http.StatusFound && strings.Contains(loc, "/gate/signin"):
	case status == http.StatusFound && strings.Contains(loc, "/gate/error"):
		return false, nil
	default:
		return false, fmt.Errorf("thunderid: check the console callback: unexpected status %d", status)
	}
	if postLogoutURI == "" {
		return true, nil
	}
	lq := url.Values{"client_id": {ClientID}, "post_logout_redirect_uri": {postLogoutURI}}
	_, status, err = probe(ctx, hc, base+"/oauth2/logout?"+lq.Encode())
	if err != nil {
		return false, fmt.Errorf("thunderid: check the console's post-logout page: %w", err)
	}
	switch status {
	case http.StatusFound:
		return true, nil
	case http.StatusBadRequest:
		return false, nil
	default:
		return false, fmt.Errorf("thunderid: check the console's post-logout page: unexpected status %d", status)
	}
}

func probe(ctx context.Context, hc *http.Client, target string) (location string, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", 0, err
	}
	resp.Body.Close()
	return resp.Header.Get("Location"), resp.StatusCode, nil
}

// noRedirects copies hc so it returns redirects instead of following them:
// the login flow reads its next step from the Location header.
func noRedirects(hc *http.Client) *http.Client {
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// UpdateClient brings an existing install's serendip client up to what a
// fresh install's bootstrap file sets, through the management API, logging
// in as the administrator:
//
//   - the console's callback and post-logout URIs, when given (empty ones
//     are skipped);
//   - every token attribute a fresh install copies into access tokens,
//     including "groups" (specs/017-authorization plan.md decision 9).
//
// Bootstrap files are imported only on first install, so an install that
// predates either change needs this once. baseURL must reach ThunderID
// without stripping the Authorization header, such as a kubectl
// port-forward; the Kubernetes Service proxy does strip it. serendipd
// itself never calls it (docs/architecture/identity.md "Console
// sessions"). It only ever adds, and writes once, or not at all when
// nothing is missing.
func UpdateClient(ctx context.Context, baseURL string, hc *http.Client, issuer, adminPassword, redirectURI, postLogoutURI string) error {
	return ensureClient(ctx, strings.TrimSuffix(baseURL, "/"), noRedirects(hc), issuer, adminPassword, redirectURI, postLogoutURI)
}

func ensureClient(ctx context.Context, base string, hc *http.Client, issuer, password, redirectURI, postLogoutURI string) error {
	token, _, err := managementToken(ctx, base, hc, issuer, password)
	if err != nil {
		return err
	}
	appURL := base + "/applications/" + clientAppID
	var app map[string]any
	if err := callJSON(ctx, hc, http.MethodGet, appURL, token, nil, &app); err != nil {
		return fmt.Errorf("thunderid: read the %s client: %w", ClientID, err)
	}
	cfg, err := oauthConfig(app)
	if err != nil {
		return err
	}
	if id, _ := cfg["clientId"].(string); id != ClientID {
		return fmt.Errorf("thunderid: application %s is client %q, want %q", clientAppID, id, ClientID)
	}
	changed := addURI(cfg, "redirectUris", redirectURI)
	changed = addURI(cfg, "postLogoutRedirectUris", postLogoutURI) || changed
	added, err := addTokenAttributes(cfg)
	if err != nil {
		return err
	}
	if !changed && !added {
		return nil
	}
	// PUT replaces the whole document: send back everything that was read
	// (spec 016 T001 spike finding 1), with only the lists extended.
	if err := callJSON(ctx, hc, http.MethodPut, appURL, token, app, nil); err != nil {
		return fmt.Errorf("thunderid: update the %s client: %w", ClientID, err)
	}
	return nil
}

// addTokenAttributes appends to the access token's attribute allow-list
// (token.accessToken.userConfig.attributes, spec 017 T001 spike finding 4)
// each of tokenAttributes it lacks, and reports whether it added any. A
// client without that structure is an error, not something to invent.
func addTokenAttributes(cfg map[string]any) (bool, error) {
	tok, _ := cfg["token"].(map[string]any)
	at, _ := tok["accessToken"].(map[string]any)
	user, ok := at["userConfig"].(map[string]any)
	if !ok {
		return false, fmt.Errorf("thunderid: the %s client has no token.accessToken.userConfig", ClientID)
	}
	list, _ := user["attributes"].([]any)
	added := false
	for _, a := range tokenAttributes {
		if !slices.Contains(list, any(a)) {
			list = append(list, a)
			added = true
		}
	}
	user["attributes"] = list
	return added, nil
}

func oauthConfig(app map[string]any) (map[string]any, error) {
	inbound, _ := app["inboundAuthConfig"].([]any)
	for _, entry := range inbound {
		e, _ := entry.(map[string]any)
		if t, _ := e["type"].(string); t != "oauth2" {
			continue
		}
		if cfg, ok := e["config"].(map[string]any); ok {
			return cfg, nil
		}
	}
	return nil, fmt.Errorf("thunderid: the %s client has no oauth2 configuration", ClientID)
}

// addURI appends uri to cfg[key] unless it is already there, and reports
// whether it did.
func addURI(cfg map[string]any, key, uri string) bool {
	if uri == "" {
		return false
	}
	list, _ := cfg[key].([]any)
	if slices.ContainsFunc(list, func(v any) bool { s, _ := v.(string); return s == uri }) {
		return false
	}
	cfg[key] = append(list, uri)
	return true
}

// managementToken logs in as the administrator through the console client,
// driving the same steps ThunderID's own sign-in page does: authorize, the
// credentials flow, the flow callback, then the code exchange with PKCE. It
// returns the token and how long it lasts (expires_in; zero when the
// provider doesn't say). Errors never include the password, code or token.
func managementToken(ctx context.Context, base string, hc *http.Client, issuer, password string) (string, time.Duration, error) {
	verifier, challenge, err := pkcePair()
	if err != nil {
		return "", 0, err
	}
	redirect := strings.TrimSuffix(issuer, "/") + "/console"
	q := url.Values{
		"response_type": {"code"}, "client_id": {consoleClientID}, "redirect_uri": {redirect},
		"scope": {systemScope}, "resource": {systemResource}, "state": {"serendip"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/oauth2/authorize?"+q.Encode(), nil)
	if err != nil {
		return "", 0, fmt.Errorf("thunderid: management login: %w", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("thunderid: management login: authorize: %w", err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusFound || err != nil {
		return "", 0, fmt.Errorf("thunderid: management login: authorize: %s", resp.Status)
	}
	execID, authID := loc.Query().Get("executionId"), loc.Query().Get("authId")
	if execID == "" || authID == "" {
		return "", 0, errors.New("thunderid: management login: authorize did not start a flow")
	}

	var step struct {
		ChallengeToken string `json:"challengeToken"`
	}
	if err := callJSON(ctx, hc, http.MethodPost, base+"/flow/execute", "", map[string]any{"executionId": execID}, &step); err != nil {
		return "", 0, fmt.Errorf("thunderid: management login: start flow: %w", err)
	}
	var done struct {
		Assertion  string `json:"assertion"`
		FlowStatus string `json:"flowStatus"`
	}
	if err := callJSON(ctx, hc, http.MethodPost, base+"/flow/execute", "", map[string]any{
		"executionId": execID, "challengeToken": step.ChallengeToken, "action": credentialsAction,
		"inputs": map[string]string{"username": AdminUsername, "password": password},
	}, &done); err != nil {
		return "", 0, fmt.Errorf("thunderid: management login: credentials: %w", err)
	}
	if done.Assertion == "" {
		return "", 0, fmt.Errorf("thunderid: management login: credentials not accepted (flow status %q)", done.FlowStatus)
	}
	var cb struct {
		RedirectURI string `json:"redirect_uri"`
	}
	if err := callJSON(ctx, hc, http.MethodPost, base+"/oauth2/auth/callback", "", map[string]string{"assertion": done.Assertion, "authId": authID}, &cb); err != nil {
		return "", 0, fmt.Errorf("thunderid: management login: callback: %w", err)
	}
	back, err := url.Parse(cb.RedirectURI)
	if err != nil || back.Query().Get("code") == "" {
		return "", 0, errors.New("thunderid: management login: no authorization code")
	}

	form := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {consoleClientID}, "code": {back.Query().Get("code")},
		"redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {systemResource},
	}
	treq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("thunderid: management login: %w", err)
	}
	treq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tresp, err := hc.Do(treq)
	if err != nil {
		return "", 0, fmt.Errorf("thunderid: management login: token: %w", err)
	}
	defer tresp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if tresp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(tresp.Body, maxManagementBody)).Decode(&tok) != nil || tok.AccessToken == "" {
		return "", 0, fmt.Errorf("thunderid: management login: token: %s", tresp.Status)
	}
	return tok.AccessToken, time.Duration(tok.ExpiresIn) * time.Second, nil
}

// statusError is a non-2xx answer from the management API. It carries the
// status only, never the body.
type statusError struct {
	method, path, status string
	code                 int
}

func (e *statusError) Error() string { return fmt.Sprintf("%s %s: %s", e.method, e.path, e.status) }

func pkcePair() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// callJSON sends body (if any) as JSON and decodes a 2xx reply into out (if
// any). A failure reports only the status, never the body, which may echo
// credentials.
func callJSON(ctx context.Context, hc *http.Client, method, target, bearer string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Drain a little so the connection (a port-forward tunnel, for the
		// directory) can be reused.
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return &statusError{method: method, path: req.URL.Path, status: resp.Status, code: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxManagementBody)).Decode(out)
}
