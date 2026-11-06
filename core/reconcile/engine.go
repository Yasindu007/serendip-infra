package reconcile

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// Reconciler observes current state for ref, diffs it against desired
// state, applies any needed change, and verifies -- idempotent: a call
// with no drift must not mutate anything (constitution rule 3,
// core/reconcile/CLAUDE.md).
type Reconciler interface {
	Reconcile(ctx context.Context, ref graph.ResourceRef) error
}

// registration pairs a Reconciler with a lister used for periodic resync
// -- list returns every ref of that Reconciler's kind so the engine can
// re-check state that didn't change via a triggering mutation (e.g. a
// health check flipping from pass to fail with no store write involved).
type registration struct {
	reconciler Reconciler
	list       func(ctx context.Context) ([]graph.ResourceRef, error)
}

// Engine dispatches Reconcile calls to per-Kind Reconcilers, triggered by
// core/event.Bus activity and periodic resync. Single goroutine,
// sequential processing -- appropriate at edge-1 scale
// (core/reconcile/CLAUDE.md); do not add concurrency speculatively.
type Engine struct {
	bus              event.Bus
	resyncEvery      time.Duration
	reconcileTimeout time.Duration
	regs             map[resource.Kind]registration
	workCh           chan workItem
	ready            chan struct{}

	// failed holds refs whose last Reconcile returned an error. Resync
	// retries them alongside the listed refs, because a ref whose record
	// was deleted never appears in a list again -- without this, a
	// teardown that fails once (provider unreachable during a delete) would
	// orphan the external state forever. Only touched from Run's goroutine.
	failed map[graph.ResourceRef]struct{}

	// due, backoff and converging schedule early re-checks requested via
	// RequeueAfter: due is when ref is next reconciled, backoff the last
	// wait (requeueBase, doubling, capped at requeueStep), converging when
	// the current run of early re-checks started. Early re-checks stop once
	// a resync interval has passed since converging -- from then on the ref
	// just rides the normal resync. All three are cleared when a Reconcile
	// returns nil. Only touched from Run's goroutine.
	due         map[graph.ResourceRef]time.Time
	backoff     map[graph.ResourceRef]time.Duration
	converging  map[graph.ResourceRef]time.Time
	requeueBase time.Duration
	requeueStep time.Duration
}

// defaultRequeueBase is the first early re-check delay after a Reconciler
// returns RequeueAfter.
const defaultRequeueBase = 2 * time.Second

// defaultRequeueStep is the longest wait between two early re-checks. With
// the 2 s base and a 30 s resync, re-checks land about 2, 6, 14, 22 and
// 30 s after the first reconcile, then normal resync takes over. Without
// the step the last wait would be 16 s (re-checks at 2, 6, 14, 30 s), so
// anything ready between 14 s and 30 s, such as a workload whose image
// isn't cached yet, would be reported at 30 s (spec 014's benchmark
// re-run).
const defaultRequeueStep = 8 * time.Second

// NewEngine returns an Engine that bounds each Reconcile call to
// reconcileTimeout and resyncs every registered kind at least once per
// resyncEvery.
func NewEngine(bus event.Bus, resyncEvery, reconcileTimeout time.Duration) *Engine {
	return &Engine{
		bus:              bus,
		resyncEvery:      resyncEvery,
		reconcileTimeout: reconcileTimeout,
		regs:             make(map[resource.Kind]registration),
		workCh:           make(chan workItem, 256),
		ready:            make(chan struct{}),
		failed:           make(map[graph.ResourceRef]struct{}),
		due:              make(map[graph.ResourceRef]time.Time),
		backoff:          make(map[graph.ResourceRef]time.Duration),
		converging:       make(map[graph.ResourceRef]time.Time),
		requeueBase:      defaultRequeueBase,
		requeueStep:      defaultRequeueStep,
	}
}

// Register wires a Reconciler for kind, with list used for periodic
// resync.
func (e *Engine) Register(kind resource.Kind, r Reconciler, list func(ctx context.Context) ([]graph.ResourceRef, error)) {
	e.regs[kind] = registration{reconciler: r, list: list}
}

// Enqueue asks for ref to be reconciled soon, the way a store event does.
// It never blocks: with the queue full the request is dropped, and resync
// covers it. A Reconciler may call it for another resource whose state its
// own change affects (a finished Build's Component).
func (e *Engine) Enqueue(ref graph.ResourceRef) {
	select {
	case e.workCh <- workItem{ref: ref, trigger: triggerEvent}:
	default:
	}
}

// Ready returns a channel closed once Run has subscribed to the event
// bus -- an event published before that point would be missed (nothing
// is listening yet). Mainly useful for synchronizing tests; production
// callers don't need to wait on it, since periodic resync would
// eventually pick up anything published before subscription anyway.
func (e *Engine) Ready() <-chan struct{} {
	return e.ready
}

// Run subscribes to the event bus, starts the periodic resync ticker,
// and processes reconcile work sequentially until ctx is canceled. Call
// once per Engine.
func (e *Engine) Run(ctx context.Context) error {
	// event.InProcessBus's own doc comment: "handlers run on the
	// publishing goroutine... must be fast and non-blocking." This
	// handler only enqueues; the actual Reconcile call happens on the
	// loop below, never here.
	unsubscribe := e.bus.Subscribe("", func(ev event.Event) {
		if _, ok := e.regs[ev.Kind]; !ok {
			return
		}
		select {
		case e.workCh <- workItem{ref: graph.ResourceRef{Kind: ev.Kind, ID: ev.ID}, trigger: triggerEvent}:
		default:
			// Queue full -- dropped. Safe: periodic resync re-lists and
			// re-enqueues everything anyway, so nothing is lost forever,
			// only delayed.
		}
	})
	defer unsubscribe()

	ticker := time.NewTicker(e.resyncEvery)
	defer ticker.Stop()

	// One timer, re-armed each iteration for the earliest scheduled early
	// re-check; dueC stays nil (never ready) while nothing is scheduled.
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	close(e.ready)

	for {
		var dueC <-chan time.Time
		if next, ok := e.nextDue(); ok {
			timer.Reset(time.Until(next))
			dueC = timer.C
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case w := <-e.workCh:
			e.reconcileOne(ctx, w.ref, w.trigger)
		case <-ticker.C:
			e.resyncAll(ctx)
		case <-dueC:
			e.reconcileDue(ctx)
		}
		timer.Stop()
	}
}

