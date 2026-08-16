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

// allKindPaths must match server.go's 14 mountResource calls exactly --
// this is the smoke-test surface, not a duplicate of T011's deep CRUD
// proof (that's TestResourceHandler_CRUDCycle, one kind, every verb).
var allKindPaths = []string{
	"organizations", "teams", "projects", "environments",
	"sites", "clusters", "nodes",
	"applications", "components",
	"capabilities", "providers", "provider-instances",
	"incidents", "alerts",
}

func TestNewServer_AllKindsRespondToPostAndGet(t *testing.T) {
	db := newTestDB(t)
	srv := api.NewServer(db, event.NewInProcessBus(), map[string]string{"key-alice": "alice"})

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
	srv := api.NewServer(db, event.NewInProcessBus(), map[string]string{"key-alice": "alice"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil) // deliberately no Authorization header
	srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestNewServer_V1Routes_RequireAuth(t *testing.T) {
	db := newTestDB(t)
	srv := api.NewServer(db, event.NewInProcessBus(), map[string]string{"key-alice": "alice"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/organizations", nil) // no Authorization header
	srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/organizations (no token) status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}
