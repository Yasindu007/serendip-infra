package resource_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestValidateDeploymentSpec(t *testing.T) {
	long := strings.Repeat("x", 32<<10+1)
	tests := []struct {
		name string
		spec resource.DeploymentSpec
		err  string
	}{
		{name: "minimal", spec: resource.DeploymentSpec{Component: "c1"}},
		{name: "everything", spec: resource.DeploymentSpec{Component: "c1", Release: "cmp-abc-5bf57b49b9",
			Overrides: resource.DeploymentOverrides{Env: map[string]string{"LOG_LEVEL": "debug", "_X": ""},
				Config: map[string]any{"replicas": 2}, Traits: map[string]map[string]any{"autoscaler": {"max": 3}}}}},
		{name: "no component", spec: resource.DeploymentSpec{}, err: "component"},
		{name: "a path in the component", spec: resource.DeploymentSpec{Component: "a/b"}, err: "component"},
		{name: "dot-dot component", spec: resource.DeploymentSpec{Component: ".."}, err: "component"},
		{name: "upper-case release", spec: resource.DeploymentSpec{Component: "c1", Release: "Release-1"}, err: "release"},
		{name: "release with a slash", spec: resource.DeploymentSpec{Component: "c1", Release: "a/b"}, err: "release"},
		{name: "bad variable name", spec: resource.DeploymentSpec{Component: "c1", Overrides: resource.DeploymentOverrides{Env: map[string]string{"1X": "v"}}}, err: "isn't a variable name"},
		{name: "variable with a dash", spec: resource.DeploymentSpec{Component: "c1", Overrides: resource.DeploymentOverrides{Env: map[string]string{"A-B": "v"}}}, err: "isn't a variable name"},
		{name: "huge value", spec: resource.DeploymentSpec{Component: "c1", Overrides: resource.DeploymentOverrides{Env: map[string]string{"A": long}}}, err: "at most"},
		{name: "bad trait instance", spec: resource.DeploymentSpec{Component: "c1", Overrides: resource.DeploymentOverrides{Traits: map[string]map[string]any{"Auto Scaler": {}}}}, err: "trait instance"},
		{name: "huge config", spec: resource.DeploymentSpec{Component: "c1", Overrides: resource.DeploymentOverrides{Config: map[string]any{"a": strings.Repeat("y", 70<<10)}}}, err: "bytes of JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := resource.ValidateDeploymentSpec(tt.spec)
			if tt.err == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !errors.Is(err, resource.ErrValidation) || !strings.Contains(err.Error(), tt.err) {
				t.Fatalf("err = %v, want a validation error containing %q", err, tt.err)
			}
		})
	}
	tooMany := map[string]string{}
	for i := range 129 {
		tooMany["V"+strings.Repeat("A", i)] = ""
	}
	if err := resource.ValidateDeploymentSpec(resource.DeploymentSpec{Component: "c1", Overrides: resource.DeploymentOverrides{Env: tooMany}}); err == nil {
		t.Error("129 variables were accepted")
	}
}

func path(from string, to ...string) resource.PromotionPath {
	return resource.PromotionPath{From: from, To: to}
}

func TestPipeline(t *testing.T) {
	// dev -> staging -> prod, and dev -> qa (a branch).
	p := resource.Pipeline{Paths: []resource.PromotionPath{path("dev", "staging", "qa"), path("staging", "prod")}}
	if err := resource.ValidatePipeline(p); err != nil {
		t.Fatal(err)
	}
	if got := p.First(); got != "dev" {
		t.Errorf("First() = %q", got)
	}
	if got := p.Environments(); !slices.Equal(got, []string{"dev", "staging", "qa", "prod"}) {
		t.Errorf("Environments() = %v", got)
	}
	for _, tt := range []struct {
		from, to string
		ok       bool
	}{
		{"dev", "staging", true}, {"dev", "qa", true}, {"staging", "prod", true},
		{"dev", "prod", false}, {"prod", "dev", false}, {"qa", "prod", false}, {"staging", "staging", false},
	} {
		if got := p.Allows(tt.from, tt.to); got != tt.ok {
			t.Errorf("Allows(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.ok)
		}
	}
	var empty resource.Pipeline
	if empty.First() != "" || empty.Allows("a", "b") || resource.ValidatePipeline(empty) != nil {
		t.Error("an empty pipeline should be a valid single stage")
	}
}

func TestValidatePipeline(t *testing.T) {
	tests := []struct {
		name  string
		paths []resource.PromotionPath
		err   string
	}{
		{name: "a path to itself", paths: []resource.PromotionPath{path("dev", "dev")}, err: "promotes to itself"},
		{name: "two paths from one", paths: []resource.PromotionPath{path("dev", "a"), path("dev", "b")}, err: "more than one path"},
		{name: "a target twice", paths: []resource.PromotionPath{path("dev", "a", "a")}, err: "listed twice"},
		{name: "no targets", paths: []resource.PromotionPath{path("dev")}, err: "at least one"},
		{name: "no source", paths: []resource.PromotionPath{path("", "a")}, err: "Environment ID"},
		{name: "a bad target", paths: []resource.PromotionPath{path("dev", "a/b")}, err: "isn't an Environment ID"},
		{name: "two starts", paths: []resource.PromotionPath{path("a", "c"), path("b", "c")}, err: "exactly one"},
		{name: "a loop with a start", paths: []resource.PromotionPath{path("dev", "a"), path("a", "b"), path("b", "a")}, err: "cycle"},
		{name: "a loop and nothing else", paths: []resource.PromotionPath{path("a", "b"), path("b", "a")}, err: "exactly one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := resource.ValidatePipeline(resource.Pipeline{Paths: tt.paths})
			if err == nil || !errors.Is(err, resource.ErrValidation) || !strings.Contains(err.Error(), tt.err) {
				t.Fatalf("err = %v, want one containing %q", err, tt.err)
			}
		})
	}
}

func TestProjectSpecJSON(t *testing.T) {
	// A Project without a pipeline stays "{}", as stored before spec 020.
	b, err := json.Marshal(resource.ProjectSpec{})
	if err != nil || string(b) != "{}" {
		t.Errorf("empty ProjectSpec = %s, %v", b, err)
	}
	var got resource.ProjectSpec
	if err := json.Unmarshal([]byte(`{"pipeline":{"paths":[{"from":"dev","to":["prod"]}]}}`), &got); err != nil || !got.Pipeline.Allows("dev", "prod") {
		t.Errorf("decoded %+v, %v", got, err)
	}
	b, _ = json.Marshal(resource.DeploymentSpec{Component: "c1"})
	if string(b) != `{"component":"c1"}` {
		t.Errorf("minimal DeploymentSpec = %s", b)
	}
}

func TestDeploymentRelationships(t *testing.T) {
	rels := resource.DeploymentRelationships(resource.DeploymentSpec{Component: "c1"})
	want := []resource.Relationship{{Type: resource.EdgeDependsOn, TargetKind: resource.KindComponent, TargetID: "c1"}}
	if !slices.Equal(rels, want) {
		t.Errorf("relationships = %+v", rels)
	}
}
