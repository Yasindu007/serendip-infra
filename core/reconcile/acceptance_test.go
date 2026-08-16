package reconcile_test

import (
	"context"
	"database/sql"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// This file proves specs/003-reconciliation-engine/spec.md's acceptance
// criteria 2-4 through the fully composed reconcile.Engine (event
// subscription + periodic resync + ClusterReconciler + real sqlite
// stores) -- T006's cluster_test.go already proves ClusterReconciler's
// own logic in isolation; this proves the whole engine actually drives
// it, mirroring specs 001/002's TestAcceptanceCriterionN_... pattern.

type acceptanceFixture struct {
	db        *sql.DB
	clusters  store.Store[resource.ClusterSpec, resource.ClusterStatus]
	providers store.Store[resource.ProviderSpec, resource.ProviderStatus]
	engine    *reconcile.Engine
}

func newAcceptanceFixture(t *testing.T, resyncEvery time.Duration) *acceptanceFixture {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() = %v", err)
	}

	bus := event.NewInProcessBus()
	clusters := sqlite.NewStore[resource.ClusterSpec, resource.ClusterStatus](db, resource.KindCluster).SetBus(bus)
	providers := sqlite.NewStore[resource.ProviderSpec, resource.ProviderStatus](db, resource.KindProvider).SetBus(bus)

	engine := reconcile.NewEngine(bus, resyncEvery, time.Second)
	engine.Register(resource.KindCluster, reconcile.NewClusterReconciler(clusters, providers, reconcile.TCPHealthChecker{}),
		func(ctx context.Context) ([]graph.ResourceRef, error) {
			list, err := clusters.List(ctx, store.ListOptions{})
			if err != nil {
				return nil, err
			}
			refs := make([]graph.ResourceRef, len(list))
			for i, c := range list {
				refs[i] = graph.ResourceRef{Kind: resource.KindCluster, ID: c.Metadata.ID}
			}
			return refs, nil
		})

	return &acceptanceFixture{db: db, clusters: clusters, providers: providers, engine: engine}
}

