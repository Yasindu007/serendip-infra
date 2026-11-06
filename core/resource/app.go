package resource

import "fmt"

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

// ComponentSpec describes one container image as a deployable workload.
// It has exactly one of Image, already pullable, and Source, which is
// built into an image (spec 022; ValidateComponentSource). A Component's
// runs-on and depends-on
// relationships (resource-model.md "Relationships") are declared through
// Metadata.Relationships on the envelope, not through Spec fields
// (specs/001-resource-model/plan.md).
type ComponentSpec struct {
	Image string `json:"image"`
	// Source is where the image is built from, when there's no Image.
	Source *ComponentSource  `json:"source,omitempty"`
	Ports  []ComponentPort   `json:"ports,omitempty"`
	Env    map[string]string `json:"env,omitempty"`
	// Dependencies are the other Components this one calls (spec 020).
	// Each becomes a depends-on edge, and the platform puts the target's
	// address in an environment variable.
	Dependencies []ComponentDependency `json:"dependencies,omitempty"`
}

// ComponentDependency is one call to another Component's port: Env names
// the variable that receives its address.
type ComponentDependency struct {
	Component string `json:"component"`
	Port      string `json:"port"`
	Env       string `json:"env"`
}

// MaxComponentDependencies is the platform's limit on one workload's
// dependencies.
const MaxComponentDependencies = 50

// ValidateComponentDependencies checks a Component's dependencies on
// their own: whether each target exists, shares the Application and has
// the port is decided where Components are known. self is the Component's
// own ID ("" on create).
func ValidateComponentDependencies(self string, s ComponentSpec) error {
	if len(s.Dependencies) > MaxComponentDependencies {
		return fmt.Errorf("%w: dependencies: at most %d", ErrValidation, MaxComponentDependencies)
	}
	envs := map[string]bool{}
	calls := map[[2]string]bool{}
	for i, d := range s.Dependencies {
		at := fmt.Sprintf("dependencies[%d]", i)
		switch {
		case d.Component == "" || !idRE.MatchString(d.Component) || d.Component == "." || d.Component == "..":
			return fmt.Errorf("%w: %s.component: a Component ID is required", ErrValidation, at)
		case self != "" && d.Component == self:
			return fmt.Errorf("%w: %s.component: a Component can't depend on itself", ErrValidation, at)
		case len(d.Port) > 63 || !dnsLabelRE.MatchString(d.Port):
			return fmt.Errorf("%w: %s.port: the name of one of the target's ports", ErrValidation, at)
		case !envNameRE.MatchString(d.Env):
			return fmt.Errorf("%w: %s.env: %q isn't a variable name", ErrValidation, at, d.Env)
		case envs[d.Env]:
			return fmt.Errorf("%w: %s.env: %s is already used by another dependency", ErrValidation, at, d.Env)
		case calls[[2]string{d.Component, d.Port}]:
			return fmt.Errorf("%w: %s: this port is already a dependency", ErrValidation, at)
		}
		if _, clash := s.Env[d.Env]; clash {
			return fmt.Errorf("%w: %s.env: %s is also set in env", ErrValidation, at, d.Env)
		}
		envs[d.Env], calls[[2]string{d.Component, d.Port}] = true, true
	}
	return nil
}

// ComponentRelationships replaces rels' depends-on edges to Components
// with the ones s's dependencies declare, keeping any others: the server
// owns those edges.
func ComponentRelationships(rels []Relationship, s ComponentSpec) []Relationship {
	var out []Relationship
	for _, r := range rels {
		if r.Type != EdgeDependsOn || r.TargetKind != KindComponent {
			out = append(out, r)
		}
	}
	seen := map[string]bool{}
	for _, d := range s.Dependencies {
		if !seen[d.Component] {
			seen[d.Component] = true
			out = append(out, Relationship{Type: EdgeDependsOn, TargetKind: KindComponent, TargetID: d.Component})
		}
	}
	return out
}

// ComponentPort is one port the Component's container listens on. Name
// is a DNS label identifying the port within its Component. Expose makes
// the port reachable from outside the cluster through the platform's
// shared gateway, served as HTTP.
type ComponentPort struct {
	Name   string `json:"name"`
	Port   int32  `json:"port"`
	Expose bool   `json:"expose,omitempty"`
}

// ComponentStatus.Endpoints lists the external address of each exposed
// port, populated only once that address is actually serving.
type ComponentStatus struct {
	Status
	Endpoints []ComponentEndpointStatus `json:"endpoints,omitempty"`
	// Image is the built image a source Component runs: its newest
	// successful build of its current source (spec 022). Empty for an
	// image Component, and until the first build succeeds.
	Image string `json:"image,omitempty"`
}

// ComponentEndpointStatus is where one exposed port can be reached from
// outside the cluster. Name matches the ComponentPort's Name.
type ComponentEndpointStatus struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Component = Resource[ComponentSpec, ComponentStatus]
