package audit

import "context"

type actorKey struct{}
type requestIDKey struct{}
type interfaceKey struct{}

// WithActor returns a context carrying the given actor identity, read
// back by ActorFrom. There is no real identity/auth system yet (that
// arrives with spec 002's HTTP API and beyond) -- callers supply this
// explicitly: tests set it directly, a future HTTP handler will derive it
// from an authenticated request.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom returns the actor set by WithActor, or "unknown" if none was
// set. A mutation must never silently attribute to an empty actor string.
func ActorFrom(ctx context.Context) string {
	actor, _ := ctx.Value(actorKey{}).(string)
	if actor == "" {
		return "unknown"
	}
	return actor
}

// WithRequestID returns a context carrying the given request ID, read
// back by RequestIDFrom. Set by api's request-ID middleware (spec 002);
// core/store/sqlite reads it back to attach to the audit_log row for
// whatever mutation the request causes.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// RequestIDFrom returns the request ID set by WithRequestID, or "" if
// none was set -- e.g. a direct core/store call made outside any HTTP
// request (a test, a future reconciler) has no request to attach.
func RequestIDFrom(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	return requestID
}

// WithInterface returns a context carrying the name of the interface
// making a mutation (e.g. "http" for spec 002's API), read back by
// InterfaceFrom.
func WithInterface(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, interfaceKey{}, name)
}

// InterfaceFrom returns the interface name set by WithInterface, or
// "core" if none was set -- preserves spec 001's behavior (every
// mutation was made directly against core/store, with no HTTP/CLI/MCP
// layer yet) for any caller that doesn't set this explicitly, e.g. tests.
func InterfaceFrom(ctx context.Context) string {
	name, _ := ctx.Value(interfaceKey{}).(string)
	if name == "" {
		return "core"
	}
	return name
}
