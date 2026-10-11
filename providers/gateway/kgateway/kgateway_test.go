package kgateway

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

type fakeHelm struct {
	calls [][]string
	errs  []error
}

func (f *fakeHelm) run(ctx context.Context, args ...string) ([]byte, error) {
	i := len(f.calls)
	f.calls = append(f.calls, args)
	if i < len(f.errs) {
		return nil, f.errs[i]
	}
	return nil, nil
}

func TestDeployer_Ensure_CRDsThenChartsInOrder(t *testing.T) {
	h := &fakeHelm{}
	var order []string
	d := &Deployer{run: func(ctx context.Context, args ...string) ([]byte, error) {
		order = append(order, "helm:"+args[2])
		return h.run(ctx, args...)
	}, ensureCRDs: func(context.Context, *kubernetes.Client) error {
		order = append(order, "gatewayapi")
		return nil
	}}

	if err := d.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() = %v", err)
	}
	if got := strings.Join(order, ","); got != "gatewayapi,helm:kgateway-crds,helm:kgateway" {
		t.Errorf("order = %s", got)
	}
	main := strings.Join(h.calls[1], " ")
	for _, want := range []string{"--version " + Version, "--namespace " + Namespace, "controller.extraEnv.KGW_ENABLE_EXPERIMENTAL_GATEWAY_API_FEATURES=false", "--wait"} {
		if !strings.Contains(main, want) {
			t.Errorf("controller install args %q missing %q", main, want)
		}
	}
	if !strings.Contains(strings.Join(h.calls[0], " "), crdsChart+" --version "+Version) {
		t.Errorf("crds install args = %v", h.calls[0])
	}
}

func TestDeployer_Ensure_FailuresStopEarly(t *testing.T) {
	d := &Deployer{run: (&fakeHelm{}).run, ensureCRDs: func(context.Context, *kubernetes.Client) error { return errors.New("offline") }}
	if err := d.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("Ensure() = %v, want the Gateway API error", err)
	}

	h := &fakeHelm{errs: []error{errors.New("crds failed")}}
	d = &Deployer{run: h.run, ensureCRDs: func(context.Context, *kubernetes.Client) error { return nil }}
	if err := d.Ensure(context.Background()); err == nil {
		t.Fatal("Ensure() = nil, want the crds failure")
	}
	if len(h.calls) != 1 {
		t.Errorf("helm calls = %d, want 1 (controller install must not run after a crds failure)", len(h.calls))
	}
}

// fakeAPI stores objects by item path. PATCH replaces the spec wholesale
// with the patch's spec (enough for the merge patches this package sends,
// which always carry the full owned spec). Every write bumps generation.
type fakeAPI struct {
	mu       sync.Mutex
	objects  map[string]kubernetes.Object
	calls    []string
	failPath string
	// onGet lets a test mutate state (e.g. mark programmed) on each GET.
	onGet func(path string, obj *kubernetes.Object)
}

func newFakeAPI() *fakeAPI { return &fakeAPI{objects: map[string]kubernetes.Object{}} }

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
		if f.onGet != nil {
			f.onGet(r.URL.Path, &obj)
			f.objects[r.URL.Path] = obj
		}
		json.NewEncoder(w).Encode(obj)
	case http.MethodPost:
		var obj kubernetes.Object
		json.NewDecoder(r.Body).Decode(&obj)
		obj.Metadata.Generation = 1
		f.objects[r.URL.Path+"/"+obj.Metadata.Name] = obj
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(obj)
	case http.MethodPatch:
		body, _ := io.ReadAll(r.Body)
		var p struct {
			Spec json.RawMessage `json:"spec"`
		}
		json.Unmarshal(body, &p)
		obj := f.objects[r.URL.Path]
		obj.Spec = p.Spec
		obj.Metadata.Generation++
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

func programmed(status string, gen int64) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"conditions": []any{map[string]any{
		"type": "Programmed", "status": status, "reason": "TestReason", "message": "test message", "observedGeneration": gen,
	}}})
	return b
}

// markProgrammed makes every GET of the Gateway report Programmed=True for
// its current generation.
func markProgrammed(path string, obj *kubernetes.Object) {
	if strings.HasPrefix(path, gatewaysPath) {
		obj.Status = programmed("True", obj.Metadata.Generation)
	}
}

func testProvider(t *testing.T, api *fakeAPI) *Provider {
	p := NewProvider(newTestClient(t, api), "localhost", 30080, map[string]string{"example.dev/proxy": "gateway"})
	p.pollInterval = time.Millisecond
	p.timeout = 50 * time.Millisecond
	return p
}

