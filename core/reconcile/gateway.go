package reconcile

import "context"

// GatewayEndpoint identifies the platform's shared external gateway: what
// routes attach to, and where clients reach it. Produced by a
// GatewayProvider and handed (by the composition root) to whichever
// provider renders routes, so the two capabilities never import each
// other.
type GatewayEndpoint struct {
	// Name and Namespace identify the gateway object routes attach to.
	Name      string
	Namespace string
	// Host is the base host external routes are published under.
	Host string
	// Port is the port clients connect to.
	Port int32
}

// GatewayProvider is the Gateway capability's interface: one shared
// external entry point into the cluster.
type GatewayProvider interface {
	// EnsureGateway makes the shared external gateway exist and reports
	// whether it is ready to accept routes. reason/message explain a
	// not-ready result.
	EnsureGateway(ctx context.Context) (ep GatewayEndpoint, ready bool, reason, message string, err error)
}
