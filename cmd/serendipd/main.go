// Command serendipd is the Serendip control-plane daemon: the HTTP API
// (api package) over core/. See specs/002-http-api/plan.md.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
	"github.com/serendip-infra/serendip-infra/providers/secrets/openbao"
	"github.com/serendip-infra/serendip-infra/providers/storage/localpath"
)

// resyncEvery/reconcileTimeout are specs/003-reconciliation-engine/plan.md's
// chosen defaults, not yet exposed as env vars -- nothing has needed to
// tune them yet (no profile-specific override exists), unlike
// SERENDIP_ADDR/SERENDIP_DB_PATH which real deployments already need to
// vary.
const (
	resyncEvery      = 30 * time.Second
	reconcileTimeout = 10 * time.Second
)

// defaultOpenBaoCredentialsPath follows agent/identity's existing
// /var/lib/serendip/ state-root convention (spec 004) -- a new,
// Serendip-owned path, not reused from K3s's own /etc/rancher/k3s/
// (ADR 0009's open-question 2).
const defaultOpenBaoCredentialsPath = "/var/lib/serendip/secrets/openbao-init.json"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "serendipd:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbPath := envOr("SERENDIP_DB_PATH", "serendip.db")
	addr := envOr("SERENDIP_ADDR", ":8080")
	apiKeys := parseAPIKeys(os.Getenv("SERENDIP_API_KEYS"))

	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("serendipd: close db: %v", err)
		}
	}()
	if err := sqlite.Migrate(ctx, db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	bus := event.NewInProcessBus()
	engine := newReconcileEngine(db, bus)

	// wireKubernetes runs before api.NewServer (reordered from this
	// package's original spec-002 sequencing) because api.SecretHandler
	// (spec 009) needs the *reconcile.SecretReconciler wireKubernetes
	// constructs -- unlike every prior resource kind, whose
	// api.ResourceHandler only ever needed a store.Store, built
	// independently of any Kubernetes-facing wiring.
	secretReconciler, err := wireKubernetes(ctx, os.Getenv("SERENDIP_KUBECONFIG"), db, bus, engine)
	if err != nil {
		return fmt.Errorf("kubernetes: %w", err)
	}

	srv := api.NewServer(db, bus, apiKeys, secretReconciler)
	srv.Addr = addr

	errCh := make(chan error, 1)
	go func() {
		log.Printf("serendipd: listening on %s (db: %s)", addr, dbPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		close(errCh)
	}()

	// engineDone always closes once Run returns, regardless of why --
	// used below to make sure no reconcile call is still in flight
	// before db.Close() runs. engineErrCh, in contrast, only ever
	// receives a value for a genuine crash (never for a plain ctx
	// cancellation): if it reacted to ctx cancellation the same way
	// ctx.Done() itself does, Go's select below could race and pick
	// this case instead of the ctx.Done() shutdown branch, skipping
	// srv.Shutdown() entirely -- errCh above avoids the same race only
	// because it's closed downstream of Shutdown() being called, not in
	// direct, parallel reaction to ctx.
	engineDone := make(chan struct{})
	engineErrCh := make(chan error, 1)
	go func() {
		defer close(engineDone)
		log.Print("serendipd: reconcile engine starting")
		if err := engine.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			engineErrCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Print("serendipd: shutdown signal received")
		<-engineDone
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		log.Print("serendipd: shut down cleanly")
		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case err := <-engineErrCh:
		return fmt.Errorf("reconcile engine: %w", err)
	}
}

// newReconcileEngine constructs the Engine and registers its one
// Reconciler (spec 003: Cluster health, no other kind yet). Not started
// here -- the caller runs engine.Run(ctx) itself so it can share the same
// shutdown-signal ctx the HTTP server already uses.
func newReconcileEngine(db *sql.DB, bus event.Bus) *reconcile.Engine {
	clusters := sqlite.NewStore[resource.ClusterSpec, resource.ClusterStatus](db, resource.KindCluster)
	providers := sqlite.NewStore[resource.ProviderSpec, resource.ProviderStatus](db, resource.KindProvider)

	engine := reconcile.NewEngine(bus, resyncEvery, reconcileTimeout)
	engine.Register(resource.KindCluster, reconcile.NewClusterReconciler(clusters, providers, reconcile.TCPHealthChecker{}),
		func(ctx context.Context) ([]graph.ResourceRef, error) {
			list, err := clusters.List(ctx, store.ListOptions{})
			if err != nil {
				return nil, err
			}
			refs := make([]graph.ResourceRef, len(list))
			for i, c := range list {
				refs[i] = graph.ResourceRef{Kind: resource.KindCluster, ID: c.Metadata.ID}
			}
			return refs, nil
		})
	return engine
}

// wireKubernetes conditionally sets up Kubernetes access: if
// kubeconfigPath is empty, no Kubernetes-facing providers are active --
// a supported, non-fatal startup state, since not every Serendip
// install necessarily has a cluster yet at first boot. If set,
// constructs the real local-path Storage provider (spec 007, replacing
// spec 006's throwaway Node-listing proof-of-concept entirely -- not
// left alongside it), registers it in the core/provider.Registry, and
// wires a VolumeReconciler into engine. The Lookup + type-assert
// happens once here, in the composition root -- core/reconcile never
// imports core/provider at all (plan.md's resolved design, mirroring
// ClusterReconciler's constructor-injected HealthChecker exactly).
// Returns the *reconcile.SecretReconciler wireOpenBao constructs (nil
// if kubeconfigPath is empty) -- api.NewServer's SecretHandler needs
// it, unlike every other resource kind's handler.
func wireKubernetes(ctx context.Context, kubeconfigPath string, db *sql.DB, bus event.Bus, engine *reconcile.Engine) (*reconcile.SecretReconciler, error) {
	if kubeconfigPath == "" {
		log.Print("serendipd: SERENDIP_KUBECONFIG not set, no Kubernetes-facing providers active")
		return nil, nil
	}

	cfg, err := kubernetes.LoadConfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %s: %w", kubeconfigPath, err)
	}
	client, err := kubernetes.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("build client: %w", err)
	}

	storageProvider, err := localpath.NewProvider(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("storage provider: %w", err)
	}

	registry := provider.NewRegistry()
	registry.Register("storage", storageProvider)

	impl, ok := registry.Lookup("storage")
	if !ok {
		return nil, fmt.Errorf("internal: just-registered capability not found in registry")
	}
	sp, ok := impl.(reconcile.StorageProvider)
	if !ok {
		return nil, fmt.Errorf("internal: registered implementation does not satisfy reconcile.StorageProvider")
	}

	volumes := sqlite.NewStore[resource.VolumeSpec, resource.VolumeStatus](db, resource.KindVolume).SetBus(bus)
	engine.Register(resource.KindVolume, reconcile.NewVolumeReconciler(volumes, sp),
		func(ctx context.Context) ([]graph.ResourceRef, error) {
			list, err := volumes.List(ctx, store.ListOptions{})
			if err != nil {
				return nil, err
			}
			refs := make([]graph.ResourceRef, len(list))
			for i, v := range list {
				refs[i] = graph.ResourceRef{Kind: resource.KindVolume, ID: v.Metadata.ID}
			}
			return refs, nil
		})

	log.Print("serendipd: kubernetes: storage provider (local-path) active")

	secretReconciler, err := wireOpenBao(ctx, client, registry, db, bus, engine)
	if err != nil {
		return nil, fmt.Errorf("openbao: %w", err)
	}

	return secretReconciler, nil
}