func TestEnsureGateway_FreshCreatesParametersAndGateway(t *testing.T) {
	api := newFakeAPI()
	api.onGet = markProgrammed
	p := testProvider(t, api)

	ep, ready, _, _, err := p.EnsureGateway(context.Background())
	if err != nil || !ready {
		t.Fatalf("EnsureGateway() = (ready %v, err %v), want ready", ready, err)
	}
	if ep.Name != GatewayName || ep.Namespace != Namespace || ep.Host != "localhost" || ep.Port != 30080 {
		t.Errorf("endpoint = %+v", ep)
	}

	params := api.objects[parametersPath+"/"+GatewayName]
	if params.Kind != "GatewayParameters" || params.APIVersion != "gateway.kgateway.dev/v1alpha1" {
		t.Errorf("parameters kind/apiVersion = %s/%s", params.Kind, params.APIVersion)
	}
	if got := string(params.Spec); got != `{"kube":{"service":{"type":"NodePort","ports":[{"port":80,"nodePort":30080}]}}}` {
		t.Errorf("parameters spec = %s", got)
	}

	gw := api.objects[gatewaysPath+"/"+GatewayName]
	var spec gatewaySpec
	json.Unmarshal(gw.Spec, &spec)
	if spec.GatewayClassName != "kgateway" || spec.Infrastructure.ParametersRef.Name != GatewayName ||
		spec.Infrastructure.ParametersRef.Kind != "GatewayParameters" || len(spec.Listeners) != 1 ||
		spec.Listeners[0].Port != 80 || spec.Listeners[0].Protocol != "HTTP" || spec.Listeners[0].AllowedRoutes.Namespaces.From != "All" {
		t.Errorf("gateway spec = %s", gw.Spec)
	}
}

func TestEnsureGateway_UnchangedMakesNoWrites(t *testing.T) {
	api := newFakeAPI()
	api.onGet = markProgrammed
	p := testProvider(t, api)
	if _, _, _, _, err := p.EnsureGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	api.calls = nil

	if _, ready, _, _, err := p.EnsureGateway(context.Background()); err != nil || !ready {
		t.Fatalf("second EnsureGateway() = (%v, %v)", ready, err)
	}
	if n := api.count(http.MethodPost) + api.count(http.MethodPatch); n != 0 {
		t.Errorf("writes on unchanged ensure = %v", api.calls)
	}
}

func TestEnsureGateway_ChangedNodePortPatchesParameters(t *testing.T) {
	api := newFakeAPI()
	api.onGet = markProgrammed
	if _, _, _, _, err := testProvider(t, api).EnsureGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	api.calls = nil

	p := NewProvider(newTestClient(t, api), "localhost", 31000, map[string]string{"example.dev/proxy": "gateway"})
	p.pollInterval, p.timeout = time.Millisecond, 50*time.Millisecond
	if _, _, _, _, err := p.EnsureGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := api.count(http.MethodPatch); n != 1 {
		t.Fatalf("PATCH calls = %v, want exactly one (GatewayParameters)", api.calls)
	}
	if !strings.Contains(string(api.objects[parametersPath+"/"+GatewayName].Spec), `"nodePort":31000`) {
		t.Errorf("parameters spec = %s", api.objects[parametersPath+"/"+GatewayName].Spec)
	}
}

func TestEnsureGateway_NeverProgrammedReportsNotReady(t *testing.T) {
	api := newFakeAPI()
	api.onGet = func(path string, obj *kubernetes.Object) {
		if strings.HasPrefix(path, gatewaysPath) {
			obj.Status = programmed("False", obj.Metadata.Generation)
		}
	}
	ep, ready, reason, message, err := testProvider(t, api).EnsureGateway(context.Background())
	if err != nil || ready {
		t.Fatalf("= (ready %v, err %v), want not ready, no error", ready, err)
	}
	if reason != "TestReason" || message != "test message" || ep.Name != GatewayName {
		t.Errorf("reason/message = %q/%q, ep %+v", reason, message, ep)
	}
}

func TestEnsureGateway_StaleProgrammedConditionIgnored(t *testing.T) {
	api := newFakeAPI()
	api.onGet = func(path string, obj *kubernetes.Object) {
		if strings.HasPrefix(path, gatewaysPath) {
			obj.Metadata.Generation = 3
			obj.Status = programmed("True", 2)
		}
	}
	if _, ready, _, _, err := testProvider(t, api).EnsureGateway(context.Background()); err != nil || ready {
		t.Errorf("= (ready %v, err %v), want not ready for a condition from an older generation", ready, err)
	}
}

func TestEnsureGateway_APIErrorPropagates(t *testing.T) {
	api := newFakeAPI()
	api.failPath = parametersPath
	if _, _, _, _, err := testProvider(t, api).EnsureGateway(context.Background()); err == nil {
		t.Error("EnsureGateway() = nil error, want the API failure")
	}
}
