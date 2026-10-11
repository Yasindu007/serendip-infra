package gatewayapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// fakeAPI is a minimal in-memory Kubernetes API keyed by item path.
type fakeAPI struct {
	mu        sync.Mutex
	objects   map[string]kubernetes.Object
	calls     []string
	patches   map[string][]byte
	failPath  string
	t         *testing.T
	createErr bool
}

func newFakeAPI(t *testing.T) *fakeAPI {
	return &fakeAPI{objects: map[string]kubernetes.Object{}, patches: map[string][]byte{}, t: t}
}

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

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if f.failPath != "" && strings.HasPrefix(r.URL.Path, f.failPath) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		obj, ok := f.objects[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(obj)
	case http.MethodPost:
		var obj kubernetes.Object
		json.NewDecoder(r.Body).Decode(&obj)
		f.objects[r.URL.Path+"/"+obj.Metadata.Name] = obj
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(obj)
	case http.MethodPatch:
		body, _ := io.ReadAll(r.Body)
		f.patches[r.URL.Path] = body
		obj := f.objects[r.URL.Path]
		var p struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Spec json.RawMessage `json:"spec"`
		}
		json.Unmarshal(body, &p)
		obj.Metadata.Annotations = p.Metadata.Annotations
		obj.Spec = p.Spec
		f.objects[r.URL.Path] = obj
		json.NewEncoder(w).Encode(obj)
	}
}

func newTestClient(t *testing.T, api *fakeAPI) *kubernetes.Client {
	t.Helper()
	srv := httptest.NewTLSServer(api)
	t.Cleanup(srv.Close)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	client, err := kubernetes.NewClient(kubernetes.Config{Host: srv.URL, CAData: caPEM})
	if err != nil {
		t.Fatalf("NewClient() = %v", err)
	}
	return client
}

const crdPath = "/apis/apiextensions.k8s.io/v1/customresourcedefinitions"
const vapPath = "/apis/admissionregistration.k8s.io/v1/validatingadmissionpolicies"

func manifest(version string) string {
	return `# header
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  annotations:
    api-approved.kubernetes.io: https://example.invalid/pull/1
    gateway.networking.k8s.io/bundle-version: ` + version + `
  name: gateways.gateway.networking.k8s.io
spec:
  group: gateway.networking.k8s.io
  versions: [{name: v1, served: true}]
status:
  storedVersions: []
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  annotations:
    gateway.networking.k8s.io/bundle-version: ` + version + `
  name: safe-upgrades.gateway.networking.k8s.io
spec:
  failurePolicy: Fail
`
}

func staticFetch(body string, called *int) func(context.Context) ([]byte, error) {
	return func(context.Context) ([]byte, error) {
		*called++
		return []byte(body), nil
	}
}

func withVersion(v string) kubernetes.Object {
	return kubernetes.Object{Metadata: kubernetes.ObjectMeta{Annotations: map[string]string{bundleVersionAnnotation: v}}}
}

func TestEnsure_AbsentObjectsAreCreatedWithAnnotations(t *testing.T) {
	api := newFakeAPI(t)
	client := newTestClient(t, api)
	fetches := 0

	if err := ensure(context.Background(), client, staticFetch(manifest(Version), &fetches)); err != nil {
		t.Fatalf("ensure() = %v", err)
	}
	if fetches != 1 {
		t.Errorf("fetches = %d, want 1", fetches)
	}
	crd, ok := api.objects[crdPath+"/gateways.gateway.networking.k8s.io"]
	if !ok {
		t.Fatal("CRD not created")
	}
	if crd.Metadata.Annotations["api-approved.kubernetes.io"] == "" {
		t.Errorf("annotations = %v, want api-approved.kubernetes.io preserved", crd.Metadata.Annotations)
	}
	if len(crd.Status) != 0 {
		t.Errorf("status sent on create = %s, want none", crd.Status)
	}
	if _, ok := api.objects[vapPath+"/safe-upgrades.gateway.networking.k8s.io"]; !ok {
		t.Error("ValidatingAdmissionPolicy not created")
	}
}

func TestEnsure_OlderBundleVersionIsUpgradedOnce(t *testing.T) {
	api := newFakeAPI(t)
	api.objects[crdPath+"/gateways.gateway.networking.k8s.io"] = withVersion("v1.3.0")
	api.objects[vapPath+"/safe-upgrades.gateway.networking.k8s.io"] = withVersion(Version)
	client := newTestClient(t, api)
	fetches := 0

	if err := ensure(context.Background(), client, staticFetch(manifest(Version), &fetches)); err != nil {
		t.Fatalf("ensure() = %v", err)
	}
	if n := api.count(http.MethodPatch); n != 1 {
		t.Fatalf("PATCH calls = %d (%v), want 1 (only the older CRD)", n, api.calls)
	}
	body := string(api.patches[crdPath+"/gateways.gateway.networking.k8s.io"])
	if !strings.Contains(body, `"gateway.networking.k8s.io/bundle-version":"`+Version+`"`) || !strings.Contains(body, `"versions"`) {
		t.Errorf("patch body = %s, want new bundle-version annotation and full spec", body)
	}
	if api.count(http.MethodPost) != 0 {
		t.Errorf("unexpected creates: %v", api.calls)
	}
}

