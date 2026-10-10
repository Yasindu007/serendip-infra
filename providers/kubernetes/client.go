package kubernetes

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrNotFound is returned by Get/GetRaw/Delete when the Kubernetes API
// responds 404 -- callers (capability-specific providers) use this to
// distinguish "doesn't exist yet, create it" from a real failure,
// mirroring core/store.ErrNotFound's existing sentinel-error pattern.
var ErrNotFound = errors.New("kubernetes: object not found")

// ObjectMeta is the subset of Kubernetes' metadata every object shares,
// enough to address, create, and identify one.
type ObjectMeta struct {
	Name            string            `json:"name,omitempty"`
	Namespace       string            `json:"namespace,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
}

// Object is the minimal envelope every Kubernetes API object shares.
// Spec/Status are left as raw JSON -- this package knows nothing about
// any specific kind's schema; callers (capability-specific providers)
// unmarshal these into their own kind-specific structs, defining only
// the fields they actually touch (same pattern agent/client established
// for core/resource's wire shape).
type Object struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   ObjectMeta      `json:"metadata"`
	Spec       json.RawMessage `json:"spec,omitempty"`
	Status     json.RawMessage `json:"status,omitempty"`
}

// listResult mirrors a Kubernetes List response's envelope -- the
// actual objects live under "items".
type listResult struct {
	Items []Object `json:"items"`
}

// Client is a minimal net/http-based REST client for the Kubernetes
// API, TLS-configured from a Config. It has no knowledge of which
// object kind lives at which REST path -- callers supply the full path.
type Client struct {
	baseURL string
	http    *http.Client
	token   string
}

// NewClient builds a Client from cfg, configuring TLS from whichever
// auth method cfg carries (client cert or bearer token -- never both,
// per ParseConfig's own invariant).
func NewClient(cfg Config) (*Client, error) {
	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURL: cfg.Host,
		token:   cfg.BearerToken,
		http: &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
		},
	}, nil
}

func buildTLSConfig(cfg Config) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cfg.CAData) {
		return nil, fmt.Errorf("kubernetes: no valid CA certificates found in kubeconfig's certificate-authority-data")
	}
	tlsConfig := &tls.Config{RootCAs: pool}

	if len(cfg.ClientCertData) > 0 {
		cert, err := tls.X509KeyPair(cfg.ClientCertData, cfg.ClientKeyData)
		if err != nil {
			return nil, fmt.Errorf("kubernetes: load client cert/key pair: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	return tlsConfig, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("kubernetes: marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: build %s %s request: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: %s %s: %w", method, path, err)
	}
	return resp, nil
}

// Get fetches the object at path (e.g. "/api/v1/nodes/my-node").
// Returns ErrNotFound on a 404.
func (c *Client) Get(ctx context.Context, path string) (Object, error) {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return Object{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Object{}, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return Object{}, unexpectedStatus(resp)
	}
	var obj Object
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return Object{}, fmt.Errorf("kubernetes: decode GET %s response: %w", path, err)
	}
	return obj, nil
}

// GetRaw fetches the raw JSON body at path, for object kinds that don't
// fit Object's spec/status envelope convention -- e.g. StorageClass,
// whose configuration fields (provisioner, volumeBindingMode,
// reclaimPolicy) are top-level, not nested under "spec" like every
// other kind this project has touched so far. Returns ErrNotFound on a
// 404.
func (c *Client) GetRaw(ctx context.Context, path string) (json.RawMessage, error) {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, unexpectedStatus(resp)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: read GET %s response: %w", path, err)
	}
	return raw, nil
}

// CreateRaw POSTs body (any JSON-marshalable value) to path and returns
// the raw JSON response -- the RawMessage counterpart to Create, for
// object kinds that don't fit Object's envelope (see GetRaw).
func (c *Client) CreateRaw(ctx context.Context, path string, body any) (json.RawMessage, error) {
	resp, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, unexpectedStatus(resp)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: read POST %s response: %w", path, err)
	}
	return raw, nil
}

// Create POSTs obj to the given collection path (e.g.
// "/api/v1/namespaces/default/persistentvolumeclaims").
func (c *Client) Create(ctx context.Context, path string, obj Object) (Object, error) {
	resp, err := c.do(ctx, http.MethodPost, path, obj)
	if err != nil {
		return Object{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return Object{}, unexpectedStatus(resp)
	}
	var created Object
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return Object{}, fmt.Errorf("kubernetes: decode POST %s response: %w", path, err)
	}
	return created, nil
}

// Delete removes the object at path. Returns ErrNotFound on a 404 --
// callers that want "already gone is fine" semantics check for it
// explicitly (the generic client reports what actually happened; a
// specific provider's business logic decides whether that's an error).
func (c *Client) Delete(ctx context.Context, path string) error {
	resp, err := c.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
		return unexpectedStatus(resp)
	}
	return nil
}

// List fetches every object at a collection path (e.g.
// "/api/v1/nodes").
func (c *Client) List(ctx context.Context, path string) ([]Object, error) {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, unexpectedStatus(resp)
	}
	var result listResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("kubernetes: decode LIST %s response: %w", path, err)
	}
	return result.Items, nil
}

func unexpectedStatus(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("kubernetes: unexpected status %d: %s", resp.StatusCode, string(body))
}
