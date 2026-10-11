// Package helm shells out to the real `helm` CLI for providers whose
// upstream's first-party distribution mechanism is a Helm chart (ADR 0010
// for OpenChoreo, ADR 0011 for kgateway). It lives under internal/ so only
// providers can import it, and deleting any one provider leaves it intact.
package helm

import (
	"context"
	"fmt"
	"os/exec"
)

// Runner abstracts one `helm` invocation -- a function value, not an
// interface, since there is exactly one real implementation and tests
// substitute a fake on the consuming provider's Deployer. Returns the
// command's combined stdout+stderr regardless of success, so a failure's
// error can embed real diagnostic output.
type Runner func(ctx context.Context, args ...string) (output []byte, err error)

// Run shells out to the real `helm` binary on $PATH.
func Run(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "helm", args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("helm %v: %w: %s", args, err, truncateOutput(out))
	}
	return out, nil
}

// WithKubeconfig returns a Runner that targets the cluster in the given
// kubeconfig file. Without it helm uses $KUBECONFIG or ~/.kube/config,
// which can be a different cluster from the one serendipd itself talks to.
// An empty path returns run unchanged.
func WithKubeconfig(run Runner, kubeconfigPath string) Runner {
	if kubeconfigPath == "" {
		return run
	}
	return func(ctx context.Context, args ...string) ([]byte, error) {
		return run(ctx, append([]string{"--kubeconfig", kubeconfigPath}, args...)...)
	}
}

// CheckAvailable confirms `helm` resolves on $PATH -- checked once at
// Deployer construction so a missing binary is reported clearly as a host
// prerequisite, not discovered deep inside the first real install call.
func CheckAvailable() error {
	if _, err := exec.LookPath("helm"); err != nil {
		return fmt.Errorf("helm not found on PATH -- it is a required host prerequisite (shelled out to directly, not a Go dependency): %w", err)
	}
	return nil
}

// maxOutputInError bounds how much of a failed helm command's combined
// output gets embedded in the returned error -- mirrors
// providers/kubernetes.Client's body-truncation convention, since helm's
// own error output can be long (a full chart validation failure dump).
const maxOutputInError = 4096

func truncateOutput(out []byte) string {
	if len(out) > maxOutputInError {
		return string(out[:maxOutputInError]) + "... (truncated)"
	}
	return string(out)
}
