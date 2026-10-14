package openchoreo

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// newTestKubeClient mirrors providers/secrets/openbao/deploy_test.go's
// own helper of the same name exactly (different package, can't reuse
// it directly).
func newTestKubeClient(t *testing.T, handler http.HandlerFunc) *kubernetes.Client {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	client, err := kubernetes.NewClient(kubernetes.Config{Host: srv.URL, CAData: caPEM})
	if err != nil {
		t.Fatalf("NewClient() = %v", err)
	}
	return client
}

func TestEnsureDataPlane_AppliesAgentTrustAndGatewayInOneObject(t *testing.T) {
	api := newFakeAPI()
	client := newTestKubeClient(t, api.handler(t))

	if err := EnsureDataPlane(context.Background(), client, testGateway); err != nil {
		t.Fatalf("EnsureDataPlane() = %v", err)
	}
	if len(api.applies) != 1 {
		t.Fatalf("applies = %d (%v), want 1", len(api.applies), api.calls)
	}
	a := api.applies[0]
	if a.path != dataPlanePath() || a.fieldManager != kubernetes.FieldManager || a.force != "true" {
		t.Errorf("apply = %s fieldManager=%q force=%q", a.path, a.fieldManager, a.force)
	}
	if a.body.APIVersion != "openchoreo.dev/v1alpha1" || a.body.Kind != "DataPlane" ||
		a.body.Metadata.Name != DefaultDataPlaneName || a.body.Metadata.Namespace != ControlPlaneNamespace {
		t.Errorf("envelope = %+v", a.body)
	}
	want := `{"clusterAgent":{"clientCA":{"secretKeyRef":{"name":"cluster-agent-tls","namespace":"openchoreo-data-plane","key":"ca.crt"}}},` +
		`"gateway":{"ingress":{"external":{"name":"serendip-external","namespace":"kgateway-system","http":{"port":30080,"host":"localhost"}}}}}`
	if string(a.body.Spec) != want {
		t.Errorf("spec = %s\nwant  %s", a.body.Spec, want)
	}

	// A repeat pass is the same request and stores nothing.
	api.writes = 0
	if err := EnsureDataPlane(context.Background(), client, testGateway); err != nil {
		t.Fatal(err)
	}
	if api.writes != 0 || string(api.applies[1].body.Spec) != want {
		t.Errorf("repeat: writes = %d, spec = %s", api.writes, api.applies[1].body.Spec)
	}
}

func TestEnsureDataPlane_GatewayChangeIsApplied(t *testing.T) {
	api := newFakeAPI()
	client := newTestKubeClient(t, api.handler(t))
	if err := EnsureDataPlane(context.Background(), client, testGateway); err != nil {
		t.Fatal(err)
	}
	moved := testGateway
	moved.Port = 31080
	api.writes = 0
	if err := EnsureDataPlane(context.Background(), client, moved); err != nil {
		t.Fatal(err)
	}
	if api.writes != 1 || !strings.Contains(string(api.objects[dataPlanePath()].Spec), `"port":31080`) {
		t.Errorf("writes = %d, spec = %s; want the new port applied", api.writes, api.objects[dataPlanePath()].Spec)
	}
}

func TestEnsureDataPlane_APIErrorPropagates(t *testing.T) {
	api := newFakeAPI()
	api.failPath = dataPlanePath()
	if err := EnsureDataPlane(context.Background(), newTestKubeClient(t, api.handler(t)), testGateway); err == nil ||
		!strings.Contains(err.Error(), "DataPlane") {
		t.Fatalf("EnsureDataPlane() = %v, want an error naming the DataPlane", err)
	}
}
