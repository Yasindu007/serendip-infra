package api

import (
	"context"
	"net/http"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/id"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// approvalHeader names the Approval a successful DELETE recorded.
const approvalHeader = "Serendip-Approval-Id"

// policyApproval is the Approval for a delete the caller's roles allowed:
// requested by the caller and approved by policy, with the decision that
// allowed it as evidence (spec 019 plan.md decision 4). The store records
// it, as Executed, in the delete's own transaction.
func policyApproval(ctx context.Context, caller policy.Principal, ru rule, resourceID string, cascade bool, evidence string) store.ApprovalIntent {
	// Only an ordinary resource can cascade; access-management deletes
	// never do, whatever the query says.
	cascade = cascade && ru.target == targetResource
	return store.ApprovalIntent{Approval: resource.Approval{
		Metadata: resource.Metadata{ID: id.New()},
		Spec: resource.ApprovalSpec{Action: resource.ApprovalAction{
			Verb: resource.ApprovalVerbDelete, Target: resource.Scope{Kind: ru.kind, ID: resourceID}, Cascade: cascade,
		}},
		Status: resource.ApprovalStatus{
			Phase:           resource.ApprovalApproved,
			RequestedBy:     requesterOf(ctx, caller),
			RequestDecision: evidence,
			Decision:        &resource.ApprovalDecision{ByPolicy: true, Evidence: evidence},
		},
	}}
}

// requesterOf is the caller as an Approval records them.
func requesterOf(ctx context.Context, caller policy.Principal) resource.ApprovalActor {
	return resource.ApprovalActor{
		Actor:     audit.ActorFrom(ctx),
		Principal: &resource.PrincipalRef{Type: caller.Type, ID: caller.ID, Via: caller.Via},
		Subject:   audit.SubjectFrom(ctx),
		Sponsor:   audit.SponsorFrom(ctx),
	}
}

// approvalTargetKind is the kind a refused request names in its
// needs-approval detail.
func approvalTargetKind(ru rule, target resource.Scope) resource.Kind {
	if ru.kind != "" {
		return ru.kind
	}
	return target.Kind
}

// approvalHeaderWriter adds the Approval's ID to a successful answer
// only: a failed delete rolled its Approval back with it.
type approvalHeaderWriter struct {
	http.ResponseWriter
	id      string
	written bool
}

func (w *approvalHeaderWriter) WriteHeader(status int) {
	if !w.written && status >= 200 && status < 300 {
		w.Header().Set(approvalHeader, w.id)
	}
	w.written = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *approvalHeaderWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *approvalHeaderWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// configApproval is the Approval for start-up's removal of an assignment
// held by an API key that is no longer configured: requested and decided
// by the server itself, following its configuration (spec 019 plan.md
// decision 5).
func configApproval(ctx context.Context, assignmentID, key string) store.ApprovalIntent {
	reason := "API key " + key + " removed from configuration"
	return store.ApprovalIntent{Approval: resource.Approval{
		Spec: resource.ApprovalSpec{
			Action: resource.ApprovalAction{Verb: resource.ApprovalVerbDelete, Target: resource.Scope{Kind: resource.KindRoleAssignment, ID: assignmentID}},
			Reason: reason,
		},
		Status: resource.ApprovalStatus{
			Phase:           resource.ApprovalApproved,
			RequestedBy:     resource.ApprovalActor{Actor: audit.ActorFrom(ctx)},
			RequestDecision: "configuration",
			Decision:        &resource.ApprovalDecision{ByPolicy: true, Evidence: "configuration: " + reason},
		},
	}}
}
