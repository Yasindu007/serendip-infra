package api

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

type recordingMux struct{ patterns []string }

func (m *recordingMux) HandleFunc(pattern string, _ func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
}

// TestEveryRouteHasARule is acceptance criterion 9: every /v1 route
// NewServer registers, with the optional secret routes included, has an
// authorization rule, and the table names no route that doesn't exist.
func TestEveryRouteHasARule(t *testing.T) {
	ctx := t.Context()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "routes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	m := &recordingMux{}
	// A zero reconciler is enough: registering never calls it.
	registerAPI(m, db, event.NewInProcessBus(), nil, nil, &reconcile.SecretReconciler{}, nil, nil, serverOptions{})

	table := routeTable()
	for _, p := range m.patterns {
		if _, ok := table[p]; !ok {
			t.Errorf("route %q has no authorization rule", p)
		}
	}
	for p := range table {
		if !slices.Contains(m.patterns, p) {
			t.Errorf("rule for %q matches no registered route", p)
		}
	}
	if len(m.patterns) < 80 {
		t.Errorf("only %d routes registered; the enumeration looks incomplete", len(m.patterns))
	}
}

// TestUnmappedRouteFailsAtStartup: registering a route the table doesn't
// know panics, so a server can't start with an unchecked route.
func TestUnmappedRouteFailsAtStartup(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registering an unmapped route didn't panic")
		}
	}()
	m := &authzMux{mux: http.NewServeMux(), a: &authorizer{table: routeTable()}}
	m.HandleFunc("GET /v1/unmapped", func(http.ResponseWriter, *http.Request) {})
}
