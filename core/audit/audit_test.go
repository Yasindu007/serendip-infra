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

func TestRequestIDFrom_DefaultsToEmpty(t *testing.T) {
	got := audit.RequestIDFrom(context.Background())
	if got != "" {
		t.Errorf("RequestIDFrom(no request ID set) = %q, want empty", got)
	}
}

func TestWithRequestID_RoundTrips(t *testing.T) {
	ctx := audit.WithRequestID(context.Background(), "req-abc-123")
	if got := audit.RequestIDFrom(ctx); got != "req-abc-123" {
		t.Errorf("RequestIDFrom(WithRequestID(...)) = %q, want %q", got, "req-abc-123")
	}
}

func TestInterfaceFrom_DefaultsToCore(t *testing.T) {
	got := audit.InterfaceFrom(context.Background())
	if got != "core" {
		t.Errorf("InterfaceFrom(no interface set) = %q, want %q", got, "core")
	}
}

func TestWithInterface_RoundTrips(t *testing.T) {
	tests := []struct{ name string }{{"http"}, {"cli"}, {"mcp"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := audit.WithInterface(context.Background(), tt.name)
			if got := audit.InterfaceFrom(ctx); got != tt.name {
				t.Errorf("InterfaceFrom(WithInterface(%q)) = %q, want %q", tt.name, got, tt.name)
			}
		})
	}
}
