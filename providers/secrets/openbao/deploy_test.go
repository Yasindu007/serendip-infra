package openbao

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	const statefulSetPath = "/apis/apps/v1/namespaces/serendip-system/statefulsets/openbao"
	var created []string
	statefulSetCreated := false
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path == statefulSetPath && statefulSetCreated {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(kubernetes.Object{Status: json.RawMessage(`{"readyReplicas":1}`)})
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		created = append(created, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/statefulsets") {
			statefulSetCreated = true
		}
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
	const statefulSetPath = "/apis/apps/v1/namespaces/serendip-system/statefulsets/openbao"
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s, want only GET calls when everything already exists", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == statefulSetPath {
			json.NewEncoder(w).Encode(kubernetes.Object{Status: json.RawMessage(`{"readyReplicas":1}`)})
			return
		}
		json.NewEncoder(w).Encode(kubernetes.Object{})
	})

	if err := NewDeployer(client).Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() = %v", err)
	}
}

func TestDeployer_Ensure_PartiallyExists_CreatesOnlyMissing(t *testing.T) {
	// Namespace and ConfigMap already exist; PVC, Service, StatefulSet don't.
	const statefulSetPath = "/apis/apps/v1/namespaces/serendip-system/statefulsets/openbao"
	exists := map[string]bool{
		"/api/v1/namespaces/serendip-system":                           true,
		"/api/v1/namespaces/serendip-system/configmaps/openbao-config": true,
	}
	var created []string
	statefulSetCreated := false
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path == statefulSetPath && statefulSetCreated {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(kubernetes.Object{Status: json.RawMessage(`{"readyReplicas":1}`)})
				return
			}
			if exists[r.URL.Path] {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(kubernetes.Object{})
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		created = append(created, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/statefulsets") {
			statefulSetCreated = true
		}
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

func TestDeployer_Ensure_WaitsAcrossMultipleNotYetReadyPolls(t *testing.T) {
	const statefulSetPath = "/apis/apps/v1/namespaces/serendip-system/statefulsets/openbao"
	readyAfterPolls := 3
	polls := 0
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path != statefulSetPath {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(kubernetes.Object{})
				return
			}
			polls++
			w.Header().Set("Content-Type", "application/json")
			if polls < readyAfterPolls {
				json.NewEncoder(w).Encode(kubernetes.Object{Status: json.RawMessage(`{"readyReplicas":0}`)})
				return
			}
			json.NewEncoder(w).Encode(kubernetes.Object{Status: json.RawMessage(`{"readyReplicas":1}`)})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(kubernetes.Object{})
	})

	d := NewDeployer(client)
	d.waitReadyPollInterval = time.Millisecond
	d.waitReadyTimeout = time.Second

	if err := d.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() = %v", err)
	}
	if polls != readyAfterPolls {
		t.Errorf("polls = %d, want exactly %d (stop polling as soon as ready)", polls, readyAfterPolls)
	}
}

func TestDeployer_Ensure_TimesOutIfNeverReady(t *testing.T) {
	const statefulSetPath = "/apis/apps/v1/namespaces/serendip-system/statefulsets/openbao"
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == statefulSetPath {
				json.NewEncoder(w).Encode(kubernetes.Object{Status: json.RawMessage(`{"readyReplicas":0}`)})
				return
			}
			json.NewEncoder(w).Encode(kubernetes.Object{})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(kubernetes.Object{})
	})

	d := NewDeployer(client)
	d.waitReadyPollInterval = time.Millisecond
	d.waitReadyTimeout = 20 * time.Millisecond

	err := d.Ensure(context.Background())
	if err == nil {
		t.Fatal("Ensure() = nil error, want a timeout error since the StatefulSet never reports ready")
	}
	if !strings.Contains(err.Error(), "wait for StatefulSet") {
		t.Errorf("error = %q, want it to name the wait-for-ready step", err.Error())
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
