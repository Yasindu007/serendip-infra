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

// applyReq is one server-side apply request a fake API server received.
type applyReq struct {
	path, fieldManager, force string
	body                      kubernetes.Object
}

func isApply(r *http.Request) bool {
	return r.Method == http.MethodPatch && r.Header.Get("Content-Type") == "application/apply-patch+yaml"
}

func recordApply(r *http.Request) applyReq {
	var body kubernetes.Object
	json.NewDecoder(r.Body).Decode(&body)
	return applyReq{
		path: r.URL.Path, fieldManager: r.URL.Query().Get("fieldManager"), force: r.URL.Query().Get("force"),
		body: body,
	}
}

func TestNewProvider_AppliesNamespace(t *testing.T) {
	var got []applyReq
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !isApply(r) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		got = append(got, recordApply(r))
		json.NewEncoder(w).Encode(kubernetes.Object{APIVersion: "v1", Kind: "Namespace", Metadata: kubernetes.ObjectMeta{Name: "serendip-system"}})
	})

	if _, err := localpath.NewProvider(context.Background(), client); err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("apply calls = %d, want 1", len(got))
	}
	a := got[0]
	if a.path != "/api/v1/namespaces/serendip-system" || a.fieldManager != "serendip" || a.force != "true" {
		t.Errorf("apply = %s fieldManager=%s force=%s", a.path, a.fieldManager, a.force)
	}
	if a.body.APIVersion != "v1" || a.body.Kind != "Namespace" || a.body.Metadata.Name != "serendip-system" {
		t.Errorf("body = %+v", a.body)
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

// setupHandler answers NewProvider's namespace apply, so Ensure/Delete
// tests don't need to re-exercise its setup logic -- they attach their own
// cases for PV/PVC-specific requests on top of this.
func setupHandler(t *testing.T, handler http.HandlerFunc) *kubernetes.Client {
	t.Helper()
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && r.URL.Path == "/api/v1/namespaces/serendip-system" {
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

const (
	pvcPath = "/api/v1/namespaces/serendip-system/persistentvolumeclaims/vol-abc123"
	pvPath  = "/api/v1/persistentvolumes/pv-vol-abc123"
)

// pvcApplyHandler records every apply and answers the PVC apply with
// pvcStatus as its status (empty: none yet).
func pvcApplyHandler(t *testing.T, got *[]applyReq, pvcStatus string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isApply(r) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		a := recordApply(r)
		*got = append(*got, a)
		resp := a.body
		if r.URL.Path == pvcPath && pvcStatus != "" {
			resp.Status = json.RawMessage(pvcStatus)
		}
		json.NewEncoder(w).Encode(resp)
	}
}

func TestProvider_Ensure_AppliesPVCThenPV_ReportsPending(t *testing.T) {
	var got []applyReq
	provider, err := localpath.NewProvider(context.Background(), setupHandler(t, pvcApplyHandler(t, &got, "")))
	if err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}

	bound, reason, _, err := provider.Ensure(context.Background(), testVolume("abc123", 10<<30))
	if err != nil {
		t.Fatalf("Ensure() = %v", err)
	}
	if bound || reason != "Pending" {
		t.Errorf("Ensure() = %v, %q; want false, Pending (no status.phase yet)", bound, reason)
	}
	if len(got) != 2 || got[0].path != pvcPath || got[1].path != pvPath {
		t.Fatalf("applies = %+v, want PVC then PV", got)
	}
	for _, a := range got {
		if a.fieldManager != "serendip" || a.force != "true" {
			t.Errorf("%s: fieldManager=%q force=%q, want serendip/true", a.path, a.fieldManager, a.force)
		}
	}

	pvc := got[0].body
	if pvc.Kind != "PersistentVolumeClaim" || pvc.Metadata.Name != "vol-abc123" || pvc.Metadata.Namespace != "serendip-system" {
		t.Errorf("PVC envelope = %+v", pvc)
	}
	var pvcSpec struct {
		VolumeName       string  `json:"volumeName"`
		StorageClassName *string `json:"storageClassName"`
		Resources        struct {
			Requests map[string]string `json:"requests"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(pvc.Spec, &pvcSpec); err != nil {
		t.Fatalf("unmarshal PVC spec: %v", err)
	}
	if pvcSpec.VolumeName != "pv-vol-abc123" {
		t.Errorf("PVC spec.volumeName = %q, want pv-vol-abc123 (static binding reference)", pvcSpec.VolumeName)
	}
	// storageClassName must be sent explicitly empty: omitting it would let
	// the cluster's default StorageClass claim the PVC.
	if pvcSpec.StorageClassName == nil || *pvcSpec.StorageClassName != "" {
		t.Errorf("PVC spec.storageClassName = %v, want explicit empty", pvcSpec.StorageClassName)
	}
	if got := pvcSpec.Resources.Requests["storage"]; got != "10737418240" {
		t.Errorf("PVC requests.storage = %q, want 10737418240", got)
	}

	pv := got[1].body
	if pv.Kind != "PersistentVolume" || pv.Metadata.Name != "pv-vol-abc123" || pv.Metadata.Namespace != "" {
		t.Errorf("PV envelope = %+v", pv)
	}
	var pvSpec struct {
		Capacity         map[string]string `json:"capacity"`
		StorageClassName *string           `json:"storageClassName"`
		HostPath         struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"hostPath"`
	}
	if err := json.Unmarshal(pv.Spec, &pvSpec); err != nil {
		t.Fatalf("unmarshal PV spec: %v", err)
	}
	if pvSpec.Capacity["storage"] != "10737418240" || pvSpec.StorageClassName == nil || *pvSpec.StorageClassName != "" ||
		pvSpec.HostPath.Path == "" || pvSpec.HostPath.Type != "DirectoryOrCreate" {
		t.Errorf("PV spec = %+v", pvSpec)
	}
}

func TestProvider_Ensure_IsTheSameApplyEveryTime(t *testing.T) {
	var got []applyReq
	provider, err := localpath.NewProvider(context.Background(), setupHandler(t, pvcApplyHandler(t, &got, `{"phase":"Bound"}`)))
	if err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}
	vol := testVolume("abc123", 1<<30)
	for range 2 {
		bound, reason, _, err := provider.Ensure(context.Background(), vol)
		if err != nil || !bound || reason != "Bound" {
			t.Fatalf("Ensure() = %v, %q, %v; want Bound", bound, reason, err)
		}
	}
	if len(got) != 4 {
		t.Fatalf("applies = %d, want 4", len(got))
	}
	for i := range 2 {
		a, b := got[i], got[i+2]
		if a.path != b.path || string(a.body.Spec) != string(b.body.Spec) {
			t.Errorf("repeat apply differs: %s %s vs %s %s", a.path, a.body.Spec, b.path, b.body.Spec)
		}
	}
}

// A PVC change the API server refuses (e.g. a resize of a bound claim)
// fails Ensure before the PV is touched.
func TestProvider_Ensure_PVCRejectedLeavesPVAlone(t *testing.T) {
	var pvApplied bool
	client := setupHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pvcPath:
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"message":"spec.resources.requests.storage: Forbidden"}`))
		case pvPath:
			pvApplied = true
			json.NewEncoder(w).Encode(kubernetes.Object{})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	provider, err := localpath.NewProvider(context.Background(), client)
	if err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}
	if _, _, _, err := provider.Ensure(context.Background(), testVolume("abc123", 2<<30)); err == nil {
		t.Fatal("Ensure() = nil, want the PVC apply error")
	}
	if pvApplied {
		t.Error("PV was applied after the PVC apply failed")
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
	if gotMethods[0] != http.MethodDelete || gotPaths[0] != pvcPath {
		t.Errorf("first request = %s %s, want DELETE .../vol-abc123 (PVC first)", gotMethods[0], gotPaths[0])
	}
	if gotMethods[1] != http.MethodDelete || gotPaths[1] != pvPath {
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
