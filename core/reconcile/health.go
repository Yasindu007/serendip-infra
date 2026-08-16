package reconcile

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// ErrNotConfigured is returned by HealthChecker.Check when the given
// contract's Kind is empty or unrecognized -- callers must treat this as
// "no health check configured yet" (most Provider descriptors don't have
// one, per resource.ProviderSpec.HealthCheck's own doc comment), not as
// "unhealthy."
var ErrNotConfigured = errors.New("reconcile: no health check configured")

// HealthChecker interprets a resource.HealthCheckContract and reports
// whether the target is reachable. Check returns nil if healthy, a
// non-nil error otherwise (ErrNotConfigured for an unrecognized Kind, any
// other error's message is suitable for a Condition.Message).
type HealthChecker interface {
	Check(ctx context.Context, contract resource.HealthCheckContract) error
}

// TCPHealthChecker implements the "tcp" HealthCheckContract.Kind: dials
// Config["address"] (e.g. "127.0.0.1:6443"). Stdlib-only and
// provider-agnostic -- reusable unchanged by any future real provider's
// health check (a Kubernetes-style apiserver, a secrets-store API, ...),
// not specific to one.
type TCPHealthChecker struct{}

func (TCPHealthChecker) Check(ctx context.Context, contract resource.HealthCheckContract) error {
	if contract.Kind != "tcp" {
		return ErrNotConfigured
	}
	address := contract.Config["address"]
	if address == "" {
		return fmt.Errorf(`reconcile: "tcp" health check missing "address" in Config`)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("reconcile: tcp dial %s: %w", address, err)
	}
	return conn.Close()
}

var _ HealthChecker = TCPHealthChecker{}
