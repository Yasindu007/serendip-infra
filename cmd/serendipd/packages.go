package main

import (
	"context"
	"database/sql"
	"log"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// packageCatalogue is the optional packages this build offers (specs/020
// plan decision 5). Neither has an installer yet: spec 022 brings builds
// and spec 023 observability, each through ADR 0010's Helm path.
func packageCatalogue() reconcile.Catalogue {
	return reconcile.Catalogue{
		"openchoreo-workflow-plane": {
			Provider: "openchoreo-workflow-plane", Capability: "build",
			Description: "Builds container images from source in the cluster (spec 022)",
		},
		"openchoreo-observability-plane": {
			Provider: "openchoreo-observability-plane", Capability: "observability",
			Description: "Stored logs, metrics, traces and alerts; heavy on a 16 GB machine (spec 023)",
		},
	}
}

// registerPackages wires the ProviderInstance reconciler into engine,
// reports each optional capability not installed until it's reconciled,
// and returns the clean-up that uninstalls a removed package.
func registerPackages(db *sql.DB, bus event.Bus, engine *reconcile.Engine, registry *provider.Registry, c reconcile.Catalogue) reconcile.CleanupFunc {
	report := func(capability string, ready bool, reason, message string) {
		registry.SetReadiness(capability, provider.Readiness{Ready: ready, Reason: reason, Message: message})
	}
	instances := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](db, resource.KindProviderInstance).SetBus(bus)
	r := reconcile.NewProviderInstanceReconciler(instances, c, report)
	if err := r.ReportStart(context.Background()); err != nil {
		log.Printf("serendipd: packages: report readiness at start: %v", err)
	}
	engine.Register(resource.KindProviderInstance, r, lister(instances, resource.KindProviderInstance))
	return reconcile.UninstallCleanup(instances, c, report)
}
