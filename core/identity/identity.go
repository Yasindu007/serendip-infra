package identity

import (
	"context"
	"errors"
	"time"
)

// Principal is an authenticated person.
type Principal struct {
	// Subject is the provider's stable, usually opaque, subject identifier.
	Subject string
	// Name is the human-readable name recorded as the audit actor.
	Name string
	// Groups are the names of the groups the person belongs to, as the
	// provider reports them in the access token. Empty when the provider
	// doesn't send them.
	Groups []string
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

// ErrUnsupported is returned by a LoginClient operation the identity
// provider doesn't offer (no revocation or end-session endpoint).
var ErrUnsupported = errors.New("identity: not supported by the provider")

// Tokens is what a successful code exchange or refresh yields. None of it
// ever leaves the server: a browser session holds only an opaque ID that
// refers to these.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	// Expiry is when AccessToken stops being accepted.
	Expiry time.Time
}

// LoginClient runs a browser login against the identity provider on the
// server's side: the authorization-code flow with PKCE, refresh, and
// logout. Errors follow Authenticator's rule: a provider that rejects a
// code or refresh token yields an error wrapping ErrInvalidCredential; a
// provider that can't be reached yields reconcile.ErrProviderNotReady, so
// callers can tell "this login is over" from "try again". Errors never
// contain a token, code or verifier.
type LoginClient interface {
	// AuthCodeURL is where to send the browser to log in. codeChallenge is
	// the S256 challenge of the verifier later passed to Exchange. state,
	// nonce and codeChallenge are all required.
	AuthCodeURL(ctx context.Context, state, nonce, codeChallenge string) (string, error)
	// Exchange trades an authorization code for tokens.
	Exchange(ctx context.Context, code, codeVerifier string) (Tokens, error)
	// Refresh trades a refresh token for new tokens. A provider that
	// rotates refresh tokens returns the replacement in Tokens.RefreshToken;
	// otherwise that field is empty and the old one stays valid.
	Refresh(ctx context.Context, refreshToken string) (Tokens, error)
	// Revoke invalidates a refresh token, or returns ErrUnsupported.
	// Callers treat it as best effort: logout proceeds whatever it returns.
	Revoke(ctx context.Context, refreshToken string) error
	// EndSessionURL is where to send the browser to end its session at
	// the provider, or ErrUnsupported.
	EndSessionURL(ctx context.Context, idTokenHint string) (string, error)
	// VerifyIDToken checks an ID token's signature, issuer, audience,
	// expiry and nonce, and returns its subject. An empty nonce is an
	// error, never a skipped check.
	VerifyIDToken(ctx context.Context, raw, nonce string) (subject string, err error)
}

// ActorPrefix starts every audit actor that names an authenticated person,
// so it can't be confused with a static API-key actor (which may not use
// it).
const ActorPrefix = "person:"

// Actor is the audit actor for p.
func Actor(p Principal) string {
	return ActorPrefix + p.Name
}
