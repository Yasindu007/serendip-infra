# Spec 015: Identity foundation (human login)

Status: Approved 2026-10-17 — plan.md next

## Problem

Identity is the last required capability of the PRD's POC scope with no
provider built (`PRD.md` "POC scope" item 3; `capability-model.md`:
Identity, required, default Thunder per ADR 0006). Today the only way to
call the Serendip API is a static API key from `SERENDIP_API_KEYS`
(spec 002). That mapping is set at process start, has no expiry, and names
whatever the operator typed. `secrets.md` calls this "intentionally weak"
and a temporary simplification.

It also blocks the UI. A browser console (`overview.md` "Experience":
Web UI) can't sensibly hold a static API key. It needs a person to log
in through a standard OIDC flow and send the API a short-lived token.

This spec builds the first slice of Identity: a running Identity provider
on edge-1, and **human identity** (`identity.md` §1) for the API. It
doesn't touch platform identity (§2) or workload identity (§3).

ADR 0006 is due its first re-check. Thunder, now ThunderID, reached 1.0.0
on 2026-08-15, and the ADR set a 2–3 month window to watch for
fall-back-to-Keycloak triggers. Research on 2026-10-17 (oss-researcher)
found no trigger fired:
- v1.0.1 shipped 2026-08-25 with no breaking changes.
- `main` is active daily.
- The repo moved again, to `github.com/thunder-id/thunderid`.
- No release tag has landed since 2026-08-25, which is worth watching as
  the window closes in mid-November.

## Scope — IN

- **ADR 0006 re-check.** Record the verdict on each fallback trigger. Update
  the repo URL and pin the version this spec deploys. Proceed only if no
  trigger fired.
- **The Identity provider running on edge-1.**
  - One instance of ThunderID with persistent storage (embedded SQLite on
    a volume, so no PostgreSQL), deployed and repaired by `serendipd`, in
    its own namespace.
  - It follows spec 014's model: background setup with retries, readiness
    reported under the `identity` capability, re-ensured by the
    foundations loop, and no fatal startup failure.
  - Its signing and encryption keys survive pod restarts and `serendipd`
    restarts. Losing them would invalidate every issued token.
- **A stable issuer URL reachable from the host.** The provider's public
  endpoints (OIDC discovery, authorize, token, JWKS) are exposed through the
  existing shared gateway (spec 012) under one fixed hostname. The issuer
  in tokens, the URL a browser is sent to, and the URL `serendipd` fetches
  keys from are therefore the same. Its admin console and management API
  are **not** exposed through the gateway.
- **Bootstrap, idempotently on first setup:**
  - An initial administrator person who can log in. Its credential is
    handed over through a root-only local file or a Kubernetes Secret, the
    same custody tier as OpenBao's init file (spec 008), never logged and
    never in the API.
  - A registered OIDC client for Serendip's own human-facing clients: a
    public client, authorization code + PKCE, with loopback redirect URIs.
    That's what the future UI and any future CLI will use.
  - `serendipd` stores whatever it needs to recognise its own client (the
    issuer and client ID) so a restart doesn't re-register it.
