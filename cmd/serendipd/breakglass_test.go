package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
	"github.com/serendip-infra/serendip-infra/core/store/storetest"
)

func openStore(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "serendip.db")
	db, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db, path
}

func assignmentsOf(t *testing.T, db *sql.DB, subject string) []resource.RoleAssignment {
	t.Helper()
	s := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment)
	all, err := s.List(t.Context(), store.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out []resource.RoleAssignment
	for _, a := range all {
		if a.Spec.Principal.ID == subject {
			out = append(out, a)
		}
	}
	return out
}

func TestBootstrapAdmin(t *testing.T) {
	db, _ := openStore(t)
	const admin = "01900000-0000-7000-8000-000000000030"
	if err := bootstrapAdmin(t.Context(), db, event.NewInProcessBus(), admin); err != nil {
		t.Fatal(err)
	}
	got := assignmentsOf(t, db, admin)
	if len(got) != 2 {
		t.Fatalf("bootstrap administrator has %d assignments, want Owner and Secrets Officer", len(got))
	}
	for _, a := range got {
		if !a.Spec.Scope.IsRoot() || (a.Spec.RoleDefinitionID != policy.OwnerRoleID && a.Spec.RoleDefinitionID != policy.SecretsOfficerRoleID) {
			t.Errorf("unexpected bootstrap assignment %+v", a.Spec)
		}
	}
	// A second start changes nothing.
	if err := bootstrapAdmin(t.Context(), db, event.NewInProcessBus(), admin); err != nil || len(assignmentsOf(t, db, admin)) != 2 {
		t.Errorf("second start: err %v, %d assignments", err, len(assignmentsOf(t, db, admin)))
	}
	// An operator who removes the administrator's roles isn't overruled.
	// (A start that granted only Owner is finished: covered below.)
	s := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment)
	for _, a := range got {
		if err := s.Delete(storetest.Approved(t.Context(), resource.KindRoleAssignment, a.Metadata.ID, false), a.Metadata.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := bootstrapAdmin(t.Context(), db, event.NewInProcessBus(), admin); err != nil || len(assignmentsOf(t, db, admin)) != 0 {
		t.Errorf("after removal: err %v, %d assignments, want none re-created", err, len(assignmentsOf(t, db, admin)))
	}
}

func TestBreakGlass_GrantsOwnerAudited(t *testing.T) {
	db, path := openStore(t)
	var out, errOut bytes.Buffer
	if err := runBreakGlass(t.Context(), []string{"grant-owner", "--subject", "sub-rescued", "--db", path}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	got := assignmentsOf(t, db, "sub-rescued")
	if len(got) != 1 || got[0].Spec.RoleDefinitionID != policy.OwnerRoleID || !got[0].Spec.Scope.IsRoot() {
		t.Fatalf("assignments = %+v, want one Owner at /", got)
	}
	if !strings.Contains(errOut.String(), "WARNING") {
		t.Errorf("no warning printed: %q", errOut.String())
	}
	var iface, actor string
	if err := db.QueryRow(`SELECT interface, actor FROM audit_log WHERE kind = 'RoleAssignment' AND resource_id = ?`, got[0].Metadata.ID).Scan(&iface, &actor); err != nil {
		t.Fatal(err)
	}
	if iface != "break-glass" || !strings.HasPrefix(actor, "break-glass") {
		t.Errorf("audit interface %q, actor %q; want break-glass", iface, actor)
	}
	// Running it again changes nothing.
	out.Reset()
	if err := runBreakGlass(t.Context(), []string{"grant-owner", "--subject", "sub-rescued", "--db", path}, &out, &errOut); err != nil || len(assignmentsOf(t, db, "sub-rescued")) != 1 {
		t.Errorf("second run: err %v, %d assignments", err, len(assignmentsOf(t, db, "sub-rescued")))
	}
	if !strings.Contains(out.String(), "already Owner") {
		t.Errorf("second run said %q", out.String())
	}
}

func TestBreakGlass_RefusesBadInput(t *testing.T) {
	db, path := openStore(t)
	reg := sqlite.NewPrincipalRegistry(db)
	for _, p := range []sqlite.KnownPrincipal{
		{Type: resource.PrincipalPerson, ID: "sub-1", DisplayName: "alex"},
		{Type: resource.PrincipalPerson, ID: "sub-2", DisplayName: "sam"},
		{Type: resource.PrincipalPerson, ID: "sub-3", DisplayName: "sam"},
	} {
		if err := reg.Seen(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) error {
		return runBreakGlass(t.Context(), args, &bytes.Buffer{}, &bytes.Buffer{})
	}
	if err := run("grant-owner", "--username", "alex", "--db", path); err != nil || len(assignmentsOf(t, db, "sub-1")) != 1 {
		t.Errorf("by username: %v", err)
	}
	for name, args := range map[string][]string{
		"no subcommand":      {},
		"unknown subcommand": {"grant-everything"},
		"neither flag":       {"grant-owner", "--db", path},
		"both flags":         {"grant-owner", "--subject", "x", "--username", "alex", "--db", path},
		"unknown username":   {"grant-owner", "--username", "nobody", "--db", path},
		"shared username":    {"grant-owner", "--username", "sam", "--db", path},
		"missing store":      {"grant-owner", "--subject", "x", "--db", filepath.Join(t.TempDir(), "typo.db")},
	} {
		if err := run(args...); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if len(assignmentsOf(t, db, "sub-2"))+len(assignmentsOf(t, db, "sub-3"))+len(assignmentsOf(t, db, "x")) != 0 {
		t.Error("a refused run granted something")
	}
}

type tokenAuthn map[string]identity.Principal

func (a tokenAuthn) Authenticate(_ context.Context, token string) (identity.Principal, error) {
	if p, ok := a[token]; ok {
		return p, nil
	}
	return identity.Principal{}, fmt.Errorf("%w: unknown", identity.ErrInvalidCredential)
}

// A running server applies a break-glass grant written by another process
// (no event reaches it) on its next policy reload.
func TestBreakGlass_RunningServerPicksItUp(t *testing.T) {
	db, path := openStore(t)
	old := api.PolicyRebuildInterval
	api.PolicyRebuildInterval = time.Second
	t.Cleanup(func() { api.PolicyRebuildInterval = old })
	srv := api.NewServer(db, event.NewInProcessBus(), nil, tokenAuthn{"tok": {Subject: "sub-locked-out", Name: "locked"}}, nil, nil, nil)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	status := func() int {
		req := httptest.NewRequest(http.MethodGet, "/v1/readiness", nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := status(); got != http.StatusForbidden {
		t.Fatalf("before break-glass: %d, want 403", got)
	}
	if err := runBreakGlass(t.Context(), []string{"grant-owner", "--subject", "sub-locked-out", "--db", path}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if status() == http.StatusOK {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Error("the running server never applied the break-glass grant")
}

// A start interrupted between the two grants finishes the second next time.
func TestBootstrapAdmin_FinishesAPartialStart(t *testing.T) {
	db, _ := openStore(t)
	const admin = "sub-admin"
	s := sqlite.NewStore[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus](db, resource.KindRoleAssignment)
	if _, err := s.Create(t.Context(), resource.RoleAssignment{Metadata: resource.Metadata{Name: "bootstrap-admin"}, Spec: resource.RoleAssignmentSpec{
		Principal: resource.PrincipalRef{Type: resource.PrincipalPerson, ID: admin}, RoleDefinitionID: policy.OwnerRoleID, Scope: resource.RootScope,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := bootstrapAdmin(t.Context(), db, event.NewInProcessBus(), admin); err != nil {
		t.Fatal(err)
	}
	got := assignmentsOf(t, db, admin)
	if len(got) != 2 {
		t.Fatalf("%d assignments, want Owner plus the missing Secrets Officer", len(got))
	}
}
