package reconcile

import (
	"context"
	"errors"
	"time"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// conditionHealthy is the one Condition.Type this spec's ClusterReconciler
// sets.
const conditionHealthy = "Healthy"

// ClusterReconciler resolves a Cluster's Provider and runs the Provider's
// HealthCheck contract against it, writing a "Healthy" Condition back.
// The one concrete Reconciler this spec ships (specs/003-reconciliation-
// engine/spec.md Scope-IN) -- observe-and-verify only, no apply/provision
// side, since there is nothing yet to provision (no real provider
// exists).
type ClusterReconciler struct {
	clusters  store.Store[resource.ClusterSpec, resource.ClusterStatus]
	providers store.Store[resource.ProviderSpec, resource.ProviderStatus]
	checker   HealthChecker
}

func NewClusterReconciler(
	clusters store.Store[resource.ClusterSpec, resource.ClusterStatus],
	providers store.Store[resource.ProviderSpec, resource.ProviderStatus],
	checker HealthChecker,
) *ClusterReconciler {
	return &ClusterReconciler{clusters: clusters, providers: providers, checker: checker}
}

var _ Reconciler = (*ClusterReconciler)(nil)

func (r *ClusterReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	cluster, err := r.clusters.Get(ctx, ref.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // deleted since the event/resync fired; nothing to do
	}
	if err != nil {
		return err
	}

	if cluster.Spec.ProviderID == "" {
		return r.setCondition(ctx, cluster, resource.ConditionUnknown, "NoProviderConfigured", "")
	}

	provider, err := r.providers.Get(ctx, cluster.Spec.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return r.setCondition(ctx, cluster, resource.ConditionUnknown, "ProviderNotFound", "")
	}
	if err != nil {
		return err
	}

	checkErr := r.checker.Check(ctx, provider.Spec.HealthCheck)
	switch {
	case errors.Is(checkErr, ErrNotConfigured):
		return r.setCondition(ctx, cluster, resource.ConditionUnknown, "NoHealthCheckConfigured", "")
	case checkErr != nil:
		return r.setCondition(ctx, cluster, resource.ConditionFalse, "HealthCheckFailed", checkErr.Error())
	default:
		return r.setCondition(ctx, cluster, resource.ConditionTrue, "HealthCheckPassed", "")
	}
}

// setCondition upserts the "Healthy" Condition on cluster and writes it
// back via Update -- but only if the computed {Status, Reason} actually
// differs from what's already stored. This is the idempotency guarantee
// (constitution rule 3, acceptance criterion 3): a no-drift reconcile
// makes zero store calls, so it produces zero audit rows and no
// ResourceVersion bump.
func (r *ClusterReconciler) setCondition(ctx context.Context, cluster resource.Cluster, status resource.ConditionStatus, reason, message string) error {
	if existing := findCondition(cluster.Status.Conditions, conditionHealthy); existing != nil &&
		existing.Status == status && existing.Reason == reason {
		return nil
	}

	cluster.Status.Conditions = upsertCondition(cluster.Status.Conditions, resource.Condition{
		Type:               conditionHealthy,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: time.Now(),
	})

	_, err := r.clusters.Update(ctx, cluster)
	if errors.Is(err, store.ErrConflict) {
		// A concurrent human/API edit raced this reconcile pass. No
		// retry here -- the next resync tick (or the racing edit's own
		// event, if it published one) reconciles again from fresh state,
		// same "level-triggered, eventually consistent" reasoning as a
		// dropped event.
		return err
	}
	return err
}

func findCondition(conditions []resource.Condition, condType string) *resource.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

// upsertCondition replaces the existing Condition of the same Type, or
// appends if none exists.
func upsertCondition(conditions []resource.Condition, c resource.Condition) []resource.Condition {
	for i := range conditions {
		if conditions[i].Type == c.Type {
			conditions[i] = c
			return conditions
		}
	}
	return append(conditions, c)
}
