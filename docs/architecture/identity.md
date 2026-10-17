# Identity

Serendip separates three distinct identities that are easy to conflate.
Keeping them separate is a deliberate design decision from the founding
discussion, not an accident of implementation.

```
1. Human identity     — a person logging into Serendip
2. Platform identity  — Serendip's own controllers talking to providers
3. Workload identity  — an application/service acting as itself
```

## 1. Human identity

```
Person → Identity Provider (OIDC/OAuth2) → Serendip
```

Used for logging into the Serendip UI/CLI/API/MCP. The identity provider
behind it is a capability (`capability-model.md`), default Thunder,
replaceable with Keycloak, Entra ID, or any OIDC-compatible external IdP.

## 2. Platform identity

```
Serendip controller → Provider API (e.g. Kubernetes API) → underlying infra
```

The identity Serendip's own control plane uses to act on providers on the
platform's behalf (constitution 2: providers are called through the
registry, never bypassed). This is distinct from both human and workload
identity — it is not something end users configure.

## 3. Workload identity

The identity an application/service uses to authenticate — the one most
directly analogous to Azure App Registrations / Service Principals or AWS
IAM roles. This is where Serendip's abstraction work matters most: a
developer should not need to know the underlying IdP's object model to give
their application an identity.

```
Serendip
  Create Application Identity
    name: payments-workload
    type: Workload Identity
    permissions:
      Key Vault: Secrets.Read
```

Serendip resolves this to the actual provider-specific mechanism:

| Environment | Resolves to |
|---|---|
| Entra ID / Azure | App/Federated Credential → Azure RBAC → Key Vault |
| AWS | IAM Role → IAM Policy |
| Kubernetes / on-prem | ServiceAccount → OIDC / SPIFFE |
| Thunder | Native OIDC/OAuth2 client |

Same Serendip-level concept (an Identity resource with Permissions), a
different implementation per provider — the same pattern as every other
capability in `capability-model.md`.

## Application authentication, end to end

### Human login (frontend)

A Component marked "Authentication: Enabled" gets an OIDC issuer, client ID,
redirect URI, and scopes provisioned automatically. The user sees "Login
with SSO"; the backend validates the resulting access token and reads
standard claims (`sub`, `email`, `groups`, `roles`, `tenant`). Serendip does
not reinvent OAuth2/OIDC — it provisions standard clients against the
configured Identity provider.

### Service-to-service

When Component A declares a dependency on Component B, Serendip creates a
workload-identity relationship between them rather than requiring the
developer to hand-manage `CLIENT_ID`/`CLIENT_SECRET` pairs. Short-lived or
federated credentials are preferred over long-lived static secrets where the
underlying provider supports it.

### Access to external/cloud resources

A developer declares "this workload needs `Secrets.Read` on this Key Vault"
at the Serendip Application level; Serendip translates that into the
provider-specific object graph (Entra App Registration + federated
credential + RBAC assignment, or an IAM role + policy, or a ServiceAccount +
OIDC trust) rather than the developer configuring the cloud console
directly.

### AI-assisted configuration

Because authentication is modeled as a declarative property of the
Application (not imperative console clicks), it is expressible as an AI
Action subject to the same approval engine as any other change
(`ai-operations.md`): e.g. "add OIDC authentication to `shop` and restrict
`/admin/*` to the Engineering group" is a proposed diff, not an
auto-applied change.

## Where the application platform fits

The Application Platform capability (default: OpenChoreo,
`capability-model.md`) owns the workload/deployment side — secrets
injection, resource dependency wiring, network policy for a Component. It
does **not** own Identity Provider selection or the higher-level Identity
resource model; Identity sits alongside Application Platform as its own
capability, feeding into it rather than being owned by it.

```
Serendip
├── Identity        → Thunder / Entra / Keycloak / ...
├── Application Platform → OpenChoreo
└── Operations
```

## What's built (spec 015)

Human identity (section 1) is real; platform and workload identity are not
built yet. Detail and evidence: `specs/015-identity-foundation/`.

### Two ways into the API

`api.AuthMiddleware` resolves every `/v1/` request's caller:

1. **Static API keys** (`SERENDIP_API_KEYS`, spec 002) are checked first,
   unchanged. Scripts, the agent and the bench harness keep using them.
2. **Otherwise the bearer token goes to the Identity capability's
   `identity.Authenticator`.** A valid OIDC **access token** from the
   configured issuer becomes the audit actor `person:<username>`
   (`identity.Actor`).

Outcomes:
- A rejected token gets a **401** problem+json that never says which
  check failed. The reason is logged, with the request ID.
