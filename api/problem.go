package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// Problem is an RFC 7807 (application/problem+json) error response.
type Problem struct {
	Type     string `json:"type,omitempty"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"` // request ID
}

// WriteProblem writes an RFC 7807 problem+json response.
func WriteProblem(w http.ResponseWriter, status int, title, detail, requestID string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Title:    title,
		Status:   status,
		Detail:   detail,
		Instance: requestID,
	})
}

// MapStoreError maps a core/store (or core/resource validation) error to
// an HTTP status and title. Checked in order: the three store sentinels,
// then resource.ErrValidation (added in this spec specifically so this
// mapping doesn't have to string-match error messages), then a 500
// default for anything unrecognized.
func MapStoreError(err error) (status int, title string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "resource not found"
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict, "resource version conflict"
	case errors.Is(err, store.ErrHasChildren):
		return http.StatusConflict, "resource has children"
	case errors.Is(err, resource.ErrValidation):
		return http.StatusBadRequest, "validation failed"
	default:
		return http.StatusInternalServerError, "internal error"
	}
}
