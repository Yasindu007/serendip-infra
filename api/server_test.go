package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
)

// allKindPaths must match server.go's 15 mountResource calls exactly --
// this is the smoke-test surface, not a duplicate of T011's deep CRUD
// proof (that's TestResourceHandler_CRUDCycle, one kind, every verb).
// "volumes" added by specs/007-storage-local-path-provider T007 --
// proves the generic ResourceHandler pattern needs zero new code for a
// new kind, same empirical check this project has run for every kind
// since spec 002.
var allKindPaths = []string{
	"organizations", "teams", "projects", "environments",
	"sites", "clusters", "nodes",
	"applications", "components",
	"capabilities", "providers", "provider-instances",
	"incidents", "alerts", "volumes",
}

func TestNewServer_AllKindsRespondToPostAndGet(t *testing.T) {
	db := newTestDB(t)
	srv := api.NewServer(db, event.NewInProcessBus(), map[string]string{"key-alice": "alice"}, nil)

	for _, path := range allKindPaths {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/"+path, strings.NewReader(`{"metadata":{"name":"smoke-test"}}`))
			req.Header.Set("Authorization", "Bearer key-alice")
			srv.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusCreated {
				t.Fatalf("POST /v1/%s status = %d, want %d, body: %s", path, w.Code, http.StatusCreated, w.Body)
			}

			var created struct {
				Metadata struct {
					ID string `json:"id"`
				} `json:"metadata"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatalf("decode create response: %v", err)
			}
			if created.Metadata.ID == "" {
				t.Fatal("created resource has no id")
			}

			w = httptest.NewRecorder()
			req = httptest.NewRequest(http.MethodGet, "/v1/"+path+"/"+created.Metadata.ID, nil)
			req.Header.Set("Authorization", "Bearer key-alice")
			srv.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("GET /v1/%s/%s status = %d, want %d, body: %s", path, created.Metadata.ID, w.Code, http.StatusOK, w.Body)
			}
		})
	}
}

func TestNewServer_Healthz_NoAuthRequired(t *testing.T) {
	db := newTestDB(t)
	srv := api.NewServer(db, event.NewInProcessBus(), map[string]string{"key-alice": "alice"}, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil) // deliberately no Authorization header
	srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestNewServer_V1Routes_RequireAuth(t *testing.T) {
	db := newTestDB(t)
	srv := api.NewServer(db, event.NewInProcessBus(), map[string]string{"key-alice": "alice"}, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/organizations", nil) // no Authorization header
	srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/organizations (no token) status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// T019 gap fix: an unmatched route must still be RFC 7807 problem+json
// (acceptance criterion 4, "every error response"), not Go's default
// plain-text 404 -- caught by contract-guardian's holistic final review.
func TestNewServer_UnmatchedRoute_IsProblemJSON(t *testing.T) {
	db := newTestDB(t)
	srv := api.NewServer(db, event.NewInProcessBus(), map[string]string{"key-alice": "alice"}, nil)

	tests := []struct {
		name string
		path string
	}{
		{"unmatched under /v1/", "/v1/no-such-kind"},
		{"unmatched top-level", "/bogus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("Authorization", "Bearer key-alice")
			srv.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d, body: %s", w.Code, http.StatusNotFound, w.Body)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", ct)
			}
			var p api.Problem
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatalf("decode Problem body: %v (body: %s)", err, w.Body)
			}
			if p.Status != http.StatusNotFound {
				t.Errorf("Problem.Status = %d, want %d", p.Status, http.StatusNotFound)
			}
		})
	}
}
