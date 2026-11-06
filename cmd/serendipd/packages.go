package main

import (
	"context"
	"database/sql"
	"log"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
	buildplane "github.com/serendip-infra/serendip-infra/providers/build/openchoreo"
)

const (
	capBuild         = "build"
	capObservability = "observability"
)

// buildsRemoval is what removing the builds package loses (specs/022-builds
// plan decision 12), shown before the request to remove it.
const buildsRemoval = "Build history stays, but run logs and the stored images built from source are deleted. " +
	"Components running a built image keep running, but can't be rebuilt, or started on a node that doesn't have the image, until the package is back."

// packageCatalogue is the optional packages this build offers (specs/020
// plan decision 5). builds installs the workflow plane (spec 022), or is
// nil when there's no cluster to install it on. Its Requirements stay nil
// until T013 measures them, so the catalogue refuses to install it until
// then. Observability (spec 023) has no installer yet.
func packageCatalogue(builds reconcile.PackageInstaller) reconcile.Catalogue {
	return reconcile.Catalogue{
		"openchoreo-workflow-plane": {
			Provider: "openchoreo-workflow-plane", Capability: capBuild,
			Description:  "Builds container images from source in the cluster",
			Installer:    builds,
			Requirements: buildplane.Requirements,
			Removal:      buildsRemoval,
		},
		"openchoreo-observability-plane": {
			Provider: "openchoreo-observability-plane", Capability: capObservability,
			Description: "Stored logs, metrics, traces and alerts; heavy on a 16 GB machine",
		},
	}
}

// onReadiness is called with each capability's readiness as packages
// report it, to hand a package's provider to the handles that use it
// while it's Ready.
type onReadiness map[string]func(ready bool)

// registerPackages wires the ProviderInstance reconciler into engine,
// reports each optional capability not installed until it's reconciled,
// and returns the clean-up that uninstalls a removed package.
func registerPackages(db *sql.DB, bus event.Bus, engine *reconcile.Engine, registry *provider.Registry, c reconcile.Catalogue, handles onReadiness) reconcile.CleanupFunc {
	report := func(capability string, ready bool, reason, message string) {
		registry.SetReadiness(capability, provider.Readiness{Ready: ready, Reason: reason, Message: message})
		if h := handles[capability]; h != nil {
			h(ready)
		}
	}
	instances := sqlite.NewStore[resource.ProviderInstanceSpec, resource.ProviderInstanceStatus](db, resource.KindProviderInstance).SetBus(bus)
	r := reconcile.NewProviderInstanceReconciler(instances, c, report)
	if err := r.ReportStart(context.Background()); err != nil {
		log.Printf("serendipd: packages: report readiness at start: %v", err)
	}
	engine.Register(resource.KindProviderInstance, r, lister(instances, resource.KindProviderInstance))
	return reconcile.UninstallCleanup(instances, c, report)
}

// buildHandle sets builds to p while the build package is Ready, and
// clears it otherwise, registering p once it's first Ready.
func buildHandle(registry *provider.Registry, builds *lazyBuilds, p reconcile.BuildProvider) func(ready bool) {
	return func(ready bool) {
		if !ready {
			if _, err := builds.get(); err == nil {
				log.Print("serendipd: kubernetes: build provider (openchoreo workflow plane) not ready")
			}
			builds.clear()
			return
		}
		if _, err := builds.get(); err == nil {
			return
		}
		registry.Register(capBuild, p)
		builds.set(p)
		log.Print("serendipd: kubernetes: build provider (openchoreo workflow plane) active")
	}
}

// registerBuilds wires the Build reconciler (spec 022) against the lazy
// build handle, with credentials read through the Secrets capability, and
// lets the Component reconciler run source Components. Must run before
// engine.Run.
func registerBuilds(db *sql.DB, bus event.Bus, engine *reconcile.Engine,
	comps *reconcile.ComponentReconciler,
	components store.Store[resource.ComponentSpec, resource.ComponentStatus],
	apps store.Store[resource.ApplicationSpec, resource.ApplicationStatus],
	builds reconcile.BuildProvider, creds reconcile.CredentialReader,
) {
	buildStore := sqlite.NewStore[resource.BuildSpec, resource.BuildStatus](db, resource.KindBuild).SetBus(bus)
	comps.WithBuilds(buildStore, builds, creds)
	engine.Register(resource.KindBuild, reconcile.NewBuildReconciler(buildStore, components, apps, builds, creds, engine.Enqueue), lister(buildStore, resource.KindBuild))
}