- **Human login accepted by the API, alongside API keys.**
  - The API accepts a bearer token that is a valid OIDC access token
    (or ID token, decided in plan.md) from the configured issuer.
    Validation checks signature (against the issuer's published keys),
    issuer, audience/client, and expiry.
  - The resolved caller becomes the audit actor (constitution rule 8)
    under a stable, provider-neutral form that says it's a person from the
    identity provider, distinguishable from an API-key actor.
  - Static API keys keep working unchanged. Every existing client, test,
    and the bench harness still uses them.
- **Provider-neutral verification.** Token verification speaks plain OIDC
  (discovery + keys), not anything specific to ThunderID. Pointing
  `serendipd` at any OIDC issuer (the planned `external-oidc` escape
  hatch, constitution rule 1) must need configuration only, no code. The
  ThunderID-specific parts are deploying it and bootstrapping the admin
  and client.
- **Readiness and degraded behaviour.** While the Identity provider isn't
  ready, `GET /v1/readiness` says so with a reason. API-key calls are
  unaffected, and a bearer token that can't be verified yet is rejected
  with a clear, honest error, never accepted.
- **Footprint measured.** The spec 013 harness reports the Identity
  provider as its own group, and is re-run on the simulated-T470 node.
  ADR 0006 requires measuring Thunder's footprint, not assuming "lighter
  than Keycloak", and constitution rule 6 forbids citing an unmeasured
  number.
- **Docs:** a new "Identity: what's built" section in `identity.md` (or a
  companion doc, decided in plan.md), the updated ADR 0006, and
  `api/CLAUDE.md`/architecture notes on the two auth paths.

## Scope — OUT (explicitly)

- **The UI.** This spec is its prerequisite. The UI MVP is its own spec,
  next.
- **A `serendip` CLI** and its `login` command. No CLI binary exists yet.
  The registered client is designed so one can use it later (auth code +
  PKCE on a loopback redirect; ThunderID has no device-code grant).
- **Authorization / RBAC / policy.** Any authenticated person gets the
  same full API access an API key has today. `core/policy` doesn't exist
  yet. This stays plainly documented, not implied away.
- **Managing users, groups or roles through the Serendip API.** People
  beyond the bootstrap admin are added in the provider's own console for
  now. A Serendip-level user model is a later spec.
- **Workload identity** (`identity.md` §3): the "Authentication: Enabled"
  Component, service-to-service identity, per-Component OIDC clients.
- **Enabling OpenChoreo's own security against this instance.** That's
  possible later (OpenChoreo takes an external OIDC issuer), but not now.
- **Keycloak or a working `external-oidc` provider registration.** The
  verification path is generic, but only ThunderID is deployed and tested.
- **HTTPS / TLS** on the issuer. The gateway serves HTTP today. A browser
  allows OIDC over plain HTTP only on `localhost`, which is the edge-1
  case. Remote browsers need the HTTPS spec first, and this limitation is
  documented.
- **High availability, PostgreSQL, Redis, multiple replicas.**
- **Retiring static API keys.**

## Affected resources and capabilities

- **Identity** capability: first real provider; registry entry; readiness.
- **Gateway** capability: one more route, owned by Serendip, for the
  issuer hostname.
- **API**: the authentication middleware gains the OIDC bearer path;
  audit actor format extended.
- **Audit** (`core/audit`): actor identity now has two sources.
- **Bench harness** (`cmd/bench-edge1`): new group.
- No new resource kinds. No change to Application/Component/Volume/Secret.

## Acceptance criteria

1. **ADR 0006** carries a dated re-check with a verdict per trigger and the
   pinned version. The repo URL is corrected.
2. **Fresh install on kind**: `serendipd` deploys the Identity provider,
   bootstraps the admin and the OIDC client, and logs
   `all capabilities ready` with `identity` ready in `/v1/readiness`.
   OIDC discovery is served at the fixed issuer URL from the host, and its
   `issuer` field equals that URL.
3. **A person logs in**: a real authorization-code + PKCE flow against the
   bootstrapped client, as the bootstrap admin, yields a token. That token
   calls `GET /v1/applications` and then creates an Application. The
   audit record for the create names the person, not an API key.
4. **Rejections**, each observed directly: no token (401). Expired token
   (401). Token signed by a different key (401). Token from another issuer
   or for another audience (401). Garbage (401). None of them leaks
   verification internals beyond a problem+json reason.
5. **API keys unchanged**: the existing API tests pass, and the spec
   011/012 e2e flow runs with an API key as before.
6. **Restarts**:
   - Restarting `serendipd` doesn't re-register the client or reset the
     admin.
   - Restarting the provider's pod keeps the same signing keys, so a token
     issued before the restart still verifies until it expires.
   - Deleting the issuer route is repaired by the foundations loop without
     a restart.
7. **Degraded startup**: with the cluster stopped, `serendipd` serves
   API-key calls, `/v1/readiness` reports `identity` not ready with a
   reason, and a bearer token is rejected with an honest "can't verify
   yet" error. When the cluster returns, identity becomes ready without a
   restart and the same token verifies.
8. **Provider neutrality**: `check-imports.sh` is clean. No product name
   appears in `core/` or `api/`. Pointing the verifier at a second,
   unrelated OIDC issuer in a unit test needs only configuration.
9. **Footprint**:
   - The bench harness re-run on the simulated-T470 node reports the
     Identity group's idle memory and CPU, and the 16 GB verdict is
     recomputed.
   - `serendipd` still passes rule 4.
   - The result is recorded under `profiles/edge-1/bench/results/` and in
     the bench write-up, labelled as a simulation.
10. `go vet ./...`, `gofmt -l .` clean; `contract-guardian` and
    `go-reviewer` PASS.

## Open questions (resolve during plan review)

- **How to install**: the official Helm chart via the existing Helm
  shell-out (ADR 0010's precedent for OpenChoreo), or direct manifests via
  `providers/kubernetes.Client` (ADR 0009's precedent for OpenBao). The
  chart's setup hook seeds the admin only at install time; plan.md weighs
  that against idempotent re-ensure.
- **JWT verification without a new dependency**: hand-rolled verification
  on the standard library (`crypto/rsa`, `crypto/ecdsa`, `encoding/json`)
  vs a library such as `go-oidc` (needs an ADR, and its transitive weight
  checked the ADR 0008 way). Leaning stdlib, as with the OpenBao client.
- **Access token vs ID token** as the API bearer, and which claim becomes
  the audit actor (`sub` alone is opaque; `sub` plus a display name such
  as `preferred_username` or `email`).
- **Issuer hostname**, e.g. `identity.<SERENDIP_GATEWAY_HOST>:30080`.
  The non-default gateway NodePort and the `*.localhost` default are
  inherited from spec 012. Pinning the issuer to the NodePort means
  changing the port later invalidates the issuer.
- **Admin credential custody**: the provider's own generated Secret vs a
  Serendip-written root-only file, and whether the bootstrap admin should
  be forced to change its password on first login.
- **Where the bootstrapped client ID lives**: in the provider's own
  storage (looked up by name on each start) vs `serendipd`'s SQLite store.
- **Key persistence**: confirm with oss-researcher, at plan time, exactly
  which files or config hold the signing and encryption keys and that
  they live on the persistent volume.
