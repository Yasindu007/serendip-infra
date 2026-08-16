package resource

const (
	KindOrganization Kind = "Organization"
	KindTeam         Kind = "Team"
	KindProject      Kind = "Project"
	KindEnvironment  Kind = "Environment"
)

// OrganizationSpec has no fields yet -- Organization is purely a top-level
// tenant boundary (docs/architecture/resource-model.md); grouping happens
// via OwnerRefs from child resources, not fields here.
type OrganizationSpec struct{}

type OrganizationStatus struct {
	Status
}

type Organization = Resource[OrganizationSpec, OrganizationStatus]

// TeamSpec has no fields yet. Teams group humans and RBAC bindings per
// resource-model.md, but RBAC enforcement is out of spec 001's scope
// (constitution rule 2 fields are reserved for a future spec, not modeled
// here to avoid inventing an RBAC shape prematurely).
type TeamSpec struct{}

type TeamStatus struct {
	Status
}

type Team = Resource[TeamSpec, TeamStatus]

// ProjectSpec has no fields yet -- Project groups related Applications;
// that grouping is expressed via OwnerRefs, not fields here.
type ProjectSpec struct{}

type ProjectStatus struct {
	Status
}

type Project = Resource[ProjectSpec, ProjectStatus]

// EnvironmentSpec carries the Environment's own capability configuration
// (resource-model.md: "this Environment's Database is HA, that one
// isn't"), keyed by capability name (see core/resource/capability.go),
// valued by the provider name to use for that capability in this
// Environment. An empty/missing entry means "use the capability's
// registry default."
type EnvironmentSpec struct {
	CapabilityOverrides map[string]string
}

// AvailabilityLevel is the honest, never-silently-upgraded description of
// what failure protection an Environment actually has (constitution rule
// 6). Nothing sets this yet in spec 001 -- the field exists so later specs
// (reconciliation, topology planner) never have to add it as a breaking
// change.
type AvailabilityLevel string

const (
	AvailabilitySingleInstance AvailabilityLevel = "single-instance"
	AvailabilityHA             AvailabilityLevel = "ha"
)

type EnvironmentStatus struct {
	Status
	Availability AvailabilityLevel
}

type Environment = Resource[EnvironmentSpec, EnvironmentStatus]
