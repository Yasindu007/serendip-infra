# Plan 015: Identity foundation (human login)

Status: Approved 2026-10-17

Spec: `spec.md` (approved 2026-10-17). Upstream facts below come from two
oss-researcher passes on 2026-10-17 against ThunderID v1.0.1's chart and
server source. Items marked **(spike)** were not confirmable from source and
are settled by T001 on a real install before anything depends on them.

## Approach in one paragraph

`serendipd` installs ThunderID with its official Helm chart (one replica,
SQLite on PVCs, plain HTTP). It seeds a fixed public OIDC client
(`serendip`) through the chart's bootstrap files and owns the admin
credential Secret itself. It exposes only ThunderID's login-facing paths
through the shared gateway, at `http://identity.<gateway host>:<node port>`.
The API's auth middleware tries the static API keys first, then hands any
other bearer token to a **provider-neutral OIDC verifier** (standard library
JWT/JWKS, no new dependency). That verifier checks signature, issuer,
audience and expiry, and yields a `person:<username>` audit actor. It all
runs under spec 014's model: background setup with retries, `identity`
readiness, a lazy handle that answers "not ready" (503) until setup is done,
and the foundations loop re-ensuring the route and Secret.

## Decisions (resolving spec.md's open questions)

1. **Install with the official Helm chart**, via the existing
   `providers/internal/helm` shell-out (ADR 0010's precedent), pinned to
   chart and image `1.0.1`. Hand-writing manifests (ADR 0009's OpenBao
   precedent) would mean re-creating the chart's init container, three hook
   PVCs, key generation (`setup.sh`) and bootstrap import. That's the
   fragile, upstream-specific part, and the chart owns it. Values Serendip
   sets:
   - `deployment.replicaCount=1`, HPA and PDB off;
   - image tag `1.0.1` with `pullPolicy: IfNotPresent` (the defaults are
     `latest`/`Always`);
   - all four `configuration.database.*.type=sqlite`, which makes the
     chart create the database PVC;
   - `configuration.server.httpOnly=true`,
     `configuration.server.publicUrl=<issuer>`;
   - `ingress.enabled=false`; the chart's own `httproute` stays off (it can
     only route `/` and would expose the console and management APIs);
   - resources raised from the chart's 50Mi/100Mi to **requests 64Mi,
     limits 256Mi** (four SQLite databases risk OOM at 100Mi; T001 and
     T008 measure the real number);
   - `setup.admin.passwordRef` pointing at Serendip's Secret (decision 3);
   - `bootstrap.scripts` containing one file, `70-serendip-client.yaml`
     (decision 4).

   Values go in a temp values file (`-f`), because `bootstrap.scripts` keys
   contain dots that `--set` can't express. Namespace `serendip-identity`,
   release `thunderid`.

2. **Re-running is safe; setup happens once.** The chart's setup job and its
   three PVCs (database, certs, secrets) are `pre-install` hooks. So
   `helm upgrade --install` on every `serendipd` start re-renders config
   (e.g. a changed issuer) without re-seeding, and the PVCs outlive
   `helm uninstall`.
   - The **signing keys** (RSA `signing.key`, kid `default-key`; ECDSA
     pair) and `crypto.key` are generated per install by `setup.sh` onto
     the certs PVC, never baked into the image. That's what keeps tokens
     valid across pod restarts (acceptance 6). T001 confirms they survive
     a pod delete.
   - Known limit, documented: the RSA signing cert is valid 365 days.
     What happens at expiry is unknown upstream, so it's recorded as a
     follow-up, not handled.

3. **Admin credential custody: a Serendip-owned Kubernetes Secret.**
   - `serendip-identity-admin` in `serendip-identity`, key `password`, 32
     random bytes (base64url).
   - Created **if absent, never updated** (Create, not server-side apply).
     A rotated value would desync from the password the one-time setup job
     already stored.
   - Referenced by `setup.admin.passwordRef`; username `admin`.
   - Why not the chart's generated Secret: chart-rendered random values
     can change on `helm upgrade` while setup doesn't re-run, leaving a
     Secret that no longer matches.
   - Same custody tier as OpenBao's init file (spec 008): readable by a
     cluster admin (`kubectl get secret`), never logged, never in the API.
   - No forced password change on first login: ThunderID's support for it
     is unverified, and edge-1's admin is the operator. Documented as a
     follow-up.

