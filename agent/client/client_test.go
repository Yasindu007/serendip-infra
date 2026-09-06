package client_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/agent/client"
)

func TestUpsert_CreatesWhenMissing(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody client.NodeWire

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/nodes/node-1":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/nodes":
			gotMethod, gotPath = r.Method, r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode POST body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			gotBody.Metadata.ResourceVersion = 1
			json.NewEncoder(w).Encode(gotBody)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := client.New(srv.URL, "test-token")
	got, err := c.Upsert(t.Context(), "node-1", "my-node", client.NodeStatusWire{CPUCores: 4})
	if err != nil {
		t.Fatalf("Upsert() = %v", err)
	}

	if gotMethod != http.MethodPost || gotPath != "/v1/nodes" {
		t.Errorf("request = %s %s, want POST /v1/nodes", gotMethod, gotPath)
	}
	if gotBody.Metadata.ID != "node-1" || gotBody.Metadata.Name != "my-node" {
		t.Errorf("create body metadata = %+v, want id=node-1 name=my-node", gotBody.Metadata)
	}
	if gotBody.Status.CPUCores != 4 {
		t.Errorf("create body status.cpuCores = %d, want 4", gotBody.Status.CPUCores)
	}
	if got.Metadata.ResourceVersion != 1 {
		t.Errorf("Upsert() returned ResourceVersion = %d, want 1", got.Metadata.ResourceVersion)
	}
}

func TestUpsert_UpdatesWhenExisting_CarriesFetchedResourceVersion(t *testing.T) {
	existing := client.NodeWire{
		Metadata: client.MetadataWire{ID: "node-1", Name: "old-name", Kind: "Node", ResourceVersion: 3},
		Status:   client.NodeStatusWire{CPUCores: 2},
	}

	var gotPutBody client.NodeWire
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/nodes/node-1":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(existing)
		case r.Method == http.MethodPut && r.URL.Path == "/v1/nodes/node-1":
			if err := json.NewDecoder(r.Body).Decode(&gotPutBody); err != nil {
				t.Errorf("decode PUT body: %v", err)
			}
			updated := gotPutBody
			updated.Metadata.ResourceVersion = 4
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(updated)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := client.New(srv.URL, "test-token")
	got, err := c.Upsert(t.Context(), "node-1", "new-name", client.NodeStatusWire{CPUCores: 8})
	if err != nil {
		t.Fatalf("Upsert() = %v", err)
	}

	if gotPutBody.Metadata.ResourceVersion != 3 {
		t.Errorf("PUT body ResourceVersion = %d, want 3 (the version GET returned)", gotPutBody.Metadata.ResourceVersion)
	}
	if gotPutBody.Metadata.Name != "new-name" {
		t.Errorf("PUT body Name = %q, want %q", gotPutBody.Metadata.Name, "new-name")
	}
	if gotPutBody.Status.CPUCores != 8 {
		t.Errorf("PUT body Status.CPUCores = %d, want 8", gotPutBody.Status.CPUCores)
	}
	if got.Metadata.ResourceVersion != 4 {
		t.Errorf("Upsert() returned ResourceVersion = %d, want 4", got.Metadata.ResourceVersion)
	}
}

// TestUpsert_PreservesExistingConditions is T009's holistic-review fix:
// Upsert must not silently wipe Conditions a future setter (e.g. a Node
// reconciler) puts on Status, even though this agent never sets them
// itself -- same round-trip-preservation principle as
// Metadata.Labels/OwnerRefs/Relationships.
func TestUpsert_PreservesExistingConditions(t *testing.T) {
	existingConditions := []client.ConditionWire{
		{Type: "Ready", Status: "True", Reason: "Joined", LastTransitionTime: time.Now().UTC()},
	}
	existing := client.NodeWire{
		Metadata: client.MetadataWire{ID: "node-1", Name: "old-name", ResourceVersion: 3},
		Status:   client.NodeStatusWire{Conditions: existingConditions, CPUCores: 2},
	}

	var gotPutBody client.NodeWire
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/nodes/node-1":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(existing)
		case r.Method == http.MethodPut && r.URL.Path == "/v1/nodes/node-1":
			json.NewDecoder(r.Body).Decode(&gotPutBody)
			updated := gotPutBody
			updated.Metadata.ResourceVersion = 4
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(updated)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := client.New(srv.URL, "test-token")
	// The caller (cmd/serendip-agent) never populates Conditions -- pass
	// none, same as real usage.
	if _, err := c.Upsert(t.Context(), "node-1", "new-name", client.NodeStatusWire{CPUCores: 8}); err != nil {
		t.Fatalf("Upsert() = %v", err)
	}

	if len(gotPutBody.Status.Conditions) != 1 || gotPutBody.Status.Conditions[0].Type != "Ready" {
		t.Errorf("PUT body Status.Conditions = %+v, want the pre-existing [%+v] preserved", gotPutBody.Status.Conditions, existingConditions[0])
	}
	if gotPutBody.Status.CPUCores != 8 {
		t.Errorf("PUT body Status.CPUCores = %d, want 8 (the new hardware fact)", gotPutBody.Status.CPUCores)
	}
}

func TestUpsert_OneRetryOn409(t *testing.T) {
	var mu sync.Mutex
	getCount, putCount := 0, 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/nodes/node-1":
			getCount++
			// First GET returns a stale version (3); after the PUT
			// conflicts and this client retries with a second GET, the
			// server now reports a newer version (5) -- simulating a
			// concurrent edit that landed in between.
			version := int64(3)
			if getCount > 1 {
				version = 5
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(client.NodeWire{
				Metadata: client.MetadataWire{ID: "node-1", Name: "old-name", ResourceVersion: version},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/nodes/node-1":
			putCount++
			var body client.NodeWire
			json.NewDecoder(r.Body).Decode(&body)
			if putCount == 1 {
				// First PUT still carries the now-stale version 3 -> conflict.
				w.WriteHeader(http.StatusConflict)
				return
			}
			// Second PUT carries the re-fetched version 5 -> succeeds.
			body.Metadata.ResourceVersion = 6
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(body)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := client.New(srv.URL, "test-token")
	got, err := c.Upsert(t.Context(), "node-1", "new-name", client.NodeStatusWire{CPUCores: 8})
	if err != nil {
		t.Fatalf("Upsert() = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if getCount != 2 {
		t.Errorf("GET call count = %d, want 2 (initial + retry after conflict)", getCount)
	}
	if putCount != 2 {
		t.Errorf("PUT call count = %d, want 2 (initial conflicting attempt + retry)", putCount)
	}
	if got.Metadata.ResourceVersion != 6 {
		t.Errorf("Upsert() returned ResourceVersion = %d, want 6", got.Metadata.ResourceVersion)
	}
}

func TestUpsert_BearerTokenSent(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "secret-token")
	_, err := c.Upsert(t.Context(), "node-1", "my-node", client.NodeStatusWire{})
	// The POST after the 404 isn't stubbed, so this will error -- that's
	// fine, this test only cares that the GET carried the bearer token.
	_ = err

	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer secret-token")
	}
}
