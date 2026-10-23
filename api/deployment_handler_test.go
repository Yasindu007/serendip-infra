package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// deployWorld is spec 020's tree: a Project with dev, staging and prod in
// a pipeline, an Application under the Project, and its Component; plus a
// second Project with its own Environment.
type deployWorld struct {
	*authzWorld
	proj, other, dev, staging, prod, elsewhere, app, web string
}

func newDeployWorld(t *testing.T) *deployWorld {
	t.Helper()
	people["tok-devops"] = identity.Principal{Subject: "sub-devops", Name: "devops"}
	people["tok-envonly"] = identity.Principal{Subject: "sub-envonly", Name: "envonly"}
	people["tok-owner-ai"] = identity.Principal{Subject: "sub-owner", Name: "owner", Via: "serendip-mcp"}
	t.Cleanup(func() {
		delete(people, "tok-devops")
		delete(people, "tok-envonly")
		delete(people, "tok-owner-ai")
	})
	d := &deployWorld{}
	d.authzWorld = newAuthzWorld(t, func(w *authzWorld) {
		ctx := context.Background()
		projects := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](w.db, resource.KindProject)
		envs := sqlite.NewStore[resource.EnvironmentSpec, resource.EnvironmentStatus](w.db, resource.KindEnvironment)
		mk := func(id *string, err error) {
			if err != nil {
				t.Fatal(err)
			}
			_ = id
		}
		under := func(kind resource.Kind, id string) []resource.OwnerRef {
			return []resource.OwnerRef{{Kind: kind, ID: id}}
		}
		p, err := projects.Create(ctx, resource.Project{Metadata: resource.Metadata{Name: "p20", OwnerRefs: under(resource.KindOrganization, w.id["org"])}})
		mk(nil, err)
		d.proj = p.Metadata.ID
		o, err := projects.Create(ctx, resource.Project{Metadata: resource.Metadata{Name: "other", OwnerRefs: under(resource.KindOrganization, w.id["org"])}})
		mk(nil, err)
		d.other = o.Metadata.ID
		for _, e := range []struct {
			name, project string
			id            *string
		}{{"dev", d.proj, &d.dev}, {"staging", d.proj, &d.staging}, {"prod", d.proj, &d.prod}, {"elsewhere", d.other, &d.elsewhere}} {
			env, err := envs.Create(ctx, resource.Environment{Metadata: resource.Metadata{Name: e.name, OwnerRefs: under(resource.KindProject, e.project)}})
			mk(nil, err)
			*e.id = env.Metadata.ID
		}
		p.Spec.Pipeline = resource.Pipeline{Paths: []resource.PromotionPath{{From: d.dev, To: []string{d.staging}}, {From: d.staging, To: []string{d.prod}}}}
		_, err = projects.Update(ctx, p)
		mk(nil, err)
		app, err := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](w.db, resource.KindApplication).Create(ctx,
			resource.Application{Metadata: resource.Metadata{Name: "shop", OwnerRefs: under(resource.KindProject, d.proj)}})
		mk(nil, err)
		d.app = app.Metadata.ID
		comp, err := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](w.db, resource.KindComponent).Create(ctx,
			resource.Component{Metadata: resource.Metadata{Name: "web", OwnerRefs: under(resource.KindApplication, d.app)}, Spec: resource.ComponentSpec{Image: "nginx"}})
		mk(nil, err)
		d.web = comp.Metadata.ID

		w.grant("tok-owner", policy.OwnerRoleID, "/")
		grant(t, w.db, resource.PrincipalPerson, "sub-reader", policy.ReaderRoleID, resource.Scope{Kind: resource.KindProject, ID: d.proj})
		grant(t, w.db, resource.PrincipalPerson, "sub-devops", policy.ContributorRoleID, resource.Scope{Kind: resource.KindProject, ID: d.proj})
		// Contributor at dev only: may write deployments there, but can't
		// read the Component, which lives under the Project.
		grant(t, w.db, resource.PrincipalPerson, "sub-envonly", policy.ContributorRoleID, resource.Scope{Kind: resource.KindEnvironment, ID: d.dev})
	})
	return d
}

func (d *deployWorld) deployment(env, release string) map[string]any {
	spec := map[string]any{"component": d.web}
	if release != "" {
		spec["release"] = release
	}
	return map[string]any{"metadata": map[string]any{"name": "web", "ownerRefs": []any{map[string]any{"kind": "Environment", "id": env}}}, "spec": spec}
}

