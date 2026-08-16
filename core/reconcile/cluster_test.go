package reconcile_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func newClusterTestStores(t *testing.T) (
	store.Store[resource.ClusterSpec, resource.ClusterStatus],
	store.Store[resource.ProviderSpec, resource.ProviderStatus],
) {
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
	clusters := sqlite.NewStore[resource.ClusterSpec, resource.ClusterStatus](db, resource.KindCluster)
	providers := sqlite.NewStore[resource.ProviderSpec, resource.ProviderStatus](db, resource.KindProvider)
	return clusters, providers
}

func listenAndClose(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestClusterReconciler_Cases(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(t *testing.T, providers store.Store[resource.ProviderSpec, resource.ProviderStatus]) resource.ClusterSpec
		wantStatus   resource.ConditionStatus
		wantReason   string
		wantHasError bool
	}{
		{
			name: "no provider configured",
			setup: func(t *testing.T, providers store.Store[resource.ProviderSpec, resource.ProviderStatus]) resource.ClusterSpec {
				return resource.ClusterSpec{}
			},
			wantStatus: resource.ConditionUnknown,
			wantReason: "NoProviderConfigured",
		},
		{
			name: "provider not found",
			setup: func(t *testing.T, providers store.Store[resource.ProviderSpec, resource.ProviderStatus]) resource.ClusterSpec {
				return resource.ClusterSpec{ProviderID: "does-not-exist"}
			},
			wantStatus: resource.ConditionUnknown,
			wantReason: "ProviderNotFound",
		},
		{
			name: "provider has no health check configured",
			setup: func(t *testing.T, providers store.Store[resource.ProviderSpec, resource.ProviderStatus]) resource.ClusterSpec {
				p, err := providers.Create(context.Background(), resource.Provider{
					Metadata: resource.Metadata{Name: "unconfigured-provider"},
				})
				if err != nil {
					t.Fatalf("Create(provider) = %v", err)
				}
				return resource.ClusterSpec{ProviderID: p.Metadata.ID}
			},
			wantStatus: resource.ConditionUnknown,
			wantReason: "NoHealthCheckConfigured",
		},
		{
			name: "health check fails",
			setup: func(t *testing.T, providers store.Store[resource.ProviderSpec, resource.ProviderStatus]) resource.ClusterSpec {
				p, err := providers.Create(context.Background(), resource.Provider{
					Metadata: resource.Metadata{Name: "unhealthy-provider"},
					Spec: resource.ProviderSpec{
						HealthCheck: resource.HealthCheckContract{
							Kind:   "tcp",
							Config: map[string]string{"address": listenAndClose(t)},
						},
					},
				})
				if err != nil {
					t.Fatalf("Create(provider) = %v", err)
				}
				return resource.ClusterSpec{ProviderID: p.Metadata.ID}
			},
			wantStatus:   resource.ConditionFalse,
			wantReason:   "HealthCheckFailed",
			wantHasError: true,
		},
		{
			name: "health check passes",
			setup: func(t *testing.T, providers store.Store[resource.ProviderSpec, resource.ProviderStatus]) resource.ClusterSpec {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatalf("Listen() = %v", err)
				}
				t.Cleanup(func() { ln.Close() })
				go func() {
					for {
						conn, err := ln.Accept()
						if err != nil {
							return
						}
						conn.Close()
					}
				}()
				p, err := providers.Create(context.Background(), resource.Provider{
					Metadata: resource.Metadata{Name: "healthy-provider"},
					Spec: resource.ProviderSpec{
						HealthCheck: resource.HealthCheckContract{
							Kind:   "tcp",
							Config: map[string]string{"address": ln.Addr().String()},
						},
					},
				})
				if err != nil {
					t.Fatalf("Create(provider) = %v", err)
				}
				return resource.ClusterSpec{ProviderID: p.Metadata.ID}
			},
			wantStatus: resource.ConditionTrue,
			wantReason: "HealthCheckPassed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusters, providers := newClusterTestStores(t)
			spec := tt.setup(t, providers)

			cluster, err := clusters.Create(context.Background(), resource.Cluster{
				Metadata: resource.Metadata{Name: "cluster-under-test"},
				Spec:     spec,
			})
			if err != nil {
				t.Fatalf("Create(cluster) = %v", err)
			}

			r := reconcile.NewClusterReconciler(clusters, providers, reconcile.TCPHealthChecker{})
			ref := graph.ResourceRef{Kind: resource.KindCluster, ID: cluster.Metadata.ID}
			if err := r.Reconcile(context.Background(), ref); err != nil {
				t.Fatalf("Reconcile() = %v", err)
			}

			got, err := clusters.Get(context.Background(), cluster.Metadata.ID)
			if err != nil {
				t.Fatalf("Get() after reconcile = %v", err)
			}
			if len(got.Status.Conditions) != 1 {
				t.Fatalf("Conditions = %+v, want exactly 1", got.Status.Conditions)
			}
			cond := got.Status.Conditions[0]
			if cond.Type != "Healthy" {
				t.Errorf("Condition.Type = %q, want %q", cond.Type, "Healthy")
			}
			if cond.Status != tt.wantStatus {
				t.Errorf("Condition.Status = %q, want %q", cond.Status, tt.wantStatus)
			}
			if cond.Reason != tt.wantReason {
				t.Errorf("Condition.Reason = %q, want %q", cond.Reason, tt.wantReason)
			}
			if tt.wantHasError && cond.Message == "" {
				t.Error("Condition.Message is empty, want a health-check error message")
			}
		})
	}
}

