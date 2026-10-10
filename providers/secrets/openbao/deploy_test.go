package openbao

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// newTestKubeClient builds a real *kubernetes.Client (through its
// actual public NewClient constructor, no test-only backdoor) pointed
// at an httptest.NewTLSServer, trusting exactly that server's own
// auto-generated certificate as the CA -- real TLS validation against
// the real test server, not bypassed. Mirrors providers/storage/
// localpath/provider_test.go's own newTestClient helper exactly.
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

func TestDeployer_Ensure_AllMissing_CreatesAllFiveInDependencyOrder(t *testing.T) {
	var created []string
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		created = append(created, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(kubernetes.Object{})
	})

	if err := NewDeployer(client).Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() = %v", err)
	}

	want := []string{
		"/api/v1/namespaces",
		"/api/v1/namespaces/serendip-system/configmaps",
		"/api/v1/namespaces/serendip-system/persistentvolumeclaims",
		"/api/v1/namespaces/serendip-system/services",
		"/apis/apps/v1/namespaces/serendip-system/statefulsets",
	}
	if len(created) != len(want) {
		t.Fatalf("created %d objects %v, want %d %v", len(created), created, len(want), want)
	}
	for i, path := range want {
		if created[i] != path {
			t.Errorf("creation order[%d] = %q, want %q (dependency order matters: namespace, then ConfigMap/PVC, then Service, then StatefulSet)", i, created[i], path)
		}
	}
}

func TestDeployer_Ensure_AllAlreadyExist_CreatesNothing(t *testing.T) {
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s, want only GET calls when everything already exists", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(kubernetes.Object{})
	})

	if err := NewDeployer(client).Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() = %v", err)
	}
}

func TestDeployer_Ensure_PartiallyExists_CreatesOnlyMissing(t *testing.T) {
	// Namespace and ConfigMap already exist; PVC, Service, StatefulSet don't.
	exists := map[string]bool{
		"/api/v1/namespaces/serendip-system":                           true,
		"/api/v1/namespaces/serendip-system/configmaps/openbao-config": true,
	}
	var created []string
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if exists[r.URL.Path] {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(kubernetes.Object{})
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		created = append(created, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(kubernetes.Object{})
	})

	if err := NewDeployer(client).Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() = %v", err)
	}

	want := []string{
		"/api/v1/namespaces/serendip-system/persistentvolumeclaims",
		"/api/v1/namespaces/serendip-system/services",
		"/apis/apps/v1/namespaces/serendip-system/statefulsets",
	}
	if len(created) != len(want) {
		t.Fatalf("created %v, want exactly %v (namespace/ConfigMap already existed, must not be re-created)", created, want)
	}
	for i, path := range want {
		if created[i] != path {
			t.Errorf("created[%d] = %q, want %q", i, created[i], path)
		}
	}
}

func TestDeployer_Ensure_UnexpectedErrorNamesWhichObjectFailed(t *testing.T) {
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/namespaces/serendip-system" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/api/v1/namespaces" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("simulated server error"))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	err := NewDeployer(client).Ensure(context.Background())
	if err == nil {
		t.Fatal("Ensure() = nil error, want the simulated namespace-creation failure to propagate")
	}
	if !strings.Contains(err.Error(), "namespace") || !strings.Contains(err.Error(), "serendip-system") {
		t.Errorf("error = %q, want it to name the namespace that failed", err.Error())
	}
}
