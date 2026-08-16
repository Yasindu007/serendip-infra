package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() = %v", err)
	}
	return db
}

// TestRequestIDMiddleware_PropagatesToResponseAndAuditRow is T003's own
// Verify line: the request ID on the response header must match the one
// attached to the audit_log row the request's mutation produced. The
// full CRUD handler doesn't exist until T011, so this test drives a
// core/store.Create call directly from a minimal test handler wrapped in
// RequestIDMiddleware -- proving the context plumbing, not the eventual
// route surface.
func TestRequestIDMiddleware_PropagatesToResponseAndAuditRow(t *testing.T) {
	db := newTestDB(t)
	orgs := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)

	var createdID string
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		created, err := orgs.Create(r.Context(), resource.Organization{
			Metadata: resource.Metadata{Name: "acme"},
		})
		if err != nil {
			t.Fatalf("Create() = %v", err)
		}
		createdID = created.Metadata.ID
		w.WriteHeader(http.StatusCreated)
	})

	handler := api.RequestIDMiddleware(testHandler)
	req := httptest.NewRequest(http.MethodPost, "/v1/organizations", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	headerReqID := w.Header().Get("X-Request-Id")
	if headerReqID == "" {
		t.Fatal("X-Request-Id response header is empty, want a generated request ID")
	}
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	var auditRequestID, auditInterface sql.NullString
	err := db.QueryRowContext(context.Background(),
		`SELECT request_id, interface FROM audit_log WHERE resource_id = ?`, createdID,
	).Scan(&auditRequestID, &auditInterface)
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if !auditRequestID.Valid || auditRequestID.String != headerReqID {
		t.Errorf("audit_log.request_id = %v, want it to match response header %q", auditRequestID, headerReqID)
	}
	if !auditInterface.Valid || auditInterface.String != api.InterfaceName {
		t.Errorf("audit_log.interface = %v, want %q", auditInterface, api.InterfaceName)
	}
}

func TestRequestIDFromContext_EmptyWhenNotSet(t *testing.T) {
	if got := api.RequestIDFromContext(context.Background()); got != "" {
		t.Errorf("RequestIDFromContext(bare context) = %q, want empty", got)
	}
}
