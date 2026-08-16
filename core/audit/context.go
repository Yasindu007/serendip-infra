package audit

import "context"

type actorKey struct{}

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
