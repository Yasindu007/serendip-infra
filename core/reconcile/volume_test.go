package reconcile_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func newVolumeTestStore(t *testing.T) store.Store[resource.VolumeSpec, resource.VolumeStatus] {
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
	return sqlite.NewStore[resource.VolumeSpec, resource.VolumeStatus](db, resource.KindVolume)
}

// fakeStorageProvider is a test double for reconcile.StorageProvider --
// no real cluster/httptest.Server needed, mirrors cluster_test.go's use
// of a real TCPHealthChecker against real listeners, except here the
// provider itself is fully faked since there's no equivalent
// lightweight real backend to stand up for a storage capability.
type fakeStorageProvider struct {
	ensureBound   bool
	ensureReason  string
	ensureMessage string
	ensureErr     error
	deleteErr     error
	ensureCalls   int
	deleteCalls   int
}

func (f *fakeStorageProvider) Ensure(ctx context.Context, vol resource.Volume) (bool, string, string, error) {
	f.ensureCalls++
	return f.ensureBound, f.ensureReason, f.ensureMessage, f.ensureErr
}

func (f *fakeStorageProvider) Delete(ctx context.Context, vol resource.Volume) error {
	f.deleteCalls++
	return f.deleteErr
}

func TestVolumeReconciler_Cases(t *testing.T) {
	tests := []struct {
		name         string
		provider     *fakeStorageProvider
		wantStatus   resource.ConditionStatus
		wantReason   string
		wantHasError bool
	}{
		{
			name:       "bound",
			provider:   &fakeStorageProvider{ensureBound: true, ensureReason: "Bound"},
			wantStatus: resource.ConditionTrue,
			wantReason: "Bound",
		},
		{
			name:       "pending (not yet bound, no error)",
			provider:   &fakeStorageProvider{ensureBound: false, ensureReason: "Pending"},
			wantStatus: resource.ConditionFalse,
			wantReason: "Pending",
		},
		{
			name:         "ensure fails",
			provider:     &fakeStorageProvider{ensureErr: errors.New("connection refused")},
			wantStatus:   resource.ConditionFalse,
			wantReason:   "EnsureFailed",
			wantHasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			volumes := newVolumeTestStore(t)
			vol, err := volumes.Create(context.Background(), resource.Volume{
				Metadata: resource.Metadata{Name: "vol-under-test"},
				Spec:     resource.VolumeSpec{SizeBytes: 1 << 30},
			})
			if err != nil {
				t.Fatalf("Create(volume) = %v", err)
			}

			r := reconcile.NewVolumeReconciler(volumes, tt.provider)
			ref := graph.ResourceRef{Kind: resource.KindVolume, ID: vol.Metadata.ID}
			if err := r.Reconcile(context.Background(), ref); err != nil {
				t.Fatalf("Reconcile() = %v", err)
			}

			got, err := volumes.Get(context.Background(), vol.Metadata.ID)
			if err != nil {
				t.Fatalf("Get() after reconcile = %v", err)
			}
			if len(got.Status.Conditions) != 1 {
				t.Fatalf("Conditions = %+v, want exactly 1", got.Status.Conditions)
			}
			cond := got.Status.Conditions[0]
			if cond.Type != "Bound" {
				t.Errorf("Condition.Type = %q, want %q", cond.Type, "Bound")
			}
			if cond.Status != tt.wantStatus {
				t.Errorf("Condition.Status = %q, want %q", cond.Status, tt.wantStatus)
			}
			if cond.Reason != tt.wantReason {
				t.Errorf("Condition.Reason = %q, want %q", cond.Reason, tt.wantReason)
			}
			if tt.wantHasError && cond.Message == "" {
				t.Error("Condition.Message is empty, want the Ensure error message")
			}
		})
	}
}

