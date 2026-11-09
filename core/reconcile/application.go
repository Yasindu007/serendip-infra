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

// Reasons for a Component's "Reachable" Condition (conditionReachable,
// shared with SecretReconciler). It is set only while at least one port is
// exposed, and says whether the exposed ports can actually be reached from
// outside the cluster. Fixed and provider-neutral like the Ready reasons.
const (
	ReasonServing       = "Serving"
	ReasonPending       = "Pending"
	ReasonRouteRejected = "RouteRejected"
)

// ComponentObservation is what an ApplicationPlatformProvider reports
// about one Component.
type ComponentObservation struct {
	Ready   bool
	Reason  string
	Message string
	// Reachable is nil when no port is exposed.
	Reachable *Reachability
}

// Reachability reports whether a Component's exposed ports are reachable
// from outside the cluster. Endpoints is only honoured when Reachable is
// true: an address is never published before it serves.
type Reachability struct {
	Reachable bool
	Reason    string
	Message   string
	Endpoints []resource.ComponentEndpointStatus
}

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
	EnsureComponent(ctx context.Context, app resource.Application, comp resource.Component) (ComponentObservation, error)
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
	if errors.Is(err, ErrProviderNotReady) {
		return requeueWhen(r.setCondition(ctx, app, resource.ConditionFalse, ReasonProviderNotReady,
			providerNotReadyMessage(applicationPlatformCapability)), true)
	}
	if err != nil {
		return r.setCondition(ctx, app, resource.ConditionFalse, ReasonEnsureFailed, err.Error())
	}
	// Still rolling out: ask for an early re-check, since nothing on the
	// Serendip side will announce the upstream catching up.
	return requeueWhen(r.setCondition(ctx, app, readyStatus(ready), reason, message), !ready && reason == ReasonProgressing)
}

// applicationPlatformCapability names the capability in not-ready
// messages.
const applicationPlatformCapability = "application platform"

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

	// Building from source (WithBuilds); nil builds means a source
	// Component is Unsupported.
	builds        store.Store[resource.BuildSpec, resource.BuildStatus]
	buildProvider BuildProvider
	creds         CredentialReader
	// lastPoll is when auto-build last read each Component's branch head.
	// It's in memory: after a restart each is read once more, early.
	lastPoll map[string]time.Time
	now      func() time.Time
}

func NewComponentReconciler(
	components store.Store[resource.ComponentSpec, resource.ComponentStatus],
	apps store.Store[resource.ApplicationSpec, resource.ApplicationStatus],
	provider ApplicationPlatformProvider,
) *ComponentReconciler {
	return &ComponentReconciler{components: components, apps: apps, provider: provider, lastPoll: map[string]time.Time{}, now: time.Now}
}

var _ Reconciler = (*ComponentReconciler)(nil)

func (r *ComponentReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	comp, err := r.components.Get(ctx, ref.ID)
	if errors.Is(err, store.ErrNotFound) {
		delete(r.lastPoll, ref.ID)
		return r.provider.DeleteComponent(ctx, resource.Component{Metadata: resource.Metadata{ID: ref.ID}})
	}
	if err != nil {
		return err
	}

	obs, src, err := r.observe(ctx, comp)
	if err != nil {
		return err
	}
	return requeueWhen(r.setState(ctx, comp, obs, src), componentConverging(obs))
}

