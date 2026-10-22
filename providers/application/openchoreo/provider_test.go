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
	return resource.Component{Metadata: resource.Metadata{ID: testCompID, Name: "web",
		OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: testAppID}}}, Spec: spec}
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

func TestEnsureApplication_CreatesTheProjectOnly(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)

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
	// No Project owner: the shared pipeline of before spec 020.
	if projSpec.DeploymentPipelineRef.Name != deploymentPipeline || projSpec.Type.Kind != "ClusterProjectType" || projSpec.Type.Name != projectTypeName {
		t.Errorf("Project spec = %s", proj.Spec)
	}
	if proj.Metadata.Labels[labelSerendipID] != testAppID || proj.Metadata.Labels[labelSerendipName] != "shop" {
		t.Errorf("Project labels = %v", proj.Metadata.Labels)
	}
	// Where it runs is each Deployment's business: no environment binding.
	for key := range api.objects {
		if strings.Contains(key, "/projectreleasebindings/") {
			t.Errorf("EnsureApplication created %s", key)
		}
	}
}

func TestEnsureApplication_UsesItsProjectsPipeline(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	app := testApp()
	app.Metadata.OwnerRefs = []resource.OwnerRef{{Kind: resource.KindProject, ID: "proj-1"}}
	if _, _, _, err := p.EnsureApplication(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	var projSpec struct {
		DeploymentPipelineRef struct{ Name string } `json:"deploymentPipelineRef"`
	}
	json.Unmarshal(api.objects[nsItem("projects", projectName(testAppID))].Spec, &projSpec)
	if projSpec.DeploymentPipelineRef.Name != pipelineName("proj-1") {
		t.Errorf("pipeline = %q, want the Project's own", projSpec.DeploymentPipelineRef.Name)
	}
	if _, ok := api.objects[nsItem("deploymentpipelines", pipelineName("proj-1"))]; !ok {
		t.Error("the Project's pipeline wasn't created for the Application to reference")
	}
	// An existing pipeline, with paths written by the Project, is left alone.
	key := nsItem("deploymentpipelines", pipelineName("proj-1"))
	pl := api.objects[key]
	pl.Spec = mustJSON(map[string]any{"promotionPaths": []any{map[string]any{"sourceEnvironmentRef": map[string]string{"name": "env-x"}}}})
	api.objects[key] = pl
	if _, _, _, err := p.EnsureApplication(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(api.objects[key].Spec), "env-x") {
		t.Errorf("the existing pipeline was overwritten: %s", api.objects[key].Spec)
	}
}

func TestEnsureApplication_ReadyProject(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	if _, _, _, err := p.EnsureApplication(context.Background(), testApp()); err != nil {
		t.Fatal(err)
	}
	key := nsItem("projects", projectName(testAppID))
	obj := api.objects[key]
	obj.Status = readyStatus("True", "Reconciled")
	api.objects[key] = obj
	api.calls, api.writes = nil, 0

	ready, reason, _, err := p.EnsureApplication(context.Background(), testApp())
	if err != nil {
		t.Fatal(err)
	}
	if !ready || reason != reconcile.ReasonDeployed {
		t.Errorf("EnsureApplication = (%v, %q), want (true, Deployed)", ready, reason)
	}
	if api.count(http.MethodPost) != 0 || api.writes != 0 {
		t.Errorf("second ensure stored something (writes %d, calls %v)", api.writes, api.calls)
	}
}

// Before spec 020 every Application had a binding to one environment. It's
// kept while one of its Components still runs there, and removed once
// their Deployments have taken over.
func TestEnsureApplication_RemovesTheLegacyBindingWhenUnused(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	legacyPRB := nsItem("projectreleasebindings", projectReleaseBindingName(testAppID))
	legacyBinding := nsItem("releasebindings", releaseBindingName(testCompID))
	api.put(legacyPRB, kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: projectReleaseBindingName(testAppID)}})
	api.put(legacyBinding, kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: releaseBindingName(testCompID)},
		Spec: mustJSON(map[string]any{"environment": environmentName, "owner": ownerRef{ProjectName: projectName(testAppID), ComponentName: componentName(testCompID)}})})

	if _, _, _, err := p.EnsureApplication(context.Background(), testApp()); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.objects[legacyPRB]; !ok {
		t.Fatal("the legacy binding went while a Component still runs there")
	}
	delete(api.objects, legacyBinding)
	if _, _, _, err := p.EnsureApplication(context.Background(), testApp()); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.objects[legacyPRB]; ok {
		t.Error("the unused legacy binding wasn't removed")
	}
}

