package openchoreo

import (
	"context"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/jsonschema"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// The schema Serendip installs is the one served, and the validator can
// check it.
func TestConfigSchemas(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	got, err := p.ConfigSchemas(context.Background(), resource.Component{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Traits) != 0 {
		t.Errorf("trait schemas = %v", got.Traits)
	}
	s, err := jsonschema.Parse(got.Environment)
	if err != nil {
		t.Fatalf("the installed schema isn't checkable: %v", err)
	}
	if problems := s.Validate(map[string]any{"replicas": float64(2)}); len(problems) != 0 {
		t.Errorf("valid config refused: %v", problems)
	}
	if problems := s.Validate(map[string]any{"replicas": float64(-1)}); len(problems) != 1 {
		t.Errorf("invalid config: %v", problems)
	}

	// No component type yet: an error, not an empty schema.
	bare := NewProvider(newTestKubeClient(t, newFakeAPI().handler(t)), testGateway)
	if _, err := bare.ConfigSchemas(context.Background(), resource.Component{}); err == nil {
		t.Error("a missing component type was read as no schema")
	}
}
