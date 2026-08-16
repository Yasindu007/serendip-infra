package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/registry"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// This is the T019 gap fix: specs/002-http-api/spec.md's Scope-IN item 2
// ("HTTP handlers over core/registry: ... get/set a capability's default
// provider") was never built by any T001-T018 task and the drop went
// undisclosed -- caught by contract-guardian's holistic final review.
// Capability register/list are already proven by
// TestNewServer_AllKindsRespondToPostAndGet; this file covers only the
// one operation the generic ResourceHandler cannot correctly perform:
// SetDefault's "must already be a registered provider" validation.

func newRegistryTestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	db := newTestDB(t)
	capStore := sqlite.NewStore[resource.CapabilitySpec, resource.CapabilityStatus](db, resource.KindCapability)
	mux := http.NewServeMux()
	api.NewRegistryHandler(registry.NewCapabilityRegistry(capStore)).Register(mux)
	return mux
}

func TestRegistryHandler_GetSetDefault(t *testing.T) {
	db := newTestDB(t)
	capStore := sqlite.NewStore[resource.CapabilitySpec, resource.CapabilityStatus](db, resource.KindCapability)
	capabilities := registry.NewCapabilityRegistry(capStore)
	if _, err := capabilities.Register(t.Context(), "identity", true, []string{"provider-a", "provider-b"}, "provider-a"); err != nil {
		t.Fatalf("Register() = %v", err)
	}

	mux := http.NewServeMux()
	api.NewRegistryHandler(capabilities).Register(mux)

	w := doRequest(mux, http.MethodGet, "/v1/capabilities/identity/default", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("Get default status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body)
	}
	var got struct {
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get-default response: %v", err)
	}
	if got.Provider != "provider-a" {
		t.Errorf("Get default = %q, want %q", got.Provider, "provider-a")
	}

	w = doRequest(mux, http.MethodPut, "/v1/capabilities/identity/default", map[string]string{"provider": "provider-b"})
	if w.Code != http.StatusOK {
		t.Fatalf("Set default status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body)
	}

	w = doRequest(mux, http.MethodGet, "/v1/capabilities/identity/default", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get-default response: %v", err)
	}
	if got.Provider != "provider-b" {
		t.Errorf("Get default after switch = %q, want %q", got.Provider, "provider-b")
	}
}

func TestRegistryHandler_SetDefault_RejectsUnregisteredProvider(t *testing.T) {
	db := newTestDB(t)
	capStore := sqlite.NewStore[resource.CapabilitySpec, resource.CapabilityStatus](db, resource.KindCapability)
	capabilities := registry.NewCapabilityRegistry(capStore)
	if _, err := capabilities.Register(t.Context(), "identity", true, []string{"provider-a"}, "provider-a"); err != nil {
		t.Fatalf("Register() = %v", err)
	}

	mux := http.NewServeMux()
	api.NewRegistryHandler(capabilities).Register(mux)

	w := doRequest(mux, http.MethodPut, "/v1/capabilities/identity/default", map[string]string{"provider": "provider-nonexistent"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (a client-supplied bad value, not a server error), body: %s", w.Code, http.StatusBadRequest, w.Body)
	}
	assertProblemJSON(t, w)

	// The default must be unchanged after a rejected switch.
	w = doRequest(mux, http.MethodGet, "/v1/capabilities/identity/default", nil)
	var got struct {
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get-default response: %v", err)
	}
	if got.Provider != "provider-a" {
		t.Errorf("Get default after rejected switch = %q, want unchanged %q", got.Provider, "provider-a")
	}
}

func TestRegistryHandler_GetDefault_UnknownCapability(t *testing.T) {
	mux := newRegistryTestMux(t)
	w := doRequest(mux, http.MethodGet, "/v1/capabilities/does-not-exist/default", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body: %s", w.Code, http.StatusNotFound, w.Body)
	}
	assertProblemJSON(t, w)
}
