package kgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestEnsureGateway_EmptyLiveLabelsMapDoesNotPerpetuallyPatch(t *testing.T) {
	api := newFakeAPI()
	api.onGet = markProgrammed
	p := NewProvider(newTestClient(t, api), "localhost", 30080, nil)
	p.pollInterval, p.timeout = 1, 50_000_000
	if _, _, _, _, err := p.EnsureGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Simulate the server storing an explicit empty labels map.
	gw := api.objects[gatewaysPath+"/"+GatewayName]
	var spec map[string]any
	json.Unmarshal(gw.Spec, &spec)
	spec["infrastructure"].(map[string]any)["labels"] = map[string]any{}
	gw.Spec, _ = json.Marshal(spec)
	api.objects[gatewaysPath+"/"+GatewayName] = gw
	api.calls = nil

	if _, _, _, _, err := p.EnsureGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := api.count(http.MethodPatch); n != 0 {
		t.Errorf("PATCH calls = %d (%v), want 0 for an empty labels map vs none", n, api.calls)
	}
}
