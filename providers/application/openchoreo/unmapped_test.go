package openchoreo

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

var testOrigin = &resource.TemplateOrigin{
	Template: "0190a6c2-0000-7000-8000-0000000000aa", TemplateName: "web-server",
	Version: "1.0", VersionID: "0190a6c2-0000-7000-8000-0000000000ab",
	By: "user:alice", At: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC),
	Parameters: map[string]any{"image": "nginx:1.27-alpine", "port": float64(80)},
}

// TestEnsure_UnmappedFieldLeavesWorkload checks that a field the provider
// doesn't map onto the platform's objects changes neither the Workload,
// the releases nor anything else written (spec 027 plan decision 9: a
// template's createdFrom must not roll anything).
func TestEnsure_UnmappedFieldLeavesWorkload(t *testing.T) {
	cases := []struct {
		name string
		app  func(resource.Application) resource.Application
		comp func(resource.ComponentSpec) resource.ComponentSpec
	}{
		{name: "component createdFrom", comp: func(s resource.ComponentSpec) resource.ComponentSpec {
			s.CreatedFrom = testOrigin
			return s
		}},
		{name: "application createdFrom", app: func(a resource.Application) resource.Application {
			a.Spec.CreatedFrom = testOrigin
			return a
		}},
		// secretEnv reaches the ReleaseBinding through the Deployment path,
		// never through EnsureComponent.
		{name: "component secretEnv", comp: func(s resource.ComponentSpec) resource.ComponentSpec {
			s.SecretEnv = []resource.ComponentSecretEnv{{Env: "DB_PASSWORD", Secret: "0190a6c2-0000-7000-8000-000000000001", Key: "password"}}
			return s
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeAPI().withComponentType()
			p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
			ctx := context.Background()
			if _, _, _, err := p.EnsureApplication(ctx, testApp()); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := ensureComp(p, ctx, testApp(), testComp(webSpec)); err != nil {
				t.Fatal(err)
			}
			wlPath := nsItem("workloads", componentName(testCompID))
			genBefore := api.objects[wlPath].Metadata.Generation
			before, err := p.Releases(ctx, testComp(webSpec))
			if err != nil {
				t.Fatal(err)
			}
			api.calls, api.writes = nil, 0

			app, comp := testApp(), testComp(webSpec)
			if tc.app != nil {
				app = tc.app(app)
			}
			if tc.comp != nil {
				comp = testComp(tc.comp(webSpec))
			}
			if _, _, _, err := p.EnsureApplication(ctx, app); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := ensureComp(p, ctx, app, comp); err != nil {
				t.Fatal(err)
			}
			if g := api.objects[wlPath].Metadata.Generation; g != genBefore {
				t.Errorf("Workload generation %d -> %d, want unchanged", genBefore, g)
			}
			if n := api.count(http.MethodPost) + api.count(http.MethodPatch); n != 0 || api.writes != 0 {
				t.Errorf("ensure after an unmapped change: %d create/merge writes, %d stored applies (%v), want none", n, api.writes, api.calls)
			}
			after, err := p.Releases(ctx, comp)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Errorf("releases %+v -> %+v, want unchanged", before, after)
			}
		})
	}
}
