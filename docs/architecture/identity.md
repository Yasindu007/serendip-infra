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
- **Exposed through the shared gateway**: only the login paths, as an
  allowlist of exact `/oauth2`, `/flow` and `/design` paths plus the
  `/.well-known` and `/gate` prefixes (`gateway.md`). The admin console, the management APIs, MCP and
  health are not routed.
  - The foundations loop re-creates the route if it's deleted.

### Logging in

The OIDC client is `serendip`: public, authorization code + PKCE. Its
redirect URIs are the fixed loopback `http://127.0.0.1:8250/callback` (for
a future CLI) and the web console's callback (below). ThunderID matches
redirect URIs exactly and imports its client definition on first install
only.

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

- **No authorization (as of spec 015).** Since spec 017, what a person
  can do depends on their role assignments; see `authorization.md`.
- **Plain HTTP.** Fine for a browser on `localhost`; anywhere else needs
  TLS first (a later spec). Browsers won't do OIDC over plain HTTP on other
  hosts anyway.
- **No revocation before expiry for bearer tokens.** They're verified
  offline, so a token stays valid until it expires (1 h by default). The
  console is different: logging out ends its session at once (below).
- **The audit actor is the username.** Since spec 017 each audit row
  also records the stable `sub` (`actor_subject`); rows from before that
  have only the username, so a renamed or reused one is ambiguous there.
  Names are cleaned of control characters and capped at 128 characters.
- **ThunderID's RSA signing certificate is valid for 365 days.** What
  happens at expiry is untested upstream; it's a follow-up.

## Console sessions (spec 016)

The web console logs people in through `serendipd` itself (ADR 0013).
Detail and evidence: `specs/016-ui-mvp/`.

### What the browser holds

- **Only an opaque session ID**, in the cookie `serendip_session`
  (`HttpOnly`, `SameSite=Strict`, `Path=/`, host-only; `Secure` once the
  public URL is https).
- **Tokens stay in `serendipd`'s memory.** The access, refresh and ID
  tokens never reach page JavaScript, browser storage, URLs or logs, so
  an XSS or a compromised npm package has no token to steal.
- **Session IDs are stored only as their SHA-256.**

### How a login runs

1. `GET /auth/login?return=<path>`. `serendipd` generates `state`, a
   nonce and a PKCE verifier, remembers them for 10 minutes (single use,
   at most 256 pending), sets a pre-login cookie scoped to `/auth`, and
   redirects to the identity provider.
2. The person signs in at the provider.
3. `GET /auth/callback`. `serendipd` checks that `state` matches the
   pre-login cookie, exchanges the code with the verifier, checks the ID
   token (signature, issuer, audience, expiry, nonce) and the access
   token, and requires both to name the same subject.
4. It creates a fresh session ID (no fixation possible) and redirects to
   the requested path. Only a same-origin path is accepted, so there's no
   open redirect.

Login routes are rate limited per client address. Errors go to
`/signin?error=<code>`, with a fixed code that never reflects the request.

### Using the API from the console

- **The cookie is a third credential** in `AuthMiddleware`, after API keys
  and bearer tokens. It resolves to the same `person:<username>` actor, so
  audit rows look the same whichever way a person came in.
- **CSRF protection.** Every cookie request must carry
  `X-Serendip-Csrf: 1`. Anything that changes state must also send an
  `Origin` exactly equal to the public URL. No CORS headers are ever sent.
  A request carrying both a bearer token and the cookie is refused.
- **`GET /v1/session`** says who the caller is and when the session ends.
  It never returns a credential.

### Expiry, refresh and removing someone

- **Expiry:** idle 30 minutes, absolute 12 hours, at most 1024 sessions.
  Sessions live in memory, so restarting `serendipd` signs everyone out.
- **Refresh:** the access token is renewed when it has under a minute
  left, **one refresh per session at a time**. ThunderID rotates refresh
  tokens and revokes the whole family when an old one is replayed, so
  concurrent refreshes would end the session. The rotated token is stored
  before anything else can fail.
  - A refused refresh ends the session.
  - An unreachable provider keeps it and answers 503.
- **Removing someone:** ThunderID has no "disabled" state. Delete the
  person in ThunderID; their next refresh fails, so console access ends
  within one access-token lifetime (≤ 1 hour).

### Logout

`POST /auth/logout` (with the CSRF checks):
- ends the session and clears the cookie;
- revokes the refresh token at the provider, best effort;
- answers with the provider's end-session URL, which the browser then
  visits to end the provider session. The URL carries no ID token.

