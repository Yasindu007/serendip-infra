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

// scriptedReconciler returns results[i] on its i-th call (nil once the
// script runs out) and records when each call happened.
type scriptedReconciler struct {
	mu      sync.Mutex
	results []error
	times   []time.Time
}

func (s *scriptedReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.times = append(s.times, time.Now())
	if i := len(s.times) - 1; i < len(s.results) {
		return s.results[i]
	}
	return nil
}

func (s *scriptedReconciler) callTimes() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.times...)
}

func waitCalls(t *testing.T, s *scriptedReconciler, want int) []time.Time {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.callTimes(); len(got) >= want {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("calls = %d after timeout, want >= %d", len(s.callTimes()), want)
	return nil
}

func startEngine(t *testing.T, r reconcile.Reconciler, resync, base time.Duration, list func(context.Context) ([]graph.ResourceRef, error)) *event.InProcessBus {
	t.Helper()
	bus := event.NewInProcessBus()
	engine := reconcile.NewEngine(bus, resync, time.Second)
	reconcile.SetRequeueBase(engine, base)
	engine.Register(resource.KindComponent, r, list)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go engine.Run(ctx)
	<-engine.Ready()
	return bus
}

func publish(bus *event.InProcessBus) {
	bus.Publish(event.Event{Type: event.EventUpdated, Kind: resource.KindComponent, ID: "c1"})
}

// A converging resource is re-checked early with doubling backoff, and
// stops being re-checked once a Reconcile returns nil.
func TestEngine_RequeueAfter_ReChecksWithBackoffUntilSettled(t *testing.T) {
	s := &scriptedReconciler{results: []error{
		reconcile.RequeueAfter{}, reconcile.RequeueAfter{}, nil,
	}}
	// Resync effectively off, so every re-check seen here is an early one.
	bus := startEngine(t, s, time.Hour, 40*time.Millisecond, nil)
	publish(bus)

	times := waitCalls(t, s, 3)
	first, second := times[1].Sub(times[0]), times[2].Sub(times[1])
	if first < 40*time.Millisecond || second < 80*time.Millisecond {
		t.Errorf("gaps = %v, %v; want >= 40ms then >= 80ms (doubling backoff)", first, second)
	}
	if second < first {
		t.Errorf("backoff did not grow: %v then %v", first, second)
	}

	time.Sleep(300 * time.Millisecond)
	if got := len(s.callTimes()); got != 3 {
		t.Errorf("calls = %d after settling, want 3 (no more early re-checks)", got)
	}
}

// The reconciler's Delay is a minimum: the engine waits at least that long
// even when its own backoff is shorter.
func TestEngine_RequeueAfter_HonorsLargerHint(t *testing.T) {
	s := &scriptedReconciler{results: []error{reconcile.RequeueAfter{Delay: 150 * time.Millisecond}}}
	bus := startEngine(t, s, time.Hour, 10*time.Millisecond, nil)
	publish(bus)

	times := waitCalls(t, s, 2)
	if gap := times[1].Sub(times[0]); gap < 150*time.Millisecond {
		t.Errorf("gap = %v, want >= the 150ms hint", gap)
	}
}

// Early re-checks stop once backoff reaches the resync interval; a
// never-converging resource then costs only normal resync.
func TestEngine_RequeueAfter_BackoffCapsAtResync(t *testing.T) {
	always := make([]error, 1000)
	for i := range always {
		always[i] = reconcile.RequeueAfter{}
	}
	s := &scriptedReconciler{results: always}
	// base 20ms, resync 100ms: early checks at +20, +40, +80, then capped.
	bus := startEngine(t, s, 100*time.Millisecond, 20*time.Millisecond, func(context.Context) ([]graph.ResourceRef, error) {
		return nil, nil // unlisted: once capped, nothing re-checks it
	})
	publish(bus)

	waitCalls(t, s, 4)
	time.Sleep(400 * time.Millisecond)
	if got := len(s.callTimes()); got != 4 {
		t.Errorf("calls = %d, want 4 (initial + 3 early re-checks, then nothing)", got)
	}
}

// RequeueAfter is not a failure: an unlisted ref that asked for a requeue
// and then settles is not retried by the failed-retry path.
func TestEngine_RequeueAfter_NeverEntersFailedSet(t *testing.T) {
	s := &scriptedReconciler{results: []error{reconcile.RequeueAfter{Delay: time.Hour}}}
	// Hint beyond resync: no early re-check is scheduled at all. If the ref
	// were in the failed set, resync (every 20ms) would retry it.
	bus := startEngine(t, s, 20*time.Millisecond, 10*time.Millisecond, func(context.Context) ([]graph.ResourceRef, error) {
		return nil, nil
	})
	publish(bus)

	waitCalls(t, s, 1)
	time.Sleep(150 * time.Millisecond)
	if got := len(s.callTimes()); got != 1 {
		t.Errorf("calls = %d, want 1 (RequeueAfter must not enter the failed-retry set)", got)
	}
}

// A plain error still goes through the failed-retry path at resync, not
// the early re-check schedule.
func TestEngine_PlainErrorStillRetriesAtResync(t *testing.T) {
	s := &scriptedReconciler{results: []error{errors.New("boom")}}
	bus := startEngine(t, s, 120*time.Millisecond, 5*time.Millisecond, func(context.Context) ([]graph.ResourceRef, error) {
		return nil, nil
	})
	publish(bus)

	times := waitCalls(t, s, 2)
	if gap := times[1].Sub(times[0]); gap < 60*time.Millisecond {
		t.Errorf("retry gap = %v, want a resync-paced retry, not an early re-check", gap)
	}
}

// Event-triggered reconciles (e.g. the update event of a reconciler's own
// status write) must not advance the backoff: a burst of events on a
// converging ref still gets its first early re-check one base delay later,
// not after the burst has doubled the schedule out toward the cap.
func TestEngine_RequeueAfter_EventsDoNotAdvanceBackoff(t *testing.T) {
	always := make([]error, 1000)
	for i := range always {
		always[i] = reconcile.RequeueAfter{}
	}
	s := &scriptedReconciler{results: always}
	bus := startEngine(t, s, time.Hour, 60*time.Millisecond, nil)
	start := time.Now()
	for range 5 {
		publish(bus) // five event-triggered reconciles, each returning RequeueAfter
	}
	waitCalls(t, s, 6)
	times := s.callTimes()
	// The sixth call is the first early re-check; with doubling per event it
	// would come ~60ms * 2^4 = 960ms after the burst instead.
	if gap := times[5].Sub(start); gap > 400*time.Millisecond {
		t.Errorf("first early re-check came %v after the event burst, want ~one base delay (60ms)", gap)
	}
}

// A ref whose backoff reached the cap gets fast re-checks again after an
// event (a store change means it is moving again, e.g. after an outage).
func TestEngine_RequeueAfter_EventResetsCappedBackoff(t *testing.T) {
	always := make([]error, 1000)
	for i := range always {
		always[i] = reconcile.RequeueAfter{}
	}
	s := &scriptedReconciler{results: always}
	// base 20ms, resync 100ms: early checks at +20, +40, +80, then capped.
	bus := startEngine(t, s, 100*time.Millisecond, 20*time.Millisecond, func(context.Context) ([]graph.ResourceRef, error) {
		return nil, nil
	})
	publish(bus)
	waitCalls(t, s, 4)
	time.Sleep(300 * time.Millisecond)
	if got := len(s.callTimes()); got != 4 {
		t.Fatalf("calls = %d before the second event, want 4 (capped)", got)
	}

	publish(bus)
	times := waitCalls(t, s, 6)
	// Without the reset the ref stays capped and unlisted, so it would never be
	// re-checked at all; the bound only needs to be finite and forgiving.
	if gap := times[5].Sub(times[4]); gap > 250*time.Millisecond {
		t.Errorf("early re-check after the event came %v later, want ~one base delay (20ms)", gap)
	}
}
