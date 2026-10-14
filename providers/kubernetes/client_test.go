package kubernetes

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestServerClient builds a Client pointed at a plain-HTTP
// httptest.Server, bypassing TLS entirely -- the CRUD methods
// (Get/Create/Delete/List) don't care about transport security, only
// about request construction and response decoding, which is what
// these tests cover. TLS/cert handling itself is covered separately by
// TestNewClient_*.
func newTestServerClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{baseURL: srv.URL, http: srv.Client()}, srv
}

func TestClient_Get(t *testing.T) {
	var gotMethod, gotPath string
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Object{APIVersion: "v1", Kind: "Node", Metadata: ObjectMeta{Name: "node-1"}})
	})

	obj, err := client.Get(context.Background(), "/api/v1/nodes/node-1")
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/v1/nodes/node-1" {
		t.Errorf("request = %s %s, want GET /api/v1/nodes/node-1", gotMethod, gotPath)
	}
	if obj.Metadata.Name != "node-1" {
		t.Errorf("Metadata.Name = %q, want %q", obj.Metadata.Name, "node-1")
	}
}

func TestClient_Get_UnexpectedStatus(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"nodes \"node-1\" not found"}`))
	})

	_, err := client.Get(context.Background(), "/api/v1/nodes/node-1")
	if err == nil {
		t.Fatal("Get() = nil error, want an error for a 404 response")
	}
}

func TestClient_Get_404ReturnsErrNotFound(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := client.Get(context.Background(), "/api/v1/nodes/node-1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() error = %v, want errors.Is(err, ErrNotFound)", err)
	}
}

func TestClient_Delete_404ReturnsErrNotFound(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	err := client.Delete(context.Background(), "/api/v1/nodes/node-1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() error = %v, want errors.Is(err, ErrNotFound)", err)
	}
}

func TestClient_Create(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody Object
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		created := gotBody
		created.Metadata.ResourceVersion = "1"
		json.NewEncoder(w).Encode(created)
	})

	req := Object{APIVersion: "v1", Kind: "PersistentVolumeClaim", Metadata: ObjectMeta{Name: "pvc-1", Namespace: "default"}}
	got, err := client.Create(context.Background(), "/api/v1/namespaces/default/persistentvolumeclaims", req)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/namespaces/default/persistentvolumeclaims" {
		t.Errorf("request = %s %s, want POST /api/v1/namespaces/default/persistentvolumeclaims", gotMethod, gotPath)
	}
	if gotBody.Metadata.Name != "pvc-1" {
		t.Errorf("request body Metadata.Name = %q, want %q", gotBody.Metadata.Name, "pvc-1")
	}
	if got.Metadata.ResourceVersion != "1" {
		t.Errorf("Create() returned ResourceVersion = %q, want %q", got.Metadata.ResourceVersion, "1")
	}
}

func TestClient_Create_ConfigMapDataFieldRoundTrips(t *testing.T) {
	var gotBody Object
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(gotBody)
	})

	req := Object{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata:   ObjectMeta{Name: "cm-1", Namespace: "default"},
		Data:       json.RawMessage(`{"server.hcl":"listener \"tcp\" {}"}`),
	}
	got, err := client.Create(context.Background(), "/api/v1/namespaces/default/configmaps", req)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if string(gotBody.Data) != string(req.Data) {
		t.Errorf("request body Data = %s, want %s", gotBody.Data, req.Data)
	}
	if string(got.Data) != string(req.Data) {
		t.Errorf("response Data = %s, want %s", got.Data, req.Data)
	}
}

func TestClient_Delete(t *testing.T) {
	var gotMethod, gotPath string
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	if err := client.Delete(context.Background(), "/api/v1/namespaces/default/persistentvolumeclaims/pvc-1"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/namespaces/default/persistentvolumeclaims/pvc-1" {
		t.Errorf("request = %s %s, want DELETE .../pvc-1", gotMethod, gotPath)
	}
}

func TestClient_List(t *testing.T) {
	var gotMethod, gotPath string
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(listResult{Items: []Object{
			{Metadata: ObjectMeta{Name: "node-1"}},
			{Metadata: ObjectMeta{Name: "node-2"}},
		}})
	})

	items, err := client.List(context.Background(), "/api/v1/nodes")
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/v1/nodes" {
		t.Errorf("request = %s %s, want GET /api/v1/nodes", gotMethod, gotPath)
	}
	if len(items) != 2 || items[0].Metadata.Name != "node-1" || items[1].Metadata.Name != "node-2" {
		t.Errorf("List() = %+v, want 2 items named node-1, node-2", items)
	}
}

func TestClient_BearerTokenSent(t *testing.T) {
	var gotAuth string
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Object{})
	})
	client.token = "secret-token"

	if _, err := client.Get(context.Background(), "/api/v1/nodes"); err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer secret-token")
	}
}

// generateTestCertPEM creates a minimal self-signed cert/key pair in
// PEM form, for testing buildTLSConfig/NewClient without depending on
// any real cluster -- only real crypto material will satisfy
// x509.NewCertPool().AppendCertsFromPEM/tls.X509KeyPair, so fixture
// strings like "fake-ca-data" (used by config_test.go, which only
// exercises parsing, not TLS construction) won't work here.
func generateTestCertPEM(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate() = %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey() = %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func TestNewClient_ValidClientCertConfig(t *testing.T) {
	caPEM, _ := generateTestCertPEM(t)
	certPEM, keyPEM := generateTestCertPEM(t)

	client, err := NewClient(Config{
		Host:           "https://127.0.0.1:6443",
		CAData:         caPEM,
		ClientCertData: certPEM,
		ClientKeyData:  keyPEM,
	})
	if err != nil {
		t.Fatalf("NewClient() = %v", err)
	}
	if client == nil {
		t.Fatal("NewClient() returned a nil client with no error")
	}
}

func TestNewClient_InvalidCAData(t *testing.T) {
	_, err := NewClient(Config{Host: "https://127.0.0.1:6443", CAData: []byte("not a valid PEM certificate")})
	if err == nil {
		t.Fatal("NewClient(invalid CA data) = nil error, want an error")
	}
}

func TestNewClient_InvalidClientCertKeyPair(t *testing.T) {
	caPEM, _ := generateTestCertPEM(t)
	_, err := NewClient(Config{
		Host:           "https://127.0.0.1:6443",
		CAData:         caPEM,
		ClientCertData: []byte("not a valid cert"),
		ClientKeyData:  []byte("not a valid key"),
	})
	if err == nil {
		t.Fatal("NewClient(invalid client cert/key) = nil error, want an error")
	}
}

func TestClient_ProxyURL(t *testing.T) {
	client := &Client{baseURL: "https://127.0.0.1:6443"}
	got := client.ProxyURL("serendip-system", "openbao", 8200)
	want := "https://127.0.0.1:6443/api/v1/namespaces/serendip-system/services/openbao:8200/proxy"
	if got != want {
		t.Errorf("ProxyURL() = %q, want %q", got, want)
	}
}

func TestClient_HTTPClient_ReturnsConfiguredInstance(t *testing.T) {
	httpClient := &http.Client{}
	client := &Client{http: httpClient}
	if got := client.HTTPClient(); got != httpClient {
		t.Errorf("HTTPClient() = %p, want the same instance %p", got, httpClient)
	}
}

func TestNewClient_BearerTokenOnly_NoClientCertRequired(t *testing.T) {
	caPEM, _ := generateTestCertPEM(t)
	client, err := NewClient(Config{Host: "https://127.0.0.1:6443", CAData: caPEM, BearerToken: "secret"})
	if err != nil {
		t.Fatalf("NewClient() = %v", err)
	}
	if client.token != "secret" {
		t.Errorf("client.token = %q, want %q", client.token, "secret")
	}
}

func TestClient_Patch(t *testing.T) {
	var gotMethod, gotPath, gotContentType string
	var gotBody map[string]any
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotContentType = r.Method, r.URL.Path, r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Object{
			APIVersion: "v1", Kind: "Namespace",
			Metadata: ObjectMeta{Name: "ns-1", Labels: map[string]string{"a": "b"}, ResourceVersion: "7"},
		})
	})

	patch := map[string]any{"metadata": map[string]any{"labels": map[string]string{"a": "b"}}}
	got, err := client.Patch(context.Background(), "/api/v1/namespaces/ns-1", patch)
	if err != nil {
		t.Fatalf("Patch() = %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/v1/namespaces/ns-1" {
		t.Errorf("request = %s %s, want PATCH /api/v1/namespaces/ns-1", gotMethod, gotPath)
	}
	if gotContentType != "application/merge-patch+json" {
		t.Errorf("Content-Type = %q, want application/merge-patch+json", gotContentType)
	}
	labels, _ := gotBody["metadata"].(map[string]any)["labels"].(map[string]any)
	if labels["a"] != "b" {
		t.Errorf("request body = %v, want metadata.labels.a=b", gotBody)
	}
	if got.Metadata.ResourceVersion != "7" || got.Metadata.Labels["a"] != "b" {
		t.Errorf("Patch() returned %+v, want decoded patched object", got.Metadata)
	}
}

func TestClient_Patch_404ReturnsErrNotFound(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := client.Patch(context.Background(), "/api/v1/namespaces/ns-1", map[string]any{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Patch() error = %v, want errors.Is(err, ErrNotFound)", err)
	}
}

func TestClient_Patch_UnexpectedStatus(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message":"invalid"}`))
	})
	_, err := client.Patch(context.Background(), "/api/v1/namespaces/ns-1", map[string]any{})
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("Patch() error = %v, want a non-ErrNotFound error for 422", err)
	}
}

