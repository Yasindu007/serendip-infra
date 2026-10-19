package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/id"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// ApprovalHandler serves /v1/approvals (spec 019): requesting a destructive
// action, reading requests, and deciding them. The authorizing mux has
// already decided the caller may ask for, read or decide the action (and,
// for a decision, that the caller may decide at all, policy.MayDecide);
// this handler records it. Executing an approved request is the approval
// reconciler's job, never this handler's (constitution 3).
type ApprovalHandler struct {
	approvals store.Store[resource.ApprovalSpec, resource.ApprovalStatus]
	tree      approvalTree
	// head returns any kind's resource name and version, or
	// store.ErrNotFound.
	head func(ctx context.Context, kind resource.Kind, id string) (string, int64, error)
	now  func() time.Time
	// mu serialises requests, so two identical requests can't both create
	// a pending approval (one serendipd per store).
	mu sync.Mutex
}

// approvalTree is what a request reads from the resource graph.
type approvalTree interface {
	Descendants(ctx context.Context, ref graph.ResourceRef) ([]graph.ResourceRef, error)
	Ancestors(ctx context.Context, ref graph.ResourceRef) ([]graph.ResourceRef, error)
}

func (h *ApprovalHandler) Register(mux Mux) {
	mux.HandleFunc("POST /v1/approvals", h.request)
	mux.HandleFunc("GET /v1/approvals", h.list)
	mux.HandleFunc("GET /v1/approvals/{id}", h.get)
	mux.HandleFunc("POST /v1/approvals/{id}/approve", h.approve)
	mux.HandleFunc("POST /v1/approvals/{id}/reject", h.reject)
}

// approvalView is an approval as the API shows it: the removed resources'
// last specs are clean-up input, never returned (plan.md decision 9).
func approvalView(a resource.Approval) resource.Approval {
	radius := make([]resource.ApprovalRef, len(a.Status.BlastRadius))
	for i, r := range a.Status.BlastRadius {
		r.LastSpec = nil
		radius[i] = r
	}
	a.Status.BlastRadius = radius
	return a
}

// request records a request for a destructive action. With every check
// allowed, policy approves it at once and the reconciler executes it; with
// any NeedsApproval, it waits for a person. Deny never gets here.
func (h *ApprovalHandler) request(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := audit.RequestIDFrom(ctx)
	caller, ok := callerFrom(ctx)
	if !ok {
		WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", reqID)
		return
	}
	var spec resource.ApprovalSpec
	if !decodeBody(w, r, &spec) {
		return
	}
	if err := resource.ValidateApprovalSpec(spec); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	needsPerson := needsApprovalFrom(ctx)
	if needsPerson && spec.Reason == "" {
		writeStoreErr(w, r, fmt.Errorf("%w: reason: required, since this request waits for a person to approve it", resource.ErrValidation))
		return
	}
	target := spec.Action.Target
	name, version, err := h.head(ctx, target.Kind, target.ID)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	all, err := h.approvals.List(ctx, store.ListOptions{Kind: resource.KindApproval})
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	// Listing every approval under the lock is fine at edge-1 scale (plan.md
	// decision 1: no index until retention).
	for _, a := range all {
		open := a.Status.Phase == resource.ApprovalPending || a.Status.Phase == resource.ApprovalApproved
		if open && a.Spec.Action == spec.Action {
			// The same action again gets the one already open, whoever asks:
			// one open approval per action. A person who could approve an
			// agent's pending request is answered with it, and can approve
			// it rather than racing it with a second request.
			writeJSON(w, http.StatusOK, approvalView(a))
			return
		}
	}

	ref := graph.ResourceRef{Kind: target.Kind, ID: target.ID}
	radius := []resource.ApprovalRef{{Kind: target.Kind, ID: target.ID, Name: name}}
	if spec.Action.Cascade {
		below, err := h.tree.Descendants(ctx, ref)
		if err != nil {
			writeStoreErr(w, r, err)
			return
		}
		for _, d := range below {
			n, _, err := h.head(ctx, d.Kind, d.ID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				writeStoreErr(w, r, err)
				return
			}
			radius = append(radius, resource.ApprovalRef{Kind: d.Kind, ID: d.ID, Name: n})
		}
	}
	owners, err := h.tree.Ancestors(ctx, ref)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	chain := make([]resource.Scope, 0, len(owners))
	for _, o := range owners {
		chain = append(chain, resource.Scope{Kind: o.Kind, ID: o.ID})
	}

	now := h.now().UTC()
	evidence := ""
	if d := audit.PolicyDecisionFrom(ctx); d != nil {
		evidence = *d
	}
	a := resource.Approval{
		Metadata: resource.Metadata{ID: id.New(), Name: fmt.Sprintf("delete %s/%s", target.Kind, name)},
		Spec:     spec,
		Status: resource.ApprovalStatus{
			RequestedBy:     requesterOf(ctx, caller),
			RequestedAt:     now,
			RequestDecision: evidence,
			TargetName:      name,
			TargetVersion:   version,
			OwnerChain:      chain,
			BlastRadius:     radius,
		},
	}
	if needsPerson {
		a.Status.Phase = resource.ApprovalPending
		a.Status.ExpiresAt = now.Add(resource.ApprovalTTL)
		setApprovalCondition(&a, resource.ApprovalConditionDecided, resource.ConditionFalse, "AwaitingDecision",
			"waiting for a person allowed to "+string(spec.Action.Verb)+" it to approve or reject", now)
	} else {
		a.Status.Phase = resource.ApprovalApproved
		a.Status.Decision = &resource.ApprovalDecision{ByPolicy: true, At: now, Evidence: evidence}
		setApprovalCondition(&a, resource.ApprovalConditionDecided, resource.ConditionTrue, "ApprovedByPolicy", "the requester's roles allow it", now)
	}
	created, err := h.approvals.Create(ctx, a)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, approvalView(created))
}

