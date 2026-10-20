package openchoreo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// fakeAPI is a minimal in-memory Kubernetes API: objects keyed by their
// full item path, POST stores under collection+"/"+name, a merge PATCH
// merges metadata.labels and top-level spec keys, and a server-side apply
// (recorded as method "APPLY") creates or updates the object the way the
// API server does for these tests' purposes: spec/data replaced by the
// applied ones, labels merged, status kept, generation bumped only when the
// spec really changes, and nothing counted as stored when nothing changed.
// failPath makes any request to a path with that prefix return 500.
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
	// applies is every apply request, in order; writes counts the applies
	// that actually changed a stored object.
	applies []appliedReq
	writes  int
}

type appliedReq struct {
	path, fieldManager, force string
	body                      kubernetes.Object
}

const methodApply = "APPLY"

func isApplyRequest(r *http.Request) bool {
	return r.Method == http.MethodPatch && r.Header.Get("Content-Type") == "application/apply-patch+yaml"
}

// lastApply returns the most recent apply to path.
func (f *fakeAPI) lastApply(t *testing.T, path string) kubernetes.Object {
	t.Helper()
	for i := len(f.applies) - 1; i >= 0; i-- {
		if f.applies[i].path == path {
			return f.applies[i].body
		}
	}
	t.Fatalf("no apply to %s (calls: %v)", path, f.calls)
	return kubernetes.Object{}
}

func jsonEqual(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var av, bv any
	json.Unmarshal(a, &av)
	json.Unmarshal(b, &bv)
	return reflect.DeepEqual(av, bv)
}

