# ADR 0006: Thunder (ThunderID) as the default Identity provider

Status: Accepted
Date: 2026-08

## Context
The Identity capability (human login OIDC/OAuth2, workload identity backing
— `docs/architecture/identity.md`, `capability-model.md`) needs a default
provider. The founding design discussion favored Thunder over Keycloak /
WSO2 Identity Server specifically for being Go-native, avoiding the
Java/JVM footprint that conflicts with constitution rule 4 (lightweight is
a feature). It explicitly flagged this as carrying real immaturity risk and
recommended a dedicated ADR before locking it in — which this is.

Current-state check (2026-08, via oss-researcher) before accepting:
- The project has been renamed **ThunderID** and moved from
  `wso2/thunder` to `github.com/asgardeo/thunder` (old repo now 404s).
- It hit **v1.0.0 on 2026-08-15** (today, as of this ADR) after a ~4-week
  alpha → beta → rc cycle starting late July 2026. The "stable" tag has
  **zero field track record**. Backlog is sizable relative to project size
  (599 open issues, 59 open PRs against 5,525 commits, 555 stars).
- WSO2 announced (May 2026) it is contributing ThunderID to the
  **OpenWallet Foundation** — reduces long-term single-vendor risk, but
  governance/repo location is mid-transition right now.
- Feature coverage is adequate for our use: OAuth 2.1/OIDC with PAR+PKCE,
  Authorization Code and Client Credentials flows, WebAuthn/passkeys,
  identity federation. Covers human login and basic workload identity via
  OIDC client provisioning; not a full SPIFFE/SVID-style federation stack.
- **No documented resource footprint anywhere.** Marketing claims "minimal
  memory footprint vs Java" with no figures. Cannot be treated as verified.
- Notable, not disqualifying: OpenChoreo itself uses Thunder for dev/local
  identity but routes its own enterprise multi-tenant auth to WSO2
  Asgardeo (hosted SaaS) because Thunder currently lacks
  centralized-directory/multi-tenant features. Serendip's edge-1/small-3
  target profile does not need that today, but standard-5+ might.

## Decision
Accept Thunder (ThunderID) as the default Identity provider
(`capability-model.md`: `identity: default: thunder`, already listing
`keycloak` and `external-oidc` as registered alternatives — this ADR does
not change that list, it just formalizes the default and its conditions).

Because the stable release is same-day as this ADR, the default carries
explicit fallback triggers rather than being treated as settled-forever:

- **Trigger to re-evaluate Keycloak as default**: no security patch or
  release activity within 2-3 months of 1.0.0, a repeated pattern of
  breaking changes post-1.0, or the OpenWallet Foundation transfer
  stalling the repo for an extended period.
- **Multi-tenant / centralized-directory needs** (relevant from
  `standard-5` upward): if Serendip needs what Thunder currently lacks,
  Keycloak is the fallback for those profiles specifically, not
  necessarily a platform-wide default change — `edge-1`/`small-3` can stay
  on Thunder while `standard-5`+ documents a Keycloak path, mirroring how
  OpenChoreo itself splits dev-local (Thunder) from enterprise (Asgardeo).
- **Resource footprint must be measured, not assumed**: `profiles/*/bench/`
  must record actual Thunder RSS/CPU on reference hardware before treating
  "lighter than Keycloak" as verified. Until measured, do not cite a
  specific number in any user-facing capability report (constitution rule
  6 — honest capability reporting extends to resource claims).

## Consequences
- Human login, and workload OIDC client provisioning
  (`docs/architecture/identity.md`) are implemented against Thunder behind
  Serendip's Identity provider interface; no Thunder-specific type leaks
  into `core/` (constitution rule 1).
- Keycloak stays a first-class registered alternative, not an afterthought
  — its JVM footprint means a Keycloak fallback should target
  `standard-5`/`small-3`, not `edge-1` (mirrors ADR 0005's OpenChoreo
  Build Plane treatment: heavier options are opt-in as profile budget
  allows).
- This ADR should be revisited explicitly once Thunder has a few months of
  post-1.0 field history, rather than left to silently age into
  "permanent default by inertia."
