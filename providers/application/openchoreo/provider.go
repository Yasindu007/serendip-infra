package openchoreo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// Provider implements reconcile.ApplicationPlatformProvider on
// OpenChoreo: a Serendip Application becomes a Project plus its one
// ProjectReleaseBinding; a Component becomes a Component (autoDeploy) plus
// its Workload. OpenChoreo's own controllers create the ComponentRelease
// and ReleaseBinding, and render the real Deployment into the Project's
// data-plane cell namespace. Everything lives in ControlPlaneNamespace,
// labeled as the org namespace by EnsurePlatformDefaults.
type Provider struct {
	client *kubernetes.Client
}

func NewProvider(client *kubernetes.Client) *Provider {
	return &Provider{client: client}
}

var _ reconcile.ApplicationPlatformProvider = (*Provider)(nil)

// endpointType is used for every Component port. TCP is the honest
// generic choice: HTTP-aware types only matter for ingress routing, which
// is disabled.
const endpointType = "TCP"

type ownerRef struct {
	ProjectName   string `json:"projectName"`
	ComponentName string `json:"componentName,omitempty"`
}

type envVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type workloadEndpoint struct {
	Type string `json:"type"`
	Port int32  `json:"port"`
}

type workloadContainer struct {
	Image string   `json:"image"`
	Env   []envVar `json:"env,omitempty"`
}

// workloadSpec models only the Workload fields this provider owns.
type workloadSpec struct {
	Owner     ownerRef                    `json:"owner"`
	Container workloadContainer           `json:"container"`
	Endpoints map[string]workloadEndpoint `json:"endpoints,omitempty"`
}

type condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	ObservedGeneration int64  `json:"observedGeneration"`
}

type objectStatus struct {
	Conditions []condition `json:"conditions"`
}

func (p *Provider) EnsureApplication(ctx context.Context, app resource.Application) (bool, string, string, error) {
	project := projectName(app.Metadata.ID)
	labels := serendipLabels(app.Metadata.ID, app.Metadata.Name)

	if _, err := getOrCreate(ctx, p.client, namespacedPath("projects"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "Project",
		Metadata: kubernetes.ObjectMeta{Name: project, Namespace: ControlPlaneNamespace, Labels: labels},
		Spec: mustJSON(map[string]any{
			"deploymentPipelineRef": map[string]string{"name": deploymentPipeline},
			"type":                  map[string]string{"kind": "ClusterProjectType", "name": projectTypeName},
		}),
	}); err != nil {
		return false, "", "", err
	}

	binding, err := getOrCreate(ctx, p.client, namespacedPath("projectreleasebindings"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "ProjectReleaseBinding",
		Metadata: kubernetes.ObjectMeta{Name: projectReleaseBindingName(app.Metadata.ID), Namespace: ControlPlaneNamespace, Labels: labels},
		Spec: mustJSON(map[string]any{
			"owner":       ownerRef{ProjectName: project},
			"environment": environmentName,
		}),
	})
	if err != nil {
		return false, "", "", err
	}
	ready, reason, message := mapReady(binding)
	return ready, reason, message, nil
}

func (p *Provider) DeleteApplication(ctx context.Context, app resource.Application) error {
	// The Project's own cleanup finalizer removes its Components,
	// ProjectReleaseBindings, and data-plane cell namespaces.
	return p.deleteIgnoringNotFound(ctx, namespacedPath("projects")+"/"+projectName(app.Metadata.ID))
}