// observe works out comp's state: a provider observation, or a not-deployed
// outcome reached before asking the provider, and for a source Component
// what its builds say. An error means the store couldn't be read or
// written.
func (r *ComponentReconciler) observe(ctx context.Context, comp resource.Component) (ComponentObservation, sourceState, error) {
	if err := validateComponentSpec(comp.Spec); err != nil {
		return notDeployed(comp, ReasonInvalidSpec, err.Error()), sourceState{}, nil
	}
	appID, err := owningApplicationID(comp)
	if err != nil {
		return notDeployed(comp, ReasonInvalidSpec, err.Error()), sourceState{}, nil
	}

	// Builds don't need the Application, so they go on while it isn't
	// Ready.
	var src sourceState
	if comp.Spec.Source != nil {
		if r.builds == nil {
			return notDeployed(comp, ReasonUnsupported, "building from source needs the builds package"), src, nil
		}
		if src, err = r.source(ctx, comp); err != nil {
			return ComponentObservation{}, sourceState{}, err
		}
	}
	run, ok := runnable(comp, src.image)
	if !ok {
		return notDeployed(comp, ReasonWaitingForBuild, src.waiting), src, nil
	}

	app, err := r.apps.Get(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		return notDeployed(comp, ReasonWaitingForApplication,
			fmt.Sprintf("owning Application %s not found", appID)), src, nil
	}
	if err != nil {
		return ComponentObservation{}, sourceState{}, err
	}
	if c := findCondition(app.Status.Conditions, conditionReady); c == nil || c.Status != resource.ConditionTrue {
		return notDeployed(comp, ReasonWaitingForApplication,
			fmt.Sprintf("owning Application %s is not Ready yet", appID)), src, nil
	}

	obs, err := r.provider.EnsureComponent(ctx, app, run)
	if errors.Is(err, ErrProviderNotReady) {
		return notDeployed(comp, ReasonProviderNotReady, providerNotReadyMessage(applicationPlatformCapability)), src, nil
	}
	if err != nil {
		return notDeployed(comp, ReasonEnsureFailed, err.Error()), src, nil
	}
	return obs, src, nil
}

// notDeployed is a Ready=False outcome reached without the provider's
// observation. Any exposed port is then not reachable either.
func notDeployed(comp resource.Component, reason, message string) ComponentObservation {
	obs := ComponentObservation{Reason: reason, Message: message}
	if exposesAny(comp.Spec) {
		obs.Reachable = &Reachability{Reason: ReasonPending, Message: "workload is not deployed"}
	}
	return obs
}

// componentConverging reports whether obs is a state expected to change
// soon without any Serendip-side event: a rollout in progress, a route not
// yet serving a Ready workload, an owning Application that isn't Ready yet
// (its becoming Ready is an Application event, not a Component one), or a
// provider still being set up. Settled (Deployed and Serving) and terminal
// (InvalidSpec, DeployFailed, Unsupported, RouteRejected, EnsureFailed)
// outcomes ride the normal resync.
func componentConverging(obs ComponentObservation) bool {
	if !obs.Ready {
		switch obs.Reason {
		case ReasonProgressing, ReasonWaitingForApplication, ReasonProviderNotReady:
			return true
		}
		return false
	}
	return obs.Reachable != nil && !obs.Reachable.Reachable && obs.Reachable.Reason == ReasonPending
}

// setState writes the Ready and Reachable Conditions and the endpoint
// list, but only when a Condition's {Status, Reason} or the endpoint list
// actually changes -- the same idempotency rule as every other
// reconciler, extended to the new fields. Message churn alone is not a
// change. Reachable is removed, and endpoints cleared, when nothing is
// exposed; endpoints are published only while Reachable is true.
func (r *ComponentReconciler) setState(ctx context.Context, comp resource.Component, obs ComponentObservation, src sourceState) error {
	now := time.Now()
	ready := resource.Condition{Type: conditionReady, Status: readyStatus(obs.Ready), Reason: obs.Reason, Message: obs.Message, LastTransitionTime: now}

	var reach *resource.Condition
	var endpoints []resource.ComponentEndpointStatus
	if obs.Reachable != nil {
		rs := *obs.Reachable
		// A route can't serve a workload that isn't running, whatever the
		// provider reports.
		if rs.Reachable && !obs.Ready {
			rs = Reachability{Reason: ReasonPending, Message: "workload is not deployed"}
		}
		reach = &resource.Condition{Type: conditionReachable, Status: readyStatus(rs.Reachable), Reason: rs.Reason, Message: rs.Message, LastTransitionTime: now}
		if rs.Reachable {
			endpoints = rs.Endpoints
		}
	}

	if conditionUnchanged(comp.Status.Conditions, ready) &&
		reachableUnchanged(comp.Status.Conditions, reach) &&
		endpointsEqual(comp.Status.Endpoints, endpoints) &&
		optionalConditionUnchanged(comp.Status.Conditions, src.autoBuild, conditionAutoBuild) &&
		optionalConditionUnchanged(comp.Status.Conditions, src.built, conditionBuilt) &&
		comp.Status.Image == src.image {
		return nil
	}

	comp.Status.Conditions = upsertCondition(comp.Status.Conditions, keepTransitionTime(comp.Status.Conditions, ready))
	if reach != nil {
		comp.Status.Conditions = upsertCondition(comp.Status.Conditions, keepTransitionTime(comp.Status.Conditions, *reach))
	} else {
		comp.Status.Conditions = removeCondition(comp.Status.Conditions, conditionReachable)
	}
	if src.autoBuild != nil {
		comp.Status.Conditions = upsertCondition(comp.Status.Conditions, *src.autoBuild)
	} else {
		comp.Status.Conditions = removeCondition(comp.Status.Conditions, conditionAutoBuild)
	}
	if src.built != nil {
		comp.Status.Conditions = upsertCondition(comp.Status.Conditions, *src.built)
	} else {
		comp.Status.Conditions = removeCondition(comp.Status.Conditions, conditionBuilt)
	}
	comp.Status.Endpoints = endpoints
	comp.Status.Image = src.image
	_, err := r.components.Update(ctx, comp)
	return err
}

