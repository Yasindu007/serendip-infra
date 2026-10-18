package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/provider"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
	"github.com/serendip-infra/serendip-infra/providers/application/openchoreo"
	"github.com/serendip-infra/serendip-infra/providers/gateway/kgateway"
	"github.com/serendip-infra/serendip-infra/providers/identity/oidc"
	"github.com/serendip-infra/serendip-infra/providers/identity/thunderid"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
	"github.com/serendip-infra/serendip-infra/providers/secrets/openbao"
	"github.com/serendip-infra/serendip-infra/providers/storage/localpath"
)

// defaultReensureInterval is how often the gateway and application
// platform foundations are re-ensured (plan.md decision 2): drift repair,
// not convergence, so minutes.
const defaultReensureInterval = 5 * time.Minute

// kubeWiring is the Kubernetes-facing half of serendipd: reconcilers wired
// to lazy provider handles, and the background plan that sets the real
// providers up.
type kubeWiring struct {
	secretReconciler *reconcile.SecretReconciler
	authn            *lazyAuthenticator
	login            *lazyLogin
	plan             setupPlan
	// adminSubject is the identity provider's bootstrap administrator,
	// made Owner at "/" on first start (spec 017).
	adminSubject string
}

// consoleURIs are the console's login callback and post-logout page,
// registered with the identity provider (specs/016-ui-mvp plan.md
// decision 7). Empty when no console is served.
type consoleURIs struct {
	callback, postLogout string
}

