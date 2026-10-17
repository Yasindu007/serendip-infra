package thunderid

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// CheckReady asks ThunderID's readiness endpoint, through the Kubernetes
// API's Service proxy, whether it is serving.
func CheckReady(ctx context.Context, client *kubernetes.Client) error {
	url := client.ProxyURL(Namespace, ServiceName, ServicePort) + "/health/readiness"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("thunderid: readiness: %w", err)
	}
	resp, err := client.HTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("thunderid: readiness: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("thunderid: not ready: %s: %s", resp.Status, body)
	}
	return nil
}

// JWKSProxyURL is where serendipd fetches ThunderID's signing keys: the same
// keys the issuer's discovery document points at, reached through the
// Service proxy so it doesn't depend on the host resolving the issuer's
// hostname (decision 9).
func JWKSProxyURL(client *kubernetes.Client) string {
	return client.ProxyURL(Namespace, ServiceName, ServicePort) + "/oauth2/jwks"
}
