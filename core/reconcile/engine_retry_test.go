package reconcile_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// flakyReconciler fails its first failures calls, then succeeds.
type flakyReconciler struct {
	mu       sync.Mutex
	failures int
	calls    int
}

func (f *flakyReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failures {
		return errors.New("provider unreachable")
	}
	return nil
}

func (f *flakyReconciler) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// A deleted resource's ref only ever arrives once, via its delete event,
// and never appears in a resync list again. If that one teardown fails,
// the engine must keep retrying it on resync until it succeeds -- then stop.
func TestEngine_FailedReconcileOfUnlistedRefIsRetriedUntilSuccess(t *testing.T) {
	bus := event.NewInProcessBus()
	flaky := &flakyReconciler{failures: 2}
	engine := reconcile.NewEngine(bus, 20*time.Millisecond, time.Second)
	engine.Register(resource.KindComponent, flaky, func(context.Context) ([]graph.ResourceRef, error) {
		return nil, nil // the record is gone: resync lists nothing
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go engine.Run(ctx)
	<-engine.Ready()

	bus.Publish(event.Event{Type: event.EventDeleted, Kind: resource.KindComponent, ID: "gone"})

	deadline := time.Now().Add(2 * time.Second)
	for flaky.callCount() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := flaky.callCount(); got < 3 {
		t.Fatalf("calls = %d, want the failed teardown retried on resync until it succeeds (>= 3)", got)
	}

	// Once it succeeds it must not be retried again.
	settled := flaky.callCount()
	time.Sleep(100 * time.Millisecond) // several resync intervals
	if got := flaky.callCount(); got != settled {
		t.Errorf("calls kept growing after success: %d -> %d", settled, got)
	}
}
