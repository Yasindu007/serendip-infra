// Package openchoreo deploys OpenChoreo (the Application Platform
// capability's default provider, ADR 0005) onto an existing Kubernetes
// cluster by shelling out to the real `helm` CLI -- OpenChoreo's own
// first-party distribution mechanism, not a handful of objects a
// hand-rolled client can construct directly the way specs 007/008 did
// for local-path/OpenBao (specs/010-openchoreo-deployment-foundation/
// spec.md's Problem section). This is this package's only reason to
// exist outside the generic providers/kubernetes.Client: everything
// else (the DataPlane CR, readiness checks) is a plain Kubernetes API
// object this package addresses the same way every prior provider
// does.
package openchoreo

import (
	"context"
	"fmt"
	"os/exec"
)

// helmRunner abstracts one `helm` invocation -- a function value, not
// an interface, since there is exactly one real implementation and one
// fake (same-package tests substitute a Deployer field directly,
// mirroring providers/secrets/openbao.Deployer's overridable
// waitReadyPollInterval/waitReadyTimeout fields). Returns the
// command's combined stdout+stderr regardless of success, so a
// failure's error can embed real diagnostic output.
type helmRunner func(ctx context.Context, args ...string) (output []byte, err error)

// defaultHelmRunner shells out to the real `helm` binary on $PATH.
func defaultHelmRunner(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "helm", args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("helm %v: %w: %s", args, err, truncateOutput(out))
	}
	return out, nil
}

// checkHelmAvailable confirms `helm` resolves on $PATH -- checked once
// at Deployer construction time so a missing binary is reported
// clearly as a host prerequisite, not discovered as a confusing
// failure deep inside Ensure's first real install call.
func checkHelmAvailable() error {
	if _, err := exec.LookPath("helm"); err != nil {
		return fmt.Errorf("openchoreo: helm not found on PATH -- it is a required host prerequisite (shelled out to directly, not a Go dependency): %w", err)
	}
	return nil
}

// maxHelmOutputInError bounds how much of a failed helm command's
// combined output gets embedded in the returned error -- mirrors
// providers/kubernetes.Client's unexpectedStatus body-truncation
// convention, since helm's own error output can be long (a full chart
// validation failure dump).
const maxHelmOutputInError = 4096

func truncateOutput(out []byte) string {
	if len(out) > maxHelmOutputInError {
		return string(out[:maxHelmOutputInError]) + "... (truncated)"
	}
	return string(out)
}
