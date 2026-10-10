package reconcile

import (
	"context"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// StorageProvider is the capability-shaped interface a VolumeReconciler
// drives -- capability-shaped interfaces live alongside their
// kind-shaped Reconciler (core/reconcile/CLAUDE.md), same as
// HealthChecker living next to ClusterReconciler. Uses only
// core/resource types -- no Kubernetes-specific concept (PersistentVolumeClaim,
// StorageClass, ...) appears here; that knowledge belongs entirely to
// whichever providers/ package implements this interface, keeping the
// interface itself genuinely swappable (e.g. for a future replicated
// storage provider at 3+ nodes, ADR 0002).
type StorageProvider interface {
	// Ensure makes vol's desired storage exist (creating it if
	// necessary) and reports whether it's currently bound/ready.
	// reason/message are suitable for the Volume's "Bound" Condition
	// (Reason/Message fields respectively) -- same error-as-detail
	// shape HealthChecker already established.
	Ensure(ctx context.Context, vol resource.Volume) (bound bool, reason, message string, err error)
	// Delete removes vol's backing storage. A no-op, not an error, if
	// it doesn't exist (already deleted, or never successfully
	// provisioned).
	Delete(ctx context.Context, vol resource.Volume) error
}
