package bootstrap

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// WaitForReady polls `systemctl is-active k3s` then `k3s kubectl get
// nodes` for a Ready condition until timeout elapses. Tolerates early
// "not up yet" failures (the API server may not exist immediately after
// the install script exits -- a known timing gotcha, not a bug) rather
// than treating them as fatal before the timeout is actually reached.
func WaitForReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	const pollInterval = 2 * time.Second

	for {
		if isSystemdActive(ctx, "k3s") && isNodeReady(ctx) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("bootstrap: k3s did not become ready within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func isSystemdActive(ctx context.Context, unit string) bool {
	out, err := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "active"
}

func isNodeReady(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "k3s", "kubectl", "get", "nodes",
		"-o", "jsonpath={.items[*].status.conditions[?(@.type=='Ready')].status}").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "True")
}
