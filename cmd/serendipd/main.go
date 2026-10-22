// Command serendipd is the Serendip control-plane daemon: the HTTP API
// (api package) over core/. See specs/002-http-api/plan.md.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/cmd/serendipd/console"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
	"github.com/serendip-infra/serendip-infra/providers/identity/oidc"
	"github.com/serendip-infra/serendip-infra/providers/identity/thunderid"
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
	if len(os.Args) > 1 && os.Args[1] == "break-glass" {
		if err := runBreakGlass(context.Background(), os.Args[2:], os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, "serendipd break-glass:", err)
			os.Exit(1)
		}
		return
	}
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
	agentIssuers, err := parseAgentIssuers(os.Getenv("SERENDIP_AGENT_ISSUERS"), os.Getenv("SERENDIP_IDENTITY_ISSUER"))
	if err != nil {
		return err
	}

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
	registry := provider.NewRegistry()

	// wireKubernetes runs before api.NewServer because api.SecretHandler
	// needs the *reconcile.SecretReconciler it constructs. It only wires:
	// installing and connecting the cluster-facing providers happens in the
	// background once the API is up (kube.plan.start below), so a slow or
	// unreachable cluster degrades serendipd instead of stopping it
	// (specs/014-reconciliation-hardening plan.md decision 4).
	// The console is served on the API's own origin (specs/016-ui-mvp
	// plan.md decisions 1-2); its public URL is what browsers use and what
	// the identity provider redirects back to.
	origin, err := consoleOrigin(addr)
	if err != nil {
		return err
	}
	consoleRoutes := consoleURIs{callback: origin.String() + "/auth/callback", postLogout: origin.String() + "/"}

	kube, err := wireKubernetes(os.Getenv("SERENDIP_KUBECONFIG"), registry, db, bus, engine, consoleRoutes,
		func(issuer string) bool { return isAgentIssuer(agentIssuers, issuer) })
	if err != nil {
		return fmt.Errorf("kubernetes: %w", err)
	}
	var secretReconciler *reconcile.SecretReconciler
	// authn stays a nil interface (not a typed nil) without a cluster, so
	// the API falls back to API keys alone.
	var authn identity.Authenticator
	if kube != nil {
		secretReconciler = kube.secretReconciler
		authn = kube.authn
	}
	registerApprovals(db, bus, engine, secretReconciler)
	if len(agentIssuers) > 0 {
		agents, err := agentVerifiers(ctx, agentIssuers)
		if err != nil {
			return err
		}
		// Tokens are routed by issuer: an agent issuer's only ever become
		// agents, everything else goes to the human issuer's verifier.
		authn = oidc.NewIssuerRouter(authn, agents)
	}

	// Before the API starts, so the administrator can act from the first
	// request (spec 017 plan.md decision 7).
	if kube != nil {
		if err := bootstrapAdmin(ctx, db, bus, kube.adminSubject); err != nil {
			return err
		}
	}

	webConsole := &api.Console{Handler: console.Handler(), Origin: origin, Sessions: api.NewSessions(api.SessionConfig{})}
	if kube != nil {
		webConsole.Login = kube.login.client
	}
	var serverOpts []api.ServerOption
	if kube != nil {
		serverOpts = append(serverOpts, api.WithDirectory(kube.directory), api.WithResourceMetadata(api.ResourceMetadata{
			Issuer: kube.issuer, CLIClient: thunderid.ClientID, AssistantClient: thunderid.DelegateClientID,
		}))
	}
	srv := api.NewServer(db, bus, apiKeys, authn, secretReconciler, registry, webConsole, serverOpts...)
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

	if kube != nil {
		// Setup goroutines touch only the cluster API and the registry,
		// never the store, so shutdown doesn't wait for them.
		kube.plan.start(ctx, newTracker(registry, setupBackoff, capStorage, capSecrets, capGateway, capAppPlatform, capIdentity))
	} else {
		log.Print("serendipd: all capabilities ready")
	}

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

// consoleOrigin is the console's public origin: SERENDIP_PUBLIC_URL, or
// http://localhost:<port of addr>. A non-localhost http origin needs
// SERENDIP_ALLOW_INSECURE_ORIGIN=1, since the session cookie would cross a
// network in clear (until HTTPS exists).
func consoleOrigin(addr string) (*url.URL, error) {
	raw := os.Getenv("SERENDIP_PUBLIC_URL")
	if raw == "" {
		_, port, err := net.SplitHostPort(addr)
		if err != nil || port == "" {
			return nil, fmt.Errorf("SERENDIP_ADDR %q: can't derive the console's public URL; set SERENDIP_PUBLIC_URL", addr)
		}
		raw = "http://localhost:" + port
	}
	origin, err := api.ParseOrigin(raw, os.Getenv("SERENDIP_ALLOW_INSECURE_ORIGIN") == "1")
	if err != nil {
		return nil, fmt.Errorf("SERENDIP_PUBLIC_URL: %w", err)
	}
	return origin, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseAPIKeys parses SERENDIP_API_KEYS, format "token1:actor1,token2:actor2"
// (see api.AuthMiddleware's doc comment; logged-in people come from the
// identity provider instead). Malformed
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
		// "person:" and "agent:" actors name callers the identity provider
		// or an agent issuer authenticated (core/identity.Actor); an API key
		// may not claim to be one.
		if strings.HasPrefix(actor, identity.ActorPrefix) || strings.HasPrefix(actor, identity.AgentActorPrefix) {
			log.Printf("serendipd: skipping SERENDIP_API_KEYS entry for actor %q: the %q and %q prefixes are reserved for authenticated people and agents", actor, identity.ActorPrefix, identity.AgentActorPrefix)
			continue
		}
		keys[token] = actor
	}
	return keys
}
