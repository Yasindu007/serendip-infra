package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
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
	clean := registerPackages(db, bus, reconcile.NewEngine(bus, time.Minute, time.Minute), registry, packageCatalogue())
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
