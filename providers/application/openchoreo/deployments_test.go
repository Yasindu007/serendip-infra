package openchoreo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

const testEnvID = "env-id-1"

func testEnv() resource.Environment {
	return resource.Environment{Metadata: resource.Metadata{ID: testEnvID, Name: "staging"}}
}

func testDeployment(spec resource.DeploymentSpec) resource.Deployment {
	if spec.Component == "" {
		spec.Component = testCompID
	}
	return resource.Deployment{Metadata: resource.Metadata{ID: "dep-1", Name: "web"}, Spec: spec}
}

func bindingPath() string {
	return nsItem("releasebindings", deploymentBindingName(testCompID, testEnvID))
}

func prbPath() string {
	return nsItem("projectreleasebindings", projectEnvBindingName(projectName(testAppID), testEnvID))
}

func TestEnvironments(t *testing.T) {
	api := newFakeAPI()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	if err := p.EnsureEnvironment(context.Background(), testEnv()); err != nil {
		t.Fatal(err)
	}
	key := nsItem("environments", environmentObjectName(testEnvID))
	env, ok := api.objects[key]
	if !ok {
		t.Fatal("Environment not created")
	}
	var spec struct {
		DataPlaneRef struct{ Kind, Name string } `json:"dataPlaneRef"`
	}
	json.Unmarshal(env.Spec, &spec)
	if spec.DataPlaneRef.Kind != "DataPlane" || spec.DataPlaneRef.Name != DefaultDataPlaneName || env.Metadata.Labels[labelSerendipID] != testEnvID {
		t.Errorf("Environment = %s %v", env.Spec, env.Metadata.Labels)
	}
	if err := p.DeleteEnvironment(context.Background(), testEnv()); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.objects[key]; ok {
		t.Error("Environment still present")
	}
	if err := p.DeleteEnvironment(context.Background(), testEnv()); err != nil {
		t.Errorf("deleting a gone Environment = %v", err)
	}
}

