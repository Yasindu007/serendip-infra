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
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
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
	return authMiddleware(apiKeys, authn, nil, nil, nil)
}

// authMiddleware is AuthMiddleware plus, when console is non-nil, the
// console's session cookie as a third credential (specs/016-ui-mvp plan.md
// decision 6). A bearer token is checked exactly as before. A request
// carrying both a bearer token and a session cookie is refused, so the two
// can never be confused. A cookie request must also pass the CSRF checks.
//
// A person's subject goes into the context for the audit record
// (audit.WithSubject), and when principals is non-nil every authenticated
// caller is recorded there, so it can be picked when assigning a role
// (specs/017-authorization plan.md decision 6). Failing to record is
// logged, never fatal to the request.
//
// An agent (a token from an agent issuer, spec 018) becomes an agent
// principal "<issuer alias>/<subject>" with no groups, and its sponsor,
// from sponsorOf when non-nil, goes into the context for the audit record
// (audit.WithSponsor). An agent with no sponsor has no assignment, so it
// can do nothing anyway. When the sponsor can't be looked up the request
// gets 503, so no agent action is ever recorded without its sponsor.
func authMiddleware(apiKeys map[string]string, authn identity.Authenticator, console *Console, principals *sqlite.PrincipalRegistry, sponsorOf func(ctx context.Context, agentID string) (string, error)) func(http.Handler) http.Handler {
	person := func(ctx context.Context, p identity.Principal) (context.Context, error) {
		if p.Kind == identity.KindAgent {
			id := identity.AgentID(p)
			recordSeen(ctx, principals, sqlite.KnownPrincipal{Type: resource.PrincipalAgent, ID: id, DisplayName: id})
			ctx = withCaller(ctx, policy.Principal{Type: resource.PrincipalAgent, ID: id})
			ctx = audit.WithSubject(audit.WithActor(ctx, identity.Actor(p)), id)
			if sponsorOf != nil {
				s, err := sponsorOf(ctx, id)
				if err != nil {
					return nil, err
				}
				if s != "" {
					ctx = audit.WithSponsor(ctx, s)
				}
			}
			return ctx, nil
		}
		recordSeen(ctx, principals, sqlite.KnownPrincipal{Type: resource.PrincipalPerson, ID: p.Subject, DisplayName: p.Name, Groups: p.Groups})
		ctx = withCaller(ctx, policy.Principal{Type: resource.PrincipalPerson, ID: p.Subject, Groups: p.Groups})
		return audit.WithSubject(audit.WithActor(ctx, identity.Actor(p)), p.Subject), nil
	}
	// serve resolves p and runs next, or answers 503 when it can't.
	serve := func(w http.ResponseWriter, r *http.Request, next http.Handler, ctx context.Context, p identity.Principal) {
		ctx, err := person(ctx, p)
		if err != nil {
			reqID := audit.RequestIDFrom(r.Context())
			log.Printf("api: caller %s not resolved (request %s): %v", identity.Actor(p), reqID, err)
			WriteProblem(w, http.StatusServiceUnavailable, "not ready", "the caller's details can't be read right now; retry later", reqID)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := audit.RequestIDFrom(r.Context())
			token, hasBearer := bearerToken(r)
			var sessionID string
			if console != nil {
				if ck, err := r.Cookie(SessionCookie); err == nil && ck.Value != "" {
					sessionID = ck.Value
				}
			}
			if hasBearer && sessionID != "" {
				WriteProblem(w, http.StatusBadRequest, "ambiguous credentials",
					"send either a bearer token or the console session cookie, not both", reqID)
				return
			}
			if sessionID != "" {
				p, ctx, ok := console.authenticateCookie(w, r, sessionID)
				if !ok {
					return
				}
				serve(w, r, next, ctx, p)
				return
			}
			if !hasBearer {
				WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", reqID)
				return
			}
			if actor, ok := apiKeys[token]; ok {
				recordSeen(r.Context(), principals, sqlite.KnownPrincipal{Type: resource.PrincipalAPIKey, ID: actor, DisplayName: actor})
				ctx := withCaller(r.Context(), policy.Principal{Type: resource.PrincipalAPIKey, ID: actor})
				next.ServeHTTP(w, r.WithContext(audit.WithActor(ctx, actor)))
				return
			}
			if authn != nil {
				p, err := authn.Authenticate(r.Context(), token)
				switch {
				case err == nil:
					serve(w, r, next, r.Context(), p)
					return
				case errors.Is(err, reconcile.ErrProviderNotReady):
					WriteProblem(w, http.StatusServiceUnavailable, "identity provider not ready",
						"the bearer token can't be verified yet; retry later", reqID)
					return
				default:
					log.Printf("api: bearer token rejected (request %s): %v", reqID, err)
				}
			}
			WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", reqID)
		})
	}
}

// recordSeen notes an authenticated principal in the registry, if any.
func recordSeen(ctx context.Context, principals *sqlite.PrincipalRegistry, p sqlite.KnownPrincipal) {
	if principals == nil || p.ID == "" {
		return
	}
	if err := principals.Seen(ctx, p); err != nil {
		log.Printf("api: record principal %s %s (request %s): %v", p.Type, p.ID, audit.RequestIDFrom(ctx), err)
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
