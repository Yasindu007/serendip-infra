package openchoreo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

const (
	testAppID  = "app-id-1"
	testCompID = "comp-id-1"
)

func testApp() resource.Application {
	return resource.Application{Metadata: resource.Metadata{ID: testAppID, Name: "shop"}}
}

func testComp(spec resource.ComponentSpec) resource.Component {
	return resource.Component{Metadata: resource.Metadata{ID: testCompID, Name: "web"}, Spec: spec}
}

var webSpec = resource.ComponentSpec{
	Image: "nginx:1.27-alpine",
	Ports: []resource.ComponentPort{{Name: "http", Port: 80}},
	Env:   map[string]string{"B": "2", "A": "1"},
}

func nsItem(plural, name string) string { return namespacedPath(plural) + "/" + name }

func readyStatus(status, reason string) json.RawMessage {
	return mustJSON(map[string]any{"conditions": []any{
		map[string]any{"type": "Ready", "status": status, "reason": reason, "message": "upstream detail"},
	}})
}

func TestEnsureApplication_FreshCreatesProjectAndBinding(t *testing.T) {
	api := newFakeAPI()
	p := NewProvider(newTestKubeClient(t, api.handler(t)))

	ready, reason, _, err := p.EnsureApplication(context.Background(), testApp())
	if err != nil {
		t.Fatalf("EnsureApplication() = %v", err)
	}
	if ready || reason != reconcile.ReasonProgressing {
		t.Errorf("fresh EnsureApplication = (%v, %q), want (false, Progressing)", ready, reason)
	}

	proj, ok := api.objects[nsItem("projects", projectName(testAppID))]
	if !ok {
		t.Fatal("Project not created")
	}
	var projSpec struct {
		DeploymentPipelineRef struct{ Name string } `json:"deploymentPipelineRef"`
		Type                  struct{ Kind, Name string }
	}
	json.Unmarshal(proj.Spec, &projSpec)
	if projSpec.DeploymentPipelineRef.Name != deploymentPipeline || projSpec.Type.Kind != "ClusterProjectType" || projSpec.Type.Name != projectTypeName {
		t.Errorf("Project spec = %s", proj.Spec)
	}
	if proj.Metadata.Labels[labelSerendipID] != testAppID || proj.Metadata.Labels[labelSerendipName] != "shop" {
		t.Errorf("Project labels = %v", proj.Metadata.Labels)
	}

	prb, ok := api.objects[nsItem("projectreleasebindings", projectReleaseBindingName(testAppID))]
	if !ok {
		t.Fatal("ProjectReleaseBinding not created")
	}
	var prbSpec struct {
		Owner       ownerRef `json:"owner"`
		Environment string   `json:"environment"`
	}
	json.Unmarshal(prb.Spec, &prbSpec)
	if prbSpec.Owner.ProjectName != projectName(testAppID) || prbSpec.Environment != environmentName {
		t.Errorf("ProjectReleaseBinding spec = %s", prb.Spec)
	}
}

func TestEnsureApplication_ReadyBinding(t *testing.T) {
	api := newFakeAPI()
	p := NewProvider(newTestKubeClient(t, api.handler(t)))
	if _, _, _, err := p.EnsureApplication(context.Background(), testApp()); err != nil {
		t.Fatal(err)
	}
	key := nsItem("projectreleasebindings", projectReleaseBindingName(testAppID))
	obj := api.objects[key]
	obj.Status = readyStatus("True", "Ready")
	api.objects[key] = obj
	api.calls = nil

	ready, reason, _, err := p.EnsureApplication(context.Background(), testApp())
	if err != nil {
		t.Fatal(err)
	}
	if !ready || reason != reconcile.ReasonDeployed {
		t.Errorf("EnsureApplication = (%v, %q), want (true, Deployed)", ready, reason)
	}
	if api.count(http.MethodPost) != 0 {
		t.Errorf("unexpected creates on second ensure: %v", api.calls)
	}
}

