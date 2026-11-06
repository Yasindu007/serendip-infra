package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// A source Component's image comes from its builds (specs/022-builds plan
// decisions 6 and 7): the newest successful build of its current source.
// Until there is one it reads WaitingForBuild. Creating a source
// Component, or changing what it builds, starts a build by itself; with
// auto-build on, the branch's head is read every autoBuildPollEvery and a
// new commit starts one.

// ReasonWaitingForBuild: a source Component has no successful build of its
// current source yet.
const ReasonWaitingForBuild = "WaitingForBuild"

// conditionAutoBuild says whether auto-build can see new commits. It's
// set only while auto-build is on.
const conditionAutoBuild = "AutoBuild"

// Reasons for the AutoBuild condition.
const (
	ReasonWatching           = "Watching"
	ReasonSSHUnsupported     = "SSHUnsupported"
	ReasonNeedsCredential    = "NeedsCredential"
	ReasonRepositoryNotFound = "RepositoryNotFound"
	ReasonNoSuchBranch       = "NoSuchBranch"
	ReasonUnreachable        = "Unreachable"
	ReasonCredentialUnread   = "CredentialUnreadable"
)

// autoBuildPollEvery is how often auto-build reads a branch's head, at
// the next pass after it's due: Components resync every 30 s, so that's
// close enough.
const autoBuildPollEvery = 5 * time.Minute

// commitID is a full commit ID, as a Build's spec.commit must be.
var commitID = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// WithBuilds lets r run source Components: their images come from builds
// in builds, which provider runs and auto-build polls for, with
// credentials read through creds. Without it, a source Component reads
// Unsupported.
func (r *ComponentReconciler) WithBuilds(
	builds store.Store[resource.BuildSpec, resource.BuildStatus],
	provider BuildProvider,
	creds CredentialReader,
) *ComponentReconciler {
	r.builds, r.buildProvider, r.creds = builds, provider, creds
	return r
}

// sourceState is what a source Component's builds say about it.
type sourceState struct {
	// image is the newest successful build's image of the current source;
	// "" means none yet, and waiting says why in plain words.
	image   string
	waiting string
	// autoBuild is the AutoBuild condition to hold, nil for none.
	autoBuild *resource.Condition
}

// source works out comp's image from its builds, starting the first build
// or a newly noticed commit's.
func (r *ComponentReconciler) source(ctx context.Context, comp resource.Component) (sourceState, error) {
	cur := *comp.Spec.Source
	all, err := r.builds.List(ctx, store.ListOptions{OwnerID: comp.Metadata.ID})
	if err != nil {
		return sourceState{}, err
	}
	var forSource []resource.Build
	for _, b := range all {
		if buildsSource(b, cur) {
			forSource = append(forSource, b)
		}
	}

	if len(forSource) == 0 {
		b, err := r.createBuild(ctx, comp, "")
		if err != nil {
			return sourceState{}, err
		}
		forSource = append(forSource, b)
	}

	var st sourceState
	var newest, latest *resource.Build
	unfinished := false
	for i := range forSource {
		b := &forSource[i]
		if !b.Status.Phase.Done() {
			unfinished = true
		}
		if latest == nil || buildBefore(*latest, *b) {
			latest = b
		}
		if b.Status.Phase == resource.BuildSucceeded && b.Status.Image != "" &&
			(newest == nil || b.Status.FinishedAt.After(newest.Status.FinishedAt) ||
				(b.Status.FinishedAt.Equal(newest.Status.FinishedAt) && buildBefore(*newest, *b))) {
			newest = b
		}
	}
	if newest != nil {
		st.image = newest.Status.Image
	} else {
		st.waiting = waitingMessage(*latest)
	}

	if cur.AutoBuild {
		cond, err := r.autoBuild(ctx, comp, forSource, unfinished)
		if err != nil {
			return sourceState{}, err
		}
		st.autoBuild = cond
	} else {
		delete(r.lastPoll, comp.Metadata.ID)
	}
	return st, nil
}

// buildsSource reports whether b builds cur: it recorded the same source,
// or hasn't recorded one yet and will take the Component's current one.
func buildsSource(b resource.Build, cur resource.ComponentSource) bool {
	if b.Status.Source == nil {
		return !b.Status.Phase.Done()
	}
	return b.Status.Source.SameBuild(cur)
}

func waitingMessage(latest resource.Build) string {
	why := ""
	if c := findCondition(latest.Status.Conditions, conditionReady); c != nil && c.Message != "" {
		why = ": " + c.Message
	}
	switch latest.Status.Phase {
	case resource.BuildFailed:
		return fmt.Sprintf("the last build, %s, failed%s; start a new build", latest.Metadata.ID, why)
	case resource.BuildCancelled:
		return fmt.Sprintf("the last build, %s, was cancelled; start a new build", latest.Metadata.ID)
	case resource.BuildSucceeded:
		return fmt.Sprintf("the last build, %s, produced no image; start a new build", latest.Metadata.ID)
	default:
		phase := latest.Status.Phase
		if phase == "" {
			phase = resource.BuildQueued
		}
		return fmt.Sprintf("waiting for build %s (%s)", latest.Metadata.ID, phase)
	}
}

