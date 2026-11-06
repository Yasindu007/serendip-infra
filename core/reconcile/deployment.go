package reconcile

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// ReasonWaitingForComponent: a Deployment's Component isn't there, or isn't
// released yet, or a Component it calls isn't running in the same
// Environment (the platform holds the release until its calls resolve).
const ReasonWaitingForComponent = "WaitingForComponent"

// DeploymentReconciler runs Deployments (specs/020 plan decision 2): one
// Component, in the Environment that owns the Deployment, through a
// DeploymentProvider. Removing what ran is the approval reconciler's
// clean-up (UndeployCleanup), since a delete always goes through an
// Approval and the record is gone by the time this runs.
type DeploymentReconciler struct {
	deployments store.Store[resource.DeploymentSpec, resource.DeploymentStatus]
	envs        store.Store[resource.EnvironmentSpec, resource.EnvironmentStatus]
	comps       store.Store[resource.ComponentSpec, resource.ComponentStatus]
	apps        store.Store[resource.ApplicationSpec, resource.ApplicationStatus]
	provider    DeploymentProvider
}

func NewDeploymentReconciler(
	deployments store.Store[resource.DeploymentSpec, resource.DeploymentStatus],
	envs store.Store[resource.EnvironmentSpec, resource.EnvironmentStatus],
	comps store.Store[resource.ComponentSpec, resource.ComponentStatus],
	apps store.Store[resource.ApplicationSpec, resource.ApplicationStatus],
	provider DeploymentProvider,
) *DeploymentReconciler {
	return &DeploymentReconciler{deployments: deployments, envs: envs, comps: comps, apps: apps, provider: provider}
}

var _ Reconciler = (*DeploymentReconciler)(nil)

func (r *DeploymentReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	d, err := r.deployments.Get(ctx, ref.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // undeployed by the approval's clean-up
	}
	if err != nil {
		return err
	}
	obs, err := r.observe(ctx, d)
	if err != nil {
		return err
	}
	converging := !obs.Ready && (obs.Reason == ReasonProgressing || obs.Reason == ReasonProviderNotReady || obs.Reason == ReasonWaitingForComponent)
	return requeueWhen(r.setState(ctx, d, obs), converging)
}

func (r *DeploymentReconciler) observe(ctx context.Context, d resource.Deployment) (DeploymentObservation, error) {
	if err := resource.ValidateDeploymentSpec(d.Spec); err != nil {
		return DeploymentObservation{Reason: ReasonInvalidSpec, Message: err.Error()}, nil
	}
	envID := ownerOf(d.Metadata, resource.KindEnvironment)
	if envID == "" {
		return DeploymentObservation{Reason: ReasonInvalidSpec, Message: "a Deployment is owned by the Environment it runs in"}, nil
	}
	env, err := r.envs.Get(ctx, envID)
	if errors.Is(err, store.ErrNotFound) {
		return DeploymentObservation{Reason: ReasonInvalidSpec, Message: fmt.Sprintf("Environment %s not found", envID)}, nil
	}
	if err != nil {
		return DeploymentObservation{}, err
	}
	comp, err := r.comps.Get(ctx, d.Spec.Component)
	if errors.Is(err, store.ErrNotFound) {
		return DeploymentObservation{Reason: ReasonWaitingForComponent, Message: fmt.Sprintf("Component %s not found", d.Spec.Component)}, nil
	}
	if err != nil {
		return DeploymentObservation{}, err
	}
	if c := findCondition(comp.Status.Conditions, conditionReady); c == nil || c.Status != resource.ConditionTrue {
		// Say why, in the Component's own words: right after a restart it's
		// often only that the platform can't be read yet, not that there's
		// no release.
		why := "it has no release yet"
		if c != nil && c.Reason != "" {
			why = "it isn't ready (" + c.Reason + ")"
		}
		return DeploymentObservation{Reason: ReasonWaitingForComponent, Message: fmt.Sprintf("waiting for Component %s: %s", comp.Metadata.ID, why)}, nil
	}
	appID := ownerOf(comp.Metadata, resource.KindApplication)
	app, err := r.apps.Get(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		return DeploymentObservation{Reason: ReasonWaitingForComponent, Message: fmt.Sprintf("the Component's Application %s not found", appID)}, nil
	}
	if err != nil {
		return DeploymentObservation{}, err
	}
	// Deploying stays inside a subscription: the Component's Application
	// and the Environment belong to the same Project.
	if p, q := ownerOf(app.Metadata, resource.KindProject), ownerOf(env.Metadata, resource.KindProject); p == "" || p != q {
		return DeploymentObservation{Reason: ReasonUnsupported,
			Message: "the Component's Application and the Environment must belong to the same Project"}, nil
	}
	// A source Component is released with the image its own reconciler
	// resolved from its builds; it's Ready only once it has one.
	run, ok := runnable(comp, comp.Status.Image)
	if !ok {
		return DeploymentObservation{Reason: ReasonWaitingForComponent,
			Message: fmt.Sprintf("waiting for Component %s: it has no built image yet", comp.Metadata.ID)}, nil
	}
	obs, err := r.provider.Deploy(ctx, app, run, env, d)
	if errors.Is(err, ErrProviderNotReady) {
		return DeploymentObservation{Reason: ReasonProviderNotReady, Message: providerNotReadyMessage(applicationPlatformCapability)}, nil
	}
	if err != nil {
		return DeploymentObservation{Reason: ReasonEnsureFailed, Message: err.Error()}, nil
	}
	return obs, nil
}

