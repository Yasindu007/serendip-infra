package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/provider"
)

func TestReadiness_RequiresAuth(t *testing.T) {
	srv := api.NewServer(newTestDB(t), event.NewInProcessBus(), map[string]string{"key-alice": "alice"}, nil, nil, provider.NewRegistry(), nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/readiness", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/readiness (no token) = %d, want 401", w.Code)
	}
}

func TestReadiness_ReportsEachCapability(t *testing.T) {
	reg := provider.NewRegistry()
	reg.SetReadiness("storage", provider.Readiness{Ready: true, Reason: "Ready"})
	reg.SetReadiness("secrets", provider.Readiness{Reason: "SetupFailed", Message: "not ready within 90s"})
	srv := api.NewServer(newTestDB(t), event.NewInProcessBus(), map[string]string{"key-alice": "alice"}, nil, nil, reg, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/readiness", nil)
	req.Header.Set("Authorization", "Bearer key-alice")
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}

	var got map[string]struct {
		Ready   bool   `json:"ready"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
		Since   string `json:"since"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body)
	}
	if s := got["storage"]; !s.Ready || s.Reason != "Ready" || s.Since == "" {
		t.Errorf("storage = %+v", s)
	}
	if s := got["secrets"]; s.Ready || s.Reason != "SetupFailed" || s.Message != "not ready within 90s" {
		t.Errorf("secrets = %+v", s)
	}
}

func TestReadiness_NilRegistryIsEmptyObject(t *testing.T) {
	srv := api.NewServer(newTestDB(t), event.NewInProcessBus(), map[string]string{"key-alice": "alice"}, nil, nil, nil, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/readiness", nil)
	req.Header.Set("Authorization", "Bearer key-alice")
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "{}\n" {
		t.Errorf("status %d body %q, want 200 {}", w.Code, w.Body)
	}
}