// This is acceptance criterion 3's core mechanism at the unit level: a
// repeated Reconcile call with no drift must make zero additional Update
// calls -- proven here by asserting ResourceVersion is unchanged after a
// second call, not just "no error."
func TestClusterReconciler_NoDrift_IsIdempotent_NoAdditionalUpdate(t *testing.T) {
	clusters, providers := newClusterTestStores(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	p, err := providers.Create(context.Background(), resource.Provider{
		Metadata: resource.Metadata{Name: "healthy-provider"},
		Spec: resource.ProviderSpec{
			HealthCheck: resource.HealthCheckContract{
				Kind:   "tcp",
				Config: map[string]string{"address": ln.Addr().String()},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create(provider) = %v", err)
	}
	cluster, err := clusters.Create(context.Background(), resource.Cluster{
		Metadata: resource.Metadata{Name: "cluster-under-test"},
		Spec:     resource.ClusterSpec{ProviderID: p.Metadata.ID},
	})
	if err != nil {
		t.Fatalf("Create(cluster) = %v", err)
	}

	r := reconcile.NewClusterReconciler(clusters, providers, reconcile.TCPHealthChecker{})
	ref := graph.ResourceRef{Kind: resource.KindCluster, ID: cluster.Metadata.ID}

	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatalf("Reconcile() (first) = %v", err)
	}
	afterFirst, err := clusters.Get(context.Background(), cluster.Metadata.ID)
	if err != nil {
		t.Fatalf("Get() after first reconcile = %v", err)
	}

	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatalf("Reconcile() (second) = %v", err)
	}
	afterSecond, err := clusters.Get(context.Background(), cluster.Metadata.ID)
	if err != nil {
		t.Fatalf("Get() after second reconcile = %v", err)
	}

	if afterSecond.Metadata.ResourceVersion != afterFirst.Metadata.ResourceVersion {
		t.Errorf("ResourceVersion after second no-drift reconcile = %d, want unchanged %d",
			afterSecond.Metadata.ResourceVersion, afterFirst.Metadata.ResourceVersion)
	}
}

func TestClusterReconciler_DeletedClusterSinceEvent_IsNotAnError(t *testing.T) {
	clusters, providers := newClusterTestStores(t)
	r := reconcile.NewClusterReconciler(clusters, providers, reconcile.TCPHealthChecker{})

	err := r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindCluster, ID: "does-not-exist"})
	if err != nil {
		t.Errorf("Reconcile(nonexistent cluster) = %v, want nil (deleted since the event fired is not an error)", err)
	}
	if errors.Is(err, store.ErrNotFound) {
		t.Error("Reconcile() should absorb ErrNotFound, not propagate it")
	}
}