- A token that can't be checked yet (the identity provider is still being
  set up, or its keys are unreachable and none are cached) gets a **503**.
  It may well be valid.
- API keys may not use the `person:` prefix for their actor names, so a
  key can't pose as a person.

Without a cluster (`SERENDIP_KUBECONFIG` unset), no identity provider is
wired, and API keys are the only way in, exactly as before.

### The verifier: provider-neutral

`providers/identity/oidc` is a standard-library JWT verifier with no
dependency (ADR 0009's reasoning).

What it checks:
- the signature, with an algorithm allow-list (RS/PS/ES/EdDSA; `none`
  and HMAC are rejected), and the key's type, curve and `alg` must match
  the token's algorithm;
- `iss`, `aud` (the client ID `serendip`), and `exp`/`nbf`/`iat`, with
  30 s leeway;
- a required `client_id`, which rejects ID tokens: they share the
  audience but are meant for the client, not the API.

Key handling:
- Keys come from discovery, or from an override URL.
- An unknown `kid` refetches at most once per 30 s.
- Cached keys keep working while the provider is down.

Pointing it at any other OIDC issuer is configuration only. That's the
planned `external-oidc` escape hatch, not registered yet.

### The default provider: ThunderID

`serendipd` installs it (`providers/identity/thunderid`, chart and image
1.0.1, `VERSIONS.md`) after the gateway, in parallel with the application
platform, under spec 014's background setup. It reports as `identity` in
`GET /v1/readiness`.

The install:
- **One instance, no node-failure protection** (constitution rule 6). It
  runs one replica with SQLite on three PVCs: database, certs (signing
  and encryption keys), secrets.
- **Signing keys** are generated once per install onto the certs PVC.
  They survive pod and `serendipd` restarts, so a token issued before a
  restart still verifies.
- **Issuer**: `http://identity.<SERENDIP_GATEWAY_HOST>:<gateway NodePort>`,
  by default `http://identity.localhost:30080`; override with
  `SERENDIP_IDENTITY_ISSUER`.
  - It must equal what tokens carry, so changing it (including the
    gateway NodePort) invalidates tokens issued under the old one.
  - `serendipd` fetches keys through the Kubernetes Service proxy, so the
    host doesn't need to resolve `*.localhost`. Browsers do.
- **Exposed through the shared gateway**: only the login paths (`/oauth2`,
  `/.well-known`, `/gate`, `/flow/execute`, `/flow/meta`,
  `/design/resolve`). The admin console, the management APIs, MCP and
  health are not routed.
  - The foundations loop re-creates the route if it's deleted.

### Logging in

The OIDC client is `serendip`: public, authorization code + PKCE. Its only
redirect URI is the fixed loopback `http://127.0.0.1:8250/callback`.
ThunderID matches redirect URIs exactly and imports its client definition
on first install only. Adding a redirect URI later (the UI will need one)
goes through ThunderID's management API; spec 015's plan.md records how.

The bootstrap administrator:
- The username is `admin`. The password is in the Kubernetes Secret
  `serendip-identity/serendip-identity-admin`, key `password`: readable by
  a cluster admin, never logged, never in the API. That's the same custody
  tier as OpenBao's init file.
- The Secret is created once, on a fresh install. If it's deleted later,
  it is **not** recreated: a new random password wouldn't be the stored
  admin's. `serendipd` logs that instead.
- Add more people in ThunderID's own console for now. There's no
  Serendip-level user management yet.

### Limits

- **No authorization.** Anyone who logs in has the same full API access as
  an API key. There's no RBAC or policy engine yet.
- **Plain HTTP.** Fine for a browser on `localhost`; anywhere else needs
  TLS first (a later spec). Browsers won't do OIDC over plain HTTP on other
  hosts anyway.
- **No revocation before expiry.** Tokens are verified offline, so a
  token stays valid until it expires (1 h by default), even after logout.
- **The audit actor is the username.** The stable `sub` isn't recorded,
  so a renamed or reused username is ambiguous in old audit rows. Names
  are cleaned of control characters and capped at 128 characters.
- **ThunderID's RSA signing certificate is valid for 365 days.** What
  happens at expiry is untested upstream; it's a follow-up.

## Open questions

- None outstanding. Thunder-as-default is covered by ADR 0006, which
  documents explicit fallback triggers to Keycloak rather than treating the
  default as permanent by inertia. Its first re-check (2026-10-17) kept the
  default. The project is now ThunderID at `github.com/thunder-id/thunderid`
  (it was `asgardeo/thunder` at 1.0.0).
