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
	srv := api.NewServer(db, bus, apiKeys)
	srv.Addr = addr

	engine := newReconcileEngine(db, bus)

	if err := wireKubernetes(ctx, os.Getenv("SERENDIP_KUBECONFIG")); err != nil {
		return fmt.Errorf("kubernetes: %w", err)
	}

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

// nodeLister is a deliberately trivial, throwaway capability interface
// proving specs/006-kubernetes-provider-foundation's registry pattern
// end-to-end: a real Kubernetes-facing implementation, registered under
// a capability name, looked up and invoked through core/provider's
// generic any-typed registry. Spec 007 replaces this entirely with the
// real StorageProvider interface and local-path implementation -- this
// type is not meant to survive past this spec.
type nodeLister interface {
	ListNodeNames(ctx context.Context) ([]string, error)
}

type kubeNodeLister struct{ client *kubernetes.Client }

func (k *kubeNodeLister) ListNodeNames(ctx context.Context) ([]string, error) {
	nodes, err := k.client.List(ctx, "/api/v1/nodes")
	if err != nil {
		return nil, err
	}
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Metadata.Name
	}
	return names, nil
}

// wireKubernetes conditionally sets up Kubernetes access: if
// kubeconfigPath is empty, no Kubernetes-facing providers are active --
// a supported, non-fatal startup state (acceptance criterion 4), since
// not every Serendip install necessarily has a cluster yet at first
// boot. If set, it also runs this spec's proof-of-concept smoke
// operation (list real Nodes once, log the count) to confirm the whole
// kubeconfig-to-API chain is real, not just wired in theory.
func wireKubernetes(ctx context.Context, kubeconfigPath string) error {
	if kubeconfigPath == "" {
		log.Print("serendipd: SERENDIP_KUBECONFIG not set, no Kubernetes-facing providers active")
		return nil
	}

	cfg, err := kubernetes.LoadConfig(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("load kubeconfig %s: %w", kubeconfigPath, err)
	}
	client, err := kubernetes.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("build client: %w", err)
	}

	registry := provider.NewRegistry()
	registry.Register("kubernetes-smoke-test", &kubeNodeLister{client: client})

	impl, ok := registry.Lookup("kubernetes-smoke-test")
	if !ok {
		return fmt.Errorf("internal: just-registered capability not found in registry")
	}
	lister, ok := impl.(nodeLister)
	if !ok {
		return fmt.Errorf("internal: registered implementation does not satisfy nodeLister")
	}
	names, err := lister.ListNodeNames(ctx)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	log.Printf("serendipd: kubernetes: found %d node(s): %v", len(names), names)
	return nil
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
