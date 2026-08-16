package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/id"
)

// InterfaceName is what this API identifies itself as in
// audit.Record.Interface -- replaces spec 001's "core" default for every
// mutation that goes through this package.
const InterfaceName = "http"

// RequestIDMiddleware assigns a UUIDv7 request ID (reusing core/id, the
// same generator spec 001 uses for resource IDs) to every request,
// setting it as a response header and injecting it into the request
// context via core/audit.WithRequestID so downstream core/store writes
// (audit records) can attach it. Also tags the context with this
// package's interface name (audit.WithInterface) -- both happen here,
// in one middleware, since both are per-request bookkeeping with no
// decision logic (unlike ActorMiddleware, T004, which can reject a
// request).
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := id.New()
		w.Header().Set("X-Request-Id", reqID)

		ctx := audit.WithRequestID(r.Context(), reqID)
		ctx = audit.WithInterface(ctx, InterfaceName)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the request ID set by RequestIDMiddleware,
// or "" if none was set (e.g. a test that doesn't wire the middleware).
func RequestIDFromContext(ctx context.Context) string {
	return audit.RequestIDFrom(ctx)
}

// ActorMiddleware resolves the caller's identity from a static API-key
// map (apiKeys: token -> actor name) and injects it via audit.WithActor.
// A missing or unrecognized token gets a 401 problem+json response and
// the handler chain short-circuits before reaching core/store -- no
// audit record, no data change, no event for a rejected request
// (specs/002-http-api/spec.md acceptance criterion 3).
//
// This is intentionally weak: no real identity provider is implemented
// yet (that's a future providers/identity spec). The swap-in point for
// real OIDC/Thunder-backed auth is this one middleware -- nothing else
// in api/ needs to change when that happens, since everything downstream
// only ever sees the resolved actor via context, never how it got there.
func ActorMiddleware(apiKeys map[string]string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			var actor string
			if ok {
				actor, ok = apiKeys[token]
			}
			if !ok {
				WriteProblem(w, http.StatusUnauthorized, "unauthorized",
					"missing or invalid bearer token", audit.RequestIDFrom(r.Context()))
				return
			}
			ctx := audit.WithActor(r.Context(), actor)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}
