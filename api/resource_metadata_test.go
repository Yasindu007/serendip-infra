package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
)

func TestResourceMetadata(t *testing.T) {
	var issuer atomic.Value
	issuer.Store("")
	srv := api.NewServer(newTestDB(t), event.NewInProcessBus(), nil, nil, nil, nil, nil, api.WithResourceMetadata(api.ResourceMetadata{
		Issuer: func() string { return issuer.Load().(string) }, CLIClient: "cli", AssistantClient: "assistant",
	}))
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://serendip.example/.well-known/oauth-protected-resource", nil))
		return rec
	}
	if rec := get(); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Errorf("before identity is ready: %d", rec.Code)
	}
	issuer.Store("https://idp.example")
	rec := get()
	var doc map[string]any
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &doc) != nil {
		t.Fatalf("ready: %d %s", rec.Code, rec.Body)
	}
	if doc["resource"] != "http://serendip.example" || doc["serendip_cli_client_id"] != "cli" || doc["serendip_assistant_client_id"] != "assistant" {
		t.Errorf("document = %v", doc)
	}
	if as, _ := doc["authorization_servers"].([]any); len(as) != 1 || as[0] != "https://idp.example" {
		t.Errorf("authorization_servers = %v", doc["authorization_servers"])
	}

	plain := api.NewServer(newTestDB(t), event.NewInProcessBus(), nil, nil, nil, nil, nil)
	rec = httptest.NewRecorder()
	plain.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("without the option: %d, want 404", rec.Code)
	}
}
