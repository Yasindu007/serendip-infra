package resource

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// KindApproval is the record of one destructive action (constitution rule
// 5, spec 019): what was asked, who asked, what it would remove, who or
// what decided, and how execution went. Every delete writes one; policy
// decides it instantly when the actor's role allows the action, and only a
// "needs approval" decision waits for a person.
const KindApproval Kind = "Approval"

// ApprovalTTL is how long a pending approval waits for a decision before
// it expires.
const ApprovalTTL = 24 * time.Hour

// ApprovalVerb is the destructive action an approval is for. Only delete
// exists today; scale-to-zero, drain and volume operations join it when
// the API has them.
type ApprovalVerb string

const ApprovalVerbDelete ApprovalVerb = "delete"

// ApprovalPhase is where an approval is in its lifecycle.
type ApprovalPhase string

const (
	ApprovalPending  ApprovalPhase = "Pending"
	ApprovalApproved ApprovalPhase = "Approved"
	ApprovalRejected ApprovalPhase = "Rejected"
	ApprovalExpired  ApprovalPhase = "Expired"
	ApprovalExecuted ApprovalPhase = "Executed"
	ApprovalFailed   ApprovalPhase = "Failed"
)

// Condition types an approval reports (spec 019 plan.md decision 10).
const (
	ApprovalConditionDecided   = "Decided"
	ApprovalConditionExecuted  = "Executed"
	ApprovalConditionCleanedUp = "CleanedUp"
	ApprovalConditionVerified  = "Verified"
)

// ApprovalAction is what an approval asks to do. Target references the
// resource by kind and ID, like a role assignment's scope; it is never
// "/".
type ApprovalAction struct {
	Verb    ApprovalVerb `json:"verb"`
	Target  Scope        `json:"target"`
	Cascade bool         `json:"cascade,omitempty"`
}

// ApprovalSpec is everything a client says about an approval. The rest is
// status, recorded by the server.
type ApprovalSpec struct {
	Action ApprovalAction `json:"action"`
	// Reason is free text. Required when the request waits for a person;
	// optional when policy approves it.
	Reason string `json:"reason,omitempty"`
}

// ApprovalActor is who requested or decided: the audit actor, plus the
// principal behind it when there is one (a server-side actor, such as
// start-up's key revocation, has none).
type ApprovalActor struct {
	Actor     string        `json:"actor"`
	Principal *PrincipalRef `json:"principal,omitempty"`
	// Subject is the identity provider's subject for a person.
	Subject string `json:"subject,omitempty"`
	// Sponsor is the person accountable for an agent.
	Sponsor string `json:"sponsor,omitempty"`
}

