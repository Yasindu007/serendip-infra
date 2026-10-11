package openchoreo

import "testing"

func TestEndpointURL_String(t *testing.T) {
	tests := []struct {
		in   endpointURL
		want string
	}{
		{endpointURL{Scheme: "http", Host: "dev-org.localhost", Port: 30080, Path: "/cmp-x-http"}, "http://dev-org.localhost:30080/cmp-x-http"},
		{endpointURL{Scheme: "http", Host: "dev-org.example.com", Port: 80, Path: "/cmp-x-http"}, "http://dev-org.example.com/cmp-x-http"},
		{endpointURL{Scheme: "https", Host: "dev-org.example.com", Port: 443, Path: "/p"}, "https://dev-org.example.com/p"},
		{endpointURL{Host: "h", Port: 8080}, "http://h:8080"},
		{endpointURL{}, ""},
	}
	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("%+v.String() = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGatewayProxyLabels_MatchesComponentNetworkPolicySelector(t *testing.T) {
	if _, ok := GatewayProxyLabels()["openchoreo.dev/system-component"]; !ok {
		t.Error("GatewayProxyLabels() lacks openchoreo.dev/system-component, which component NetworkPolicies admit gateway traffic by")
	}
}
