package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// BuildObservation is what the build capability reports for one Build's
// run.
type BuildObservation struct {
	Phase resource.BuildPhase
	// Message says why a run failed, in the provider's own plain words.
	// It and each step's message are stored in the Build's status, which
	// the API returns: a provider never puts log output or a credential
	// in them.
	Message string
	// Commit is the commit checked out, and Image the reference pushed,
	// once each step has produced them.
	Commit     string
	Image      string
	StartedAt  time.Time
	FinishedAt time.Time
	Steps      []resource.BuildStep
	// Gone means the platform has no run for the Build: it expired after
	// finishing, or was never started.
	Gone bool
}

// BuildProvider builds Components' source into images (specs/022-builds
// plan decision 10). It's registered under the "build" capability.
type BuildProvider interface {
	// StartBuild starts b's run for comp. cred is the repository's
	// credential, read from the Secrets capability, or nil for a public
	// one. Starting a run that already exists is no error.
	StartBuild(ctx context.Context, app resource.Application, comp resource.Component, b resource.Build, cred map[string]any) error
	ObserveBuild(ctx context.Context, b resource.Build) (BuildObservation, error)
	// CancelBuild stops b's run. It's idempotent.
	CancelBuild(ctx context.Context, b resource.Build) error
	// RemoveBuild removes b's run and its copy of the credential. It's
	// idempotent.
	RemoveBuild(ctx context.Context, b resource.Build) error
	BuildLogs(ctx context.Context, b resource.Build, opts LogOptions) (LogStream, error)
	BuildEvents(ctx context.Context, b resource.Build, since time.Time) ([]RuntimeEvent, error)
	// HeadCommit is the full commit ID at the head of src's branch. Its
	// errors wrap ErrSourceSSH, ErrSourceAuth, ErrSourceNotFound or
	// ErrSourceNoBranch where one applies; any other error means the
	// repository couldn't be reached.
	HeadCommit(ctx context.Context, src resource.ComponentSource, cred map[string]any) (string, error)
}

// Errors a BuildProvider's HeadCommit wraps, so auto-build can say why it
// can't see new commits without knowing how the provider reads them.
var (
	// ErrSourceSSH: the repository is reached over SSH, which can't be
	// polled.
	ErrSourceSSH = errors.New("reconcile: source: SSH URLs can't be polled")
	// ErrSourceAuth: the repository needs a credential, refused the one
	// given, or doesn't exist (some hosts answer the same for all three).
	ErrSourceAuth = errors.New("reconcile: source: credential needed or refused")
	// ErrSourceNotFound: the repository doesn't exist.
	ErrSourceNotFound = errors.New("reconcile: source: repository not found")
	// ErrSourceNoBranch: the repository has no such branch.
	ErrSourceNoBranch = errors.New("reconcile: source: no such branch")
)

// CredentialReader reads a Secret's value by the Secret's ID, through the
// Secrets capability (SecretReconciler.ReadValue does). A Secret that
// doesn't exist is store.ErrNotFound.
type CredentialReader interface {
	ReadValue(ctx context.Context, id string) (map[string]any, error)
}

// buildCapability names the capability in not-ready messages.
const buildCapability = "build"

// AutoBuildActor is who the builds Serendip starts by itself are
// attributed to: a source Component's first build, and each new commit
// auto-build notices (plan decisions 6 and 7).
const AutoBuildActor = "system:auto-build"

// Reasons on a Build's Ready condition beyond its phases.
const (
	ReasonStartFailed    = "StartFailed"
	ReasonWaitingForTurn = "WaitingForTurn"
)

// BuildReconciler runs Builds through a BuildProvider (plan decision 5):
// one run at a time per Component, oldest first; the credential read
// through the Secrets capability; cancel from spec.cancel. A status is
// written only when it changes. A finished Build re-queues its Component,
// which picks up the new image, and the Component's queued Builds.
//
// A finished Build isn't looked at again: its run expires on the platform
// an hour after it ends, and its record keeps the outcome. Runs are
// removed by the approval's clean-up when the Component goes
// (BuildCleanup).
type BuildReconciler struct {
	builds   store.Store[resource.BuildSpec, resource.BuildStatus]
	comps    store.Store[resource.ComponentSpec, resource.ComponentStatus]
	apps     store.Store[resource.ApplicationSpec, resource.ApplicationStatus]
	provider BuildProvider
	creds    CredentialReader
	// enqueue asks for ref to be reconciled soon (Engine.Enqueue); nil
	// leaves it to the next resync.
	enqueue func(graph.ResourceRef)
	now     func() time.Time
}

