package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// loopbackAddr is where the identity provider sends the sign-in answer: the
// fixed loopback registered on the CLI client, which the provider matches
// exactly (spec 021 plan decision 2).
const loopbackAddr = "127.0.0.1:8250"

// metadata is the part of Serendip's RFC 9728 document the CLI uses.
type metadata struct {
	Issuers         []string `json:"authorization_servers"`
	CLIClient       string   `json:"serendip_cli_client_id"`
	AssistantClient string   `json:"serendip_assistant_client_id"`
}

// endpoints is the part of the issuer's OpenID discovery the CLI uses.
type endpoints struct {
	Issuer     string `json:"issuer"`
	Authorize  string `json:"authorization_endpoint"`
	Token      string `json:"token_endpoint"`
	Revocation string `json:"revocation_endpoint"`
}

func getJSON(ctx context.Context, hc *http.Client, target string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", target, resp.Status)
	}
	b, err := readBounded(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// discover learns from the server which identity provider and client to
// sign in with, then the provider's endpoints. Every endpoint must be on
// the issuer's own origin, over https or to this machine: the CLI opens
// the authorize URL in a browser and later sends refresh tokens to the
// token endpoint, so neither may point anywhere else.
func discover(ctx context.Context, hc *http.Client, server string, insecure bool) (metadata, endpoints, error) {
	var m metadata
	if err := getJSON(ctx, hc, strings.TrimSuffix(server, "/")+"/.well-known/oauth-protected-resource", &m); err != nil {
		return m, endpoints{}, fmt.Errorf("ask %s how to sign in: %w", server, err)
	}
	if len(m.Issuers) != 1 || m.CLIClient == "" {
		return m, endpoints{}, fmt.Errorf("%s doesn't name one identity provider and CLI client", server)
	}
	issuer := strings.TrimSuffix(m.Issuers[0], "/")
	iu, err := checkURL(issuer, insecure)
	if err != nil {
		return m, endpoints{}, fmt.Errorf("identity provider: %w", err)
	}
	var e endpoints
	if err := getJSON(ctx, hc, issuer+"/.well-known/openid-configuration", &e); err != nil {
		return m, e, fmt.Errorf("read the identity provider's configuration: %w", err)
	}
	if strings.TrimSuffix(e.Issuer, "/") != issuer || e.Authorize == "" || e.Token == "" {
		return m, e, fmt.Errorf("the identity provider's configuration doesn't match issuer %s", issuer)
	}
	for _, raw := range []string{e.Authorize, e.Token, e.Revocation} {
		if raw == "" {
			continue
		}
		u, err := checkURL(raw, insecure)
		if err != nil {
			return m, e, fmt.Errorf("identity provider endpoint: %w", err)
		}
		if u.Scheme != iu.Scheme || !strings.EqualFold(u.Host, iu.Host) {
			return m, e, fmt.Errorf("identity provider endpoint %s isn't on the issuer's origin %s", raw, iu.Scheme+"://"+iu.Host)
		}
	}
	return m, e, nil
}

// pkce returns a verifier and its S256 challenge. crypto/rand never fails
// (Go 1.24+), so its error is not checked here or in randomState.
func pkce() (verifier, challenge string) {
	b := make([]byte, 32)
	rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(h[:])
}

func randomState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// loginOptions are the parts of sign-in a test replaces.
type loginOptions struct {
	// listen is where the CLI waits for the answer: loopbackAddr, except
	// in tests, whose fake provider doesn't check the redirect.
	listen      string
	openBrowser func(string) error
	noBrowser   bool
	insecure    bool
	in          io.Reader
	out         io.Writer
	timeout     time.Duration
}

// login signs a person in with the authorization code flow and PKCE:
// through the browser and the loopback redirect, or, when the loopback
// can't be used or there's no browser, by having the person open the link
// anywhere and paste back the address it lands on (plan decision 2).
func login(ctx context.Context, hc *http.Client, server string, o loginOptions) (*credentials, error) {
	m, e, err := discover(ctx, hc, server, o.insecure)
	if err != nil {
		return nil, err
	}
	redirect := "http://" + loopbackAddr + "/callback"
	verifier, challenge := pkce()
	state := randomState()
	q := url.Values{
		"response_type": {"code"}, "client_id": {m.CLIClient}, "redirect_uri": {redirect},
		"scope": {"openid"}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	authURL := e.Authorize + "?" + q.Encode()

	var code string
	var ln net.Listener
	if !o.noBrowser {
		ln, err = net.Listen("tcp", o.listen)
		if err != nil {
			fmt.Fprintf(o.out, "Can't listen on %s (%v); signing in without it.\n", o.listen, err)
		}
	}
	if ln != nil {
		code, err = loopbackCode(ctx, ln, state, authURL, o)
	} else {
		code, err = pastedCode(state, authURL, o)
	}
	if err != nil {
		return nil, err
	}
	tok, err := tokenRequest(ctx, hc, e.Token, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {m.CLIClient}, "code": {code},
		"redirect_uri": {redirect}, "code_verifier": {verifier},
	})
	if err != nil {
		return nil, err
	}
	c := &credentials{
		Server: server, Issuer: strings.TrimSuffix(m.Issuers[0], "/"), ClientID: m.CLIClient, AssistantID: m.AssistantClient,
		TokenURL: e.Token, RevocationURL: e.Revocation,
	}
	tok.apply(c)
	return c, nil
}

// loopbackCode waits for the browser to bring the code to the loopback
// listener. Only an answer carrying this sign-in's state counts: anything
// else (another page, a forged request) gets 400 and the wait goes on, so
// a stray request can't abort the sign-in.
func loopbackCode(ctx context.Context, ln net.Listener, state, authURL string, o loginOptions) (string, error) {
	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		q := r.URL.Query()
		if r.URL.Path != "/callback" || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "<p>This isn't the answer to the sign-in serendip is waiting for.</p>")
			return
		}
		code, err := codeFrom(q, state)
		if err != nil {
			fmt.Fprintf(w, "<p>Sign-in failed: %s</p>", html.EscapeString(err.Error()))
		} else {
			fmt.Fprint(w, "<p>Signed in to Serendip. You can close this tab and return to the terminal.</p>")
		}
		select {
		case done <- result{code, err}:
		default:
		}
	})}
	go srv.Serve(ln)
	defer func() {
		// Let the answer page reach the browser before closing.
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()

	fmt.Fprintf(o.out, "Opening your browser to sign in. If it doesn't open, visit:\n\n  %s\n\n", authURL)
	if o.openBrowser != nil {
		if err := o.openBrowser(authURL); err != nil {
			fmt.Fprintf(o.out, "(The browser didn't open: %v)\n", err)
		}
	}
	timer := time.NewTimer(o.timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.code, r.err
	case <-timer.C:
		return "", errors.New("timed out waiting for the browser; run serendip login --no-browser to paste the address instead")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func pastedCode(state, authURL string, o loginOptions) (string, error) {
	fmt.Fprintf(o.out, "Open this link in a browser, on any device, and sign in:\n\n  %s\n\n", authURL)
	fmt.Fprintf(o.out, "The browser then goes to an address starting with http://%s/callback that won't load.\nPaste that whole address here: ", loopbackAddr)
	line, err := bufio.NewReader(o.in).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", errors.New("no address pasted")
	}
	u, err := url.Parse(strings.TrimSpace(line))
	if err != nil {
		return "", errors.New("that isn't an address")
	}
	return codeFrom(u.Query(), state)
}

// codeFrom reads the authorization code from a redirect's query. The state
// is checked first, so nothing without it (a forged redirect, or a forged
// error) is believed.
func codeFrom(q url.Values, state string) (string, error) {
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
		return "", errors.New("the sign-in answer doesn't belong to this sign-in (state mismatch); start again")
	}
	if e := q.Get("error"); e != "" {
		return "", fmt.Errorf("the identity provider refused: %s %s", clean(e), clean(q.Get("error_description")))
	}
	code := q.Get("code")
	if code == "" {
		return "", errors.New("the sign-in answer has no code")
	}
	return code, nil
}

