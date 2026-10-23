package main

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// lazy holds a capability provider that background setup stores once it
// succeeds. Until then get returns reconcile.ErrProviderNotReady, which
// reconcilers report as an honest "still being set up" Condition. This is
// what lets reconcilers (and the API) be wired at startup, before any
// cluster-facing setup has finished (specs/014-reconciliation-hardening
// plan.md decision 4). Lives in the composition root so neither core/ nor
// providers/ change shape.
type lazy[T any] struct {
	p atomic.Pointer[T]
}

func (l *lazy[T]) set(v T) { l.p.Store(&v) }

func (l *lazy[T]) get() (T, error) {
	p := l.p.Load()
	if p == nil {
		var zero T
		return zero, reconcile.ErrProviderNotReady
	}
	return *p, nil
}

// lazyAuthenticator answers reconcile.ErrProviderNotReady until identity
// setup has handed it a verifier, which the API reports as 503 for bearer
// tokens it can't check yet (specs/015-identity-foundation plan.md
// decision 11). API keys never reach it.
type lazyAuthenticator struct {
	lazy[identity.Authenticator]
}

var _ identity.Authenticator = (*lazyAuthenticator)(nil)

func (l *lazyAuthenticator) Authenticate(ctx context.Context, token string) (identity.Principal, error) {
	a, err := l.get()
	if err != nil {
		return identity.Principal{}, err
	}
	return a.Authenticate(ctx, token)
}

// lazyDirectory answers reconcile.ErrProviderNotReady until identity setup
// has handed it the provider's directory, so callers fall back to the
// principals they have seen sign in (specs/018-agent-manager-poc plan.md
// decision 5).
type lazyDirectory struct {
	lazy[identity.Directory]
}

var _ identity.Directory = (*lazyDirectory)(nil)

func (l *lazyDirectory) People(ctx context.Context, query string) (identity.DirectoryResult, error) {
	d, err := l.get()
	if err != nil {
		return identity.DirectoryResult{}, err
	}
	return d.People(ctx, query)
}

func (l *lazyDirectory) Groups(ctx context.Context, query string) (identity.DirectoryResult, error) {
	d, err := l.get()
	if err != nil {
		return identity.DirectoryResult{}, err
	}
	return d.Groups(ctx, query)
}

func (l *lazyDirectory) GroupsOf(ctx context.Context, subject string) ([]string, error) {
	d, err := l.get()
	if err != nil {
		return nil, err
	}
	return d.GroupsOf(ctx, subject)
}

type lazyStorage struct {
	lazy[reconcile.StorageProvider]
}

var _ reconcile.StorageProvider = (*lazyStorage)(nil)

func (l *lazyStorage) Ensure(ctx context.Context, vol resource.Volume) (bool, string, string, error) {
	p, err := l.get()
	if err != nil {
		return false, "", "", err
	}
	return p.Ensure(ctx, vol)
}

func (l *lazyStorage) Delete(ctx context.Context, vol resource.Volume) error {
	p, err := l.get()
	if err != nil {
		return err
	}
	return p.Delete(ctx, vol)
}

type lazySecrets struct {
	lazy[reconcile.SecretsProvider]
}

var _ reconcile.SecretsProvider = (*lazySecrets)(nil)

func (l *lazySecrets) Exists(ctx context.Context, sec resource.Secret) (bool, error) {
	p, err := l.get()
	if err != nil {
		return false, err
	}
	return p.Exists(ctx, sec)
}

func (l *lazySecrets) WriteValue(ctx context.Context, sec resource.Secret, value map[string]any) error {
	p, err := l.get()
	if err != nil {
		return err
	}
	return p.WriteValue(ctx, sec, value)
}

func (l *lazySecrets) ReadValue(ctx context.Context, sec resource.Secret) (map[string]any, error) {
	p, err := l.get()
	if err != nil {
		return nil, err
	}
	return p.ReadValue(ctx, sec)
}

func (l *lazySecrets) Delete(ctx context.Context, sec resource.Secret) error {
	p, err := l.get()
	if err != nil {
		return err
	}
	return p.Delete(ctx, sec)
}

type lazyPlatform struct {
	lazy[reconcile.ApplicationPlatformProvider]
}

var _ reconcile.ApplicationPlatformProvider = (*lazyPlatform)(nil)

func (l *lazyPlatform) EnsureApplication(ctx context.Context, app resource.Application) (bool, string, string, error) {
	p, err := l.get()
	if err != nil {
		return false, "", "", err
	}
	return p.EnsureApplication(ctx, app)
}

func (l *lazyPlatform) DeleteApplication(ctx context.Context, app resource.Application) error {
	p, err := l.get()
	if err != nil {
		return err
	}
	return p.DeleteApplication(ctx, app)
}

