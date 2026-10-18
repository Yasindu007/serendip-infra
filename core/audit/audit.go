package audit

import (
	"encoding/json"
	"time"
)

// Record is one audit entry: who changed what, when, via what interface,
// under what policy decision (constitution rule 8). Every mutation
// through core/store writes exactly one Record in the same transaction
// as the mutation itself -- this is not an optional wrapper a caller
// could forget to apply (specs/001-resource-model/plan.md).
type Record struct {
	ID         string
	Actor      string
	Interface  string
	Kind       string
	ResourceID string
	Before     json.RawMessage // nil on Create -- nothing existed before
	After      json.RawMessage // nil on Delete -- nothing exists after
	At         time.Time
	// PolicyDecision is the authorization decision behind the record
	// (spec 017): "allow: <role> (<assignment>) at <scope>" for a mutation,
	// "deny: missing <action> at <scope>" for a refused one. Nil for
	// mutations made outside an authorized request.
	PolicyDecision *string
	// ActorSubject is the stable identifier behind Actor when the actor is a
	// person: the identity provider's subject, which survives a renamed or
	// reused username (spec 017). Empty for API keys and system actors.
	ActorSubject string
	// ActorSponsor is the person accountable for the actor when the actor
	// is an agent (spec 018): the sponsor its role assignments name. Empty
	// for everyone else.
	ActorSponsor string
	// RequestID ties this record back to the HTTP request that caused it
	// (spec 002, RequestIDFrom). Empty for mutations not made through the
	// API (a direct core/store call, e.g. a test or a future reconciler).
	RequestID string
}