// tokens is a token endpoint's answer.
type tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func (t tokens) apply(c *credentials) {
	c.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		c.RefreshToken = t.RefreshToken
	}
	life := time.Duration(t.ExpiresIn) * time.Second
	if life <= 0 {
		life = 5 * time.Minute
	}
	c.Expiry = time.Now().Add(life)
}

// tokenRequest posts a form to a token endpoint. Its errors carry the
// provider's error code and description, cleaned, never a token or code.
func tokenRequest(ctx context.Context, hc *http.Client, endpoint string, form url.Values) (tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return tokens{}, fmt.Errorf("token endpoint: %w", err)
	}
	defer resp.Body.Close()
	var body struct {
		tokens
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	raw, rerr := readBounded(resp.Body)
	if rerr != nil || json.Unmarshal(raw, &body) != nil || resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		if body.Error != "" {
			return tokens{}, fmt.Errorf("token endpoint: %s: %s", clean(body.Error), clean(body.ErrorDescription))
		}
		return tokens{}, fmt.Errorf("token endpoint: %s", resp.Status)
	}
	return body.tokens, nil
}

// errSignedOut means there's no usable sign-in for the server.
var errSignedOut = errors.New("not signed in: run serendip login")

// freshToken returns a current access token from the stored sign-in for
// server, refreshing it under the credentials lock when it's about to
// expire. A sign-in made for another server is never sent to this one.
func freshToken(ctx context.Context, hc *http.Client, dir, server string) (string, error) {
	c, err := loadCredentials(dir)
	if err != nil {
		return "", err
	}
	if err := usable(c, server); err != nil {
		return "", err
	}
	if time.Until(c.Expiry) > time.Minute {
		return c.AccessToken, nil
	}
	unlock, err := lockCredentials(ctx, dir)
	if err != nil {
		return "", err
	}
	defer unlock()
	// Another process may have refreshed, or signed out, while this one
	// waited.
	if c, err = loadCredentials(dir); err != nil {
		return "", err
	}
	if err := usable(c, server); err != nil {
		return "", err
	}
	if time.Until(c.Expiry) > time.Minute {
		return c.AccessToken, nil
	}
	if c.RefreshToken == "" {
		return "", fmt.Errorf("%w (the sign-in has expired)", errSignedOut)
	}
	t, err := tokenRequest(ctx, hc, c.TokenURL, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}, "client_id": {c.ClientID},
	})
	if err != nil {
		return "", fmt.Errorf("%w (the sign-in couldn't be renewed: %v)", errSignedOut, err)
	}
	t.apply(c)
	// Saved before use: the old refresh token is spent.
	if err := saveCredentials(dir, c); err != nil {
		return "", fmt.Errorf("save the renewed sign-in: %w", err)
	}
	return c.AccessToken, nil
}

