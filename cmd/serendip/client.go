package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/serendip-infra/serendip-infra/api/catalog"
)

// maxBody bounds what the CLI reads from any server.
const maxBody = 8 << 20

// newHTTPClient returns a client that resolves every "*.localhost" name to
// the loopback address, as RFC 6761 allows and browsers and curl do (Go's
// resolver doesn't on Windows, and a local install's identity provider is
// identity.localhost), and never sends such a request through a proxy.
func newHTTPClient() *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		Proxy: func(r *http.Request) (*url.URL, error) {
			if isLocalhost(r.URL.Hostname()) {
				return nil, nil
			}
			return http.ProxyFromEnvironment(r)
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err == nil && isLocalhost(host) {
				addr = net.JoinHostPort("127.0.0.1", port)
			}
			return d.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: tr,
		// Never follow a redirect with a bearer token on it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func isLocalhost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// checkURL refuses anything but https, or http to this machine, unless
// insecure: a bearer token or a refresh token must never cross a network
// in clear text.
func checkURL(raw string, insecure bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%q isn't an absolute URL", raw)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && (insecure || isLocalhost(u.Hostname())):
	case u.Scheme == "http":
		return nil, fmt.Errorf("%s is plain http to another machine; use https, or --insecure if you accept sending credentials in clear text", raw)
	default:
		return nil, fmt.Errorf("%s: only http and https are allowed", raw)
	}
	return u, nil
}

// sameServer reports whether two server addresses name the same server.
func sameServer(a, b string) bool {
	norm := func(s string) string {
		u, err := url.Parse(strings.TrimSpace(s))
		if err != nil {
			return s
		}
		return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimSuffix(u.Path, "/")
	}
	return norm(a) == norm(b)
}

// credentials is the signed-in session, kept in the user's configuration
// directory, readable by them alone (spec 021 scope item 7).
type credentials struct {
	Server        string    `json:"server"`
	Issuer        string    `json:"issuer"`
	ClientID      string    `json:"clientId"`
	AssistantID   string    `json:"assistantClientId,omitempty"`
	TokenURL      string    `json:"tokenEndpoint"`
	RevocationURL string    `json:"revocationEndpoint,omitempty"`
	AccessToken   string    `json:"accessToken"`
	RefreshToken  string    `json:"refreshToken,omitempty"`
	Expiry        time.Time `json:"expiry"`
}

// configDir is where the CLI keeps its files: SERENDIP_CONFIG_DIR, or
// "serendip" under the user's configuration directory. On Windows that is
// under %AppData%, which the user profile already restricts to its owner
// and administrators; a SERENDIP_CONFIG_DIR elsewhere must be as private.
func configDir(env func(string) string) (string, error) {
	if d := env("SERENDIP_CONFIG_DIR"); d != "" {
		return d, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find the configuration directory: %w", err)
	}
	return filepath.Join(d, "serendip"), nil
}

func credentialsPath(dir string) string { return filepath.Join(dir, "credentials.json") }

func loadCredentials(dir string) (*credentials, error) {
	var b []byte
	var err error
	// A Windows reader can briefly meet the file mid-rename.
	for range 5 {
		b, err = os.ReadFile(credentialsPath(dir))
		if err == nil || errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s is damaged; sign in again: %w", credentialsPath(dir), err)
	}
	return &c, nil
}

// saveCredentials writes c with owner-only permissions, through a synced
// temporary file renamed into place, so a crash never leaves half a file
// and a rotated refresh token is never lost. Callers hold the lock.
func saveCredentials(dir string, c *credentials) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	// On Windows Chmod only sets the read-only flag; the directory's ACL is
	// what keeps the file private there.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// On Windows the rename fails while another process has the file open
	// for reading; such reads are short, so retry for a while.
	for i := 0; ; i++ {
		err = os.Rename(tmp.Name(), credentialsPath(dir))
		if err == nil || i == 20 {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Lock timings: a holder refreshes over one HTTP request (30 s at most), so
// waiters wait longer than that, and only a lock older than any refresh can
// be is taken over.
const (
	lockWait  = 45 * time.Second
	lockStale = 2 * time.Minute
)

// lockCredentials serialises changes to the sign-in between CLI processes:
// rotated refresh tokens are single-use, and ThunderID revokes the whole
// family when one is replayed (spec 021 T001), so two processes refreshing
// at once would sign the person out.
//
// The lock is a file holding a random nonce. A stale lock is taken over by
// renaming it away first, so two waiters can't both break it, and unlocking
// removes the file only while it still holds this holder's nonce.
func lockCredentials(ctx context.Context, dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "credentials.lock")
	nonce := make([]byte, 16)
	rand.Read(nonce) // never fails (crypto/rand, Go 1.24+)
	mine := hex.EncodeToString(nonce)
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, werr := f.WriteString(mine)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				os.Remove(path)
				return nil, errors.Join(werr, cerr)
			}
			return func() {
				if b, err := os.ReadFile(path); err == nil && string(b) == mine {
					os.Remove(path)
				}
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if st, serr := os.Stat(path); serr == nil && time.Since(st.ModTime()) > lockStale {
			// Whoever renames it away is the one who broke it.
			aside := path + "." + mine
			if os.Rename(path, aside) == nil {
				os.Remove(aside)
			}
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another serendip command holds %s; retry, or remove it if none is running", path)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// apiClient sends requests to Serendip's API.
type apiClient struct {
	server string
	hc     *http.Client
	// bearer returns the credential for each request.
	bearer func(ctx context.Context) (string, error)
	// clientName is what X-Serendip-Client declares (cli or mcp).
	clientName string
}

type response struct {
	Status int
	Header http.Header
	Body   []byte
}

// errTooLarge is an answer over maxBody.
var errTooLarge = fmt.Errorf("the answer is larger than %d bytes; narrow the request", maxBody)

// readBounded reads at most maxBody bytes, and fails rather than return a
// truncated answer as if it were whole.
func readBounded(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBody {
		return nil, errTooLarge
	}
	return b, nil
}

func (c *apiClient) do(ctx context.Context, r catalog.Request) (response, error) {
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, strings.TrimSuffix(c.server, "/")+r.Path, body)
	if err != nil {
		return response{}, err
	}
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Serendip-Client", c.clientName)
	tok, err := c.bearer(ctx)
	if err != nil {
		return response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.hc.Do(req)
	if err != nil {
		// A *url.Error names the server and path, which the person chose;
		// the token is in a header and never in the error.
		return response{}, err
	}
	defer resp.Body.Close()
	b, err := readBounded(resp.Body)
	if err != nil {
		return response{}, err
	}
	return response{Status: resp.StatusCode, Header: resp.Header, Body: b}, nil
}
