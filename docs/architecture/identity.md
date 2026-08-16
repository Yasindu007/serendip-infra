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

## Open questions

- None outstanding. Thunder-as-default is now covered by ADR 0006, which
  notes Thunder (renamed ThunderID, repo moved to `asgardeo/thunder`) hit
  v1.0.0 same-day as the ADR and documents explicit fallback triggers to
  Keycloak rather than treating the default as permanent by inertia.
