# ADR 0009: OpenBao as the default Secrets & PKI provider

Status: Accepted
Date: 2026-10-10

**Correction, 2026-10-10 (during spec 008 T007 real-cluster
verification)**: this ADR's Decision section originally named the
`file` storage backend for standalone mode. Real-cluster testing found
OpenBao 2.7.0 removed the `file` backend entirely (`unknown storage
type file` is a genuine, confirmed-upstream fatal startup error, not a
config mistake — OpenBao's own packaged default config hit the same
crash post-2.7.0, fixed in their own PR #4088 by switching to
`pebbledb`). This ADR's storage-backend references below are updated
to `pebbledb` — the documented non-HA, single-node durable replacement
for `file`, functionally equivalent for this ADR's purposes (still a
local-disk-backed, non-HA, single-node backend; no PVC/deployment-mode
decision below changes). Two further real findings from the same T007
pass, documented in full in `specs/008-openbao-secrets-foundation/
plan.md`: (1) the KV v2 REST API section below is correct on the wire
but incomplete — a production `Init`'d instance never auto-mounts a
KV v2 engine (only `-dev` mode does), so this spec also had to add an
explicit idempotent mount-enable step; (2) an intermediate attempt at
matching the container args to the official image's entrypoint
"exactly" turned out to double-bind the OpenBao listener and crash
every Pod on its first boot, confirmed on two independent clusters —
reverted to the minimal, non-duplicating args.

## Context

The Secrets & PKI capability (`docs/architecture/capability-model.md:40`,
`overview.md:68`) already names OpenBao as its default provider from the
founding design discussion. No dedicated `docs/architecture/secrets.md`
exists yet, and no ADR had formalized the choice or checked its
current state — this ADR does that before spec 008 commits to a design.

Current-state check (2026-10-10, via oss-researcher + independent
verification) before accepting:

- **Governance**: MPL-2.0, governed by the Linux Foundation + OpenSSF.
  Forked from HashiCorp Vault in December 2023 after Vault's license
  moved to BSL (Vault is now IBM-owned). This is a mature, multi-vendor
  governed project, not a single-maintainer risk — a materially
  different risk profile than Thunder (ADR 0006).
- **Maturity**: latest release v2.7.1. Vault's own decade-plus of
  production field history carries over substantially, since OpenBao is
  a fork, not a rewrite — most of the codebase predates the fork.
- **Kubernetes deployment**: official Helm chart, **StatefulSet-only**
  (no plain Deployment option). Three modes:
  - **dev**: in-memory, auto-unsealed, data lost on restart. Fine for
    a demo, wrong for anything this project would call "implemented."
  - **standalone**: file-storage backend, requires a PVC. Upstream's
    own docs say this is **not appropriate for production** (no HA),
    but it does persist real data across restarts.
  - **HA (Raft)**: multi-replica, production-grade, explicitly out of
    scope at this project's current single-node phase (same tier as
    ADR 0002's storage-progression precedent: start minimal, add HA
    later when a real multi-node profile needs it).
- **Init/unseal lifecycle**: standard Vault-style. A fresh instance
  must be initialized (produces N unseal key shares via Shamir's
  Secret Sharing + one root token), then unsealed (supply a threshold
  of key shares) before it serves requests. Re-seals on every restart
  unless auto-unseal is configured (cloud KMS integration — not
  applicable to a self-hosted single-node target).
- **KV v2 REST API**: plain HTTP+JSON. Write:
  `POST/PUT /v1/secret/data/<path>` with body `{"data": {...}}` and
  header `X-Vault-Token: <token>`. Read: `GET /v1/secret/data/<path>`,
  returns a double-nested `{"data": {"data": {...}, "metadata": {...}}}`
  shape. Delete/list follow the same plain-REST pattern. This is simple
  enough to hand-roll against the existing `providers/kubernetes.Client`
  pattern (spec 006) without a product-specific SDK.
- **Auth methods**: Kubernetes auth method exists (validates a
  ServiceAccount JWT via TokenReview) — relevant for a future
  workload-identity story, not needed for spec 008's foundation scope.
- **Safety-critical, version-specific**: `mlock` (prevents secret
  material from being swapped to disk) was **removed entirely in
  OpenBao 2.7** — any `disable_mlock` config directive is now a fatal
  startup error, not a no-op. Any config template this project ships
  must not carry that directive forward from older Vault-era examples.
