package openchoreo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// useFastRetries overrides CheckReady's retry knobs for the duration
// of a test so the OneNotReady/GetError tests below don't pay the
// real ~4s retry cost just to prove the final error propagates.
func useFastRetries(t *testing.T) {
	t.Helper()
	origRetries, origDelay := checkReadyRetries, checkReadyRetryDelay
	checkReadyRetries, checkReadyRetryDelay = 1, time.Millisecond
	t.Cleanup(func() { checkReadyRetries, checkReadyRetryDelay = origRetries, origDelay })
}

func TestCheckReady_AllReady_ReturnsNil(t *testing.T) {
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(kubernetes.Object{Status: json.RawMessage(`{"readyReplicas":1}`)})
	})

	if err := CheckReady(context.Background(), client); err != nil {
		t.Fatalf("CheckReady() = %v, want nil when every deployment is ready", err)
	}
}

func TestCheckReady_OneNotReady_NamesIt(t *testing.T) {
	useFastRetries(t)
	notReadyPath := "/apis/apps/v1/namespaces/" + ControlPlaneNamespace + "/deployments/" + expectedDeployments[2].name
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == notReadyPath {
			json.NewEncoder(w).Encode(kubernetes.Object{Status: json.RawMessage(`{"readyReplicas":0}`)})
			return
		}
		json.NewEncoder(w).Encode(kubernetes.Object{Status: json.RawMessage(`{"readyReplicas":1}`)})
	})

	err := CheckReady(context.Background(), client)
	if err == nil {
		t.Fatal("CheckReady() = nil, want an error naming the not-ready deployment")
	}
	if !strings.Contains(err.Error(), expectedDeployments[2].name) {
		t.Errorf("CheckReady() error = %q, want it to name %q", err.Error(), expectedDeployments[2].name)
	}
}

func TestCheckReady_GetError_Propagates(t *testing.T) {
	useFastRetries(t)
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("simulated server error"))
	})

	if err := CheckReady(context.Background(), client); err == nil {
		t.Fatal("CheckReady() = nil, want the simulated GET failure to propagate")
	}
}

// TestCheckReady_TransientBlipThenSuccess_Recovers is go-reviewer's
// T007 finding, directly addressed: a single transient failure (e.g.
// an apiserver blip right after Helm's own --wait already reported
// ready) must not propagate all the way up and kill serendipd --
// CheckReady's retry must absorb it.
func TestCheckReady_TransientBlipThenSuccess_Recovers(t *testing.T) {
	origRetries, origDelay := checkReadyRetries, checkReadyRetryDelay
	checkReadyRetries, checkReadyRetryDelay = 3, time.Millisecond
	t.Cleanup(func() { checkReadyRetries, checkReadyRetryDelay = origRetries, origDelay })

	var calls int
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("transient blip"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(kubernetes.Object{Status: json.RawMessage(`{"readyReplicas":1}`)})
	})

	if err := CheckReady(context.Background(), client); err != nil {
		t.Fatalf("CheckReady() = %v, want the second attempt's success to recover from the first attempt's transient blip", err)
	}
}
