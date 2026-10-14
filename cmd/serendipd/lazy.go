package main

import (
	"context"
	"sync/atomic"

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
