package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// benchWorld is the ownership chain an authorization decision walks on a
// real install (org > project > environment > application > component),
// with listItems components in one application, plus a sibling project
// whose 20 assignments every decision has to skip.
type benchWorld struct {
	db     *sql.DB
	compID string
	appID  string
	authz  http.Handler // NewServer: authentication and authorization
	plain  http.Handler // the same handlers on a plain mux: no authorization
}

const listItems = 100

func newBenchWorld(b *testing.B) *benchWorld {
	b.Helper()
	ctx := context.Background()
	w := &benchWorld{db: newTestDB(b)}
	mk := func(s func(meta resource.Metadata) (string, error), name string, owner resource.Scope) string {
		meta := resource.Metadata{Name: name}
		if !owner.IsRoot() {
			meta.OwnerRefs = []resource.OwnerRef{{Kind: owner.Kind, ID: owner.ID}}
		}
		id, err := s(meta)
		if err != nil {
			b.Fatal(err)
		}
		return id
	}
	orgs := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](w.db, resource.KindOrganization)
	projs := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](w.db, resource.KindProject)
	envs := sqlite.NewStore[resource.EnvironmentSpec, resource.EnvironmentStatus](w.db, resource.KindEnvironment)
	apps := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](w.db, resource.KindApplication)
	comps := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](w.db, resource.KindComponent)
	in := func(s any) func(resource.Metadata) (string, error) {
		return func(m resource.Metadata) (string, error) {
			switch st := s.(type) {
			case *sqlite.Store[resource.OrganizationSpec, resource.OrganizationStatus]:
				return create(ctx, st, m)
			case *sqlite.Store[resource.ProjectSpec, resource.ProjectStatus]:
				return create(ctx, st, m)
			case *sqlite.Store[resource.EnvironmentSpec, resource.EnvironmentStatus]:
				return create(ctx, st, m)
			case *sqlite.Store[resource.ApplicationSpec, resource.ApplicationStatus]:
				return create(ctx, st, m)
			default:
				r, err := comps.Create(ctx, resource.Component{Metadata: m, Spec: resource.ComponentSpec{Image: "nginx:1.27"}})
				return r.Metadata.ID, err
			}
		}
	}
	org := mk(in(orgs), "org", resource.RootScope)
	proj := mk(in(projs), "proj", resource.Scope{Kind: resource.KindOrganization, ID: org})
	other := mk(in(projs), "other", resource.Scope{Kind: resource.KindOrganization, ID: org})
	env := mk(in(envs), "prod", resource.Scope{Kind: resource.KindProject, ID: proj})
	w.appID = mk(in(apps), "app", resource.Scope{Kind: resource.KindEnvironment, ID: env})
	for i := range listItems {
		id := mk(in(comps), fmt.Sprintf("c%03d", i), resource.Scope{Kind: resource.KindApplication, ID: w.appID})
		if i == 0 {
			w.compID = id
		}
	}
	// The caller is Reader at the project, three levels above the
	// components: every decision walks the chain.
	grant(b, w.db, resource.PrincipalPerson, "sub-reader", policy.ReaderRoleID, resource.Scope{Kind: resource.KindProject, ID: proj})
	for i := range 20 {
		grant(b, w.db, resource.PrincipalPerson, fmt.Sprintf("sub-%d", i), policy.ContributorRoleID, resource.Scope{Kind: resource.KindProject, ID: other})
	}

	// The same Reader grant for an agent, with its sponsor (spec 018).
	agents := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](w.db, resource.KindRoleAssignment)
	if _, err := agents.Create(ctx, resource.RoleAssignment{Metadata: resource.Metadata{Name: "agent"}, Spec: resource.RoleAssignmentSpec{
		Principal:        resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "am-dev/bench", Sponsor: &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "sub-reader"}},
		RoleDefinitionID: policy.ReaderRoleID, Scope: resource.Scope{Kind: resource.KindProject, ID: proj},
	}}); err != nil {
		b.Fatal(err)
	}

	authn := &fakeAuthn{tokens: map[string]identity.Principal{
		"tok":       {Subject: "sub-reader", Name: "reader"},
		"tok-agent": {Kind: identity.KindAgent, Issuer: "am-dev", Subject: "bench", Name: "bench"},
	}}
	w.authz = api.NewServer(w.db, event.NewInProcessBus(), nil, authn, nil, nil, nil).Handler
	mux := http.NewServeMux()
	api.NewResourceHandler(comps, "components").Register(mux)
	w.plain = api.AuthMiddleware(nil, authn)(mux)
	return w
}

func get(h http.Handler, path string) int { return getAs(h, path, "tok") }

func getAs(h http.Handler, path, token string) int {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code
}

// timed runs the request b.N times, reporting p50 and p99 per request:
// the targets are percentiles, which ns/op (a mean) isn't.
func timed(b *testing.B, h http.Handler, path string) { timedAs(b, h, path, "tok") }

func timedAs(b *testing.B, h http.Handler, path, token string) {
	b.Helper()
	if code := getAs(h, path, token); code != http.StatusOK {
		b.Fatalf("GET %s: %d", path, code)
	}
	var d []time.Duration
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		start := time.Now()
		getAs(h, path, token)
		d = append(d, time.Since(start))
	}
	b.StopTimer()
	slices.Sort(d)
	b.ReportMetric(float64(d[len(d)/2].Microseconds()), "p50-µs")
	b.ReportMetric(float64(d[len(d)*99/100].Microseconds()), "p99-µs")
}

// BenchmarkAuthorization measures what authorization adds per request
// (specs/017-authorization plan.md decision 12): the same handlers with
// and without it, for one Component and for a 100-item list filtered item
// by item. The difference between the pairs is authorization's cost.
func BenchmarkAuthorization(b *testing.B) {
	w := newBenchWorld(b)
	one := "/v1/components/" + w.compID
	list := "/v1/components?ownerId=" + w.appID
	b.Run("get/without", func(b *testing.B) { timed(b, w.plain, one) })
	b.Run("get/with", func(b *testing.B) { timed(b, w.authz, one) })
	b.Run("list100/without", func(b *testing.B) { timed(b, w.plain, list) })
	b.Run("list100/with", func(b *testing.B) { timed(b, w.authz, list) })
}

// BenchmarkAgentRequests measures what an agent caller costs over a person
// with the same role (specs/018-agent-manager-poc T010): the sponsor
// lookup in the middleware and the guardrail checks in each decision.
// Token verification is measured separately, in providers/identity/oidc.
func BenchmarkAgentRequests(b *testing.B) {
	w := newBenchWorld(b)
	one := "/v1/components/" + w.compID
	list := "/v1/components?ownerId=" + w.appID
	b.Run("get/person", func(b *testing.B) { timedAs(b, w.authz, one, "tok") })
	b.Run("get/agent", func(b *testing.B) { timedAs(b, w.authz, one, "tok-agent") })
	b.Run("list100/person", func(b *testing.B) { timedAs(b, w.authz, list, "tok") })
	b.Run("list100/agent", func(b *testing.B) { timedAs(b, w.authz, list, "tok-agent") })
}