// wireKubernetes conditionally sets up Kubernetes access. If kubeconfigPath
// is empty, no Kubernetes-facing providers are active -- a supported,
// non-fatal startup state, since not every Serendip install has a cluster
// at first boot -- and it returns nil.
//
// Otherwise it fails fast only on configuration errors (an unreadable
// kubeconfig, a missing helm binary, an invalid setting), registers the
// Volume, Secret, Application, and Component reconcilers against lazy
// provider handles, and returns the setup plan that installs and connects
// the real providers in the background (specs/014-reconciliation-hardening
// plan.md decision 4). A slow or unreachable cluster then degrades
// serendipd -- API up, resources reporting ProviderNotReady, setup retried
// -- instead of stopping it. The Lookup + type-assert pattern from earlier
// specs is gone: each setup step hands its concrete provider to the
// matching lazy handle directly, and registers it for completeness.
func wireKubernetes(kubeconfigPath string, registry *provider.Registry, db *sql.DB, bus event.Bus, engine *reconcile.Engine, console consoleURIs) (*kubeWiring, error) {
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
	nodePort, err := parseNodePort(os.Getenv("SERENDIP_GATEWAY_NODE_PORT"))
	if err != nil {
		return nil, err
	}
	reensureEvery, err := parseReensureInterval(os.Getenv("SERENDIP_REENSURE_INTERVAL"))
	if err != nil {
		return nil, err
	}
	// Both deployers check that helm is on PATH: a broken install, not a
	// slow cluster, so it fails startup.
	gatewayDeployer, err := kgateway.NewDeployer(client, kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}
	platformDeployer, err := openchoreo.NewDeployer(client, kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("openchoreo: %w", err)
	}
	identityDeployer, err := thunderid.NewDeployer(client, kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	identityDeployer.SetConsoleRedirects(thunderid.ConsoleRedirects{Callback: console.callback, PostLogout: console.postLogout})
	// The issuer depends on the gateway's endpoint, known only once the
	// gateway is up, but a malformed override is a config error: fail now.
	issuerOverride := os.Getenv("SERENDIP_IDENTITY_ISSUER")
	if issuerOverride != "" {
		if _, err := thunderid.Issuer(reconcile.GatewayEndpoint{}, issuerOverride); err != nil {
			return nil, fmt.Errorf("SERENDIP_IDENTITY_ISSUER: %w", err)
		}
	}
	gateway := kgateway.NewProvider(client, envOr("SERENDIP_GATEWAY_HOST", "localhost"), nodePort, openchoreo.GatewayProxyLabels())
	credentialsPath := envOr("SERENDIP_OPENBAO_CREDENTIALS_PATH", defaultOpenBaoCredentialsPath)

	storage, secrets, platform, authn := &lazyStorage{}, &lazySecrets{}, &lazyPlatform{}, &lazyAuthenticator{}
	login := &lazyLogin{}
	// verifier is the identity setup's token verifier, shared with the
	// console's relying party (one key cache).
	var verifier atomic.Pointer[oidc.Verifier]
	// ensureConsoleLogin registers the console's URIs with the provider and
	// then enables the console's login. Its failure never blocks API
	// authentication; the foundations loop retries it.
	ensureConsoleLogin := func(ctx context.Context, issuer string) {
		v := verifier.Load()
		if console.callback == "" || v == nil {
			return
		}
		if err := thunderid.EnsureClientRedirects(ctx, client, console.callback, console.postLogout); err != nil {
			log.Printf("serendipd: console login not available yet: %v", err)
			return
		}
		if login.client() != nil {
			return
		}
		rp, err := oidc.NewRelyingParty(oidc.RPConfig{
			Issuer: issuer, BackChannelURL: thunderid.ProxyBaseURL(client), ClientID: thunderid.ClientID,
			RedirectURL: console.callback, PostLogoutRedirectURL: console.postLogout,
			Verifier: v, HTTPClient: client.HTTPClient(),
		})
		if err != nil {
			log.Printf("serendipd: console login: %v", err)
			return
		}
		login.set(rp)
		log.Printf("serendipd: console login enabled (callback %s)", console.callback)
	}
	secretReconciler := registerKubernetesReconcilers(db, bus, engine, storage, secrets, platform)

	plan := setupPlan{
		storage: func(ctx context.Context) error {
			p, err := localpath.NewProvider(ctx, client)
			if err != nil {
				return err
			}
			registry.Register(capStorage, p)
			storage.set(p)
			log.Print("serendipd: kubernetes: storage provider (local-path) active")
			return nil
		},
		secrets: func(ctx context.Context) error {
			if err := openbao.NewDeployer(client).Ensure(ctx); err != nil {
				return fmt.Errorf("deploy: %w", err)
			}
			baoClient := openbao.NewClient(client.ProxyURL(openbao.Namespace, openbao.ServiceName, openbao.ListenPort), client.HTTPClient())
			if err := openbao.Bootstrap(ctx, baoClient, credentialsPath); err != nil {
				return fmt.Errorf("bootstrap: %w", err)
			}
			p := openbao.NewProvider(baoClient)
			registry.Register(capSecrets, p)
			secrets.set(p)
			log.Print("serendipd: kubernetes: secrets provider (openbao) active")
			return nil
		},
		gateway: func(ctx context.Context) (reconcile.GatewayEndpoint, error) {
			if err := gatewayDeployer.Ensure(ctx); err != nil {
				return reconcile.GatewayEndpoint{}, fmt.Errorf("deploy: %w", err)
			}
			ep, err := ensureGatewayReady(ctx, gateway)
			if err != nil {
				return reconcile.GatewayEndpoint{}, err
			}
			registry.Register(capGateway, gateway)
			log.Printf("serendipd: kubernetes: gateway provider (kgateway) active, external routes on %s:%d", ep.Host, ep.Port)
			return ep, nil
		},
		platform: func(ctx context.Context, ep reconcile.GatewayEndpoint) error {
			if err := platformDeployer.Ensure(ctx); err != nil {
				return fmt.Errorf("deploy: %w", err)
			}
			if err := ensurePlatformFoundations(ctx, client, ep); err != nil {
				return err
			}
			if err := openchoreo.CheckReady(ctx, client); err != nil {
				return fmt.Errorf("readiness: %w", err)
			}
			p := openchoreo.NewProvider(client, ep)
			registry.Register(capAppPlatform, p)
			platform.set(p)
			log.Print("serendipd: kubernetes: application platform provider (openchoreo) active")
			return nil
		},
		identity: func(ctx context.Context, ep reconcile.GatewayEndpoint) error {
			issuer, err := thunderid.Issuer(ep, issuerOverride)
			if err != nil {
				return err
			}
			if err := identityDeployer.Ensure(ctx, issuer); err != nil {
				return fmt.Errorf("deploy: %w", err)
			}
			if err := thunderid.CheckReady(ctx, client); err != nil {
				return err
			}
			if err := thunderid.EnsureRoute(ctx, client, ep, issuer); err != nil {
				return err
			}
			// Keys come through the Service proxy; the issuer check is still
			// against the public issuer URL tokens carry (plan.md decision 9).
			v, err := oidc.NewVerifier(oidc.Config{
				Issuer:     issuer,
				Audience:   thunderid.ClientID,
				JWKSURL:    thunderid.JWKSProxyURL(client),
				HTTPClient: client.HTTPClient(),
			})
			if err != nil {
				return err
			}
			if err := v.Warm(ctx); err != nil {
				return fmt.Errorf("signing keys: %w", err)
			}
			registry.Register(capIdentity, v)
			authn.set(v)
			verifier.Store(v)
			ensureConsoleLogin(ctx, issuer)
			log.Printf("serendipd: kubernetes: identity provider (thunderid) active, issuer %s, client %q; admin user %q, password in Secret %s/%s",
				issuer, thunderid.ClientID, thunderid.AdminUsername, thunderid.Namespace, thunderid.AdminSecretName)
			return nil
		},
		identityFoundations: func(ctx context.Context, ep reconcile.GatewayEndpoint) error {
			issuer, err := thunderid.Issuer(ep, issuerOverride)
			if err != nil {
				return err
			}
			if err := thunderid.EnsureRoute(ctx, client, ep, issuer); err != nil {
				return err
			}
			ensureConsoleLogin(ctx, issuer)
			return nil
		},
		gatewayFoundations: func(ctx context.Context) error {
			// The CRDs are the gateway's foundation; ensuring them here
			// (not via the Helm deployer) keeps a no-drift pass to reads.
			if err := openchoreo.EnsureGatewayAPICRDs(ctx, client); err != nil {
				return err
			}
			_, err := ensureGatewayReady(ctx, gateway)
			return err
		},
		platformFoundations: func(ctx context.Context, ep reconcile.GatewayEndpoint) error {
			return ensurePlatformFoundations(ctx, client, ep)
		},
		reensureEvery: reensureEvery,
	}
	return &kubeWiring{secretReconciler: secretReconciler, authn: authn, login: login, plan: plan, adminSubject: thunderid.AdminSubject}, nil
}

// ensureGatewayReady ensures the shared gateway and treats "not programmed
// within the provider's wait" as a failed attempt, so setup retries it.
func ensureGatewayReady(ctx context.Context, gp reconcile.GatewayProvider) (reconcile.GatewayEndpoint, error) {
	ep, ready, reason, message, err := gp.EnsureGateway(ctx)
	if err != nil {
		return reconcile.GatewayEndpoint{}, err
	}
	if !ready {
		return reconcile.GatewayEndpoint{}, fmt.Errorf("gateway %s/%s not ready: %s: %s (if NodePort %d is already in use, set SERENDIP_GATEWAY_NODE_PORT)",
			ep.Namespace, ep.Name, reason, message, ep.Port)
	}
	return ep, nil
}

// ensurePlatformFoundations ensures what Applications and Components deploy
// through: the DataPlane (with its gateway wiring) and the platform
// defaults. All server-side applies, so a no-drift pass stores nothing. The
// Gateway API CRDs the platform's routes need belong to the gateway's
// foundations, which always run first.
func ensurePlatformFoundations(ctx context.Context, client *kubernetes.Client, ep reconcile.GatewayEndpoint) error {
	if err := openchoreo.EnsureDataPlane(ctx, client, ep); err != nil {
		return fmt.Errorf("data plane: %w", err)
	}
	if err := openchoreo.EnsurePlatformDefaults(ctx, client); err != nil {
		return fmt.Errorf("platform defaults: %w", err)
	}
	return nil
}

// registerKubernetesReconcilers wires every Kubernetes-backed kind into
// engine against the given (lazy) providers, and returns the
// SecretReconciler api.SecretHandler needs. Must run before engine.Run.
func registerKubernetesReconcilers(db *sql.DB, bus event.Bus, engine *reconcile.Engine,
	storage reconcile.StorageProvider, secrets reconcile.SecretsProvider, platform reconcile.ApplicationPlatformProvider,
) *reconcile.SecretReconciler {
	volumes := sqlite.NewStore[resource.VolumeSpec, resource.VolumeStatus](db, resource.KindVolume).SetBus(bus)
	engine.Register(resource.KindVolume, reconcile.NewVolumeReconciler(volumes, storage), lister(volumes, resource.KindVolume))

	secretStore := sqlite.NewStore[resource.SecretSpec, resource.SecretStatus](db, resource.KindSecret).SetBus(bus)
	secretReconciler := reconcile.NewSecretReconciler(secretStore, secrets)
	engine.Register(resource.KindSecret, secretReconciler, lister(secretStore, resource.KindSecret))

	apps := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, resource.KindApplication).SetBus(bus)
	components := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](db, resource.KindComponent).SetBus(bus)
	engine.Register(resource.KindApplication, reconcile.NewApplicationReconciler(apps, platform), lister(apps, resource.KindApplication))
	engine.Register(resource.KindComponent, reconcile.NewComponentReconciler(components, apps, platform), lister(components, resource.KindComponent))

	return secretReconciler
}

