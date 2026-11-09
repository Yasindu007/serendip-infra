package openchoreo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/internal/helm"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// The workflow-plane chart and where things live (VERSIONS.md).
const (
	workflowPlaneChart   = "oci://ghcr.io/openchoreo/helm-charts/openchoreo-workflow-plane"
	workflowPlaneVersion = "1.3.1"
	workflowPlaneRelease = "openchoreo-workflow-plane"

	// PlaneNamespace holds the chart's Argo controller and cluster agent.
	PlaneNamespace = "openchoreo-workflow-plane"
	// BuildsNamespace holds the registry and every WorkflowRun. Runs are
	// standalone, with no OpenChoreo component or project labels (T001
	// item 3).
	BuildsNamespace = "serendip-builds"
	// RunNamespace is where OpenChoreo runs a BuildsNamespace run's Argo
	// Workflow and pods, and where its <run>-git Secret goes. OpenChoreo
	// creates it on the first run; the build reconciler ensures it before.
	RunNamespace = "workflows-" + BuildsNamespace
	// WorkflowPlaneName is the one ClusterWorkflowPlane, which the chart's
	// default planeID matches.
	WorkflowPlaneName = "default"
	// BuildLabel names the Serendip Build on a run and its credential
	// copy, so leftovers can be found.
	BuildLabel = "serendip.dev/build"

	controlPlaneNamespace = "openchoreo-control-plane"
	apiVersion            = "openchoreo.dev/v1alpha1"

	// The chart's own names (its values.yaml and the Argo subchart's
	// helpers, with fullnameOverride "argo").
	agentDeployment      = "cluster-agent-workflowplane"
	argoDeployment       = "argo-workflow-controller"
	agentTLSSecretName   = "cluster-agent-tls"
	clusterGatewayCAName = "cluster-gateway-ca"
	clusterGatewayCAKey  = "ca.crt"
	helmInstallTimeout   = "10m"
	helmUninstallTimeout = "5m"
)

// agentSettle is how long after an install a check that may only be
// settling is waited out rather than repaired: the agent's connection
// only reads true at the controller's next poll, about 60 s after it
// connects (T001 item 1), and the registry pulls its image. Each repair
// that didn't fix it doubles the wait, up to maxSettle, so a lasting
// fault doesn't run Helm every few minutes.
const (
	agentSettle = 3 * time.Minute
	maxSettle   = 30 * time.Minute
)

// readyMessage is shown once everything checks out.
const readyMessage = "builds are ready; images are stored in the package's registry on this node"

// managedBy labels the objects Serendip hand-builds here.
var managedBy = map[string]string{"app.kubernetes.io/managed-by": "serendip"}

// ErrRunsRemaining is Uninstall's answer while build runs are still being
// removed. The clean-up retries until they're gone.
var ErrRunsRemaining = errors.New("build runs are still being removed")

// Installer installs and removes the openchoreo-workflow-plane package
// (plan decision 2). It's a reconcile.PackageInstaller. There's one
// package instance per capability (the store's unique key, RP-033), so
// its settling state isn't kept per instance.
type Installer struct {
	run    helm.Runner
	client *kubernetes.Client
	now    func() time.Time

	mu sync.Mutex
	// lastInstall is when this process last applied the package, and
	// repairs how many settling faults in a row it has repaired since
	// the checks last passed. Neither is persisted: after a restart a
	// failing check repairs straight away.
	lastInstall time.Time
	repairs     int
}

var _ reconcile.PackageInstaller = (*Installer)(nil)

// NewInstaller builds an Installer, failing fast if `helm` isn't on
// $PATH, a host prerequisite (ADR 0010).
func NewInstaller(client *kubernetes.Client, kubeconfigPath string) (*Installer, error) {
	if err := helm.CheckAvailable(); err != nil {
		return nil, fmt.Errorf("openchoreo builds: %w", err)
	}
	return newInstaller(helm.WithKubeconfig(helm.Run, kubeconfigPath), client), nil
}

func newInstaller(run helm.Runner, client *kubernetes.Client) *Installer {
	return &Installer{run: run, client: client, now: time.Now}
}

// helmInstallArgs installs Argo's minimal CRDs (crds.full=false) instead
// of the upstream default's full ones: the full schemas are 4.66 MiB, and
// held kube-apiserver ~550 MiB higher at idle on the simulated T470
// (T013, RP-046). Serendip writes every Workflow itself, so the
// server-side validation they add buys little. The minimal set ships in
// the chart, so no install downloads from GitHub, and Helm owns it.
// crds.keep=true keeps RP-039's decision: the CRDs outlive removal.
func helmInstallArgs() []string {
	return []string{
		"upgrade", "--install", workflowPlaneRelease, workflowPlaneChart,
		"--version", workflowPlaneVersion,
		"--namespace", PlaneNamespace, "--create-namespace",
		"--set", "argo-workflows.crds.full=false",
		"--set", "argo-workflows.crds.keep=true",
		"--wait", "--timeout", helmInstallTimeout,
	}
}