func decodeDeployment(t *testing.T, body []byte) resource.Deployment {
	t.Helper()
	var dep resource.Deployment
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return dep
}

// running pretends the reconciler ran d with release.
func (d *deployWorld) running(t *testing.T, id, release string) {
	t.Helper()
	s := sqlite.NewStore[resource.DeploymentSpec, resource.DeploymentStatus](d.db, resource.KindDeployment)
	dep, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	dep.Status.History = resource.RecordRelease(dep.Status.History, dep.Status.Release, release)
	dep.Status.Release = release
	if _, err := s.Update(context.Background(), dep); err != nil {
		t.Fatal(err)
	}
}

func TestDeployments_AlongThePipeline(t *testing.T) {
	d := newDeployWorld(t)

	// A new release enters at the pipeline's first Environment.
	got := d.do("tok-devops", http.MethodPost, "/v1/deployments", d.deployment(d.dev, ""))
	if got.Code != http.StatusCreated {
		t.Fatalf("deploy to dev: %d %s", got.Code, got.Body)
	}
	dev := decodeDeployment(t, got.Body.Bytes())
	if len(dev.Metadata.Relationships) != 1 || dev.Metadata.Relationships[0].TargetID != d.web || dev.Metadata.Relationships[0].Type != resource.EdgeDependsOn {
		t.Errorf("relationships = %+v, want the server's depends-on edge", dev.Metadata.Relationships)
	}
	// Not straight into staging, and once per Environment.
	if got := d.do("tok-devops", http.MethodPost, "/v1/deployments", d.deployment(d.staging, "")); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "first Environment") {
		t.Errorf("latest into staging: %d %s", got.Code, got.Body)
	}
	if got := d.do("tok-devops", http.MethodPost, "/v1/deployments", d.deployment(d.dev, "")); got.Code != http.StatusConflict {
		t.Errorf("a second Deployment in dev: %d %s", got.Code, got.Body)
	}

	// Nothing to promote until dev runs something.
	if got := d.do("tok-devops", http.MethodPost, "/v1/deployments/"+dev.Metadata.ID+"/promote", map[string]any{"to": d.staging}); got.Code != http.StatusConflict {
		t.Errorf("promoting before anything runs: %d %s", got.Code, got.Body)
	}
	d.running(t, dev.Metadata.ID, "cmp-web-r1")

	// Skipping staging is refused; promoting to it creates its Deployment.
	if got := d.do("tok-devops", http.MethodPost, "/v1/deployments/"+dev.Metadata.ID+"/promote", map[string]any{"to": d.prod}); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "promotes here") {
		t.Errorf("dev to prod: %d %s", got.Code, got.Body)
	}
	got = d.do("tok-devops", http.MethodPost, "/v1/deployments/"+dev.Metadata.ID+"/promote", map[string]any{"to": d.staging})
	if got.Code != http.StatusCreated {
		t.Fatalf("dev to staging: %d %s", got.Code, got.Body)
	}
	staging := decodeDeployment(t, got.Body.Bytes())
	if staging.Spec.Release != "cmp-web-r1" || staging.Spec.Component != d.web {
		t.Errorf("staging = %+v", staging.Spec)
	}
	d.running(t, staging.Metadata.ID, "cmp-web-r1")

	// staging keeps its own overrides across a promotion.
	staging = decodeDeployment(t, d.do("tok-devops", http.MethodGet, "/v1/deployments/"+staging.Metadata.ID, nil).Body.Bytes())
	staging.Spec.Overrides = resource.DeploymentOverrides{Env: map[string]string{"TIER": "staging"}}
	got = d.do("tok-devops", http.MethodPut, "/v1/deployments/"+staging.Metadata.ID, staging)
	if got.Code != http.StatusOK {
		t.Fatalf("set staging overrides: %d %s", got.Code, got.Body)
	}
	d.running(t, dev.Metadata.ID, "cmp-web-r2")
	got = d.do("tok-devops", http.MethodPost, "/v1/deployments/"+dev.Metadata.ID+"/promote", map[string]any{"to": d.staging})
	if got.Code != http.StatusOK {
		t.Fatalf("second promotion: %d %s", got.Code, got.Body)
	}
	staging = decodeDeployment(t, got.Body.Bytes())
	if staging.Spec.Release != "cmp-web-r2" || staging.Spec.Overrides.Env["TIER"] != "staging" {
		t.Errorf("staging after promotion = %+v", staging.Spec)
	}
	d.running(t, staging.Metadata.ID, "cmp-web-r2")

	// Rollback: staging may go back to what it ran before…
	staging = decodeDeployment(t, d.do("tok-devops", http.MethodGet, "/v1/deployments/"+staging.Metadata.ID, nil).Body.Bytes())
	staging.Spec.Release = "cmp-web-r1"
	if got := d.do("tok-devops", http.MethodPut, "/v1/deployments/"+staging.Metadata.ID, staging); got.Code != http.StatusOK {
		t.Errorf("rollback in staging: %d %s", got.Code, got.Body)
	}
	// …but not to a release it never ran that dev doesn't run.
	staging = decodeDeployment(t, d.do("tok-devops", http.MethodGet, "/v1/deployments/"+staging.Metadata.ID, nil).Body.Bytes())
	staging.Spec.Release = "cmp-web-r9"
	if got := d.do("tok-devops", http.MethodPut, "/v1/deployments/"+staging.Metadata.ID, staging); got.Code != http.StatusBadRequest {
		t.Errorf("an unknown release in staging: %d %s", got.Code, got.Body)
	}
	// Every step is audited with its actor, and a promotion's row names
	// every decision: the route's, writing at the target and reading the
	// Component.
	var promotions int
	rows, err := d.db.QueryContext(context.Background(), `SELECT actor, COALESCE(policy_decision, '') FROM audit_log WHERE resource_id = ?`, staging.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var actor, decision string
		if err := rows.Scan(&actor, &decision); err != nil {
			t.Fatal(err)
		}
		if actor == "person:devops" && strings.Contains(decision, "allow: Contributor") && strings.Contains(decision, "serendip/deployments/write at Environment/"+d.staging) &&
			strings.Contains(decision, "serendip/components/read at Component/"+d.web) {
			promotions++
		}
	}
	if promotions != 2 {
		t.Errorf("audit rows naming both promotion decisions = %d, want 2", promotions)
	}

	// dev moves on; staging may still edit its overrides while keeping its
	// own release, and promoting what's already there writes nothing.
	d.running(t, dev.Metadata.ID, "cmp-web-r3")
	staging = decodeDeployment(t, d.do("tok-devops", http.MethodGet, "/v1/deployments/"+staging.Metadata.ID, nil).Body.Bytes())
	staging.Spec.Release = "cmp-web-r2"
	got = d.do("tok-devops", http.MethodPut, "/v1/deployments/"+staging.Metadata.ID, staging)
	if got.Code != http.StatusOK {
		t.Fatalf("back to r2: %d %s", got.Code, got.Body)
	}
	staging = decodeDeployment(t, got.Body.Bytes())
	staging.Spec.Overrides.Env["TIER"] = "staging-2"
	d.running(t, dev.Metadata.ID, "cmp-web-r4")
	got = d.do("tok-devops", http.MethodPut, "/v1/deployments/"+staging.Metadata.ID, staging)
	if got.Code != http.StatusOK {
		t.Fatalf("editing overrides behind the pipeline: %d %s", got.Code, got.Body)
	}
	staging = decodeDeployment(t, got.Body.Bytes())
	d.running(t, staging.Metadata.ID, staging.Spec.Release)
	first := d.do("tok-devops", http.MethodPost, "/v1/deployments/"+staging.Metadata.ID+"/promote", map[string]any{"to": d.prod})
	if first.Code != http.StatusCreated {
		t.Fatalf("staging to prod: %d %s", first.Code, first.Body)
	}
	prodDep := decodeDeployment(t, first.Body.Bytes())
	again := d.do("tok-devops", http.MethodPost, "/v1/deployments/"+staging.Metadata.ID+"/promote", map[string]any{"to": d.prod})
	if again.Code != http.StatusOK || decodeDeployment(t, again.Body.Bytes()).Metadata.ResourceVersion != prodDep.Metadata.ResourceVersion {
		t.Errorf("a repeat promotion: %d %s", again.Code, again.Body)
	}

	// The Environment and Component are fixed once created.
	moved := staging
	moved.Metadata.OwnerRefs = []resource.OwnerRef{{Kind: resource.KindEnvironment, ID: d.prod}}
	moved.Spec.Release = "cmp-web-r1"
	if got := d.do("tok-devops", http.MethodPut, "/v1/deployments/"+staging.Metadata.ID, moved); got.Code != http.StatusBadRequest {
		t.Errorf("moving a Deployment: %d %s", got.Code, got.Body)
	}
}