// setState writes the Ready Condition, the running release, its history
// and the endpoints, only when one of them changes (message churn alone
// isn't a change).
func (r *DeploymentReconciler) setState(ctx context.Context, d resource.Deployment, obs DeploymentObservation) error {
	ready := resource.Condition{Type: conditionReady, Status: readyStatus(obs.Ready), Reason: obs.Reason, Message: obs.Message, LastTransitionTime: time.Now()}
	release, history := d.Status.Release, d.Status.History
	if obs.Release != "" && obs.Ready {
		release = obs.Release
		history = resource.RecordRelease(d.Status.History, d.Status.Release, obs.Release)
	}
	var endpoints []resource.ComponentEndpointStatus
	if obs.Ready {
		endpoints = obs.Endpoints
	}
	if conditionUnchanged(d.Status.Conditions, ready) && release == d.Status.Release &&
		slices.Equal(history, d.Status.History) && endpointsEqual(endpoints, d.Status.Endpoints) {
		return nil
	}
	d.Status.Conditions = upsertCondition(d.Status.Conditions, keepTransitionTime(d.Status.Conditions, ready))
	d.Status.Release, d.Status.History, d.Status.Endpoints = release, history, endpoints
	_, err := r.deployments.Update(ctx, d)
	if errors.Is(err, store.ErrConflict) {
		// Edited while the platform was asked: the edit's own reconcile
		// observes again and writes the state (as setReady does for
		// packages).
		return nil
	}
	return err
}

func ownerOf(m resource.Metadata, kind resource.Kind) string {
	for _, o := range m.OwnerRefs {
		if o.Kind == kind {
			return o.ID
		}
	}
	return ""
}

// ProjectReconciler writes each Project's pipeline to the platform.
type ProjectReconciler struct {
	projects store.Store[resource.ProjectSpec, resource.ProjectStatus]
	provider DeploymentProvider
}

func NewProjectReconciler(projects store.Store[resource.ProjectSpec, resource.ProjectStatus], provider DeploymentProvider) *ProjectReconciler {
	return &ProjectReconciler{projects: projects, provider: provider}
}

var _ Reconciler = (*ProjectReconciler)(nil)

func (r *ProjectReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	p, err := r.projects.Get(ctx, ref.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // the pipeline goes with the approval's clean-up
	}
	if err != nil {
		return err
	}
	ready := resource.Condition{Type: conditionReady, Status: resource.ConditionTrue, Reason: ReasonDeployed, Message: "pipeline in place", LastTransitionTime: time.Now()}
	if err := resource.ValidatePipeline(p.Spec.Pipeline); err != nil {
		ready = resource.Condition{Type: conditionReady, Status: resource.ConditionFalse, Reason: ReasonInvalidSpec, Message: err.Error(), LastTransitionTime: time.Now()}
	} else if err := r.provider.EnsurePipeline(ctx, p); errors.Is(err, ErrProviderNotReady) {
		ready = resource.Condition{Type: conditionReady, Status: resource.ConditionFalse, Reason: ReasonProviderNotReady, Message: providerNotReadyMessage(applicationPlatformCapability), LastTransitionTime: time.Now()}
	} else if err != nil {
		return err
	}
	if conditionUnchanged(p.Status.Conditions, ready) {
		return nil
	}
	p.Status.Conditions = upsertCondition(p.Status.Conditions, keepTransitionTime(p.Status.Conditions, ready))
	_, err = r.projects.Update(ctx, p)
	return err
}

// Clean-up for the approval reconciler (spec 019), by kind: what each
// removed record ran on the platform.

// UndeployCleanup stops a removed Deployment running.
func UndeployCleanup(provider DeploymentProvider) CleanupFunc {
	return func(ctx context.Context, ref resource.ApprovalRef) error {
		// The provider finds what to remove by the Deployment's ID alone, so
		// a damaged LastSpec can't strand the clean-up.
		return provider.Undeploy(ctx, resource.Deployment{Metadata: resource.Metadata{ID: ref.ID, Name: ref.Name}})
	}
}

// EnvironmentCleanup removes a removed Environment from the platform.
func EnvironmentCleanup(provider DeploymentProvider) CleanupFunc {
	return func(ctx context.Context, ref resource.ApprovalRef) error {
		return provider.DeleteEnvironment(ctx, resource.Environment{Metadata: resource.Metadata{ID: ref.ID, Name: ref.Name}})
	}
}

// PipelineCleanup removes a removed Project's pipeline.
func PipelineCleanup(provider DeploymentProvider) CleanupFunc {
	return func(ctx context.Context, ref resource.ApprovalRef) error {
		return provider.DeletePipeline(ctx, ref.ID)
	}
}
