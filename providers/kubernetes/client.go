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
	"net/url"
)

// ErrNotFound is returned by Get/Delete when the Kubernetes API
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
	Annotations     map[string]string `json:"annotations,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	// Generation is server-set and read-only; sending it on create is
	// harmless (ignored), and it's what status.observedGeneration is
	// compared against.
	Generation int64 `json:"generation,omitempty"`
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
	// Data is a top-level field some kinds (ConfigMap, Secret) carry
	// instead of a nested Spec -- left as raw JSON for the same reason
	// Spec/Status are: this package has no per-kind schema knowledge.
	Data json.RawMessage `json:"data,omitempty"`
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
	// tls is the configuration http's transport uses, kept for connections
	// made outside it (dialWebSocket).
	tls *tls.Config
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
		tls:     tlsConfig,
		http: &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
		},
	}, nil
}

// HTTPClient returns the Client's already-TLS-configured, already-
// authenticated http.Client -- for a capability-specific provider that
// needs to reach something other than the Kubernetes API itself (e.g.
// a cluster-internal Service via ProxyURL) without reconstructing
// kubeconfig-derived TLS config independently.
func (c *Client) HTTPClient() *http.Client {
	return c.http
}

// ProxyURL builds the Kubernetes API server's Service proxy subresource
// path for the named Service in namespace, on port -- a stable, core
// Kubernetes API feature that forwards requests through the API
// server to a cluster-internal (e.g. ClusterIP) Service, reusing
// whichever authenticated connection the caller already has to the API
// server itself. The returned URL has no trailing slash; append a
// path like "/v1/sys/health" to reach that path on the proxied
// Service.
func (c *Client) ProxyURL(namespace, service string, port int) string {
	return fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s:%d/proxy", c.baseURL, namespace, service, port)
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
	return c.doWithContentType(ctx, method, path, body, "application/json")
}

func (c *Client) doWithContentType(ctx context.Context, method, path string, body any, contentType string) (*http.Response, error) {
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
		req.Header.Set("Content-Type", contentType)
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

// Patch applies patch to the object at path as a JSON merge patch (RFC
// 7386) and returns the resulting object. Merge patch rather than PUT
// because a PUT would round-trip through Object, silently dropping every
// metadata field this package doesn't model (finalizers, annotations,
// ownerReferences). Returns ErrNotFound on a 404.
func (c *Client) Patch(ctx context.Context, path string, patch any) (Object, error) {
	resp, err := c.doWithContentType(ctx, http.MethodPatch, path, patch, "application/merge-patch+json")
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
	var patched Object
	if err := json.NewDecoder(resp.Body).Decode(&patched); err != nil {
		return Object{}, fmt.Errorf("kubernetes: decode PATCH %s response: %w", path, err)
	}
	return patched, nil
}

// FieldManager is the one server-side-apply field manager every Serendip
// provider applies under, so ownership of Serendip-set fields is tracked
// consistently across providers and releases.
const FieldManager = "serendip"

// Apply server-side-applies obj at its item path (e.g.
// "/api/v1/namespaces/default/configmaps/foo") under fieldManager, creating
// it if absent, and returns the resulting object. obj is the full desired
// state of every field this manager owns: a field it applied before and
// omits now is removed by the API server, while fields owned only by other
// managers are left alone. force=true because the caller is the source of
// truth for every field it declares on objects it owns; a conflict there
// is drift to undo. obj must carry apiVersion, kind, and metadata.name
// (and namespace, if namespaced), and should not carry resourceVersion
// (that would turn the apply into an optimistic-concurrency check). The
// body is JSON, which is valid YAML for the apply-patch content type.
// Returns ErrNotFound on a 404 (e.g. the parent namespace doesn't exist).
func (c *Client) Apply(ctx context.Context, path string, obj Object, fieldManager string) (Object, error) {
	q := url.Values{"fieldManager": {fieldManager}, "force": {"true"}}
	resp, err := c.doWithContentType(ctx, http.MethodPatch, path+"?"+q.Encode(), obj, "application/apply-patch+yaml")
	if err != nil {
		return Object{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Object{}, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return Object{}, unexpectedStatus(resp)
	}
	var applied Object
	if err := json.NewDecoder(resp.Body).Decode(&applied); err != nil {
		return Object{}, fmt.Errorf("kubernetes: decode apply %s response: %w", path, err)
	}
	return applied, nil
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