func TestClient_Create_StillSendsJSONContentType(t *testing.T) {
	var gotContentType string
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"x"}}`))
	})
	if _, err := client.Create(context.Background(), "/api/v1/namespaces/default/configmaps", Object{}); err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
}

func TestClient_Apply_RequestShape(t *testing.T) {
	var gotMethod, gotPath, gotCT string
	var gotQuery map[string][]string
	var gotBody map[string]any
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotCT = r.Method, r.URL.Path, r.Header.Get("Content-Type")
		gotQuery = r.URL.Query()
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(Object{APIVersion: "v1", Kind: "ConfigMap", Metadata: ObjectMeta{Name: "cm", Namespace: "ns", Generation: 3}})
	})

	got, err := client.Apply(context.Background(), "/api/v1/namespaces/ns/configmaps/cm", Object{
		APIVersion: "v1", Kind: "ConfigMap",
		Metadata: ObjectMeta{Name: "cm", Namespace: "ns", Labels: map[string]string{"a": "b"}},
		Data:     json.RawMessage(`{"k":"v"}`),
	}, "serendip")
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/v1/namespaces/ns/configmaps/cm" {
		t.Errorf("request = %s %s, want PATCH to the item path", gotMethod, gotPath)
	}
	if gotCT != "application/apply-patch+yaml" {
		t.Errorf("Content-Type = %q, want application/apply-patch+yaml", gotCT)
	}
	if fm, force := gotQuery["fieldManager"], gotQuery["force"]; len(fm) != 1 || fm[0] != "serendip" || len(force) != 1 || force[0] != "true" {
		t.Errorf("query = %v, want fieldManager=serendip&force=true", gotQuery)
	}
	meta, _ := gotBody["metadata"].(map[string]any)
	if gotBody["apiVersion"] != "v1" || gotBody["kind"] != "ConfigMap" || meta["name"] != "cm" || meta["namespace"] != "ns" {
		t.Errorf("body = %v, want apiVersion/kind/metadata.name/namespace", gotBody)
	}
	if _, ok := meta["resourceVersion"]; ok {
		t.Errorf("body carries resourceVersion: %v", meta)
	}
	if got.Metadata.Generation != 3 {
		t.Errorf("decoded generation = %d, want 3", got.Metadata.Generation)
	}
}

func TestClient_Apply_Errors(t *testing.T) {
	client, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if _, err := client.Apply(context.Background(), "/x", Object{}, "serendip"); !errors.Is(err, ErrNotFound) {
		t.Errorf("404: err = %v, want ErrNotFound", err)
	}
	client, _ = newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	})
	if _, err := client.Apply(context.Background(), "/x", Object{}, "serendip"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("409: err = %v, want an unexpected-status error", err)
	}
}
