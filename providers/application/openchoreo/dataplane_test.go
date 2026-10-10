package openchoreo

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// newTestKubeClient mirrors providers/secrets/openbao/deploy_test.go's
// own helper of the same name exactly (different package, can't reuse
// it directly).
func newTestKubeClient(t *testing.T, handler http.HandlerFunc) *kubernetes.Client {
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

func TestEnsureDefaultDataPlane_Absent_CreatesWithExpectedShape(t *testing.T) {
	const wantPath = "/apis/openchoreo.dev/v1alpha1/namespaces/openchoreo-control-plane/dataplanes"
	var created bool
	var gotObj kubernetes.Object
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path != wantPath {
			t.Errorf("POST path = %q, want %q", r.URL.Path, wantPath)
		}
		created = true
		json.NewDecoder(r.Body).Decode(&gotObj)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(gotObj)
	})

	if err := EnsureDefaultDataPlane(context.Background(), client); err != nil {
		t.Fatalf("EnsureDefaultDataPlane() = %v", err)
	}
	if !created {
		t.Fatal("DataPlane was not created")
	}
	if gotObj.APIVersion != "openchoreo.dev/v1alpha1" || gotObj.Kind != "DataPlane" {
		t.Errorf("created object apiVersion/kind = %q/%q, want openchoreo.dev/v1alpha1/DataPlane", gotObj.APIVersion, gotObj.Kind)
	}
	if gotObj.Metadata.Name != DefaultDataPlaneName || gotObj.Metadata.Namespace != ControlPlaneNamespace {
		t.Errorf("created object metadata = %+v, want name=%q namespace=%q", gotObj.Metadata, DefaultDataPlaneName, ControlPlaneNamespace)
	}

	var spec dataPlaneSpec
	if err := json.Unmarshal(gotObj.Spec, &spec); err != nil {
		t.Fatalf("unmarshal created spec = %v", err)
	}
	ref := spec.ClusterAgent.ClientCA.SecretKeyRef
	if ref.Name != clusterAgentTLSSecretName || ref.Namespace != DataPlaneNamespace || ref.Key != clusterAgentTLSSecretKey {
		t.Errorf("secretKeyRef = %+v, want name=%q namespace=%q key=%q", ref, clusterAgentTLSSecretName, DataPlaneNamespace, clusterAgentTLSSecretKey)
	}
}

func TestEnsureDefaultDataPlane_AlreadyExists_DoesNotReCreate(t *testing.T) {
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Fatalf("unexpected POST %s, want no create call when the DataPlane already exists", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(kubernetes.Object{})
	})

	if err := EnsureDefaultDataPlane(context.Background(), client); err != nil {
		t.Fatalf("EnsureDefaultDataPlane() = %v", err)
	}
}

func TestEnsureDefaultDataPlane_UnexpectedGetError_Propagates(t *testing.T) {
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("simulated server error"))
	})

	err := EnsureDefaultDataPlane(context.Background(), client)
	if err == nil {
		t.Fatal("EnsureDefaultDataPlane() = nil, want the simulated GET failure to propagate")
	}
}
