package reconcile_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// testPackage is the test package that proves the mechanics (no real
// package exists before specs 022 and 023).
type testPackage struct {
	mu          sync.Mutex
	state       reconcile.PackageState
	err         error
	installs    int
	uninstalled []string
}

func (p *testPackage) Install(context.Context, resource.ProviderInstance) (reconcile.PackageState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.installs++
	return p.state, p.err
}

func (p *testPackage) Uninstall(_ context.Context, id string) error {
	p.uninstalled = append(p.uninstalled, id)
	return nil
}

type readinessLog map[string]string // capability -> "ready reason"

func (l readinessLog) report(capability string, ready bool, reason, _ string) {
	l[capability] = map[bool]string{true: "ready ", false: ""}[ready] + reason
}

func newPackages(t *testing.T) (*testPackage, reconcile.Catalogue) {
	t.Helper()
	pkg := &testPackage{}
	return pkg, reconcile.Catalogue{
		"test-builder": {Provider: "test-builder", Capability: "build", Installer: pkg},
		"later":        {Provider: "later", Capability: "observability"},
	}
}

func TestProviderInstanceReconciler_InstallsToReady(t *testing.T) {
	ctx := context.Background()
	s := newAppStores(t)
	insts := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](s.db, resource.KindProviderInstance)
	pkg, catalogue := newPackages(t)
	log := readinessLog{}
	r := reconcile.NewProviderInstanceReconciler(insts, catalogue, log.report)

	if err := r.ReportStart(ctx); err != nil {
		t.Fatal(err)
	}
	if log["build"] != reconcile.ReasonNotInstalled || log["observability"] != reconcile.ReasonUnavailable {
		t.Errorf("at start: %v", log)
	}

	inst, err := insts.Create(ctx, resource.ProviderInstance{Metadata: resource.Metadata{Name: "builds"},
		Spec: resource.ProviderInstanceSpec{CapabilityID: "build", ProviderID: "test-builder"}})
	if err != nil {
		t.Fatal(err)
	}
	ref := graph.ResourceRef{Kind: resource.KindProviderInstance, ID: inst.Metadata.ID}

	pkg.state = reconcile.PackageState{Message: "2 of 3 charts ready"}
	checkRequeue(t, r.Reconcile(ctx, ref), true)
	got, _ := insts.Get(ctx, inst.Metadata.ID)
	if c := readyCondition(t, got.Status.Conditions); c.Reason != reconcile.ReasonInstalling || c.Message != "2 of 3 charts ready" || log["build"] != reconcile.ReasonInstalling {
		t.Errorf("installing: %+v, readiness %v", c, log)
	}

	pkg.state = reconcile.PackageState{Ready: true}
	checkRequeue(t, r.Reconcile(ctx, ref), false)
	got, _ = insts.Get(ctx, inst.Metadata.ID)
	if c := readyCondition(t, got.Status.Conditions); c.Status != resource.ConditionTrue || log["build"] != "ready "+reconcile.ReasonInstalled {
		t.Errorf("installed: %+v, readiness %v", c, log)
	}
	version := got.Metadata.ResourceVersion
	checkRequeue(t, r.Reconcile(ctx, ref), false)
	if again, _ := insts.Get(ctx, inst.Metadata.ID); again.Metadata.ResourceVersion != version {
		t.Error("a no-change reconcile wrote the ProviderInstance")
	}

	// A failure is reported and retried.
	pkg.err = errors.New("chart pull failed")
	if err := r.Reconcile(ctx, ref); err == nil {
		t.Error("an install failure wasn't returned for retry")
	}
	got, _ = insts.Get(ctx, inst.Metadata.ID)
	if c := readyCondition(t, got.Status.Conditions); c.Reason != reconcile.ReasonInstallFailed || log["build"] != reconcile.ReasonInstallFailed {
		t.Errorf("failed: %+v, readiness %v", c, log)
	}
	if c := readyCondition(t, got.Status.Conditions); strings.Contains(c.Message, "chart pull") {
		t.Errorf("the installer's error text reached the condition: %q", c.Message)
	}
	// The platform not being up yet is waiting, not failing.
	pkg.err = reconcile.ErrProviderNotReady
	checkRequeue(t, r.Reconcile(ctx, ref), true)

	// On restart, an installed package is "starting", not missing.
	if err := r.ReportStart(ctx); err != nil || log["build"] != reconcile.ReasonInstalling {
		t.Errorf("restart: %v, readiness %v", err, log)
	}

	// A second instance (past the API) doesn't install or touch readiness.
	pkg.err, pkg.installs = nil, 0
	log["build"] = "ready " + reconcile.ReasonInstalled
	dup, err := insts.Create(ctx, resource.ProviderInstance{Metadata: resource.Metadata{Name: "again"},
		Spec: resource.ProviderInstanceSpec{CapabilityID: "build", ProviderID: "test-builder"}})
	if err != nil {
		t.Fatal(err)
	}
	checkRequeue(t, r.Reconcile(ctx, graph.ResourceRef{Kind: resource.KindProviderInstance, ID: dup.Metadata.ID}), false)
	got, _ = insts.Get(ctx, dup.Metadata.ID)
	if c := readyCondition(t, got.Status.Conditions); c.Reason != reconcile.ReasonDuplicate || pkg.installs != 0 || log["build"] != "ready "+reconcile.ReasonInstalled {
		t.Errorf("duplicate: %+v, installs %d, readiness %v", c, pkg.installs, log)
	}

	// Its removal leaves the capability as the first instance has it.
	clean := reconcile.UninstallCleanup(insts, catalogue, log.report)
	spec, _ := json.Marshal(dup.Spec)
	approved := store.WithApproval(ctx, store.ApprovalIntent{Approval: resource.Approval{
		Spec:   resource.ApprovalSpec{Action: resource.ApprovalAction{Verb: resource.ApprovalVerbDelete, Target: resource.Scope{Kind: resource.KindProviderInstance, ID: dup.Metadata.ID}}},
		Status: resource.ApprovalStatus{Phase: resource.ApprovalApproved},
	}})
	if err := insts.Delete(approved, dup.Metadata.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := clean(ctx, resource.ApprovalRef{ID: dup.Metadata.ID, LastSpec: spec}); err != nil || log["build"] != "ready "+reconcile.ReasonInstalled {
		t.Errorf("removing the duplicate: %v, readiness %v", err, log)
	}
}

