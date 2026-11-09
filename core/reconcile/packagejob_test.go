package reconcile_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// slowPackage blocks each Install and Uninstall until released, like a
// Helm run that takes minutes, and records whether its context was
// cancelled before it finished.
type slowPackage struct {
	release   chan struct{}
	state     reconcile.PackageState
	cancelled chan bool
}

func newSlowPackage() *slowPackage {
	return &slowPackage{release: make(chan struct{}), cancelled: make(chan bool, 4),
		state: reconcile.PackageState{Ready: true, Message: "ready", Applied: "v1"}}
}

func (p *slowPackage) wait(ctx context.Context) {
	<-p.release
	p.cancelled <- ctx.Err() != nil
}

func (p *slowPackage) Install(ctx context.Context, _ resource.ProviderInstance) (reconcile.PackageState, error) {
	p.wait(ctx)
	return p.state, nil
}

func (p *slowPackage) Uninstall(ctx context.Context, _ string) error {
	p.wait(ctx)
	return nil
}

// An install longer than a reconcile runs in the background: reconciles
// read Installing and come back, the install's context outlives each
// reconcile's, and the first reconcile after it finishes takes its
// result (specs/022-builds T013).
func TestProviderInstanceReconciler_SlowInstallRunsInBackground(t *testing.T) {
	s := newAppStores(t)
	insts := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](s.db, resource.KindProviderInstance)
	pkg := newSlowPackage()
	cat := reconcile.Catalogue{"slow": {Provider: "slow", Capability: "build", Installer: pkg, Requirements: measured}}
	log := readinessLog{}
	r := reconcile.NewProviderInstanceReconciler(insts, cat, log.report)
	reconcile.SetPackageJobWait(r, 20*time.Millisecond)

	inst, err := insts.Create(context.Background(), resource.ProviderInstance{Metadata: resource.Metadata{Name: "slow"},
		Spec: resource.ProviderInstanceSpec{CapabilityID: "build", ProviderID: "slow"}})
	if err != nil {
		t.Fatal(err)
	}
	ref := graph.ResourceRef{Kind: resource.KindProviderInstance, ID: inst.Metadata.ID}
	reconcileOnce := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return r.Reconcile(ctx, ref)
	}

	for i := 0; i < 2; i++ {
		checkRequeue(t, reconcileOnce(), true)
		got, _ := insts.Get(context.Background(), inst.Metadata.ID)
		if c := readyCondition(t, got.Status.Conditions); c.Reason != reconcile.ReasonInstalling || log["build"] != reconcile.ReasonInstalling {
			t.Fatalf("while installing: %+v, readiness %v", c, log)
		}
	}

	close(pkg.release)
	if <-pkg.cancelled {
		t.Error("the install's context was cancelled with the reconcile that started it")
	}
	checkRequeue(t, reconcileOnce(), false)
	got, _ := insts.Get(context.Background(), inst.Metadata.ID)
	if c := readyCondition(t, got.Status.Conditions); c.Reason != reconcile.ReasonInstalled || got.Status.Applied != "v1" || log["build"] != "ready "+reconcile.ReasonInstalled {
		t.Errorf("after the install: %+v, applied %q, readiness %v", c, got.Status.Applied, log)
	}
}

// A removal longer than a reconcile also runs in the background: the
// clean-up answers ErrPackageBusy until it has finished, then succeeds.
func TestUninstallCleanup_SlowRemovalRunsInBackground(t *testing.T) {
	s := newAppStores(t)
	insts := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](s.db, resource.KindProviderInstance)
	pkg := newSlowPackage()
	cat := reconcile.Catalogue{"slow": {Provider: "slow", Capability: "build", Installer: pkg, Requirements: measured}}
	log := readinessLog{"build": "ready Ready"}
	r := reconcile.NewProviderInstanceReconciler(insts, cat, log.report)
	reconcile.SetPackageJobWait(r, 20*time.Millisecond)
	clean := r.UninstallCleanup()
	spec, _ := json.Marshal(resource.ProviderInstanceSpec{CapabilityID: "build", ProviderID: "slow"})
	ref := resource.ApprovalRef{ID: "pi-1", LastSpec: spec}

	for i := 0; i < 2; i++ {
		if err := clean(context.Background(), ref); !errors.Is(err, reconcile.ErrPackageBusy) {
			t.Fatalf("while removing: %v, want ErrPackageBusy", err)
		}
	}
	close(pkg.release)
	if <-pkg.cancelled {
		t.Error("the removal's context was cancelled")
	}
	if err := clean(context.Background(), ref); err != nil {
		t.Fatalf("after the removal: %v", err)
	}
	if log["build"] != reconcile.ReasonNotInstalled {
		t.Errorf("readiness after the removal = %q", log["build"])
	}
}
