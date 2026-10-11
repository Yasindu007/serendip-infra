package resource_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestComponentExposureAndEndpoints_JSONWireShape(t *testing.T) {
	spec := resource.ComponentSpec{Image: "nginx", Ports: []resource.ComponentPort{{Name: "http", Port: 80, Expose: true}, {Name: "metrics", Port: 9113}}}
	got, _ := json.Marshal(spec)
	if want := `{"image":"nginx","ports":[{"name":"http","port":80,"expose":true},{"name":"metrics","port":9113}]}`; string(got) != want {
		t.Errorf("spec = %s, want %s", got, want)
	}

	st := resource.ComponentStatus{Endpoints: []resource.ComponentEndpointStatus{{Name: "http", URL: "http://x.localhost:30080/p"}}}
	got, _ = json.Marshal(st)
	if !strings.Contains(string(got), `"endpoints":[{"name":"http","url":"http://x.localhost:30080/p"}]`) {
		t.Errorf("status = %s, want endpoints wire shape", got)
	}

	got, _ = json.Marshal(resource.ComponentStatus{})
	if strings.Contains(string(got), "endpoints") {
		t.Errorf("empty status = %s, want endpoints omitted", got)
	}
}