func TestEnsurePipeline(t *testing.T) {
	api := newFakeAPI()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	project := resource.Project{Metadata: resource.Metadata{ID: "proj-1", Name: "shop"},
		Spec: resource.ProjectSpec{Pipeline: resource.Pipeline{Paths: []resource.PromotionPath{
			{From: "dev", To: []string{"staging"}}, {From: "staging", To: []string{"prod"}},
		}}}}
	if err := p.EnsurePipeline(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	var spec struct {
		PromotionPaths []struct {
			Source  struct{ Kind, Name string }   `json:"sourceEnvironmentRef"`
			Targets []struct{ Kind, Name string } `json:"targetEnvironmentRefs"`
		} `json:"promotionPaths"`
	}
	json.Unmarshal(api.objects[nsItem("deploymentpipelines", pipelineName("proj-1"))].Spec, &spec)
	if len(spec.PromotionPaths) != 2 || spec.PromotionPaths[0].Source.Name != environmentObjectName("dev") ||
		spec.PromotionPaths[0].Targets[0].Name != environmentObjectName("staging") || spec.PromotionPaths[1].Targets[0].Name != environmentObjectName("prod") {
		t.Errorf("pipeline = %+v", spec)
	}
	// An empty pipeline is an empty list, which the platform accepts.
	if err := p.EnsurePipeline(context.Background(), resource.Project{Metadata: resource.Metadata{ID: "proj-2"}}); err != nil {
		t.Fatal(err)
	}
	if got := string(api.objects[nsItem("deploymentpipelines", pipelineName("proj-2"))].Spec); got != `{"promotionPaths":[]}` {
		t.Errorf("empty pipeline = %s", got)
	}
}

func TestCreateRelease(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	rel, err := p.CreateRelease(context.Background(), testComp(webSpec))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rel.Name, componentName(testCompID)+"-") || rel.Image != "nginx:1.27-alpine" {
		t.Errorf("release = %+v", rel)
	}
	obj := api.objects[nsItem("componentreleases", rel.Name)]
	var spec struct {
		ComponentType struct {
			Kind, Name string
			Spec       json.RawMessage
		} `json:"componentType"`
		Owner    ownerRef                   `json:"owner"`
		Workload map[string]json.RawMessage `json:"workload"`
	}
	if err := json.Unmarshal(obj.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.ComponentType.Kind != "ClusterComponentType" || spec.ComponentType.Name != componentTypeWorkload+"/"+componentTypeName ||
		!jsonEqual(spec.ComponentType.Spec, mustJSON(componentTypeSpec())) {
		t.Errorf("componentType = %s %s", spec.ComponentType.Kind, spec.ComponentType.Name)
	}
	if spec.Owner != (ownerRef{ProjectName: projectName(testAppID), ComponentName: componentName(testCompID)}) {
		t.Errorf("owner = %+v", spec.Owner)
	}
	// The platform refuses an owner inside the workload (T005 spike).
	if _, ok := spec.Workload["owner"]; ok || spec.Workload["container"] == nil {
		t.Errorf("workload = %v", spec.Workload)
	}
	// The same content is the same release: no new write.
	api.writes = 0
	again, err := p.CreateRelease(context.Background(), testComp(webSpec))
	if err != nil || again.Name != rel.Name || api.writes != 0 {
		t.Errorf("second CreateRelease = %+v, %v, %d writes", again, err, api.writes)
	}
	// A Component with no Application can't name its project.
	if _, err := p.CreateRelease(context.Background(), resource.Component{Metadata: resource.Metadata{ID: "c"}, Spec: webSpec}); err == nil {
		t.Error("released a Component with no Application")
	}
	// Without the component type there's nothing to snapshot.
	bare := newFakeAPI()
	if _, err := NewProvider(newTestKubeClient(t, bare.handler(t)), testGateway).CreateRelease(context.Background(), testComp(webSpec)); err == nil {
		t.Error("released without a component type")
	}
}

func TestReleases(t *testing.T) {
	api := newFakeAPI()
	put := func(name, component, created, image string) {
		api.put(nsItem("componentreleases", name), kubernetes.Object{
			Metadata: kubernetes.ObjectMeta{Name: name, CreationTimestamp: created},
			Spec: mustJSON(map[string]any{"owner": ownerRef{ComponentName: component},
				"workload": map[string]any{"container": map[string]string{"image": image}}}),
		})
	}
	mine := componentName(testCompID)
	put(mine+"-old", mine, "2026-10-20T10:00:00Z", "web:1")
	put(mine+"-new", mine, "2026-10-22T10:00:00Z", "web:2")
	put("cmp-other-x", "cmp-other", "2026-10-23T10:00:00Z", "other:1")
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	got, err := p.Releases(context.Background(), testComp(webSpec))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != mine+"-new" || got[0].Image != "web:2" || got[1].Name != mine+"-old" || got[0].Created.IsZero() {
		t.Errorf("releases = %+v, want this Component's, newest first", got)
	}
	// Within one second, and with no time at all: still a fixed order.
	put(mine+"-b", mine, "2026-10-22T10:00:00Z", "web:3")
	put(mine+"-x", mine, "", "web:0")
	got, _ = p.Releases(context.Background(), testComp(webSpec))
	var names []string
	for _, r := range got {
		names = append(names, r.Name)
	}
	if want := []string{mine + "-new", mine + "-b", mine + "-old", mine + "-x"}; strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", names, want)
	}
}

// A release's name is its content: stable for the same spec however its
// maps are built, and different when anything in it changes.
func TestReleaseNameIsItsContent(t *testing.T) {
	name := func(spec resource.ComponentSpec) string {
		t.Helper()
		api := newFakeAPI().withComponentType()
		r, err := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway).CreateRelease(context.Background(), testComp(spec))
		if err != nil {
			t.Fatal(err)
		}
		return r.Name
	}
	a := resource.ComponentSpec{Image: "web:1", Env: map[string]string{"A": "1", "B": "2"},
		Ports: []resource.ComponentPort{{Name: "http", Port: 80}, {Name: "metrics", Port: 9090}}}
	b := resource.ComponentSpec{Image: "web:1", Env: map[string]string{"B": "2", "A": "1"},
		Ports: []resource.ComponentPort{{Name: "metrics", Port: 9090}, {Name: "http", Port: 80}}}
	if name(a) != name(b) {
		t.Error("the same spec, built in another order, got another name")
	}
	c := a
	c.Image = "web:2"
	if name(a) == name(c) {
		t.Error("a different image kept the name")
	}
}

