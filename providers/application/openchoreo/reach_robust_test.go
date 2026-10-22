package openchoreo

import (
	"testing"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

func TestObserveReachability_StaleRouteConditionIsPending(t *testing.T) {
	reach, _ := deployedExposedWith(t, func(route *kubernetes.Object) { route.Metadata.Generation = 2 })
	if reach.Reachable || reach.Reason != reconcile.ReasonPending {
		t.Errorf("Reachable = %+v, want Pending for Accepted recorded at an older route generation", reach)
	}
}

func TestObserveReachability_ParentWithoutNamespaceDoesNotMatch(t *testing.T) {
	reach, _ := deployedExposedWith(t, func(route *kubernetes.Object) {
		route.Status = mustJSON(map[string]any{"parents": []any{map[string]any{
			"parentRef": map[string]any{"name": testGateway.Name},
			"conditions": []any{
				map[string]any{"type": "Accepted", "status": "True"},
				map[string]any{"type": "ResolvedRefs", "status": "True"},
			},
		}}})
	})
	if reach.Reachable {
		t.Errorf("Reachable = %+v, want not attached: an empty parentRef namespace means the route's own", reach)
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
func deployedExposedWith(t *testing.T, mutate func(*kubernetes.Object)) (reconcile.Reachability, *fakeAPI) {
	t.Helper()
	api := newFakeAPI().withComponentType()
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
	return observe(t, api), api
}
