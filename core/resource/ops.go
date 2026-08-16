package resource

import "time"

const (
	KindAlert    Kind = "Alert"
	KindIncident Kind = "Incident"
)

// AlertSpec describes a single detected anomaly (resource-model.md:
// "a metric threshold, log pattern, synthetic check failure, ...").
// Types only in spec 001 -- no lifecycle logic (spec.md Scope OUT).
type AlertSpec struct {
	Source   string `json:"source,omitempty"` // what produced this alert (observability backend, synthetic check, ...)
	Rule     string `json:"rule,omitempty"`
	Severity string `json:"severity,omitempty"`
}

type AlertStatus struct {
	Status
	FiredAt time.Time `json:"firedAt,omitzero"`
}

type Alert = Resource[AlertSpec, AlertStatus]

// IncidentSpec captures what's declared about an Incident up front.
// Diagnosis and the approval/action trail (docs/architecture/operations.md
// full lifecycle) are explicitly out of scope for spec 001 -- types only.
type IncidentSpec struct {
	Severity string `json:"severity,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

// IncidentStatus tracks what an Incident has correlated so far. Nothing
// populates CorrelatedAlerts or AffectedResources in spec 001 -- that's
// operations.md's correlation engine and ai-operations.md's blast-radius
// output, both later specs. The shape exists now so those specs don't
// need a breaking change to add it.
type IncidentStatus struct {
	Status
	CorrelatedAlerts  []string `json:"correlatedAlerts,omitempty"`  // Alert IDs
	AffectedResources []string `json:"affectedResources,omitempty"` // resource IDs
}

type Incident = Resource[IncidentSpec, IncidentStatus]