func deploy(t *testing.T, p *Provider, d resource.Deployment) reconcile.DeploymentObservation {
	t.Helper()
	obs, err := p.Deploy(context.Background(), testApp(), testComp(webSpec), testEnv(), d)
	if err != nil {
		t.Fatalf("Deploy() = %v", err)
	}
	return obs
}

func TestDeploy_CreatesWhatRunsTheComponent(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	obs := deploy(t, p, testDeployment(resource.DeploymentSpec{Overrides: resource.DeploymentOverrides{
		Env:    map[string]string{"Z": "last", "A": "first"},
		Config: map[string]any{"replicas": 2},
		Traits: map[string]map[string]any{"autoscaler": {"max": 3}},
	}}))
	if obs.Ready || obs.Reason != reconcile.ReasonProgressing {
		t.Errorf("fresh Deploy = (%v, %q), want Progressing until the platform reports", obs.Ready, obs.Reason)
	}
	if _, ok := api.objects[nsItem("environments", environmentObjectName(testEnvID))]; !ok {
		t.Error("the Environment wasn't ensured")
	}
	var prb struct {
		Owner       ownerRef `json:"owner"`
		Environment string   `json:"environment"`
	}
	json.Unmarshal(api.objects[prbPath()].Spec, &prb)
	if prb.Owner.ProjectName != projectName(testAppID) || prb.Environment != environmentObjectName(testEnvID) {
		t.Errorf("project binding = %+v", prb)
	}
	var b bindingSpec
	json.Unmarshal(api.objects[bindingPath()].Spec, &b)
	releases, _ := p.Releases(context.Background(), testComp(webSpec))
	if len(releases) != 1 || b.ReleaseName != releases[0].Name || obs.Release != b.ReleaseName {
		t.Errorf("bound %q, observed %q, releases %v: want the Component's current release", b.ReleaseName, obs.Release, releases)
	}
	if b.State != "Active" || b.Environment != environmentObjectName(testEnvID) ||
		b.Owner != (ownerRef{ProjectName: projectName(testAppID), ComponentName: componentName(testCompID)}) {
		t.Errorf("binding = %+v", b)
	}
	if b.ComponentTypeEnvironmentConfigs["replicas"] != float64(2) || b.TraitEnvironmentConfigs["autoscaler"]["max"] != float64(3) {
		t.Errorf("config overrides = %v %v", b.ComponentTypeEnvironmentConfigs, b.TraitEnvironmentConfigs)
	}
	if b.WorkloadOverrides == nil || len(b.WorkloadOverrides.Container.Env) != 2 || b.WorkloadOverrides.Container.Env[0].Key != "A" {
		t.Errorf("env overrides = %+v, want both, sorted", b.WorkloadOverrides)
	}

	// Overrides removed are dropped from the binding, not left behind.
	deploy(t, p, testDeployment(resource.DeploymentSpec{}))
	var after map[string]json.RawMessage
	json.Unmarshal(api.lastApply(t, bindingPath()).Spec, &after)
	for _, field := range []string{"componentTypeEnvironmentConfigs", "traitEnvironmentConfigs", "workloadOverrides"} {
		if _, ok := after[field]; ok {
			// Server-side apply removes a field the manager stops sending.
			t.Errorf("the apply after removing overrides still sends %s", field)
		}
	}
}

func TestDeploy_PinnedRelease(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	obs := deploy(t, p, testDeployment(resource.DeploymentSpec{Release: "cmp-nope-123"}))
	if obs.Ready || obs.Reason != reconcile.ReasonUnsupported || !strings.Contains(obs.Message, "doesn't exist") {
		t.Errorf("missing release = %+v", obs)
	}
	if _, ok := api.objects[bindingPath()]; ok {
		t.Error("bound a release that doesn't exist")
	}
	// Another Component's release is refused, before anything is written.
	api.put(nsItem("componentreleases", "cmp-other-1"), kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: "cmp-other-1"},
		Spec: mustJSON(map[string]any{"owner": ownerRef{ComponentName: "cmp-someone-else"}})})
	api.writes = 0
	obs = deploy(t, p, testDeployment(resource.DeploymentSpec{Release: "cmp-other-1"}))
	if obs.Reason != reconcile.ReasonUnsupported || !strings.Contains(obs.Message, "isn't a release of this Component") || api.writes != 0 {
		t.Errorf("another Component's release = %+v, %d writes", obs, api.writes)
	}
	// An earlier release of its own is a rollback.
	api.put(nsItem("componentreleases", "cmp-old-1"), kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: "cmp-old-1"},
		Spec: mustJSON(map[string]any{"owner": ownerRef{ComponentName: componentName(testCompID)}})})
	deploy(t, p, testDeployment(resource.DeploymentSpec{Release: "cmp-old-1"}))
	var b bindingSpec
	json.Unmarshal(api.objects[bindingPath()].Spec, &b)
	if b.ReleaseName != "cmp-old-1" {
		t.Errorf("rollback bound %q", b.ReleaseName)
	}
}

