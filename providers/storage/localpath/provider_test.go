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

func TestNewProvider_CreatesNamespaceWhenMissing(t *testing.T) {
	var createCalled bool
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/serendip-system":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces":
			createCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(kubernetes.Object{APIVersion: "v1", Kind: "Namespace", Metadata: kubernetes.ObjectMeta{Name: "serendip-system"}})
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
	if !createCalled {
		t.Error("namespace Create was never called")
	}
}

func TestNewProvider_SkipsCreateWhenNamespaceAlreadyExists(t *testing.T) {
	var createCalls int
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/serendip-system":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: "serendip-system"}})
		case r.Method == http.MethodPost:
			createCalls++
			t.Errorf("unexpected Create call to %s -- namespace already exists, should not be recreated", r.URL.Path)
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
		t.Errorf("Create calls = %d, want 0 (namespace already existed)", createCalls)
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

// setupHandler returns a handler that always reports the namespace as
// already existing, so Ensure/Delete tests don't need to re-exercise
// NewProvider's own setup logic -- they attach their own switch cases
// for PV/PVC-specific requests on top of this.
func setupHandler(t *testing.T, handler http.HandlerFunc) *kubernetes.Client {
	t.Helper()
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/serendip-system" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: "serendip-system"}})
			return
		}
		handler(w, r)
	})
}

func testVolume(id string, sizeBytes int64) resource.Volume {
	return resource.Volume{
		Metadata: resource.Metadata{ID: id, Name: "test-vol"},
		Spec:     resource.VolumeSpec{SizeBytes: sizeBytes},
	}
}

func TestProvider_Ensure_CreatesPVAndPVCWhenMissing_ReportsPending(t *testing.T) {
	var gotPVBody, gotPVCBody kubernetes.Object
	var pvCreated, pvcCreated bool

	client := setupHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/persistentvolumes/pv-vol-abc123":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/persistentvolumes":
			pvCreated = true
			json.NewDecoder(r.Body).Decode(&gotPVBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(kubernetes.Object{APIVersion: "v1", Kind: "PersistentVolume", Metadata: kubernetes.ObjectMeta{Name: "pv-vol-abc123"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/serendip-system/persistentvolumeclaims/vol-abc123":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/serendip-system/persistentvolumeclaims":
			pvcCreated = true
			json.NewDecoder(r.Body).Decode(&gotPVCBody)
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
		t.Error("bound = true, want false (freshly-created PVC isn't instantly Bound in the response itself)")
	}
	if reason != "Pending" {
		t.Errorf("reason = %q, want %q", reason, "Pending")
	}
	if !pvCreated {
		t.Fatal("PV was never created")
	}
	if !pvcCreated {
		t.Fatal("PVC was never created")
	}

	var pvSpec struct {
		Capacity         map[string]string `json:"capacity"`
		StorageClassName string            `json:"storageClassName"`
		HostPath         struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"hostPath"`
	}
	if err := json.Unmarshal(gotPVBody.Spec, &pvSpec); err != nil {
		t.Fatalf("unmarshal PV spec: %v", err)
	}
	if pvSpec.Capacity["storage"] != "10737418240" {
		t.Errorf("PV spec.capacity.storage = %q, want %q", pvSpec.Capacity["storage"], "10737418240")
	}
	if pvSpec.StorageClassName != "" {
		t.Errorf("PV spec.storageClassName = %q, want empty (static binding, no dynamic-provisioner matching)", pvSpec.StorageClassName)
	}
	if pvSpec.HostPath.Path == "" {
		t.Error("PV spec.hostPath.path is empty, want a real path")
	}

	var pvcSpec struct {
		VolumeName       string `json:"volumeName"`
		StorageClassName string `json:"storageClassName"`
		Resources        struct {
			Requests map[string]string `json:"requests"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(gotPVCBody.Spec, &pvcSpec); err != nil {
		t.Fatalf("unmarshal PVC spec: %v", err)
	}
	if pvcSpec.VolumeName != "pv-vol-abc123" {
		t.Errorf("PVC spec.volumeName = %q, want %q (static binding reference)", pvcSpec.VolumeName, "pv-vol-abc123")
	}
	if pvcSpec.Resources.Requests["storage"] != "10737418240" {
		t.Errorf("PVC spec.resources.requests.storage = %q, want %q", pvcSpec.Resources.Requests["storage"], "10737418240")
	}
}

func TestProvider_Ensure_SkipsPVCreateWhenAlreadyExists(t *testing.T) {
	var pvCreateCalls int
	client := setupHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/persistentvolumes/pv-vol-abc123":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: "pv-vol-abc123"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/persistentvolumes":
			pvCreateCalls++
			t.Error("unexpected PV Create call -- PV already existed")
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/serendip-system/persistentvolumeclaims/vol-abc123":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"vol-abc123","namespace":"serendip-system"},"status":{"phase":"Bound"}}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	provider, err := localpath.NewProvider(context.Background(), client)
	if err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}
	if _, _, _, err := provider.Ensure(context.Background(), testVolume("abc123", 10<<30)); err != nil {
		t.Fatalf("Ensure() = %v", err)
	}
	if pvCreateCalls != 0 {
		t.Errorf("PV Create calls = %d, want 0", pvCreateCalls)
	}
}

func TestProvider_Ensure_ExistingBoundPVC_ReportsBound(t *testing.T) {
	client := setupHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/persistentvolumes/pv-vol-abc123":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: "pv-vol-abc123"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/serendip-system/persistentvolumeclaims/vol-abc123":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"vol-abc123","namespace":"serendip-system"},"status":{"phase":"Bound"}}`))
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
	if !bound {
		t.Error("bound = false, want true (status.phase is Bound)")
	}
	if reason != "Bound" {
		t.Errorf("reason = %q, want %q", reason, "Bound")
	}
}

func TestProvider_Delete_RemovesExistingPVAndPVC(t *testing.T) {
	var gotMethods, gotPaths []string
	client := setupHandler(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethods = append(gotMethods, r.Method)
		gotPaths = append(gotPaths, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})

	provider, err := localpath.NewProvider(context.Background(), client)
	if err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}

	if err := provider.Delete(context.Background(), testVolume("abc123", 0)); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if len(gotPaths) != 2 {
		t.Fatalf("request count = %d, want 2 (PVC then PV)", len(gotPaths))
	}
	if gotMethods[0] != http.MethodDelete || gotPaths[0] != "/api/v1/namespaces/serendip-system/persistentvolumeclaims/vol-abc123" {
		t.Errorf("first request = %s %s, want DELETE .../vol-abc123 (PVC first)", gotMethods[0], gotPaths[0])
	}
	if gotMethods[1] != http.MethodDelete || gotPaths[1] != "/api/v1/persistentvolumes/pv-vol-abc123" {
		t.Errorf("second request = %s %s, want DELETE .../pv-vol-abc123 (PV second)", gotMethods[1], gotPaths[1])
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
