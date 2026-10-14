package reconcile_test

import (
	"context"
	"errors"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

func createExposedComponent(t *testing.T, s appStores, appID string, expose bool) resource.Component {
	t.Helper()
	comp, err := s.components.Create(context.Background(), resource.Component{
		Metadata: resource.Metadata{Name: "web", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: appID}}},
		Spec: resource.ComponentSpec{Image: "nginx", Ports: []resource.ComponentPort{
			{Name: "http", Port: 80, Expose: expose},
			{Name: "metrics", Port: 9113},
		}},
	})
	if err != nil {
		t.Fatalf("Create(component) = %v", err)
	}
	return comp
}

var testEndpoints = []resource.ComponentEndpointStatus{{Name: "http", URL: "http://example.localhost:30080/web-http"}}

func TestComponentReconciler_Reachability(t *testing.T) {
	tests := []struct {
		name          string
		expose        bool
		appReady      resource.ConditionStatus
		result        ensureResult
		wantReachable *resource.ConditionStatus // nil: no Reachable condition
		wantReason    string
		wantEndpoints int
		wantRequeue   bool
	}{
		{name: "not exposed: no Reachable condition, no endpoints", appReady: resource.ConditionTrue,
			result: ensureResult{ready: true, reason: reconcile.ReasonDeployed}},
		{name: "exposed and serving", expose: true, appReady: resource.ConditionTrue,
			result: ensureResult{ready: true, reason: reconcile.ReasonDeployed,
				reach: &reconcile.Reachability{Reachable: true, Reason: reconcile.ReasonServing, Endpoints: testEndpoints}},
			wantReachable: ptr(resource.ConditionTrue), wantReason: reconcile.ReasonServing, wantEndpoints: 1},
		{name: "exposed and pending publishes no endpoints", expose: true, appReady: resource.ConditionTrue,
			result: ensureResult{reason: reconcile.ReasonProgressing,
				reach: &reconcile.Reachability{Reason: reconcile.ReasonPending, Endpoints: testEndpoints}},
			wantReachable: ptr(resource.ConditionFalse), wantReason: reconcile.ReasonPending, wantRequeue: true},
		{name: "deployed but route not serving yet: requeue", expose: true, appReady: resource.ConditionTrue,
			result: ensureResult{ready: true, reason: reconcile.ReasonDeployed,
				reach: &reconcile.Reachability{Reason: reconcile.ReasonPending}},
			wantReachable: ptr(resource.ConditionFalse), wantReason: reconcile.ReasonPending, wantRequeue: true},
		{name: "route rejected", expose: true, appReady: resource.ConditionTrue,
			result: ensureResult{ready: true, reason: reconcile.ReasonDeployed,
				reach: &reconcile.Reachability{Reason: reconcile.ReasonRouteRejected, Message: "listener not found"}},
			wantReachable: ptr(resource.ConditionFalse), wantReason: reconcile.ReasonRouteRejected},
		{name: "exposed but application not ready: pending without provider call", expose: true, appReady: resource.ConditionFalse,
			wantReachable: ptr(resource.ConditionFalse), wantReason: reconcile.ReasonPending, wantRequeue: true},
		{name: "exposed and provider error: pending", expose: true, appReady: resource.ConditionTrue,
			result:        ensureResult{err: errors.New("timeout")},
			wantReachable: ptr(resource.ConditionFalse), wantReason: reconcile.ReasonPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newAppStores(t)
			app := createApp(t, s, tt.appReady)
			comp := createExposedComponent(t, s, app.Metadata.ID, tt.expose)
			r := reconcile.NewComponentReconciler(s.components, s.apps, &fakeAppPlatform{comp: tt.result})

			checkRequeue(t, r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindComponent, ID: comp.Metadata.ID}), tt.wantRequeue)
			got, _ := s.components.Get(context.Background(), comp.Metadata.ID)
			c := findCond(got.Status.Conditions, "Reachable")
			switch {
			case tt.wantReachable == nil && c != nil:
				t.Errorf("Reachable = %+v, want no Reachable condition", *c)
			case tt.wantReachable != nil && c == nil:
				t.Errorf("no Reachable condition, want {%s, %s}", *tt.wantReachable, tt.wantReason)
			case tt.wantReachable != nil && (c.Status != *tt.wantReachable || c.Reason != tt.wantReason):
				t.Errorf("Reachable = {%s, %s}, want {%s, %s}", c.Status, c.Reason, *tt.wantReachable, tt.wantReason)
			}
			if len(got.Status.Endpoints) != tt.wantEndpoints {
				t.Errorf("Endpoints = %+v, want %d", got.Status.Endpoints, tt.wantEndpoints)
			}
		})
	}
}