func (p *Provider) EnsureComponent(ctx context.Context, app resource.Application, comp resource.Component) (bool, string, string, error) {
	if len(comp.Spec.Ports) == 0 {
		return false, reconcile.ReasonUnsupported,
			"components without ports are not supported by this provider yet", nil
	}

	project := projectName(app.Metadata.ID)
	name := componentName(comp.Metadata.ID)
	labels := serendipLabels(comp.Metadata.ID, comp.Metadata.Name)

	existing, err := getOrCreate(ctx, p.client, namespacedPath("components"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "Component",
		Metadata: kubernetes.ObjectMeta{Name: name, Namespace: ControlPlaneNamespace, Labels: labels},
		Spec: mustJSON(map[string]any{
			"owner":         ownerRef{ProjectName: project},
			"autoDeploy":    true,
			"componentType": map[string]string{"kind": "ClusterComponentType", "name": componentTypeWorkload + "/" + componentTypeName},
		}),
	})
	if err != nil {
		return false, "", "", err
	}
	var compSpec struct {
		Owner ownerRef `json:"owner"`
	}
	if err := json.Unmarshal(existing.Spec, &compSpec); err != nil {
		return false, "", "", fmt.Errorf("decode Component %s spec: %w", name, err)
	}
	if compSpec.Owner.ProjectName != project {
		// owner is immutable upstream; moving a Component to another
		// Application would need delete-and-recreate.
		return false, reconcile.ReasonUnsupported,
			"moving a component to a different application is not supported; delete it and create a new component under the target application", nil
	}

	patched, err := p.ensureWorkload(ctx, project, name, labels, comp.Spec)
	if err != nil {
		return false, "", "", err
	}
	if patched {
		// The ReleaseBinding still describes the previous release at this
		// point; reading it now would report the old version as Deployed.
		return false, reconcile.ReasonProgressing, "rolling out updated workload", nil
	}

	binding, err := p.client.Get(ctx, namespacedPath("releasebindings")+"/"+releaseBindingName(comp.Metadata.ID))
	if errors.Is(err, kubernetes.ErrNotFound) {
		return false, reconcile.ReasonProgressing, "waiting for the platform to create the release binding", nil
	}
	if err != nil {
		return false, "", "", fmt.Errorf("get ReleaseBinding %s: %w", releaseBindingName(comp.Metadata.ID), err)
	}

	current, err := p.bindingReleasesDesired(ctx, binding, desiredWorkload(project, name, comp.Spec))
	if err != nil {
		return false, "", "", err
	}
	if !current {
		return false, reconcile.ReasonProgressing, "waiting for a release of the current spec to be bound", nil
	}
	ready, reason, message := mapReady(binding)
	return ready, reason, message, nil
}

// bindingReleasesDesired reports whether the ComponentRelease the binding
// points at was cut from the desired workload. Right after a Workload
// change the binding can still be Ready for the previous release; the
// Component's own reconcile may run again before the platform reacts, so
// "did I just patch" alone isn't enough to avoid reporting the old
// version as Deployed.
func (p *Provider) bindingReleasesDesired(ctx context.Context, binding kubernetes.Object, desired workloadSpec) (bool, error) {
	if len(binding.Spec) == 0 {
		return false, nil
	}
	var bindingSpec struct {
		ReleaseName string `json:"releaseName"`
	}
	if err := json.Unmarshal(binding.Spec, &bindingSpec); err != nil {
		return false, fmt.Errorf("decode ReleaseBinding %s spec: %w", binding.Metadata.Name, err)
	}
	if bindingSpec.ReleaseName == "" {
		return false, nil
	}
	release, err := p.client.Get(ctx, namespacedPath("componentreleases")+"/"+bindingSpec.ReleaseName)
	if errors.Is(err, kubernetes.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get ComponentRelease %s: %w", bindingSpec.ReleaseName, err)
	}
	var releaseSpec struct {
		Workload workloadSpec `json:"workload"`
	}
	if err := json.Unmarshal(release.Spec, &releaseSpec); err != nil {
		return false, fmt.Errorf("decode ComponentRelease %s spec: %w", bindingSpec.ReleaseName, err)
	}
	return workloadsEqual(releaseSpec.Workload, desired), nil
}

func (p *Provider) DeleteComponent(ctx context.Context, comp resource.Component) error {
	// The Component's own cleanup finalizer removes its Workload, releases,
	// and live data-plane resources.
	return p.deleteIgnoringNotFound(ctx, namespacedPath("components")+"/"+componentName(comp.Metadata.ID))
}

// ensureWorkload creates the Workload, or patches it only when a field this
// provider owns actually differs -- a no-drift reconcile makes no writes.
// Reports whether an existing Workload was patched.
func (p *Provider) ensureWorkload(ctx context.Context, project, name string, labels map[string]string, spec resource.ComponentSpec) (bool, error) {
	desired := desiredWorkload(project, name, spec)
	path := namespacedPath("workloads") + "/" + name

	existing, err := p.client.Get(ctx, path)
	if errors.Is(err, kubernetes.ErrNotFound) {
		_, err = p.client.Create(ctx, namespacedPath("workloads"), kubernetes.Object{
			APIVersion: apiVersion, Kind: "Workload",
			Metadata: kubernetes.ObjectMeta{Name: name, Namespace: ControlPlaneNamespace, Labels: labels},
			Spec:     mustJSON(desired),
		})
		if err != nil {
			return false, fmt.Errorf("create Workload %s: %w", name, err)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get Workload %s: %w", name, err)
	}

	var current workloadSpec
	if err := json.Unmarshal(existing.Spec, &current); err != nil {
		return false, fmt.Errorf("decode Workload %s spec: %w", name, err)
	}
	if workloadsEqual(current, desired) {
		return false, nil
	}
	if _, err := p.client.Patch(ctx, path, map[string]any{"spec": workloadPatch(current, desired)}); err != nil {
		return false, fmt.Errorf("patch Workload %s: %w", name, err)
	}
	return true, nil
}

func desiredWorkload(project, name string, spec resource.ComponentSpec) workloadSpec {
	w := workloadSpec{
		Owner:     ownerRef{ProjectName: project, ComponentName: name},
		Container: workloadContainer{Image: spec.Image},
		Endpoints: make(map[string]workloadEndpoint, len(spec.Ports)),
	}
	for _, port := range spec.Ports {
		w.Endpoints[port.Name] = workloadEndpoint{Type: endpointType, Port: port.Port}
	}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		w.Container.Env = append(w.Container.Env, envVar{Key: k, Value: spec.Env[k]})
	}
	return w
}

