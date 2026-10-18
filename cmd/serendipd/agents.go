package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/providers/identity/oidc"
)

// agentIssuer is one trusted issuer of agent tokens
// (specs/018-agent-manager-poc plan.md decision 6).
type agentIssuer struct {
	alias       string
	issuer      string
	audience    string
	maxLifetime time.Duration // 0: the verifier's default
}

// parseAgentIssuers parses SERENDIP_AGENT_ISSUERS: comma-separated
// "alias=issuer|audience[|maxLifetime]", for example
// "am-dev=http://default-idp.amp.localhost:8080|https://serendip.local/api|1h".
// Unlike API keys, a malformed entry is an error, not a skipped line: a
// trust root that is silently wrong must not start. humanIssuer, when set,
// is the configured human issuer, which no agent issuer may equal.
func parseAgentIssuers(raw, humanIssuer string) ([]agentIssuer, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []agentIssuer
	aliases, issuers := map[string]bool{}, map[string]bool{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		alias, rest, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("SERENDIP_AGENT_ISSUERS entry %q: want alias=issuer|audience[|maxLifetime]", entry)
		}
		parts := strings.Split(rest, "|")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, fmt.Errorf("SERENDIP_AGENT_ISSUERS entry %q: want alias=issuer|audience[|maxLifetime]", entry)
		}
		a := agentIssuer{alias: alias, issuer: parts[0], audience: parts[1]}
		if !oidc.ValidIssuerAlias(a.alias) {
			return nil, fmt.Errorf("SERENDIP_AGENT_ISSUERS: alias %q: want 1-32 of a-z, 0-9 and -", a.alias)
		}
		if err := validIssuerURL(a.issuer); err != nil {
			return nil, fmt.Errorf("SERENDIP_AGENT_ISSUERS %s: %w", a.alias, err)
		}
		if a.audience == "" {
			return nil, fmt.Errorf("SERENDIP_AGENT_ISSUERS %s: an audience is required", a.alias)
		}
		if len(parts) == 3 {
			d, err := time.ParseDuration(parts[2])
			if err != nil || d <= 0 || d > oidc.MaxAgentTokenLifetime {
				return nil, fmt.Errorf("SERENDIP_AGENT_ISSUERS %s: maxLifetime %q: want a duration up to %s", a.alias, parts[2], oidc.MaxAgentTokenLifetime)
			}
			a.maxLifetime = d
		}
		switch {
		case aliases[a.alias]:
			return nil, fmt.Errorf("SERENDIP_AGENT_ISSUERS: alias %q is used twice", a.alias)
		case issuers[strings.TrimSuffix(a.issuer, "/")]:
			return nil, fmt.Errorf("SERENDIP_AGENT_ISSUERS: issuer %q is listed twice", a.issuer)
		case humanIssuer != "" && strings.TrimSuffix(a.issuer, "/") == strings.TrimSuffix(humanIssuer, "/"):
			return nil, fmt.Errorf("SERENDIP_AGENT_ISSUERS %s: the human identity issuer can't also be an agent issuer", a.alias)
		}
		aliases[a.alias], issuers[strings.TrimSuffix(a.issuer, "/")] = true, true
		out = append(out, a)
	}
	return out, nil
}

func validIssuerURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("issuer %q: %w", s, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("issuer %q: want an absolute http(s) URL without query, fragment or user info", s)
	}
	return nil
}

// agentVerifiers builds one verifier per agent issuer, keyed by issuer URL,
// and fetches each issuer's keys once in the background so the log says
// whether agents can be verified. A failed fetch is retried on the first
// agent token.
func agentVerifiers(ctx context.Context, issuers []agentIssuer) (map[string]identity.Authenticator, error) {
	out := make(map[string]identity.Authenticator, len(issuers))
	for _, a := range issuers {
		v, err := oidc.NewVerifier(oidc.Config{
			Issuer:      a.issuer,
			Audience:    a.audience,
			Kind:        identity.KindAgent,
			IssuerAlias: a.alias,
			MaxLifetime: a.maxLifetime,
		})
		if err != nil {
			return nil, fmt.Errorf("agent issuer %s: %w", a.alias, err)
		}
		out[a.issuer] = v
		go func() {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			if err := v.Warm(ctx); err != nil {
				log.Printf("serendipd: agent issuer %s (%s): signing keys not reachable yet; agent tokens get 503 until they are: %v", a.alias, a.issuer, err)
				return
			}
			log.Printf("serendipd: agent issuer %s trusted: %s, audience %q (agent tokens only)", a.alias, a.issuer, a.audience)
		}()
	}
	return out, nil
}
