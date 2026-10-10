package localpath_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
	"github.com/serendip-infra/serendip-infra/providers/storage/localpath"
)

// newTestClient builds a real *kubernetes.Client (through its actual
// public NewClient constructor, no test-only backdoor) pointed at an
// httptest.NewTLSServer, trusting exactly that server's own
// auto-generated certificate as the CA -- real TLS validation against
// the real test server, not bypassed.
func newTestClient(t *testing.T, handler http.HandlerFunc) *kubernetes.Client {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	client, err := kubernetes.NewClient(kubernetes.Config{Host: srv.URL, CAData: caPEM})
	if err != nil {
		t.Fatalf("NewClient() = %v", err)
	}
	return client
}

func TestNewProvider_CreatesNamespaceAndStorageClassWhenMissing(t *testing.T) {
	var gotStorageClassBody map[string]any

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/serendip-system":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(kubernetes.Object{APIVersion: "v1", Kind: "Namespace", Metadata: kubernetes.ObjectMeta{Name: "serendip-system"}})
		case r.Method == http.MethodGet && r.URL.Path == "/apis/storage.k8s.io/v1/storageclasses/serendip-local-path":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/apis/storage.k8s.io/v1/storageclasses":
			json.NewDecoder(r.Body).Decode(&gotStorageClassBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"apiVersion":"storage.k8s.io/v1","kind":"StorageClass","metadata":{"name":"serendip-local-path"}}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	provider, err := localpath.NewProvider(context.Background(), client)
	if err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}
	if provider == nil {
		t.Fatal("NewProvider() returned a nil provider with no error")
	}

	if gotStorageClassBody["provisioner"] != "rancher.io/local-path" {
		t.Errorf("StorageClass body provisioner = %v, want %q", gotStorageClassBody["provisioner"], "rancher.io/local-path")
	}
	if gotStorageClassBody["volumeBindingMode"] != "Immediate" {
		t.Errorf("StorageClass body volumeBindingMode = %v, want %q (not WaitForFirstConsumer)", gotStorageClassBody["volumeBindingMode"], "Immediate")
	}
	if meta, ok := gotStorageClassBody["metadata"].(map[string]any); ok {
		if _, hasAnnotations := meta["annotations"]; hasAnnotations {
			t.Error("StorageClass body metadata has annotations, want none (not marked default)")
		}
	}
}

func TestNewProvider_SkipsCreateWhenAlreadyExists(t *testing.T) {
	var createCalls int
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/serendip-system":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: "serendip-system"}})
		case r.Method == http.MethodGet && r.URL.Path == "/apis/storage.k8s.io/v1/storageclasses/serendip-local-path":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"metadata":{"name":"serendip-local-path"}}`))
		case r.Method == http.MethodPost:
			createCalls++
			t.Errorf("unexpected Create call to %s -- object already exists, should not be recreated", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	if _, err := localpath.NewProvider(context.Background(), client); err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}
	if createCalls != 0 {
		t.Errorf("Create calls = %d, want 0 (both objects already existed)", createCalls)
	}
}

func TestNewProvider_PropagatesUnexpectedError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if _, err := localpath.NewProvider(context.Background(), client); err == nil {
		t.Fatal("NewProvider() = nil error, want an error for a genuine server failure")
	}
}
