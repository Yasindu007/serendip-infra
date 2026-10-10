package localpath_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
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

// setupHandler returns a handler that always reports the namespace/
// StorageClass as already existing, so T005's Ensure/Delete tests don't
// need to re-exercise T004's setup logic -- they attach their own
// switch cases for the PVC-specific requests on top of this.
func setupHandler(t *testing.T, pvcHandler http.HandlerFunc) *kubernetes.Client {
	t.Helper()
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/serendip-system":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: "serendip-system"}})
		case r.Method == http.MethodGet && r.URL.Path == "/apis/storage.k8s.io/v1/storageclasses/serendip-local-path":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"metadata":{"name":"serendip-local-path"}}`))
		default:
			pvcHandler(w, r)
		}
	})
}

func testVolume(id string, sizeBytes int64) resource.Volume {
	return resource.Volume{
		Metadata: resource.Metadata{ID: id, Name: "test-vol"},
		Spec:     resource.VolumeSpec{SizeBytes: sizeBytes},
	}
}

func TestProvider_Ensure_CreatesWhenMissing_ReportsPending(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody kubernetes.Object
	client := setupHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/serendip-system/persistentvolumeclaims/vol-abc123":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/serendip-system/persistentvolumeclaims":
			gotMethod, gotPath = r.Method, r.URL.Path
			json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			// A freshly-created PVC's response has no status.phase yet
			// in real Kubernetes -- matches that here, not Bound.
			json.NewEncoder(w).Encode(kubernetes.Object{
				APIVersion: "v1", Kind: "PersistentVolumeClaim",
				Metadata: kubernetes.ObjectMeta{Name: "vol-abc123", Namespace: "serendip-system"},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	provider, err := localpath.NewProvider(context.Background(), client)
	if err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}

	bound, reason, _, err := provider.Ensure(context.Background(), testVolume("abc123", 10<<30))
	if err != nil {
		t.Fatalf("Ensure() = %v", err)
	}
	if bound {
		t.Error("bound = true, want false (freshly-created PVC isn't instantly Bound)")
	}
	if reason != "Pending" {
		t.Errorf("reason = %q, want %q", reason, "Pending")
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/namespaces/serendip-system/persistentvolumeclaims" {
		t.Errorf("create request = %s %s, want POST .../persistentvolumeclaims", gotMethod, gotPath)
	}
	if gotBody.Metadata.Name != "vol-abc123" {
		t.Errorf("create body Metadata.Name = %q, want %q", gotBody.Metadata.Name, "vol-abc123")
	}
	var spec struct {
		StorageClassName string `json:"storageClassName"`
		Resources        struct {
			Requests map[string]string `json:"requests"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(gotBody.Spec, &spec); err != nil {
		t.Fatalf("unmarshal create body spec: %v", err)
	}
	if spec.StorageClassName != "serendip-local-path" {
		t.Errorf("spec.storageClassName = %q, want %q", spec.StorageClassName, "serendip-local-path")
	}
	if spec.Resources.Requests["storage"] != "10737418240" {
		t.Errorf("spec.resources.requests.storage = %q, want %q", spec.Resources.Requests["storage"], "10737418240")
	}
}

func TestProvider_Ensure_ExistingBoundPVC_ReportsBound(t *testing.T) {
	client := setupHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/serendip-system/persistentvolumeclaims/vol-abc123" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"vol-abc123","namespace":"serendip-system"},"status":{"phase":"Bound"}}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	})

	provider, err := localpath.NewProvider(context.Background(), client)
	if err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}

	bound, reason, _, err := provider.Ensure(context.Background(), testVolume("abc123", 10<<30))
	if err != nil {
		t.Fatalf("Ensure() = %v", err)
	}
	if !bound {
		t.Error("bound = false, want true (status.phase is Bound)")
	}
	if reason != "Bound" {
		t.Errorf("reason = %q, want %q", reason, "Bound")
	}
}

func TestProvider_Delete_RemovesExistingPVC(t *testing.T) {
	var gotMethod, gotPath string
	client := setupHandler(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	provider, err := localpath.NewProvider(context.Background(), client)
	if err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}

	if err := provider.Delete(context.Background(), testVolume("abc123", 0)); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/namespaces/serendip-system/persistentvolumeclaims/vol-abc123" {
		t.Errorf("request = %s %s, want DELETE .../vol-abc123", gotMethod, gotPath)
	}
}

func TestProvider_Delete_AlreadyGone_ReturnsNilNotError(t *testing.T) {
	client := setupHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	provider, err := localpath.NewProvider(context.Background(), client)
	if err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}

	if err := provider.Delete(context.Background(), testVolume("abc123", 0)); err != nil {
		t.Errorf("Delete() = %v, want nil (already gone is success, not an error)", err)
	}
}
