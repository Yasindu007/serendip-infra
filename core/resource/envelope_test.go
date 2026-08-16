package resource_test

import (
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		meta    resource.Metadata
		wantErr bool
	}{
		{
			name: "valid minimal",
			meta: resource.Metadata{Name: "my-app"},
		},
		{
			name:    "empty name rejected",
			meta:    resource.Metadata{Name: ""},
			wantErr: true,
		},
		{
			name: "valid labels including prefixed key and empty value",
			meta: resource.Metadata{
				Name: "my-app",
				Labels: map[string]string{
					"team":                     "platform",
					"serendip.dev/environment": "production",
					"empty-value-allowed":      "",
				},
			},
		},
		{
			name: "invalid label key rejected",
			meta: resource.Metadata{
				Name:   "my-app",
				Labels: map[string]string{"bad key with spaces": "x"},
			},
			wantErr: true,
		},
		{
			name: "invalid label value rejected",
			meta: resource.Metadata{
				Name:   "my-app",
				Labels: map[string]string{"team": "not valid!"},
			},
			wantErr: true,
		},
		{
			name: "invalid label prefix rejected",
			meta: resource.Metadata{
				Name:   "my-app",
				Labels: map[string]string{"BadPrefix/team": "platform"},
			},
			wantErr: true,
		},
		{
			name: "relationship with EdgeOwns type rejected",
			meta: resource.Metadata{
				Name: "my-app",
				Relationships: []resource.Relationship{
					{Type: resource.EdgeOwns, TargetKind: resource.KindProject, TargetID: "p1"},
				},
			},
			wantErr: true,
		},
		{
			name: "relationship with non-owns type accepted",
			meta: resource.Metadata{
				Name: "my-app",
				Relationships: []resource.Relationship{
					{Type: resource.EdgeRunsOn, TargetKind: resource.KindCluster, TargetID: "c1"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := resource.Validate(tt.meta)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate(%+v) error = %v, wantErr %v", tt.meta, err, tt.wantErr)
			}
		})
	}
}

func TestResource_GenericEnvelope(t *testing.T) {
	type fakeSpec struct{ Foo string }
	type fakeStatus struct{ resource.Status }

	r := resource.Resource[fakeSpec, fakeStatus]{
		Metadata: resource.Metadata{Name: "example"},
		Spec:     fakeSpec{Foo: "bar"},
		Status: fakeStatus{Status: resource.Status{
			Conditions: []resource.Condition{
				{Type: "Ready", Status: resource.ConditionTrue},
			},
		}},
	}

	if r.Spec.Foo != "bar" {
		t.Errorf("Spec.Foo = %q, want %q", r.Spec.Foo, "bar")
	}
	if len(r.Status.Conditions) != 1 {
		t.Errorf("len(Status.Conditions) = %d, want 1", len(r.Status.Conditions))
	}
}