func NewBuildReconciler(
	builds store.Store[resource.BuildSpec, resource.BuildStatus],
	comps store.Store[resource.ComponentSpec, resource.ComponentStatus],
	apps store.Store[resource.ApplicationSpec, resource.ApplicationStatus],
	provider BuildProvider,
	creds CredentialReader,
	enqueue func(graph.ResourceRef),
) *BuildReconciler {
	return &BuildReconciler{builds: builds, comps: comps, apps: apps, provider: provider, creds: creds, enqueue: enqueue, now: time.Now}
}

var _ Reconciler = (*BuildReconciler)(nil)

func (r *BuildReconciler) Reconcile(ctx context.Context, ref graph.ResourceRef) error {
	b, err := r.builds.Get(ctx, ref.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // its run goes with the approval's clean-up
	}
	if err != nil {
		return err
	}
	if b.Status.Phase.Done() {
		return nil
	}
	st := &buildState{s: b.Status, now: r.now}
	if st.s.Phase == "" {
		st.s.Phase = resource.BuildQueued
	}
	runErr := r.step(ctx, b, st)
	// skip: the Component couldn't be read (runErr says why, or it's being
	// removed), so there's nothing to write.
	if st.skip {
		return runErr
	}
	stored, err := r.write(ctx, b, st.s)
	if err != nil {
		return err
	}
	if !stored {
		// Edited meanwhile (a cancel): look again from the stored state.
		return RequeueAfter{}
	}
	if runErr != nil {
		return runErr
	}
	if st.s.Phase.Done() {
		r.finished(ctx, b)
	}
	return requeueWhen(nil, st.requeue)
}

// buildState is the status one pass works out.
type buildState struct {
	s       resource.BuildStatus
	requeue bool // an early re-check is wanted
	skip    bool // nothing to write
	now     func() time.Time
}

func (st *buildState) ready(reason, message string) {
	st.s.Conditions = setCondition(st.s.Conditions, conditionReady, readyStatus(st.s.Phase == resource.BuildSucceeded), reason, message, st.now())
}

func (st *buildState) finish(phase resource.BuildPhase, message string) {
	st.s.Phase = phase
	if st.s.FinishedAt.IsZero() {
		st.s.FinishedAt = st.now().UTC()
	}
	st.requeue = false
	st.ready(string(phase), message)
}

func (st *buildState) notReady(capability string) {
	st.requeue = true
	st.ready(ReasonProviderNotReady, providerNotReadyMessage(capability))
}

// step moves b on by one pass into st. An error is returned after st is
// written.
func (r *BuildReconciler) step(ctx context.Context, b resource.Build, st *buildState) error {
	if err := resource.ValidateBuild(b.Metadata, b.Spec); err != nil {
		st.finish(resource.BuildFailed, err.Error())
		return nil
	}
	compID := resource.BuildComponent(b.Metadata)
	comp, err := r.comps.Get(ctx, compID)
	if errors.Is(err, store.ErrNotFound) {
		// The Component is being removed, and its Builds with it.
		st.skip = true
		return nil
	}
	if err != nil {
		st.skip = true
		return err
	}
	if st.s.Source == nil {
		if comp.Spec.Source == nil {
			st.finish(resource.BuildFailed, fmt.Sprintf("Component %s doesn't build from source", compID))
			return nil
		}
		src := *comp.Spec.Source
		src.AutoBuild = false
		st.s.Source = &src
	}

	if b.Spec.Cancel {
		// Cancel even when Started isn't recorded: a run may have started in
		// a pass whose status write lost to the cancel's edit. Cancelling a
		// run that doesn't exist is no error.
		err := r.provider.CancelBuild(ctx, b)
		if errors.Is(err, ErrProviderNotReady) {
			st.notReady(buildCapability)
			return nil
		}
		if err != nil {
			return fmt.Errorf("cancel build: %w", err)
		}
		st.finish(resource.BuildCancelled, "cancelled")
		// The run is gone, so no later read moves its steps on: one that
		// hadn't finished was cancelled with it (specs/022-builds T014 U2).
		// A copy: st.s.Steps still shares b.Status's array.
		st.s.Steps = slices.Clone(st.s.Steps)
		for i, s := range st.s.Steps {
			if !s.Phase.Done() {
				st.s.Steps[i].Phase, st.s.Steps[i].FinishedAt = resource.BuildCancelled, st.s.FinishedAt
			}
		}
		return nil
	}

	if !st.s.Started {
		ahead, err := r.ahead(ctx, b)
		if err != nil {
			return err
		}
		if ahead != "" {
			st.ready(ReasonWaitingForTurn, fmt.Sprintf("waiting for build %s to finish: one build at a time per Component", ahead))
			return nil
		}
		if started, err := r.start(ctx, b, comp, st); !started {
			return err
		}
		st.s.Started = true
	}
	return r.observe(ctx, b, st)
}

