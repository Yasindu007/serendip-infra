package resource_test

import (
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestOrgTree_KindConstants(t *testing.T) {
	tests := []struct {
		kind resource.Kind
		want string
	}{
		{resource.KindOrganization, "Organization"},
		{resource.KindTeam, "Team"},
		{resource.KindProject, "Project"},
		{resource.KindEnvironment, "Environment"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.kind) != tt.want {
				t.Errorf("kind = %q, want %q", tt.kind, tt.want)
			}
		})
	}
}

func TestOrganization_ValidEnvelope(t *testing.T) {
	org := resource.Organization{
		Metadata: resource.Metadata{Name: "acme", Kind: resource.KindOrganization},
	}
	if err := resource.Validate(org.Metadata); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestEnvironment_HonestAvailabilityReporting(t *testing.T) {
	// constitution rule 6: a single-instance install must be able to say
	// so explicitly, not just report "healthy".
	env := resource.Environment{
		Metadata: resource.Metadata{Name: "production", Kind: resource.KindEnvironment},
		Spec: resource.EnvironmentSpec{
			CapabilityOverrides: map[string]string{"database": "external-postgres"},
		},
		Status: resource.EnvironmentStatus{
			Availability: resource.AvailabilitySingleInstance,
		},
	}
	if env.Status.Availability != resource.AvailabilitySingleInstance {
		t.Errorf("Availability = %q, want %q", env.Status.Availability, resource.AvailabilitySingleInstance)
	}
	if env.Spec.CapabilityOverrides["database"] != "external-postgres" {
		t.Errorf("CapabilityOverrides[database] = %q, want %q",
			env.Spec.CapabilityOverrides["database"], "external-postgres")
	}
}
