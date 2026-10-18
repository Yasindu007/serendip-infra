package identity_test

import (
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/identity"
)

func TestActor(t *testing.T) {
	tests := []struct {
		p    identity.Principal
		want string
	}{
		{identity.Principal{Subject: "0190-0030", Name: "admin"}, "person:admin"},
		{identity.Principal{Subject: "s", Name: "a@example.com"}, "person:a@example.com"},
		{identity.Principal{Kind: identity.KindAgent, Issuer: "am-dev", Subject: "01a1", Name: "01a1"}, "agent:am-dev/01a1"},
	}
	for _, tt := range tests {
		if got := identity.Actor(tt.p); got != tt.want {
			t.Errorf("Actor(%+v) = %q, want %q", tt.p, got, tt.want)
		}
		prefix := identity.ActorPrefix
		if tt.p.Kind == identity.KindAgent {
			prefix = identity.AgentActorPrefix
		}
		if !strings.HasPrefix(identity.Actor(tt.p), prefix) {
			t.Errorf("Actor(%+v) lacks %q", tt.p, prefix)
		}
	}
}