func (l *lazyPlatform) EnsureComponent(ctx context.Context, app resource.Application, comp resource.Component) (reconcile.ComponentObservation, error) {
	p, err := l.get()
	if err != nil {
		return reconcile.ComponentObservation{}, err
	}
	return p.EnsureComponent(ctx, app, comp)
}

func (l *lazyPlatform) DeleteComponent(ctx context.Context, comp resource.Component) error {
	p, err := l.get()
	if err != nil {
		return err
	}
	return p.DeleteComponent(ctx, comp)
}

// lazyLogin holds the console's login client once identity setup has made
// sure the provider accepts the console's redirect URIs. Until then the
// console reports the identity provider as unavailable rather than sending
// people to a login that would reject them.
type lazyLogin struct{ lazy[identity.LoginClient] }

// client returns the login client, or nil while it isn't ready.
func (l *lazyLogin) client() identity.LoginClient {
	lc, err := l.get()
	if err != nil {
		return nil
	}
	return lc
}

// lazyDeployments is the platform's DeploymentProvider (spec 020), set
// with lazyPlatform when the platform is ready.
type lazyDeployments struct {
	lazy[reconcile.DeploymentProvider]
}

var _ reconcile.DeploymentProvider = (*lazyDeployments)(nil)

func (l *lazyDeployments) EnsureEnvironment(ctx context.Context, env resource.Environment) error {
	p, err := l.get()
	if err != nil {
		return err
	}
	return p.EnsureEnvironment(ctx, env)
}

func (l *lazyDeployments) DeleteEnvironment(ctx context.Context, env resource.Environment) error {
	p, err := l.get()
	if err != nil {
		return err
	}
	return p.DeleteEnvironment(ctx, env)
}

func (l *lazyDeployments) EnsurePipeline(ctx context.Context, project resource.Project) error {
	p, err := l.get()
	if err != nil {
		return err
	}
	return p.EnsurePipeline(ctx, project)
}

func (l *lazyDeployments) DeletePipeline(ctx context.Context, projectID string) error {
	p, err := l.get()
	if err != nil {
		return err
	}
	return p.DeletePipeline(ctx, projectID)
}

func (l *lazyDeployments) Releases(ctx context.Context, comp resource.Component) ([]reconcile.Release, error) {
	p, err := l.get()
	if err != nil {
		return nil, err
	}
	return p.Releases(ctx, comp)
}

func (l *lazyDeployments) CreateRelease(ctx context.Context, comp resource.Component) (reconcile.Release, error) {
	p, err := l.get()
	if err != nil {
		return reconcile.Release{}, err
	}
	return p.CreateRelease(ctx, comp)
}

func (l *lazyDeployments) Deploy(ctx context.Context, app resource.Application, comp resource.Component, env resource.Environment, d resource.Deployment) (reconcile.DeploymentObservation, error) {
	p, err := l.get()
	if err != nil {
		return reconcile.DeploymentObservation{}, err
	}
	return p.Deploy(ctx, app, comp, env, d)
}

func (l *lazyDeployments) Undeploy(ctx context.Context, d resource.Deployment) error {
	p, err := l.get()
	if err != nil {
		return err
	}
	return p.Undeploy(ctx, d)
}

// lazyRuntime is the platform's RuntimeProvider (spec 020), set with
// lazyPlatform when the platform is ready.
type lazyRuntime struct {
	lazy[reconcile.RuntimeProvider]
}

var _ reconcile.RuntimeProvider = (*lazyRuntime)(nil)

func (l *lazyRuntime) RuntimeObjects(ctx context.Context, d resource.Deployment, env resource.Environment) ([]reconcile.RuntimeObject, error) {
	p, err := l.get()
	if err != nil {
		return nil, err
	}
	return p.RuntimeObjects(ctx, d, env)
}

func (l *lazyRuntime) Events(ctx context.Context, d resource.Deployment, env resource.Environment, since time.Time) ([]reconcile.RuntimeEvent, error) {
	p, err := l.get()
	if err != nil {
		return nil, err
	}
	return p.Events(ctx, d, env, since)
}

func (l *lazyRuntime) Logs(ctx context.Context, d resource.Deployment, env resource.Environment, opts reconcile.LogOptions) (reconcile.LogStream, error) {
	p, err := l.get()
	if err != nil {
		return nil, err
	}
	return p.Logs(ctx, d, env, opts)
}

// lazySchemas is the platform's ConfigSchemaProvider (spec 020), set with
// lazyPlatform when the platform is ready.
type lazySchemas struct {
	lazy[reconcile.ConfigSchemaProvider]
}

var _ reconcile.ConfigSchemaProvider = (*lazySchemas)(nil)

func (l *lazySchemas) ConfigSchemas(ctx context.Context, comp resource.Component) (reconcile.ConfigSchemas, error) {
	p, err := l.get()
	if err != nil {
		return reconcile.ConfigSchemas{}, err
	}
	return p.ConfigSchemas(ctx, comp)
}
