package openchoreo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/serendip-infra/serendip-infra/providers/internal/helm"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// Chart OCI references and pinned versions -- confirmed real,
// currently-pullable versions via `helm pull` during planning
// (specs/010-openchoreo-deployment-foundation/plan.md resolution 1),
// not assumed from documentation. certManagerVersion is pinned
// separately from the OpenChoreo charts since it's an independent
// upstream project.
const (
	certManagerChart   = "oci://quay.io/jetstack/charts/cert-manager"
	certManagerVersion = "1.21.2"

	controlPlaneChart = "oci://ghcr.io/openchoreo/helm-charts/openchoreo-control-plane"
	dataPlaneChart    = "oci://ghcr.io/openchoreo/helm-charts/openchoreo-data-plane"
	openChoreoVersion = "1.3.1"

	certManagerNamespace  = "cert-manager"
	ControlPlaneNamespace = "openchoreo-control-plane"
	DataPlaneNamespace    = "openchoreo-data-plane"
)

// Deployer installs cert-manager and both OpenChoreo charts via `helm
// upgrade --install`, which is itself idempotent -- unlike specs
// 007/008's hand-built-object Deployers, there is no separate
// GET-then-Create check here; a second Ensure call against an
// already-installed release at the same version is a cheap no-op
// Helm performs internally. client is used for the one piece of this
// install Helm itself can't do: single-cluster co-location needs the
// data plane's cluster-agent to trust the control plane's
// cluster-gateway, via a ConfigMap (cluster-gateway-ca) the control-
// plane chart creates only in its own namespace -- a real gap found
// during T006 real-cluster verification, not assumed from plan.md.
type Deployer struct {
	run    helm.Runner
	client *kubernetes.Client
}

// NewDeployer builds a Deployer, failing fast if `helm` isn't on
// $PATH -- a required host prerequisite (this package's own doc
// comment), reported clearly at construction rather than discovered
// deep inside Ensure's first install call.
func NewDeployer(client *kubernetes.Client, kubeconfigPath string) (*Deployer, error) {
	if err := helm.CheckAvailable(); err != nil {
		return nil, fmt.Errorf("openchoreo: %w", err)
	}
	return &Deployer{run: helm.WithKubeconfig(helm.Run, kubeconfigPath), client: client}, nil
}

// Ensure installs, in order, cert-manager (a real prerequisite
// neither OpenChoreo chart bundles -- plan.md's own corrected
// finding, confirmed by inspecting the pulled chart templates
// directly), the OpenChoreo control-plane chart, and the OpenChoreo
// data-plane chart -- each with --wait/--timeout so Helm's own
// well-tested readiness logic blocks until every Deployment/Job it
// creates is ready/complete (plan.md resolution 3 -- no hand-rolled
// poll loop needed here, unlike providers/secrets/openbao/deploy.go's
// waitStatefulSetReady, which existed only because that spec built
// Kubernetes objects directly with no Helm release to --wait on).
//
// security.enabled=false, backstage.enabled=false, and
// gateway.enabled=false are set on the OpenChoreo charts because
// their defaults reference placeholder domains
// ("thunder.openchoreo.invalid", "*.openchoreo.invalid") that the
// charts' own validation templates reject outright -- not an
// arbitrary simplification. Each turns off a real dependency this
// foundation spec explicitly defers: Thunder-backed auth, the
// Backstage developer portal (nothing to show until Project/Component
// CRs exist), and kgateway-routed application traffic (nothing to
// route to yet). See plan.md's Open-question resolutions 2-4 for the
// full reasoning, confirmed against the charts' own validate.yaml
// logic, not inferred.
//
// The data plane chart ALSO needs gateway.tls.enabled=false
// explicitly, unlike the control-plane chart -- a real discrepancy
// found during T006 real-cluster verification, not assumed from
// plan.md: the control-plane chart's own placeholder-domain check is
// gated on "gateway.enabled AND gateway.tls.enabled" together (so
// gateway.enabled=false alone suffices there), but the data-plane
// chart's equivalent check (_helpers.tpl's validateHostnames) is
// gated on gateway.tls.enabled alone, independent of gateway.enabled
// -- so gateway.enabled=false alone still left the default
// gateway.tls.hostname ("*.openchoreoapis.invalid") tripping the
// check.
func (d *Deployer) Ensure(ctx context.Context) error {
	if _, err := d.run(ctx,
		"upgrade", "--install", "cert-manager", certManagerChart,
		"--version", certManagerVersion,
		"--namespace", certManagerNamespace, "--create-namespace",
		"--set", "crds.enabled=true",
		"--wait", "--timeout", "5m",
	); err != nil {
		return fmt.Errorf("openchoreo: install cert-manager: %w", err)
	}

	if _, err := d.run(ctx,
		"upgrade", "--install", "openchoreo-control-plane", controlPlaneChart,
		"--version", openChoreoVersion,
		"--namespace", ControlPlaneNamespace, "--create-namespace",
		"--set", "security.enabled=false",
		"--set", "backstage.enabled=false",
		"--set", "gateway.enabled=false",
		"--wait", "--timeout", "10m",
	); err != nil {
		return fmt.Errorf("openchoreo: install control plane: %w", err)
	}

	if err := d.ensureDataPlaneTrustsControlPlane(ctx); err != nil {
		return fmt.Errorf("openchoreo: %w", err)
	}

	if _, err := d.run(ctx,
		"upgrade", "--install", "openchoreo-data-plane", dataPlaneChart,
		"--version", openChoreoVersion,
		"--namespace", DataPlaneNamespace, "--create-namespace",
		"--set", "gateway.enabled=false",
		"--set", "gateway.tls.enabled=false",
		"--wait", "--timeout", "10m",
	); err != nil {
		return fmt.Errorf("openchoreo: install data plane: %w", err)
	}

	return nil
}