// usable checks a stored sign-in belongs to server.
func usable(c *credentials, server string) error {
	if c == nil {
		return errSignedOut
	}
	if !sameServer(c.Server, server) {
		return fmt.Errorf("%w (signed in to %s, not %s)", errSignedOut, c.Server, server)
	}
	return nil
}

// logout revokes the stored sign-in at the identity provider, which also
// stops any AI assistant's further token exchanges (spec 021 T001), and
// deletes it. It holds the credentials lock, so a refresh can't write a
// new sign-in back after it. The local file goes even when revocation
// fails.
func logout(ctx context.Context, hc *http.Client, dir string, out io.Writer) error {
	unlock, err := lockCredentials(ctx, dir)
	if err != nil {
		return err
	}
	defer unlock()
	c, err := loadCredentials(dir)
	if err != nil {
		return err
	}
	if c == nil {
		fmt.Fprintln(out, "Not signed in.")
		return nil
	}
	var revokeErr error
	if c.RevocationURL != "" {
		for _, tok := range []string{c.RefreshToken, c.AccessToken} {
			if tok == "" {
				continue
			}
			if err := revoke(ctx, hc, c.RevocationURL, c.ClientID, tok); err != nil {
				revokeErr = err
			}
		}
	}
	if err := os.Remove(credentialsPath(dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if revokeErr != nil {
		return fmt.Errorf("signed out here, but the identity provider didn't confirm revoking the session: %w", revokeErr)
	}
	fmt.Fprintln(out, "Signed out.")
	return nil
}

func revoke(ctx context.Context, hc *http.Client, endpoint, clientID, tok string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(url.Values{"token": {tok}, "client_id": {clientID}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revocation endpoint: %s", resp.Status)
	}
	return nil
}

// openBrowser opens target, an http(s) URL discover checked, in the
// person's browser.
func openBrowser(target string) error {
	if !strings.HasPrefix(target, "https://") && !strings.HasPrefix(target, "http://") {
		return fmt.Errorf("refusing to open %q", target)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// clean strips control characters, including terminal escapes, from text
// a server supplied before it reaches the terminal.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}
