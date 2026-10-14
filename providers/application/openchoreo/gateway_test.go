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
	api := newFakeAPI()
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
	api := newFakeAPI()
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
	if obs.Reason != reconcile.ReasonProgressing {
		t.Errorf("reason = %q, want Progressing right after the spec change", obs.Reason)
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
// Component and returns its observation.
func deployedExposed(t *testing.T, o routeOpt) (reconcile.ComponentObservation, *fakeAPI) {
	t.Helper()
	api := newFakeAPI()
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

	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	obs, err := p.EnsureComponent(context.Background(), testApp(), testComp(exposedSpec))
	if err != nil {
		t.Fatalf("EnsureComponent() = %v", err)
	}
	return obs, api
}

func TestEnsureComponent_ReachabilityObservation(t *testing.T) {
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
		{"workload not deployed", routeOpt{bindingNotReady: true}, false, reconcile.ReasonPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs, _ := deployedExposed(t, tt.opt)
			if obs.Reachable == nil {
				t.Fatal("Reachable = nil, want an observation for an exposed component")
			}
			if obs.Reachable.Reachable != tt.wantReach || obs.Reachable.Reason != tt.wantReason {
				t.Errorf("Reachable = {%v, %s, %q}, want {%v, %s}", obs.Reachable.Reachable, obs.Reachable.Reason, obs.Reachable.Message, tt.wantReach, tt.wantReason)
			}
			if tt.wantReach {
				if len(obs.Reachable.Endpoints) != 1 || obs.Reachable.Endpoints[0] != (resource.ComponentEndpointStatus{Name: "http", URL: testInvokeURL}) {
					t.Errorf("Endpoints = %+v", obs.Reachable.Endpoints)
				}
			}
		})
	}
}

func TestEnsureComponent_RouteListUsesComponentAndVisibilitySelector(t *testing.T) {
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
	api := newFakeAPI()
	api.lists = map[string][]kubernetes.Object{routesPath: nil}
	putBoundRelease(api, webSpec, readyStatus("True", "Ready"))
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
