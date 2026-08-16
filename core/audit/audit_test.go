package audit_test

import (
	"context"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/audit"
)

func TestActorFrom_DefaultsToUnknown(t *testing.T) {
	got := audit.ActorFrom(context.Background())
	if got != "unknown" {
		t.Errorf("ActorFrom(no actor set) = %q, want %q", got, "unknown")
	}
}

func TestWithActor_RoundTrips(t *testing.T) {
	tests := []struct{ actor string }{
		{"alice"}, {"system:reconciler"}, {"api:svc-account-1"},
	}
	for _, tt := range tests {
		t.Run(tt.actor, func(t *testing.T) {
			ctx := audit.WithActor(context.Background(), tt.actor)
			if got := audit.ActorFrom(ctx); got != tt.actor {
				t.Errorf("ActorFrom(WithActor(%q)) = %q, want %q", tt.actor, got, tt.actor)
			}
		})
	}
}
