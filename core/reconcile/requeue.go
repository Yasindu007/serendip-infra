package reconcile

import (
	"errors"
	"fmt"
	"time"
)

// RequeueAfter is returned by a Reconciler whose resource is still
// converging (work in flight upstream that no store event will announce)
// to ask the Engine for an early re-check. It is not a failure: the ref is
// not added to the failed-retry set and nothing is logged as an error.
//
// Delay is a minimum hint. The Engine applies whichever is larger, the
// hint or its own per-ref backoff (requeueBase doubling up to requeueStep,
// for at most one resync interval), so a resource that never converges
// gets a few early re-checks and then only normal resync.
type RequeueAfter struct {
	Delay time.Duration
}

func (r RequeueAfter) Error() string {
	return fmt.Sprintf("reconcile: requeue after %s", r.Delay)
}

// ErrProviderNotReady is returned by a capability provider handle whose
// provider hasn't finished setting up yet (the composition root keeps
// retrying setup in the background). Reconcilers report it as an honest
// "not ready yet" Condition and requeue, rather than as a failure.
var ErrProviderNotReady = errors.New("reconcile: capability provider not ready")

// ReasonProviderNotReady is the Condition reason every reconciler sets
// when its capability's provider returned ErrProviderNotReady.
const ReasonProviderNotReady = "ProviderNotReady"

// providerNotReadyMessage is the Condition message for
// ReasonProviderNotReady, naming the capability, never a product.
func providerNotReadyMessage(capability string) string {
	return "the " + capability + " capability is still being set up"
}

// requeueWhen turns a converging outcome into a RequeueAfter (letting the
// Engine pick the delay), unless writing status failed -- that error wins
// and goes through the Engine's normal failed-retry path.
func requeueWhen(err error, converging bool) error {
	if err != nil {
		return err
	}
	if converging {
		return RequeueAfter{}
	}
	return nil
}