func (h *ApprovalHandler) get(w http.ResponseWriter, r *http.Request) {
	a, err := h.approvals.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, approvalView(a))
}

// list returns the approvals the caller may read, newest first, filtered
// by ?phase=, ?targetKind= and ?targetId=, and ?requester= (an actor).
func (h *ApprovalHandler) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, err := h.approvals.List(ctx, store.ListOptions{Kind: resource.KindApproval})
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	q := r.URL.Query()
	matching := make([]resource.Approval, 0, len(all))
	for _, a := range all {
		t := a.Spec.Action.Target
		if (q.Get("phase") != "" && string(a.Status.Phase) != q.Get("phase")) ||
			(q.Get("targetKind") != "" && string(t.Kind) != q.Get("targetKind")) ||
			(q.Get("targetId") != "" && t.ID != q.Get("targetId")) ||
			(q.Get("requester") != "" && a.Status.RequestedBy.Actor != q.Get("requester")) {
			continue
		}
		matching = append(matching, a)
	}
	readable, err := filterReadableApprovals(ctx, matching)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	slices.SortFunc(readable, func(x, y resource.Approval) int {
		return cmp.Or(y.Status.RequestedAt.Compare(x.Status.RequestedAt), cmp.Compare(x.Metadata.ID, y.Metadata.ID))
	})
	out := make([]resource.Approval, len(readable))
	for i, a := range readable {
		out[i] = approvalView(a)
	}
	writeJSON(w, http.StatusOK, out)
}

// decisionBody is what approve and reject accept.
type decisionBody struct {
	Comment string `json:"comment,omitempty"`
}

func (h *ApprovalHandler) approve(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, resource.ApprovalApproved)
}

func (h *ApprovalHandler) reject(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, resource.ApprovalRejected)
}

// decide records a person's or API key's decision on a pending approval.
// The authorizer has checked they may decide it at all (MayDecide) and are
// allowed the action themselves, now.
func (h *ApprovalHandler) decide(w http.ResponseWriter, r *http.Request, to resource.ApprovalPhase) {
	ctx := r.Context()
	reqID := audit.RequestIDFrom(ctx)
	caller, ok := callerFrom(ctx)
	if !ok {
		WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", reqID)
		return
	}
	var body decisionBody
	if !decodeOptionalBody(w, r, &body) {
		return
	}
	if err := resource.ValidateApprovalComment(body.Comment); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	a, err := h.approvals.Get(ctx, r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	now := h.now().UTC()
	switch {
	case a.Status.Phase != resource.ApprovalPending:
		conflict(w, r, fmt.Sprintf("the approval is %s; only a pending approval can be decided", a.Status.Phase))
		return
	case !a.Status.ExpiresAt.IsZero() && !now.Before(a.Status.ExpiresAt):
		conflict(w, r, "the approval expired at "+a.Status.ExpiresAt.Format(time.RFC3339)+"; request it again")
		return
	}
	if err := resource.ApprovalTransition(a.Status.Phase, to); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	evidence := ""
	if d := audit.PolicyDecisionFrom(ctx); d != nil {
		evidence = *d
	}
	by := requesterOf(ctx, caller)
	a.Status.Phase = to
	a.Status.ExpiresAt = time.Time{}
	a.Status.Decision = &resource.ApprovalDecision{By: &by, At: now, Comment: body.Comment, Evidence: evidence}
	reason := "ApprovedByPerson"
	if to == resource.ApprovalRejected {
		reason = "Rejected"
	}
	setApprovalCondition(&a, resource.ApprovalConditionDecided, resource.ConditionTrue, reason, "decided by "+by.Actor, now)
	updated, err := h.approvals.Update(ctx, a)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, approvalView(updated))
}

// setApprovalCondition replaces the condition of typ, or appends it.
func setApprovalCondition(a *resource.Approval, typ string, status resource.ConditionStatus, reason, message string, at time.Time) {
	c := resource.Condition{Type: typ, Status: status, Reason: reason, Message: message, LastTransitionTime: at}
	for i := range a.Status.Conditions {
		if a.Status.Conditions[i].Type == typ {
			a.Status.Conditions[i] = c
			return
		}
	}
	a.Status.Conditions = append(a.Status.Conditions, c)
}
