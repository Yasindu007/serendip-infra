package openchoreo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// deploymentCheck names one Deployment to confirm ready. Exact
// Helm-release-name-prefixed names are confirmed against a real
// install (plan.md flags this explicitly as unverified-by-planning-
// alone) -- these are this package's current best-confirmed guesses
// from the chart's _helpers.tpl naming convention, corrected during
// T006's real-cluster verification if wrong.
type deploymentCheck struct {
	namespace string
	name      string
}

// expectedDeployments is the full set this foundation install expects
// ready, given backstage/gateway are disabled (Deployer.Ensure):
// cluster-gateway, controller-manager, event-forwarder, and
// openchoreo-api in the control plane; cluster-agent-dataplane in the
// data plane. Names confirmed directly against a real install (T006
// real-cluster verification) -- bare component names, with NO
// Helm-release-name prefix, since both charts set
// fullnameOverride/component-name conventions that don't include the
// release name. plan.md's own Risks section flagged these names as
// unverified by planning alone; this is that verification. The authz
// bootstrap Job is deliberately not checked here -- it's a Helm hook
// Job (runs once, reports Complete, not a Deployment) already covered
// by Ensure's own --wait on the control-plane release.
var expectedDeployments = []deploymentCheck{
	{namespace: ControlPlaneNamespace, name: "cluster-gateway"},
	{namespace: ControlPlaneNamespace, name: "controller-manager"},
	{namespace: ControlPlaneNamespace, name: "event-forwarder"},
	{namespace: ControlPlaneNamespace, name: "openchoreo-api"},
	{namespace: DataPlaneNamespace, name: "cluster-agent-dataplane"},
}

type deploymentStatus struct {
	ReadyReplicas int `json:"readyReplicas"`
}

// checkReadyRetries/checkReadyRetryDelay bound a short, cheap retry
// around the whole check -- go-reviewer's finding during T007: a
// single GET immediately after Helm's own --wait already reported
// every Deployment ready can still race a brief apiserver/informer-
// cache lag, and CheckReady's error propagates all the way up through
// wireOpenChoreo/wireKubernetes/run() to os.Exit(1) -- a transient
// blip would kill the whole daemon, not just this one (currently
// Reconciler-less, smoke-test-only) capability. Three attempts, 2s
// apart, is enough to absorb that class of blip without meaningfully
// slowing down the real failure case (a genuinely broken Deployment
// still fails after ~4s, not hangs).
var (
	checkReadyRetries    = 3
	checkReadyRetryDelay = 2 * time.Second
)

// CheckReady confirms every Deployment in expectedDeployments reports
// status.readyReplicas >= 1 -- a second, independent confirmation
// beyond trusting Helm's own --wait exit code (plan.md resolution 3),
// cheap to include since it's a handful of plain GETs through the
// existing providers/kubernetes.Client.
func CheckReady(ctx context.Context, client *kubernetes.Client) error {
	var err error
	for attempt := 1; attempt <= checkReadyRetries; attempt++ {
		if err = checkReadyOnce(ctx, client); err == nil {
			return nil
		}
		if attempt < checkReadyRetries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(checkReadyRetryDelay):
			}
		}
	}
	return err
}

func checkReadyOnce(ctx context.Context, client *kubernetes.Client) error {
	for _, dep := range expectedDeployments {
		path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", dep.namespace, dep.name)
		obj, err := client.Get(ctx, path)
		if err != nil {
			return fmt.Errorf("openchoreo: get deployment %s/%s: %w", dep.namespace, dep.name, err)
		}
		var status deploymentStatus
		if len(obj.Status) > 0 {
			if err := json.Unmarshal(obj.Status, &status); err != nil {
				return fmt.Errorf("openchoreo: decode deployment %s/%s status: %w", dep.namespace, dep.name, err)
			}
		}
		if status.ReadyReplicas < 1 {
			return fmt.Errorf("openchoreo: deployment %s/%s not ready (readyReplicas=%d)", dep.namespace, dep.name, status.ReadyReplicas)
		}
	}
	return nil
}
