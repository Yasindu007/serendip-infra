package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

type fakeStorage struct{ ensured int }

func (f *fakeStorage) Ensure(context.Context, resource.Volume) (bool, string, string, error) {
	f.ensured++
	return true, "Bound", "", nil
}
func (f *fakeStorage) Delete(context.Context, resource.Volume) error { return nil }

func TestLazy_NotReadyUntilSetThenDelegates(t *testing.T) {
	l := &lazyStorage{}
	if _, _, _, err := l.Ensure(context.Background(), resource.Volume{}); !errors.Is(err, reconcile.ErrProviderNotReady) {
		t.Fatalf("Ensure before set = %v, want ErrProviderNotReady", err)
	}
	if err := l.Delete(context.Background(), resource.Volume{}); !errors.Is(err, reconcile.ErrProviderNotReady) {
		t.Fatalf("Delete before set = %v, want ErrProviderNotReady", err)
	}
	f := &fakeStorage{}
	l.set(f)
	if bound, _, _, err := l.Ensure(context.Background(), resource.Volume{}); err != nil || !bound || f.ensured != 1 {
		t.Errorf("Ensure after set = (%v, %v), calls %d; want delegated", bound, err, f.ensured)
	}
}

func TestLazy_AllWrappersReportNotReady(t *testing.T) {
	ctx := context.Background()
	s, p := &lazySecrets{}, &lazyPlatform{}
	for name, err := range map[string]error{
		"Exists":            func() error { _, err := s.Exists(ctx, resource.Secret{}); return err }(),
		"WriteValue":        s.WriteValue(ctx, resource.Secret{}, nil),
		"ReadValue":         func() error { _, err := s.ReadValue(ctx, resource.Secret{}); return err }(),
		"Secrets.Delete":    s.Delete(ctx, resource.Secret{}),
		"EnsureApplication": func() error { _, _, _, err := p.EnsureApplication(ctx, resource.Application{}); return err }(),
		"DeleteApplication": p.DeleteApplication(ctx, resource.Application{}),
		"EnsureComponent": func() error {
			_, err := p.EnsureComponent(ctx, resource.Application{}, resource.Component{})
			return err
		}(),
		"DeleteComponent": p.DeleteComponent(ctx, resource.Component{}),
	} {
		if !errors.Is(err, reconcile.ErrProviderNotReady) {
			t.Errorf("%s = %v, want ErrProviderNotReady", name, err)
		}
	}
}

// Failed setup is retried with doubling gaps, reported as SetupFailed in
// the meantime, and Ready once it succeeds.
func TestTracker_RetriesWithBackoffAndReportsReadiness(t *testing.T) {
	reg := provider.NewRegistry()
	tr := newTracker(reg, backoff{initial: 20 * time.Millisecond, max: 40 * time.Millisecond}, "storage")
	var times []time.Time
	ok := tr.run(context.Background(), "storage", func(context.Context) error {
		times = append(times, time.Now())
		if len(times) < 4 {
			if len(times) > 1 {
				if rd := reg.Readiness()["storage"]; rd.Ready || rd.Reason != readinessSetupFailed {
					t.Errorf("readiness during retry = %+v, want SetupFailed", rd)
				}
			}
			return errors.New("cluster unreachable")
		}
		return nil
	})
	if !ok || len(times) != 4 {
		t.Fatalf("run = %v after %d attempts, want success on attempt 4", ok, len(times))
	}
	gaps := []time.Duration{times[1].Sub(times[0]), times[2].Sub(times[1]), times[3].Sub(times[2])}
	if gaps[0] < 20*time.Millisecond || gaps[1] < 40*time.Millisecond || gaps[2] < 40*time.Millisecond || gaps[2] > 200*time.Millisecond {
		t.Errorf("gaps = %v, want ~20ms, ~40ms, then capped at ~40ms", gaps)
	}
	if rd := reg.Readiness()["storage"]; !rd.Ready || rd.Reason != readinessReady {
		t.Errorf("readiness = %+v, want Ready", rd)
	}
}

func TestTracker_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tr := newTracker(provider.NewRegistry(), backoff{initial: time.Hour, max: time.Hour}, "storage")
	done := make(chan bool)
	go func() {
		done <- tr.run(ctx, "storage", func(context.Context) error { return errors.New("down") })
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Error("run = true after cancel, want false")
		}
	case <-time.After(time.Second):
		t.Fatal("run did not return after cancel")
	}
}