// HelmInstallArgs is the Helm command a full install runs. The footprint
// benchmark runs it once by itself, to compare a full Helm resync with
// the fingerprint path (specs/022-builds T013).
func HelmInstallArgs() []string { return helmInstallArgs() }

const (
	clusterWorkflowPlanesPath = "/apis/openchoreo.dev/v1alpha1/clusterworkflowplanes"
	workflowRunsPath          = "/apis/openchoreo.dev/v1alpha1/namespaces/" + BuildsNamespace + "/workflowruns"
)

// clusterWorkflowPlane registers the plane, trusting the chart's cluster
// agent certificate, like the data plane's DataPlane.
func clusterWorkflowPlane() kubernetes.Object {
	return kubernetes.Object{
		APIVersion: apiVersion, Kind: "ClusterWorkflowPlane",
		Metadata: kubernetes.ObjectMeta{Name: WorkflowPlaneName, Labels: managedBy},
		Spec: mustJSON(map[string]any{
			"planeID": WorkflowPlaneName,
			"clusterAgent": map[string]any{"clientCA": map[string]any{"secretKeyRef": map[string]string{
				"name": agentTLSSecretName, "namespace": PlaneNamespace, "key": clusterGatewayCAKey,
			}}},
		}),
	}
}

// Fingerprint identifies what a full install applies: the chart, Helm's
// arguments, and every hand-built object. When an instance's
// status.applied equals it, Install only checks (plan decision 8): a
// no-change Helm run took 19–24 s in T001. The copied CA isn't in it: a
// rotated CA disconnects the agent, and that check repairs it.
func Fingerprint() string { return fingerprint() }

var fingerprint = sync.OnceValue(func() string {
	objs := append([]kubernetes.Object{clusterWorkflowPlane()}, registryObjects()...)
	objs = append(objs, clusterWorkflow())
	b := mustJSON(struct {
		Helm    []string            `json:"helm"`
		Objects []kubernetes.Object `json:"objects"`
	}{helmInstallArgs(), objs})
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
})

// problem is what a quick check found wrong.
type problem struct {
	// message says it in words for people.
	message string
	// settling: it may only be still coming up after an install (a
	// rollout, the agent's connection), not broken.
	settling bool
	// chart: the chart's own objects are at fault, so a repair runs Helm.
	// Otherwise a repair only re-applies the hand-built objects.
	chart bool
}

// Install brings the package towards installed. With a matching
// fingerprint and passing checks it runs no Helm. A settling check soon
// after an install is waited out; anything else is repaired, with Helm
// only when the chart's own objects are at fault.
func (i *Installer) Install(ctx context.Context, inst resource.ProviderInstance) (reconcile.PackageState, error) {
	want := fingerprint()
	if inst.Status.Applied != want {
		return i.install(ctx, want, true)
	}
	p, err := i.check(ctx)
	if err != nil {
		return reconcile.PackageState{}, err
	}
	if p == nil {
		i.mu.Lock()
		i.repairs = 0
		i.mu.Unlock()
		return reconcile.PackageState{Ready: true, Message: readyMessage, Applied: want}, nil
	}
	if p.settling && i.waitOut() {
		return reconcile.PackageState{Message: p.message, Applied: want}, nil
	}
	return i.install(ctx, want, p.chart)
}

// waitOut reports whether a settling fault is still inside its settle
// window, counting a repair when it isn't.
func (i *Installer) waitOut() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.lastInstall.IsZero() {
		return false
	}
	if i.now().Sub(i.lastInstall) < min(agentSettle<<i.repairs, maxSettle) {
		return true
	}
	// Stop counting at the cap, so the shift can't overflow.
	if agentSettle<<i.repairs < maxSettle {
		i.repairs++
	}
	return false
}

