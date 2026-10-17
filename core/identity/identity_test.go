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
	}
	for _, tt := range tests {
		if got := identity.Actor(tt.p); got != tt.want {
			t.Errorf("Actor(%+v) = %q, want %q", tt.p, got, tt.want)
		}
		if !strings.HasPrefix(identity.Actor(tt.p), identity.ActorPrefix) {
			t.Errorf("Actor(%+v) lacks ActorPrefix", tt.p)
		}
	}
}
