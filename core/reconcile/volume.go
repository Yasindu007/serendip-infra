package reconcile

import (
	"context"
	"errors"
	"time"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// conditionBound is the one Condition.Type this spec's VolumeReconciler
// sets.
const conditionBound = "Bound"

// VolumeReconciler drives a StorageProvider to make a Volume's desired
// storage exist, writing a "Bound" Condition back. The provider is
// injected as a concrete value, not looked up from core/provider.Registry
// inside Reconcile -- the Lookup + type-assert happens once, in
// cmd/serendipd's composition root, mirroring ClusterReconciler's own
// constructor-injected HealthChecker exactly (no runtime registry
// dependency inside core/reconcile at all).
type VolumeReconciler struct {
	volumes  store.Store[resource.VolumeSpec, resource.VolumeStatus]
	provider StorageProvider
}

func NewVolumeReconciler(
	volumes store.Store[resource.VolumeSpec, resource.VolumeStatus],
	provider StorageProvider,
) *VolumeReconciler {
	return &VolumeReconciler{volumes: volumes, provider: provider}
}

var _ Reconciler = (*VolumeReconciler)(nil)

func (r *VolumeReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	vol, err := r.volumes.Get(ctx, ref.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // deleted since the event/resync fired; nothing to do
	}
	if err != nil {
		return err
	}

	bound, reason, message, err := r.provider.Ensure(ctx, vol)
	if err != nil {
		return r.setCondition(ctx, vol, resource.ConditionFalse, "EnsureFailed", err.Error())
	}

	status := resource.ConditionFalse
	if bound {
		status = resource.ConditionTrue
	}
	return r.setCondition(ctx, vol, status, reason, message)
}

// setCondition upserts the "Bound" Condition on vol and writes it back
// via Update -- but only if the computed {Status, Reason} actually
// differs from what's already stored. Same idempotency guarantee as
// ClusterReconciler.setCondition (constitution rule 3, acceptance
// criterion 3): a no-drift reconcile makes zero store calls, zero audit
// rows, no ResourceVersion bump.
func (r *VolumeReconciler) setCondition(ctx context.Context, vol resource.Volume, status resource.ConditionStatus, reason, message string) error {
	if existing := findCondition(vol.Status.Conditions, conditionBound); existing != nil &&
		existing.Status == status && existing.Reason == reason {
		return nil
	}

	vol.Status.Conditions = upsertCondition(vol.Status.Conditions, resource.Condition{
		Type:               conditionBound,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: time.Now(),
	})

	_, err := r.volumes.Update(ctx, vol)
	if errors.Is(err, store.ErrConflict) {
		// A concurrent human/API edit raced this reconcile pass. No
		// retry here -- the next resync tick (or the racing edit's own
		// event, if it published one) reconciles again from fresh state,
		// same reasoning as ClusterReconciler.setCondition.
		return err
	}
	return err
}