func TestEnsureComponent_FreshCreatesComponentAndWorkload(t *testing.T) {
	api := newFakeAPI()
	p := NewProvider(newTestKubeClient(t, api.handler(t)))

	ready, reason, _, err := p.EnsureComponent(context.Background(), testApp(), testComp(webSpec))
	if err != nil {
		t.Fatalf("EnsureComponent() = %v", err)
	}
	if ready || reason != reconcile.ReasonProgressing {
		t.Errorf("fresh EnsureComponent = (%v, %q), want (false, Progressing) while no ReleaseBinding exists", ready, reason)
	}

	comp, ok := api.objects[nsItem("components", componentName(testCompID))]
	if !ok {
		t.Fatal("Component not created")
	}
	var compSpec struct {
		Owner         ownerRef `json:"owner"`
		AutoDeploy    bool     `json:"autoDeploy"`
		ComponentType struct{ Kind, Name string }
	}
	json.Unmarshal(comp.Spec, &compSpec)
	if compSpec.Owner.ProjectName != projectName(testAppID) || !compSpec.AutoDeploy ||
		compSpec.ComponentType.Kind != "ClusterComponentType" || compSpec.ComponentType.Name != "deployment/serendip-service" {
		t.Errorf("Component spec = %s", comp.Spec)
	}

	wl, ok := api.objects[nsItem("workloads", componentName(testCompID))]
	if !ok {
		t.Fatal("Workload not created")
	}
	var got workloadSpec
	json.Unmarshal(wl.Spec, &got)
	want := workloadSpec{
		Owner:     ownerRef{ProjectName: projectName(testAppID), ComponentName: componentName(testCompID)},
		Container: workloadContainer{Image: "nginx:1.27-alpine", Env: []envVar{{"A", "1"}, {"B", "2"}}},
		Endpoints: map[string]workloadEndpoint{"http": {Type: "TCP", Port: 80}},
	}
	if string(mustJSON(got)) != string(mustJSON(want)) {
		t.Errorf("Workload spec = %s, want %s", mustJSON(got), mustJSON(want))
	}
}

func TestEnsureComponent_UnchangedWorkloadIsNotPatched(t *testing.T) {
	api := newFakeAPI()
	p := NewProvider(newTestKubeClient(t, api.handler(t)))
	if _, _, _, err := p.EnsureComponent(context.Background(), testApp(), testComp(webSpec)); err != nil {
		t.Fatal(err)
	}
	api.calls = nil

	if _, _, _, err := p.EnsureComponent(context.Background(), testApp(), testComp(webSpec)); err != nil {
		t.Fatal(err)
	}
	if n := api.count(http.MethodPost) + api.count(http.MethodPatch); n != 0 {
		t.Errorf("writes on no-drift ensure = %v, want none", api.calls)
	}
}

func TestEnsureComponent_ChangedSpecPatchesOnlyOwnedFields(t *testing.T) {
	api := newFakeAPI()
	p := NewProvider(newTestKubeClient(t, api.handler(t)))
	if _, _, _, err := p.EnsureComponent(context.Background(), testApp(), testComp(webSpec)); err != nil {
		t.Fatal(err)
	}
	api.calls = nil

	changed := resource.ComponentSpec{
		Image: "nginx:1.28-alpine",
		Ports: []resource.ComponentPort{{Name: "metrics", Port: 9090}},
	}
	// A Ready binding for the previous release must not make the update
	// look Deployed in the same pass that patched the Workload.
	api.put(nsItem("releasebindings", releaseBindingName(testCompID)), kubernetes.Object{Status: readyStatus("True", "Ready")})
	ready, reason, _, err := p.EnsureComponent(context.Background(), testApp(), testComp(changed))
	if err != nil {
		t.Fatal(err)
	}
	if ready || reason != reconcile.ReasonProgressing {
		t.Errorf("after patch = (%v, %q), want (false, Progressing)", ready, reason)
	}
	if n := api.count(http.MethodPatch); n != 1 {
		t.Fatalf("PATCH calls = %d (%v), want 1", n, api.calls)
	}
	var body struct {
		Spec map[string]json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(api.lastPatch, &body); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if _, ok := body.Spec["owner"]; ok {
		t.Error("patch touches immutable spec.owner")
	}
	if got := string(body.Spec["container"]); got != `{"env":null,"image":"nginx:1.28-alpine"}` {
		t.Errorf("patch container = %s", got)
	}
	if got := string(body.Spec["endpoints"]); got != `{"http":null,"metrics":{"type":"TCP","port":9090}}` {
		t.Errorf("patch endpoints = %s, want removed endpoint nulled", got)
	}
}

func TestEnsureComponent_ReleaseBindingStatusMapping(t *testing.T) {
	tests := []struct {
		status, reason string
		wantReady      bool
		wantReason     string
	}{
		{"True", "Ready", true, reconcile.ReasonDeployed},
		{"False", "ResourcesProgressing", false, reconcile.ReasonProgressing},
		{"False", "ResourcesNotReady", false, reconcile.ReasonProgressing},
		{"False", "ResourcesDegraded", false, reconcile.ReasonDeployFailed},
		{"False", "ResourceApplyFailed", false, reconcile.ReasonDeployFailed},
		{"False", "RenderingFailed", false, reconcile.ReasonDeployFailed},
		{"False", "EnvironmentNotFound", false, reconcile.ReasonDeployFailed},
		{"False", "DataPlaneNotFound", false, reconcile.ReasonDeployFailed},
		{"Unknown", "SomethingNew", false, reconcile.ReasonProgressing},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			api := newFakeAPI()
			putBoundRelease(api, webSpec, readyStatus(tt.status, tt.reason))
			p := NewProvider(newTestKubeClient(t, api.handler(t)))

			ready, reason, message, err := p.EnsureComponent(context.Background(), testApp(), testComp(webSpec))
			if err != nil {
				t.Fatal(err)
			}
			if ready != tt.wantReady || reason != tt.wantReason {
				t.Errorf("= (%v, %q), want (%v, %q)", ready, reason, tt.wantReady, tt.wantReason)
			}
			if !strings.Contains(message, tt.reason) || !strings.Contains(message, "upstream detail") {
				t.Errorf("message = %q, want upstream reason and message carried", message)
			}
		})
	}
}

