package resource_test

import (
	"encoding/json"
	"reflect"
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

func TestComponentSpec_JSONWireShape(t *testing.T) {
	tests := []struct {
		name string
		spec resource.ComponentSpec
		want string
	}{
		{
			name: "full",
			spec: resource.ComponentSpec{
				Image: "nginx:1.27-alpine",
				Ports: []resource.ComponentPort{{Name: "http", Port: 80}},
				Env:   map[string]string{"GREETING": "hi"},
			},
			want: `{"image":"nginx:1.27-alpine","ports":[{"name":"http","port":80}],"env":{"GREETING":"hi"}}`,
		},
		{
			name: "empty ports and env omitted",
			spec: resource.ComponentSpec{Image: "busybox"},
			want: `{"image":"busybox"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.spec)
			if err != nil {
				t.Fatalf("Marshal() = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Marshal() = %s, want %s", got, tt.want)
			}
			var back resource.ComponentSpec
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatalf("Unmarshal() = %v", err)
			}
			if !reflect.DeepEqual(back, tt.spec) {
				t.Errorf("round trip = %+v, want %+v", back, tt.spec)
			}
		})
	}
}
