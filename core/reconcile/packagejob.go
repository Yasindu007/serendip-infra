package reconcile

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// A package's install or removal can take minutes (pulling images,
// waiting for rollouts), far longer than one reconcile may run, and a
// reconcile's context is cancelled at its deadline, killing whatever the
// installer started half-way (specs/022-builds T013 found exactly that).
// So installs and removals run as background jobs, at most one per
// package at a time, since a package is platform-wide. Each reconcile, or
// clean-up retry, only looks at the job: it waits a moment, so a quick
// call (a resync's checks) still finishes in the same reconcile, and
// otherwise comes back later.

// packageJobTimeout bounds one install or removal.
const packageJobTimeout = 30 * time.Minute

// packageJobWait is how long a reconcile waits for a job before coming
// back later: well inside the engine's reconcile timeout.
const packageJobWait = 3 * time.Second

// ErrPackageBusy is a clean-up's answer while the package is still being
// installed or removed; the clean-up is retried.
var ErrPackageBusy = errors.New("the package is still being installed or removed")

type jobOp int

const (
	opInstall jobOp = iota
	opUninstall
)

type packageJob struct {
	op         jobOp
	instanceID string
	// spec is what an install job installs, so a result for an instance
	// edited since isn't taken as current.
	spec  resource.ProviderInstanceSpec
	done  chan struct{}
	state PackageState
	err   error
}

func (j *packageJob) finished() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}

// packageJobs tracks each package's current job, by provider name.
type packageJobs struct {
	mu   sync.Mutex
	jobs map[string]*packageJob
	// wait is packageJobWait; tests shorten it.
	wait time.Duration
}

func newPackageJobs() *packageJobs {
	return &packageJobs{jobs: map[string]*packageJob{}, wait: packageJobWait}
}

// run gives the outcome of op on instanceID for package p: the job's
// result once it has finished (consuming it), or done false while it's
// still running. A finished job for something else is dropped and a new
// one started; a running one for something else makes this call wait its
// turn (busy). The job's context keeps ctx's values (the audit identity)
// but not its deadline.
func (js *packageJobs) run(ctx context.Context, p string, op jobOp, instanceID string, spec resource.ProviderInstanceSpec,
	do func(context.Context) (PackageState, error),
) (j *packageJob, done, busy bool) {
	js.mu.Lock()
	j = js.jobs[p]
	switch {
	case j == nil:
	case !j.finished():
		mine := j.op == op && j.instanceID == instanceID && reflect.DeepEqual(j.spec, spec)
		js.mu.Unlock()
		if !mine {
			return nil, false, true
		}
		return js.await(p, j)
	case j.op == op && j.instanceID == instanceID && reflect.DeepEqual(j.spec, spec):
		delete(js.jobs, p)
		js.mu.Unlock()
		return j, true, false
	default:
		delete(js.jobs, p) // a stale result: nobody is waiting for it
	}
	job := &packageJob{op: op, instanceID: instanceID, spec: spec, done: make(chan struct{})}
	js.jobs[p] = job
	js.mu.Unlock()

	jobCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), packageJobTimeout)
	go func() {
		defer cancel()
		defer close(job.done)
		job.state, job.err = do(jobCtx)
	}()
	return js.await(p, job)
}

// await waits up to js.wait for j, consuming it if it finished.
func (js *packageJobs) await(p string, j *packageJob) (*packageJob, bool, bool) {
	select {
	case <-j.done:
	case <-time.After(js.wait):
		return nil, false, false
	}
	js.mu.Lock()
	if js.jobs[p] == j {
		delete(js.jobs, p)
	}
	js.mu.Unlock()
	return j, true, false
}