### The console's address at the identity provider

The `serendip` client must list `<public URL>/auth/callback` as a redirect
URI and `<public URL>/` as a post-logout URI. The public URL is
`SERENDIP_PUBLIC_URL`, by default `http://localhost:<port>`. Plain http on
any other host needs `SERENDIP_ALLOW_INSECURE_ORIGIN=1`, because the
cookie would then cross a network in clear.

- **Fresh installs: automatic.** The bootstrap client already lists both
  URIs.
- **Every startup and re-ensure: a read-only check.** `serendipd` probes
  the provider's public authorize and logout endpoints with no credentials
  to confirm that both URIs are accepted. It logs `console login enabled`
  once they are.
- **An install that predates the console, or after changing
  `SERENDIP_PUBLIC_URL`: a one-time step by an operator.** `serendipd`
  doesn't make this change itself yet. The Service proxy strips the
  `Authorization` header the management API needs, and spec 018's
  channel (ADR 0015) is so far used only for reads. Until
  the step is done, the console reports the identity provider as
  unavailable and `serendipd` logs the missing URIs. API keys and bearer
  tokens are unaffected. The steps:

  ```sh
  # 1. A tunnel that keeps headers (leave it running).
  kubectl -n serendip-identity port-forward svc/thunderid-service 18090:8090

  # 2. In another shell: the admin password, then the registration.
  export SERENDIP_IDENTITY_ADMIN_PASSWORD="$(kubectl -n serendip-identity \
    get secret serendip-identity-admin -o jsonpath='{.data.password}' | base64 -d)"
  go run ./cmd/serendip-identity-redirects --public-url http://localhost:8080
  unset SERENDIP_IDENTITY_ADMIN_PASSWORD
  ```

  - `--public-url` is the new `SERENDIP_PUBLIC_URL`.
  - `--issuer` defaults to `http://identity.localhost:30080`; pass the
    issuer `serendipd` logs if it differs.
  - The command adds only missing URIs and never removes one. It prints
    the result, never the password or a token.
  - Within one re-ensure interval, `serendipd` logs `console login
    enabled`.

  Since spec 018, `serendipd` has a channel that could make this change
  itself (ADR 0015). Using it for the console URIs is left to the HTTPS
  spec, which changes the public URL anyway.

### Limits

- **No RBAC** (as of spec 015). Since spec 017, roles at scopes decide
  what each person can do; see `authorization.md`.
- **Plain HTTP works only for a browser on the same machine.** Remote
  browsers need the HTTPS spec.
- **Sign-in and sign-out are logged with the actor** but aren't audit
  records yet. The audit log covers mutations.

## Subjects and groups (spec 017)

Authorization identifies a person by their **subject** (`sub`), which
survives a renamed or reused username. The audit actor stays
`person:<username>` for readability, and each audit row also records the
subject (`actor_subject`).

- **Groups** come from the access token's `groups` claim: names, flat as
  sent, at most 100 of up to 128 characters each.
  - A malformed or oversized claim rejects the token rather than being
    trimmed, so which groups apply never depends on an arbitrary cut.
  - ID tokens never carry groups (T001 spike), so only the access token
    is read.
  - Group changes apply when the person's next token is issued: their
    next sign-in, or the console's session refresh.
- **ThunderID** puts `groups` in access tokens through the `serendip`
  client's token attributes. Fresh installs get it from the bootstrap
  file. Existing installs run `serendip-identity-redirects` once, which
  adds any missing token attributes and, with `--public-url`, the console
  URIs.
- **Known principals.** `serendipd` records each person and API key that
  authenticates, and each of a person's groups, at most once a minute per
  unchanged principal. This is what the console offers when assigning a
  role, and the fallback for Check access when the live directory can't be
  read.
  - It's a snapshot from sign-ins. Since spec 018, the live directory
    below is asked first, and this snapshot is the fallback.
- **The bootstrap administrator's subject** is ThunderID's fixed
  bootstrap user ID (`thunderid.AdminSubject`), made Owner and Secrets
  Officer at `/` on first start.

## The live directory (spec 018)

`core/identity.Directory` reads the identity provider's people and groups
as they are now. Its three methods:
- `People(query)`;
- `Groups(query)`;
- `GroupsOf(subject)`, a person's direct groups.

It answers `reconcile.ErrProviderNotReady` when it can't, and
`ErrUnknownSubject` for someone the provider doesn't hold.

