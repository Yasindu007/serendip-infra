# ThunderID provider — version assumptions

ADR 0006 records why ThunderID is the Identity default; spec 015's plan.md
records how it is deployed.

| What | Pinned | Where |
|---|---|---|
| ThunderID chart | `oci://ghcr.io/thunder-id/helm-charts/thunderid` `1.0.1` (digest `sha256:f54599dc3715fe054576c4a8351b7346f153205d17f6b553f82fab11c4536218`) | `values.go` (`Chart`, `ChartVersion`) |
| ThunderID image | `ghcr.io/thunder-id/thunderid:1.0.1` | `values.go` (`ImageTag`) |
| Gateway API HTTPRoute | `gateway.networking.k8s.io/v1` | `route.go` |

Bump the chart and image together: chart 1.0.1's `appVersion` is 1.0.1.

## Facts confirmed against chart and image 1.0.1 (spec 015 T001 spike)

- The setup job, the admin-credentials Secret (when generated), the
  bootstrap ConfigMap and the three PVCs (database 1Gi, certs 16Mi, secrets
  16Mi) are `pre-install` hooks. `helm upgrade --install` on an existing
  release re-renders configuration and does not re-seed.
- The certs PVC (`/opt/thunderid/config/certs`) holds the per-install signing
  keys (RSA `signing.*`, ECDSA `ecdsa-signing.*`) and `crypto.key`. The
  JWKS is unchanged across a pod restart.
- `bootstrap.scripts` files are mounted next to the shipped ones in
  `/opt/thunderid/bootstrap/` and imported once, in name order. The shipped
  `01-default-resources.yaml` creates:
  - the default OU `01900000-0000-7000-8000-000000000001`;
  - `default-flow` (`…061`);
  - the `CONSOLE` client;
  - the admin user, whose `sub` is a UUID (`…030`) and `username` is
    `admin`.
- Access-token claims come from
  `inboundAuthConfig[].config.token.accessToken.userConfig.attributes`.
- Access tokens are `typ: at+jwt`, RS256 by default, `aud` = client ID, and
  carry `client_id`. ID tokens are `typ: JWT` with no `client_id`.
- `iss` equals `configuration.server.publicUrl` exactly. Discovery builds
  every endpoint from it, never from the request Host.
- The login flow: `/oauth2/authorize` → `/gate/signin` (SPA) →
  `POST /flow/execute` → `POST /oauth2/auth/callback` → redirect with code.
  The SPA also calls `/flow/meta` and `/design/resolve` (unauthenticated).
- The chart's own `httproute` block can only route `/`, which would expose
  `/console` and the management APIs. Serendip renders its own HTTPRoute.
- That route matches exact paths for everything under `/oauth2`, `/flow`
  and `/design`, plus the prefixes `/.well-known` (discovery) and `/gate`
  (the sign-in page and its assets) (spec 018 T001). A `/oauth2` prefix would also expose
  `/oauth2/introspect`, `/oauth2/dcr/register` and `/oauth2/par`, which
  reach ThunderID's own checks. `/oauth2/auth/callback` must stay
  routed, because the sign-in page POSTs to it. Logout uses
  `/oauth2/logout` and `/oauth2/logout/callback`. Management paths
  (`/users`, `/groups`, `/agents`, `/resource-servers`, `/flows`,
  `/design/themes`, …) are bare top-level paths, so they are never
  matched.
- A `system`-scoped admin token for the management API needs the `CONSOLE`
  client plus `resource=https://localhost:8090/mcp` (RFC 8707). Without it,
  authorize fails with `invalid_target`.

## Measured footprint

T001, unthrottled kind node: about 11.5 MiB working set and about 0.2 mcores
idle. The simulated-T470 figure is about 10.7 MiB and 0.2 mcores (spec 015 T008), in
`profiles/edge-1/bench/002-edge1-footprint.md`.

## Known limits

- The RSA signing certificate is valid for 365 days. What ThunderID does at
  expiry is unknown.
- Plain HTTP (`httpOnly: true`) is acceptable only on `localhost`.

## Console redirect URIs (spec 016, verified on 1.0.1 by the T001 spike)

- **Management access:** obtained by driving the default sign-in flow
  (`/oauth2/authorize` → `/flow/execute` ×2 → `/oauth2/auth/callback` →
  `/oauth2/token`) as the built-in `CONSOLE` client, with
  `scope=openid system` and `resource=https://localhost:8090/mcp`. Both
  values are 1.0.1 defaults. Without `resource`, authorize fails with
  `invalid_target`.
- **The credentials step:** action ID `action_001` in the default
  authentication flow.
- **Updating the client:** `GET`/`PUT /applications/{id}`. PUT replaces
  the whole document, so the full fetched document is sent back. Apps
  imported from bootstrap files are editable
  (`declarative_resources.enabled: false`).
- **Refresh tokens:** rotation is on (`renew_on_grant`,
  `revoke_previous_on_renew`), with a lifetime of 86400 s. Replaying an
  old refresh token revokes the whole family.
- **Logout and revocation:** `/oauth2/logout` accepts `client_id`
  without `id_token_hint`. Post-logout URIs match exactly.
  `/oauth2/revoke` accepts a public client by `client_id`.

Any of these may change in a minor release; re-verify them on an upgrade.