func TestProviderInstanceReconciler_RefusesWhatItCantInstall(t *testing.T) {
	ctx := context.Background()
	s := newAppStores(t)
	insts := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](s.db, resource.KindProviderInstance)
	pkg, catalogue := newPackages(t)
	r := reconcile.NewProviderInstanceReconciler(insts, catalogue, readinessLog{}.report)
	log := readinessLog{}
	r = reconcile.NewProviderInstanceReconciler(insts, catalogue, log.report)
	for _, spec := range []resource.ProviderInstanceSpec{
		{CapabilityID: "build", ProviderID: "unknown"},
		{CapabilityID: "observability", ProviderID: "test-builder"},
		{CapabilityID: "observability", ProviderID: "later"},
		{CapabilityID: "build", ProviderID: "test-builder", EnvironmentID: "env-1"},
	} {
		inst, err := insts.Create(ctx, resource.ProviderInstance{Metadata: resource.Metadata{Name: "x"}, Spec: spec})
		if err != nil {
			t.Fatal(err)
		}
		checkRequeue(t, r.Reconcile(ctx, graph.ResourceRef{Kind: resource.KindProviderInstance, ID: inst.Metadata.ID}), false)
		got, _ := insts.Get(ctx, inst.Metadata.ID)
		if c := readyCondition(t, got.Status.Conditions); c.Reason != reconcile.ReasonInvalidSpec {
			t.Errorf("%+v: %+v", spec, c)
		}
	}
	if pkg.installs != 0 {
		t.Errorf("installed %d times", pkg.installs)
	}
	// The capabilities named are reported invalid, not left as they were.
	if log["build"] != reconcile.ReasonInvalidSpec || log["observability"] != reconcile.ReasonInvalidSpec {
		t.Errorf("readiness = %v", log)
	}
	// Gone: the approval's clean-up's business.
	if err := r.Reconcile(ctx, graph.ResourceRef{Kind: resource.KindProviderInstance, ID: "gone"}); err != nil {
		t.Error(err)
	}
}

func TestUninstallCleanup(t *testing.T) {
	pkg, catalogue := newPackages(t)
	log := readinessLog{"build": "ready Ready"}
	s := newAppStores(t)
	insts := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](s.db, resource.KindProviderInstance)
	clean := reconcile.UninstallCleanup(insts, catalogue, log.report)
	spec, _ := json.Marshal(resource.ProviderInstanceSpec{CapabilityID: "build", ProviderID: "test-builder"})
	if err := clean(context.Background(), resource.ApprovalRef{ID: "pi-1", LastSpec: spec}); err != nil {
		t.Fatal(err)
	}
	if len(pkg.uninstalled) != 1 || pkg.uninstalled[0] != "pi-1" || log["build"] != reconcile.ReasonNotInstalled {
		t.Errorf("uninstalled %v, readiness %v", pkg.uninstalled, log)
	}
	// Something this build never installed: nothing to do.
	other, _ := json.Marshal(resource.ProviderInstanceSpec{ProviderID: "unknown"})
	if err := clean(context.Background(), resource.ApprovalRef{ID: "pi-2", LastSpec: other}); err != nil || len(pkg.uninstalled) != 1 {
		t.Errorf("unknown package: %v, %v", err, pkg.uninstalled)
	}
	// A missing or damaged last spec can't strand the clean-up: every
	// package removes what the instance might have installed.
	for _, last := range [][]byte{nil, []byte("{oops")} {
		pkg.uninstalled = nil
		if err := clean(context.Background(), resource.ApprovalRef{ID: "pi-3", LastSpec: last}); err != nil || len(pkg.uninstalled) != 1 || pkg.uninstalled[0] != "pi-3" {
			t.Errorf("last spec %q: %v, uninstalled %v", last, err, pkg.uninstalled)
		}
	}
}
