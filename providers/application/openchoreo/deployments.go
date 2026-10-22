package openchoreo

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// Environments, pipelines, releases and Deployments (spec 020 plan
// decision 2, T001 spike findings 1-5, T005).
//
// Serendip owns every release and every binding: Components are created
// with autoDeploy off, so the platform neither makes releases nor binds
// them itself, and a Serendip Deployment is the only thing that runs a
// Component anywhere. A release is the platform's own snapshot shape,
// {componentType, owner, workload}, named after its content.

var _ reconcile.DeploymentProvider = (*Provider)(nil)

// EnsureEnvironment makes env exist on the default data plane.
func (p *Provider) EnsureEnvironment(ctx context.Context, env resource.Environment) error {
	_, err := apply(ctx, p.client, namespacedPath("environments"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "Environment",
		Metadata: kubernetes.ObjectMeta{Name: environmentObjectName(env.Metadata.ID), Namespace: ControlPlaneNamespace,
			Labels: serendipLabels(env.Metadata.ID, env.Metadata.Name)},
		Spec: mustJSON(map[string]any{
			"dataPlaneRef": map[string]string{"kind": "DataPlane", "name": DefaultDataPlaneName},
			"isProduction": false,
		}),
	})
	return err
}

// DeleteEnvironment removes env's platform Environment.
func (p *Provider) DeleteEnvironment(ctx context.Context, env resource.Environment) error {
	return p.deleteIgnoringNotFound(ctx, namespacedPath("environments")+"/"+environmentObjectName(env.Metadata.ID))
}

// EnsurePipeline writes project's promotion order as its DeploymentPipeline.
// The platform doesn't enforce it (T001 finding 2); Serendip does. An empty
// pipeline is accepted (verified on 1.3.1).
func (p *Provider) EnsurePipeline(ctx context.Context, project resource.Project) error {
	_, err := apply(ctx, p.client, namespacedPath("deploymentpipelines"), pipelineObject(project.Metadata.ID, project.Metadata.Name, project.Spec.Pipeline))
	return err
}

func pipelineObject(projectID, name string, pl resource.Pipeline) kubernetes.Object {
	paths := make([]any, 0, len(pl.Paths))
	for _, path := range pl.Paths {
		targets := make([]any, 0, len(path.To))
		for _, t := range path.To {
			targets = append(targets, map[string]string{"kind": "Environment", "name": environmentObjectName(t)})
		}
		paths = append(paths, map[string]any{
			"sourceEnvironmentRef":  map[string]string{"kind": "Environment", "name": environmentObjectName(path.From)},
			"targetEnvironmentRefs": targets,
		})
	}
	return kubernetes.Object{
		APIVersion: apiVersion, Kind: "DeploymentPipeline",
		Metadata: kubernetes.ObjectMeta{Name: pipelineName(projectID), Namespace: ControlPlaneNamespace,
			Labels: serendipLabels(projectID, name)},
		Spec: mustJSON(map[string]any{"promotionPaths": paths}),
	}
}

// releaseSpec is a ComponentRelease's spec. The workload carries no owner
// (the CRD refuses one; T005 spike).
type releaseSpec struct {
	ComponentType releaseComponentType `json:"componentType"`
	Owner         ownerRef             `json:"owner"`
	Workload      releaseWorkload      `json:"workload"`
}

type releaseComponentType struct {
	Kind string          `json:"kind"`
	Name string          `json:"name"`
	Spec json.RawMessage `json:"spec"`
}

type releaseWorkload struct {
	Container workloadContainer           `json:"container"`
	Endpoints map[string]workloadEndpoint `json:"endpoints,omitempty"`
}

// componentProject is the platform project of a Component's Application.
func componentProject(comp resource.Component) (string, error) {
	for _, o := range comp.Metadata.OwnerRefs {
		if o.Kind == resource.KindApplication {
			return projectName(o.ID), nil
		}
	}
	return "", fmt.Errorf("component %s has no Application owner", comp.Metadata.ID)
}

// CreateRelease snapshots comp's current spec, with the component type as
// it is now, as a release. The same content gives the same name, so this
// is idempotent.
func (p *Provider) CreateRelease(ctx context.Context, comp resource.Component) (reconcile.Release, error) {
	project, err := componentProject(comp)
	if err != nil {
		return reconcile.Release{}, err
	}
	return p.createRelease(ctx, project, comp)
}