// wireOpenBao deploys OpenBao into the cluster (idempotent, spec 008),
// runs its init/unseal bootstrap, registers the resulting
// *openbao.Provider adapter (spec 009 -- supersedes spec 008's bare
// *openbao.Client registration, same "replace, don't leave a parallel
// path" precedent spec 007 set for spec 006's nodeLister) under the
// "secrets" capability, constructs the Secret store, and wires a
// SecretReconciler into engine (mirrors wireKubernetes' own
// VolumeReconciler wiring block immediately above).
func wireOpenBao(ctx context.Context, client *kubernetes.Client, registry *provider.Registry, db *sql.DB, bus event.Bus, engine *reconcile.Engine) (*reconcile.SecretReconciler, error) {
	if err := openbao.NewDeployer(client).Ensure(ctx); err != nil {
		return nil, fmt.Errorf("deploy: %w", err)
	}

	baoClient := openbao.NewClient(client.ProxyURL(openbao.Namespace, openbao.ServiceName, openbao.ListenPort), client.HTTPClient())

	credentialsPath := envOr("SERENDIP_OPENBAO_CREDENTIALS_PATH", defaultOpenBaoCredentialsPath)
	if err := openbao.Bootstrap(ctx, baoClient, credentialsPath); err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}

	registry.Register("secrets", openbao.NewProvider(baoClient))

	impl, ok := registry.Lookup("secrets")
	if !ok {
		return nil, fmt.Errorf("internal: just-registered capability not found in registry")
	}
	secretsProvider, ok := impl.(reconcile.SecretsProvider)
	if !ok {
		return nil, fmt.Errorf("internal: registered implementation does not satisfy reconcile.SecretsProvider")
	}

	secrets := sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](db, resource.KindSecret).SetBus(bus)
	secretReconciler := reconcile.NewSecretReconciler(secrets, secretsProvider)
	engine.Register(resource.KindSecret, secretReconciler,
		func(ctx context.Context) ([]graph.ResourceRef, error) {
			list, err := secrets.List(ctx, store.ListOptions{})
			if err != nil {
				return nil, err
			}
			refs := make([]graph.ResourceRef, len(list))
			for i, s := range list {
				refs[i] = graph.ResourceRef{Kind: resource.KindSecret, ID: s.Metadata.ID}
			}
			return refs, nil
		})

	log.Print("serendipd: kubernetes: secrets provider (openbao) active")
	return secretReconciler, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseAPIKeys parses SERENDIP_API_KEYS, format "token1:actor1,token2:actor2"
// (see api.ActorMiddleware's doc comment -- this whole mechanism is
// explicitly temporary until a real identity provider exists). Malformed
// entries are skipped with a warning rather than failing startup; an
// empty/unset env var means every request gets 401, which is a safe
// default, not a silent open door.
func parseAPIKeys(raw string) map[string]string {
	keys := make(map[string]string)
	if raw == "" {
		return keys
	}
	for _, pair := range strings.Split(raw, ",") {
		token, actor, ok := strings.Cut(pair, ":")
		if !ok || token == "" || actor == "" {
			log.Printf("serendipd: skipping malformed SERENDIP_API_KEYS entry %q", pair)
			continue
		}
		keys[token] = actor
	}
	return keys
}
