package api_test

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

func TestMapStoreError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"not found", store.ErrNotFound, 404},
		{"conflict", store.ErrConflict, 409},
		{"has children", store.ErrHasChildren, 409},
		{"validation", resource.ErrValidation, 400},
		{"wrapped validation", errWrap(resource.ErrValidation), 400},
		{"unrecognized", errors.New("boom"), 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, title := api.MapStoreError(tt.err)
			if status != tt.wantStatus {
				t.Errorf("MapStoreError(%v) status = %d, want %d", tt.err, status, tt.wantStatus)
			}
			if title == "" {
				t.Errorf("MapStoreError(%v) title = empty, want non-empty", tt.err)
			}
		})
	}
}

func errWrap(err error) error {
	return errors.Join(err, errors.New("extra context"))
}

func TestWriteProblem(t *testing.T) {
	w := httptest.NewRecorder()
	api.WriteProblem(w, 404, "resource not found", "no organization with that id", "req-123")

	if got := w.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	if w.Code != 404 {
		t.Errorf("status code = %d, want 404", w.Code)
	}

	var p api.Problem
	if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if p.Status != 404 || p.Title != "resource not found" || p.Instance != "req-123" {
		t.Errorf("decoded Problem = %+v, unexpected fields", p)
	}
}