// install applies the package in plan decision 2's order: the CA trust
// copy and the chart (when withHelm), the ClusterWorkflowPlane, the
// registry, then the workflow. The registry's host port is checked
// first, so a taken port costs no Helm run.
func (i *Installer) install(ctx context.Context, want string, withHelm bool) (reconcile.PackageState, error) {
	holder, err := registryPortHolder(ctx, i.client)
	if err != nil {
		return reconcile.PackageState{}, fmt.Errorf("openchoreo builds: %w", err)
	}
	if holder != "" {
		return reconcile.PackageState{Message: fmt.Sprintf(
			"the build registry needs port %d on the node, and pod %s is using it; free the port to finish installing", registryPort, holder)}, nil
	}
	if err := ensureNamespace(ctx, i.client, PlaneNamespace); err != nil {
		return reconcile.PackageState{}, fmt.Errorf("openchoreo builds: %w", err)
	}
	if err := copyClusterGatewayCA(ctx, i.client); err != nil {
		return reconcile.PackageState{}, err
	}
	if withHelm {
		if err := i.helmInstall(ctx); err != nil {
			return reconcile.PackageState{}, fmt.Errorf("openchoreo builds: install the workflow plane: %w", err)
		}
	}
	if _, err := apply(ctx, i.client, clusterWorkflowPlanesPath, clusterWorkflowPlane()); err != nil {
		return reconcile.PackageState{}, fmt.Errorf("openchoreo builds: %w", err)
	}
	if err := ensureNamespace(ctx, i.client, BuildsNamespace); err != nil {
		return reconcile.PackageState{}, fmt.Errorf("openchoreo builds: %w", err)
	}
	if err := ensureRegistry(ctx, i.client); err != nil {
		return reconcile.PackageState{}, fmt.Errorf("openchoreo builds: %w", err)
	}
	if _, err := apply(ctx, i.client, clusterWorkflowsPath, clusterWorkflow()); err != nil {
		return reconcile.PackageState{}, fmt.Errorf("openchoreo builds: %w", err)
	}
	i.mu.Lock()
	i.lastInstall = i.now()
	i.mu.Unlock()

	p, err := i.check(ctx)
	if err != nil {
		return reconcile.PackageState{}, err
	}
	if p != nil {
		return reconcile.PackageState{Message: p.message, Applied: want}, nil
	}
	i.mu.Lock()
	i.repairs = 0
	i.mu.Unlock()
	return reconcile.PackageState{Ready: true, Message: readyMessage, Applied: want}, nil
}

// helmBusy is Helm's answer while a release is locked by an operation
// that hasn't finished.
const helmBusy = "another operation (install/upgrade/rollback) is in progress"

// helmInstall runs the chart's install. A release left locked mid-way
// (a process killed while Helm ran: specs/022-builds T013 found one) is
// recovered first, then installed again. Serendip runs one Helm job per
// package at a time (core's package jobs), so a lock it finds isn't its
// own live one; a person's own helm run at that moment would be undone
// (RP-045).
func (i *Installer) helmInstall(ctx context.Context) error {
	out, err := i.run(ctx, helmInstallArgs()...)
	if err == nil || !strings.Contains(string(out)+err.Error(), helmBusy) {
		return err
	}
	if rerr := i.recoverPending(ctx); rerr != nil {
		return fmt.Errorf("%w; recovering the release left mid-operation: %v", err, rerr)
	}
	_, err = i.run(ctx, helmInstallArgs()...)
	return err
}

// recoverPending unlocks a release an unfinished operation left pending:
// a first install that never finished is uninstalled (there's nothing to
// keep), and an upgrade or rollback is rolled back to the last release.
func (i *Installer) recoverPending(ctx context.Context) error {
	out, err := i.run(ctx, "status", workflowPlaneRelease, "--namespace", PlaneNamespace, "-o", "json")
	if err != nil {
		return err
	}
	var st struct {
		Info struct {
			Status string `json:"status"`
		} `json:"info"`
	}
	if start := strings.IndexByte(string(out), '{'); start < 0 || json.Unmarshal(out[start:], &st) != nil {
		return fmt.Errorf("read the release's status: %.200s", out)
	}
	log.Printf("openchoreo builds: release %s was left %s; recovering it", workflowPlaneRelease, st.Info.Status)
	switch st.Info.Status {
	case "pending-install":
		_, err = i.run(ctx, "uninstall", workflowPlaneRelease, "--namespace", PlaneNamespace, "--wait", "--timeout", helmUninstallTimeout)
	case "pending-upgrade", "pending-rollback":
		_, err = i.run(ctx, "rollback", workflowPlaneRelease, "--namespace", PlaneNamespace, "--wait", "--timeout", helmInstallTimeout)
	default:
		err = fmt.Errorf("the release is %q, not pending", st.Info.Status)
	}
	return err
}