func workloadsEqual(a, b workloadSpec) bool {
	return a.Container.Image == b.Container.Image &&
		envEqual(a.Container.Env, b.Container.Env) &&
		endpointsEqual(a.Endpoints, b.Endpoints)
}

func envEqual(a, b []envVar) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	am := make(map[string]string, len(a))
	for _, e := range a {
		am[e.Key] = e.Value
	}
	bm := make(map[string]string, len(b))
	for _, e := range b {
		bm[e.Key] = e.Value
	}
	return reflect.DeepEqual(am, bm)
}

func endpointsEqual(a, b map[string]workloadEndpoint) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// workloadPatch builds a JSON merge patch body for spec. Merge patch
// replaces arrays wholesale (fine for env) but merges objects key by key,
// so endpoints removed from the desired set must be sent as explicit nulls.
func workloadPatch(current, desired workloadSpec) map[string]any {
	endpoints := make(map[string]any, len(desired.Endpoints)+len(current.Endpoints))
	for k := range current.Endpoints {
		endpoints[k] = nil
	}
	for k, v := range desired.Endpoints {
		endpoints[k] = v
	}
	var env any = desired.Container.Env
	if len(desired.Container.Env) == 0 {
		env = nil
	}
	return map[string]any{
		"container": map[string]any{"image": desired.Container.Image, "env": env},
		"endpoints": endpoints,
	}
}

// failureReasons are upstream Ready-condition reasons (ReleaseBinding and
// ProjectReleaseBinding share most of their vocabulary) that indicate the
// deployment won't converge on its own. Anything else not-ready is
// reported as progressing.
var failureReasons = map[string]bool{
	"ResourceApplyFailed":         true,
	"RenderingFailed":             true,
	"ResourcesDegraded":           true,
	"NamespaceDegraded":           true,
	"EnvironmentNotFound":         true,
	"DataPlaneNotFound":           true,
	"DataPlaneNotConfigured":      true,
	"ProjectNotFound":             true,
	"ComponentNotFound":           true,
	"InvalidReleaseConfiguration": true,
	"ReleaseOwnershipConflict":    true,
	"ReleaseUpdateFailed":         true,
	"JobFailed":                   true,
}

// mapReady translates an upstream object's Ready condition into the
// provider-neutral reasons core/reconcile expects, carrying the upstream
// reason and message in the returned message. A Ready condition computed
// for an older generation of obj's spec says nothing about the current
// one, so it counts as progressing.
func mapReady(obj kubernetes.Object) (bool, string, string) {
	var st objectStatus
	if len(obj.Status) > 0 {
		if err := json.Unmarshal(obj.Status, &st); err != nil {
			return false, reconcile.ReasonProgressing, "status not parseable: " + err.Error()
		}
	}
	for _, c := range st.Conditions {
		if c.Type != "Ready" {
			continue
		}
		detail := c.Reason
		if c.Message != "" {
			detail += ": " + c.Message
		}
		switch {
		case c.ObservedGeneration > 0 && c.ObservedGeneration < obj.Metadata.Generation:
			return false, reconcile.ReasonProgressing, "status not yet observed for latest spec (" + detail + ")"
		case c.Status == "True":
			return true, reconcile.ReasonDeployed, detail
		case failureReasons[c.Reason]:
			return false, reconcile.ReasonDeployFailed, detail
		default:
			return false, reconcile.ReasonProgressing, detail
		}
	}
	return false, reconcile.ReasonProgressing, "no Ready condition reported yet"
}

func (p *Provider) deleteIgnoringNotFound(ctx context.Context, path string) error {
	err := p.client.Delete(ctx, path)
	if err == nil || errors.Is(err, kubernetes.ErrNotFound) {
		return nil
	}
	return fmt.Errorf("delete %s: %w", path, err)
}
