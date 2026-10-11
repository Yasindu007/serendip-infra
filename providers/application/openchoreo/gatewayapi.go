package openchoreo

import (
	"context"
	"fmt"

	"github.com/serendip-infra/serendip-infra/providers/internal/gatewayapi"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// EnsureGatewayAPICRDs installs the pinned Gateway API definitions.
// OpenChoreo's RenderedRelease controller lists Gateway API kinds in the
// data plane and fails every reconcile without them, even with its own
// gateway disabled (found in spec 011's real-cluster verification). The
// gateway provider ensures the same definitions; whichever runs first does
// the work and the other is a cheap no-op.
func EnsureGatewayAPICRDs(ctx context.Context, client *kubernetes.Client) error {
	if err := gatewayapi.EnsureCRDs(ctx, client); err != nil {
		return fmt.Errorf("openchoreo: %w", err)
	}
	return nil
}
