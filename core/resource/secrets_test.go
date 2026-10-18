package resource_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestSecret_KindConstant(t *testing.T) {
	if string(resource.KindSecret) != "Secret" {
		t.Errorf("KindSecret = %q, want %q", resource.KindSecret, "Secret")
	}
}

func TestSecret_ValidEnvelope(t *testing.T) {
	sec := resource.Secret{
		Metadata: resource.Metadata{Name: "db-password", Kind: resource.KindSecret},
		Spec:     resource.SecretSpec{Path: "myapp/db-password"},
	}
	if err := resource.Validate(sec.Metadata); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if sec.Spec.Path != "myapp/db-password" {
		t.Errorf("Path = %q, want %q", sec.Spec.Path, "myapp/db-password")
	}
}

func TestSecret_InvalidEnvelope_EmptyName(t *testing.T) {
	sec := resource.Secret{
		Metadata: resource.Metadata{Kind: resource.KindSecret},
		Spec:     resource.SecretSpec{Path: "myapp/db-password"},
	}
	if err := resource.Validate(sec.Metadata); err == nil {
		t.Fatal("Validate(empty Name) = nil, want an error (same rule every other kind follows)")
	}
}

func TestSecret_StatusHasConditionsLikeEveryOtherKind(t *testing.T) {
	sec := resource.Secret{
		Status: resource.SecretStatus{
			Status: resource.Status{
				Conditions: []resource.Condition{
					{Type: "Reachable", Status: resource.ConditionTrue, Reason: "ValueExists"},
				},
			},
		},
	}
	if len(sec.Status.Conditions) != 1 {
		t.Fatalf("Conditions = %+v, want exactly 1", sec.Status.Conditions)
	}
	if sec.Status.Conditions[0].Type != "Reachable" {
		t.Errorf("Conditions[0].Type = %q, want %q", sec.Status.Conditions[0].Type, "Reachable")
	}
}

// TestSecretSpec_HasNoFieldCapableOfHoldingAValue is this spec's central
// security property, checked structurally (not just "we didn't populate
// one in a test"): SecretSpec must have exactly the one field it's
// documented to have (Path), so there is no second field a future
// change could accidentally start stuffing a plaintext value into
// without this test catching it.
func TestSecretSpec_HasNoFieldCapableOfHoldingAValue(t *testing.T) {
	typ := reflect.TypeFor[resource.SecretSpec]()
	if typ.NumField() != 1 {
		t.Fatalf("SecretSpec has %d fields, want exactly 1 (Path) -- a new field here is exactly how a secret value would end up permanently captured in core/audit's whole-resource snapshots", typ.NumField())
	}
	if typ.Field(0).Name != "Path" {
		t.Errorf("SecretSpec's one field = %q, want %q", typ.Field(0).Name, "Path")
	}
}

// TestSecretStatus_OnlyKnownFieldsBesidesConditions guards the same
// property on Status: only the embedded Status (Conditions) and
// LastSyncedAt are allowed. LastSyncedAt is a timestamp, structurally
// incapable of holding an arbitrary secret value.
func TestSecretStatus_OnlyKnownFieldsBesidesConditions(t *testing.T) {
	typ := reflect.TypeFor[resource.SecretStatus]()
	if typ.NumField() != 2 {
		t.Fatalf("SecretStatus has %d fields, want exactly 2 (embedded Status, LastSyncedAt)", typ.NumField())
	}
	if typ.Field(1).Name != "LastSyncedAt" {
		t.Errorf("SecretStatus's second field = %q, want %q", typ.Field(1).Name, "LastSyncedAt")
	}
}

func TestValidateSecretSpec(t *testing.T) {
	good := []string{"db-password", "myapp/db-password", "a/b/c", "team.x/key_1"}
	bad := []string{"", "/abs", "trailing/", "a//b", "a/../b", "..", ".", "a/./b", "a?b", "a#b", "a%2Fb", "a\\b", "a\nb", strings.Repeat("a", resource.MaxSecretPathLen+1)}
	for _, p := range good {
		if err := resource.ValidateSecretSpec(resource.SecretSpec{Path: p}); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	for _, p := range bad {
		if err := resource.ValidateSecretSpec(resource.SecretSpec{Path: p}); !errors.Is(err, resource.ErrValidation) {
			t.Errorf("%q: err = %v, want ErrValidation", p, err)
		}
	}
}
