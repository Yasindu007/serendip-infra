package oidc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/serendip-infra/serendip-infra/core/identity"
)

// IssuerRouter is an identity.Authenticator over the human issuer and any
// number of agent issuers (specs/018-agent-manager-poc plan.md decision 7).
// It reads a token's "iss" without trusting it, only to pick the one
// verifier for exactly that issuer; that verifier then checks everything,
// including "iss" itself. No issuer is ever tried as a fallback for
// another, so a token is an agent only when an agent issuer signed it.
type IssuerRouter struct {
	people identity.Authenticator
	agents map[string]identity.Authenticator
}

var _ identity.Authenticator = (*IssuerRouter)(nil)

// NewIssuerRouter routes tokens from each issuer in agents (issuer URL ->
// its verifier) there, and every other token to people, which may be nil
// when only agents are accepted.
func NewIssuerRouter(people identity.Authenticator, agents map[string]identity.Authenticator) *IssuerRouter {
	return &IssuerRouter{people: people, agents: agents}
}

// Authenticate verifies raw with the verifier its issuer names.
func (r *IssuerRouter) Authenticate(ctx context.Context, raw string) (identity.Principal, error) {
	t, err := parseToken(raw)
	if err != nil {
		return identity.Principal{}, invalid(err.Error())
	}
	var c struct {
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(t.payload, &c); err != nil {
		return identity.Principal{}, invalid("claims: " + err.Error())
	}
	if a, ok := r.agents[c.Iss]; ok {
		return a.Authenticate(ctx, raw)
	}
	if r.people == nil {
		return identity.Principal{}, invalid(fmt.Sprintf("issuer %q not trusted", c.Iss))
	}
	return r.people.Authenticate(ctx, raw)
}