// start starts b's run, reporting whether it started; when it didn't, st
// says why.
func (r *BuildReconciler) start(ctx context.Context, b resource.Build, comp resource.Component, st *buildState) (bool, error) {
	appID, err := owningApplicationID(comp)
	if err != nil {
		st.finish(resource.BuildFailed, err.Error())
		return false, nil
	}
	app, err := r.apps.Get(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		st.ready(ReasonWaitingForApplication, fmt.Sprintf("owning Application %s not found", appID))
		return false, nil
	}
	if err != nil {
		return false, err
	}

	var cred map[string]any
	if id := st.s.Source.CredentialSecret; id != "" {
		cred, err = r.creds.ReadValue(ctx, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			st.finish(resource.BuildFailed, fmt.Sprintf("the credential Secret %s doesn't exist", id))
			return false, nil
		case errors.Is(err, ErrProviderNotReady):
			st.notReady("secrets")
			return false, nil
		case err != nil:
			// The cause goes to the log only: a secrets backend's error text
			// doesn't belong in a status the API returns.
			st.ready(ReasonStartFailed, fmt.Sprintf("couldn't read the credential Secret %s; retrying", id))
			return false, fmt.Errorf("read credential Secret %s: %w", id, err)
		}
	}

	// The provider builds the Build's own source, not the Component's
	// current one.
	comp.Spec.Source = st.s.Source
	err = r.provider.StartBuild(ctx, app, comp, b, cred)
	if errors.Is(err, ErrProviderNotReady) {
		st.notReady(buildCapability)
		return false, nil
	}
	if err != nil {
		// Retried each pass, with no cap: the Build reads StartFailed and
		// holds its Component's queue until it starts or is cancelled.
		// The credential copy is made before the run, so a failed start may
		// have left one: remove it rather than keep it until a retry.
		if rmErr := r.provider.RemoveBuild(ctx, b); rmErr != nil {
			log.Printf("reconcile: build %s: remove after a failed start: %v", b.Metadata.ID, rmErr)
		}
		st.ready(ReasonStartFailed, "the build couldn't be started; retrying")
		return false, fmt.Errorf("start build: %w", err)
	}
	return true, nil
}

// observe copies the run's state into st.
func (r *BuildReconciler) observe(ctx context.Context, b resource.Build, st *buildState) error {
	obs, err := r.provider.ObserveBuild(ctx, b)
	if errors.Is(err, ErrProviderNotReady) {
		st.notReady(buildCapability)
		return nil
	}
	if err != nil {
		return fmt.Errorf("observe build: %w", err)
	}
	if obs.Gone {
		st.finish(resource.BuildFailed, "the build's run disappeared from the platform before it finished")
		return nil
	}
	// A value once seen is kept if a later read lacks it.
	if obs.Commit != "" {
		st.s.Commit = obs.Commit
	}
	if obs.Image != "" {
		st.s.Image = obs.Image
	}
	if len(obs.Steps) > 0 {
		st.s.Steps = obs.Steps
	}
	if !obs.StartedAt.IsZero() {
		st.s.StartedAt = obs.StartedAt
	}
	if !obs.FinishedAt.IsZero() {
		st.s.FinishedAt = obs.FinishedAt
	}
	switch obs.Phase {
	case resource.BuildSucceeded, resource.BuildFailed, resource.BuildCancelled:
		st.finish(obs.Phase, obs.Message)
		return nil
	case resource.BuildRunning:
		st.s.Phase = resource.BuildRunning
	default:
		st.s.Phase = resource.BuildQueued
	}
	st.requeue = true
	st.ready(string(st.s.Phase), obs.Message)
	return nil
}

