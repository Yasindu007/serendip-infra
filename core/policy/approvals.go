package policy

import (
	"fmt"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// Who may decide an approval (spec 019 plan.md decision 8). Authority comes
// from roles: the approver must be allowed the action itself, which the
// caller checks with Decide at the target, as for the action. MayDecide adds
// what roles can't express.

var (
	// ErrApproverGuardrail refuses a decider that isn't a person or an API
	// key: an agent, or anything server-side, never approves (constitution
	// 5).
	ErrApproverGuardrail = fmt.Errorf("%w: only a person or an API key may approve or reject", ErrForbidden)
	// ErrSelfApproval refuses the requester deciding their own request
	// (separation of duties).
	ErrSelfApproval = fmt.Errorf("%w: separation of duties: the requester can't decide their own request", ErrForbidden)
)

// MayDecide reports whether approver may approve or reject a request made
// by requester (nil when a server-side actor made it). An agent's sponsor
// may decide its agent's request.
func MayDecide(approver Principal, requester *resource.PrincipalRef) error {
	switch approver.Type {
	case resource.PrincipalPerson, resource.PrincipalAPIKey:
	default:
		return ErrApproverGuardrail
	}
	if approver.ID == "" || approver.Via != "" {
		return ErrApproverGuardrail
	}
	// A person may decide what their AI assistant asked for (Via set), as a
	// sponsor decides their agent's request.
	if requester != nil && requester.Type == approver.Type && requester.ID == approver.ID && requester.Via == "" {
		return ErrSelfApproval
	}
	return nil
}