func (p *Provider) createRelease(ctx context.Context, project string, comp resource.Component) (reconcile.Release, error) {
	typeName := componentTypeWorkload + "/" + componentTypeName
	ct, err := p.client.Get(ctx, clusterPath("clustercomponenttypes")+"/"+componentTypeName)
	if err != nil {
		return reconcile.Release{}, fmt.Errorf("get the component type %s: %w", componentTypeName, err)
	}
	name := componentName(comp.Metadata.ID)
	w := desiredWorkload(project, name, comp.Spec)
	spec := releaseSpec{
		ComponentType: releaseComponentType{Kind: "ClusterComponentType", Name: typeName, Spec: ct.Spec},
		Owner:         ownerRef{ProjectName: project, ComponentName: name},
		Workload:      releaseWorkload{Container: w.Container, Endpoints: w.Endpoints},
	}
	content, err := json.Marshal(spec)
	if err != nil {
		return reconcile.Release{}, fmt.Errorf("encode the release of %s: %w", name, err)
	}
	relName := releaseObjectName(comp.Metadata.ID, content)
	obj, err := apply(ctx, p.client, namespacedPath("componentreleases"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "ComponentRelease",
		Metadata: kubernetes.ObjectMeta{Name: relName, Namespace: ControlPlaneNamespace,
			Labels: serendipLabels(comp.Metadata.ID, comp.Metadata.Name)},
		Spec: content,
	})
	if err != nil {
		return reconcile.Release{}, err
	}
	return releaseOf(obj), nil
}

func releaseOf(obj kubernetes.Object) reconcile.Release {
	r := reconcile.Release{Name: obj.Metadata.Name}
	if t, err := time.Parse(time.RFC3339, obj.Metadata.CreationTimestamp); err == nil {
		r.Created = t
	}
	var s struct {
		Workload struct {
			Container struct {
				Image string `json:"image"`
			} `json:"container"`
		} `json:"workload"`
	}
	if json.Unmarshal(obj.Spec, &s) == nil {
		r.Image = s.Workload.Container.Image
	}
	return r
}

// Releases lists comp's releases, newest first: Serendip's, and any the
// platform made before Serendip took them over (those carry no labels, so
// the list is the namespace's, filtered here; RP-029). Creation times have
// one-second resolution: releases made within the same second come in name
// order.
func (p *Provider) Releases(ctx context.Context, comp resource.Component) ([]reconcile.Release, error) {
	name := componentName(comp.Metadata.ID)
	all, err := p.client.List(ctx, namespacedPath("componentreleases"))
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	var out []reconcile.Release
	for _, obj := range all {
		var s struct {
			Owner ownerRef `json:"owner"`
		}
		if json.Unmarshal(obj.Spec, &s) != nil || s.Owner.ComponentName != name {
			continue
		}
		out = append(out, releaseOf(obj))
	}
	slices.SortFunc(out, func(a, b reconcile.Release) int {
		if c := b.Created.Compare(a.Created); c != 0 {
			return c
		}
		return cmp.Compare(b.Name, a.Name)
	})
	return out, nil
}

// bindingSpec is a ReleaseBinding's spec, as Serendip writes it.
type bindingSpec struct {
	Environment                     string                    `json:"environment"`
	Owner                           ownerRef                  `json:"owner"`
	ReleaseName                     string                    `json:"releaseName"`
	State                           string                    `json:"state"`
	ComponentTypeEnvironmentConfigs map[string]any            `json:"componentTypeEnvironmentConfigs,omitempty"`
	TraitEnvironmentConfigs         map[string]map[string]any `json:"traitEnvironmentConfigs,omitempty"`
	WorkloadOverrides               *workloadOverrides        `json:"workloadOverrides,omitempty"`
}

type workloadOverrides struct {
	Container struct {
		Env []envVar `json:"env"`
	} `json:"container"`
}

