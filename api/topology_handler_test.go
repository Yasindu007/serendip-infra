package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

func (d *deployWorld) component(t *testing.T, token, app, name string, spec map[string]any) *http.Response {
	t.Helper()
	got := d.do(token, http.MethodPost, "/v1/components", map[string]any{
		"metadata": map[string]any{"name": name, "ownerRefs": []any{map[string]any{"kind": "Application", "id": app}}},
		"spec":     spec,
	})
	return got.Result()
}

func decodeComponent(t *testing.T, r *http.Response) resource.Component {
	t.Helper()
	var c resource.Component
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestComponents_DependenciesAndTopology(t *testing.T) {
	d := newDeployWorld(t)
	apiComp := d.component(t, "tok-devops", d.app, "api", map[string]any{"image": "api:1", "ports": []any{map[string]any{"name": "http", "port": 8080}}})
	if apiComp.StatusCode != http.StatusCreated {
		t.Fatalf("create api: %d", apiComp.StatusCode)
	}
	target := decodeComponent(t, apiComp)

	dep := func(comp, port, env string) map[string]any {
		return map[string]any{"component": comp, "port": port, "env": env}
	}
	front := d.component(t, "tok-devops", d.app, "front", map[string]any{"image": "front:1",
		"dependencies": []any{dep(target.Metadata.ID, "http", "API_URL")}})
	if front.StatusCode != http.StatusCreated {
		t.Fatalf("create front: %d", front.StatusCode)
	}
	caller := decodeComponent(t, front)
	if len(caller.Metadata.Relationships) != 1 || caller.Metadata.Relationships[0].TargetID != target.Metadata.ID || caller.Metadata.Relationships[0].Type != resource.EdgeDependsOn {
		t.Errorf("relationships = %+v, want the server's depends-on edge", caller.Metadata.Relationships)
	}

	// Refused: a missing port, itself, another Application's Component.
	if got := d.component(t, "tok-devops", d.app, "x", map[string]any{"image": "x", "dependencies": []any{dep(target.Metadata.ID, "grpc", "A")}}); got.StatusCode != http.StatusBadRequest {
		t.Errorf("a missing port: %d", got.StatusCode)
	}
	caller.Spec.Dependencies = append(caller.Spec.Dependencies, resource.ComponentDependency{Component: caller.Metadata.ID, Port: "http", Env: "SELF"})
	if got := d.do("tok-devops", http.MethodPut, "/v1/components/"+caller.Metadata.ID, caller); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "itself") {
		t.Errorf("itself: %d %s", got.Code, got.Body)
	}
	other := d.do("tok-owner", http.MethodPost, "/v1/applications", map[string]any{"metadata": map[string]any{"name": "billing",
		"ownerRefs": []any{map[string]any{"kind": "Project", "id": d.proj}}}})
	var otherApp resource.Application
	json.Unmarshal(other.Body.Bytes(), &otherApp)
	if got := d.component(t, "tok-devops", otherApp.Metadata.ID, "y", map[string]any{"image": "y", "dependencies": []any{dep(target.Metadata.ID, "http", "A")}}); got.StatusCode != http.StatusBadRequest {
		t.Errorf("another Application's Component: %d", got.StatusCode)
	}

	// What it calls, and what calls it.
	got := d.do("tok-reader", http.MethodGet, "/v1/components/"+target.Metadata.ID+"/dependencies", nil)
	var links struct {
		Calls    []map[string]string `json:"calls"`
		CalledBy []map[string]string `json:"calledBy"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &links); err != nil || got.Code != http.StatusOK {
		t.Fatalf("dependencies: %d %s", got.Code, got.Body)
	}
	if len(links.Calls) != 0 || len(links.CalledBy) != 1 || links.CalledBy[0]["from"] != caller.Metadata.ID || links.CalledBy[0]["env"] != "API_URL" {
		t.Errorf("api's links = %+v", links)
	}

	// The Application's diagram.
	got = d.do("tok-reader", http.MethodGet, "/v1/applications/"+d.app+"/topology", nil)
	var topo struct {
		Components   []map[string]any    `json:"components"`
		Dependencies []map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &topo); err != nil || got.Code != http.StatusOK {
		t.Fatalf("topology: %d %s", got.Code, got.Body)
	}
	if len(topo.Components) != 3 || len(topo.Dependencies) != 1 || topo.Dependencies[0]["to"] != target.Metadata.ID || topo.Dependencies[0]["port"] != "http" {
		t.Errorf("topology = %+v", topo)
	}
	if got := d.do("tok-envonly", http.MethodGet, "/v1/applications/"+d.app+"/topology", nil); got.Code != http.StatusForbidden {
		t.Errorf("topology without reading the Application: %d", got.Code)
	}

	// A port another Component calls stays until the call is removed.
	target.Spec.Ports = nil
	if got := d.do("tok-devops", http.MethodPut, "/v1/components/"+target.Metadata.ID, target); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "front") {
		t.Errorf("removing a called port: %d %s", got.Code, got.Body)
	}
	// Nor can it move to another Application, or be deleted.
	target = decodeComponent(t, d.do("tok-devops", http.MethodGet, "/v1/components/"+target.Metadata.ID, nil).Result())
	target.Metadata.OwnerRefs = []resource.OwnerRef{{Kind: resource.KindApplication, ID: otherApp.Metadata.ID}}
	if got := d.do("tok-devops", http.MethodPut, "/v1/components/"+target.Metadata.ID, target); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "Application can't change") {
		t.Errorf("moving to another Application: %d %s", got.Code, got.Body)
	}
	if got := d.do("tok-owner", http.MethodDelete, "/v1/components/"+target.Metadata.ID, nil); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "front") {
		t.Errorf("deleting a called Component: %d %s", got.Code, got.Body)
	}
	// Once the call goes, so can it.
	caller = decodeComponent(t, d.do("tok-devops", http.MethodGet, "/v1/components/"+caller.Metadata.ID, nil).Result())
	caller.Spec.Dependencies = nil
	if got := d.do("tok-devops", http.MethodPut, "/v1/components/"+caller.Metadata.ID, caller); got.Code != http.StatusOK || strings.Contains(got.Body.String(), `"relationships"`) {
		t.Errorf("dropping the dependency: %d %s", got.Code, got.Body)
	}
	if got := d.do("tok-owner", http.MethodDelete, "/v1/components/"+target.Metadata.ID, nil); got.Code != http.StatusNoContent {
		t.Errorf("deleting it now: %d %s", got.Code, got.Body)
	}
	if got := d.do("tok-owner", http.MethodGet, "/v1/applications/missing/topology", nil); got.Code != http.StatusNotFound && got.Code != http.StatusForbidden {
		t.Errorf("a missing Application's topology: %d", got.Code)
	}
}

type fakePlatform struct{ err error }

func (f *fakePlatform) Platform(context.Context) (reconcile.PlatformView, error) {
	return reconcile.PlatformView{Runtimes: []reconcile.PlatformRuntime{{Name: "default", Ready: true}},
		ComponentTypes: []reconcile.PlatformComponent{{Name: "serendip-service"}}}, f.err
}

func TestPlatformView(t *testing.T) {
	d := newDeployWorld(t, api.WithPlatformView(&fakePlatform{}))
	got := d.do("tok-owner", http.MethodGet, "/v1/platform", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"name":"serendip-service"`) || !strings.Contains(got.Body.String(), `"traits":[]`) {
		t.Errorf("platform: %d %s", got.Code, got.Body)
	}
	// It's platform-wide: a Reader of one Project doesn't see it.
	if got := d.do("tok-reader", http.MethodGet, "/v1/platform", nil); got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "serendip/platform/read") {
		t.Errorf("a Project's Reader: %d %s", got.Code, got.Body)
	}
	bare := newDeployWorld(t)
	if got := bare.do("tok-owner", http.MethodGet, "/v1/platform", nil); got.Code != http.StatusServiceUnavailable {
		t.Errorf("no platform: %d", got.Code)
	}
}