// lister returns the engine's resync lister for one kind's store.
func lister[S, St any](s store.Store[S, St], kind resource.Kind) func(context.Context) ([]graph.ResourceRef, error) {
	return func(ctx context.Context) ([]graph.ResourceRef, error) {
		list, err := s.List(ctx, store.ListOptions{})
		if err != nil {
			return nil, err
		}
		refs := make([]graph.ResourceRef, len(list))
		for i, r := range list {
			refs[i] = graph.ResourceRef{Kind: kind, ID: r.Metadata.ID}
		}
		return refs, nil
	}
}

// parseNodePort parses SERENDIP_GATEWAY_NODE_PORT; empty means the default.
// Kubernetes only allocates NodePorts from 30000-32767 by default.
func parseNodePort(raw string) (int32, error) {
	if raw == "" {
		return kgateway.DefaultNodePort, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 30000 || n > 32767 {
		return 0, fmt.Errorf("SERENDIP_GATEWAY_NODE_PORT=%q: must be a port in the NodePort range 30000-32767", raw)
	}
	return int32(n), nil
}

// parseReensureInterval parses SERENDIP_REENSURE_INTERVAL (a Go duration,
// e.g. "5m", "30s"); empty means the default.
func parseReensureInterval(raw string) (time.Duration, error) {
	if raw == "" {
		return defaultReensureInterval, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("SERENDIP_REENSURE_INTERVAL=%q: must be a positive duration such as 5m or 30s", raw)
	}
	return d, nil
}
