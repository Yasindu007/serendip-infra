package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// grant assigns role to a principal at scope, straight into db. Call it
// before api.NewServer, whose policy index loads on first use.
func grant(t testing.TB, db *sql.DB, pt resource.PrincipalType, id, role string, scope resource.Scope) string {
	t.Helper()
	s := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment)
	created, err := s.Create(context.Background(), resource.RoleAssignment{
		Metadata: resource.Metadata{Name: "test-grant"},
		Spec:     resource.RoleAssignmentSpec{Principal: resource.PrincipalRef{Type: pt, ID: id}, RoleDefinitionID: role, Scope: scope},
	})
	if err != nil {
		t.Fatalf("grant %s %s: %v", pt, id, err)
	}
	return created.Metadata.ID
}

func newTestDB(t testing.TB) *sql.DB {
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

// fakeAuthn is a scripted identity.Authenticator: tokens maps a credential
// to its Principal; notReady makes every call answer "not ready yet".
type fakeAuthn struct {
	tokens   map[string]identity.Principal
	notReady bool
	calls    atomic.Int32
}

func (f *fakeAuthn) Authenticate(_ context.Context, token string) (identity.Principal, error) {
	f.calls.Add(1)
	if f.notReady {
		return identity.Principal{}, reconcile.ErrProviderNotReady
	}
	if p, ok := f.tokens[token]; ok {
		return p, nil
	}
	return identity.Principal{}, fmt.Errorf("%w: unknown test token", identity.ErrInvalidCredential)
}

// TestAuthMiddleware covers both ways in -- the static API keys and the
// identity provider -- and every way out (specs/015-identity-foundation
// plan.md decision 11). Rejected requests must never reach next: that's
// specs/002-http-api/spec.md acceptance criterion 3, literally (if next
// never runs, no core/store call inside it can happen either).
func TestAuthMiddleware(t *testing.T) {
	apiKeys := map[string]string{"key-alice": "alice"}
	valid := map[string]identity.Principal{"jwt-admin": {Subject: "0190-0030", Name: "admin"}}

	tests := []struct {
		name      string
		header    string
		authn     *fakeAuthn // nil: no identity provider wired
		wantCode  int
		wantActor string // checked when wantCode is 200
		wantAuthn int    // expected Authenticate calls (-1: don't care)
	}{
		{"API key", "Bearer key-alice", &fakeAuthn{tokens: valid}, http.StatusOK, "alice", 0},
		{"API key, no identity provider", "Bearer key-alice", nil, http.StatusOK, "alice", -1},
		{"valid identity token", "Bearer jwt-admin", &fakeAuthn{tokens: valid}, http.StatusOK, "person:admin", 1},
		{"invalid identity token", "Bearer jwt-forged", &fakeAuthn{tokens: valid}, http.StatusUnauthorized, "", 1},
		{"identity provider not ready", "Bearer jwt-admin", &fakeAuthn{tokens: valid, notReady: true}, http.StatusServiceUnavailable, "", 1},
		{"API key while identity not ready", "Bearer key-alice", &fakeAuthn{notReady: true}, http.StatusOK, "alice", 0},
		{"unknown token, no identity provider", "Bearer jwt-admin", nil, http.StatusUnauthorized, "", -1},
		{"no Authorization header", "", &fakeAuthn{tokens: valid}, http.StatusUnauthorized, "", 0},
		{"malformed (no Bearer prefix)", "key-alice", &fakeAuthn{tokens: valid}, http.StatusUnauthorized, "", 0},
		{"empty bearer token", "Bearer ", &fakeAuthn{tokens: valid}, http.StatusUnauthorized, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var nextCalled bool
			var gotActor string
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				gotActor = audit.ActorFrom(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			var authn identity.Authenticator
			if tt.authn != nil {
				authn = tt.authn
			}
			handler := api.AuthMiddleware(apiKeys, authn)(next)
			req := httptest.NewRequest(http.MethodPost, "/v1/organizations", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.wantCode, w.Body.String())
			}
			if tt.wantCode == http.StatusOK {
				if !nextCalled || gotActor != tt.wantActor {
					t.Errorf("next called = %v with actor %q, want called with %q", nextCalled, gotActor, tt.wantActor)
				}
			} else {
				if nextCalled {
					t.Error("next handler was called for a rejected request, want it never reached")
				}
				if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
					t.Errorf("Content-Type = %q, want application/problem+json", ct)
				}
				if strings.Contains(w.Body.String(), "unknown test token") {
					t.Errorf("problem body leaks the verifier's reason: %s", w.Body.String())
				}
			}
			if tt.wantAuthn >= 0 && tt.authn != nil && int(tt.authn.calls.Load()) != tt.wantAuthn {
				t.Errorf("Authenticate calls = %d, want %d", tt.authn.calls.Load(), tt.wantAuthn)
			}
		})
	}
}

// TestAuth_RecordsSubjectAndPrincipals: a person's mutation carries their
// subject into the audit row; an API key's carries none. Both callers, and
// the person's groups, land in the known-principals registry
// (specs/017-authorization T004).
func TestAuth_RecordsSubjectAndPrincipals(t *testing.T) {
	db := newTestDB(t)
	authn := &fakeAuthn{tokens: map[string]identity.Principal{
		"tok-carol": {Subject: "sub-carol", Name: "carol", Groups: []string{"platform"}},
	}}
	grant(t, db, resource.PrincipalPerson, "sub-carol", policy.OwnerRoleID, resource.RootScope)
	h := api.NewServer(db, event.NewInProcessBus(), map[string]string{"key-ci": "ci"}, authn, nil, nil, nil).Handler
	create := func(token, name string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/organizations", strings.NewReader(`{"metadata":{"name":"`+name+`"},"spec":{}}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rec.Code, rec.Body)
		}
	}
	create("tok-carol", "by-carol")
	create("key-ci", "by-ci")

	rows, err := db.Query(`SELECT actor, COALESCE(actor_subject, '') FROM audit_log WHERE kind = 'Organization' ORDER BY at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var actor, subject string
		if err := rows.Scan(&actor, &subject); err != nil {
			t.Fatal(err)
		}
		got[actor] = subject
	}
	if got["person:carol"] != "sub-carol" || got["ci"] != "" || len(got) != 2 {
		t.Errorf("audit actor -> subject = %v", got)
	}

	known, err := sqlite.NewPrincipalRegistry(db).List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range known {
		seen[string(p.Type)+"/"+p.ID] = true
	}
	for _, want := range []string{"person/sub-carol", "group/platform", "apiKey/ci"} {
		if !seen[want] {
			t.Errorf("registry lacks %s: %+v", want, known)
		}
	}
}
