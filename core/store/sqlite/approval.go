package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/id"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// Approvals in the delete transaction (spec 019 plan.md decisions 2-3).
// Delete refuses without an approval, checks the target is what was
// reviewed, and records the Approval with everything it removed, all in
// the delete's own transaction: a delete never commits without its
// Approval, and an Approval never claims a delete that rolled back.

// removedRow is one row a delete removed: what an Approval's blast radius
// lists, with the spec its kind's clean-up needs once the row is gone.
type removedRow struct {
	ref  graph.ResourceRef
	name string
	spec json.RawMessage
}

// checkIntent verifies the approval names exactly this delete. A
// mismatch is a caller bug (an intent reused for another delete), so it's
// refused rather than recorded against the wrong target.
func checkIntent(in store.ApprovalIntent, kind resource.Kind, resourceID string, cascade bool) error {
	act := in.Approval.Spec.Action
	if act.Verb != resource.ApprovalVerbDelete || act.Target != (resource.Scope{Kind: kind, ID: resourceID}) || act.Cascade != cascade {
		return fmt.Errorf("%w: the approval is for %s %s (cascade %v), not this delete of %s/%s (cascade %v)",
			store.ErrNoApproval, act.Verb, act.Target, act.Cascade, kind, resourceID, cascade)
	}
	if err := resource.ApprovalTransition(in.Approval.Status.Phase, resource.ApprovalExecuted); err != nil {
		return fmt.Errorf("%w: %v", store.ErrNoApproval, err)
	}
	// A stored approval was reviewed by a person: executing it without the
	// reviewed version and blast radius would skip the stale-target check,
	// so it's refused rather than run unchecked.
	if in.Existing && (in.ExpectedVersion == 0 || in.ExpectedRadius == nil) {
		return fmt.Errorf("%w: a stored approval must carry the reviewed version and blast radius", store.ErrNoApproval)
	}
	return nil
}

// checkReviewed returns store.ErrStale when the target's version, or the
// set of resources the delete would remove, differs from what was
// reviewed. It runs before anything is removed.
func checkReviewed(ctx context.Context, tx *sql.Tx, in store.ApprovalIntent, ref graph.ResourceRef, version int64) error {
	if in.ExpectedVersion != 0 && version != in.ExpectedVersion {
		return fmt.Errorf("%w: %s %s is at version %d, the approval reviewed %d", store.ErrStale, ref.Kind, ref.ID, version, in.ExpectedVersion)
	}
	if in.ExpectedRadius == nil {
		return nil
	}
	below, err := graph.Descendants(ctx, tx, ref)
	if err != nil {
		return err
	}
	actual := map[resource.Scope]bool{{Kind: ref.Kind, ID: ref.ID}: true}
	for _, d := range below {
		actual[resource.Scope{Kind: d.Kind, ID: d.ID}] = true
	}
	expected := make(map[resource.Scope]bool, len(in.ExpectedRadius))
	for _, s := range in.ExpectedRadius {
		expected[s] = true
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("%w: the delete would remove %d resources, the approval reviewed %d", store.ErrStale, len(actual), len(expected))
	}
	for s := range actual {
		if !expected[s] {
			return fmt.Errorf("%w: %s wasn't in the reviewed blast radius", store.ErrStale, s)
		}
	}
	return nil
}

