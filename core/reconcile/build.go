package reconcile

import (
	"context"
	"time"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// BuildObservation is what the build capability reports for one Build's
// run.
type BuildObservation struct {
	Phase resource.BuildPhase
	// Message says why a run failed, in the provider's own plain words.
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
	// HeadCommit is the full commit ID at the head of src's branch.
	HeadCommit(ctx context.Context, src resource.ComponentSource, cred map[string]any) (string, error)
}