// optionalConditionUnchanged compares an optional condition of type typ (AutoBuild
// or Built), message included: their messages are Serendip's own, and
// each says something different.
func optionalConditionUnchanged(conditions []resource.Condition, want *resource.Condition, typ string) bool {
	existing := findCondition(conditions, typ)
	if want == nil || existing == nil {
		return want == nil && existing == nil
	}
	return existing.Status == want.Status && existing.Reason == want.Reason && existing.Message == want.Message
}

// keepTransitionTime preserves c's previous LastTransitionTime when its
// Status hasn't changed -- per Kubernetes conventions the time marks a
// status transition, not every rewrite (e.g. a Reason-only change, or
// another Condition changing in the same write).
func keepTransitionTime(conditions []resource.Condition, c resource.Condition) resource.Condition {
	if existing := findCondition(conditions, c.Type); existing != nil && existing.Status == c.Status {
		c.LastTransitionTime = existing.LastTransitionTime
	}
	return c
}

// ownWords are the reasons whose message Serendip writes itself from its
// own state, so a new message is news (a different Component missing, a
// different validation error) and is written. Other reasons carry the
// platform's wording, whose churn alone isn't worth a store write.
var ownWords = map[string]bool{
	ReasonWaitingForComponent:   true,
	ReasonWaitingForApplication: true,
	ReasonInvalidSpec:           true,
	ReasonUnsupported:           true,
	ReasonWaitingForBuild:       true,
}

func conditionUnchanged(conditions []resource.Condition, want resource.Condition) bool {
	existing := findCondition(conditions, want.Type)
	return existing != nil && existing.Status == want.Status && existing.Reason == want.Reason &&
		(!ownWords[want.Reason] || existing.Message == want.Message)
}

func reachableUnchanged(conditions []resource.Condition, want *resource.Condition) bool {
	if want == nil {
		return findCondition(conditions, conditionReachable) == nil
	}
	return conditionUnchanged(conditions, *want)
}

func endpointsEqual(a, b []resource.ComponentEndpointStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func removeCondition(conditions []resource.Condition, condType string) []resource.Condition {
	out := make([]resource.Condition, 0, len(conditions))
	for _, c := range conditions {
		if c.Type != condType {
			out = append(out, c)
		}
	}
	return out
}

func exposesAny(spec resource.ComponentSpec) bool {
	for _, p := range spec.Ports {
		if p.Expose {
			return true
		}
	}
	return false
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// validateComponentSpec rejects specs no provider could run, so they
// surface as InvalidSpec rather than as a provider-side failure.
func validateComponentSpec(spec resource.ComponentSpec) error {
	if err := resource.ValidateComponentSource(spec); err != nil {
		return err
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