func startListener(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func waitForCondition(t *testing.T, f *acceptanceFixture, clusterID string, wantStatus resource.ConditionStatus) resource.Cluster {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last resource.Cluster
	for time.Now().Before(deadline) {
		got, err := f.clusters.Get(context.Background(), clusterID)
		if err != nil {
			t.Fatalf("Get() = %v", err)
		}
		last = got
		for _, c := range got.Status.Conditions {
			if c.Type == "Healthy" && c.Status == wantStatus {
				return got
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Condition never reached Status=%q within timeout; last Conditions = %+v", wantStatus, last.Status.Conditions)
	return resource.Cluster{}
}

// TestAcceptanceCriterion2_EventTriggered_ClusterMarkedHealthy is spec.md's
// acceptance criterion 2: a Cluster with a Provider whose HealthCheck
// targets a real, reachable TCP listener gets a "Healthy" Condition with
// Status: True after one reconcile pass, through the real Engine
// (event-triggered), not by calling ClusterReconciler directly.
func TestAcceptanceCriterion2_EventTriggered_ClusterMarkedHealthy(t *testing.T) {
	f := newAcceptanceFixture(t, time.Hour) // resync interval large: isolate event-triggering
	addr, stop := startListener(t)
	defer stop()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go f.engine.Run(ctx)
	<-f.engine.Ready()

	provider, err := f.providers.Create(context.Background(), resource.Provider{
		Metadata: resource.Metadata{Name: "acceptance-provider"},
		Spec:     resource.ProviderSpec{HealthCheck: resource.HealthCheckContract{Kind: "tcp", Config: map[string]string{"address": addr}}},
	})
	if err != nil {
		t.Fatalf("Create(provider) = %v", err)
	}
	cluster, err := f.clusters.Create(context.Background(), resource.Cluster{
		Metadata: resource.Metadata{Name: "acceptance-cluster"},
		Spec:     resource.ClusterSpec{ProviderID: provider.Metadata.ID},
	})
	if err != nil {
		t.Fatalf("Create(cluster) = %v", err)
	}

	got := waitForCondition(t, f, cluster.Metadata.ID, resource.ConditionTrue)
	if got.Status.Conditions[0].Reason != "HealthCheckPassed" {
		t.Errorf("Condition.Reason = %q, want %q", got.Status.Conditions[0].Reason, "HealthCheckPassed")
	}
}

// TestAcceptanceCriterion3_RepeatedReconcile_NoDrift_ExactlyOneAuditRow is
// spec.md's acceptance criterion 3: reconciling the same unchanged
// Cluster twice in a row produces exactly one audit_log row and one
// ResourceVersion bump, not two -- the second pass is a verified no-op.
// Proven through the real Engine (event-triggered dispatch), not by
// calling ClusterReconciler.Reconcile directly (cluster_test.go already
// covers that at the unit level).
func TestAcceptanceCriterion3_RepeatedReconcile_NoDrift_ExactlyOneAuditRow(t *testing.T) {
	f := newAcceptanceFixture(t, time.Hour)
	addr, stop := startListener(t)
	defer stop()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go f.engine.Run(ctx)
	<-f.engine.Ready()

	provider, err := f.providers.Create(context.Background(), resource.Provider{
		Metadata: resource.Metadata{Name: "acceptance-provider"},
		Spec:     resource.ProviderSpec{HealthCheck: resource.HealthCheckContract{Kind: "tcp", Config: map[string]string{"address": addr}}},
	})
	if err != nil {
		t.Fatalf("Create(provider) = %v", err)
	}
	cluster, err := f.clusters.Create(context.Background(), resource.Cluster{
		Metadata: resource.Metadata{Name: "acceptance-cluster"},
		Spec:     resource.ClusterSpec{ProviderID: provider.Metadata.ID},
	})
	if err != nil {
		t.Fatalf("Create(cluster) = %v", err)
	}

	// First pass: Create's own event triggers the first reconcile.
	afterFirst := waitForCondition(t, f, cluster.Metadata.ID, resource.ConditionTrue)

	// Second pass: nothing changed about the Cluster or Provider, so
	// there is no event to trigger a second reconcile naturally -- an
	// Update event on the Cluster itself (an actual no-op edit, e.g.
	// re-saving the same Name) is the most direct way to trigger a
	// second real reconcile pass without waiting for resync (which
	// criterion 4 tests separately).
	if _, err := f.clusters.Update(context.Background(), afterFirst); err != nil {
		t.Fatalf("Update() (no-op re-save) = %v", err)
	}
	// Give the triggered reconcile a moment to run; it should NOT
	// change the Condition (already True/HealthCheckPassed) and
	// therefore should not call Update a second time.
	time.Sleep(200 * time.Millisecond)

	afterSecond, err := f.clusters.Get(context.Background(), cluster.Metadata.ID)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	// ResourceVersion after the explicit no-op Update above is
	// afterFirst.ResourceVersion+1; the reconciler's own pass must not
	// add a further bump on top of that.
	wantVersion := afterFirst.Metadata.ResourceVersion + 1
	if afterSecond.Metadata.ResourceVersion != wantVersion {
		t.Errorf("ResourceVersion after re-save + reconcile = %d, want %d (reconciler must not write again for unchanged Condition)",
			afterSecond.Metadata.ResourceVersion, wantVersion)
	}

	rows := countAuditRows(t, f, cluster.Metadata.ID)
	// Create (1) + the reconciler's one real Condition-setting Update (1)
	// + the test's explicit no-op re-save Update (1) = 3. The reconciler
	// must not have added a 4th row reacting to the no-op re-save.
	if rows != 3 {
		t.Errorf("audit_log rows for cluster = %d, want 3 (Create + reconciler Update + explicit no-op re-save), reconciler must not write a redundant 4th row", rows)
	}
}

// TestAcceptanceCriterion4_ResyncAlone_DetectsHealthFlip is spec.md's
// acceptance criterion 4: after the first reconcile marks a Cluster
// healthy, stopping the backing listener with no store mutation (nothing
// triggers a bus event) still gets caught -- by periodic resync alone --
// flipping the Condition to Status: False.
func TestAcceptanceCriterion4_ResyncAlone_DetectsHealthFlip(t *testing.T) {
	f := newAcceptanceFixture(t, 50*time.Millisecond) // short resync interval for the test
	addr, stop := startListener(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go f.engine.Run(ctx)
	<-f.engine.Ready()

	provider, err := f.providers.Create(context.Background(), resource.Provider{
		Metadata: resource.Metadata{Name: "acceptance-provider"},
		Spec:     resource.ProviderSpec{HealthCheck: resource.HealthCheckContract{Kind: "tcp", Config: map[string]string{"address": addr}}},
	})
	if err != nil {
		t.Fatalf("Create(provider) = %v", err)
	}
	cluster, err := f.clusters.Create(context.Background(), resource.Cluster{
		Metadata: resource.Metadata{Name: "acceptance-cluster"},
		Spec:     resource.ClusterSpec{ProviderID: provider.Metadata.ID},
	})
	if err != nil {
		t.Fatalf("Create(cluster) = %v", err)
	}

	waitForCondition(t, f, cluster.Metadata.ID, resource.ConditionTrue)

	// No store mutation happens here -- nothing publishes an event.
	// Only the periodic resync ticker can be responsible for whatever
	// happens next.
	stop()

	got := waitForCondition(t, f, cluster.Metadata.ID, resource.ConditionFalse)
	if got.Status.Conditions[0].Reason != "HealthCheckFailed" {
		t.Errorf("Condition.Reason = %q, want %q", got.Status.Conditions[0].Reason, "HealthCheckFailed")
	}
}

// countAuditRows mirrors the direct-query approach
// core/store/sqlite/store_test.go's countAuditRecords and
// api/acceptance_test.go's countAllAuditRows already use -- audit_log
// isn't exposed through store.Store, so tests that need to assert on it
// query the underlying *sql.DB directly.
// TestAcceptanceCriterion5_AuditRowsShowReconcilerIdentity is spec.md's
// acceptance criterion 5: every reconciler-driven audit_log row shows
// Interface = reconcile.InterfaceName and Actor = reconcile.ActorName --
// not "core" or "http" (Interface) and not "unknown" (Actor). This
// checks the persisted row itself, not just the context value
// engine_test.go's TestEngine_ReconcileContext_CarriesAuditIdentity
// already proves is set correctly before the store call happens.
func TestAcceptanceCriterion5_AuditRowsShowReconcilerIdentity(t *testing.T) {
	f := newAcceptanceFixture(t, time.Hour)
	addr, stop := startListener(t)
	defer stop()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go f.engine.Run(ctx)
	<-f.engine.Ready()

	provider, err := f.providers.Create(context.Background(), resource.Provider{
		Metadata: resource.Metadata{Name: "acceptance-provider"},
		Spec:     resource.ProviderSpec{HealthCheck: resource.HealthCheckContract{Kind: "tcp", Config: map[string]string{"address": addr}}},
	})
	if err != nil {
		t.Fatalf("Create(provider) = %v", err)
	}
	cluster, err := f.clusters.Create(context.Background(), resource.Cluster{
		Metadata: resource.Metadata{Name: "acceptance-cluster"},
		Spec:     resource.ClusterSpec{ProviderID: provider.Metadata.ID},
	})
	if err != nil {
		t.Fatalf("Create(cluster) = %v", err)
	}

	waitForCondition(t, f, cluster.Metadata.ID, resource.ConditionTrue)

	rows, err := f.db.QueryContext(context.Background(),
		`SELECT actor, interface FROM audit_log WHERE resource_id = ? ORDER BY at`, cluster.Metadata.ID)
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	defer rows.Close()

	var sawReconcilerRow bool
	for rows.Next() {
		var actor, iface string
		if err := rows.Scan(&actor, &iface); err != nil {
			t.Fatalf("scan audit_log row: %v", err)
		}
		if iface != reconcile.InterfaceName {
			continue // this is the Create row, made directly against the store (Interface defaults to "core"), not reconciler-driven
		}
		sawReconcilerRow = true
		if actor != reconcile.ActorName {
			t.Errorf("reconciler-driven audit row actor = %q, want %q (not \"unknown\")", actor, reconcile.ActorName)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit_log rows: %v", err)
	}
	if !sawReconcilerRow {
		t.Fatal("no audit_log row with Interface = reconcile.InterfaceName found -- the reconciler's Update never happened or wasn't audited")
	}
}

func countAuditRows(t *testing.T, f *acceptanceFixture, resourceID string) int {
	t.Helper()
	var n int
	err := f.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM audit_log WHERE resource_id = ?`, resourceID).Scan(&n)
	if err != nil {
		t.Fatalf("count audit_log rows for %s: %v", resourceID, err)
	}
	return n
}
