package kgateway

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEnsureGateway_StampsProxyLabelsOnInfrastructure(t *testing.T) {
	api := newFakeAPI()
	api.onGet = markProgrammed
	if _, _, _, _, err := testProvider(t, api).EnsureGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	var spec gatewaySpec
	json.Unmarshal(api.objects[gatewaysPath+"/"+GatewayName].Spec, &spec)
	if spec.Infrastructure.Labels["example.dev/proxy"] != "gateway" {
		t.Errorf("infrastructure.labels = %v, want the configured proxy labels", spec.Infrastructure.Labels)
	}
}

func TestEnsureGateway_NoProxyLabelsOmitsField(t *testing.T) {
	p := NewProvider(nil, "localhost", 30080, nil)
	raw, _ := json.Marshal(p.desiredGateway())
	var m map[string]map[string]any
	json.Unmarshal(raw, &m)
	if _, ok := m["infrastructure"]["labels"]; ok {
		t.Errorf("gateway spec = %s, want no infrastructure.labels without configured labels", raw)
	}
}
