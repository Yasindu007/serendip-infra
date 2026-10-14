package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
)

// Capability names, as registered in core/provider.Registry and reported by
// GET /v1/readiness.
const (
	capStorage     = "storage"
	capSecrets     = "secrets"
	capGateway     = "gateway"
	capAppPlatform = "application-platform"
)

// Provider-neutral readiness reasons.
const (
	readinessSettingUp    = "SettingUp"
	readinessSetupFailed  = "SetupFailed"
	readinessReady        = "Ready"
	readinessRepairFailed = "RepairFailed"
)

// backoff is a retry schedule: initial, doubling, capped at max, forever.
type backoff struct {
	initial, max time.Duration
}

// setupBackoff bounds how often a failing capability setup is retried
// (plan.md decision 5): one attempt's own waits (Helm --wait, OpenBao's
// readiness wait) bound the attempt, this bounds the gap between attempts.
var setupBackoff = backoff{initial: 5 * time.Second, max: 2 * time.Minute}

// tracker runs capability setup with retries and reports each capability's
// state to the registry's readiness. Safe for concurrent use: every setup
// goroutine and the foundations loop share one.
type tracker struct {
	registry *provider.Registry
	names    []string
	backoff  backoff

	allReady sync.Once
}

func newTracker(registry *provider.Registry, b backoff, names ...string) *tracker {
	t := &tracker{registry: registry, names: names, backoff: b}
	for _, n := range names {
		registry.SetReadiness(n, provider.Readiness{Reason: readinessSettingUp, Message: "setup has not started yet"})
	}
	return t
}

// run calls setup until it succeeds (true) or ctx ends (false), sleeping
// per the backoff between failures. Every failure is logged and reported
// as SetupFailed with the error, so GET /v1/readiness says why.
func (t *tracker) run(ctx context.Context, name string, setup func(context.Context) error) bool {
	t.registry.SetReadiness(name, provider.Readiness{Reason: readinessSettingUp, Message: "setting up"})
	delay := t.backoff.initial
	for attempt := 1; ; attempt++ {
		err := setup(ctx)
		if err == nil {
			t.ready(name)
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		log.Printf("serendipd: %s setup failed (attempt %d, retrying in %s): %v", name, attempt, delay, err)
		t.registry.SetReadiness(name, provider.Readiness{Reason: readinessSetupFailed,
			Message: fmt.Sprintf("attempt %d failed, retrying in %s: %v", attempt, delay, err)})
		select {
		case <-ctx.Done():
			return false
		case <-time.After(delay):
		}
		delay = min(delay*2, t.backoff.max)
	}
}

// waiting reports name as not started because it depends on another
// capability.
func (t *tracker) waiting(name, message string) {
	t.registry.SetReadiness(name, provider.Readiness{Reason: readinessSettingUp, Message: message})
}

// repaired records the outcome of a foundations re-ensure pass for name.
func (t *tracker) repaired(name string, err error) {
	if err == nil {
		t.ready(name)
		return
	}
	log.Printf("serendipd: %s re-ensure failed (retrying next pass): %v", name, err)
	t.registry.SetReadiness(name, provider.Readiness{Reason: readinessRepairFailed, Message: err.Error()})
}

func (t *tracker) ready(name string) {
	t.registry.SetReadiness(name, provider.Readiness{Ready: true, Reason: readinessReady})
	rd := t.registry.Readiness()
	for _, n := range t.names {
		if !rd[n].Ready {
			return
		}
	}
	// Logged once: the benchmark harness and scripts wait for this line.
	t.allReady.Do(func() { log.Print("serendipd: all capabilities ready") })
}

// setupPlan is the background work for the Kubernetes-facing capabilities:
// the setup step of each, plus the foundations re-ensure run on an interval
// once the gateway and application platform are up. Storage, secrets, and
// the gateway set up independently; the application platform needs the
// gateway's endpoint, so it starts only after the gateway succeeds.
type setupPlan struct {
	storage  func(context.Context) error
	secrets  func(context.Context) error
	gateway  func(context.Context) (reconcile.GatewayEndpoint, error)
	platform func(context.Context, reconcile.GatewayEndpoint) error

	// gatewayFoundations and platformFoundations re-ensure what setup
	// ensured once (plan.md decision 2); each returns the pass's error.
	gatewayFoundations  func(context.Context) error
	platformFoundations func(context.Context, reconcile.GatewayEndpoint) error
	reensureEvery       time.Duration
}

// start launches the plan's goroutines and returns at once. They all stop
// when ctx ends.
func (p setupPlan) start(ctx context.Context, t *tracker) {
	go t.run(ctx, capStorage, p.storage)
	go t.run(ctx, capSecrets, p.secrets)
	go func() {
		t.waiting(capAppPlatform, "waiting for the gateway capability")
		var ep reconcile.GatewayEndpoint
		if !t.run(ctx, capGateway, func(ctx context.Context) (err error) {
			ep, err = p.gateway(ctx)
			return err
		}) {
			return
		}
		// The gateway is repaired from here on, even while the platform's
		// setup is still retrying.
		var platformUp atomic.Bool
		go p.foundations(ctx, t, ep, &platformUp)
		if t.run(ctx, capAppPlatform, func(ctx context.Context) error { return p.platform(ctx, ep) }) {
			platformUp.Store(true)
		}
	}()
}

// foundations re-ensures the gateway foundations every reensureEvery until
// ctx ends, and the application platform's too once platformUp is set. A
// failed pass is logged and reported as not ready, never fatal; the next
// pass retries. Gateway first, since the platform's routes attach to it.
func (p setupPlan) foundations(ctx context.Context, t *tracker, ep reconcile.GatewayEndpoint, platformUp *atomic.Bool) {
	ticker := time.NewTicker(p.reensureEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		err := p.gatewayFoundations(ctx)
		if ctx.Err() != nil {
			return // shutting down: not a failed repair
		}
		t.repaired(capGateway, err)
		if !platformUp.Load() {
			continue
		}
		err = p.platformFoundations(ctx, ep)
		if ctx.Err() != nil {
			return
		}
		t.repaired(capAppPlatform, err)
	}
}