// recordApproval inserts the intent's Approval, or updates the stored one,
// as Executed with the rows actually removed (the target first). It
// returns the event to publish after commit.
func recordApproval(ctx context.Context, tx *sql.Tx, in store.ApprovalIntent, target resource.Metadata, owners []graph.ResourceRef, removed []removedRow, now time.Time) (event.Event, error) {
	a := in.Approval
	var before *resource.Approval
	if in.Existing {
		stored, err := scanResource[resource.ApprovalSpec, resource.ApprovalStatus](tx.QueryRowContext(ctx, `
			SELECT id, name, labels, owner_refs, relationships, spec, status,
			       resource_version, created_at, updated_at
			FROM resources WHERE id = ? AND kind = ?`, a.Metadata.ID, string(resource.KindApproval)), resource.KindApproval)
		if err != nil {
			return event.Event{}, fmt.Errorf("sqlite: approval %s: %w", a.Metadata.ID, err)
		}
		if stored.Metadata.ResourceVersion != a.Metadata.ResourceVersion || stored.Status.Phase != a.Status.Phase {
			return event.Event{}, store.ErrConflict
		}
		before = &stored
	} else {
		if a.Metadata.ID == "" {
			a.Metadata.ID = id.New()
		}
		if a.Metadata.Name == "" {
			a.Metadata.Name = fmt.Sprintf("delete %s/%s", target.Kind, target.ID)
		}
		a.Metadata.CreatedAt = now
		a.Metadata.ResourceVersion = 1
		if a.Status.RequestedAt.IsZero() {
			a.Status.RequestedAt = now
		}
		if a.Status.TargetName == "" {
			a.Status.TargetName = target.Name
		}
		if a.Status.TargetVersion == 0 {
			a.Status.TargetVersion = target.ResourceVersion
		}
	}
	if len(a.Status.OwnerChain) == 0 {
		for _, o := range owners {
			a.Status.OwnerChain = append(a.Status.OwnerChain, resource.Scope{Kind: o.Kind, ID: o.ID})
		}
	}
	a.Metadata.Kind = resource.KindApproval
	a.Metadata.OwnerRefs, a.Metadata.Relationships = nil, nil
	a.Metadata.UpdatedAt = now
	a.Status.Phase = resource.ApprovalExecuted
	a.Status.ExpiresAt = time.Time{}
	a.Status.ExecutedAt = now
	a.Status.BlastRadius = make([]resource.ApprovalRef, 0, len(removed))
	for _, r := range removed {
		a.Status.BlastRadius = append(a.Status.BlastRadius, resource.ApprovalRef{Kind: r.ref.Kind, ID: r.ref.ID, Name: r.name, LastSpec: r.spec})
	}
	setCondition(&a.Status.Status, resource.Condition{
		Type: resource.ApprovalConditionExecuted, Status: resource.ConditionTrue, Reason: "Deleted",
		Message: fmt.Sprintf("removed %d resource(s)", len(removed)), LastTransitionTime: now,
	})
	if err := resource.Validate(a.Metadata); err != nil {
		return event.Event{}, err
	}
	cols, err := marshalColumns(a)
	if err != nil {
		return event.Event{}, err
	}

	ev := event.Event{Kind: resource.KindApproval, ID: a.Metadata.ID, Actor: audit.ActorFrom(ctx), At: now}
	var beforeJSON json.RawMessage
	if before != nil {
		a.Metadata.CreatedAt = before.Metadata.CreatedAt
		a.Metadata.ResourceVersion = before.Metadata.ResourceVersion + 1
		if beforeJSON, err = json.Marshal(before); err != nil {
			return event.Event{}, fmt.Errorf("sqlite: marshal audit before: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE resources
			SET name = ?, spec = ?, status = ?, resource_version = ?, updated_at = ?
			WHERE id = ? AND kind = ? AND resource_version = ?`,
			a.Metadata.Name, cols.spec, cols.status, a.Metadata.ResourceVersion, now.Format(timeLayout),
			a.Metadata.ID, string(resource.KindApproval), before.Metadata.ResourceVersion); err != nil {
			return event.Event{}, fmt.Errorf("sqlite: update approval %s: %w", a.Metadata.ID, err)
		}
		ev.Type = event.EventUpdated
	} else {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO resources
				(id, kind, name, labels, owner_refs, relationships, spec, status,
				 resource_version, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.Metadata.ID, string(resource.KindApproval), a.Metadata.Name,
			cols.labels, cols.ownerRefs, cols.relationships, cols.spec, cols.status,
			a.Metadata.ResourceVersion, now.Format(timeLayout), now.Format(timeLayout)); err != nil {
			return event.Event{}, fmt.Errorf("sqlite: create approval %s: %w", a.Metadata.ID, err)
		}
		ev.Type = event.EventCreated
	}

	afterJSON, err := json.Marshal(a)
	if err != nil {
		return event.Event{}, fmt.Errorf("sqlite: marshal audit after: %w", err)
	}
	if err := writeAudit(ctx, tx, audit.Record{
		ID:             id.New(),
		Actor:          ev.Actor,
		Interface:      audit.InterfaceFrom(ctx),
		Kind:           string(resource.KindApproval),
		ResourceID:     a.Metadata.ID,
		Before:         beforeJSON,
		After:          afterJSON,
		At:             now,
		RequestID:      audit.RequestIDFrom(ctx),
		ActorSubject:   audit.SubjectFrom(ctx),
		ActorSponsor:   audit.SponsorFrom(ctx),
		PolicyDecision: audit.PolicyDecisionFrom(ctx),
	}); err != nil {
		return event.Event{}, err
	}
	return ev, nil
}

// setCondition replaces the condition of c's type, or appends it.
func setCondition(st *resource.Status, c resource.Condition) {
	for i := range st.Conditions {
		if st.Conditions[i].Type == c.Type {
			st.Conditions[i] = c
			return
		}
	}
	st.Conditions = append(st.Conditions, c)
}
