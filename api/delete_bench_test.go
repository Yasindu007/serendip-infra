package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// BenchmarkDelete times DELETE /v1/components/{id} through the full server
// (spec 019 T009). It uses only what existed before spec 019, so the same
// file runs on the commit before it and the difference is the cost of
// recording each delete's Approval.
func BenchmarkDelete(b *testing.B) {
	ctx := context.Background()
	db := newTestDB(b)
	apps := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, resource.KindApplication)
	comps := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](db, resource.KindComponent)
	app, err := apps.Create(ctx, resource.Application{Metadata: resource.Metadata{Name: "app"}})
	if err != nil {
		b.Fatal(err)
	}
	h := api.NewServer(db, event.NewInProcessBus(), map[string]string{"bench-key": "bench"}, nil, nil, nil, nil).Handler

	var d []time.Duration
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		c, err := comps.Create(ctx, resource.Component{
			Metadata: resource.Metadata{Name: fmt.Sprintf("c%d", i), OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: app.Metadata.ID}}},
			Spec:     resource.ComponentSpec{Image: "nginx:1.27"},
		})
		if err != nil {
			b.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodDelete, "/v1/components/"+c.Metadata.ID, nil)
		r.Header.Set("Authorization", "Bearer bench-key")
		rec := httptest.NewRecorder()
		b.StartTimer()
		start := time.Now()
		h.ServeHTTP(rec, r)
		d = append(d, time.Since(start))
		if rec.Code != http.StatusNoContent {
			b.Fatalf("DELETE: %d %s", rec.Code, rec.Body)
		}
	}
	b.StopTimer()
	slices.Sort(d)
	b.ReportMetric(float64(d[len(d)/2].Microseconds()), "p50-µs")
	b.ReportMetric(float64(d[len(d)*99/100].Microseconds()), "p99-µs")
}
