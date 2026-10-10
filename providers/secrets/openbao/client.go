package openbao

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// ErrSecretNotFound is returned by ReadSecret/DeleteSecret when
// OpenBao responds 404 -- mirrors providers/kubernetes.Client's
// ErrNotFound convention.
var ErrSecretNotFound = errors.New("openbao: secret not found")

// DefaultKVMount is OpenBao's default KV v2 secrets-engine mount path.
const DefaultKVMount = "secret"

// Client is a minimal net/http-based REST client for OpenBao's HTTP
// API -- health, init, unseal, and KV v2 read/write/delete only (see
// doc.go for why this isn't the official SDK). baseURL is expected to
// be a providers/kubernetes.Client.ProxyURL result in real usage, but
// this package has no import on providers/kubernetes -- it just needs
// a base URL and an *http.Client, however the caller constructed them.
//
// Once registered in core/provider.Registry, a single Client instance
// is shared for the process lifetime, and SetToken is called exactly
// once at startup (Bootstrap) after the token wasn't known yet at
// construction time -- tokenMu guards that one write against any
// concurrent do() read, even though in practice Bootstrap always
// completes before any other caller obtains the registered Client.
type Client struct {
	baseURL string
	http    *http.Client

	tokenMu sync.RWMutex
	token   string
}

// NewClient builds a Client against baseURL (no trailing slash)
// using httpClient for transport.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, http: httpClient}
}

// SetToken sets the X-Vault-Token header value used by every
// subsequent request. OpenBao retains this header name for backward
// compatibility with Vault's wire protocol.
func (c *Client) SetToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.token = token
}

func (c *Client) currentToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("openbao: marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("openbao: build %s %s request: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := c.currentToken(); token != "" {
		req.Header.Set("X-Vault-Token", token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openbao: %s %s: %w", method, path, err)
	}
	return resp, nil
}

// HealthStatus is the subset of OpenBao's /v1/sys/health response this
// provider needs.
type HealthStatus struct {
	Initialized bool `json:"initialized"`
	Sealed      bool `json:"sealed"`
}

// Health reports whether the OpenBao instance is initialized and
// sealed. It requests normalized response codes
// (standbyok/uninitcode/sealedcode all mapped to 200) so every
// documented health state comes back as a plain 200 with a JSON body
// -- this provider has no need to distinguish standby/DR-secondary/
// performance-standby states, only initialized/sealed.
func (c *Client) Health(ctx context.Context) (HealthStatus, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/sys/health?standbyok=true&uninitcode=200&sealedcode=200", nil)
	if err != nil {
		return HealthStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return HealthStatus{}, unexpectedStatus(resp)
	}
	var status HealthStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return HealthStatus{}, fmt.Errorf("openbao: decode health response: %w", err)
	}
	return status, nil
}

type initRequest struct {
	SecretShares    int `json:"secret_shares"`
	SecretThreshold int `json:"secret_threshold"`
}

type initResponse struct {
	Keys      []string `json:"keys"`
	RootToken string   `json:"root_token"`
}

// Init initializes a fresh OpenBao instance with a Shamir split of
// shares/threshold, returning the resulting unseal key shares and the
// root token. Spec 008 always calls this with (1, 1) per ADR 0009's
// documented single-share custody simplification.
func (c *Client) Init(ctx context.Context, shares, threshold int) (unsealKeys []string, rootToken string, err error) {
	resp, err := c.do(ctx, http.MethodPost, "/v1/sys/init", initRequest{SecretShares: shares, SecretThreshold: threshold})
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", unexpectedStatus(resp)
	}
	var result initResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("openbao: decode init response: %w", err)
	}
	return result.Keys, result.RootToken, nil
}

type unsealRequest struct {
	Key string `json:"key"`
}

type unsealResponse struct {
	Sealed bool `json:"sealed"`
}

// Unseal submits one unseal key share. Returns the resulting sealed
// state (false once enough shares have been submitted -- always true
// after exactly one call for spec 008's 1-of-1 threshold).
func (c *Client) Unseal(ctx context.Context, key string) (sealed bool, err error) {
	resp, err := c.do(ctx, http.MethodPost, "/v1/sys/unseal", unsealRequest{Key: key})
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, unexpectedStatus(resp)
	}
	var result unsealResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("openbao: decode unseal response: %w", err)
	}
	return result.Sealed, nil
}

