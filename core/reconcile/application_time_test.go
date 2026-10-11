package reconcile_test

import (
	"context"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestComponentReconciler_ReachableChangeKeepsReadyTransitionTime(t *testing.T) {
	s := newAppStores(t)
	app := createApp(t, s, resource.ConditionTrue)
	comp := createExposedComponent(t, s, app.Metadata.ID, true)
	p := &fakeAppPlatform{comp: ensureResult{ready: true, reason: reconcile.ReasonDeployed,
		reach: &reconcile.Reachability{Reason: reconcile.ReasonPending}}}
	r := reconcile.NewComponentReconciler(s.components, s.apps, p)
	ref := graph.ResourceRef{Kind: resource.KindComponent, ID: comp.Metadata.ID}
	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	first, _ := s.components.Get(context.Background(), comp.Metadata.ID)
	readyAt := findCond(first.Status.Conditions, "Ready").LastTransitionTime

	p.comp.reach = &reconcile.Reachability{Reachable: true, Reason: reconcile.ReasonServing, Endpoints: testEndpoints}
	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	second, _ := s.components.Get(context.Background(), comp.Metadata.ID)
	if got := findCond(second.Status.Conditions, "Ready").LastTransitionTime; !got.Equal(readyAt) {
		t.Errorf("Ready LastTransitionTime moved %v -> %v on a Reachable-only change", readyAt, got)
	}
	if c := findCond(second.Status.Conditions, "Reachable"); c == nil || c.Status != resource.ConditionTrue {
		t.Errorf("Reachable = %+v, want True", c)
	}
}