func TestEnsure_NewerBundleVersionIsNeverDowngraded(t *testing.T) {
	api := newFakeAPI(t)
	api.objects[crdPath+"/gateways.gateway.networking.k8s.io"] = withVersion("v1.7.0")
	client := newTestClient(t, api)
	fetches := 0

	if err := ensure(context.Background(), client, staticFetch(manifest(Version), &fetches)); err != nil {
		t.Fatalf("ensure() = %v", err)
	}
	if _, ok := api.patches[crdPath+"/gateways.gateway.networking.k8s.io"]; ok {
		t.Error("newer CRD was patched, want it left alone")
	}
}

func TestEnsure_AllPinnedPresentAtVersion_SkipsDownload(t *testing.T) {
	api := newFakeAPI(t)
	for _, ref := range pinnedObjects {
		api.objects[collectionPaths[ref.kind]+"/"+ref.name] = withVersion(Version)
	}
	client := newTestClient(t, api)
	fetches := 0

	if err := ensure(context.Background(), client, staticFetch(manifest(Version), &fetches)); err != nil {
		t.Fatalf("ensure() = %v", err)
	}
	if fetches != 0 {
		t.Errorf("fetches = %d, want 0 when everything is already at %s", fetches, Version)
	}
	if n := api.count(http.MethodPost) + api.count(http.MethodPatch); n != 0 {
		t.Errorf("writes = %d, want 0", n)
	}
}

func TestEnsure_OnePinnedObjectOlder_Downloads(t *testing.T) {
	api := newFakeAPI(t)
	for _, ref := range pinnedObjects {
		api.objects[collectionPaths[ref.kind]+"/"+ref.name] = withVersion(Version)
	}
	api.objects[crdPath+"/httproutes.gateway.networking.k8s.io"] = withVersion("v1.3.0")
	client := newTestClient(t, api)
	fetches := 0

	if err := ensure(context.Background(), client, staticFetch(manifest(Version), &fetches)); err != nil {
		t.Fatalf("ensure() = %v", err)
	}
	if fetches != 1 {
		t.Errorf("fetches = %d, want 1", fetches)
	}
}

func TestEnsure_FetchErrorPropagates(t *testing.T) {
	client := newTestClient(t, newFakeAPI(t))
	err := ensure(context.Background(), client, func(context.Context) ([]byte, error) { return nil, errors.New("offline") })
	if err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("ensure() = %v, want the fetch error", err)
	}
}

func TestApplyManifest_RejectsUnknownKinds(t *testing.T) {
	client := newTestClient(t, newFakeAPI(t))
	err := applyManifest(context.Background(), client, []byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: x\n"))
	if err == nil {
		t.Error("applyManifest() = nil, want an error for an unexpected kind")
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.3.0", "v1.6.1", -1},
		{"v1.6.1", "v1.6.1", 0},
		{"v1.10.0", "v1.6.1", 1},
		{"v1.6.1-rc1", "v1.6.1", 0},
		{"", "v1.6.1", -1},
		{"garbage", "v1.6.1", -1},
	}
	for _, tt := range tests {
		if got := compareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestPinnedObjectsHaveCollections(t *testing.T) {
	for _, ref := range pinnedObjects {
		if _, ok := collectionPaths[ref.kind]; !ok {
			t.Errorf("pinned %s %s has no collection path", ref.kind, ref.name)
		}
	}
}

func TestFetchManifest_VerifiesChecksum(t *testing.T) {
	body := []byte(manifest(Version))
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()

	if got, err := fetchManifest(context.Background(), srv.Client(), srv.URL, hex.EncodeToString(sum[:])); err != nil || string(got) != string(body) {
		t.Fatalf("fetchManifest(good sha) = (%d bytes, %v)", len(got), err)
	}
	if _, err := fetchManifest(context.Background(), srv.Client(), srv.URL, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("fetchManifest(bad sha) = %v, want checksum mismatch", err)
	}
}

func TestFetchManifest_TooLargeAndNon200(t *testing.T) {
	big := make([]byte, maxManifestBytes+10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(big) }))
	defer srv.Close()
	if _, err := fetchManifest(context.Background(), srv.Client(), srv.URL, ""); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("oversized = %v, want size error", err)
	}
	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer srv404.Close()
	if _, err := fetchManifest(context.Background(), srv404.Client(), srv404.URL, ""); err == nil {
		t.Error("404 = nil, want error")
	}
}

func TestSplitYAMLDocuments(t *testing.T) {
	docs := splitYAMLDocuments([]byte("a: 1\n--- # next\nb: 2\r\n---   \nc: 3\n---x: not a separator\n"))
	if len(docs) != 3 || !strings.Contains(string(docs[2]), "---x") {
		t.Errorf("splitYAMLDocuments = %q, want 3 docs with '---x' kept in the last", docs)
	}
}
