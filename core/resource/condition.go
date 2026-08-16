package resource

import "time"

// ConditionStatus is the tri-state value of a Condition, matching
// Kubernetes API conventions (docs/architecture/resource-model.md
// "Status conventions").
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// Condition is one observed fact about a resource's Status, e.g.
// {Type: "Available", Status: ConditionFalse, Reason: "NoReplicas"}.
// Every resource kind's Status embeds Status (below), so all kinds share
// this shape regardless of what Type values they use.
type Condition struct {
	Type               string          `json:"type"`
	Status             ConditionStatus `json:"status"`
	Reason             string          `json:"reason,omitempty"`
	Message            string          `json:"message,omitempty"`
	LastTransitionTime time.Time       `json:"lastTransitionTime"`
}

// Status is embedded by every kind-specific Status struct so Conditions
// are handled uniformly across all resource kinds (spec 001 open question
// "shared status package", resolved in specs/001-resource-model/plan.md).
type Status struct {
	Conditions []Condition `json:"conditions,omitempty"`
}