func TestDeploy_RepeatStoresNothing(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	d := testDeployment(resource.DeploymentSpec{Overrides: resource.DeploymentOverrides{Env: map[string]string{"A": "1"}, Config: map[string]any{"replicas": 2}}})
	deploy(t, p, d)
	api.writes = 0
	deploy(t, p, d)
	if api.writes != 0 {
		t.Errorf("an unchanged Deploy stored %d objects", api.writes)
	}
}

func TestDeploy_NotYetReported(t *testing.T) {
	for name, obj := range map[string]kubernetes.Object{
		"no Ready condition yet": {Metadata: kubernetes.ObjectMeta{Generation: 1}, Status: mustJSON(map[string]any{"conditions": []any{}})},
		"Ready for an older generation": {Metadata: kubernetes.ObjectMeta{Generation: 3}, Status: mustJSON(map[string]any{"conditions": []any{
			map[string]any{"type": "Ready", "status": "True", "reason": "Ready", "observedGeneration": 2}}})},
	} {
		api := newFakeAPI().withComponentType()
		obj.Metadata.Name = deploymentBindingName(testCompID, testEnvID)
		api.put(bindingPath(), obj)
		p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
		if obs := deploy(t, p, testDeployment(resource.DeploymentSpec{})); obs.Ready || obs.Reason != reconcile.ReasonProgressing {
			t.Errorf("%s: = %+v, want Progressing", name, obs)
		}
	}
}

func TestDeploy_StatusMapping(t *testing.T) {
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
			api := newFakeAPI().withComponentType()
			api.put(bindingPath(), kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: deploymentBindingName(testCompID, testEnvID), Generation: 1},
				Status: readyStatus(tt.status, tt.reason)})
			p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
			obs := deploy(t, p, testDeployment(resource.DeploymentSpec{}))
			if obs.Ready != tt.wantReady || obs.Reason != tt.wantReason {
				t.Errorf("= (%v, %q), want (%v, %q)", obs.Ready, obs.Reason, tt.wantReady, tt.wantReason)
			}
			if !strings.Contains(obs.Message, tt.reason) || !strings.Contains(obs.Message, "upstream detail") {
				t.Errorf("message = %q, want upstream reason and message carried", obs.Message)
			}
		})
	}
}

// A Ready Deployment reports its endpoints, and takes over from the
// Component's binding of before spec 020.
func TestDeploy_ReadyReportsEndpointsAndRemovesTheLegacyBinding(t *testing.T) {
	for _, routesFail := range []bool{false, true} {
		api := newFakeAPI().withComponentType()
		api.lists = map[string][]kubernetes.Object{routesPath: {{
			Metadata: kubernetes.ObjectMeta{Name: "r1", Labels: map[string]string{"openchoreo.dev/endpoint-name": "http"}},
			Status: mustJSON(map[string]any{"parents": []any{map[string]any{
				"parentRef":  map[string]any{"name": testGateway.Name, "namespace": testGateway.Namespace},
				"conditions": []any{map[string]any{"type": "Accepted", "status": "True"}, map[string]any{"type": "ResolvedRefs", "status": "True"}},
			}}}),
		}}}
		if routesFail {
			api.failPath = routesPath
		}
		api.put(prbPath(), kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: "prb"}, Status: mustJSON(map[string]any{"namespace": testCellNamespace})})
		api.put(bindingPath(), kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: deploymentBindingName(testCompID, testEnvID)},
			Status: mustJSON(map[string]any{
				"conditions": []any{map[string]any{"type": "Ready", "status": "True", "reason": "Ready"}},
				"endpoints":  []any{map[string]any{"name": "http", "invokeURL": testInvokeURL}},
			})})
		legacy := nsItem("releasebindings", releaseBindingName(testCompID))
		api.put(legacy, kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: releaseBindingName(testCompID)}})
		p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)

		obs, err := p.Deploy(context.Background(), testApp(), testComp(exposedSpec), testEnv(), testDeployment(resource.DeploymentSpec{}))
		if err != nil {
			t.Fatal(err)
		}
		if !obs.Ready {
			t.Fatalf("routes failing %v: not ready: %+v", routesFail, obs)
		}
		want := 1
		if routesFail {
			want = 0 // routes unknown: running, but no endpoint to show yet
		}
		if len(obs.Endpoints) != want || (want == 1 && obs.Endpoints[0].URL != testInvokeURL) {
			t.Errorf("routes failing %v: endpoints = %+v", routesFail, obs.Endpoints)
		}
		if _, ok := api.objects[legacy]; ok {
			t.Errorf("routes failing %v: the legacy binding wasn't removed", routesFail)
		}
	}
}

