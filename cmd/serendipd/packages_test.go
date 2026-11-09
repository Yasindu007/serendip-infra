package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
	buildplane "github.com/serendip-infra/serendip-infra/providers/build/openchoreo"
)

// The optional capabilities show in GET /v1/readiness from the start,
// as not available in this build until specs 022 and 023.
func TestPackagesReportReadiness(t *testing.T) {
	db, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	bus := event.NewInProcessBus()
	registry := provider.NewRegistry()
	clean := registerPackages(db, bus, reconcile.NewEngine(bus, time.Minute, time.Minute), registry, packageCatalogue(nil), nil)
	if clean == nil {
		t.Fatal("no clean-up")
	}
	got := registry.Readiness()
	for _, capability := range []string{"build", "observability"} {
		if rd, ok := got[capability]; !ok || rd.Ready || rd.Reason != reconcile.ReasonUnavailable {
			t.Errorf("%s readiness = %+v", capability, rd)
		}
	}
}

// The builds package carries its measured cost (T013), so it can be
// installed, and the catalogue states the same numbers the package does.
func TestBuildsPackageInstallableWithMeasuredCost(t *testing.T) {
	c := packageCatalogue(installerStub{})
	p := c["openchoreo-workflow-plane"]
	if p.Installer == nil || p.Removal == "" || p.Capability != capBuild {
		t.Fatalf("builds entry = %+v", p)
	}
	if err := p.Installable(); err != nil {
		t.Fatalf("Installable() = %v, want nil", err)
	}
	if p.Requirements == nil || *p.Requirements != *buildplane.Requirements {
		t.Fatalf("Requirements = %+v, want the package's %+v", p.Requirements, buildplane.Requirements)
	}
	if err := c.Validate(resource.ProviderInstanceSpec{ProviderID: p.Provider, CapabilityID: capBuild}); err != nil {
		t.Fatalf("Validate = %v", err)
	}
}

// The build handle answers ErrProviderNotReady until the package is
// Ready, and again once it stops being Ready.
func TestBuildHandleFollowsReadiness(t *testing.T) {
	registry := provider.NewRegistry()
	builds := &lazyBuilds{}
	var p reconcile.BuildProvider = &lazyBuilds{} // any provider will do
	set := buildHandle(registry, builds, p)
	notReady := func() bool {
		_, err := builds.HeadCommit(t.Context(), resource.ComponentSource{}, nil)
		return errors.Is(err, reconcile.ErrProviderNotReady)
	}
	if !notReady() {
		t.Fatal("handle ready before the package")
	}
	set(true)
	if got, err := builds.get(); err != nil || got != p {
		t.Fatalf("after Ready: %v, %v", got, err)
	}
	if impl, ok := registry.Lookup(capBuild); !ok || impl != p {
		t.Fatalf("registry build = %v, %v", impl, ok)
	}
	set(true) // a repeated report changes nothing
	set(false)
	if !notReady() {
		t.Fatal("handle still set after the package stopped being Ready")
	}
}

type installerStub struct{}

func (installerStub) Install(context.Context, resource.ProviderInstance) (reconcile.PackageState, error) {
	return reconcile.PackageState{}, nil
}
func (installerStub) Uninstall(context.Context, string) error { return nil }