func TestEnsureComponent_NoReadyConditionYetIsProgressing(t *testing.T) {
	api := newFakeAPI()
	putBoundRelease(api, webSpec, nil)
	p := NewProvider(newTestKubeClient(t, api.handler(t)))
	ready, reason, _, err := p.EnsureComponent(context.Background(), testApp(), testComp(webSpec))
	if err != nil || ready || reason != reconcile.ReasonProgressing {
		t.Errorf("= (%v, %q, %v), want (false, Progressing, nil)", ready, reason, err)
	}
}

func TestEnsureComponent_ZeroPortsUnsupportedWithoutAPICalls(t *testing.T) {
	api := newFakeAPI()
	p := NewProvider(newTestKubeClient(t, api.handler(t)))
	ready, reason, _, err := p.EnsureComponent(context.Background(), testApp(), testComp(resource.ComponentSpec{Image: "busybox"}))
	if err != nil || ready || reason != reconcile.ReasonUnsupported {
		t.Errorf("= (%v, %q, %v), want (false, Unsupported, nil)", ready, reason, err)
	}
	if len(api.calls) != 0 {
		t.Errorf("API calls = %v, want none", api.calls)
	}
}

func TestEnsureComponent_MovedBetweenApplicationsUnsupported(t *testing.T) {
	api := newFakeAPI()
	p := NewProvider(newTestKubeClient(t, api.handler(t)))
	if _, _, _, err := p.EnsureComponent(context.Background(), testApp(), testComp(webSpec)); err != nil {
		t.Fatal(err)
	}
	other := resource.Application{Metadata: resource.Metadata{ID: "app-id-2"}}
	ready, reason, _, err := p.EnsureComponent(context.Background(), other, testComp(webSpec))
	if err != nil || ready || reason != reconcile.ReasonUnsupported {
		t.Errorf("= (%v, %q, %v), want (false, Unsupported, nil)", ready, reason, err)
	}
}

func TestEnsureComponent_APIErrorReturned(t *testing.T) {
	api := newFakeAPI()
	api.failPath = namespacedPath("components")
	p := NewProvider(newTestKubeClient(t, api.handler(t)))
	if _, _, _, err := p.EnsureComponent(context.Background(), testApp(), testComp(webSpec)); err == nil {
		t.Error("EnsureComponent() = nil error, want the API failure")
	}
}

func TestDelete_RemovesObjectAndIgnoresNotFound(t *testing.T) {
	api := newFakeAPI()
	p := NewProvider(newTestKubeClient(t, api.handler(t)))
	if _, _, _, err := p.EnsureApplication(context.Background(), testApp()); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := p.EnsureComponent(context.Background(), testApp(), testComp(webSpec)); err != nil {
		t.Fatal(err)
	}

	idOnlyComp := resource.Component{Metadata: resource.Metadata{ID: testCompID}}
	idOnlyApp := resource.Application{Metadata: resource.Metadata{ID: testAppID}}
	if err := p.DeleteComponent(context.Background(), idOnlyComp); err != nil {
		t.Fatalf("DeleteComponent() = %v", err)
	}
	if _, ok := api.objects[nsItem("components", componentName(testCompID))]; ok {
		t.Error("Component CR still present after DeleteComponent")
	}
	if err := p.DeleteApplication(context.Background(), idOnlyApp); err != nil {
		t.Fatalf("DeleteApplication() = %v", err)
	}
	if _, ok := api.objects[nsItem("projects", projectName(testAppID))]; ok {
		t.Error("Project CR still present after DeleteApplication")
	}

	if err := p.DeleteComponent(context.Background(), idOnlyComp); err != nil {
		t.Errorf("second DeleteComponent() = %v, want nil for already-gone", err)
	}
	if err := p.DeleteApplication(context.Background(), idOnlyApp); err != nil {
		t.Errorf("second DeleteApplication() = %v, want nil for already-gone", err)
	}
}