func TestDeploy_NotReadyKeepsTheLegacyBinding(t *testing.T) {
	api := newFakeAPI().withComponentType()
	legacy := nsItem("releasebindings", releaseBindingName(testCompID))
	api.put(legacy, kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: releaseBindingName(testCompID)}})
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	deploy(t, p, testDeployment(resource.DeploymentSpec{}))
	if _, ok := api.objects[legacy]; !ok {
		t.Error("the legacy binding went before the Deployment was running")
	}
}

func TestUndeploy(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	deploy(t, p, testDeployment(resource.DeploymentSpec{}))
	// Another Component of the same Application, in the same Environment.
	other := nsItem("releasebindings", deploymentBindingName("comp-id-2", testEnvID))
	api.put(other, kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: deploymentBindingName("comp-id-2", testEnvID)},
		Spec: mustJSON(bindingSpec{Environment: environmentObjectName(testEnvID), Owner: ownerRef{ProjectName: projectName(testAppID), ComponentName: "cmp-2"}})})

	d := testDeployment(resource.DeploymentSpec{})
	if err := p.Undeploy(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.objects[bindingPath()]; ok {
		t.Error("the binding is still there")
	}
	if _, ok := api.objects[prbPath()]; !ok {
		t.Fatal("the project binding went while another Component still runs there")
	}
	delete(api.objects, other)
	// A sibling already being deleted doesn't keep the project binding.
	going := nsItem("releasebindings", deploymentBindingName("comp-id-3", testEnvID))
	api.put(going, kubernetes.Object{Metadata: kubernetes.ObjectMeta{Name: deploymentBindingName("comp-id-3", testEnvID), DeletionTimestamp: "2026-10-23T10:00:00Z"},
		Spec: mustJSON(bindingSpec{Environment: environmentObjectName(testEnvID), Owner: ownerRef{ProjectName: projectName(testAppID), ComponentName: "cmp-3"}})})
	// Undeploying what's already gone is fine, and the project binding goes
	// with the last binding.
	deploy(t, p, testDeployment(resource.DeploymentSpec{}))
	if err := p.Undeploy(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.objects[prbPath()]; ok {
		t.Error("the project binding stayed after the last Deployment there")
	}
	if err := p.Undeploy(context.Background(), d); err != nil {
		t.Errorf("undeploying again = %v", err)
	}
}

// A failure after deciding the project binding goes leaves the
// Deployment's own binding, so a retry can still find the project.
func TestUndeploy_RetryAfterAFailureStillCleansUp(t *testing.T) {
	api := newFakeAPI().withComponentType()
	p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
	deploy(t, p, testDeployment(resource.DeploymentSpec{}))
	api.failPath = namespacedPath("projectreleasebindings")
	if err := p.Undeploy(context.Background(), testDeployment(resource.DeploymentSpec{})); err == nil {
		t.Fatal("Undeploy succeeded with the project binding undeletable")
	}
	if _, ok := api.objects[bindingPath()]; !ok {
		t.Fatal("the Deployment's binding went before the project binding")
	}
	api.failPath = ""
	if err := p.Undeploy(context.Background(), testDeployment(resource.DeploymentSpec{})); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.objects[prbPath()]; ok {
		t.Error("the retry left the project binding")
	}
	if _, ok := api.objects[bindingPath()]; ok {
		t.Error("the retry left the Deployment's binding")
	}
}
