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

const statefulSetPath = "/apis/apps/v1/namespaces/serendip-system/statefulsets/openbao"

// appliedObject is one server-side apply request the fake API server got.
type appliedObject struct {
	path, fieldManager, force string
	body                      kubernetes.Object
}

// applyRecorder answers every apply by echoing the body, and every GET of
// the StatefulSet as ready.
func applyRecorder(t *testing.T, got *[]appliedObject) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == statefulSetPath:
			json.NewEncoder(w).Encode(kubernetes.Object{Status: json.RawMessage(`{"readyReplicas":1}`)})
		case r.Method == http.MethodPatch && r.Header.Get("Content-Type") == "application/apply-patch+yaml":
			var body kubernetes.Object
			json.NewDecoder(r.Body).Decode(&body)
			*got = append(*got, appliedObject{path: r.URL.Path, fieldManager: r.URL.Query().Get("fieldManager"), force: r.URL.Query().Get("force"), body: body})
			json.NewEncoder(w).Encode(body)
		default:
			t.Errorf("unexpected request %s %s (want only applies and the readiness GET)", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

func TestDeployer_Ensure_AppliesAllFiveInDependencyOrder(t *testing.T) {
	var got []appliedObject
	if err := NewDeployer(newTestKubeClient(t, applyRecorder(t, &got))).Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() = %v", err)
	}

	want := []struct{ path, kind string }{
		{"/api/v1/namespaces/serendip-system", "Namespace"},
		{"/api/v1/namespaces/serendip-system/configmaps/openbao-config", "ConfigMap"},
		{"/api/v1/namespaces/serendip-system/persistentvolumeclaims/openbao-data", "PersistentVolumeClaim"},
		{"/api/v1/namespaces/serendip-system/services/openbao", "Service"},
		{statefulSetPath, "StatefulSet"},
	}
	if len(got) != len(want) {
		t.Fatalf("applied %d objects, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		a := got[i]
		if a.path != w.path || a.body.Kind != w.kind {
			t.Errorf("apply[%d] = %s (%s), want %s (%s) -- dependency order: namespace, ConfigMap/PVC, Service, StatefulSet", i, a.path, a.body.Kind, w.path, w.kind)
		}
		if a.fieldManager != "serendip" || a.force != "true" {
			t.Errorf("apply[%d] fieldManager=%q force=%q, want serendip/true", i, a.fieldManager, a.force)
		}
		if a.body.Metadata.ResourceVersion != "" {
			t.Errorf("apply[%d] carries resourceVersion %q", i, a.body.Metadata.ResourceVersion)
		}
	}

	// List-map entries must carry every key of their associative list.
	var svc struct {
		Ports []map[string]any `json:"ports"`
	}
	json.Unmarshal(got[3].body.Spec, &svc)
	if len(svc.Ports) != 1 || svc.Ports[0]["protocol"] != "TCP" {
		t.Errorf("Service ports = %v, want one TCP port with protocol set", svc.Ports)
	}
	if !strings.Contains(string(got[4].body.Spec), `"image":"`+Image+`"`) ||
		!strings.Contains(string(got[4].body.Spec), `"containerPort":8200,"protocol":"TCP"`) {
		t.Errorf("StatefulSet spec missing image or protocol-keyed containerPort: %s", got[4].body.Spec)
	}
	var cmData map[string]string
	json.Unmarshal(got[1].body.Data, &cmData)
	if cmData["server.hcl"] != serverConfigHCL {
		t.Errorf("ConfigMap data = %v", cmData)
	}
}

// Re-running Ensure sends the identical applies: the API server, not this
// code, decides there is nothing to change.
func TestDeployer_Ensure_RepeatIsIdenticalApply(t *testing.T) {
	var got []appliedObject
	d := NewDeployer(newTestKubeClient(t, applyRecorder(t, &got)))
	for range 2 {
		if err := d.Ensure(context.Background()); err != nil {
			t.Fatalf("Ensure() = %v", err)
		}
	}
	if len(got) != 10 {
		t.Fatalf("applies = %d, want 10", len(got))
	}
	for i := range 5 {
		a, b := got[i], got[i+5]
		if a.path != b.path || string(a.body.Spec) != string(b.body.Spec) || string(a.body.Data) != string(b.body.Data) {
			t.Errorf("repeat apply %d differs: %+v vs %+v", i, a, b)
		}
	}
}

func TestDeployer_Ensure_WaitsAcrossMultipleNotYetReadyPolls(t *testing.T) {
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
		case r.URL.Path == "/api/v1/namespaces/serendip-system" && r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("simulated server error"))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	err := NewDeployer(client).Ensure(context.Background())
	if err == nil {
		t.Fatal("Ensure() = nil error, want the simulated namespace apply failure to propagate")
	}
	if !strings.Contains(err.Error(), "namespace") || !strings.Contains(err.Error(), "serendip-system") {
		t.Errorf("error = %q, want it to name the namespace that failed", err.Error())
	}
}