// Deploy runs comp, of app, in env, as d says: its release (the
// Component's current one when empty) with d's overrides. A first deploy, a
// promotion and a rollback are the same call. When the Deployment is
// Ready, the Component's legacy binding from before spec 020 is removed:
// the Deployment has taken over what it ran.
func (p *Provider) Deploy(ctx context.Context, app resource.Application, comp resource.Component, env resource.Environment, d resource.Deployment) (reconcile.DeploymentObservation, error) {
	project := projectName(app.Metadata.ID)
	name := componentName(comp.Metadata.ID)
	// A pinned release must exist and be this Component's: checked before
	// anything is written.
	release := d.Spec.Release
	if release != "" {
		obj, err := p.client.Get(ctx, namespacedPath("componentreleases")+"/"+release)
		if errors.Is(err, kubernetes.ErrNotFound) {
			return reconcile.DeploymentObservation{Reason: reconcile.ReasonUnsupported,
				Message: fmt.Sprintf("release %q doesn't exist", release)}, nil
		}
		if err != nil {
			return reconcile.DeploymentObservation{}, fmt.Errorf("get release %s: %w", release, err)
		}
		var s struct {
			Owner ownerRef `json:"owner"`
		}
		if json.Unmarshal(obj.Spec, &s) != nil || s.Owner.ComponentName != name {
			return reconcile.DeploymentObservation{Reason: reconcile.ReasonUnsupported,
				Message: fmt.Sprintf("release %q isn't a release of this Component", release)}, nil
		}
	}

	if err := p.EnsureEnvironment(ctx, env); err != nil {
		return reconcile.DeploymentObservation{}, err
	}
	envName := environmentObjectName(env.Metadata.ID)
	prbName := projectEnvBindingName(project, env.Metadata.ID)
	if _, err := apply(ctx, p.client, namespacedPath("projectreleasebindings"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "ProjectReleaseBinding",
		Metadata: kubernetes.ObjectMeta{Name: prbName, Namespace: ControlPlaneNamespace,
			Labels: serendipLabels(app.Metadata.ID, app.Metadata.Name)},
		Spec: mustJSON(map[string]any{"owner": ownerRef{ProjectName: project}, "environment": envName}),
	}); err != nil {
		return reconcile.DeploymentObservation{}, err
	}
	if release == "" {
		r, err := p.createRelease(ctx, project, comp)
		if err != nil {
			return reconcile.DeploymentObservation{}, err
		}
		release = r.Name
	}

	spec := bindingSpec{
		Environment: envName,
		Owner:       ownerRef{ProjectName: project, ComponentName: name},
		ReleaseName: release,
		State:       "Active",
	}
	o := d.Spec.Overrides
	if len(o.Config) > 0 {
		spec.ComponentTypeEnvironmentConfigs = o.Config
	}
	if len(o.Traits) > 0 {
		spec.TraitEnvironmentConfigs = o.Traits
	}
	if len(o.Env) > 0 {
		wo := &workloadOverrides{}
		for _, k := range sortedKeys(o.Env) {
			wo.Container.Env = append(wo.Container.Env, envVar{Key: k, Value: o.Env[k]})
		}
		spec.WorkloadOverrides = wo
	}
	binding, err := apply(ctx, p.client, namespacedPath("releasebindings"), kubernetes.Object{
		APIVersion: apiVersion, Kind: "ReleaseBinding",
		Metadata: kubernetes.ObjectMeta{Name: deploymentBindingName(comp.Metadata.ID, env.Metadata.ID), Namespace: ControlPlaneNamespace,
			Labels: serendipLabels(d.Metadata.ID, d.Metadata.Name)},
		Spec: mustJSON(spec),
	})
	if err != nil {
		return reconcile.DeploymentObservation{}, err
	}
	ready, reason, message := mapReady(binding)
	obs := reconcile.DeploymentObservation{Release: release, Ready: ready, Reason: reason, Message: message}
	if !ready {
		return obs, nil
	}
	if exposed := exposedPorts(comp.Spec); len(exposed) > 0 {
		reach, err := p.observeReachability(ctx, prbName, comp, binding, exposed)
		switch {
		case err != nil:
			obs.Message = "running; its endpoints couldn't be read: " + err.Error()
		case reach.Reachable:
			obs.Endpoints = reach.Endpoints
		default:
			obs.Message = "running; endpoints not reachable yet: " + reach.Message
		}
	}
	// Running here now: the binding auto-deploy made before spec 020 can go.
	// Best effort: a failure leaves it for the next pass, and must not make
	// a running Deployment look failed.
	_ = p.deleteIgnoringNotFound(ctx, namespacedPath("releasebindings")+"/"+releaseBindingName(comp.Metadata.ID))
	return obs, nil
}

// Undeploy stops d running in env. When it was the last of its
// Application's deployments there, the Application's binding to the
// Environment goes too, removing the data-plane namespace. That's decided,
// and done, before the Deployment's own binding is deleted, so a retry
// after a failure still finds the binding it needs to know the project.
// Reconciles run one at a time (core/reconcile's engine), so a Deploy into
// the same Environment can't interleave.
func (p *Provider) Undeploy(ctx context.Context, d resource.Deployment, env resource.Environment) error {
	path := namespacedPath("releasebindings") + "/" + deploymentBindingName(d.Spec.Component, env.Metadata.ID)
	binding, err := p.client.Get(ctx, path)
	if errors.Is(err, kubernetes.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get the binding of Deployment %s: %w", d.Metadata.ID, err)
	}
	var spec bindingSpec
	if err := json.Unmarshal(binding.Spec, &spec); err != nil {
		return fmt.Errorf("decode the binding of Deployment %s: %w", d.Metadata.ID, err)
	}
	all, err := p.client.List(ctx, namespacedPath("releasebindings"))
	if err != nil {
		return fmt.Errorf("list bindings: %w", err)
	}
	last := true
	for _, b := range all {
		var s bindingSpec
		if b.Metadata.Name == binding.Metadata.Name || b.Metadata.DeletionTimestamp != "" || json.Unmarshal(b.Spec, &s) != nil {
			continue // itself, or a sibling already going
		}
		if s.Owner.ProjectName == spec.Owner.ProjectName && s.Environment == spec.Environment {
			last = false
			break
		}
	}
	if last {
		if err := p.deleteIgnoringNotFound(ctx, namespacedPath("projectreleasebindings")+"/"+projectEnvBindingName(spec.Owner.ProjectName, env.Metadata.ID)); err != nil {
			return err
		}
	}
	return p.deleteIgnoringNotFound(ctx, path)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