4. **The OIDC client is declared, with a fixed client ID.**
   - `70-serendip-client.yaml` declares an application `serendip`
     (clientId `serendip`, OU handle `default`, auth flow `default-flow`):
     public client, `pkceRequired: true`, `tokenEndpointAuthMethod: none`,
     grants `authorization_code` + `refresh_token`, access-token user
     attributes including `username` and `email`.
   - Field nesting is copied from the shipped `Console` app in
     `01-default-resources.yaml` (spike).
   - Because the ID is fixed, `serendipd` stores nothing to recognise its
     client: issuer (config) plus `serendip` is the whole identity. That
     answers spec.md's "where does the client ID live".
   - **Redirect URIs** match exactly (no RFC 8252 port wildcarding), and
     bootstrap files are only imported on first install. So this spec
     registers a fixed loopback `http://127.0.0.1:8250/callback`, for the
     verification flow now and a future CLI later.
   - The UI spec adds its own redirect URI. How (management API with a
     system token, or re-import) is a T001 spike question, recorded for
     that spec, not built here.

5. **Issuer URL**:
   - Default `http://identity.<SERENDIP_GATEWAY_HOST>:<gateway NodePort>`,
     i.e. `http://identity.localhost:30080`; overridable with
     `SERENDIP_IDENTITY_ISSUER`.
   - It's ThunderID's `publicUrl` exactly, with no trailing slash: `iss`
     equals `publicUrl`, and discovery builds endpoints from config, not
     the Host header.
   - Changing it later is a supported reconfiguration (helm re-renders),
     but tokens issued under the old issuer stop verifying. Documented,
     including the spec 012 note that the NodePort is part of it.

6. **Route: a Serendip-owned HTTPRoute** `serendip-identity` in
   `serendip-identity`, applied server-side.
   - It's attached to the shared gateway's `GatewayEndpoint`, hostname
     `identity.<host>`, backend `thunderid-service:8090`.
   - `PathPrefix` rules for **only** `/oauth2`, `/.well-known`, `/gate`,
     `/flow/execute` and `/auth`, the login-facing set.
   - `/console`, `/mcp`, `/health` and every management API are **not**
     routed (spec scope).
   - Whether `/auth` is needed for the default password flow is a spike
     question; drop it if not.

7. **The bearer is the access token; audience `serendip`.** ThunderID access
   tokens are JWTs (RS256 by default; ES256/EdDSA possible) with `iss`,
   `sub`, `exp`, `aud` (defaults to the client ID), `client_id`, `scope`
   and configured user attributes. The verifier requires all of these:
   - `iss` == issuer;
   - `aud` contains the configured audience (default `serendip`);
   - `exp` in the future, `nbf`/`iat` not in the future, 30 s leeway;
   - a signature by a JWKS key whose `kty` matches `alg`; `alg: none`,
     HMAC, and unknown algs are rejected.

   ID tokens are not accepted: they're for the client, and their audience
   is the same client ID, so audience alone can't tell them apart. They
   carry no `scope`/`client_id`, and the verifier requires `client_id`
   (spike: confirm present in access tokens).

8. **Audit actor: `person:<username>`**:
   - `username` comes from the token's user attributes, falling back to
     `email`, then `sub`. `sub` is opaque, so a human-readable name in
     audit rows matters more at edge-1 scale than surviving a rename;
     documented.
   - API-key actors keep their configured names.
   - To stop an API key impersonating a person, `parseAPIKeys` **rejects**
     actor names starting with `person:` (logged and skipped, like other
     malformed entries).

