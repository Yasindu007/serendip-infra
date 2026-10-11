package reconcile

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// conditionReady is the one Condition.Type ApplicationReconciler and
// ComponentReconciler set.
const conditionReady = "Ready"

// Reasons for the "Ready" Condition on Applications and Components. The
// set is fixed and provider-neutral: an ApplicationPlatformProvider maps
// its own upstream status vocabulary onto the first four, carrying any
// upstream detail in the message instead (constitution rule 1 -- no
// product vocabulary in core/ Condition reasons). The last three are set
// by the reconcilers themselves.
const (
	ReasonDeployed     = "Deployed"
	ReasonProgressing  = "Progressing"
	ReasonDeployFailed = "DeployFailed"
	ReasonUnsupported  = "Unsupported"

	ReasonInvalidSpec           = "InvalidSpec"
	ReasonWaitingForApplication = "WaitingForApplication"
	ReasonEnsureFailed          = "EnsureFailed"
)

// ApplicationPlatformProvider is the capability-shaped interface
// ApplicationReconciler and ComponentReconciler drive. Uses only
// core/resource types; whatever objects a provider creates to realise an
// Application or Component are entirely its own business.
//
// Ensure* methods have StorageProvider.Ensure's shape: both capabilities
// provision real external state and report readiness, with reason drawn
// from the Reason* constants above. Delete* methods receive a resource
// that may carry only Metadata.ID (the Serendip-side record is already
// gone when they're called from a reconciler's not-found branch), so a
// provider must be able to locate what to tear down from the ID alone.
// Both are no-ops, not errors, if nothing exists to delete.
type ApplicationPlatformProvider interface {
	EnsureApplication(ctx context.Context, app resource.Application) (ready bool, reason, message string, err error)
	DeleteApplication(ctx context.Context, app resource.Application) error
	// EnsureComponent is given comp's owning Application, already
	// confirmed Ready by ComponentReconciler.
	EnsureComponent(ctx context.Context, app resource.Application, comp resource.Component) (ready bool, reason, message string, err error)
	DeleteComponent(ctx context.Context, comp resource.Component) error
}

// ApplicationReconciler drives an ApplicationPlatformProvider to make an
// Application's platform-side grouping exist, writing a "Ready"
// Condition back. Provider is constructor-injected, same as
// VolumeReconciler.
type ApplicationReconciler struct {
	apps     store.Store[resource.ApplicationSpec, resource.ApplicationStatus]
	provider ApplicationPlatformProvider
}

func NewApplicationReconciler(
	apps store.Store[resource.ApplicationSpec, resource.ApplicationStatus],
	provider ApplicationPlatformProvider,
) *ApplicationReconciler {
	return &ApplicationReconciler{apps: apps, provider: provider}
}

var _ Reconciler = (*ApplicationReconciler)(nil)

func (r *ApplicationReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	app, err := r.apps.Get(ctx, ref.ID)
	if errors.Is(err, store.ErrNotFound) {
		// The provider holds real external state for this Application,
		// so a deleted record must still tear it down (provider-model.md:
		// don't copy ClusterReconciler's passive no-op here).
		return r.provider.DeleteApplication(ctx, resource.Application{Metadata: resource.Metadata{ID: ref.ID}})
	}
	if err != nil {
		return err
	}

	ready, reason, message, err := r.provider.EnsureApplication(ctx, app)
	if err != nil {
		return r.setCondition(ctx, app, resource.ConditionFalse, ReasonEnsureFailed, err.Error())
	}
	return r.setCondition(ctx, app, readyStatus(ready), reason, message)
}

// setCondition writes only when {Status, Reason} changes -- the same
// idempotency guarantee as VolumeReconciler.setCondition. Message is
// excluded from the comparison so upstream message churn alone doesn't
// cause writes.
func (r *ApplicationReconciler) setCondition(ctx context.Context, app resource.Application, status resource.ConditionStatus, reason, message string) error {
	if existing := findCondition(app.Status.Conditions, conditionReady); existing != nil &&
		existing.Status == status && existing.Reason == reason {
		return nil
	}
	app.Status.Conditions = upsertCondition(app.Status.Conditions, resource.Condition{
		Type:               conditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: time.Now(),
	})
	_, err := r.apps.Update(ctx, app)
	return err
}

