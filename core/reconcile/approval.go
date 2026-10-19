package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// DeleteFunc deletes a resource of any kind through its store; the
// context carries the approval (store.WithApproval).
type DeleteFunc func(ctx context.Context, kind resource.Kind, id string, cascade bool) error

// CleanupFunc cleans up after one resource an executed approval removed,
// from what the approval recorded (a Secret's value, at its path). It
// must be idempotent: a retry after a crash runs it again.
type CleanupFunc func(ctx context.Context, ref resource.ApprovalRef) error

// ApprovalReconciler carries approvals through to the end (spec 019 plan.md
// decision 7): it expires overdue requests, executes approved ones through
// the store's delete (which refuses a target that changed since it was
// reviewed), cleans up after what they removed, and verifies the result.
// Deciding is never its job: that's a person's, or policy's, through the
// API.
type ApprovalReconciler struct {
	approvals store.Store[resource.ApprovalSpec, resource.ApprovalStatus]
	delete    DeleteFunc
	// cleaners are the kinds that need clean-up after a delete. A kind
	// listed here is never verified until its clean-up has succeeded.
	cleaners map[resource.Kind]CleanupFunc
	exists   func(ctx context.Context, kind resource.Kind, id string) (bool, error)
	now      func() time.Time
}

func NewApprovalReconciler(
	approvals store.Store[resource.ApprovalSpec, resource.ApprovalStatus],
	del DeleteFunc,
	cleaners map[resource.Kind]CleanupFunc,
	exists func(ctx context.Context, kind resource.Kind, id string) (bool, error),
	now func() time.Time,
) *ApprovalReconciler {
	return &ApprovalReconciler{approvals: approvals, delete: del, cleaners: cleaners, exists: exists, now: now}
}

var _ Reconciler = (*ApprovalReconciler)(nil)

// Condition reasons an approval's conditions carry beyond the Decided ones
// the API sets.
const (
	ReasonStaleTarget    = "StaleTarget"
	ReasonDeleteFailed   = "DeleteFailed"
	ReasonCleanupPending = "Pending"
	ReasonCleanupDone    = "Done"
	ReasonNothingToClean = "NothingToClean"
)

func (r *ApprovalReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	a, err := r.approvals.Get(ctx, ref.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	switch a.Status.Phase {
	case resource.ApprovalPending:
		return r.expire(ctx, a)
	case resource.ApprovalApproved:
		return r.execute(ctx, a)
	case resource.ApprovalExecuted:
		return r.finish(ctx, a)
	default:
		// Rejected, Expired, Failed: nothing will happen to it again.
		return nil
	}
}

// expire moves an overdue pending approval to Expired, or asks to be
// checked again when it falls due.
func (r *ApprovalReconciler) expire(ctx context.Context, a resource.Approval) error {
	if a.Status.ExpiresAt.IsZero() {
		return nil
	}
	now := r.now().UTC()
	if left := a.Status.ExpiresAt.Sub(now); left > 0 {
		return RequeueAfter{Delay: left}
	}
	if err := resource.ApprovalTransition(a.Status.Phase, resource.ApprovalExpired); err != nil {
		return err
	}
	a.Status.Phase = resource.ApprovalExpired
	a.Status.Settled = true
	a.Status.Conditions = upsertCondition(a.Status.Conditions, resource.Condition{
		Type: resource.ApprovalConditionDecided, Status: resource.ConditionTrue, Reason: "Expired",
		Message: "nobody decided before " + a.Status.ExpiresAt.Format(time.RFC3339), LastTransitionTime: now,
	})
	_, err := r.approvals.Update(ctx, a)
	return err
}

// execute runs an approved action. The store checks, in the delete's own
// transaction, that the target and what a cascade removes are exactly what
// was reviewed, and moves the approval to Executed with it.
func (r *ApprovalReconciler) execute(ctx context.Context, a resource.Approval) error {
	act := a.Spec.Action
	if act.Verb != resource.ApprovalVerbDelete {
		return r.fail(ctx, a, ReasonDeleteFailed, fmt.Sprintf("%q can't be executed", act.Verb))
	}
	// A second layer under the API's (constitution 5): only policy, or a
	// person or API key who isn't the requester, can have approved it.
	if err := decidedLegitimately(a); err != nil {
		return r.fail(ctx, a, ReasonDeleteFailed, "not executed: "+err.Error())
	}
	radius := make([]resource.Scope, len(a.Status.BlastRadius))
	for i, ref := range a.Status.BlastRadius {
		radius[i] = ref.Scope()
	}
	intent := store.ApprovalIntent{Approval: a, Existing: true, ExpectedVersion: a.Status.TargetVersion, ExpectedRadius: radius}
	// Only the delete carries the approval as its decision; the approval's
	// own status writes below are the reconciler's.
	delCtx := store.WithApproval(audit.WithPolicyDecision(ctx, executionDecision(a)), intent)

	err := r.delete(delCtx, act.Target.Kind, act.Target.ID, act.Cascade)
	switch {
	case err == nil:
		// The store moved it to Executed; that update's event brings it back
		// here for clean-up.
		return nil
	case errors.Is(err, store.ErrStale), errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrHasChildren):
		return r.fail(ctx, a, ReasonStaleTarget, "the target changed since the request, so nothing was deleted: "+err.Error())
	case errors.Is(err, store.ErrNoApproval):
		// The record can't authorise the delete (no reviewed version or
		// radius): retrying would never succeed.
		return r.fail(ctx, a, ReasonDeleteFailed, "not executed: "+err.Error())
	default:
		// Transient (or a conflicting write to the approval itself): the
		// engine retries, and the store re-checks everything then. Say so
		// on the approval, once per distinct error.
		log.Printf("reconcile: approval %s: delete of %s failed, will retry: %v", a.Metadata.ID, act.Target, err)
		msg := "the delete failed and will be retried"
		if prev := findCondition(a.Status.Conditions, resource.ApprovalConditionExecuted); prev == nil || prev.Message != msg {
			a.Status.Conditions = upsertCondition(a.Status.Conditions, resource.Condition{
				Type: resource.ApprovalConditionExecuted, Status: resource.ConditionFalse, Reason: ReasonDeleteFailed,
				Message: msg, LastTransitionTime: r.now().UTC(),
			})
			if _, uerr := r.approvals.Update(ctx, a); uerr != nil {
				return errors.Join(err, uerr)
			}
		}
		return err
	}
}

