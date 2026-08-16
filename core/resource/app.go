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

// ComponentSpec has no fields yet in spec 001. A Component's runs-on and
// depends-on relationships (resource-model.md "Relationships") are
// declared through Metadata.Relationships on the envelope, not through
// Spec fields here -- there is no separate relationship-declaration
// mechanism (specs/001-resource-model/plan.md).
type ComponentSpec struct{}

type ComponentStatus struct {
	Status
}

type Component = Resource[ComponentSpec, ComponentStatus]
