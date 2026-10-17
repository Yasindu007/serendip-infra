package reconcile

import "time"

// SetRequeueBase shortens the first early re-check delay so engine tests
// run in milliseconds. Must be called before Run.
func SetRequeueBase(e *Engine, d time.Duration) {
	e.requeueBase = d
}

// SetRequeueStep sets the longest wait between early re-checks. Must be
// called before Run.
func SetRequeueStep(e *Engine, d time.Duration) {
	e.requeueStep = d
}
