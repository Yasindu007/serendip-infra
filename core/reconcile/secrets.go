package reconcile

import (
	"context"
	"errors"
	"time"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// conditionReachable is the one Condition.Type SecretReconciler sets.
// Named "Reachable", not "Synced" -- there is no desired value in
// SecretSpec to compare against (resource.SecretSpec's own doc
// comment), only existence to observe. A "Synced" name would wrongly
// imply a value-matching guarantee this Reconciler cannot make
// (constitution rule 6, honest capability reporting).
const conditionReachable = "Reachable"

// SecretsProvider is the capability-shaped interface SecretReconciler
// drives -- capability-shaped interfaces live alongside their
// kind-shaped Reconciler (core/reconcile/CLAUDE.md), same as
// HealthChecker/StorageProvider living next to their own Reconcilers.
// Uses only core/resource types -- no OpenBao/Vault-specific concept
// (a "mount", a Shamir key share, ...) appears here; that knowledge
// belongs entirely to whichever providers/ package implements this
// interface.
//
// Exists, not Ensure: unlike StorageProvider.Ensure (which provisions a
// PV/PVC that didn't exist), there is nothing analogous to provision
// here -- the KV v2 mount this provider writes into already exists,
// set up once by spec 008's Bootstrap. All a reconcile loop can
// honestly do is observe whether a value currently exists at a
// Secret's path and report that (constitution rule 6) -- not silently
// imply a repair action that doesn't exist.
//
// WriteValue/ReadValue/Delete are called synchronously by
// SecretReconciler's own WriteValue/ReadValue/DeleteWithValue methods
// (below), driven directly by api/, not by Reconcile or the engine's
// resync loop -- see those methods' doc comments for why.
type SecretsProvider interface {
	// Exists reports whether a value currently exists at sec's Path.
	Exists(ctx context.Context, sec resource.Secret) (bool, error)
	// WriteValue writes value at sec's Path, replacing any existing
	// value there.
	WriteValue(ctx context.Context, sec resource.Secret, value map[string]any) error
	// ReadValue reads the value currently at sec's Path.
	ReadValue(ctx context.Context, sec resource.Secret) (map[string]any, error)
	// Delete removes the value at sec's Path. A no-op, not an error,
	// if it doesn't exist (already deleted, or never written).
	Delete(ctx context.Context, sec resource.Secret) error
}

// SecretReconciler drives a SecretsProvider to observe whether a
// Secret's value is currently reachable, writing a "Reachable"
// Condition back. The provider is injected as a concrete value, not
// looked up from core/provider.Registry inside Reconcile -- mirrors
// VolumeReconciler/ClusterReconciler's constructor-injected provider
// exactly (the Lookup + type-assert happens once, in cmd/serendipd's
// composition root).
type SecretReconciler struct {
	secrets  store.Store[resource.SecretSpec, resource.SecretStatus]
	provider SecretsProvider
}

func NewSecretReconciler(
	secrets store.Store[resource.SecretSpec, resource.SecretStatus],
	provider SecretsProvider,
) *SecretReconciler {
	return &SecretReconciler{secrets: secrets, provider: provider}
}

var _ Reconciler = (*SecretReconciler)(nil)

// Reconcile's store.ErrNotFound branch is a plain no-op, deliberately
// unlike VolumeReconciler's -- a Secret's Path (unused once the
// Serendip-side record is gone) is the only way to know what to clean
// up in the provider, so that cleanup happens synchronously in api/'s
// Delete handler (via DeleteWithValue, below) before the record
// disappears, not here, after it already has.
func (r *SecretReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	sec, err := r.secrets.Get(ctx, ref.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	exists, err := r.provider.Exists(ctx, sec)
	if err != nil {
		return r.setCondition(ctx, sec, resource.ConditionUnknown, "CheckFailed", err.Error())
	}
	if exists {
		return r.setCondition(ctx, sec, resource.ConditionTrue, "ValueExists", "")
	}
	return r.setCondition(ctx, sec, resource.ConditionFalse, "NoValueWritten", "")
}

// setCondition upserts the "Reachable" Condition on sec and writes it
// back via Update -- but only if the computed {Status, Reason}
// actually differs from what's already stored. Same idempotency
// guarantee as ClusterReconciler/VolumeReconciler's setCondition
// (constitution rule 3): a no-drift reconcile makes zero store calls,
// zero audit rows, no ResourceVersion bump.
func (r *SecretReconciler) setCondition(ctx context.Context, sec resource.Secret, status resource.ConditionStatus, reason, message string) error {
	if existing := findCondition(sec.Status.Conditions, conditionReachable); existing != nil &&
		existing.Status == status && existing.Reason == reason {
		return nil
	}

	sec.Status.Conditions = upsertCondition(sec.Status.Conditions, resource.Condition{
		Type:               conditionReachable,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: time.Now(),
	})

	_, err := r.secrets.Update(ctx, sec)
	if errors.Is(err, store.ErrConflict) {
		// A concurrent human/API edit raced this reconcile pass. No
		// retry here -- the next resync tick reconciles again from
		// fresh state, same reasoning as ClusterReconciler/
		// VolumeReconciler's setCondition.
		return err
	}
	return err
}

// WriteValue writes value through to the provider at id's current
// Path, then persists that fact via an ordinary store.Update -- which
// is also how this write gets audited, with zero core/audit changes:
// Update's Before/After snapshot is the whole Secret (safe, since the
// value never lives in Spec or Status), and every store mutation
// already writes exactly one audit row (core/store/sqlite/store.go).
// Called directly by api/SecretHandler, not by the reconcile engine --
// a human/API-driven write is synchronous by nature (the caller wants
// to know immediately whether it succeeded), not something to defer to
// the next resync tick.
func (r *SecretReconciler) WriteValue(ctx context.Context, id string, value map[string]any) (resource.Secret, error) {
	sec, err := r.secrets.Get(ctx, id)
	if err != nil {
		return resource.Secret{}, err
	}

	if err := r.provider.WriteValue(ctx, sec, value); err != nil {
		return resource.Secret{}, err
	}

	now := time.Now()
	sec.Status.LastSyncedAt = &now
	sec.Status.Conditions = upsertCondition(sec.Status.Conditions, resource.Condition{
		Type:               conditionReachable,
		Status:             resource.ConditionTrue,
		Reason:             "ValueExists",
		LastTransitionTime: now,
	})

	return r.secrets.Update(ctx, sec)
}

// ReadValue reads the value currently at id's Path. No store write --
// reading a secret's value is explicitly not audited in this spec
// (specs/009-secrets-capability/plan.md resolution 5): auditing it the
// same way WriteValue is would bump ResourceVersion on every read,
// breaking ordinary optimistic-concurrency expectations for any caller
// doing read-then-Update on the same Secret's metadata. A correct
// read-audit trail needs a genuinely separate, non-ResourceVersion-
// affecting append-only write path core/audit doesn't have today.
func (r *SecretReconciler) ReadValue(ctx context.Context, id string) (map[string]any, error) {
	sec, err := r.secrets.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return r.provider.ReadValue(ctx, sec)
}

// DeleteWithValue removes id's value from the provider, then deletes
// the Serendip-side record -- in that order, deliberately. If the
// provider delete fails, the record is left in place so a retry has
// something to retry (a store.Delete success with a failed provider
// delete would silently orphan the real value, unreachable through the
// API but still live in the provider). Called directly by
// api/SecretHandler's Delete, not by Reconcile's store.ErrNotFound
// branch -- see that method's doc comment for why.
func (r *SecretReconciler) DeleteWithValue(ctx context.Context, id string, cascade bool) error {
	sec, err := r.secrets.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := r.provider.Delete(ctx, sec); err != nil {
		return err
	}
	return r.secrets.Delete(ctx, id, cascade)
}
