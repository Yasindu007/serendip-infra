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
	// PolicyDecision is reserved for a future policy engine (spec 003+).
	// Nil until then -- nothing sets it in spec 001.
	PolicyDecision *string
	// RequestID ties this record back to the HTTP request that caused it
	// (spec 002, RequestIDFrom). Empty for mutations not made through the
	// API (a direct core/store call, e.g. a test or a future reconciler).
	RequestID string
}