- **Official Go client weight — independently verified, not just
  researched**: using the exact methodology ADR 0008 established
  (throwaway module + `go get` + minimal real usage + `go list -deps`,
  filtered against stdlib), `github.com/openbao/openbao/api/v2` pulls
  in roughly 20 real, non-stdlib packages for a program that does
  nothing but construct a client: a full HCL config-language parser
  (`hashicorp/hcl`), a full HTTP/2 stack (`golang.org/x/net/http2`,
  `hpack`, `idna`, `http/httpguts`), i18n text processing
  (`golang.org/x/text/{transform,bidi,norm}`), JWT/crypto
  (`go-jose/go-jose/v4`), plus `hashicorp/go-retryablehttp`,
  `hashicorp/go-sockaddr`, `mitchellh/mapstructure`,
  `cenkalti/backoff/v5`, and others — none of which plain KV v2
  REST+JSON calls need. This is the same category of problem
  `client-go` had (the reason spec 006 hand-rolled
  `providers/kubernetes.Client` instead of adopting it).

## Decision

Accept OpenBao as the default Secrets & PKI provider
(`capability-model.md`'s existing `secrets: default: openbao` stands;
this ADR formalizes it and sets the implementation constraints below).

- **Deployment mode: standalone (`pebbledb` storage backend + real
  PVC), not dev, not HA.** Dev mode's ephemeral in-memory storage would make the
  capability "too stubbed to call implemented" (constitution rule 6);
  HA/Raft multi-replica is explicitly deferred until a real multi-node
  profile exists to justify it, mirroring ADR 0002.
- **No Helm.** Construct the StatefulSet/Service/PVC/ConfigMap directly
  via the existing generic `providers/kubernetes.Client`
  (spec 006) — the same precedent spec 007 set when it bypassed even a
  StorageClass in favor of direct API object creation. Introducing Helm
  as a new tool/dependency is not justified by one provider's manifests.
- **Hand-roll a minimal OpenBao REST client** (plain HTTP+JSON+one
  header, scoped to KV v2 read/write and the init/unseal endpoints
  spec 008 needs) instead of taking a dependency on
  `github.com/openbao/openbao/api/v2`, on the same transitive-weight
  grounds `client-go` was rejected on.
- **Root token / unseal key custody (temporary, documented
  simplification)**: a single-share (1-of-1) unseal key plus the root
  token are generated at init time and stored in a root-only local
  file (mode 0600) alongside K3s's own kubeconfig — the same tier of
  simplification as spec 002's static API keys. This solves the
  chicken-and-egg problem of "where do you store OpenBao's own
  credentials before Secrets-as-a-capability exists to store anything
  for you." It is explicitly not a production-grade secret-custody
  story and must be documented as such, not silently normalized.

Because OpenBao is materially more mature than Thunder, this ADR does
not carry Thunder-style "stable release is same-day" re-evaluation
urgency. It still sets fallback triggers:

- **Trigger to revisit standalone mode**: once a real multi-node
  profile (`standard-5`+) exists, standalone's single-point-of-failure
  storage backend becomes a real gap — HA/Raft should be designed then,
  not assumed now.
- **Trigger to revisit root-token local-file custody**: once the
  Secrets capability itself is implemented (spec 009) and can represent
  its own bootstrap credential, or once an auto-unseal mechanism
  (cloud KMS, or a future TPM-backed story matching this project's
  edge-node target hardware) is in scope, replace the local-file
  approach rather than let it age into a permanent pattern.
- **Trigger to revisit the hand-rolled client**: if OpenBao's REST API
  surface needed grows substantially beyond KV v2 + init/unseal (e.g.
  PKI secrets engine, dynamic database credentials), re-check whether
  the official SDK's weight is still disproportionate to the surface
  used, the same way this ADR re-checked `client-go`'s.

## Consequences

- Spec 008 builds the OpenBao deployment foundation (StatefulSet/PVC/
  ConfigMap via `providers/kubernetes.Client`, init/unseal bootstrap,
  a minimal hand-rolled REST client for KV v2) — mirroring how specs
  005+006 were foundation work consumed by spec 007's actual capability.
  Spec 009 builds the real Secrets capability/resource kind/
  `SecretsProvider`/reconciler on top of spec 008's foundation.
- No OpenBao-specific type leaks into `core/` (constitution rule 1) —
  the eventual `SecretsProvider` interface (spec 009) is defined in
  `core/reconcile`, matching `StorageProvider`'s precedent
  (`provider-model.md`).
- `docs/architecture/secrets.md` should be written (or `storage.md`
  used as the template) once spec 008/009 are far enough along to
  describe the real shipped shape, not the aspirational one.
