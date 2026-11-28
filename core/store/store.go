package store

import (
	"context"
	"errors"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// ErrNotFound is returned by Get/Update/Delete when no resource matches
// the given id.
var ErrNotFound = errors.New("store: resource not found")

// ErrConflict is returned by Update when the caller's ResourceVersion
// doesn't match the currently stored one (optimistic concurrency).
var ErrConflict = errors.New("store: resource version conflict")

// ErrHasChildren is returned by Delete(cascade=false) when the resource
// has "owns" children (docs/architecture/resource-model.md
// "Relationships"). Retry with cascade=true to delete them too.
var ErrHasChildren = errors.New("store: resource has children; delete rejected without cascade")

// ErrInUse is returned by Delete when the resource, or one a cascade
// would remove, is of a kind that refuses deletion while used
// (resource.RefusesDeleteWhileUsed) and something outside the delete
// still depends on it: a Secret or SMTP server a channel names, or a
// channel a rule names (specs/024-alerts plan decision 1).
var ErrInUse = errors.New("store: resource is in use")

// ErrDuplicate is returned by Create when a resource of the same kind
// already holds the new one's unique key (specs/022-builds plan decision
// 9): the store's guarantee, behind any check a caller made first.
var ErrDuplicate = errors.New("store: a resource with the same unique key already exists")

// ListOptions filters Store.List. Label matching is exact-match only in
// spec 001 -- full Kubernetes-style set-based selectors (in/notin/exists)
// are deferred until a real consumer needs them
// (specs/001-resource-model/plan.md open-question resolution).
type ListOptions struct {
	Kind    resource.Kind
	OwnerID string
	Labels  map[string]string
}

// Store is the generic persistence interface every resource kind uses,
// instantiated per kind (e.g. Store[resource.OrganizationSpec,
// resource.OrganizationStatus]). See core/store/sqlite for the default
// implementation (ADR 0003) and specs/001-resource-model/plan.md for why
// audit and graph-edge sync happen inside the same transaction as every
// mutation here, rather than as an optional wrapper.
type Store[S, St any] interface {
	Get(ctx context.Context, id string) (resource.Resource[S, St], error)
	List(ctx context.Context, opts ListOptions) ([]resource.Resource[S, St], error)
	Create(ctx context.Context, r resource.Resource[S, St]) (resource.Resource[S, St], error)
	// Update requires r.Metadata.ResourceVersion to match the currently
	// stored value; mismatch returns ErrConflict.
	Update(ctx context.Context, r resource.Resource[S, St]) (resource.Resource[S, St], error)
	// Delete rejects removing a resource that has "owns" children unless
	// cascade is true (docs/architecture/resource-model.md "Relationships").
	// The context must carry an approval (WithApproval) naming this delete;
	// without one Delete returns ErrNoApproval and changes nothing.
	Delete(ctx context.Context, id string, cascade bool) error
}

// ErrNoApproval is returned by Delete when the context carries no
// approval (spec 019, constitution rule 5): no delete happens without one
// on the record. It fails closed, so a delete path that forgets to attach
// one is refused rather than deleting unrecorded.
var ErrNoApproval = errors.New("store: delete refused: no approval")

// ErrStale is returned by Delete when the target, or what a cascade would
// remove, has changed since the approval was requested. Nothing is
// deleted: nobody deletes what nobody reviewed.
var ErrStale = errors.New("store: target changed since the approval was requested")

// ApprovalIntent is the approval a Delete records, carried on the context
// the way audit.WithPolicyDecision carries the decision: every delete path
// already passes its request context down. Delete writes or completes the
// Approval in the same transaction as the delete.
type ApprovalIntent struct {
	// Approval is the record. Its phase is Approved, and its action names
	// exactly the delete being made (kind, ID, cascade). Delete moves it
	// to Executed and fills in the blast radius it actually removed.
	Approval resource.Approval
	// Existing is true when Approval is already stored (the approval
	// reconciler executing an approved request); Delete then updates it,
	// and its ResourceVersion must still match. Otherwise Delete inserts
	// it (a DELETE approved by policy).
	Existing bool
	// ExpectedVersion is the target's resourceVersion as reviewed. Zero
	// skips the check, which only a new (policy-approved) Approval may do.
	ExpectedVersion int64
	// ExpectedRadius is what the delete was reviewed to remove, the
	// target included. Nil skips the check, which only a new
	// (policy-approved) Approval may do; with Existing, both are required.
	ExpectedRadius []resource.Scope
}

type approvalKey struct{}

// WithApproval returns a context carrying the approval the next Delete
// records.
func WithApproval(ctx context.Context, in ApprovalIntent) context.Context {
	return context.WithValue(ctx, approvalKey{}, in)
}

// ApprovalFrom returns the approval set by WithApproval.
func ApprovalFrom(ctx context.Context) (ApprovalIntent, bool) {
	in, ok := ctx.Value(approvalKey{}).(ApprovalIntent)
	return in, ok
}

// Tx is an open transaction that spans resource kinds (specs/027 plan
// decision 8). It is opaque: only the implementation that opened it can
// write through it, and a TxCreator handed another implementation's Tx
// refuses it.
type Tx interface {
	// StoreTx marks the type; it does nothing.
	StoreTx()
}

// Atomic opens multi-kind transactions. Atomically runs fn inside one
// transaction and commits if fn returns nil; any error from fn, or a
// panic, rolls back every row, edge and audit record fn wrote, and no
// event is published. Events for what fn created are published only
// after commit, in creation order.
//
// Inside fn, write only through tx: a read or write that opens its own
// transaction on the same database waits for this one to finish, and
// the default implementation has one connection, so it would wait until
// ctx ends. tx is not safe for concurrent use, and is refused once fn
// has returned.
type Atomic interface {
	Atomically(ctx context.Context, fn func(tx Tx) error) error
}

// TxCreator creates a resource inside a transaction Atomic opened, with
// Create's checks, edges and audit record. A unique-key clash returns
// ErrDuplicate; the caller returns it from fn so the whole batch rolls
// back.
type TxCreator[S, St any] interface {
	CreateIn(ctx context.Context, tx Tx, r resource.Resource[S, St]) (resource.Resource[S, St], error)
}