// mountsResponse is the subset of /v1/sys/mounts' response this
// provider needs -- just enough to check whether a given mount path
// already exists. OpenBao/Vault's response duplicates every mount
// both at the top level (legacy shape, pre-dates the "data" wrapper)
// and under "data" (current shape) -- this provider only reads the
// top-level keys, which every version of the API has always included.
type mountsResponse map[string]json.RawMessage

// EnsureKVv2Mount idempotently enables a KV v2 secrets engine at
// mount (no trailing slash) -- unlike "-dev" mode, a production
// OpenBao instance mounts nothing but its built-in system backends
// (sys/, cubbyhole/, identity/) after Init, so ReadSecret/WriteSecret
// 404 with "no handler for route" against a fresh instance until this
// runs at least once. Checks for an existing mount first (GET
// /v1/sys/mounts) rather than unconditionally POSTing, since OpenBao
// errors on enabling a path that's already in use -- this makes the
// call safe to repeat on every Bootstrap call, which matters because
// an instance can reach "initialized" (permanently, OpenBao's own
// state) while a later step in the same Bootstrap call still fails,
// leaving a future restart's Bootstrap call on the "already
// initialized" branch forever, with no other path back to this check.
func (c *Client) EnsureKVv2Mount(ctx context.Context, mount string) error {
	resp, err := c.do(ctx, http.MethodGet, "/v1/sys/mounts", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return unexpectedStatus(resp)
	}
	var mounts mountsResponse
	if err := json.NewDecoder(resp.Body).Decode(&mounts); err != nil {
		return fmt.Errorf("openbao: decode mounts response: %w", err)
	}
	if _, exists := mounts[mount+"/"]; exists {
		return nil
	}

	enableResp, err := c.do(ctx, http.MethodPost, "/v1/sys/mounts/"+mount, map[string]any{
		"type":    "kv",
		"options": map[string]string{"version": "2"},
	})
	if err != nil {
		return err
	}
	defer enableResp.Body.Close()
	if enableResp.StatusCode != http.StatusNoContent && enableResp.StatusCode != http.StatusOK {
		return unexpectedStatus(enableResp)
	}
	return nil
}

type kvV2WriteRequest struct {
	Data map[string]any `json:"data"`
}

type kvV2ReadResponse struct {
	Data struct {
		Data map[string]any `json:"data"`
	} `json:"data"`
}

// ReadSecret GETs the KV v2 secret at mount/path, unwrapping the
// double-nested {"data":{"data":{...},"metadata":{...}}} response
// shape down to just the inner data map. Returns ErrSecretNotFound on
// a 404.
func (c *Client) ReadSecret(ctx context.Context, mount, path string) (map[string]any, error) {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/%s/data/%s", mount, path), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrSecretNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, unexpectedStatus(resp)
	}
	var result kvV2ReadResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openbao: decode read-secret response: %w", err)
	}
	return result.Data.Data, nil
}

// WriteSecret PUTs data as the KV v2 secret at mount/path.
func (c *Client) WriteSecret(ctx context.Context, mount, path string, data map[string]any) error {
	resp, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/v1/%s/data/%s", mount, path), kvV2WriteRequest{Data: data})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return unexpectedStatus(resp)
	}
	return nil
}

// DeleteSecret permanently removes every version of the KV v2 secret
// at mount/path -- hits the metadata endpoint (not data), a full
// delete rather than KV v2's soft/recoverable delete-latest-version
// semantics, since this provider has no use for undelete at this
// stage. A 404 is treated as success, not an error -- already gone is
// fine, mirrors providers/kubernetes.Client's Delete-on-not-found
// callers' convention for this same case.
func (c *Client) DeleteSecret(ctx context.Context, mount, path string) error {
	resp, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/v1/%s/metadata/%s", mount, path), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return unexpectedStatus(resp)
	}
	return nil
}

// unexpectedStatus embeds the response body verbatim (up to 4096
// bytes) in the returned error, same convention as
// providers/kubernetes.Client's sibling helper. OpenBao/Vault's
// documented error-response shape is {"errors": [...]} (message
// strings, never a request-data echo), so this can't surface a root
// token or unseal key (those only ever appear in a successful 200
// body, never passed here) -- but callers of ReadSecret/WriteSecret
// still shouldn't log an error value from this package without
// awareness that it may embed OpenBao's own error text.
func unexpectedStatus(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("openbao: unexpected status %d: %s", resp.StatusCode, string(body))
}