// The application platform starts only once the gateway has succeeded, with
// the gateway's endpoint; then the foundations loop re-ensures both, and a
// failed pass flips readiness to RepairFailed until the next good pass.
func TestSetupPlan_OrderingAndFoundations(t *testing.T) {
	reg := provider.NewRegistry()
	tr := newTracker(reg, backoff{initial: 5 * time.Millisecond, max: 5 * time.Millisecond},
		capStorage, capSecrets, capGateway, capAppPlatform)
	ep := reconcile.GatewayEndpoint{Name: "gw", Namespace: "ns", Host: "localhost", Port: 30080}

	var mu sync.Mutex
	var gatewayDone bool
	var gatewayAttempts int
	var platformEP reconcile.GatewayEndpoint
	var platformBeforeGateway bool
	var repairPasses atomic.Int32
	failRepair := atomic.Bool{}
	failRepair.Store(true)
	platformReady := make(chan struct{})
	var platformOnce sync.Once

	plan := setupPlan{
		storage: func(context.Context) error { return nil },
		secrets: func(context.Context) error { return nil },
		gateway: func(context.Context) (reconcile.GatewayEndpoint, error) {
			mu.Lock()
			defer mu.Unlock()
			gatewayAttempts++
			if gatewayAttempts < 3 {
				if rd := reg.Readiness()[capAppPlatform]; rd.Ready {
					t.Error("application platform ready before the gateway")
				}
				return reconcile.GatewayEndpoint{}, errors.New("gateway not programmed")
			}
			gatewayDone = true
			return ep, nil
		},
		platform: func(_ context.Context, got reconcile.GatewayEndpoint) error {
			mu.Lock()
			defer mu.Unlock()
			platformBeforeGateway = !gatewayDone
			platformEP = got
			platformOnce.Do(func() { close(platformReady) })
			return nil
		},
		gatewayFoundations: func(context.Context) error { return nil },
		platformFoundations: func(_ context.Context, got reconcile.GatewayEndpoint) error {
			repairPasses.Add(1)
			if got != ep {
				t.Errorf("foundations endpoint = %+v, want %+v", got, ep)
			}
			if failRepair.Load() {
				return errors.New("environment deleted and could not be recreated")
			}
			return nil
		},
		reensureEvery: 10 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	plan.start(ctx, tr)

	select {
	case <-platformReady:
	case <-time.After(2 * time.Second):
		t.Fatal("application platform setup never ran")
	}
	mu.Lock()
	if platformBeforeGateway || platformEP != ep {
		t.Errorf("platform ran before gateway = %v, with endpoint %+v", platformBeforeGateway, platformEP)
	}
	mu.Unlock()

	waitFor(t, func() bool { return reg.Readiness()[capAppPlatform].Reason == readinessRepairFailed })
	if reg.Readiness()[capAppPlatform].Ready {
		t.Error("RepairFailed capability still reported ready")
	}
	failRepair.Store(false)
	waitFor(t, func() bool { return reg.Readiness()[capAppPlatform].Ready })
	for _, name := range []string{capStorage, capSecrets, capGateway, capAppPlatform} {
		if !reg.Readiness()[name].Ready {
			t.Errorf("%s not ready: %+v", name, reg.Readiness()[name])
		}
	}
	if repairPasses.Load() < 2 {
		t.Errorf("foundations passes = %d, want repeated passes", repairPasses.Load())
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met before timeout")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestParseReensureInterval(t *testing.T) {
	tests := []struct {
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{"", 5 * time.Minute, false},
		{"30s", 30 * time.Second, false},
		{"10m", 10 * time.Minute, false},
		{"0s", 0, true},
		{"-1m", 0, true},
		{"five", 0, true},
	}
	for _, tt := range tests {
		got, err := parseReensureInterval(tt.raw)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseReensureInterval(%q) = (%v, %v), want (%v, err=%v)", tt.raw, got, err, tt.want, tt.wantErr)
		}
	}
}

// The gateway keeps being repaired while the application platform's setup
// is still failing; the platform's own foundations wait for its setup.
func TestSetupPlan_GatewayRepairedWhilePlatformSetupRetries(t *testing.T) {
	reg := provider.NewRegistry()
	tr := newTracker(reg, backoff{initial: 5 * time.Millisecond, max: 5 * time.Millisecond}, capGateway, capAppPlatform)
	var gatewayPasses, platformPasses atomic.Int32
	plan := setupPlan{
		storage: func(context.Context) error { return nil },
		secrets: func(context.Context) error { return nil },
		gateway: func(context.Context) (reconcile.GatewayEndpoint, error) {
			return reconcile.GatewayEndpoint{Name: "gw"}, nil
		},
		platform: func(context.Context, reconcile.GatewayEndpoint) error {
			return errors.New("charts not installable yet")
		},
		gatewayFoundations: func(context.Context) error {
			gatewayPasses.Add(1)
			return nil
		},
		platformFoundations: func(context.Context, reconcile.GatewayEndpoint) error {
			platformPasses.Add(1)
			return nil
		},
		reensureEvery: 5 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	plan.start(ctx, tr)
	waitFor(t, func() bool { return gatewayPasses.Load() >= 3 })
	if n := platformPasses.Load(); n != 0 {
		t.Errorf("platform foundations ran %d times before its setup succeeded", n)
	}
	if rd := reg.Readiness()[capAppPlatform]; rd.Ready || rd.Reason != readinessSetupFailed {
		t.Errorf("platform readiness = %+v, want SetupFailed", rd)
	}
}

type fakeAuthenticator struct{}

func (fakeAuthenticator) Authenticate(context.Context, string) (identity.Principal, error) {
	return identity.Principal{Subject: "s", Name: "admin"}, nil
}

func TestLazyAuthenticator_NotReadyUntilSet(t *testing.T) {
	l := &lazyAuthenticator{}
	if _, err := l.Authenticate(context.Background(), "tok"); !errors.Is(err, reconcile.ErrProviderNotReady) {
		t.Fatalf("before set = %v, want ErrProviderNotReady", err)
	}
	l.set(fakeAuthenticator{})
	if p, err := l.Authenticate(context.Background(), "tok"); err != nil || p.Name != "admin" {
		t.Errorf("after set = (%+v, %v), want delegated", p, err)
	}
}

// Identity waits for the gateway, then sets up alongside the application
// platform (neither blocks the other); a failed identity setup is retried
// and reported SetupFailed; once up, the foundations loop re-ensures it and
// reports RepairFailed on a bad pass.
func TestSetupPlan_IdentityAfterGatewayAlongsidePlatform(t *testing.T) {
	reg := provider.NewRegistry()
	tr := newTracker(reg, backoff{initial: 5 * time.Millisecond, max: 5 * time.Millisecond},
		capGateway, capAppPlatform, capIdentity)
	ep := reconcile.GatewayEndpoint{Name: "gw", Namespace: "ns", Host: "localhost", Port: 30080}

	gatewayGate := make(chan struct{})
	platformGate := make(chan struct{}) // holds platform setup until identity is up
	var identityAttempts, identityRepairs atomic.Int32
	var identityEP atomic.Value
	failIdentityRepair := atomic.Bool{}
	failIdentityRepair.Store(true)

	plan := setupPlan{
		storage: func(context.Context) error { return nil },
		secrets: func(context.Context) error { return nil },
		gateway: func(ctx context.Context) (reconcile.GatewayEndpoint, error) {
			<-gatewayGate
			return ep, nil
		},
		platform: func(ctx context.Context, _ reconcile.GatewayEndpoint) error {
			select {
			case <-platformGate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		identity: func(_ context.Context, got reconcile.GatewayEndpoint) error {
			identityEP.Store(got)
			if identityAttempts.Add(1) < 3 {
				return errors.New("chart not installable yet")
			}
			return nil
		},
		gatewayFoundations:  func(context.Context) error { return nil },
		platformFoundations: func(context.Context, reconcile.GatewayEndpoint) error { return nil },
		identityFoundations: func(context.Context, reconcile.GatewayEndpoint) error {
			identityRepairs.Add(1)
			if failIdentityRepair.Load() {
				return errors.New("route deleted and could not be recreated")
			}
			return nil
		},
		reensureEvery: 10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	plan.start(ctx, tr)

	time.Sleep(20 * time.Millisecond)
	if n := identityAttempts.Load(); n != 0 {
		t.Fatalf("identity setup ran %d times before the gateway was up", n)
	}
	if rd := reg.Readiness()[capIdentity]; rd.Ready || rd.Reason != readinessSettingUp {
		t.Errorf("identity readiness before gateway = %+v, want SettingUp", rd)
	}
	close(gatewayGate)

	// Identity becomes ready while the platform's setup is still blocked.
	waitFor(t, func() bool { return identityAttempts.Load() >= 3 })
	waitFor(t, func() bool {
		return reg.Readiness()[capIdentity].Ready || reg.Readiness()[capIdentity].Reason == readinessRepairFailed
	})
	if got := identityEP.Load(); got != ep {
		t.Errorf("identity endpoint = %+v, want %+v", got, ep)
	}
	if reg.Readiness()[capAppPlatform].Ready {
		t.Error("platform ready although its setup is still blocked")
	}

	waitFor(t, func() bool { return reg.Readiness()[capIdentity].Reason == readinessRepairFailed })
	failIdentityRepair.Store(false)
	waitFor(t, func() bool { return reg.Readiness()[capIdentity].Ready })
	close(platformGate)
	waitFor(t, func() bool { return reg.Readiness()[capAppPlatform].Ready })
	if identityRepairs.Load() < 2 {
		t.Errorf("identity foundations passes = %d, want repeated passes", identityRepairs.Load())
	}
}