// Concurrent creates of one Component in one Environment: exactly one wins.
func TestDeployments_OnePerEnvironmentUnderConcurrency(t *testing.T) {
	d := newDeployWorld(t)
	codes := make(chan int, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			codes <- d.do("tok-devops", http.MethodPost, "/v1/deployments", d.deployment(d.dev, "")).Code
		})
	}
	wg.Wait()
	close(codes)
	created := 0
	for c := range codes {
		if c == http.StatusCreated {
			created++
		} else if c != http.StatusConflict {
			t.Errorf("unexpected %d", c)
		}
	}
	if created != 1 {
		t.Errorf("%d Deployments created, want 1", created)
	}
}

func TestDeployments_Authorization(t *testing.T) {
	d := newDeployWorld(t)
	if got := d.do("tok-reader", http.MethodPost, "/v1/deployments", d.deployment(d.dev, "")); got.Code != http.StatusForbidden {
		t.Errorf("Reader deploys: %d", got.Code)
	}
	// Writing at the Environment isn't enough: the Component must be readable.
	got := d.do("tok-envonly", http.MethodPost, "/v1/deployments", d.deployment(d.dev, ""))
	if got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "serendip/components/read") {
		t.Errorf("deploy without reading the Component: %d %s", got.Code, got.Body)
	}
	// Deploying stays inside the Project.
	if got := d.do("tok-owner", http.MethodPost, "/v1/deployments", d.deployment(d.elsewhere, "")); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "same Project") {
		t.Errorf("deploy into another Project: %d %s", got.Code, got.Body)
	}
	// Promoting needs writing at the target.
	dev := decodeDeployment(t, d.do("tok-owner", http.MethodPost, "/v1/deployments", d.deployment(d.dev, "")).Body.Bytes())
	d.running(t, dev.Metadata.ID, "cmp-web-r1")
	if got := d.do("tok-reader", http.MethodPost, "/v1/deployments/"+dev.Metadata.ID+"/promote", map[string]any{"to": d.staging}); got.Code != http.StatusForbidden {
		t.Errorf("Reader promotes: %d %s", got.Code, got.Body)
	}
	// An AI acting for the Owner may promote (a write), but its undeploy
	// waits for a person.
	if got := d.do("tok-owner-ai", http.MethodPost, "/v1/deployments/"+dev.Metadata.ID+"/promote", map[string]any{"to": d.staging}); got.Code != http.StatusCreated {
		t.Errorf("the Owner's assistant promotes: %d %s", got.Code, got.Body)
	}
	if got := d.do("tok-owner-ai", http.MethodDelete, "/v1/deployments/"+dev.Metadata.ID, nil); got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "needs-approval") {
		t.Errorf("the Owner's assistant undeploys: %d %s", got.Code, got.Body)
	}
	// The Owner's own undeploy is approved by policy, as any delete.
	if got := d.do("tok-owner", http.MethodDelete, "/v1/deployments/"+dev.Metadata.ID, nil); got.Code != http.StatusNoContent || got.Header().Get("Serendip-Approval-Id") == "" {
		t.Errorf("the Owner undeploys: %d %s", got.Code, got.Body)
	}
}

