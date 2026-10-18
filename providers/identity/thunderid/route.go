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

// routeExact are the exact paths routed to ThunderID: what a browser login
// and logout call (specs/018-agent-manager-poc plan.md, T001 finding 1),
// plus the standard OIDC endpoints a client uses. Everything else under
// /oauth2 -- introspect, dynamic client registration, PAR, CIBA -- is not
// routed, and nor are the admin console, the management APIs, MCP and
// health endpoints.
var routeExact = []string{
	"/oauth2/authorize", "/oauth2/token", "/oauth2/jwks", "/oauth2/userinfo", "/oauth2/revoke",
	"/oauth2/auth/callback", "/oauth2/logout", "/oauth2/logout/callback",
	"/flow/execute", "/flow/meta", "/design/resolve",
}

// routePrefixes are routed whole: discovery documents, and the sign-in
// page with its assets.
var routePrefixes = []string{"/.well-known", "/gate"}

const routePath = "/apis/gateway.networking.k8s.io/v1/namespaces/" + Namespace + "/httproutes/" + RouteName

// EnsureRoute server-side-applies the HTTPRoute that attaches ThunderID's
// login endpoints to the shared gateway ep under issuer's hostname. A
// no-drift pass changes nothing; an older route's rules are replaced, since
// the apply owns the whole list.
func EnsureRoute(ctx context.Context, client *kubernetes.Client, ep reconcile.GatewayEndpoint, issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil {
		return fmt.Errorf("thunderid: route: issuer: %w", err)
	}
	matches := make([]any, 0, len(routeExact)+len(routePrefixes))
	for _, p := range routeExact {
		matches = append(matches, map[string]any{"path": map[string]string{"type": "Exact", "value": p}})
	}
	for _, p := range routePrefixes {
		matches = append(matches, map[string]any{"path": map[string]string{"type": "PathPrefix", "value": p}})
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
