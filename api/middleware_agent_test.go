package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/identity"
)

type oneAgent struct{}

func (oneAgent) Authenticate(context.Context, string) (identity.Principal, error) {
	return identity.Principal{Kind: identity.KindAgent, Issuer: "am-dev", Subject: "a1", Name: "a1"}, nil
}

// An agent whose sponsor can't be looked up is refused, never served with
// an audit trail missing its sponsor.
func TestAuthMiddleware_AgentSponsor(t *testing.T) {
	tests := []struct {
		name        string
		sponsorOf   func(context.Context, string) (string, error)
		wantStatus  int
		wantSponsor string
	}{
		{"sponsored", func(context.Context, string) (string, error) { return "sub-owner", nil }, http.StatusOK, "sub-owner"},
		{"no assignment", func(context.Context, string) (string, error) { return "", nil }, http.StatusOK, ""},
		{"lookup fails", func(context.Context, string) (string, error) { return "", errors.New("database is locked") }, http.StatusServiceUnavailable, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotSponsor, gotActor string
			h := authMiddleware(nil, oneAgent{}, nil, nil, tt.sponsorOf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotSponsor, gotActor = audit.SponsorFrom(r.Context()), audit.ActorFrom(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/v1/session", nil)
			req.Header.Set("Authorization", "Bearer agent-token")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus || gotSponsor != tt.wantSponsor {
				t.Errorf("status %d, sponsor %q; want %d, %q", rec.Code, gotSponsor, tt.wantStatus, tt.wantSponsor)
			}
			if tt.wantStatus == http.StatusOK && gotActor != "agent:am-dev/a1" {
				t.Errorf("actor = %q", gotActor)
			}
		})
	}
}