func TestProjects_PipelineNamesItsOwnEnvironments(t *testing.T) {
	d := newDeployWorld(t)
	get := d.do("tok-owner", http.MethodGet, "/v1/projects/"+d.proj, nil)
	var p resource.Project
	if err := json.Unmarshal(get.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	p.Spec.Pipeline.Paths = append(p.Spec.Pipeline.Paths, resource.PromotionPath{From: d.prod, To: []string{d.elsewhere}})
	if got := d.do("tok-owner", http.MethodPut, "/v1/projects/"+d.proj, p); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "another Project") {
		t.Errorf("a pipeline into another Project: %d %s", got.Code, got.Body)
	}
	p.Spec.Pipeline.Paths = []resource.PromotionPath{{From: d.dev, To: []string{d.dev}}}
	if got := d.do("tok-owner", http.MethodPut, "/v1/projects/"+d.proj, p); got.Code != http.StatusBadRequest {
		t.Errorf("an invalid pipeline: %d %s", got.Code, got.Body)
	}
	created := d.do("tok-owner", http.MethodPost, "/v1/projects", map[string]any{"metadata": map[string]any{"name": "fresh"},
		"spec": map[string]any{"pipeline": map[string]any{"paths": []any{map[string]any{"from": d.dev, "to": []string{d.staging}}}}}})
	if created.Code != http.StatusBadRequest {
		t.Errorf("a new Project with a pipeline: %d %s", created.Code, created.Body)
	}
}