// This is acceptance criterion 3's core mechanism at the unit level: a
// repeated Reconcile call with no drift must make zero additional
// Update calls -- proven here by asserting ResourceVersion is unchanged
// after a second call, not just "no error" (mirrors
// cluster_test.go's TestClusterReconciler_NoDrift_IsIdempotent_NoAdditionalUpdate).
func TestVolumeReconciler_NoDrift_IsIdempotent_NoAdditionalUpdate(t *testing.T) {
	volumes := newVolumeTestStore(t)
	vol, err := volumes.Create(context.Background(), resource.Volume{
		Metadata: resource.Metadata{Name: "vol-under-test"},
		Spec:     resource.VolumeSpec{SizeBytes: 1 << 30},
	})
	if err != nil {
		t.Fatalf("Create(volume) = %v", err)
	}

	provider := &fakeStorageProvider{ensureBound: true, ensureReason: "Bound"}
	r := reconcile.NewVolumeReconciler(volumes, provider)
	ref := graph.ResourceRef{Kind: resource.KindVolume, ID: vol.Metadata.ID}

	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatalf("Reconcile() (first) = %v", err)
	}
	afterFirst, err := volumes.Get(context.Background(), vol.Metadata.ID)
	if err != nil {
		t.Fatalf("Get() after first reconcile = %v", err)
	}

	if err := r.Reconcile(context.Background(), ref); err != nil {
		t.Fatalf("Reconcile() (second) = %v", err)
	}
	afterSecond, err := volumes.Get(context.Background(), vol.Metadata.ID)
	if err != nil {
		t.Fatalf("Get() after second reconcile = %v", err)
	}

	if afterSecond.Metadata.ResourceVersion != afterFirst.Metadata.ResourceVersion {
		t.Errorf("ResourceVersion after second no-drift reconcile = %d, want unchanged %d",
			afterSecond.Metadata.ResourceVersion, afterFirst.Metadata.ResourceVersion)
	}
	if provider.ensureCalls != 2 {
		t.Errorf("provider.Ensure call count = %d, want 2 (both reconciles call Ensure; only the store write is skipped on no-drift)", provider.ensureCalls)
	}
}

// TestVolumeReconciler_DeletedVolumeSinceEvent_CallsProviderDelete is a
// real architectural gap this spec's T008 real-cluster testing
// surfaced: unlike ClusterReconciler (a passive observer with nothing
// to clean up), this Reconciler's provider actually provisions real
// external storage -- "the Serendip-side record is gone" must still
// trigger StorageProvider.Delete on the backing PV/PVC, not silently
// no-op. Confirmed via a real disposable K3s cluster: deleting a
// Volume through the API left the real PVC/PV behind until this fix.
func TestVolumeReconciler_DeletedVolumeSinceEvent_CallsProviderDelete(t *testing.T) {
	volumes := newVolumeTestStore(t)
	provider := &fakeStorageProvider{}
	r := reconcile.NewVolumeReconciler(volumes, provider)

	err := r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindVolume, ID: "does-not-exist"})
	if err != nil {
		t.Errorf("Reconcile(nonexistent volume) = %v, want nil", err)
	}
	if provider.ensureCalls != 0 {
		t.Errorf("provider.Ensure call count = %d, want 0 (should never be called for a missing Volume)", provider.ensureCalls)
	}
	if provider.deleteCalls != 1 {
		t.Errorf("provider.Delete call count = %d, want 1 (cleanup must still happen for a resource gone from the store)", provider.deleteCalls)
	}
}

func TestVolumeReconciler_DeletedVolumeSinceEvent_PropagatesProviderDeleteError(t *testing.T) {
	volumes := newVolumeTestStore(t)
	provider := &fakeStorageProvider{deleteErr: errors.New("connection refused")}
	r := reconcile.NewVolumeReconciler(volumes, provider)

	err := r.Reconcile(context.Background(), graph.ResourceRef{Kind: resource.KindVolume, ID: "does-not-exist"})
	if err == nil {
		t.Error("Reconcile(nonexistent volume) = nil, want the provider's Delete error propagated so the next resync/retry can try again")
	}
}