// check runs the quick checks (plan decision 8), about 5 GETs: the
// plane's two Deployments and the registry are ready, the agent is
// connected, and the workflow exists. It returns nil when all is well;
// err is for a failed request.
func (i *Installer) check(ctx context.Context) (*problem, error) {
	for _, d := range []struct {
		namespace, name, what string
		chart                 bool
	}{
		{PlaneNamespace, argoDeployment, "the workflow controller", true},
		{PlaneNamespace, agentDeployment, "the workflow plane's agent", true},
		{BuildsNamespace, registryName, "the build registry", false},
	} {
		obj, err := i.client.Get(ctx, deploymentsPath(d.namespace)+"/"+d.name)
		if errors.Is(err, kubernetes.ErrNotFound) {
			return &problem{message: d.what + " is missing", chart: d.chart}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("openchoreo builds: get deployment %s/%s: %w", d.namespace, d.name, err)
		}
		var status struct {
			ReadyReplicas int `json:"readyReplicas"`
		}
		if len(obj.Status) > 0 {
			if err := json.Unmarshal(obj.Status, &status); err != nil {
				return nil, fmt.Errorf("openchoreo builds: decode deployment %s/%s status: %w", d.namespace, d.name, err)
			}
		}
		if status.ReadyReplicas < 1 {
			return &problem{message: "waiting for " + d.what + " to be ready", settling: true, chart: d.chart}, nil
		}
	}

	plane, err := i.client.Get(ctx, clusterWorkflowPlanesPath+"/"+WorkflowPlaneName)
	if errors.Is(err, kubernetes.ErrNotFound) {
		return &problem{message: "the workflow plane isn't registered"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("openchoreo builds: get ClusterWorkflowPlane: %w", err)
	}
	var planeStatus struct {
		AgentConnection struct {
			Connected bool `json:"connected"`
		} `json:"agentConnection"`
	}
	if len(plane.Status) > 0 {
		if err := json.Unmarshal(plane.Status, &planeStatus); err != nil {
			return nil, fmt.Errorf("openchoreo builds: decode ClusterWorkflowPlane status: %w", err)
		}
	}
	if !planeStatus.AgentConnection.Connected {
		// Settled and still not connected, the agent's trust is the
		// likeliest fault, so the repair re-copies the CA and runs Helm.
		return &problem{message: "waiting for the workflow plane's agent to connect", settling: true, chart: true}, nil
	}

	if _, err := i.client.Get(ctx, clusterWorkflowsPath+"/"+WorkflowName); errors.Is(err, kubernetes.ErrNotFound) {
		return &problem{message: "the build workflow is missing"}, nil
	} else if err != nil {
		return nil, fmt.Errorf("openchoreo builds: get ClusterWorkflow: %w", err)
	}
	return nil, nil
}

// Uninstall removes the package in plan decision 12's order: the
// workflow, any runs, the ClusterWorkflowPlane, the chart, then the
// registry with its PVC, and last the credential copies and the CA copy.
// Every step treats "already gone" as done, so it's idempotent, and safe
// for an instance that never installed anything. The namespaces stay,
// empty, as Helm's --create-namespace leaves them, and so do the Argo
// CRDs (RP-039).
func (i *Installer) Uninstall(ctx context.Context, instanceID string) error {
	if err := deleteIfExists(ctx, i.client, clusterWorkflowsPath+"/"+WorkflowName); err != nil {
		return fmt.Errorf("openchoreo builds: %w", err)
	}
	// A run's finalizer removes its Workflow and pods through the plane,
	// so runs must be gone before the plane is.
	left, err := i.removeRuns(ctx)
	if err != nil {
		return fmt.Errorf("openchoreo builds: %w", err)
	}
	if left > 0 {
		return fmt.Errorf("openchoreo builds: %w (%d left)", ErrRunsRemaining, left)
	}
	if err := deleteIfExists(ctx, i.client, clusterWorkflowPlanesPath+"/"+WorkflowPlaneName); err != nil {
		return fmt.Errorf("openchoreo builds: %w", err)
	}
	if _, err := i.run(ctx,
		"uninstall", workflowPlaneRelease, "--namespace", PlaneNamespace,
		"--ignore-not-found", "--wait", "--timeout", helmUninstallTimeout,
	); err != nil {
		return fmt.Errorf("openchoreo builds: uninstall the workflow plane: %w", err)
	}
	if err := removeRegistry(ctx, i.client); err != nil {
		return fmt.Errorf("openchoreo builds: %w", err)
	}
	creds := "/api/v1/namespaces/" + RunNamespace + "/secrets?" + url.Values{"labelSelector": {BuildLabel}}.Encode()
	if err := deleteIfExists(ctx, i.client, creds); err != nil {
		return fmt.Errorf("openchoreo builds: delete credential copies: %w", err)
	}
	if err := deleteIfExists(ctx, i.client, fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", PlaneNamespace, clusterGatewayCAName)); err != nil {
		return fmt.Errorf("openchoreo builds: %w", err)
	}
	return nil
}

// removeRuns deletes every run in BuildsNamespace and returns how many
// are still there afterwards, held by their finalizers. A run stuck on
// its finalizer holds the removal until it's cleared.
func (i *Installer) removeRuns(ctx context.Context) (int, error) {
	runs, err := i.listRuns(ctx)
	if err != nil || len(runs) == 0 {
		return 0, err
	}
	for _, run := range runs {
		if err := deleteIfExists(ctx, i.client, workflowRunsPath+"/"+run.Metadata.Name); err != nil {
			return 0, err
		}
	}
	runs, err = i.listRuns(ctx)
	return len(runs), err
}

// listRuns lists the runs in BuildsNamespace. No run type at all (the
// platform isn't installed) is none.
func (i *Installer) listRuns(ctx context.Context) ([]kubernetes.Object, error) {
	raw, err := i.client.GetRaw(ctx, workflowRunsPath)
	if errors.Is(err, kubernetes.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list build runs: %w", err)
	}
	var list struct {
		Items []kubernetes.Object `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("decode build runs: %w", err)
	}
	return list.Items, nil
}

// copyClusterGatewayCA copies the control plane's real CA into a
// cluster-gateway-ca ConfigMap in PlaneNamespace, which the chart's agent
// mounts to trust the control plane (T001 item 1). It's read from the
// cert-manager-issued Secret, not the control plane's own placeholder
// ConfigMap: the same gap, and the same fix, as the data plane's
// ensureDataPlaneTrustsControlPlane. Without the control plane, it
// reports reconcile.ErrProviderNotReady.
func copyClusterGatewayCA(ctx context.Context, client *kubernetes.Client) error {
	src, err := client.Get(ctx, fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", controlPlaneNamespace, clusterGatewayCAName))
	if errors.Is(err, kubernetes.ErrNotFound) {
		return fmt.Errorf("openchoreo builds: the control plane's CA isn't there yet: %w", reconcile.ErrProviderNotReady)
	}
	if err != nil {
		return fmt.Errorf("openchoreo builds: get %s/%s secret: %w", controlPlaneNamespace, clusterGatewayCAName, err)
	}
	var data map[string][]byte
	if err := json.Unmarshal(src.Data, &data); err != nil {
		return fmt.Errorf("openchoreo builds: decode %s/%s secret: %w", controlPlaneNamespace, clusterGatewayCAName, err)
	}
	ca, ok := data[clusterGatewayCAKey]
	if !ok {
		return fmt.Errorf("openchoreo builds: %s/%s secret has no %s", controlPlaneNamespace, clusterGatewayCAName, clusterGatewayCAKey)
	}
	_, err = apply(ctx, client, fmt.Sprintf("/api/v1/namespaces/%s/configmaps", PlaneNamespace), kubernetes.Object{
		APIVersion: "v1", Kind: "ConfigMap",
		Metadata: kubernetes.ObjectMeta{Name: clusterGatewayCAName, Namespace: PlaneNamespace},
		Data:     mustJSON(map[string]string{clusterGatewayCAKey: string(ca)}),
	})
	if err != nil {
		return fmt.Errorf("openchoreo builds: %w", err)
	}
	return nil
}

// ensureNamespace server-side-applies namespace with no fields beyond its
// name, so it never contends with Helm's --create-namespace.
func ensureNamespace(ctx context.Context, client *kubernetes.Client, namespace string) error {
	_, err := apply(ctx, client, "/api/v1/namespaces", kubernetes.Object{
		APIVersion: "v1", Kind: "Namespace",
		Metadata: kubernetes.ObjectMeta{Name: namespace},
	})
	return err
}

func apply(ctx context.Context, client *kubernetes.Client, collection string, obj kubernetes.Object) (kubernetes.Object, error) {
	applied, err := client.Apply(ctx, collection+"/"+obj.Metadata.Name, obj, kubernetes.FieldManager)
	if err != nil {
		return kubernetes.Object{}, fmt.Errorf("apply %s %s: %w", obj.Kind, obj.Metadata.Name, err)
	}
	return applied, nil
}

// deleteIfExists deletes path, treating "already gone" as done.
func deleteIfExists(ctx context.Context, client *kubernetes.Client, path string) error {
	if err := client.Delete(ctx, path); err != nil && !errors.Is(err, kubernetes.ErrNotFound) {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	return nil
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("openchoreo builds: marshal static object: %v", err))
	}
	return b
}
