// Package kgateway implements the Gateway capability on kgateway (ADR
// 0011): one shared external Gateway, exposed on a fixed NodePort, that
// other providers attach routes to.
package kgateway

import (
	"context"
	"fmt"

	"github.com/serendip-infra/serendip-infra/providers/internal/gatewayapi"
	"github.com/serendip-infra/serendip-infra/providers/internal/helm"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// Version must stay within a range that supports gatewayapi.Version (ADR
// 0011: kgateway 2.4.x supports Gateway API 1.4-1.6). Bump both together.
const (
	Version   = "v2.4.6"
	Namespace = "kgateway-system"

	crdsChart = "oci://cr.kgateway.dev/kgateway-dev/charts/kgateway-crds"
	chart     = "oci://cr.kgateway.dev/kgateway-dev/charts/kgateway"
)

// Deployer installs the Gateway API definitions kgateway needs, then
// kgateway's own CRDs chart and controller chart.
type Deployer struct {
	run        helm.Runner
	client     *kubernetes.Client
	ensureCRDs func(ctx context.Context, client *kubernetes.Client) error
}

// NewDeployer fails fast if `helm` isn't on $PATH.
func NewDeployer(client *kubernetes.Client, kubeconfigPath string) (*Deployer, error) {
	if err := helm.CheckAvailable(); err != nil {
		return nil, fmt.Errorf("kgateway: %w", err)
	}
	return &Deployer{run: helm.WithKubeconfig(helm.Run, kubeconfigPath), client: client, ensureCRDs: gatewayapi.EnsureCRDs}, nil
}

// Ensure is idempotent: helm upgrade --install is a no-op for an
// already-installed release at the same version. Experimental Gateway API
// features are switched off because only standard-channel CRDs are
// installed (ADR 0011).
func (d *Deployer) Ensure(ctx context.Context) error {
	if err := d.ensureCRDs(ctx, d.client); err != nil {
		return fmt.Errorf("kgateway: %w", err)
	}
	if _, err := d.run(ctx,
		"upgrade", "--install", "kgateway-crds", crdsChart,
		"--version", Version,
		"--namespace", Namespace, "--create-namespace",
		"--wait", "--timeout", "5m",
	); err != nil {
		return fmt.Errorf("kgateway: install crds: %w", err)
	}
	if _, err := d.run(ctx,
		"upgrade", "--install", "kgateway", chart,
		"--version", Version,
		"--namespace", Namespace, "--create-namespace",
		"--set", "controller.extraEnv.KGW_ENABLE_EXPERIMENTAL_GATEWAY_API_FEATURES=false",
		"--wait", "--timeout", "5m",
	); err != nil {
		return fmt.Errorf("kgateway: install controller: %w", err)
	}
	return nil
}