func TestComponentReconciler_UnexposingRemovesReachableAndEndpoints(t *testing.T) {
	s := newAppStores(t)
	app := createApp(t, s, resource.ConditionTrue)
	comp := createExposedComponent(t, s, app.Metadata.ID, true)
	p := &fakeAppPlatform{comp: ensureResult{ready: true, reason: reconcile.ReasonDeployed,
		reach: &reconcile.Reachability{Reachable: true, Reason: reconcile.ReasonServing, Endpoints: testEndpoints}}}
	r := reconcile.NewComponentReconciler(s.components, s.apps, p)
	ref := graph.ResourceRef{Kind: resource.KindComponent, ID: comp.Metadata.ID}
	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatal(err)
	}

	p.comp.reach = nil // the provider observes nothing exposed anymore
	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	got, _ := s.components.Get(context.Background(), comp.Metadata.ID)
	if c := findCond(got.Status.Conditions, "Reachable"); c != nil {
		t.Errorf("Reachable still present: %+v", *c)
	}
	if len(got.Status.Endpoints) != 0 {
		t.Errorf("Endpoints = %+v, want cleared", got.Status.Endpoints)
	}
	if c := findCond(got.Status.Conditions, "Ready"); c == nil || c.Reason != reconcile.ReasonDeployed {
		t.Errorf("Ready = %+v, want untouched Deployed", c)
	}
}

func TestComponentReconciler_ReachabilityIdempotency(t *testing.T) {
	s := newAppStores(t)
	app := createApp(t, s, resource.ConditionTrue)
	comp := createExposedComponent(t, s, app.Metadata.ID, true)
	p := &fakeAppPlatform{comp: ensureResult{ready: true, reason: reconcile.ReasonDeployed,
		reach: &reconcile.Reachability{Reachable: true, Reason: reconcile.ReasonServing, Endpoints: testEndpoints}}}
	r := reconcile.NewComponentReconciler(s.components, s.apps, p)
	ref := graph.ResourceRef{Kind: resource.KindComponent, ID: comp.Metadata.ID}
	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	first, _ := s.components.Get(context.Background(), comp.Metadata.ID)
	rows := countAuditRowsDB(t, s.db, comp.Metadata.ID)

	p.comp.reach = &reconcile.Reachability{Reachable: true, Reason: reconcile.ReasonServing, Message: "different upstream text", Endpoints: testEndpoints}
	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	second, _ := s.components.Get(context.Background(), comp.Metadata.ID)
	if second.Metadata.ResourceVersion != first.Metadata.ResourceVersion || countAuditRowsDB(t, s.db, comp.Metadata.ID) != rows {
		t.Errorf("no-drift reconcile wrote: RV %d -> %d", first.Metadata.ResourceVersion, second.Metadata.ResourceVersion)
	}

	p.comp.reach = &reconcile.Reachability{Reachable: true, Reason: reconcile.ReasonServing,
		Endpoints: []resource.ComponentEndpointStatus{{Name: "http", URL: "http://other.localhost:30080/web-http"}}}
	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	third, _ := s.components.Get(context.Background(), comp.Metadata.ID)
	if third.Metadata.ResourceVersion != second.Metadata.ResourceVersion+1 {
		t.Errorf("changed URL: RV %d -> %d, want exactly one write", second.Metadata.ResourceVersion, third.Metadata.ResourceVersion)
	}
	if len(third.Status.Endpoints) != 1 || third.Status.Endpoints[0].URL != "http://other.localhost:30080/web-http" {
		t.Errorf("Endpoints = %+v, want updated URL", third.Status.Endpoints)
	}
}

func findCond(conds []resource.Condition, typ string) *resource.Condition {
	for i := range conds {
		if conds[i].Type == typ {
			return &conds[i]
		}
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

func TestComponentReconciler_ReachableWithoutReadyIsDowngraded(t *testing.T) {
	s := newAppStores(t)
	app := createApp(t, s, resource.ConditionTrue)
	comp := createExposedComponent(t, s, app.Metadata.ID, true)
	p := &fakeAppPlatform{comp: ensureResult{reason: reconcile.ReasonProgressing,
		reach: &reconcile.Reachability{Reachable: true, Reason: reconcile.ReasonServing, Endpoints: testEndpoints}}}
	r := reconcile.NewComponentReconciler(s.components, s.apps, p)
	checkRequeue(t, r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindComponent, ID: comp.Metadata.ID}), true)
	got, _ := s.components.Get(context.Background(), comp.Metadata.ID)
	if c := findCond(got.Status.Conditions, "Reachable"); c == nil || c.Status != resource.ConditionFalse || c.Reason != reconcile.ReasonPending {
		t.Errorf("Reachable = %+v, want False/Pending while the workload isn't Ready", c)
	}
	if len(got.Status.Endpoints) != 0 {
		t.Errorf("Endpoints = %+v, want none", got.Status.Endpoints)
	}
}
