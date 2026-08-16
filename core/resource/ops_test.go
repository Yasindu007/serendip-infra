package resource_test

import (
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestOps_KindConstants(t *testing.T) {
	tests := []struct {
		kind resource.Kind
		want string
	}{
		{resource.KindAlert, "Alert"},
		{resource.KindIncident, "Incident"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.kind) != tt.want {
				t.Errorf("kind = %q, want %q", tt.kind, tt.want)
			}
		})
	}
}

func TestAlert_ValidEnvelope(t *testing.T) {
	alert := resource.Alert{
		Metadata: resource.Metadata{Name: "high-cpu-node-1", Kind: resource.KindAlert},
		Spec: resource.AlertSpec{
			Source:   "observability-backend",
			Rule:     "cpu > 90% for 5m",
			Severity: "warning",
		},
		Status: resource.AlertStatus{FiredAt: time.Now()},
	}
	if err := resource.Validate(alert.Metadata); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if alert.Status.FiredAt.IsZero() {
		t.Error("FiredAt should be set")
	}
}

func TestIncident_NoLifecycleLogicYet(t *testing.T) {
	// spec 001 scope: types only. Nothing here computes correlation --
	// this just pins that the shape exists for operations.md to build on.
	incident := resource.Incident{
		Metadata: resource.Metadata{Name: "inc-2026-08-16-001", Kind: resource.KindIncident},
		Spec: resource.IncidentSpec{
			Severity: "critical",
			Summary:  "database unreachable",
		},
		Status: resource.IncidentStatus{
			CorrelatedAlerts:  []string{"alert-1", "alert-2"},
			AffectedResources: []string{"component-backend"},
		},
	}
	if err := resource.Validate(incident.Metadata); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if len(incident.Status.CorrelatedAlerts) != 2 {
		t.Errorf("len(CorrelatedAlerts) = %d, want 2", len(incident.Status.CorrelatedAlerts))
	}
}
