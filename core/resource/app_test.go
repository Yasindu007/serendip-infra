package resource_test

import (
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestApp_KindConstants(t *testing.T) {
	tests := []struct {
		kind resource.Kind
		want string
	}{
		{resource.KindApplication, "Application"},
		{resource.KindComponent, "Component"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.kind) != tt.want {
				t.Errorf("kind = %q, want %q", tt.kind, tt.want)
			}
		})
	}
}

func TestComponent_RelationshipsDeclaredOnEnvelope(t *testing.T) {
	// Component has no Spec fields for runs-on/depends-on: those go
	// through Metadata.Relationships (specs/001-resource-model/plan.md).
	component := resource.Component{
		Metadata: resource.Metadata{
			Name: "backend",
			Kind: resource.KindComponent,
			OwnerRefs: []resource.OwnerRef{
				{Kind: resource.KindApplication, ID: "app-1"},
			},
			Relationships: []resource.Relationship{
				{Type: resource.EdgeRunsOn, TargetKind: resource.KindCluster, TargetID: "cluster-1"},
				{Type: resource.EdgeDependsOn, TargetKind: resource.KindComponent, TargetID: "component-db"},
			},
		},
	}
	if err := resource.Validate(component.Metadata); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if len(component.Metadata.Relationships) != 2 {
		t.Fatalf("len(Relationships) = %d, want 2", len(component.Metadata.Relationships))
	}
	if component.Metadata.Relationships[0].Type != resource.EdgeRunsOn {
		t.Errorf("Relationships[0].Type = %q, want %q", component.Metadata.Relationships[0].Type, resource.EdgeRunsOn)
	}
}
