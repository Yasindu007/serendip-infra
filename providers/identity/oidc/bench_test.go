package oidc_test

import (
	"slices"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/providers/identity/oidc"
)

// BenchmarkTokens measures verifying real RS256 tokens (specs/018-agent-
// manager-poc T010): a person's token with the human verifier alone (the
// spec 017 path), then a person's and an agent's token through the issuer
// router. The router's cost is the person pair's difference; the agent's
// extra checks are the router pair's.
func BenchmarkTokens(b *testing.B) {
	people, agents := newIssuer(&testing.T{}), newIssuer(&testing.T{})
	defer people.srv.Close()
	defer agents.srv.Close()
	now := time.Now()
	pv, err := oidc.NewVerifier(oidc.Config{Issuer: people.url(), Audience: "serendip", Now: func() time.Time { return now }})
	if err != nil {
		b.Fatal(err)
	}
	av, err := oidc.NewVerifier(oidc.Config{Issuer: agents.url(), Audience: agentAud, Kind: identity.KindAgent, IssuerAlias: "am-dev", Now: func() time.Time { return now }})
	if err != nil {
		b.Fatal(err)
	}
	r := oidc.NewIssuerRouter(pv, map[string]identity.Authenticator{agents.url(): av})
	person := people.sign("RS256", "rsa1", people.claims(now, nil))
	agent := agents.sign("RS256", "rsa1", agents.agentClaims(now, nil))

	run := func(b *testing.B, a identity.Authenticator, tok string) {
		if _, err := a.Authenticate(b.Context(), tok); err != nil { // fetches keys once
			b.Fatal(err)
		}
		var d []time.Duration
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			start := time.Now()
			if _, err := a.Authenticate(b.Context(), tok); err != nil {
				b.Fatal(err)
			}
			d = append(d, time.Since(start))
		}
		b.StopTimer()
		slices.Sort(d)
		b.ReportMetric(float64(d[len(d)/2].Microseconds()), "p50-µs")
		b.ReportMetric(float64(d[len(d)*99/100].Microseconds()), "p99-µs")
	}
	b.Run("person/verifier", func(b *testing.B) { run(b, pv, person) })
	b.Run("person/router", func(b *testing.B) { run(b, r, person) })
	b.Run("agent/router", func(b *testing.B) { run(b, r, agent) })
}
