package openchoreo

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

var testGateway = reconcile.GatewayEndpoint{Name: "serendip-external", Namespace: "kgateway-system", Host: "localhost", Port: 30080}

const (
	testCellNamespace = "dp-cell"
	testInvokeURL     = "http://development-openchoreo-control-plane.localhost:30080/cmp-x-http"
	routesPath        = "/apis/gateway.networking.k8s.io/v1/namespaces/" + testCellNamespace + "/httproutes"
)

var exposedSpec = resource.ComponentSpec{
	Image: "nginx:1.27-alpine",
	Ports: []resource.ComponentPort{{Name: "http", Port: 80, Expose: true}, {Name: "metrics", Port: 9113}},
}

func dataPlanePath() string {
	return dataPlaneAPIPath(ControlPlaneNamespace) + "/" + DefaultDataPlaneName
}

func TestEnsureComponent_ExposureMapsToEndpointTypeAndVisibility(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	if _, err := p.EnsureComponent(context.Background(), testApp(), testComp(exposedSpec)); err != nil {
		t.Fatal(err)
	}
	var wl workloadSpec
	json.Unmarshal(api.objects[nsItem("workloads", componentName(testCompID))].Spec, &wl)
	if got := string(mustJSON(wl.Endpoints)); got != `{"http":{"type":"HTTP","port":80,"visibility":["external"]},"metrics":{"type":"TCP","port":9113}}` {
		t.Errorf("endpoints = %s", got)
	}
}

// Un-exposing a port is just a Workload apply without visibility: the API
// server drops the field Serendip no longer applies, so no explicit null is
// needed (or sent).
func TestEnsureComponent_UnexposingAppliesWithoutVisibility(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	if _, err := p.EnsureComponent(context.Background(), testApp(), testComp(exposedSpec)); err != nil {
		t.Fatal(err)
	}

	internal := exposedSpec
	internal.Ports = []resource.ComponentPort{{Name: "http", Port: 80}, {Name: "metrics", Port: 9113}}
	obs, err := p.EnsureComponent(context.Background(), testApp(), testComp(internal))
	if err != nil {
		t.Fatal(err)
	}
	// A spec change is a new release, ready to deploy.
	if !obs.Ready || obs.Reason != "Released" || !strings.Contains(obs.Message, "release "+componentName(testCompID)+"-") {
		t.Errorf("after the change = (%v, %q, %q), want a new release", obs.Ready, obs.Reason, obs.Message)
	}
	wl := api.lastApply(t, nsItem("workloads", componentName(testCompID)))
	if strings.Contains(string(wl.Spec), "visibility") {
		t.Errorf("Workload apply = %s, want no visibility key at all", wl.Spec)
	}
	if !strings.Contains(string(wl.Spec), `"http":{"type":"TCP","port":80}`) {
		t.Errorf("Workload apply = %s, want http as a plain TCP endpoint", wl.Spec)
	}
}

type routeOpt struct {
	missing         bool
	otherGateway    bool
	accepted, refs  string
	noURL           bool
	bindingNotReady bool
	noCellNamespace bool
}

// deployedExposed builds the cluster state of a deployed, exposed
// Component and returns what observeReachability makes of its binding
// (Deploy asks only once the binding is Ready).
func deployedExposed(t *testing.T, o routeOpt) (reconcile.Reachability, *fakeAPI) {
	t.Helper()
	api := newFakeAPI().withComponentType()
	api.lists = map[string][]kubernetes.Object{}

	status := "True"
	if o.bindingNotReady {
		status = "False"
	}
	var endpoints []any
	if !o.noURL {
		endpoints = []any{map[string]any{"name": "http", "invokeURL": testInvokeURL}}
	}
	api.put(nsItem("releasebindings", releaseBindingName(testCompID)), kubernetes.Object{
		Metadata: kubernetes.ObjectMeta{Name: releaseBindingName(testCompID)},
		Spec:     mustJSON(map[string]string{"releaseName": "rel-1"}),
		Status: mustJSON(map[string]any{
			"conditions": []any{map[string]any{"type": "Ready", "status": status, "reason": "Ready"}},
			"endpoints":  endpoints,
		}),
	})
	api.put(nsItem("componentreleases", "rel-1"), kubernetes.Object{
		Spec: mustJSON(map[string]any{"workload": desiredWorkload(projectName(testAppID), componentName(testCompID), exposedSpec)}),
	})
	prbStatus := map[string]any{"namespace": testCellNamespace}
	if o.noCellNamespace {
		prbStatus = map[string]any{}
	}
	api.put(nsItem("projectreleasebindings", projectReleaseBindingName(testAppID)), kubernetes.Object{Status: mustJSON(prbStatus)})

	accepted, refs := o.accepted, o.refs
	if accepted == "" {
		accepted = "True"
	}
	if refs == "" {
		refs = "True"
	}
	parentName := testGateway.Name
	if o.otherGateway {
		parentName = "someone-elses-gateway"
	}
	var routes []kubernetes.Object
	if !o.missing {
		routes = append(routes, kubernetes.Object{
			Metadata: kubernetes.ObjectMeta{Name: "r1", Labels: map[string]string{"openchoreo.dev/endpoint-name": "http"}},
			Status: mustJSON(map[string]any{"parents": []any{map[string]any{
				"parentRef": map[string]any{"name": parentName, "namespace": testGateway.Namespace},
				"conditions": []any{
					map[string]any{"type": "Accepted", "status": accepted, "reason": "NoMatchingListener", "message": "no listener"},
					map[string]any{"type": "ResolvedRefs", "status": refs, "reason": "BackendNotFound", "message": "no backend"},
				},
			}}}),
		})
	}
	api.lists[routesPath] = routes

	return observe(t, api), api
}

