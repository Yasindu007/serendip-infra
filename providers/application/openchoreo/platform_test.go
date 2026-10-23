package openchoreo

import (
	"context"
	"net/http"
	"testing"
)

func TestPlatform(t *testing.T) {
	lists := map[string]string{
		namespacedPath("dataplanes"): `{"items":[
			{"metadata":{"name":"default"},"spec":{},"status":{"conditions":[{"type":"Created","status":"True"}]}},
			{"metadata":{"name":"edge"},"spec":{},"status":{"conditions":[{"type":"Ready","status":"False","message":"agent not connected"}]}}]}`,
		clusterPath("clustercomponenttypes"): `{"items":[{"metadata":{"name":"serendip-service","annotations":{"openchoreo.dev/description":"A service"}},
			"spec":{"environmentConfigs":{"openAPIV3Schema":{"type":"object"}},"allowedTraits":[{"kind":"ClusterTrait","name":"autoscaler"}]}}]}`,
		namespacedPath("componenttypes"): `{"items":[]}`,
		clusterPath("clustertraits"):     `{"items":[{"metadata":{"name":"autoscaler"},"spec":{"parameters":{"openAPIV3Schema":{"type":"object","properties":{"max":{"type":"integer"}}}}}}]}`,
		namespacedPath("traits"):         `{"items":[]}`,
	}
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, ok := lists[r.URL.Path]
		if !ok { // clusterdataplanes: a kind this platform doesn't serve
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	})
	view, err := NewProvider(client, testGateway).Platform(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Runtimes) != 2 || !view.Runtimes[0].Ready || view.Runtimes[1].Ready || view.Runtimes[1].Message != "agent not connected" {
		t.Errorf("runtimes = %+v", view.Runtimes)
	}
	if len(view.ComponentTypes) != 1 {
		t.Fatalf("component types = %+v", view.ComponentTypes)
	}
	ct := view.ComponentTypes[0]
	if ct.Name != "serendip-service" || ct.Description != "A service" || string(ct.Environment) != `{"type":"object"}` || len(ct.Parameters) != 0 ||
		len(ct.AllowedTraits) != 1 || ct.AllowedTraits[0] != "autoscaler" {
		t.Errorf("component type = %+v", ct)
	}
	if len(view.Traits) != 1 || view.Traits[0].Name != "autoscaler" || len(view.Traits[0].Parameters) == 0 {
		t.Errorf("traits = %+v", view.Traits)
	}
}

func TestPlatform_FailsOnAnError(t *testing.T) {
	client := newTestKubeClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusForbidden)
	})
	if _, err := NewProvider(client, testGateway).Platform(context.Background()); err == nil {
		t.Error("a refused list was read as an empty platform")
	}
}
