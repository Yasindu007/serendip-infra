package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func newTestHandler(t *testing.T) *api.ResourceHandler[resource.OrganizationSpec, resource.OrganizationStatus] {
	t.Helper()
	db := newTestDB(t)
	s := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)
	return api.NewResourceHandler(s, "organizations")
}

func newTestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	newTestHandler(t).Register(mux)
	return mux
}

// doRequest wraps the mux with RequestIDMiddleware so audit records/error
// responses get a real request ID, matching how api/server.go (T012)
// will actually wire things.
func doRequest(mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	var reqBody *bytes.Buffer
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	w := httptest.NewRecorder()
	api.RequestIDMiddleware(mux).ServeHTTP(w, req)
	return w
}

func TestResourceHandler_CRUDCycle(t *testing.T) {
	// newTestMux creates its own store/DB; reuse one handler+db across
	// the whole cycle instead so Get/Update/Delete see what Create wrote.
	db := newTestDB(t)
	s := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)
	mux := http.NewServeMux()
	api.NewResourceHandler(s, "organizations").Register(mux)

	// Create
	w := doRequest(mux, http.MethodPost, "/v1/organizations", resource.Organization{
		Metadata: resource.Metadata{Name: "acme"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, want %d, body: %s", w.Code, http.StatusCreated, w.Body)
	}
	var created resource.Organization
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Metadata.ID == "" || created.Metadata.Name != "acme" {
		t.Fatalf("created = %+v, want a real ID and Name=acme", created.Metadata)
	}

	// Get
	w = doRequest(mux, http.MethodGet, "/v1/organizations/"+created.Metadata.ID, nil)
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

	// List
	w = doRequest(mux, http.MethodGet, "/v1/organizations", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("List status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body)
	}
	var list []resource.Organization
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}

	// Update
	got.Metadata.Name = "acme-renamed"
	w = doRequest(mux, http.MethodPut, "/v1/organizations/"+created.Metadata.ID, got)
	if w.Code != http.StatusOK {
		t.Fatalf("Update status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body)
	}
	var updated resource.Organization
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Metadata.Name != "acme-renamed" {
		t.Errorf("Update Name = %q, want %q", updated.Metadata.Name, "acme-renamed")
	}
	if updated.Metadata.ResourceVersion != created.Metadata.ResourceVersion+1 {
		t.Errorf("Update ResourceVersion = %d, want %d", updated.Metadata.ResourceVersion, created.Metadata.ResourceVersion+1)
	}

	// Delete
	w = doRequest(mux, http.MethodDelete, "/v1/organizations/"+created.Metadata.ID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("Delete status = %d, want %d, body: %s", w.Code, http.StatusNoContent, w.Body)
	}

	// Confirm it's actually gone.
	w = doRequest(mux, http.MethodGet, "/v1/organizations/"+created.Metadata.ID, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("Get after Delete status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestResourceHandler_Get_NotFound(t *testing.T) {
	mux := newTestMux(t)
	w := doRequest(mux, http.MethodGet, "/v1/organizations/does-not-exist", nil)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body: %s", w.Code, http.StatusNotFound, w.Body)
	}
	assertProblemJSON(t, w)
}

func TestResourceHandler_Create_ValidationError(t *testing.T) {
	mux := newTestMux(t)
	// Empty Name fails resource.Validate -> resource.ErrValidation -> 400.
	w := doRequest(mux, http.MethodPost, "/v1/organizations", resource.Organization{
		Metadata: resource.Metadata{Name: ""},
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body: %s", w.Code, http.StatusBadRequest, w.Body)
	}
	assertProblemJSON(t, w)
}

func TestResourceHandler_Update_VersionConflict(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)
	mux := http.NewServeMux()
	api.NewResourceHandler(s, "organizations").Register(mux)

	w := doRequest(mux, http.MethodPost, "/v1/organizations", resource.Organization{
		Metadata: resource.Metadata{Name: "acme"},
	})
	var created resource.Organization
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	// First update succeeds and bumps the version.
	firstUpdate := created
	firstUpdate.Metadata.Name = "acme-v2"
	w = doRequest(mux, http.MethodPut, "/v1/organizations/"+created.Metadata.ID, firstUpdate)
	if w.Code != http.StatusOK {
		t.Fatalf("first Update status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body)
	}

	// Second update, still carrying the stale (pre-first-update) version, must conflict.
	staleUpdate := created
	staleUpdate.Metadata.Name = "acme-conflict"
	w = doRequest(mux, http.MethodPut, "/v1/organizations/"+created.Metadata.ID, staleUpdate)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale Update status = %d, want %d, body: %s", w.Code, http.StatusConflict, w.Body)
	}
	assertProblemJSON(t, w)
}

func TestResourceHandler_Create_MalformedBody(t *testing.T) {
	mux := newTestMux(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/organizations", bytes.NewBufferString("{not valid json"))
	w := httptest.NewRecorder()
	api.RequestIDMiddleware(mux).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body: %s", w.Code, http.StatusBadRequest, w.Body)
	}
	assertProblemJSON(t, w)
}

// The handler decodes the body as JSON regardless of the Content-Type
// header -- api/CLAUDE.md's "handlers are thin" rule and specs/002-http-api
// name no framework-level content negotiation, so this pins the actual
// (permissive) behavior as intentional rather than an accidental gap.
func TestResourceHandler_Create_IgnoresContentTypeHeader(t *testing.T) {
	mux := newTestMux(t)
	body, err := json.Marshal(resource.Organization{Metadata: resource.Metadata{Name: "acme"}})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/organizations", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	api.RequestIDMiddleware(mux).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (a wrong Content-Type header should not block a valid JSON body), body: %s", w.Code, http.StatusCreated, w.Body)
	}
}

func TestResourceHandler_List_CombinedOwnerAndLabelFilters(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)
	mux := http.NewServeMux()
	api.NewResourceHandler(s, "organizations").Register(mux)

	create := func(name, ownerID, labelValue string) {
		w := doRequest(mux, http.MethodPost, "/v1/organizations", resource.Organization{
			Metadata: resource.Metadata{
				Name:      name,
				OwnerRefs: []resource.OwnerRef{{Kind: resource.KindOrganization, ID: ownerID}},
				Labels:    map[string]string{"env": labelValue},
			},
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("Create(%s) status = %d, body: %s", name, w.Code, w.Body)
		}
	}
	create("match", "owner-1", "prod")
	create("wrong-label", "owner-1", "staging")
	create("wrong-owner", "owner-2", "prod")

	w := doRequest(mux, http.MethodGet, "/v1/organizations?ownerId=owner-1&label.env=prod", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("List status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body)
	}
	var list []resource.Organization
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(list) != 1 || list[0].Metadata.Name != "match" {
		t.Fatalf("List(ownerId=owner-1, label.env=prod) = %+v, want exactly [match]", list)
	}
}

func assertProblemJSON(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var p api.Problem
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode Problem body: %v (body: %s)", err, w.Body)
	}
	if p.Status != w.Code {
		t.Errorf("Problem.Status = %d, want it to match HTTP status %d", p.Status, w.Code)
	}
}