// autoBuild reads the branch's head when it's due, starting a build of a
// new commit, and returns the AutoBuild condition to hold. While a build
// of the source is unfinished it doesn't read: that build is the newest
// anyway, and only one runs at a time.
func (r *ComponentReconciler) autoBuild(ctx context.Context, comp resource.Component, forSource []resource.Build, unfinished bool) (*resource.Condition, error) {
	src := comp.Spec.Source.WithDefaults()
	existing := findCondition(comp.Status.Conditions, conditionAutoBuild)
	now := r.now().UTC()
	cond := func(status resource.ConditionStatus, reason, message string) *resource.Condition {
		c := keepTransitionTime(comp.Status.Conditions, resource.Condition{
			Type: conditionAutoBuild, Status: status, Reason: reason, Message: message, LastTransitionTime: now,
		})
		return &c
	}
	if src.SSH() {
		return cond(resource.ConditionFalse, ReasonSSHUnsupported, "auto-build can't see new commits for SSH URLs; use HTTPS"), nil
	}
	watching := cond(resource.ConditionTrue, ReasonWatching, fmt.Sprintf("watching %s for new commits", src.Branch))
	keep := func() *resource.Condition {
		if existing != nil {
			c := *existing
			return &c
		}
		return watching
	}
	if last, ok := r.lastPoll[comp.Metadata.ID]; unfinished || (ok && now.Sub(last) < autoBuildPollEvery) {
		return keep(), nil
	}
	r.lastPoll[comp.Metadata.ID] = now

	var cred map[string]any
	if id := src.CredentialSecret; id != "" {
		var err error
		cred, err = r.creds.ReadValue(ctx, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return cond(resource.ConditionFalse, ReasonNeedsCredential, fmt.Sprintf("auto-build can't read the repository: the credential Secret %s doesn't exist", id)), nil
		case errors.Is(err, ErrProviderNotReady):
			delete(r.lastPoll, comp.Metadata.ID) // try again at the next pass
			return keep(), nil
		case err != nil:
			log.Printf("reconcile: component %s: auto-build: read credential %s: %v", comp.Metadata.ID, id, err)
			return cond(resource.ConditionFalse, ReasonCredentialUnread, fmt.Sprintf("auto-build couldn't read the credential Secret %s; retrying", id)), nil
		}
	}

	head, err := r.buildProvider.HeadCommit(ctx, src, cred)
	switch {
	case errors.Is(err, ErrProviderNotReady):
		delete(r.lastPoll, comp.Metadata.ID)
		return keep(), nil
	case errors.Is(err, ErrSourceSSH):
		return cond(resource.ConditionFalse, ReasonSSHUnsupported, "auto-build can't see new commits for SSH URLs; use HTTPS"), nil
	case errors.Is(err, ErrSourceAuth):
		return cond(resource.ConditionFalse, ReasonNeedsCredential, "auto-build can't read the repository: it needs a credential, refused the one given, or doesn't exist"), nil
	case errors.Is(err, ErrSourceNotFound):
		return cond(resource.ConditionFalse, ReasonRepositoryNotFound, "auto-build can't read the repository: it doesn't exist"), nil
	case errors.Is(err, ErrSourceNoBranch):
		return cond(resource.ConditionFalse, ReasonNoSuchBranch, fmt.Sprintf("auto-build can't see new commits: the repository has no branch %s", src.Branch)), nil
	case err != nil:
		log.Printf("reconcile: component %s: auto-build: read head: %v", comp.Metadata.ID, err)
		return r.unreachable(comp, now), nil
	}

	if !commitID.MatchString(head) {
		// A Build at this commit would never validate: don't make one.
		log.Printf("reconcile: component %s: auto-build: the provider returned a malformed head %q", comp.Metadata.ID, head)
		return r.unreachable(comp, now), nil
	}
	if !builtCommit(forSource, head) {
		if _, err := r.createBuild(ctx, comp, head); err != nil {
			delete(r.lastPoll, comp.Metadata.ID) // retried at once, not in 5 minutes
			return nil, err
		}
	}
	return watching, nil
}

// unreachable is the condition for a poll that failed, dated from the
// first failure in a row.
func (r *ComponentReconciler) unreachable(comp resource.Component, now time.Time) *resource.Condition {
	if c := findCondition(comp.Status.Conditions, conditionAutoBuild); c != nil && c.Reason == ReasonUnreachable {
		kept := *c
		return &kept
	}
	return &resource.Condition{Type: conditionAutoBuild, Status: resource.ConditionFalse, Reason: ReasonUnreachable,
		Message: "auto-build couldn't reach the repository since " + now.Format(time.RFC3339), LastTransitionTime: now}
}

// builtCommit reports whether any of the Component's builds asked for, or
// built, commit, whatever its outcome: a failed commit isn't retried by
// itself.
func builtCommit(all []resource.Build, commit string) bool {
	for _, b := range all {
		if b.Spec.Commit == commit || b.Status.Commit == commit {
			return true
		}
	}
	return false
}

// createBuild creates a Build of comp, at commit or (empty) the branch's
// head, attributed to auto-build.
func (r *ComponentReconciler) createBuild(ctx context.Context, comp resource.Component, commit string) (resource.Build, error) {
	name := "auto-build"
	if commit != "" {
		name += "-" + commit[:12]
	}
	b, err := r.builds.Create(audit.WithActor(ctx, AutoBuildActor), resource.Build{
		Metadata: resource.Metadata{Name: name, Kind: resource.KindBuild,
			OwnerRefs: []resource.OwnerRef{{Kind: resource.KindComponent, ID: comp.Metadata.ID}}},
		Spec: resource.BuildSpec{Commit: commit},
	})
	if err != nil {
		return resource.Build{}, fmt.Errorf("create build of %s: %w", comp.Metadata.ID, err)
	}
	return b, nil
}

// runnable is comp as a provider runs it: a source Component with its
// built image in place of its source, since providers run images. ok is
// false when it has none yet.
func runnable(comp resource.Component, image string) (resource.Component, bool) {
	if comp.Spec.Source == nil {
		return comp, true
	}
	if image == "" {
		return comp, false
	}
	comp.Spec.Image, comp.Spec.Source = image, nil
	return comp, true
}