// ahead names the build b waits behind: one of its Component's builds
// that's started, or queued before it. "" means it's b's turn.
func (r *BuildReconciler) ahead(ctx context.Context, b resource.Build) (string, error) {
	others, err := r.builds.List(ctx, store.ListOptions{OwnerID: resource.BuildComponent(b.Metadata)})
	if err != nil {
		return "", err
	}
	for _, o := range others {
		if o.Metadata.ID == b.Metadata.ID || o.Status.Phase.Done() || resource.BuildComponent(o.Metadata) == "" {
			continue
		}
		if o.Status.Started || buildBefore(o, b) {
			return o.Metadata.ID, nil
		}
	}
	return "", nil
}

// buildBefore orders builds by creation, then ID.
func buildBefore(a, b resource.Build) bool {
	if !a.Metadata.CreatedAt.Equal(b.Metadata.CreatedAt) {
		return a.Metadata.CreatedAt.Before(b.Metadata.CreatedAt)
	}
	return a.Metadata.ID < b.Metadata.ID
}

// finished re-queues what a finished build unblocks: its Component (a new
// image, or a message to update) and the Component's other unfinished
// builds.
func (r *BuildReconciler) finished(ctx context.Context, b resource.Build) {
	if r.enqueue == nil {
		return
	}
	compID := resource.BuildComponent(b.Metadata)
	r.enqueue(graph.ResourceRef{Kind: resource.KindComponent, ID: compID})
	others, err := r.builds.List(ctx, store.ListOptions{OwnerID: compID})
	if err != nil {
		return // resync picks them up
	}
	for _, o := range others {
		if o.Metadata.ID != b.Metadata.ID && !o.Status.Phase.Done() {
			r.enqueue(graph.ResourceRef{Kind: resource.KindBuild, ID: o.Metadata.ID})
		}
	}
}

// write stores next, only when it differs from b's status. stored is
// false when the Build was edited meanwhile (a cancel), and next was
// dropped: the next pass goes on from the stored state, and starting
// again is safe, since starting a run that exists is no error.
func (r *BuildReconciler) write(ctx context.Context, b resource.Build, next resource.BuildStatus) (stored bool, err error) {
	if buildStatusEqual(b.Status, next) {
		return true, nil
	}
	b.Status = next
	_, err = r.builds.Update(ctx, b)
	if errors.Is(err, store.ErrConflict) {
		return false, nil
	}
	return err == nil, err
}

// buildStatusEqual compares two statuses, with times compared as
// instants and a condition's transition time ignored.
func buildStatusEqual(a, b resource.BuildStatus) bool {
	if a.Phase != b.Phase || a.Commit != b.Commit || a.Image != b.Image || a.Started != b.Started ||
		!a.StartedAt.Equal(b.StartedAt) || !a.FinishedAt.Equal(b.FinishedAt) ||
		(a.Source == nil) != (b.Source == nil) || (a.Source != nil && *a.Source != *b.Source) ||
		len(a.Steps) != len(b.Steps) || len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for i := range a.Steps {
		x, y := a.Steps[i], b.Steps[i]
		if x.Name != y.Name || x.Phase != y.Phase || x.Message != y.Message || !x.StartedAt.Equal(y.StartedAt) || !x.FinishedAt.Equal(y.FinishedAt) {
			return false
		}
	}
	for i := range a.Conditions {
		x, y := a.Conditions[i], b.Conditions[i]
		if x.Type != y.Type || x.Status != y.Status || x.Reason != y.Reason || x.Message != y.Message {
			return false
		}
	}
	return true
}

// setCondition upserts a condition, keeping its transition time while its
// status holds.
func setCondition(conditions []resource.Condition, typ string, status resource.ConditionStatus, reason, message string, now time.Time) []resource.Condition {
	c := resource.Condition{Type: typ, Status: status, Reason: reason, Message: message, LastTransitionTime: now.UTC()}
	return upsertCondition(conditions, keepTransitionTime(conditions, c))
}

// BuildCleanup removes a removed Build's run and its credential copy, for
// the approval reconciler: Builds go with their Component's delete.
func BuildCleanup(provider BuildProvider) CleanupFunc {
	return func(ctx context.Context, ref resource.ApprovalRef) error {
		return provider.RemoveBuild(ctx, resource.Build{Metadata: resource.Metadata{ID: ref.ID, Name: ref.Name, Kind: resource.KindBuild}})
	}
}
