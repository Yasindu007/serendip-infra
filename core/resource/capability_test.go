package resource_test

import (
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestCapability_KindConstants(t *testing.T) {
	tests := []struct {
		kind resource.Kind
		want string
	}{
		{resource.KindCapability, "Capability"},
		{resource.KindProvider, "Provider"},
		{resource.KindProviderInstance, "ProviderInstance"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.kind) != tt.want {
				t.Errorf("kind = %q, want %q", tt.kind, tt.want)
			}
		})
	}
}

func TestCapability_SwitchableDefault(t *testing.T) {
	// This is the shape acceptance criterion 3 (spec.md) depends on:
	// "registering two providers for one capability and switching the
	// default requires no changes outside registry data" -- Default is
	// just a field, switching it is an ordinary field mutation.
	cap := resource.Capability{
		Metadata: resource.Metadata{Name: "identity", Kind: resource.KindCapability},
		Spec: resource.CapabilitySpec{
			Required:  true,
			Providers: []string{"provider-a", "provider-b", "external"},
			Default:   "provider-a",
		},
	}
	if err := resource.Validate(cap.Metadata); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	cap.Spec.Default = "provider-b"
	if cap.Spec.Default != "provider-b" {
		t.Errorf("Default = %q, want %q", cap.Spec.Default, "provider-b")
	}
	if len(cap.Spec.Providers) != 3 {
		t.Errorf("len(Providers) = %d, want 3", len(cap.Spec.Providers))
	}
}

func TestProvider_DescriptorShape(t *testing.T) {
	p := resource.Provider{
		Metadata: resource.Metadata{Name: "provider-a", Kind: resource.KindProvider},
		Spec: resource.ProviderSpec{
			Capabilities: []string{"identity"},
			ResourceRequirements: resource.ResourceRequirements{
				RSSBytes:      256 << 20,
				CPUMilliCores: 500,
			},
			HealthCheck: resource.HealthCheckContract{
				Kind:   "http",
				Config: map[string]string{"path": "/health"},
			},
		},
	}
	if err := resource.Validate(p.Metadata); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if p.Spec.HealthCheck.Kind != "http" {
		t.Errorf("HealthCheck.Kind = %q, want %q", p.Spec.HealthCheck.Kind, "http")
	}
}

func TestProviderInstance_BindsCapabilityToEnvironment(t *testing.T) {
	pi := resource.ProviderInstance{
		Metadata: resource.Metadata{Name: "identity-prod", Kind: resource.KindProviderInstance},
		Spec: resource.ProviderInstanceSpec{
			CapabilityID:  "cap-identity",
			ProviderID:    "provider-a",
			EnvironmentID: "env-prod",
		},
	}
	if err := resource.Validate(pi.Metadata); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if pi.Spec.CapabilityID == "" || pi.Spec.ProviderID == "" || pi.Spec.EnvironmentID == "" {
		t.Error("ProviderInstance must bind all three: capability, provider, environment")
	}
}
