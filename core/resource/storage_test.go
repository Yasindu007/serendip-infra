package resource_test

import (
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestStorage_KindConstant(t *testing.T) {
	if string(resource.KindVolume) != "Volume" {
		t.Errorf("KindVolume = %q, want %q", resource.KindVolume, "Volume")
	}
}

func TestVolume_ValidEnvelope(t *testing.T) {
	vol := resource.Volume{
		Metadata: resource.Metadata{Name: "data-vol", Kind: resource.KindVolume},
		Spec:     resource.VolumeSpec{SizeBytes: 10 << 30},
	}
	if err := resource.Validate(vol.Metadata); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if vol.Spec.SizeBytes != 10<<30 {
		t.Errorf("SizeBytes = %d, want %d", vol.Spec.SizeBytes, 10<<30)
	}
}

func TestVolume_InvalidEnvelope_EmptyName(t *testing.T) {
	vol := resource.Volume{
		Metadata: resource.Metadata{Kind: resource.KindVolume},
		Spec:     resource.VolumeSpec{SizeBytes: 1 << 30},
	}
	if err := resource.Validate(vol.Metadata); err == nil {
		t.Fatal("Validate(empty Name) = nil, want an error (same rule every other kind follows)")
	}
}

func TestVolume_StatusHasConditionsLikeEveryOtherKind(t *testing.T) {
	vol := resource.Volume{
		Status: resource.VolumeStatus{
			Status: resource.Status{
				Conditions: []resource.Condition{
					{Type: "Bound", Status: resource.ConditionTrue, Reason: "Bound"},
				},
			},
		},
	}
	if len(vol.Status.Conditions) != 1 {
		t.Fatalf("Conditions = %+v, want exactly 1", vol.Status.Conditions)
	}
	if vol.Status.Conditions[0].Type != "Bound" {
		t.Errorf("Conditions[0].Type = %q, want %q", vol.Status.Conditions[0].Type, "Bound")
	}
}
