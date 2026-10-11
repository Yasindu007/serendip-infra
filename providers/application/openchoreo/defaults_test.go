package openchoreo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// fakeAPI is a minimal in-memory Kubernetes API: objects keyed by their
// full item path, POST stores under collection+"/"+name, PATCH merges
// top-level metadata.labels only (all these tests need). failPath makes
// any request to a path with that prefix return 500.
type fakeAPI struct {
	mu       sync.Mutex
	objects  map[string]kubernetes.Object
	calls    []string // "METHOD path"
	failPath string
	// lastPatch is the raw body of the most recent PATCH request.
	lastPatch []byte
	// lists serves GETs on collection paths as {"items": ...}; lastQuery
	// is the raw query of the most recent such list request.
	lists     map[string][]kubernetes.Object
	lastQuery string
}

func newFakeAPI() *fakeAPI { return &fakeAPI{objects: map[string]kubernetes.Object{}} }

func (f *fakeAPI) put(path string, obj kubernetes.Object) { f.objects[path] = obj }

func (f *fakeAPI) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, method+" ") {
			n++
		}
	}
	return n
}

func (f *fakeAPI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		if f.failPath != "" && strings.HasPrefix(r.URL.Path, f.failPath) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("simulated failure"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			if items, ok := f.lists[r.URL.Path]; ok {
				f.lastQuery = r.URL.RawQuery
				json.NewEncoder(w).Encode(map[string]any{"items": items})
				return
			}
			obj, ok := f.objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(obj)
		case http.MethodPost:
			var obj kubernetes.Object
			if err := json.NewDecoder(r.Body).Decode(&obj); err != nil {
				t.Errorf("decode POST body: %v", err)
			}
			f.objects[r.URL.Path+"/"+obj.Metadata.Name] = obj
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(obj)
		case http.MethodPatch:
			obj, ok := f.objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			f.lastPatch, _ = io.ReadAll(r.Body)
			var patch map[string]json.RawMessage
			json.Unmarshal(f.lastPatch, &patch)
			if meta, ok := patch["metadata"]; ok {
				var m struct {
					Labels map[string]string `json:"labels"`
				}
				json.Unmarshal(meta, &m)
				if obj.Metadata.Labels == nil {
					obj.Metadata.Labels = map[string]string{}
				}
				for k, v := range m.Labels {
					obj.Metadata.Labels[k] = v
				}
			}
			if spec, ok := patch["spec"]; ok {
				obj.Spec = mergeJSON(obj.Spec, spec)
			}
			f.objects[r.URL.Path] = obj
			json.NewEncoder(w).Encode(obj)
		case http.MethodDelete:
			if _, ok := f.objects[r.URL.Path]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(f.objects, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("{}"))
		}
	}
}

// mergeJSON is a one-level-deep merge of patch's top-level keys into base,
// enough to model the merge patches this package sends.
func mergeJSON(base, patch json.RawMessage) json.RawMessage {
	b := map[string]json.RawMessage{}
	json.Unmarshal(base, &b)
	p := map[string]json.RawMessage{}
	json.Unmarshal(patch, &p)
	for k, v := range p {
		b[k] = v
	}
	out, _ := json.Marshal(b)
	return out
}

const nsPath = "/api/v1/namespaces/openchoreo-control-plane"