// nextDue returns the earliest scheduled early re-check, if any.
func (e *Engine) nextDue() (time.Time, bool) {
	var next time.Time
	for _, at := range e.due {
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	return next, !next.IsZero()
}

// reconcileDue reconciles every ref whose early re-check is due. Done
// directly rather than via workCh so a full queue can't drop a schedule.
func (e *Engine) reconcileDue(ctx context.Context) {
	now := time.Now()
	var refs []graph.ResourceRef
	for ref, at := range e.due {
		if !at.After(now) {
			refs = append(refs, ref)
		}
	}
	for _, ref := range refs {
		if ctx.Err() != nil {
			return
		}
		delete(e.due, ref)
		e.reconcileOne(ctx, ref, triggerEarly)
	}
}

// resyncAll re-lists every registered kind and enqueues every ref the
// same way an event does -- one code path for both triggers, so a full
// queue during a resync burst degrades identically to an event burst.
func (e *Engine) resyncAll(ctx context.Context) {
	for ref := range e.failed {
		select {
		case e.workCh <- workItem{ref: ref, trigger: triggerResync}:
		default:
		}
	}
	for kind, reg := range e.regs {
		refs, err := reg.list(ctx)
		if err != nil {
			log.Printf("reconcile: resync list %s: %v", kind, err)
			continue
		}
		for _, ref := range refs {
			select {
			case e.workCh <- workItem{ref: ref, trigger: triggerResync}:
			default:
			}
		}
	}
}

// reconcileOne runs ref's Reconciler once; trigger says what caused the
// call, which decides how a RequeueAfter moves the backoff (scheduleEarly).
func (e *Engine) reconcileOne(ctx context.Context, ref graph.ResourceRef, trigger trigger) {
	reg, ok := e.regs[ref.Kind]
	if !ok {
		return
	}
	reconcileCtx, cancel := context.WithTimeout(ctx, e.reconcileTimeout)
	defer cancel()
	reconcileCtx = audit.WithInterface(reconcileCtx, InterfaceName)
	reconcileCtx = audit.WithActor(reconcileCtx, ActorName)

	err := reg.reconciler.Reconcile(reconcileCtx, ref)
	var requeue RequeueAfter
	switch {
	case err == nil:
		delete(e.failed, ref)
		delete(e.due, ref)
		delete(e.backoff, ref)
		delete(e.converging, ref)
	case errors.As(err, &requeue):
		delete(e.failed, ref)
		e.scheduleEarly(ref, requeue.Delay, trigger)
	default:
		delete(e.due, ref)
		delete(e.backoff, ref)
		delete(e.converging, ref)
		e.failed[ref] = struct{}{}
		log.Printf("reconcile: %s/%s: %v (will retry on next resync)", ref.Kind, ref.ID, err)
	}
}

// scheduleEarly arms ref's next early re-check at max(hint, backoff), with
// the backoff moved according to what triggered the reconcile:
//
//   - an early re-check doubles it, capped at requeueStep: a resource that
//     keeps converging without change is re-checked less often, but never
//     less than once per step;
//   - an event resets it to the base and restarts the converging window:
//     events come only from store writes, which reconcilers make only when
//     state changed (or a user edited the resource), so the resource is
//     moving and fast re-checks start over -- e.g. after a long outage left
//     it riding resync, the first sign of progress brings early re-checks
//     back. This relies on reconcilers not writing when nothing changed
//     (core/reconcile/CLAUDE.md): one that wrote on every pass would keep
//     restarting the window;
//   - resync leaves both alone.
//
// Event and resync reconciles only ever pull an existing schedule earlier,
// never push it later. Once a resync interval has passed since the ref
// started converging (or the hint alone reaches it) the ref stops being
// scheduled early: normal resync covers it, so a resource that never
// converges (and so never writes) costs only the re-checks of its first
// resync interval.
func (e *Engine) scheduleEarly(ref graph.ResourceRef, hint time.Duration, t trigger) {
	now := time.Now()
	b, ok := e.backoff[ref]
	switch {
	case !ok || t == triggerEvent:
		b = e.requeueBase
		e.converging[ref] = now
	case t == triggerEarly:
		b = min(b*2, e.requeueStep)
	}
	e.backoff[ref] = b
	if hint >= e.resyncEvery || now.Sub(e.converging[ref]) >= e.resyncEvery {
		if t == triggerEarly {
			delete(e.due, ref)
		}
		return
	}
	at := now.Add(max(b, hint))
	// An event during a long early backoff therefore brings the next
	// re-check forward to the base delay, by design.
	if existing, ok := e.due[ref]; ok && t != triggerEarly && existing.Before(at) {
		return
	}
	e.due[ref] = at
}

// trigger is what caused a reconcile.
type trigger int

const (
	triggerEvent trigger = iota
	triggerResync
	triggerEarly
)

// workItem is one queued reconcile request.
type workItem struct {
	ref     graph.ResourceRef
	trigger trigger
}
