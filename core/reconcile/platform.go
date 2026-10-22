package reconcile

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// The Application Platform capability beyond running Components in one
// place (specs/020-openchoreo-console-features): Environments and their
// promotion order, releases, Deployments, the runtime view, and
// configuration schemas. Each is its own interface, so a provider offers
// what it can and the API reports the rest as unavailable. They speak in
// Serendip's terms; no platform's names appear here (constitution 1).

// Release is one immutable snapshot of a Component's workload that can be
// deployed. Names are the platform's.
type Release struct {
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	// Image is the container image the release runs, when the platform
	// says.
	Image string `json:"image,omitempty"`
}

// DeploymentObservation is what the platform reports for a Deployment.
type DeploymentObservation struct {
	// Release is the release actually running.
	Release   string
	Ready     bool
	Reason    string
	Message   string
	Endpoints []resource.ComponentEndpointStatus
}

// DeploymentProvider runs Components in Environments.
type DeploymentProvider interface {
	// EnsureEnvironment makes env exist on the platform.
	EnsureEnvironment(ctx context.Context, env resource.Environment) error
	// DeleteEnvironment removes it. The API refuses while Deployments
	// remain.
	DeleteEnvironment(ctx context.Context, env resource.Environment) error
	// EnsurePipeline makes project's promotion order exist on the platform
	// (the platform doesn't enforce it; Serendip does).
	EnsurePipeline(ctx context.Context, project resource.Project) error
	// Releases lists comp's releases, newest first.
	Releases(ctx context.Context, comp resource.Component) ([]Release, error)
	// CreateRelease snapshots comp's current workload as a release.
	CreateRelease(ctx context.Context, comp resource.Component) (Release, error)
	// Deploy makes d run: comp, of app, in env, with d's release and
	// overrides. It's the same call for a first deploy, a promotion and a
	// rollback.
	Deploy(ctx context.Context, app resource.Application, comp resource.Component, env resource.Environment, d resource.Deployment) (DeploymentObservation, error)
	// Undeploy stops d running. Only its ID and spec are needed, since the
	// Serendip record may already be gone.
	Undeploy(ctx context.Context, d resource.Deployment, env resource.Environment) error
}

// RuntimeObject is one object a Deployment runs as on the platform.
type RuntimeObject struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Health is the platform's summary: Healthy, Progressing, Degraded,
	// Suspended or Unknown.
	Health  string `json:"health"`
	Message string `json:"message,omitempty"`
}

// RuntimeEvent is a recent event about one of those objects.
type RuntimeEvent struct {
	Time       time.Time `json:"time"`
	Type       string    `json:"type"` // Normal or Warning
	Reason     string    `json:"reason"`
	Message    string    `json:"message"`
	ObjectKind string    `json:"objectKind"`
	ObjectName string    `json:"objectName"`
	Count      int       `json:"count,omitempty"`
}

// LogOptions select a Deployment's logs.
type LogOptions struct {
	// Follow keeps the stream open for new lines until ctx ends.
	Follow bool
	// TailLines is how many recent lines to start with; zero means a
	// provider default.
	TailLines int
	// Since limits lines to those newer than now minus Since.
	Since time.Duration
	// Previous reads the previous instance of a restarted container.
	Previous bool
}

// LogLine is one line of a Deployment's logs.
type LogLine struct {
	Time time.Time `json:"time"`
	// Instance is which running copy of the workload wrote it.
	Instance string `json:"instance"`
	Line     string `json:"line"`
}

// LogStream yields log lines until it ends or is closed.
type LogStream interface {
	// Next returns the next line, or io.EOF when the stream ends.
	Next() (LogLine, error)
	io.Closer
}

// RuntimeProvider reads what a Deployment is running.
type RuntimeProvider interface {
	RuntimeObjects(ctx context.Context, d resource.Deployment, env resource.Environment) ([]RuntimeObject, error)
	Events(ctx context.Context, d resource.Deployment, env resource.Environment, since time.Time) ([]RuntimeEvent, error)
	Logs(ctx context.Context, d resource.Deployment, env resource.Environment, opts LogOptions) (LogStream, error)
}

// ConfigSchemas are the JSON Schemas for what can be configured on a
// Component: its per-environment configuration (DeploymentOverrides.Config)
// and each trait's (DeploymentOverrides.Traits), by trait instance name.
type ConfigSchemas struct {
	Environment json.RawMessage            `json:"environment,omitempty"`
	Traits      map[string]json.RawMessage `json:"traits,omitempty"`
}

// ConfigSchemaProvider reports what can be configured on a Component.
type ConfigSchemaProvider interface {
	ConfigSchemas(ctx context.Context, comp resource.Component) (ConfigSchemas, error)
}

// PlatformView is the read-only description of the platform: where
// workloads run, and the kinds of Component and trait available.
type PlatformView struct {
	Runtimes       []PlatformRuntime   `json:"runtimes"`
	ComponentTypes []PlatformComponent `json:"componentTypes"`
	Traits         []PlatformComponent `json:"traits"`
}

// PlatformRuntime is a place workloads run.
type PlatformRuntime struct {
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	Message string `json:"message,omitempty"`
}

// PlatformComponent is a component type or trait, with its schemas.
type PlatformComponent struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Environment json.RawMessage `json:"environment,omitempty"`
}

// PlatformViewProvider describes the platform.
type PlatformViewProvider interface {
	Platform(ctx context.Context) (PlatformView, error)
}