func TestEnsurePlatformDefaults_AllAbsent_CreatesEachOnce(t *testing.T) {
	api := newFakeAPI()
	api.put(nsPath, kubernetes.Object{APIVersion: "v1", Kind: "Namespace", Metadata: kubernetes.ObjectMeta{Name: ControlPlaneNamespace}})
	client := newTestKubeClient(t, api.handler(t))

	if err := EnsurePlatformDefaults(context.Background(), client); err != nil {
		t.Fatalf("EnsurePlatformDefaults() = %v", err)
	}

	if api.objects[nsPath].Metadata.Labels[controlPlaneNamespaceLabel] != "true" {
		t.Errorf("namespace labels = %v, want %s=true", api.objects[nsPath].Metadata.Labels, controlPlaneNamespaceLabel)
	}
	wantPaths := map[string]string{
		"/apis/openchoreo.dev/v1alpha1/clusterprojecttypes/serendip-default":                             "ClusterProjectType",
		"/apis/openchoreo.dev/v1alpha1/clustercomponenttypes/serendip-service":                           "ClusterComponentType",
		"/apis/openchoreo.dev/v1alpha1/namespaces/openchoreo-control-plane/environments/development":     "Environment",
		"/apis/openchoreo.dev/v1alpha1/namespaces/openchoreo-control-plane/deploymentpipelines/serendip": "DeploymentPipeline",
	}
	for path, kind := range wantPaths {
		obj, ok := api.objects[path]
		if !ok {
			t.Errorf("missing %s at %s", kind, path)
			continue
		}
		if obj.Kind != kind || obj.APIVersion != apiVersion {
			t.Errorf("%s: kind/apiVersion = %s/%s", path, obj.Kind, obj.APIVersion)
		}
	}
	if got := api.count(http.MethodPost); got != 4 {
		t.Errorf("POST calls = %d, want 4", got)
	}
	if got := api.count(http.MethodPatch); got != 1 {
		t.Errorf("PATCH calls = %d, want 1", got)
	}

	var env struct {
		DataPlaneRef struct{ Kind, Name string } `json:"dataPlaneRef"`
	}
	json.Unmarshal(api.objects["/apis/openchoreo.dev/v1alpha1/namespaces/openchoreo-control-plane/environments/development"].Spec, &env)
	if env.DataPlaneRef.Kind != "DataPlane" || env.DataPlaneRef.Name != DefaultDataPlaneName {
		t.Errorf("Environment dataPlaneRef = %+v, want DataPlane/%s", env.DataPlaneRef, DefaultDataPlaneName)
	}

	var ct struct {
		WorkloadType string `json:"workloadType"`
		Resources    []struct {
			ID string `json:"id"`
		} `json:"resources"`
	}
	json.Unmarshal(api.objects["/apis/openchoreo.dev/v1alpha1/clustercomponenttypes/serendip-service"].Spec, &ct)
	if ct.WorkloadType != "deployment" {
		t.Errorf("component type workloadType = %q, want deployment", ct.WorkloadType)
	}
	var ids []string
	for _, r := range ct.Resources {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "deployment,service,httproute-external,env-config" {
		t.Errorf("component type resources = %v, want [deployment service httproute-external env-config]", ids)
	}
}

func TestEnsurePlatformDefaults_AllPresent_NoWrites(t *testing.T) {
	api := newFakeAPI()
	client := newTestKubeClient(t, api.handler(t))
	api.put(nsPath, kubernetes.Object{APIVersion: "v1", Kind: "Namespace",
		Metadata: kubernetes.ObjectMeta{Name: ControlPlaneNamespace, Labels: map[string]string{controlPlaneNamespaceLabel: "true"}}})
	if err := EnsurePlatformDefaults(context.Background(), client); err != nil {
		t.Fatalf("first EnsurePlatformDefaults() = %v", err)
	}
	api.calls = nil

	if err := EnsurePlatformDefaults(context.Background(), client); err != nil {
		t.Fatalf("second EnsurePlatformDefaults() = %v", err)
	}
	if n := api.count(http.MethodPost) + api.count(http.MethodPatch); n != 0 {
		t.Errorf("writes on second run = %d (%v), want 0", n, api.calls)
	}
}

func TestEnsurePlatformDefaults_ErrorsNameTheFailingStep(t *testing.T) {
	tests := []struct {
		failPath string
		wantIn   string
	}{
		{nsPath, "label namespace"},
		{"/apis/openchoreo.dev/v1alpha1/clustercomponenttypes", "ClusterComponentType"},
		{"/apis/openchoreo.dev/v1alpha1/namespaces/openchoreo-control-plane/deploymentpipelines", "DeploymentPipeline"},
	}
	for _, tt := range tests {
		t.Run(tt.wantIn, func(t *testing.T) {
			api := newFakeAPI()
			api.put(nsPath, kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: ControlPlaneNamespace}})
			api.failPath = tt.failPath
			client := newTestKubeClient(t, api.handler(t))

			err := EnsurePlatformDefaults(context.Background(), client)
			if err == nil || !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("EnsurePlatformDefaults() = %v, want error mentioning %q", err, tt.wantIn)
			}
		})
	}
}