// ApprovalRef is one resource an approval removes: the target first, then
// everything a cascade takes with it.
type ApprovalRef struct {
	Kind Kind   `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// LastSpec is the resource's spec as it was deleted: the input its
	// kind's clean-up needs once the record is gone (a Secret's value
	// lives at its path). Set by the store when it deletes; never returned
	// by the API.
	LastSpec json.RawMessage `json:"lastSpec,omitempty"`
	// CleanedUp is set once the clean-up for this ref's kind succeeded.
	// Kinds without a clean-up never set it.
	CleanedUp bool `json:"cleanedUp,omitempty"`
}

// Scope returns the ref as a scope.
func (r ApprovalRef) Scope() Scope { return Scope{Kind: r.Kind, ID: r.ID} }

// ApprovalDecision is how an approval was decided. ByPolicy is true when
// the requester's own role allowed the action; otherwise By is the person
// or API key who approved or rejected it.
type ApprovalDecision struct {
	ByPolicy bool           `json:"byPolicy,omitempty"`
	By       *ApprovalActor `json:"by,omitempty"`
	At       time.Time      `json:"at"`
	Comment  string         `json:"comment,omitempty"`
	// Evidence is the policy decision that allowed the decider: the roles,
	// assignments and scopes, as recorded in audit.
	Evidence string `json:"evidence,omitempty"`
}

// ApprovalStatus is recorded by the server and never taken from a client.
type ApprovalStatus struct {
	Status
	Phase       ApprovalPhase `json:"phase"`
	RequestedBy ApprovalActor `json:"requestedBy"`
	RequestedAt time.Time     `json:"requestedAt"`
	// ExpiresAt is set only while a decision is awaited.
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
	// RequestDecision is the policy decision when the request was made:
	// "allow: …" or "needsApproval: …".
	RequestDecision string `json:"requestDecision,omitempty"`
	// TargetName and TargetVersion are the target as reviewed. Execution
	// refuses a target whose version has moved on.
	TargetName    string `json:"targetName,omitempty"`
	TargetVersion int64  `json:"targetVersion,omitempty"`
	// OwnerChain is the target's owners, nearest first, as of the request.
	// Once the target is gone, reading the approval is decided at the
	// first of them that still exists.
	OwnerChain []Scope `json:"ownerChain,omitempty"`
	// BlastRadius is everything the action removes, the target first. At
	// request time it's what the action would remove; after execution it's
	// what it did remove.
	BlastRadius []ApprovalRef     `json:"blastRadius,omitempty"`
	Decision    *ApprovalDecision `json:"decision,omitempty"`
	ExecutedAt  time.Time         `json:"executedAt,omitzero"`
}

type Approval = Resource[ApprovalSpec, ApprovalStatus]

// approvalMoves are the legal phase transitions. Rejected, Expired,
// Executed and Failed are terminal.
var approvalMoves = map[ApprovalPhase][]ApprovalPhase{
	ApprovalPending:  {ApprovalApproved, ApprovalRejected, ApprovalExpired},
	ApprovalApproved: {ApprovalExecuted, ApprovalFailed},
}

// ApprovalTransition checks a phase change. Every writer of an approval's
// phase goes through it, so a decided approval can never be moved back
// or decided again.
func ApprovalTransition(from, to ApprovalPhase) error {
	if slices.Contains(approvalMoves[from], to) {
		return nil
	}
	return fmt.Errorf("%w: an approval can't move from %q to %q", ErrValidation, from, to)
}

// Terminal reports whether nothing more will happen to an approval in
// phase p, apart from an executed one's clean-up and verification.
func (p ApprovalPhase) Terminal() bool {
	_, moves := approvalMoves[p]
	return !moves && p != ""
}

const (
	// MaxApprovalReasonLen bounds an approval's reason.
	MaxApprovalReasonLen = 2048
	// MaxApprovalCommentLen bounds a decision's comment.
	MaxApprovalCommentLen = 1024
)

// ValidateApprovalSpec checks what a client sent. Whether the caller may
// request it, and whether the target exists, are answered elsewhere.
func ValidateApprovalSpec(s ApprovalSpec) error {
	if s.Action.Verb != ApprovalVerbDelete {
		return fmt.Errorf("%w: action.verb: %q is not supported (only %q)", ErrValidation, s.Action.Verb, ApprovalVerbDelete)
	}
	t := s.Action.Target
	if t.IsRoot() || t.Kind == "" || strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("%w: action.target: kind and id are required", ErrValidation)
	}
	if !idRE.MatchString(t.ID) || t.ID == "." || t.ID == ".." {
		return fmt.Errorf("%w: action.target.id: invalid ID %q", ErrValidation, t.ID)
	}
	if !validText(s.Reason, MaxApprovalReasonLen) {
		return fmt.Errorf("%w: reason: at most %d characters, no control characters", ErrValidation, MaxApprovalReasonLen)
	}
	return nil
}

// ValidateApprovalComment checks a decision's comment.
func ValidateApprovalComment(c string) error {
	if !validText(c, MaxApprovalCommentLen) {
		return fmt.Errorf("%w: comment: at most %d characters, no control characters", ErrValidation, MaxApprovalCommentLen)
	}
	return nil
}
