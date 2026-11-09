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

// SetBuildClock sets the build reconciler's clock.
func SetBuildClock(r *BuildReconciler, now func() time.Time) { r.now = now }

// SetComponentClock sets the Component reconciler's clock, which paces
// auto-build's polls.
func SetComponentClock(r *ComponentReconciler, now func() time.Time) { r.now = now }

// SetPackageJobWait sets how long a reconcile waits for a package's
// install or removal before coming back later.
func SetPackageJobWait(r *ProviderInstanceReconciler, d time.Duration) { r.jobs.wait = d }
