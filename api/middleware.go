package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/id"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
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
// decision logic (unlike AuthMiddleware, which can reject a
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

// AuthMiddleware resolves the caller's identity and injects it via
// audit.WithActor. A bearer token is first looked up in the static API-key
// map (apiKeys: token -> actor name, unchanged since spec 002); anything
// else goes to authn, the Identity capability's Authenticator, whose
// Principal becomes a "person:" actor (specs/015-identity-foundation
// plan.md decision 11). authn may be nil (no identity provider wired, e.g.
// no cluster), which leaves API keys as the only way in, exactly as before.
//
// A missing or rejected token gets a 401 problem+json response; a token the
// identity provider can't check yet gets a 503 (it may well be valid). In
// both cases the handler chain short-circuits before reaching core/store --
// no audit record, no data change, no event for a rejected request
// (specs/002-http-api/spec.md acceptance criterion 3). The detail never
// says which check a token failed. Everything downstream only ever sees the
// resolved actor via context, never how it got there.
func AuthMiddleware(apiKeys map[string]string, authn identity.Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := audit.RequestIDFrom(r.Context())
			token, ok := bearerToken(r)
			if !ok {
				WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", reqID)
				return
			}
			actor, ok := apiKeys[token]
			if !ok && authn != nil {
				p, err := authn.Authenticate(r.Context(), token)
				switch {
				case err == nil:
					actor, ok = identity.Actor(p), true
				case errors.Is(err, reconcile.ErrProviderNotReady):
					WriteProblem(w, http.StatusServiceUnavailable, "identity provider not ready",
						"the bearer token can't be verified yet; retry later", reqID)
					return
				default:
					log.Printf("api: bearer token rejected (request %s): %v", reqID, err)
				}
			}
			if !ok {
				WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", reqID)
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
