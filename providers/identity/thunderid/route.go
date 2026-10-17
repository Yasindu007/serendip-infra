package thunderid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// RouteName is the Serendip-owned HTTPRoute exposing the login endpoints.
const RouteName = "serendip-identity"

// routePaths are the only path prefixes routed to ThunderID: what a browser
// login and token exchange need (decision 6, amended by T001 finding 6).
// The admin console, management APIs, MCP and health endpoints are not
// routed.
var routePaths = []string{"/oauth2", "/.well-known", "/gate", "/flow/execute", "/flow/meta", "/design/resolve"}

const routePath = "/apis/gateway.networking.k8s.io/v1/namespaces/" + Namespace + "/httproutes/" + RouteName

// EnsureRoute server-side-applies the HTTPRoute that attaches ThunderID's
// login endpoints to the shared gateway ep under issuer's hostname. A
// no-drift pass changes nothing.
func EnsureRoute(ctx context.Context, client *kubernetes.Client, ep reconcile.GatewayEndpoint, issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil {
		return fmt.Errorf("thunderid: route: issuer: %w", err)
	}
	matches := make([]any, len(routePaths))
	for i, p := range routePaths {
		matches[i] = map[string]any{"path": map[string]string{"type": "PathPrefix", "value": p}}
	}
	spec, err := json.Marshal(map[string]any{
		"parentRefs": []any{map[string]string{"name": ep.Name, "namespace": ep.Namespace}},
		"hostnames":  []string{u.Hostname()},
		"rules": []any{map[string]any{
			"matches":     matches,
			"backendRefs": []any{map[string]any{"name": ServiceName, "port": ServicePort}},
		}},
	})
	if err != nil {
		return fmt.Errorf("thunderid: route: %w", err)
	}
	_, err = client.Apply(ctx, routePath, kubernetes.Object{
		APIVersion: "gateway.networking.k8s.io/v1",
		Kind:       "HTTPRoute",
		Metadata: kubernetes.ObjectMeta{
			Name:      RouteName,
			Namespace: Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "serendip"},
		},
		Spec: spec,
	}, kubernetes.FieldManager)
	if err != nil {
		return fmt.Errorf("thunderid: route: %w", err)
	}
	return nil
}

// Issuer is the issuer URL: override when set, otherwise
// http://identity.<gateway host>:<gateway port> (decision 5). It becomes
// ThunderID's publicUrl, which is exactly what tokens carry as "iss", so it
// never has a trailing slash.
func Issuer(ep reconcile.GatewayEndpoint, override string) (string, error) {
	if override == "" {
		return fmt.Sprintf("http://identity.%s:%d", ep.Host, ep.Port), nil
	}
	u, err := url.Parse(override)
	if err != nil {
		return "", fmt.Errorf("identity issuer %q: %w", override, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("identity issuer %q: must be an absolute http(s) URL", override)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("identity issuer: must not carry a query, fragment, or user info")
	}
	return strings.TrimSuffix(override, "/"), nil
}