// ComponentReconciler drives an ApplicationPlatformProvider to run a
// Component's workload, writing a "Ready" Condition back. A Component
// must be owned by exactly one Application, which must itself be Ready
// before the provider is asked to deploy anything into it.
type ComponentReconciler struct {
	components store.Store[resource.ComponentSpec, resource.ComponentStatus]
	apps       store.Store[resource.ApplicationSpec, resource.ApplicationStatus]
	provider   ApplicationPlatformProvider
}

func NewComponentReconciler(
	components store.Store[resource.ComponentSpec, resource.ComponentStatus],
	apps store.Store[resource.ApplicationSpec, resource.ApplicationStatus],
	provider ApplicationPlatformProvider,
) *ComponentReconciler {
	return &ComponentReconciler{components: components, apps: apps, provider: provider}
}

var _ Reconciler = (*ComponentReconciler)(nil)

func (r *ComponentReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	comp, err := r.components.Get(ctx, ref.ID)
	if errors.Is(err, store.ErrNotFound) {
		return r.provider.DeleteComponent(ctx, resource.Component{Metadata: resource.Metadata{ID: ref.ID}})
	}
	if err != nil {
		return err
	}

	if err := validateComponentSpec(comp.Spec); err != nil {
		return r.setCondition(ctx, comp, resource.ConditionFalse, ReasonInvalidSpec, err.Error())
	}
	appID, err := owningApplicationID(comp)
	if err != nil {
		return r.setCondition(ctx, comp, resource.ConditionFalse, ReasonInvalidSpec, err.Error())
	}

	app, err := r.apps.Get(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		return r.setCondition(ctx, comp, resource.ConditionFalse, ReasonWaitingForApplication,
			fmt.Sprintf("owning Application %s not found", appID))
	}
	if err != nil {
		return err
	}
	if c := findCondition(app.Status.Conditions, conditionReady); c == nil || c.Status != resource.ConditionTrue {
		return r.setCondition(ctx, comp, resource.ConditionFalse, ReasonWaitingForApplication,
			fmt.Sprintf("owning Application %s is not Ready yet", appID))
	}

	ready, reason, message, err := r.provider.EnsureComponent(ctx, app, comp)
	if err != nil {
		return r.setCondition(ctx, comp, resource.ConditionFalse, ReasonEnsureFailed, err.Error())
	}
	return r.setCondition(ctx, comp, readyStatus(ready), reason, message)
}

func (r *ComponentReconciler) setCondition(ctx context.Context, comp resource.Component, status resource.ConditionStatus, reason, message string) error {
	if existing := findCondition(comp.Status.Conditions, conditionReady); existing != nil &&
		existing.Status == status && existing.Reason == reason {
		return nil
	}
	comp.Status.Conditions = upsertCondition(comp.Status.Conditions, resource.Condition{
		Type:               conditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: time.Now(),
	})
	_, err := r.components.Update(ctx, comp)
	return err
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// validateComponentSpec rejects specs no provider could run, so they
// surface as InvalidSpec rather than as a provider-side failure.
func validateComponentSpec(spec resource.ComponentSpec) error {
	if spec.Image == "" {
		return errors.New("spec.image is required")
	}
	seen := make(map[string]bool, len(spec.Ports))
	for i, p := range spec.Ports {
		if !dnsLabel.MatchString(p.Name) {
			return fmt.Errorf("spec.ports[%d].name %q must be a DNS label (lowercase alphanumerics and '-', at most 63 chars)", i, p.Name)
		}
		if p.Port < 1 || p.Port > 65535 {
			return fmt.Errorf("spec.ports[%d].port %d must be between 1 and 65535", i, p.Port)
		}
		if seen[p.Name] {
			return fmt.Errorf("spec.ports[%d].name %q is duplicated", i, p.Name)
		}
		seen[p.Name] = true
	}
	return nil
}

// owningApplicationID returns the ID of comp's single Application owner.
func owningApplicationID(comp resource.Component) (string, error) {
	var ids []string
	for _, o := range comp.Metadata.OwnerRefs {
		if o.Kind == resource.KindApplication {
			ids = append(ids, o.ID)
		}
	}
	switch len(ids) {
	case 1:
		return ids[0], nil
	case 0:
		return "", errors.New("component must have an ownerRef to exactly one Application, found none")
	default:
		return "", fmt.Errorf("component must have an ownerRef to exactly one Application, found %d", len(ids))
	}
}

func readyStatus(ready bool) resource.ConditionStatus {
	if ready {
		return resource.ConditionTrue
	}
	return resource.ConditionFalse
}
