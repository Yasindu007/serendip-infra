package id_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/serendip-infra/serendip-infra/core/id"
)

func TestNew_ValidUUIDv7(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"first"}, {"second"}, {"third"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := id.New()
			parsed, err := uuid.Parse(got)
			if err != nil {
				t.Fatalf("id.New() = %q, not a valid UUID: %v", got, err)
			}
			if parsed.Version() != 7 {
				t.Errorf("id.New() version = %d, want 7 (UUIDv7)", parsed.Version())
			}
			if len(got) != 36 {
				t.Errorf("id.New() length = %d, want 36 (canonical UUID string)", len(got))
			}
		})
	}
}

func TestNew_NoCollisions(t *testing.T) {
	const n = 100_000
	seen := make(map[string]struct{}, n)
	for i := range n {
		got := id.New()
		if _, dup := seen[got]; dup {
			t.Fatalf("id.New() produced a duplicate after %d generations: %q", i, got)
		}
		seen[got] = struct{}{}
	}
}
