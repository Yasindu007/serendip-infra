package reconcile_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// fakeReconciler records every call it receives, safe for concurrent use
// since Engine.Run's event handler and its own processing loop run on
// different goroutines relative to the test's assertions.
type fakeReconciler struct {
	mu    sync.Mutex
	calls []graph.ResourceRef
	ctxs  []context.Context
}

func (f *fakeReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ref)
	f.ctxs = append(f.ctxs, ctx)
	return nil
}

func (f *fakeReconciler) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func waitForCallCount(t *testing.T, f *fakeReconciler, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.callCount() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("callCount() = %d after timeout, want >= %d", f.callCount(), want)
}

func TestEngine_EventTriggered_DispatchesMatchingKindOnly(t *testing.T) {
	bus := event.NewInProcessBus()
	fake := &fakeReconciler{}
	engine := reconcile.NewEngine(bus, time.Hour, time.Second) // resyncEvery large: isolate event-triggering
	engine.Register(resource.KindCluster, fake, nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()
	<-engine.Ready()

	// Matching kind: must be dispatched.
	bus.Publish(event.Event{Type: event.EventCreated, Kind: resource.KindCluster, ID: "cluster-1"})
	waitForCallCount(t, fake, 1)

	// Non-matching kind: must not be dispatched.
	bus.Publish(event.Event{Type: event.EventCreated, Kind: resource.KindOrganization, ID: "org-1"})
	time.Sleep(50 * time.Millisecond) // give a wrongly-dispatched call a chance to land
	if got := fake.callCount(); got != 1 {
		t.Errorf("callCount() after non-matching-kind event = %d, want still 1", got)
	}

	if fake.calls[0] != (graph.ResourceRef{Kind: resource.KindCluster, ID: "cluster-1"}) {
		t.Errorf("calls[0] = %+v, want {Kind:Cluster ID:cluster-1}", fake.calls[0])
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Run() returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after ctx cancellation")
	}
}

// This is T004's Verify line: a Reconcile call's context carries both
// audit identity constants, set by the Engine, not by the Reconciler.
func TestEngine_ReconcileContext_CarriesAuditIdentity(t *testing.T) {
	bus := event.NewInProcessBus()
	fake := &fakeReconciler{}
	engine := reconcile.NewEngine(bus, time.Hour, time.Second)
	engine.Register(resource.KindCluster, fake, nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go engine.Run(ctx)
	<-engine.Ready()

	bus.Publish(event.Event{Type: event.EventCreated, Kind: resource.KindCluster, ID: "cluster-1"})
	waitForCallCount(t, fake, 1)

	fake.mu.Lock()
	gotCtx := fake.ctxs[0]
	fake.mu.Unlock()

	if got := audit.InterfaceFrom(gotCtx); got != reconcile.InterfaceName {
		t.Errorf("InterfaceFrom(reconcile ctx) = %q, want %q", got, reconcile.InterfaceName)
	}
	if got := audit.ActorFrom(gotCtx); got != reconcile.ActorName {
		t.Errorf("ActorFrom(reconcile ctx) = %q, want %q", got, reconcile.ActorName)
	}
}

func TestEngine_Run_ReturnsOnContextCancellation(t *testing.T) {
	bus := event.NewInProcessBus()
	engine := reconcile.NewEngine(bus, time.Hour, time.Second)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Run() = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after ctx cancellation")
	}
}
