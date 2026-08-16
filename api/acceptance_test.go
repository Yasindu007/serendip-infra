package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// This file proves specs/002-http-api/spec.md's acceptance criteria 2-4
// directly, through the fully composed api.NewServer stack (request ID ->
// actor -> mux), not an isolated middleware or handler -- T003/T004's
// middleware_test.go and T011-T015's handler_test.go already cover those
// in depth. Mirrors spec 001's TestAcceptanceCriterion2_... naming
// (core/store/sqlite/cascade_test.go).

func newAcceptanceServer(t *testing.T) (http.Handler, *sql.DB, *event.InProcessBus) {
	t.Helper()
	db := newTestDB(t)
	bus := event.NewInProcessBus()
	srv := api.NewServer(db, bus, map[string]string{"key-alice": "alice"})
	return srv.Handler, db, bus
}

func doAuthedRequest(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func countAllAuditRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM audit_log").Scan(&n); err != nil {
		t.Fatalf("count audit_log rows: %v", err)
	}
	return n
}

// TestAcceptanceCriterion2_CreateGetDeleteThroughAPI_AuditShowsRealActorAndInterface
// is spec.md's acceptance criterion 2: a resource created via the API is
// retrievable via the API with the same data; deleting it makes it
// unretrievable; audit_log shows a real Interface (not "core") and a real
// Actor (not "unknown") for every mutation made through the API.
func TestAcceptanceCriterion2_CreateGetDeleteThroughAPI_AuditShowsRealActorAndInterface(t *testing.T) {
	h, db, _ := newAcceptanceServer(t)

	w := doAuthedRequest(t, h, http.MethodPost, "/v1/organizations", "key-alice", resource.Organization{
		Metadata: resource.Metadata{Name: "acme"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, want %d, body: %s", w.Code, http.StatusCreated, w.Body)
	}
	var created resource.Organization
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = doAuthedRequest(t, h, http.MethodGet, "/v1/organizations/"+created.Metadata.ID, "key-alice", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("Get status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body)
	}
	var got resource.Organization
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got.Metadata.Name != "acme" {
		t.Errorf("Get Name = %q, want %q", got.Metadata.Name, "acme")
	}

	w = doAuthedRequest(t, h, http.MethodDelete, "/v1/organizations/"+created.Metadata.ID, "key-alice", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("Delete status = %d, want %d, body: %s", w.Code, http.StatusNoContent, w.Body)
	}

	w = doAuthedRequest(t, h, http.MethodGet, "/v1/organizations/"+created.Metadata.ID, "key-alice", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("Get after Delete status = %d, want %d", w.Code, http.StatusNotFound)
	}

	rows, err := db.QueryContext(context.Background(),
		`SELECT actor, interface FROM audit_log WHERE resource_id = ? ORDER BY at`, created.Metadata.ID)
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var actor, iface string
		if err := rows.Scan(&actor, &iface); err != nil {
			t.Fatalf("scan audit_log row: %v", err)
		}
		if actor != "alice" {
			t.Errorf(`audit row actor = %q, want "alice" (not "unknown")`, actor)
		}
		if iface != api.InterfaceName {
			t.Errorf(`audit row interface = %q, want %q (not "core")`, iface, api.InterfaceName)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit_log rows: %v", err)
	}
	if count != 2 { // Create + Delete
		t.Errorf("audit_log row count for %s = %d, want 2 (Create + Delete)", created.Metadata.ID, count)
	}
}

// TestAcceptanceCriterion3_RejectedMutation_NoAuditRowNoEventNoDataChange is
// spec.md's acceptance criterion 3: a mutating request with no resolvable
// actor is rejected with 401 before any core/store call -- no audit
// record, no data change, no event published.
func TestAcceptanceCriterion3_RejectedMutation_NoAuditRowNoEventNoDataChange(t *testing.T) {
	h, db, bus := newAcceptanceServer(t)

	var events []event.Event
	unsubscribe := bus.Subscribe("", func(e event.Event) { events = append(events, e) })
	defer unsubscribe()

	before := countAllAuditRows(t, db)

	// No Authorization header at all -- must never reach core/store.
	w := doAuthedRequest(t, h, http.MethodPost, "/v1/organizations", "", resource.Organization{
		Metadata: resource.Metadata{Name: "should-not-exist"},
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body: %s", w.Code, http.StatusUnauthorized, w.Body)
	}

	after := countAllAuditRows(t, db)
	if after != before {
		t.Errorf("audit_log row count changed from %d to %d, want unchanged for a rejected request", before, after)
	}
	if len(events) != 0 {
		t.Errorf("events published = %+v, want none for a rejected request", events)
	}

	w = doAuthedRequest(t, h, http.MethodGet, "/v1/organizations", "key-alice", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("List status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body)
	}
	var list []resource.Organization
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List after rejected create = %+v, want empty (no data change)", list)
	}
}

// TestAcceptanceCriterion4_ErrorIsProblemJSONWithRequestID_MatchesAuditRow
// is spec.md's acceptance criterion 4: every error response is valid RFC
// 7807 problem+json and carries a request ID; the same request ID is
// attached to the resulting audit_log row when the request did reach a
// mutation.
func TestAcceptanceCriterion4_ErrorIsProblemJSONWithRequestID_MatchesAuditRow(t *testing.T) {
	h, db, _ := newAcceptanceServer(t)

	w := doAuthedRequest(t, h, http.MethodGet, "/v1/organizations/does-not-exist", "key-alice", nil)
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
	if w.Header().Get("X-Request-Id") == "" {
		t.Fatal("X-Request-Id response header is empty, want a generated request ID")
	}

	w = doAuthedRequest(t, h, http.MethodPost, "/v1/organizations", "key-alice", resource.Organization{
		Metadata: resource.Metadata{Name: "acme"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, want %d, body: %s", w.Code, http.StatusCreated, w.Body)
	}
	mutationReqID := w.Header().Get("X-Request-Id")
	if mutationReqID == "" {
		t.Fatal("X-Request-Id response header is empty on Create, want a generated request ID")
	}
	var created resource.Organization
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	var auditReqID sql.NullString
	err := db.QueryRowContext(context.Background(),
		`SELECT request_id FROM audit_log WHERE resource_id = ?`, created.Metadata.ID,
	).Scan(&auditReqID)
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if !auditReqID.Valid || auditReqID.String != mutationReqID {
		t.Errorf("audit_log.request_id = %v, want it to match response header %q", auditReqID, mutationReqID)
	}
}
