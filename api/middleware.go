package api

import (
	"context"
	"net/http"

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
