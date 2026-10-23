package resource_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestValidateComponentDependencies(t *testing.T) {
	dep := func(comp, port, env string) resource.ComponentDependency {
		return resource.ComponentDependency{Component: comp, Port: port, Env: env}
	}
	tests := []struct {
		name string
		spec resource.ComponentSpec
		err  string
	}{
		{name: "none"},
		{name: "two", spec: resource.ComponentSpec{Dependencies: []resource.ComponentDependency{dep("c2", "http", "API_URL"), dep("c3", "grpc", "AUTH_ADDR")}}},
		{name: "two ports of one target", spec: resource.ComponentSpec{Dependencies: []resource.ComponentDependency{dep("c2", "http", "A"), dep("c2", "admin", "B")}}},
		{name: "no component", spec: resource.ComponentSpec{Dependencies: []resource.ComponentDependency{dep("", "http", "A")}}, err: "component"},
		{name: "itself", spec: resource.ComponentSpec{Dependencies: []resource.ComponentDependency{dep("self", "http", "A")}}, err: "itself"},
		{name: "bad port", spec: resource.ComponentSpec{Dependencies: []resource.ComponentDependency{dep("c2", "HTTP", "A")}}, err: "port"},
		{name: "bad variable", spec: resource.ComponentSpec{Dependencies: []resource.ComponentDependency{dep("c2", "http", "1A")}}, err: "isn't a variable name"},
		{name: "a variable twice", spec: resource.ComponentSpec{Dependencies: []resource.ComponentDependency{dep("c2", "http", "A"), dep("c3", "http", "A")}}, err: "already used"},
		{name: "a port twice", spec: resource.ComponentSpec{Dependencies: []resource.ComponentDependency{dep("c2", "http", "A"), dep("c2", "http", "B")}}, err: "already a dependency"},
		{name: "clashes with env", spec: resource.ComponentSpec{Env: map[string]string{"A": "x"}, Dependencies: []resource.ComponentDependency{dep("c2", "http", "A")}}, err: "also set in env"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := resource.ValidateComponentDependencies("self", tt.spec)
			if tt.err == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, resource.ErrValidation) || !strings.Contains(err.Error(), tt.err) {
				t.Fatalf("err = %v, want %q", err, tt.err)
			}
		})
	}
	many := resource.ComponentSpec{}
	for i := range resource.MaxComponentDependencies + 1 {
		many.Dependencies = append(many.Dependencies, dep("c"+strings.Repeat("x", i+1), "http", "V"+strings.Repeat("X", i)))
	}
	if err := resource.ValidateComponentDependencies("", many); err == nil {
		t.Error("51 dependencies were accepted")
	}
}

func TestComponentRelationships(t *testing.T) {
	other := resource.Relationship{Type: resource.EdgeDependsOn, TargetKind: resource.KindVolume, TargetID: "v1"}
	stale := resource.Relationship{Type: resource.EdgeDependsOn, TargetKind: resource.KindComponent, TargetID: "old"}
	got := resource.ComponentRelationships([]resource.Relationship{other, stale}, resource.ComponentSpec{Dependencies: []resource.ComponentDependency{
		{Component: "c2", Port: "http", Env: "A"}, {Component: "c2", Port: "admin", Env: "B"}, {Component: "c3", Port: "http", Env: "C"}}})
	want := []resource.Relationship{other,
		{Type: resource.EdgeDependsOn, TargetKind: resource.KindComponent, TargetID: "c2"},
		{Type: resource.EdgeDependsOn, TargetKind: resource.KindComponent, TargetID: "c3"}}
	if !slices.Equal(got, want) {
		t.Errorf("relationships = %+v", got)
	}
}
