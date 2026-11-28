package openchoreo

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// TestEnsureComponent_UnmappedFieldLeavesWorkload checks that a
// ComponentSpec field the provider doesn't map onto the Workload changes
// neither the Workload nor the releases (spec 027 plan decision 9: a
// template's createdFrom must not roll anything).
func TestEnsureComponent_UnmappedFieldLeavesWorkload(t *testing.T) {
	cases := []struct {
		name   string
		change func(resource.ComponentSpec) resource.ComponentSpec
	}{
		// secretEnv is only a stand-in: EnsureComponent never reads it (the
		// Deployment path does). T002 adds createdFrom, the field this test
		// is for, on the Component and on the Application.
		{"secretEnv (reaches the ReleaseBinding, not the Workload)", func(s resource.ComponentSpec) resource.ComponentSpec {
			s.SecretEnv = []resource.ComponentSecretEnv{{Env: "DB_PASSWORD", Secret: "0190a6c2-0000-7000-8000-000000000001", Key: "password"}}
			return s
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeAPI().withComponentType()
			p := NewProvider(newTestKubeClient(t, api.handler(t)), testGateway)
			ctx := context.Background()
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

			changed := testComp(tc.change(webSpec))
			if _, _, _, err := ensureComp(p, ctx, testApp(), changed); err != nil {
				t.Fatal(err)
			}
			if g := api.objects[wlPath].Metadata.Generation; g != genBefore {
				t.Errorf("Workload generation %d -> %d, want unchanged", genBefore, g)
			}
			if n := api.count(http.MethodPost) + api.count(http.MethodPatch); n != 0 || api.writes != 0 {
				t.Errorf("ensure after an unmapped change: %d create/merge writes, %d stored applies (%v), want none", n, api.writes, api.calls)
			}
			after, err := p.Releases(ctx, changed)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Errorf("releases %+v -> %+v, want unchanged", before, after)
			}
		})
	}
}