// observe runs observeReachability over the stored binding and
// application binding.
func observe(t *testing.T, api *fakeAPI) reconcile.Reachability {
	t.Helper()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	binding := api.objects[nsItem("releasebindings", releaseBindingName(testCompID))]
	reach, err := p.observeReachability(context.Background(), projectReleaseBindingName(testAppID), testComp(exposedSpec), binding, exposedPorts(exposedSpec))
	if err != nil {
		t.Fatalf("observeReachability() = %v", err)
	}
	return reach
}

func TestObserveReachability(t *testing.T) {
	tests := []struct {
		name       string
		opt        routeOpt
		wantReach  bool
		wantReason string
	}{
		{"accepted route with URL is serving", routeOpt{}, true, reconcile.ReasonServing},
		{"route not rendered yet", routeOpt{missing: true}, false, reconcile.ReasonPending},
		{"route attached to a different gateway only", routeOpt{otherGateway: true}, false, reconcile.ReasonPending},
		{"route rejected by the gateway", routeOpt{accepted: "False"}, false, reconcile.ReasonRouteRejected},
		{"unresolved backend", routeOpt{refs: "False"}, false, reconcile.ReasonRouteRejected},
		{"acceptance unknown", routeOpt{accepted: "Unknown"}, false, reconcile.ReasonPending},
		{"no URL published yet", routeOpt{noURL: true}, false, reconcile.ReasonPending},
		{"cell namespace not reported", routeOpt{noCellNamespace: true}, false, reconcile.ReasonPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reach, _ := deployedExposed(t, tt.opt)
			if reach.Reachable != tt.wantReach || reach.Reason != tt.wantReason {
				t.Errorf("Reachable = {%v, %s, %q}, want {%v, %s}", reach.Reachable, reach.Reason, reach.Message, tt.wantReach, tt.wantReason)
			}
			if tt.wantReach {
				if len(reach.Endpoints) != 1 || reach.Endpoints[0] != (resource.ComponentEndpointStatus{Name: "http", URL: testInvokeURL}) {
					t.Errorf("Endpoints = %+v", reach.Endpoints)
				}
			}
		})
	}
}

func TestObserveReachability_RouteListUsesComponentAndVisibilitySelector(t *testing.T) {
	_, api := deployedExposed(t, routeOpt{})
	sel, err := url.ParseQuery(api.lastQuery)
	if err != nil {
		t.Fatal(err)
	}
	want := "openchoreo.dev/component=" + componentName(testCompID) + ",openchoreo.dev/endpoint-visibility=external"
	if got := sel.Get("labelSelector"); got != want {
		t.Errorf("labelSelector = %q, want %q", got, want)
	}
}

func TestEnsureComponent_NotExposedMakesNoRouteCalls(t *testing.T) {
	api := newFakeAPI().withComponentType()
	api.lists = map[string][]kubernetes.Object{routesPath: nil}
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)

	obs, err := p.EnsureComponent(context.Background(), testApp(), testComp(webSpec))
	if err != nil {
		t.Fatal(err)
	}
	if obs.Reachable != nil {
		t.Errorf("Reachable = %+v, want nil when nothing is exposed", obs.Reachable)
	}
	for _, c := range api.calls {
		if strings.Contains(c, "httproutes") || strings.Contains(c, "projectreleasebindings") {
			t.Errorf("unexpected call %s for a component with nothing exposed", c)
		}
	}
}