func TestDelete_APIErrorReturned(t *testing.T) {
	api := newFakeAPI()
	api.failPath = namespacedPath("components")
	p := NewProvider(newTestKubeClient(t, api.handler(t)))
	if err := p.DeleteComponent(context.Background(), resource.Component{Metadata: resource.Metadata{ID: testCompID}}); err == nil {
		t.Error("DeleteComponent() = nil, want the API failure")
	}
}

func TestMapReady_StaleObservedGenerationIsProgressing(t *testing.T) {
	obj := kubernetes.Object{
		Metadata: kubernetes.ObjectMeta{Generation: 3},
		Status: mustJSON(map[string]any{"conditions": []any{
			map[string]any{"type": "Ready", "status": "True", "reason": "Ready", "observedGeneration": 2},
		}}),
	}
	if ready, reason, _ := mapReady(obj); ready || reason != reconcile.ReasonProgressing {
		t.Errorf("stale generation = (%v, %q), want (false, Progressing)", ready, reason)
	}
	obj.Metadata.Generation = 2
	if ready, reason, _ := mapReady(obj); !ready || reason != reconcile.ReasonDeployed {
		t.Errorf("current generation = (%v, %q), want (true, Deployed)", ready, reason)
	}
}

func TestMapReady_UnparseableStatus(t *testing.T) {
	ready, reason, message := mapReady(kubernetes.Object{Status: json.RawMessage(`{"conditions":"oops"}`)})
	if ready || reason != reconcile.ReasonProgressing || !strings.Contains(message, "not parseable") {
		t.Errorf("= (%v, %q, %q), want (false, Progressing, parse message)", ready, reason, message)
	}
}

// putBoundRelease stores a ReleaseBinding pointing at a ComponentRelease
// whose workload snapshot was cut from spec.
func putBoundRelease(api *fakeAPI, spec resource.ComponentSpec, status json.RawMessage) {
	api.put(nsItem("releasebindings", releaseBindingName(testCompID)), kubernetes.Object{
		Metadata: kubernetes.ObjectMeta{Name: releaseBindingName(testCompID)},
		Spec:     mustJSON(map[string]string{"releaseName": "rel-1"}),
		Status:   status,
	})
	api.put(nsItem("componentreleases", "rel-1"), kubernetes.Object{
		Spec: mustJSON(map[string]any{"workload": desiredWorkload(projectName(testAppID), componentName(testCompID), spec)}),
	})
}

func TestEnsureComponent_ReadyBindingForStaleReleaseIsProgressing(t *testing.T) {
	api := newFakeAPI()
	old := webSpec
	old.Image = "nginx:1.26-alpine"
	putBoundRelease(api, old, readyStatus("True", "Ready"))
	p := NewProvider(newTestKubeClient(t, api.handler(t)))

	ready, reason, _, err := p.EnsureComponent(context.Background(), testApp(), testComp(webSpec))
	if err != nil || ready || reason != reconcile.ReasonProgressing {
		t.Errorf("= (%v, %q, %v), want (false, Progressing, nil) while the bound release predates the spec", ready, reason, err)
	}
}

func TestEnsureComponent_BoundReleaseMissingIsProgressing(t *testing.T) {
	api := newFakeAPI()
	putBoundRelease(api, webSpec, readyStatus("True", "Ready"))
	delete(api.objects, nsItem("componentreleases", "rel-1"))
	p := NewProvider(newTestKubeClient(t, api.handler(t)))

	ready, reason, _, err := p.EnsureComponent(context.Background(), testApp(), testComp(webSpec))
	if err != nil || ready || reason != reconcile.ReasonProgressing {
		t.Errorf("= (%v, %q, %v), want (false, Progressing, nil)", ready, reason, err)
	}
}
