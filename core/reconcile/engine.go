package reconcile

import (
	"context"
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
	workCh           chan graph.ResourceRef
	ready            chan struct{}
}

// NewEngine returns an Engine that bounds each Reconcile call to
// reconcileTimeout and resyncs every registered kind at least once per
// resyncEvery.
func NewEngine(bus event.Bus, resyncEvery, reconcileTimeout time.Duration) *Engine {
	return &Engine{
		bus:              bus,
		resyncEvery:      resyncEvery,
		reconcileTimeout: reconcileTimeout,
		regs:             make(map[resource.Kind]registration),
		workCh:           make(chan graph.ResourceRef, 256),
		ready:            make(chan struct{}),
	}
}

// Register wires a Reconciler for kind, with list used for periodic
// resync.
func (e *Engine) Register(kind resource.Kind, r Reconciler, list func(ctx context.Context) ([]graph.ResourceRef, error)) {
	e.regs[kind] = registration{reconciler: r, list: list}
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
		case e.workCh <- graph.ResourceRef{Kind: ev.Kind, ID: ev.ID}:
		default:
			// Queue full -- dropped. Safe: periodic resync re-lists and
			// re-enqueues everything anyway, so nothing is lost forever,
			// only delayed.
		}
	})
	defer unsubscribe()

	ticker := time.NewTicker(e.resyncEvery)
	defer ticker.Stop()

	close(e.ready)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ref := <-e.workCh:
			e.reconcileOne(ctx, ref)
		case <-ticker.C:
			e.resyncAll(ctx)
		}
	}
}

// resyncAll re-lists every registered kind and enqueues every ref the
// same way an event does -- one code path for both triggers, so a full
// queue during a resync burst degrades identically to an event burst.
func (e *Engine) resyncAll(ctx context.Context) {
	for kind, reg := range e.regs {
		refs, err := reg.list(ctx)
		if err != nil {
			log.Printf("reconcile: resync list %s: %v", kind, err)
			continue
		}
		for _, ref := range refs {
			select {
			case e.workCh <- ref:
			default:
			}
		}
	}
}

func (e *Engine) reconcileOne(ctx context.Context, ref graph.ResourceRef) {
	reg, ok := e.regs[ref.Kind]
	if !ok {
		return
	}
	reconcileCtx, cancel := context.WithTimeout(ctx, e.reconcileTimeout)
	defer cancel()
	reconcileCtx = audit.WithInterface(reconcileCtx, InterfaceName)
	reconcileCtx = audit.WithActor(reconcileCtx, ActorName)

	if err := reg.reconciler.Reconcile(reconcileCtx, ref); err != nil {
		log.Printf("reconcile: %s/%s: %v", ref.Kind, ref.ID, err)
	}
}