9. **Verification is standard library only; no ADR needed.**
   - JWS compact parsing, `crypto/rsa` (PKCS#1 v1.5, SHA-256/384/512),
     `crypto/ecdsa` (P-256/384, raw R||S signatures), `crypto/ed25519`,
     JWK→public key with `encoding/json` and `math/big`. Same reasoning as
     the hand-rolled OpenBao client (ADR 0009) and the rejected
     `client-go`: `go-oidc` would pull in `go-jose` and `x/oauth2` for
     roughly 300 lines of code we can test fully.
   - **JWKS source**: discovery (`<issuer>/.well-known/openid-configuration`
     → `jwks_uri`) by default, which is the external-OIDC path. A
     configured **override URL and HTTP client** handle the ThunderID
     case, where `serendipd` fetches `/oauth2/jwks` through the Kubernetes
     API's Service proxy (`client.ProxyURL`, as for OpenBao). The browser
     still uses the gateway issuer; the iss check is on the claim, not on
     where keys came from. This avoids relying on the host resolving
     `*.localhost`: browsers do, but Go's resolver and some Linux setups
     don't.
   - **Key cache**: refreshed on an unknown `kid`, at most once per 30 s;
     cached keys keep verifying while the provider is unreachable.

10. **Core interface: a new `core/identity` package**, provider-neutral:

    ```go
    type Principal struct {
        Subject string // stable subject from the provider ("sub")
        Name    string // display name used in the audit actor
    }
    type Authenticator interface {
        // Authenticate verifies a bearer credential. ErrInvalidCredential
        // for anything that isn't valid; reconcile.ErrProviderNotReady
        // (from the composition root's lazy handle) when it can't be
        // checked yet.
        Authenticate(ctx context.Context, token string) (Principal, error)
    }
    var ErrInvalidCredential = errors.New("identity: invalid credential")
    func Actor(p Principal) string // "person:" + Name
    ```

    It is not under `core/reconcile`, because nothing here reconciles a
    resource kind. Deploying the provider is foundation work, like OpenBao
    in spec 008, so no new kind and no reconciler (spec scope).

11. **API middleware** (`api.ActorMiddleware` →
    `AuthMiddleware(apiKeys, authn identity.Authenticator)`):
    - an API-key match comes first and is unchanged;
    - otherwise, if `authn` is non-nil, the token is authenticated;
    - `ErrInvalidCredential` → 401 `unauthorized` ("invalid bearer token",
      no detail on which check failed);
    - `ErrProviderNotReady` → **503** with the existing problem mapping
      ("identity provider not ready, retry later");
    - with `authn` nil (no kubeconfig), behaviour is exactly today's.

    `serendipd` without a cluster therefore keeps working on API keys
    alone.

12. **Wiring** (`cmd/serendipd`):
    - New capability `identity` in the tracker. Setup runs **after the
      gateway** (it needs the endpoint for the route and default issuer),
      in parallel with the application platform.
    - Steps: ensure namespace + admin Secret → `helm upgrade --install` →
      wait for `/health/readiness` via the Service proxy → apply the
      HTTPRoute → fetch JWKS once (proves keys are reachable) → hand the
      verifier to `lazyAuthenticator`.
    - The foundations loop re-ensures the HTTPRoute and the admin Secret's
      existence each pass (`RepairFailed` on error), not Helm. That keeps
      spec 014's "a no-drift pass is reads only" property.
    - A deleted Helm release is not repaired in-loop; it's repaired on the
      next start, documented like OpenChoreo's release.

13. **Bench**: `cmd/bench-edge1` maps namespace `serendip-identity` to a new
    group `Identity (ThunderID)`.

## Interfaces and types created

- `core/identity`: `Principal`, `Authenticator`, `ErrInvalidCredential`,
  `Actor`.
- `providers/identity/oidc`: `Verifier` (implements
  `identity.Authenticator`), `Config{Issuer, Audience, JWKSURL, HTTPClient,
  Leeway, Now}`, `NewVerifier(cfg) (*Verifier, error)`, `(*Verifier).Warm`
  (fetch keys once; used by setup). Unexported: JWS parse, JWK decode,
  per-alg verify, key cache.
- `providers/identity/thunderid`: `Deployer` (`NewDeployer(client,
  kubeconfigPath)`, `Ensure(ctx, issuer)`), `EnsureAdminSecret`,
  `EnsureRoute(ctx, client, ep, host)`, `CheckReady`, `JWKSProxyURL`,
  constants `Namespace`, `ClientID`, `ChartVersion`.
- `cmd/serendipd`: `capIdentity`, `lazyAuthenticator`, setup and
  foundations steps, env `SERENDIP_IDENTITY_ISSUER`.
- `api`: `AuthMiddleware` replaces `ActorMiddleware`; `NewServer` takes an
  `identity.Authenticator` (nil allowed).

## Files touched

New:
- `core/identity/{doc.go,identity.go,identity_test.go}`
- `providers/identity/oidc/{doc.go,verifier.go,jwk.go,jws.go,verifier_test.go,jwk_test.go}`
- `providers/identity/thunderid/{doc.go,deploy.go,values.go,route.go,ready.go,*_test.go}`
- `docs/architecture/identity.md` section "What's built (spec 015)"

Changed:
- `api/middleware.go`, `api/middleware_test.go`, `api/server.go`,
  `api/acceptance_test.go` (if it constructs the server), `api/CLAUDE.md`
- `cmd/serendipd/{main.go,wiring.go,setup.go,lazy.go,setup_test.go}`
- `cmd/bench-edge1/summary.go` (+ test)
- `docs/decisions/0006-thunder-as-identity-provider.md` (re-check, repo
  URL, pinned version)
- `docs/architecture/{capability-model.md,provider-model.md}` (identity
  now has a provider), `profiles/edge-1/bench/002-edge1-footprint.md`,
  `docs/PROGRESS.md`

## Risks

- **Unconfirmed upstream details** (bootstrap app nesting, `client_id` and
  `username` in access tokens, `sub` format, DB mount path, `/auth`
  necessity, scripted login flow). Mitigation: T001 spike on a real
  install before T003–T005 depend on them. If a detail differs, plan.md is
  amended there, not silently.
- **Scripted login for acceptance 3.** The login UI (`/gate`) is an SPA
  driving `POST /flow/execute`. Verification needs a non-browser driver:
  authorize → read `executionId` → POST the username/password step → follow
  the redirect to the loopback → exchange the code with the PKCE verifier.
  If the flow API proves impractical to script, the fallback is a real
  browser login (the user, or a browser tool) with the code captured at the
  loopback. Either way the token is real.
- **Footprint.** ThunderID publishes no idle RSS; the chart's 100Mi limit
  hints it's small, but four SQLite DBs with `max_open_conns: 500` are
  unknown. T008 measures; if it threatens the 16 GB verdict, that's
  reported, not hidden (ADR 0006's own trigger).
- **ADR 0006's "no release" trigger closes mid-November** (no tag since
  2026-08-25). If it fires during implementation, stop and raise it.
- **Plain HTTP**: tokens cross the wire unencrypted, which is acceptable
  only on `localhost` (spec scope). Remote browsers are blocked by browsers
  anyway (OIDC over http on non-localhost). Documented loudly.
- **Signing-cert expiry at 365 days**: unknown upstream behaviour;
  follow-up, documented.
- **Anyone who logs in is an administrator** (no RBAC): stated in the
  identity doc and API notes.

## Explicitly NOT done

Everything in spec.md "Scope — OUT", plus these consequences of the
decisions above:
- No discovery/config endpoint on Serendip's API for clients (e.g.
  `GET /v1/auth/config`). The UI spec adds what it needs.
- No redirect-URI management after install; no UI redirect URI.
- No management-API client in Go (no `system` token handling), unless T001
  shows a declared client is impossible and it's the only route.
- No `SERENDIP_OIDC_*` config to point at an external issuer. The verifier
  supports it; registering an `external-oidc` provider is a later spec.
- No token revocation/introspection checks: access tokens are verified
  offline until expiry (1 h default). Documented.
- No refresh-token handling on the server side (that's a client concern).
- No Helm-release repair in the foundations loop.
