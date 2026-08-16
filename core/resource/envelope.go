package resource

import "time"

// Kind identifies a resource type (e.g. "Organization", "Application").
// Concrete values are declared as constants alongside each kind's Spec/
// Status types (core/resource/orgtree.go, infra.go, app.go, capability.go,
// ops.go).
type Kind string

// EdgeType identifies a typed relationship between two resources
// (docs/architecture/resource-model.md "Relationships (resource graph)").
// Declared here rather than in core/graph so core/graph can depend on
// core/resource without a reverse import.
type EdgeType string

const (
	EdgeOwns       EdgeType = "owns"
	EdgeRunsOn     EdgeType = "runs-on"
	EdgeDependsOn  EdgeType = "depends-on"
	EdgeProvidedBy EdgeType = "provided-by"
)

// OwnerRef points at the resource that owns this one, driving an implicit
// "owns" edge in the resource graph. A resource may have zero or more
// owners (e.g. a Component is owned by its Application).
type OwnerRef struct {
	Kind Kind   `json:"kind"`
	ID   string `json:"id"`
}

// Relationship declares a non-owns edge (runs-on, depends-on, provided-by)
// from this resource to another. Owns edges are derived from OwnerRefs
// instead -- a Relationship must never carry EdgeOwns.
type Relationship struct {
	Type       EdgeType `json:"type"`
	TargetKind Kind     `json:"targetKind"`
	TargetID   string   `json:"targetId"`
}

// Metadata is the envelope every Serendip resource shares, regardless of
// kind (docs/architecture/resource-model.md "Resource envelope").
type Metadata struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Kind            Kind              `json:"kind"`
	Labels          map[string]string `json:"labels,omitempty"`
	OwnerRefs       []OwnerRef        `json:"ownerRefs,omitempty"`
	Relationships   []Relationship    `json:"relationships,omitempty"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
	ResourceVersion int64             `json:"resourceVersion"`
}

// Resource pairs Metadata with a kind-specific desired state (Spec) and
// observed state (Status). Every resource kind instantiates this generic
// envelope rather than hand-rolling its own metadata handling.
type Resource[S any, St any] struct {
	Metadata Metadata `json:"metadata"`
	Spec     S        `json:"spec"`
	Status   St       `json:"status"`
}
