package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// fakeSchemas publishes serendip-service's real per-environment schema;
// err, when set, fails every read.
type fakeSchemas struct {
	env json.RawMessage
	err error
}

func newFakeSchemas(t *testing.T) *fakeSchemas {
	t.Helper()
	raw, err := os.ReadFile("../core/jsonschema/testdata/serendip-service-environment.json")
	if err != nil {
		t.Fatal(err)
	}
	return &fakeSchemas{env: raw}
}

func (f *fakeSchemas) ConfigSchemas(context.Context, resource.Component) (reconcile.ConfigSchemas, error) {
	return reconcile.ConfigSchemas{Environment: f.env}, f.err
}

func (d *deployWorld) withConfig(env string, config map[string]any, traits map[string]any) map[string]any {
	dep := d.deployment(env, "")
	overrides := map[string]any{"config": config}
	if traits != nil {
		overrides["traits"] = traits
	}
	dep["spec"].(map[string]any)["overrides"] = overrides
	return dep
}

func TestDeployments_ConfigurationFollowsTheSchema(t *testing.T) {
	schemas := newFakeSchemas(t)
	d := newDeployWorld(t, api.WithConfigSchemas(schemas))

	got := d.do("tok-devops", http.MethodPost, "/v1/deployments", d.withConfig(d.dev, map[string]any{"replicas": -1, "imagePullPolicy": "Sometimes"}, nil))
	if got.Code != http.StatusBadRequest {
		t.Fatalf("invalid configuration: %d %s", got.Code, got.Body)
	}
	var problem struct{ Detail string }
	json.Unmarshal(got.Body.Bytes(), &problem)
	for _, want := range []string{"overrides.config.replicas: must be at least 0", `overrides.config.imagePullPolicy: must be one of "Always", "IfNotPresent", "Never"`} {
		if !strings.Contains(problem.Detail, want) {
			t.Errorf("detail %q lacks %q", problem.Detail, want)
		}
	}
	if got := d.do("tok-devops", http.MethodPost, "/v1/deployments", d.withConfig(d.dev, map[string]any{"replica": 2}, nil)); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "isn't a known setting") {
		t.Errorf("a typo: %d %s", got.Code, got.Body)
	}
	if got := d.do("tok-devops", http.MethodPost, "/v1/deployments", d.withConfig(d.dev, nil, map[string]any{"autoscaler": map[string]any{"max": 3}})); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "no trait instance autoscaler") {
		t.Errorf("an unknown trait: %d %s", got.Code, got.Body)
	}

	got = d.do("tok-devops", http.MethodPost, "/v1/deployments", d.withConfig(d.dev, map[string]any{"replicas": 2, "resources": map[string]any{"limits": map[string]any{"memory": "512Mi"}}}, nil))
	if got.Code != http.StatusCreated {
		t.Fatalf("valid configuration: %d %s", got.Code, got.Body)
	}
	dev := decodeDeployment(t, got.Body.Bytes())
	d.running(t, dev.Metadata.ID, "cmp-web-r1")

	// The platform going away doesn't block changes that leave the
	// configuration as it is: promoting, or changing the release.
	schemas.err = reconcile.ErrProviderNotReady
	if got := d.do("tok-devops", http.MethodPost, "/v1/deployments/"+dev.Metadata.ID+"/promote", map[string]any{"to": d.staging}); got.Code != http.StatusCreated {
		t.Errorf("promote while the platform is away: %d %s", got.Code, got.Body)
	}
	dev = decodeDeployment(t, d.do("tok-devops", http.MethodGet, "/v1/deployments/"+dev.Metadata.ID, nil).Body.Bytes())
	dev.Spec.Release = "cmp-web-r0"
	if got := d.do("tok-devops", http.MethodPut, "/v1/deployments/"+dev.Metadata.ID, dev); got.Code != http.StatusOK {
		t.Errorf("a release change while the platform is away: %d %s", got.Code, got.Body)
	}
	// Changing the configuration can't be checked, so it waits.
	dev = decodeDeployment(t, d.do("tok-devops", http.MethodGet, "/v1/deployments/"+dev.Metadata.ID, nil).Body.Bytes())
	dev.Spec.Overrides.Config["replicas"] = 3
	if got := d.do("tok-devops", http.MethodPut, "/v1/deployments/"+dev.Metadata.ID, dev); got.Code != http.StatusServiceUnavailable {
		t.Errorf("a config change while the platform is away: %d %s", got.Code, got.Body)
	}
	// A platform failure is a 502 that names nothing internal.
	schemas.err = errors.New("dial tcp 10.43.0.1:443: internal detail")
	if got := d.do("tok-devops", http.MethodPut, "/v1/deployments/"+dev.Metadata.ID, dev); got.Code != http.StatusBadGateway || strings.Contains(got.Body.String(), "10.43") {
		t.Errorf("a platform failure: %d %s", got.Code, got.Body)
	}
}

func TestDeployments_ConfigurationWithoutAPlatform(t *testing.T) {
	d := newDeployWorld(t)
	if got := d.do("tok-devops", http.MethodPost, "/v1/deployments", d.withConfig(d.dev, map[string]any{"replicas": 2}, nil)); got.Code != http.StatusServiceUnavailable {
		t.Errorf("configuration with no platform: %d %s", got.Code, got.Body)
	}
}

func TestComponentSchemas(t *testing.T) {
	schemas := newFakeSchemas(t)
	d := newDeployWorld(t, api.WithConfigSchemas(schemas))
	got := d.do("tok-reader", http.MethodGet, "/v1/components/"+d.web+"/schemas", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"environment":{`) || !strings.Contains(got.Body.String(), "imagePullPolicy") {
		t.Errorf("schemas: %d %s", got.Code, got.Body)
	}
	if got := d.do("tok-envonly", http.MethodGet, "/v1/components/"+d.web+"/schemas", nil); got.Code != http.StatusForbidden {
		t.Errorf("without reading the Component: %d", got.Code)
	}
	schemas.err = reconcile.ErrProviderNotReady
	if got := d.do("tok-reader", http.MethodGet, "/v1/components/"+d.web+"/schemas", nil); got.Code != http.StatusServiceUnavailable {
		t.Errorf("platform not ready: %d", got.Code)
	}
	bare := newDeployWorld(t)
	if got := bare.do("tok-reader", http.MethodGet, "/v1/components/"+bare.web+"/schemas", nil); got.Code != http.StatusServiceUnavailable {
		t.Errorf("no platform: %d", got.Code)
	}
}
