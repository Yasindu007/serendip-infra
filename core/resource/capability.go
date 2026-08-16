package resource

const (
	KindCapability       Kind = "Capability"
	KindProvider         Kind = "Provider"
	KindProviderInstance Kind = "ProviderInstance"
)

// CapabilitySpec is the capability declaration described in
// docs/architecture/capability-model.md's "Capability spec format":
// what capability this is, whether a fresh install can run without it,
// which providers are registered for it, and which one is the default.
type CapabilitySpec struct {
	Required  bool     `json:"required"`
	Providers []string `json:"providers,omitempty"` // registered provider names for this capability
	Default   string   `json:"default,omitempty"`   // provider name a fresh install picks absent user choice
}

type CapabilityStatus struct {
	Status
}

type Capability = Resource[CapabilitySpec, CapabilityStatus]

// ResourceRequirements is a provider's declared resource footprint on
// reference hardware, used by the topology planner and installer
// (docs/architecture/provider-model.md "Registration"). Nothing verifies
// these numbers in spec 001 -- that's a benchmark/reconciliation concern.
type ResourceRequirements struct {
	RSSBytes      int64 `json:"rssBytes,omitempty"`
	CPUMilliCores int64 `json:"cpuMilliCores,omitempty"`
}

// HealthCheckContract describes how the reconciliation engine (spec 003)
// should check whether this provider is alive. Stored as data only in
// spec 001 -- nothing executes it yet (provider-model.md "Lifecycle").
type HealthCheckContract struct {
	Kind   string            `json:"kind,omitempty"` // interpreted by the reconciliation engine, not here
	Config map[string]string `json:"config,omitempty"`
}

// ProviderSpec is a provider's registration descriptor
// (docs/architecture/provider-model.md "Registration"): which
// capabilities it implements, its resource requirements, and its health
// check contract.
type ProviderSpec struct {
	Capabilities         []string             `json:"capabilities,omitempty"`
	ResourceRequirements ResourceRequirements `json:"resourceRequirements,omitzero"`
	HealthCheck          HealthCheckContract  `json:"healthCheck,omitzero"`
}

type ProviderStatus struct {
	Status
}

type Provider = Resource[ProviderSpec, ProviderStatus]

// ProviderInstanceSpec binds a registered Provider to a specific
// Capability within a specific Environment -- "this Environment uses this
// Provider for this Capability" (capability-model.md, resource-model.md).
type ProviderInstanceSpec struct {
	CapabilityID  string            `json:"capabilityId,omitempty"`
	ProviderID    string            `json:"providerId,omitempty"`
	EnvironmentID string            `json:"environmentId,omitempty"`
	Config        map[string]string `json:"config,omitempty"`
}

type ProviderInstanceStatus struct {
	Status
}

type ProviderInstance = Resource[ProviderInstanceSpec, ProviderInstanceStatus]