func (f *fakeAPI) apply(t *testing.T, w http.ResponseWriter, r *http.Request) {
	var in kubernetes.Object
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		t.Errorf("decode apply body: %v", err)
	}
	q := r.URL.Query()
	f.applies = append(f.applies, appliedReq{path: r.URL.Path, fieldManager: q.Get("fieldManager"), force: q.Get("force"), body: in})

	obj, existed := f.objects[r.URL.Path]
	changed := !existed || !jsonEqual(obj.Spec, in.Spec) || !jsonEqual(obj.Data, in.Data)
	if !existed {
		obj = kubernetes.Object{APIVersion: in.APIVersion, Kind: in.Kind,
			Metadata: kubernetes.ObjectMeta{Name: in.Metadata.Name, Namespace: in.Metadata.Namespace, Generation: 1}}
	} else if !jsonEqual(obj.Spec, in.Spec) {
		obj.Metadata.Generation++
	}
	obj.Spec, obj.Data = in.Spec, in.Data
	for k, v := range in.Metadata.Labels {
		if obj.Metadata.Labels == nil {
			obj.Metadata.Labels = map[string]string{}
		}
		if obj.Metadata.Labels[k] != v {
			obj.Metadata.Labels[k] = v
			changed = true
		}
	}
	if changed {
		f.writes++
	}
	f.objects[r.URL.Path] = obj
	json.NewEncoder(w).Encode(obj)
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
		method := r.Method
		if isApplyRequest(r) {
			method = methodApply
		}
		f.calls = append(f.calls, method+" "+r.URL.Path)
		if f.failPath != "" && strings.HasPrefix(r.URL.Path, f.failPath) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("simulated failure"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if method == methodApply {
			f.apply(t, w, r)
			return
		}
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

func TestEnsurePlatformDefaults_AllAbsent_AppliesEach(t *testing.T) {
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
	// RP-018: the OpenChoreo API is closed to every pod.
	policyPath := "/apis/networking.k8s.io/v1/namespaces/openchoreo-control-plane/networkpolicies/serendip-deny-openchoreo-api"
	if policy, ok := api.objects[policyPath]; !ok || policy.Kind != "NetworkPolicy" {
		t.Errorf("missing the NetworkPolicy closing the OpenChoreo API at %s", policyPath)
	} else {
		var spec struct {
			PodSelector struct {
				MatchLabels map[string]string `json:"matchLabels"`
			} `json:"podSelector"`
			PolicyTypes []string `json:"policyTypes"`
			Ingress     []any    `json:"ingress"`
		}
		if err := json.Unmarshal(policy.Spec, &spec); err != nil {
			t.Fatal(err)
		}
		if spec.PodSelector.MatchLabels["app.kubernetes.io/component"] != "api-server" ||
			len(spec.PolicyTypes) != 1 || spec.PolicyTypes[0] != "Ingress" || spec.Ingress == nil || len(spec.Ingress) != 0 {
			t.Errorf("policy spec = %+v, want api-server pods selected, Ingress only, and an empty (deny-all) ingress list", spec)
		}
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
	if got := api.count(methodApply); got != 5 {
		t.Errorf("apply calls = %d, want 5", got)
	}
	for _, a := range api.applies {
		if a.fieldManager != kubernetes.FieldManager || a.force != "true" {
			t.Errorf("%s: fieldManager=%q force=%q, want %s/true", a.path, a.fieldManager, a.force, kubernetes.FieldManager)
		}
	}
	// The namespace label is the one merge patch: Helm owns that namespace.
	if got := api.count(http.MethodPatch); got != 1 {
		t.Errorf("merge PATCH calls = %d, want 1 (namespace label)", got)
	}
	if got := api.count(http.MethodPost); got != 0 {
		t.Errorf("POST calls = %d, want 0 (apply creates)", got)
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

func TestEnsurePlatformDefaults_AllPresent_NothingStored(t *testing.T) {
	api := newFakeAPI()
	client := newTestKubeClient(t, api.handler(t))
	api.put(nsPath, kubernetes.Object{APIVersion: "v1", Kind: "Namespace",
		Metadata: kubernetes.ObjectMeta{Name: ControlPlaneNamespace, Labels: map[string]string{controlPlaneNamespaceLabel: "true"}}})
	if err := EnsurePlatformDefaults(context.Background(), client); err != nil {
		t.Fatalf("first EnsurePlatformDefaults() = %v", err)
	}
	api.calls, api.writes = nil, 0

	if err := EnsurePlatformDefaults(context.Background(), client); err != nil {
		t.Fatalf("second EnsurePlatformDefaults() = %v", err)
	}
	if n := api.count(http.MethodPost) + api.count(http.MethodPatch); n != 0 || api.writes != 0 {
		t.Errorf("second run: %d create/merge writes, %d stored applies (%v), want none", n, api.writes, api.calls)
	}
}

// A default deleted by hand comes back on the next pass (cmd's foundations
// loop), and a default edited by hand is put back.
func TestEnsurePlatformDefaults_RepairsDeletedAndEdited(t *testing.T) {
	api := newFakeAPI()
	api.put(nsPath, kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: ControlPlaneNamespace, Labels: map[string]string{controlPlaneNamespaceLabel: "true"}}})
	client := newTestKubeClient(t, api.handler(t))
	if err := EnsurePlatformDefaults(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	envPath := namespacedPath("environments") + "/" + environmentName
	ctPath := clusterPath("clustercomponenttypes") + "/" + componentTypeName
	want := api.objects[ctPath].Spec
	delete(api.objects, envPath)
	edited := api.objects[ctPath]
	edited.Spec = mustJSON(map[string]any{"workloadType": "statefulset"})
	api.objects[ctPath] = edited

	if err := EnsurePlatformDefaults(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.objects[envPath]; !ok {
		t.Error("deleted Environment was not recreated")
	}
	if !jsonEqual(api.objects[ctPath].Spec, want) {
		t.Errorf("edited component type spec = %s, want restored", api.objects[ctPath].Spec)
	}
}

func TestEnsurePlatformDefaults_ErrorsNameTheFailingStep(t *testing.T) {
	tests := []struct {
		failPath string
		wantIn   string
	}{
		{nsPath, "label namespace"},
		{"/apis/openchoreo.dev/v1alpha1/clustercomponenttypes/", "ClusterComponentType"},
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