// decidedLegitimately checks who approved a: policy, or a person or API key
// other than the requester (policy.MayDecide), as the API enforces.
func decidedLegitimately(a resource.Approval) error {
	d := a.Status.Decision
	switch {
	case d == nil:
		return errors.New("the approval records no decision")
	case d.ByPolicy:
		return nil
	case d.By == nil || d.By.Principal == nil:
		return errors.New("the approval records no approver")
	}
	return policy.MayDecide(policy.Principal{Type: d.By.Principal.Type, ID: d.By.Principal.ID}, a.Status.RequestedBy.Principal)
}

// executionDecision is what the delete's audit rows record as the decision
// that allowed it: the approval, who approved it, and who asked.
func executionDecision(a resource.Approval) string {
	by := "policy"
	if d := a.Status.Decision; d != nil && !d.ByPolicy && d.By != nil {
		by = d.By.Actor
	}
	return fmt.Sprintf("approved: %s by %s; requested by %s", a.Metadata.ID, by, a.Status.RequestedBy.Actor)
}

// fail ends an approved action that can't be executed. Nothing was
// deleted.
func (r *ApprovalReconciler) fail(ctx context.Context, a resource.Approval, reason, message string) error {
	if err := resource.ApprovalTransition(a.Status.Phase, resource.ApprovalFailed); err != nil {
		return err
	}
	a.Status.Phase = resource.ApprovalFailed
	a.Status.Settled = true
	a.Status.Conditions = upsertCondition(a.Status.Conditions, resource.Condition{
		Type: resource.ApprovalConditionExecuted, Status: resource.ConditionFalse, Reason: reason,
		Message: message, LastTransitionTime: r.now().UTC(),
	})
	_, err := r.approvals.Update(ctx, a)
	return err
}

