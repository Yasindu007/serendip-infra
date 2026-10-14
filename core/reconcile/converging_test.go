package reconcile

import (
	"errors"
	"testing"
)

func TestComponentConverging(t *testing.T) {
	pending := &Reachability{Reason: ReasonPending}
	tests := []struct {
		name string
		obs  ComponentObservation
		want bool
	}{
		{"progressing", ComponentObservation{Reason: ReasonProgressing}, true},
		{"waiting for application", ComponentObservation{Reason: ReasonWaitingForApplication}, true},
		{"provider not ready", ComponentObservation{Reason: ReasonProviderNotReady}, true},
		{"deployed, route pending", ComponentObservation{Ready: true, Reason: ReasonDeployed, Reachable: pending}, true},
		{"deployed, nothing exposed", ComponentObservation{Ready: true, Reason: ReasonDeployed}, false},
		{"serving", ComponentObservation{Ready: true, Reason: ReasonDeployed, Reachable: &Reachability{Reachable: true, Reason: ReasonServing}}, false},
		{"route rejected", ComponentObservation{Ready: true, Reason: ReasonDeployed, Reachable: &Reachability{Reason: ReasonRouteRejected}}, false},
		{"deploy failed", ComponentObservation{Reason: ReasonDeployFailed, Reachable: pending}, false},
		{"invalid spec", ComponentObservation{Reason: ReasonInvalidSpec, Reachable: pending}, false},
		{"unsupported", ComponentObservation{Reason: ReasonUnsupported}, false},
		{"ensure failed", ComponentObservation{Reason: ReasonEnsureFailed}, false},
	}
	for _, tt := range tests {
		if got := componentConverging(tt.obs); got != tt.want {
			t.Errorf("%s: componentConverging = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestRequeueWhen(t *testing.T) {
	writeErr := errors.New("store conflict")
	var rq RequeueAfter
	if err := requeueWhen(writeErr, true); !errors.Is(err, writeErr) || errors.As(err, &rq) {
		t.Errorf("failed status write = %v, want the write error (not a requeue)", err)
	}
	if err := requeueWhen(nil, true); !errors.As(err, &rq) {
		t.Errorf("converging = %v, want RequeueAfter", err)
	}
	if err := requeueWhen(nil, false); err != nil {
		t.Errorf("settled = %v, want nil", err)
	}
}
