package kgateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
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

// A proxy label no longer configured is simply absent from the next apply;
// the API server removes it (apply semantics), so no explicit null is sent.
func TestEnsureGateway_DroppedProxyLabelIsAbsentFromApply(t *testing.T) {
	api := newFakeAPI()
	api.onGet = markProgrammed
	if _, _, _, _, err := testProvider(t, api).EnsureGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := NewProvider(newTestClient(t, api), "localhost", 30080, nil)
	p.pollInterval, p.timeout = time.Millisecond, 50*time.Millisecond
	if _, _, _, _, err := p.EnsureGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	applies := api.applies[gatewaysPath+"/"+GatewayName]
	last := string(applies[len(applies)-1].Spec)
	if strings.Contains(last, "labels") || strings.Contains(last, "example.dev/proxy") {
		t.Errorf("gateway apply = %s, want no labels at all", last)
	}
}
