// Package storetest helps tests delete through core/store, which refuses a
// delete without an approval (spec 019). Approved builds a real one, the
// same shape the API attaches to a DELETE that policy allowed, so a test
// exercises the production path rather than a bypass.
package storetest

import (
	"context"

	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// Approved returns ctx carrying a policy-decided approval for deleting
// kind/id, with or without cascade. Each delete needs its own: the store
// refuses an approval that names a different delete.
func Approved(ctx context.Context, kind resource.Kind, id string, cascade bool) context.Context {
	return store.WithApproval(ctx, store.ApprovalIntent{Approval: resource.Approval{
		Spec: resource.ApprovalSpec{Action: resource.ApprovalAction{
			Verb: resource.ApprovalVerbDelete, Target: resource.Scope{Kind: kind, ID: id}, Cascade: cascade,
		}},
		Status: resource.ApprovalStatus{
			Phase:    resource.ApprovalApproved,
			Decision: &resource.ApprovalDecision{ByPolicy: true, Evidence: "storetest"},
		},
	}})
}