// finish cleans up after an executed action, one removed resource at a
// time, saving progress after each, then verifies that everything it
// removed is gone. An approval is Verified only when both are done.
func (r *ApprovalReconciler) finish(ctx context.Context, a resource.Approval) error {
	if c := findCondition(a.Status.Conditions, resource.ApprovalConditionVerified); c != nil && c.Status == resource.ConditionTrue {
		return nil
	}
	for i := range a.Status.BlastRadius {
		ref := a.Status.BlastRadius[i]
		clean, ok := r.cleaners[ref.Kind]
		if !ok || ref.CleanedUp {
			continue
		}
		if err := clean(ctx, ref); err != nil {
			return r.cleanupPending(ctx, a, err)
		}
		a.Status.BlastRadius[i].CleanedUp = true
		updated, err := r.approvals.Update(ctx, a)
		if err != nil {
			return err
		}
		a = updated
	}

	now := r.now().UTC()
	reason, message := ReasonNothingToClean, "nothing it removed needs cleaning up"
	if r.needsCleanup(a) {
		reason, message = ReasonCleanupDone, "everything it removed is cleaned up"
	}
	prevCleaned := findCondition(a.Status.Conditions, resource.ApprovalConditionCleanedUp)
	cleanedRecorded := prevCleaned != nil && prevCleaned.Status == resource.ConditionTrue && prevCleaned.Reason == reason
	a.Status.Conditions = upsertCondition(a.Status.Conditions, resource.Condition{
		Type: resource.ApprovalConditionCleanedUp, Status: resource.ConditionTrue, Reason: reason, Message: message, LastTransitionTime: now,
	})
	var left []string
	for _, ref := range a.Status.BlastRadius {
		ok, err := r.exists(ctx, ref.Kind, ref.ID)
		if err != nil {
			return err
		}
		if ok {
			left = append(left, ref.Scope().String())
		}
	}
	verified := resource.Condition{Type: resource.ApprovalConditionVerified, Status: resource.ConditionTrue, Reason: "Gone",
		Message: fmt.Sprintf("all %d removed resource(s) are gone", len(a.Status.BlastRadius)), LastTransitionTime: now}
	if len(left) > 0 {
		verified.Status, verified.Reason, verified.Message = resource.ConditionFalse, "StillPresent", fmt.Sprintf("still present: %v", left)
	}
	// No write when nothing changed since the last check
	// (core/reconcile/CLAUDE.md).
	prevVerified := findCondition(a.Status.Conditions, resource.ApprovalConditionVerified)
	if !(prevVerified != nil && prevVerified.Status == verified.Status && prevVerified.Message == verified.Message && cleanedRecorded) {
		a.Status.Conditions = upsertCondition(a.Status.Conditions, verified)
		// Verified ends it: nothing more will happen, so resync stops
		// revisiting it.
		a.Status.Settled = len(left) == 0
		if _, err := r.approvals.Update(ctx, a); err != nil {
			return err
		}
	}
	// Something removed is still there (it shouldn't be): look again soon
	// rather than only at the next resync.
	return requeueWhen(nil, len(left) > 0)
}

// cleanupPending records why clean-up hasn't finished, once per distinct
// reason, and asks to be retried: the erase is retried until it succeeds.
func (r *ApprovalReconciler) cleanupPending(ctx context.Context, a resource.Approval, cause error) error {
	left := 0
	for _, ref := range a.Status.BlastRadius {
		if _, ok := r.cleaners[ref.Kind]; ok && !ref.CleanedUp {
			left++
		}
	}
	// The cause is logged, never stored: a provider's error text doesn't
	// belong in what the API returns.
	reason, message := ReasonCleanupPending, fmt.Sprintf("%d left to clean up; retrying", left)
	notReady := errors.Is(cause, ErrProviderNotReady)
	if notReady {
		reason, message = ReasonProviderNotReady, fmt.Sprintf("%d left to clean up; waiting for its capability to be ready", left)
	} else {
		log.Printf("reconcile: approval %s: clean-up failed, will retry: %v", a.Metadata.ID, cause)
	}
	prev := findCondition(a.Status.Conditions, resource.ApprovalConditionCleanedUp)
	if prev == nil || prev.Status != resource.ConditionFalse || prev.Reason != reason || prev.Message != message {
		a.Status.Conditions = upsertCondition(a.Status.Conditions, resource.Condition{
			Type: resource.ApprovalConditionCleanedUp, Status: resource.ConditionFalse, Reason: reason, Message: message, LastTransitionTime: r.now().UTC(),
		})
		if _, err := r.approvals.Update(ctx, a); err != nil {
			return err
		}
	}
	if notReady {
		return RequeueAfter{}
	}
	return cause
}

// needsCleanup reports whether anything a removed has a clean-up.
func (r *ApprovalReconciler) needsCleanup(a resource.Approval) bool {
	for _, ref := range a.Status.BlastRadius {
		if _, ok := r.cleaners[ref.Kind]; ok {
			return true
		}
	}
	return false
}
