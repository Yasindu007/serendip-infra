package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/id"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// approvalsBenchCount is how many approvals the reads are measured against:
// about a year of a busy edge-1 install (spec 019 T007a, T009).
const approvalsBenchCount = 20_000

// newApprovalsBenchWorld is an Organization > Project > Application with a
// Component, 20,000 approvals (50 of them pending, the rest executed and
// settled), an Owner key, and a Reader at the Project, so reads go through
// real decisions.
func newApprovalsBenchWorld(b *testing.B) (h http.Handler, db *sql.DB, compID string) {
	b.Helper()
	ctx := context.Background()
	db = newTestDB(b)
	org, err := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization).
		Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "org"}})
	if err != nil {
		b.Fatal(err)
	}
	proj, err := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](db, resource.KindProject).
		Create(ctx, resource.Project{Metadata: resource.Metadata{Name: "proj", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindOrganization, ID: org.Metadata.ID}}}})
	if err != nil {
		b.Fatal(err)
	}
	app, err := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, resource.KindApplication).
		Create(ctx, resource.Application{Metadata: resource.Metadata{Name: "app", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: proj.Metadata.ID}}}})
	if err != nil {
		b.Fatal(err)
	}
	comp, err := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](db, resource.KindComponent).
		Create(ctx, resource.Component{Metadata: resource.Metadata{Name: "web", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: app.Metadata.ID}}},
			Spec: resource.ComponentSpec{Image: "nginx:1.27"}})
	if err != nil {
		b.Fatal(err)
	}
	grant(b, db, resource.PrincipalPerson, "sub-reader", policy.ReaderRoleID, resource.Scope{Kind: resource.KindProject, ID: proj.Metadata.ID})

	// Seeded in one transaction with rows shaped as the store writes them:
	// a Create per approval, with its audit row, would take minutes.
	chain := fmt.Sprintf(`[{"kind":"Application","id":%q},{"kind":"Project","id":%q},{"kind":"Organization","id":%q}]`,
		app.Metadata.ID, proj.Metadata.ID, org.Metadata.ID)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO resources
		(id, kind, name, labels, owner_refs, relationships, spec, status, resource_version, created_at, updated_at)
		VALUES (?, 'Approval', ?, 'null', 'null', 'null', ?, ?, 2, ?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	start := time.Now().UTC().Add(-approvalsBenchCount * time.Minute)
	for i := range approvalsBenchCount {
		target := fmt.Sprintf("gone-%d", i) // executed: its target is gone
		status := fmt.Sprintf(`{"phase":"Executed","settled":true,"requestedBy":{"actor":"person:alice"},"ownerChain":%s,`+
			`"blastRadius":[{"kind":"Component","id":%q,"name":"c","lastSpec":{"image":"nginx:1.27"}}],`+
			`"conditions":[{"type":"Verified","status":"True","reason":"Gone","lastTransitionTime":"2026-10-19T00:00:00Z"}]}`, chain, target)
		if i >= approvalsBenchCount-50 {
			target = comp.Metadata.ID + "-" + fmt.Sprint(i) // pending, on live-looking targets
			status = fmt.Sprintf(`{"phase":"Pending","requestedBy":{"actor":"agent:am/a1"},"ownerChain":%s}`, chain)
		}
		at := start.Add(time.Duration(i) * time.Minute).Format("2006-01-02T15:04:05.000000000Z07:00")
		spec := fmt.Sprintf(`{"action":{"verb":"delete","target":{"kind":"Component","id":%q}},"reason":"bench"}`, target)
		if _, err := stmt.ExecContext(ctx, id.New(), "delete c", spec, status, at, at); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}

	authn := &fakeAuthn{tokens: map[string]identity.Principal{"tok": {Subject: "sub-reader", Name: "reader"}}}
	h = api.NewServer(db, event.NewInProcessBus(), map[string]string{"bench-key": "bench"}, authn, nil, nil, nil).Handler
	return h, db, comp.Metadata.ID
}

// timeRequests runs do b.N times, reporting p50 and p99.
func timeRequests(b *testing.B, do func() int, want int) {
	b.Helper()
	if code := do(); code != want {
		b.Fatalf("status %d, want %d", code, want)
	}
	var d []time.Duration
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		start := time.Now()
		do()
		d = append(d, time.Since(start))
	}
	b.StopTimer()
	slices.Sort(d)
	b.ReportMetric(float64(d[len(d)/2].Microseconds()), "p50-µs")
	b.ReportMetric(float64(d[len(d)*99/100].Microseconds()), "p99-µs")
}

func serveAs(h http.Handler, method, path, token, body string) int {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code
}

// BenchmarkApprovals times the approval reads against 20,000 approvals
// (spec 019 T009 target: p99 under 50 ms each).
func BenchmarkApprovals(b *testing.B) {
	h, db, compID := newApprovalsBenchWorld(b)
	b.Run("list-first-page/owner", func(b *testing.B) {
		timeRequests(b, func() int { return serveAs(h, http.MethodGet, "/v1/approvals", "bench-key", "") }, http.StatusOK)
	})
	b.Run("list-first-page/reader", func(b *testing.B) {
		timeRequests(b, func() int { return serveAs(h, http.MethodGet, "/v1/approvals", "tok", "") }, http.StatusOK)
	})
	b.Run("list-pending/owner", func(b *testing.B) {
		timeRequests(b, func() int { return serveAs(h, http.MethodGet, "/v1/approvals?phase=Pending", "bench-key", "") }, http.StatusOK)
	})
	// A request, its duplicate check included: the first creates it, every
	// later one finds it open.
	b.Run("request/owner", func(b *testing.B) {
		body := fmt.Sprintf(`{"action":{"verb":"delete","target":{"kind":"Component","id":%q}},"reason":"bench"}`, compID)
		serveAs(h, http.MethodPost, "/v1/approvals", "bench-key", body) // created once; every timed request finds it open
		timeRequests(b, func() int { return serveAs(h, http.MethodPost, "/v1/approvals", "bench-key", body) }, http.StatusOK)
	})
	b.Run("resync-list", func(b *testing.B) {
		ctx := context.Background()
		timeRequests(b, func() int {
			if _, err := sqlite.ListApprovals(ctx, db, sqlite.ApprovalQuery{Unsettled: true}); err != nil {
				b.Fatal(err)
			}
			return http.StatusOK
		}, http.StatusOK)
	})
}
