package identity

import (
	"context"
	"errors"
	"time"
)

// Kind is what sort of caller a Principal is.
type Kind int

const (
	// KindPerson is someone who signed in to the identity provider. It is
	// the zero value.
	KindPerson Kind = iota
	// KindAgent is an AI agent with its own identity from a trusted agent
	// issuer (specs/018-agent-manager-poc plan.md decision 7). It is never
	// a person, and it has no groups.
	KindAgent
)

// Principal is an authenticated caller: a person, or an agent.
type Principal struct {
	// Kind says which. A token from the human issuer is always a person,
	// and one from an agent issuer always an agent.
	Kind Kind
	// Issuer is the alias of the agent issuer an agent came from; empty
	// for a person.
	Issuer string
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

// AgentActorPrefix starts every audit actor that names an agent. API keys
// may not use it either.
const AgentActorPrefix = "agent:"

// AgentID is an agent's principal ID: its issuer's alias and its subject,
// so subjects from different agent issuers can never collide.
func AgentID(p Principal) string {
	return p.Issuer + "/" + p.Subject
}

// Actor is the audit actor for p: "person:<name>" for a person, and
// "agent:<issuer alias>/<subject>" for an agent.
func Actor(p Principal) string {
	if p.Kind == KindAgent {
		return AgentActorPrefix + AgentID(p)
	}
	return ActorPrefix + p.Name
}

// DirectoryEntry is one person or group in the identity provider's
// directory. For a person, ID is the subject their tokens carry and Name
// is the name they sign in with. For a group, ID and Name are both the
// group's name, since that is what tokens list and what role assignments
// name.
type DirectoryEntry struct {
	ID   string
	Name string
}

// DirectoryResult is one search's answer. Truncated says the provider
// holds more than was searched or returned, so a missing entry may still
// exist.
type DirectoryResult struct {
	Items     []DirectoryEntry
	Truncated bool
}

// Directory reads the identity provider's people and groups as they are
// now, rather than as they were when someone last signed in. Every method
// returns an error wrapping reconcile.ErrProviderNotReady when the
// provider can't be reached, so callers can fall back to what they already
// know; such an error may name provider internals, so it is logged, never
// shown to an API client.
type Directory interface {
	// People returns the people whose name, display name or email
	// contains query, ignoring case; an empty query matches everyone.
	People(ctx context.Context, query string) (DirectoryResult, error)
	// Groups returns the groups whose name contains query, ignoring case.
	Groups(ctx context.Context, query string) (DirectoryResult, error)
	// GroupsOf returns the names of the groups subject belongs to
	// directly, or ErrUnknownSubject.
	GroupsOf(ctx context.Context, subject string) ([]string, error)
}

// ErrUnknownSubject is returned by Directory.GroupsOf for a subject the
// provider doesn't hold.
var ErrUnknownSubject = errors.New("identity: no such subject in the directory")
