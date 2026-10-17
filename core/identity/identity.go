package identity

import (
	"context"
	"errors"
)

// Principal is an authenticated person.
type Principal struct {
	// Subject is the provider's stable, usually opaque, subject identifier.
	Subject string
	// Name is the human-readable name recorded as the audit actor.
	Name string
}

// Authenticator verifies a bearer credential.
type Authenticator interface {
	// Authenticate returns the Principal a credential belongs to. Every
	// credential that isn't valid -- malformed, expired, wrongly signed,
	// for another issuer or audience -- yields an error wrapping
	// ErrInvalidCredential. A credential that can't be checked yet (the
	// provider is still being set up, or its keys are unreachable and none
	// are cached) yields reconcile.ErrProviderNotReady instead, so callers
	// can say "try again" rather than "no".
	Authenticate(ctx context.Context, token string) (Principal, error)
}

// ErrInvalidCredential is wrapped by every rejection of a credential.
var ErrInvalidCredential = errors.New("identity: invalid credential")

// ActorPrefix starts every audit actor that names an authenticated person,
// so it can't be confused with a static API-key actor (which may not use
// it).
const ActorPrefix = "person:"

// Actor is the audit actor for p.
func Actor(p Principal) string {
	return ActorPrefix + p.Name
}