// clusterGatewayCAConfigMapName is the ConfigMap name both charts
// agree on by default (the data-plane chart's
// clusterAgent.tls.serverCAConfigMap default). clusterGatewayCASecretName
// is the cert-manager-issued Secret (type kubernetes.io/tls) the
// control-plane chart's cluster-gateway Certificate actually writes
// the real CA into -- confirmed directly against both pulled charts
// and a real cluster, not assumed.
const (
	clusterGatewayCAConfigMapName = "cluster-gateway-ca"
	clusterGatewayCASecretName    = "cluster-gateway-ca"
)

// ensureDataPlaneTrustsControlPlane idempotently ensures the
// DataPlaneNamespace exists and creates a cluster-gateway-ca ConfigMap
// in it containing the control plane's real CA certificate, so the
// data plane chart's cluster-agent (installed right after this
// returns) can mount its expected server-ca ConfigMap with real
// content on its very first boot.
//
// Reads the CA from the control plane's cluster-gateway-ca Secret
// (cert-manager-issued, type kubernetes.io/tls), NOT from the control
// plane's own cluster-gateway-ca ConfigMap -- a real, confirmed
// upstream gap found during T006 real-cluster verification: the
// control-plane chart's own ConfigMap
// (templates/cluster-gateway/ca-configmap-placeholder.yaml) is
// created as a permanent placeholder ("# Placeholder certificate -
// will be replaced by actual CA certificate... The CA extractor hook
// job will populate it"), but no such job exists anywhere in the
// chart (the only Job in the whole chart is the unrelated authz
// bootstrap job) -- confirmed by grepping every template for `kind:
// Job`. Nothing in controller-manager's logs shows any CA-sync
// activity either. The real CA cert-manager actually issues sits in
// the Secret the whole time; this bypasses the chart's own broken
// placeholder entirely rather than waiting on a mechanism that
// doesn't exist.
func (d *Deployer) ensureDataPlaneTrustsControlPlane(ctx context.Context) error {
	if err := ensureNamespace(ctx, d.client, DataPlaneNamespace); err != nil {
		return fmt.Errorf("ensure %s namespace: %w", DataPlaneNamespace, err)
	}

	dstPath := fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", DataPlaneNamespace, clusterGatewayCAConfigMapName)
	if _, err := d.client.Get(ctx, dstPath); err == nil {
		return nil
	} else if !errors.Is(err, kubernetes.ErrNotFound) {
		return fmt.Errorf("get %s/%s configmap: %w", DataPlaneNamespace, clusterGatewayCAConfigMapName, err)
	}

	srcPath := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", ControlPlaneNamespace, clusterGatewayCASecretName)
	src, err := d.client.Get(ctx, srcPath)
	if err != nil {
		return fmt.Errorf("get %s/%s secret: %w", ControlPlaneNamespace, clusterGatewayCASecretName, err)
	}
	var secretData map[string][]byte
	if err := json.Unmarshal(src.Data, &secretData); err != nil {
		return fmt.Errorf("decode %s/%s secret data: %w", ControlPlaneNamespace, clusterGatewayCASecretName, err)
	}
	caCert, ok := secretData["ca.crt"]
	if !ok {
		return fmt.Errorf("%s/%s secret has no ca.crt key", ControlPlaneNamespace, clusterGatewayCASecretName)
	}

	configMapData, err := json.Marshal(map[string]string{"ca.crt": string(caCert)})
	if err != nil {
		return fmt.Errorf("marshal configmap data: %w", err)
	}

	_, err = d.client.Create(ctx, fmt.Sprintf("/api/v1/namespaces/%s/configmaps", DataPlaneNamespace), kubernetes.Object{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata:   kubernetes.ObjectMeta{Name: clusterGatewayCAConfigMapName, Namespace: DataPlaneNamespace},
		Data:       configMapData,
	})
	if err != nil {
		return fmt.Errorf("create %s/%s configmap: %w", DataPlaneNamespace, clusterGatewayCAConfigMapName, err)
	}
	return nil
}

// ensureNamespace idempotently creates namespace if it doesn't exist
// -- mirrors providers/secrets/openbao/deploy.go's own ensureNamespace
// exactly (same pattern, different package).
func ensureNamespace(ctx context.Context, client *kubernetes.Client, namespace string) error {
	_, err := client.Get(ctx, "/api/v1/namespaces/"+namespace)
	if err == nil {
		return nil
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		return err
	}
	_, err = client.Create(ctx, "/api/v1/namespaces", kubernetes.Object{
		APIVersion: "v1",
		Kind:       "Namespace",
		Metadata:   kubernetes.ObjectMeta{Name: namespace},
	})
	return err
}