func TestEnsureComponent_FreshCreatesComponentAndWorkload(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)

	ready, reason, _, err := ensureComp(p, context.Background(), testApp(), testComp(webSpec))
	if err != nil {
		t.Fatalf("EnsureComponent() = %v", err)
	}
	if !ready || reason != "Released" {
		t.Errorf("fresh EnsureComponent = (%v, %q), want (true, Released): it runs nowhere until deployed", ready, reason)
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
	if compSpec.Owner.ProjectName != projectName(testAppID) || compSpec.AutoDeploy ||
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

func TestEnsureComponent_UnchangedWorkloadStoresNothing(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	if _, _, _, err := ensureComp(p, context.Background(), testApp(), testComp(webSpec)); err != nil {
		t.Fatal(err)
	}
	api.calls, api.writes = nil, 0

	if _, _, _, err := ensureComp(p, context.Background(), testApp(), testComp(webSpec)); err != nil {
		t.Fatal(err)
	}
	if n := api.count(http.MethodPost) + api.count(http.MethodPatch); n != 0 || api.writes != 0 {
		t.Errorf("no-drift ensure: %d create/merge writes, %d stored applies (%v), want none", n, api.writes, api.calls)
	}
	for _, a := range api.applies {
		if a.fieldManager != kubernetes.FieldManager || a.force != "true" {
			t.Errorf("%s: fieldManager=%q force=%q", a.path, a.fieldManager, a.force)
		}
	}
}

func TestEnsureComponent_ChangedSpecIsANewRelease(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	if _, _, _, err := ensureComp(p, context.Background(), testApp(), testComp(webSpec)); err != nil {
		t.Fatal(err)
	}
	wlPath := nsItem("workloads", componentName(testCompID))
	genBefore := api.objects[wlPath].Metadata.Generation
	before, _ := p.Releases(context.Background(), testComp(webSpec))

	changed := resource.ComponentSpec{
		Image: "nginx:1.28-alpine",
		Ports: []resource.ComponentPort{{Name: "metrics", Port: 9090}},
	}
	ready, reason, msg, err := ensureComp(p, context.Background(), testApp(), testComp(changed))
	if err != nil {
		t.Fatal(err)
	}
	if !ready || reason != "Released" {
		t.Errorf("after change = (%v, %q), want (true, Released)", ready, reason)
	}
	if api.objects[wlPath].Metadata.Generation == genBefore {
		t.Error("Workload generation unchanged; the spec change was not applied")
	}
	// The whole desired spec is applied, owner included (unchanged, so no
	// clash with its immutability); the removed endpoint is simply absent
	// and the API server drops it.
	var got workloadSpec
	json.Unmarshal(api.lastApply(t, wlPath).Spec, &got)
	if got.Owner.ProjectName != projectName(testAppID) || got.Container.Image != "nginx:1.28-alpine" || len(got.Container.Env) != 0 {
		t.Errorf("Workload apply = %+v", got)
	}
	if _, ok := got.Endpoints["http"]; ok || got.Endpoints["metrics"].Port != 9090 || got.Endpoints["metrics"].Type != "TCP" {
		t.Errorf("endpoints = %+v, want only metrics", got.Endpoints)
	}
	after, _ := p.Releases(context.Background(), testComp(changed))
	if len(before) != 1 || len(after) != 2 || !strings.Contains(msg, after[0].Name) && !strings.Contains(msg, after[1].Name) {
		t.Errorf("releases before %v, after %v, message %q: want one new release, named", before, after, msg)
	}
}

func TestEnsureComponent_ZeroPortsUnsupportedWithoutAPICalls(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	ready, reason, _, err := ensureComp(p, context.Background(), testApp(), testComp(resource.ComponentSpec{Image: "busybox"}))
	if err != nil || ready || reason != reconcile.ReasonUnsupported {
		t.Errorf("= (%v, %q, %v), want (false, Unsupported, nil)", ready, reason, err)
	}
	if len(api.calls) != 0 {
		t.Errorf("API calls = %v, want none", api.calls)
	}
}

func TestEnsureComponent_MovedBetweenApplicationsUnsupported(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	if _, _, _, err := ensureComp(p, context.Background(), testApp(), testComp(webSpec)); err != nil {
		t.Fatal(err)
	}
	other := resource.Application{Metadata: resource.Metadata{ID: "app-id-2"}}
	ready, reason, _, err := ensureComp(p, context.Background(), other, testComp(webSpec))
	if err != nil || ready || reason != reconcile.ReasonUnsupported {
		t.Errorf("= (%v, %q, %v), want (false, Unsupported, nil)", ready, reason, err)
	}
}

func TestEnsureComponent_APIErrorReturned(t *testing.T) {
	api := newFakeAPI().withComponentType()
	api.failPath = namespacedPath("components")
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	if _, _, _, err := ensureComp(p, context.Background(), testApp(), testComp(webSpec)); err == nil {
		t.Error("EnsureComponent() = nil error, want the API failure")
	}
}

func TestDelete_RemovesObjectAndIgnoresNotFound(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	if _, _, _, err := p.EnsureApplication(context.Background(), testApp()); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ensureComp(p, context.Background(), testApp(), testComp(webSpec)); err != nil {
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
	api := newFakeAPI().withComponentType()
	api.failPath = namespacedPath("components")
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
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

// ensureComp unpacks EnsureComponent's observation into the workload
// readiness fields most tests assert on.
func ensureComp(p *Provider, ctx context.Context, app resource.Application, comp resource.Component) (bool, string, string, error) {
	obs, err := p.EnsureComponent(ctx, app, comp)
	return obs.Ready, obs.Reason, obs.Message, err
}
