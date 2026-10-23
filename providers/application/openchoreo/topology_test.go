package openchoreo

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// A Component's dependencies reach the platform the way spike finding 8
// verified: on the Workload and in each release, as project-visible
// connections to the target's endpoint with its address in a variable.
func TestDependenciesReachTheWorkloadAndTheRelease(t *testing.T) {
	spec := webSpec
	spec.Dependencies = []resource.ComponentDependency{{Component: "comp-api", Port: "http", Env: "API_URL"}}
	want := `{"endpoints":[{"component":"` + componentName("comp-api") + `","name":"http","visibility":"project","envBindings":{"address":"API_URL"}}]}`

	w := desiredWorkload("app-x", "cmp-x", spec)
	got, _ := json.Marshal(w.Dependencies)
	if !jsonEqual(got, json.RawMessage(want)) {
		t.Errorf("workload dependencies = %s\nwant %s", got, want)
	}
	if none := desiredWorkload("app-x", "cmp-x", webSpec); none.Dependencies != nil {
		t.Error("a Component without dependencies got a dependencies block")
	}

	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	rel, err := p.CreateRelease(context.Background(), testComp(spec))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := p.CreateRelease(context.Background(), testComp(webSpec))
	if err != nil {
		t.Fatal(err)
	}
	if rel.Name == plain.Name {
		t.Error("adding a dependency didn't make a new release")
	}
	var r struct {
		Workload struct {
			Dependencies json.RawMessage `json:"dependencies"`
		} `json:"workload"`
	}
	if err := json.Unmarshal(api.objects[nsItem("componentreleases", rel.Name)].Spec, &r); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(r.Workload.Dependencies, json.RawMessage(want)) {
		t.Errorf("release dependencies = %s", r.Workload.Dependencies)
	}
}
