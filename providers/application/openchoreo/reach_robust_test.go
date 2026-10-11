package openchoreo

import (
	"context"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

func TestEnsureComponent_RouteListErrorKeepsReadyAndReportsPending(t *testing.T) {
	api := newFakeAPI()
	api.failPath = routesPath
	api.lists = map[string][]kubernetes.Object{}
	putExposedDeployment(api)
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)

	obs, err := p.EnsureComponent(context.Background(), testApp(), testComp(exposedSpec))
	if err != nil {
		t.Fatalf("EnsureComponent() = %v, want the route error folded into Reachable", err)
	}
	if !obs.Ready || obs.Reason != reconcile.ReasonDeployed {
		t.Errorf("Ready = (%v, %s), want the healthy workload's Deployed kept", obs.Ready, obs.Reason)
	}
	if obs.Reachable == nil || obs.Reachable.Reachable || obs.Reachable.Reason != reconcile.ReasonPending {
		t.Errorf("Reachable = %+v, want Pending", obs.Reachable)
	}
}

func TestEnsureComponent_StaleRouteConditionIsPending(t *testing.T) {
	obs, _ := deployedExposedWith(t, func(route *kubernetes.Object) { route.Metadata.Generation = 2 })
	if obs.Reachable == nil || obs.Reachable.Reachable || obs.Reachable.Reason != reconcile.ReasonPending {
		t.Errorf("Reachable = %+v, want Pending for Accepted recorded at an older route generation", obs.Reachable)
	}
}

func TestEnsureComponent_ParentWithoutNamespaceDoesNotMatch(t *testing.T) {
	obs, _ := deployedExposedWith(t, func(route *kubernetes.Object) {
		route.Status = mustJSON(map[string]any{"parents": []any{map[string]any{
			"parentRef": map[string]any{"name": testGateway.Name},
			"conditions": []any{
				map[string]any{"type": "Accepted", "status": "True"},
				map[string]any{"type": "ResolvedRefs", "status": "True"},
			},
		}}})
	})
	if obs.Reachable == nil || obs.Reachable.Reachable {
		t.Errorf("Reachable = %+v, want not attached: an empty parentRef namespace means the route's own", obs.Reachable)
	}
}

func TestEndpointURL_IPv6Host(t *testing.T) {
	if got := (endpointURL{Scheme: "http", Host: "fd00::1", Port: 30080, Path: "/p"}).String(); got != "http://[fd00::1]:30080/p" {
		t.Errorf("String() = %q", got)
	}
}

// putExposedDeployment stores a deployed, exposed component's binding,
// release, and application binding (no routes).
func putExposedDeployment(api *fakeAPI) {
	api.put(nsItem("releasebindings", releaseBindingName(testCompID)), kubernetes.Object{
		Metadata: kubernetes.ObjectMeta{Name: releaseBindingName(testCompID)},
		Spec:     mustJSON(map[string]string{"releaseName": "rel-1"}),
		Status: mustJSON(map[string]any{
			"conditions": []any{map[string]any{"type": "Ready", "status": "True", "reason": "Ready"}},
			"endpoints":  []any{map[string]any{"name": "http", "invokeURL": testInvokeURL}},
		}),
	})
	api.put(nsItem("componentreleases", "rel-1"), kubernetes.Object{
		Spec: mustJSON(map[string]any{"workload": desiredWorkload(projectName(testAppID), componentName(testCompID), exposedSpec)}),
	})
	api.put(nsItem("projectreleasebindings", projectReleaseBindingName(testAppID)), kubernetes.Object{Status: mustJSON(map[string]any{"namespace": testCellNamespace})})
}

// deployedExposedWith is deployedExposed's happy path with one hook to
// alter the rendered route before observing.
func deployedExposedWith(t *testing.T, mutate func(*kubernetes.Object)) (reconcile.ComponentObservation, *fakeAPI) {
	t.Helper()
	api := newFakeAPI()
	putExposedDeployment(api)
	route := kubernetes.Object{
		Metadata: kubernetes.ObjectMeta{Name: "r1", Generation: 1, Labels: map[string]string{"openchoreo.dev/endpoint-name": "http"}},
		Status: mustJSON(map[string]any{"parents": []any{map[string]any{
			"parentRef": map[string]any{"name": testGateway.Name, "namespace": testGateway.Namespace},
			"conditions": []any{
				map[string]any{"type": "Accepted", "status": "True", "observedGeneration": 1},
				map[string]any{"type": "ResolvedRefs", "status": "True", "observedGeneration": 1},
			},
		}}}),
	}
	mutate(&route)
	api.lists = map[string][]kubernetes.Object{routesPath: {route}}
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	obs, err := p.EnsureComponent(context.Background(), testApp(), testComp(exposedSpec))
	if err != nil {
		t.Fatalf("EnsureComponent() = %v", err)
	}
	return obs, api
}