- **How `serendipd` reaches ThunderID** (ADR 0015):
  - through a port-forward over the Kubernetes API
    (`providers/kubernetes` `ServiceDialer`, the kubelet's
    `v4.channel.k8s.io` WebSocket protocol), which keeps the
    `Authorization` header the Service proxy strips;
  - logged in as the bootstrap administrator. The password is read from
    its Secret per login and the token is kept in memory only.
  - Only reads are made.
- **Search:** ThunderID 1.0.1 filters by exact match only. So a search
  pages through at most 1,000 entries and returns at most 50 substring
  matches, with `truncated` set when it stopped early.
  - A person's entry uses ThunderID's user `id`, which is the token `sub`.
    It doesn't use `attributes.sub`, which holds the username.
- **The API:**
  - `GET /v1/authorization/directory?type=person|group&q=` and
    `GET /v1/authorization/principals/person/{id}/groups`.
  - Both are gated like `/principals`: the caller must be able to assign
    roles somewhere.
  - Each answer carries `source`: `live`, or `signedIn` when the directory
    couldn't be read within 10 s and the known-principals snapshot
    answered instead.
  - A person the provider doesn't hold is a 404.
  - Errors are logged in short form, never returned, since they can name
    pod internals.
- **The console:**
  - the Person and Group pickers search as you type;
  - Check access shows a person's live groups, labelled;
  - both say when they fall back to sign-ins.
- **Cost** (spec 018 T010):
  - about 7 ms per read on a warm tunnel;
  - about 39 ms when a new tunnel opens (idle ones close at 30 s);
  - about 300 ms for the admin login, which is cached.

## Agent identities (spec 018)

An AI agent has its own identity from a **trusted agent issuer**, an
OIDC provider other than the human one. The first is WSO2 Agent Manager's
ThunderID, run beside Serendip (`specs/018-agent-manager-poc/findings.md`).

- **Configuration:** `SERENDIP_AGENT_ISSUERS`, comma-separated
  `alias=issuer|audience[|maxLifetime]`, for example
  `am-dev=http://default-idp.amp.localhost:8080|https://serendip.local/api`.
  - **A malformed entry stops startup.** So do a duplicate alias or
    issuer, and an issuer that is also the human issuer. The collision is
    checked again at identity setup, comparing URLs without a trailing
    slash.
  - **The alias** is 1–32 characters of a–z, 0–9 and `-`.
  - **The lifetime** defaults to 1 h and is capped at 24 h.
  - The issuer's discovery document and keys must be reachable from
    `serendipd` by name. In the POC on Windows that needed a hosts entry,
    because Go doesn't resolve `*.localhost` subdomains there.
- **Routing:** `oidc.IssuerRouter` reads a token's `iss` without trusting
  it, only to pick the verifier for exactly that issuer. That verifier
  then checks everything, and no other verifier is ever tried.
- **Agent tokens must:**
  - say `grant_type: client_credentials`, so a person's login token from
    the same issuer is refused;
  - carry `iat`;
  - live at most the maximum lifetime, plus 30 s of leeway;
  - carry the exact audience;
  - have a printable `sub` of at most 256 bytes.

  Their groups are ignored: group names belong to their issuer.
- **In the other direction,** the human verifier refuses a
  `client_credentials` token, because a machine is never a person.
- **The principal and the audit actor:**
  - The principal is `agent` with ID `<alias>/<sub>`, so the same `sub`
    from two issuers is two agents.
  - The audit actor is `agent:<alias>/<sub>`. API keys may use neither
    `agent:` nor `person:`.
  - Every audit row also records the agent's sponsor in `actor_sponsor`.
    If the sponsor can't be read, the request gets 503 rather than being
    recorded without one.
- **What a valid token grants: nothing.** Every agent in the issuer can
  get a token for Serendip's audience. Only role assignments give access,
  and agents have their own guardrails (`authorization.md`).
- **Not done:** on-behalf-of delegation (an agent acting for a person),
  managing agent issuers through the API, and creating agents from
  Serendip.

## Open questions

- None outstanding. Thunder-as-default is covered by ADR 0006, which
  documents explicit fallback triggers to Keycloak rather than treating the
  default as permanent by inertia. Its first re-check (2026-10-17) kept the
  default. The project is now ThunderID at `github.com/thunder-id/thunderid`
  (it was `asgardeo/thunder` at 1.0.0).
