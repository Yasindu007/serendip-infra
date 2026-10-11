package resource

const (
	KindApplication Kind = "Application"
	KindComponent   Kind = "Component"
)

// ApplicationSpec has no fields yet -- Application is the grouping a
// developer thinks in terms of (resource-model.md); its Components (owned
// via OwnerRefs) carry the actual deployable detail.
type ApplicationSpec struct{}

type ApplicationStatus struct {
	Status
}

type Application = Resource[ApplicationSpec, ApplicationStatus]

// ComponentSpec describes one already-built container image as a
// deployable workload. Building images from source is out of scope here;
// Image must already be pullable. A Component's runs-on and depends-on
// relationships (resource-model.md "Relationships") are declared through
// Metadata.Relationships on the envelope, not through Spec fields
// (specs/001-resource-model/plan.md).
type ComponentSpec struct {
	Image string            `json:"image"`
	Ports []ComponentPort   `json:"ports,omitempty"`
	Env   map[string]string `json:"env,omitempty"`
}

// ComponentPort is one port the Component's container listens on. Name
// is a DNS label identifying the port within its Component.
type ComponentPort struct {
	Name string `json:"name"`
	Port int32  `json:"port"`
}

type ComponentStatus struct {
	Status
}

type Component = Resource[ComponentSpec, ComponentStatus]
