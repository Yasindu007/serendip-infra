# Spec 009: Secrets capability (Secret resource kind, SecretsProvider, reconciler)

Status: Draft — human review required before plan.md

## Problem

Spec 008 built the OpenBao deployment foundation — a real, reachable,
init/unsealed OpenBao instance, registered under the `"secrets"`
capability in `core/provider.Registry` as a bare `*openbao.Client`, with
`ReadSecret`/`WriteSecret`/`DeleteSecret` already proven end-to-end
against a real cluster. But that `*openbao.Client` is reachable only for
a direct smoke-test call (same spirit as spec 006's `nodeLister` before
spec 007) — there is no `Secret` resource kind, no capability-shaped
`SecretsProvider` interface in `core/`, and no Reconciler. Nothing a
human or API caller can create, list, or delete today represents a
secret. This spec is spec 008's declared follow-on, same split as
specs 006→007: foundation, then the real capability.

**This spec's shape differs from specs 007/008 in one structural way,
not just a naming choice.** Every mutation through `core/store` writes
an unconditional, whole-resource JSON snapshot into the audit log's
`Before`/`After` columns (`core/store/sqlite/store.go`, confirmed by
reading the actual `Create`/`Update`/`Delete` code, not assumed) — there
is no per-field redaction mechanism anywhere in `core/audit` or
`core/store` today. If a secret's plaintext value ever lived in
`SecretSpec` (mirroring how `VolumeSpec.SizeBytes` is "desired state"),
it would be captured verbatim, forever, in that audit trail — the exact
outcome using a dedicated secrets engine instead of bare Kubernetes
Secrets is supposed to prevent. **The actual secret value must never be
representable in `Secret`'s `Spec` or `Status` at all.** This is a hard
structural constraint this spec's design must satisfy, not a style
preference — see Scope-IN below for what that implies about the API
shape, and Open Questions for what it leaves unresolved.

## Scope — IN

- **A new resource kind, `Secret`** (`core/resource`, `KindSecret`,
  `SecretSpec`, `SecretStatus`). No naming collision risk with the
  `"secrets"` capability name the way `Volume`/`Storage` had (spec
  007) — `Secret` (singular, a resource) and `secrets` (the capability)
  read unambiguously side by side. `SecretSpec` is a **reference only**:
  where in the Secrets provider this secret's value lives (a path/mount
  concept, `core/resource`-level — no OpenBao-specific field names or
  types; `providers/secrets/openbao` is the only place that know what a
  "mount" is), plus any non-sensitive metadata (e.g. a human
  description). It never carries, and structurally cannot carry, the
  secret's actual value. `SecretStatus` mirrors `VolumeStatus`'s
  embedded `Status`/`Conditions` shape — a Condition reporting whether
  this secret is currently reachable/synced through its provider, not
  whether its *value* matches anything (there is nothing in `Spec` to
  compare a value against).
- **`SecretsProvider` Go interface**, defined in `core/reconcile`
  alongside `SecretReconciler` (mirrors `StorageProvider`/
  `VolumeReconciler`'s placement) — capability-shaped, using only
  `core/resource.Secret` and plain Go types, no OpenBao concept leaking
  into `core/`. Covers: ensure-reachable/exists, write a value, read a
  value, delete. (Exact method signatures are plan.md's job — see Open
  Questions; the shape is new territory, not a direct copy of
  `StorageProvider`'s two methods, since value read/write is a distinct
  operation from the reconciler-driven existence/Condition tracking
  `Ensure` covers for `Volume`.)
- **A thin adapter in `providers/secrets/openbao`** implementing
  `SecretsProvider` by wrapping spec 008's existing `*openbao.Client` —
  translating a `resource.Secret`'s reference fields into the
  `(mount, path)` strings `ReadSecret`/`WriteSecret`/`DeleteSecret`
  already take. No new REST/wire-protocol code — spec 008's client
  already covers the wire level completely; this is composition, not a
  new client.
- **`SecretReconciler`** in `core/reconcile`: looks up `"secrets"` in
  `core/provider.Registry` (`cmd/serendipd`'s composition root does the
  type-assert, same as every prior capability), drives the
  existence/reachability Condition, and — learning spec 007's own
  documented lesson (`provider-model.md`'s breadcrumb) — correctly
  calls the provider's delete path when the Serendip-side `Secret`
  record is gone, not a silent no-op.
- **New `api/` surface for the secret *value*** — a deliberate
  deviation from specs 007/008's "no `api/` changes" precedent, required
  by this spec's own structural constraint above: since a `Secret`'s
  value cannot live in `Spec`, the existing generic
  `ResourceHandler[S,St]`/`mountResource` pattern (which reads/writes
  whole resources) cannot be how a value gets set or read. A separate,
  dedicated read/write path is needed that talks to the
  `SecretsProvider` directly rather than through `store.Update`. Exact
  routes/method shapes are plan.md's job (Open Questions).
- **Every value write (and, pending plan.md's decision, every value
  read) is still audited** per constitution rule 8 — "every mutation...
  is not optional" — but the audit record must capture *who did what to
  which secret, when*, never the value itself. Whether this reuses
  `core/audit.Record` with a deliberately redacted `Before`/`After` (or
  needs a narrow, explicit extension) is plan.md's job, but **some**
  audit trail for value access is in scope; skipping it outright is not
  an option constitution rule 8 allows.
- **`cmd/serendipd` wiring**: register `SecretReconciler` with the
  reconcile engine (gated the same way every `SERENDIP_KUBECONFIG`-
  dependent wiring already is), wire the new value-read/write route(s)
  into the API server.
- One proof-of-concept flow through the whole stack, against a real
  cluster (this session established `kind` as the preferred mechanism
  over hand-rolled WSL2 clones — see `docs/PROGRESS.md`): create a
  `Secret` through the ordinary resource API, write a value through the
  new value endpoint, read it back byte-for-byte equal, confirm directly
  against OpenBao's own API (bypassing Serendip) that the value actually
  lives where `SecretSpec`'s reference says it should, delete the
  `Secret` and confirm OpenBao's value is gone too.

## Scope — OUT (explicitly)

- **Secret rotation, versioning, leasing, or expiry** — KV v2 keeps
  multiple versions server-side regardless; this spec only ever reads/
  writes the latest version. Rotation policy is real future work, not
  invented speculatively now (constitution rule 10).
- **PKI secrets engine, dynamic database credentials, Kubernetes auth
  method / workload identity** — already out per ADR 0009 and spec
  008's own Scope-OUT; still out here.
- **Any `Application`/`Component` wiring that would inject a Secret's
  value into a running workload** — needs the Application Platform
  provider (OpenChoreo), which doesn't exist yet. Same reasoning, same
  precedent as spec 007 excluding Volume-mounting.
- **AI-initiated secret access** (constitution rule 5's safety
  hierarchy: AI never gets raw access, always flows through policy
  checks) — no AI operations engine exists yet to be the caller. Not
  reachable, therefore not built.
- **UI/CLI/MCP surfaces** — API only, same precedent as every prior
  spec; interface parity (constitution rule 7) is a standing future
  commitment, not something every individual spec re-proves.
- **Multiple concurrent `SecretsProvider` instances / per-Environment
  provider selection** — one default provider via the Registry, same
  as every capability so far; the registry's own replace-the-default
  mechanism (`provider-model.md`) already covers switching later
  without a code change.
- **A policy engine actually gating who may read/write which secret** —
  `core/policy` doesn't exist yet (constitution rule 5's "Policy check"
  step is aspirational platform-wide, not something this spec invents
  in isolation). `api/middleware.go`'s existing actor-extraction is the
  only access control available, same as every other resource kind
  today.

## Acceptance criteria

1. `go test ./core/reconcile/... ./providers/secrets/openbao/... ./api/...`
   green: `SecretReconciler`'s existence/Condition/delete-cascade logic
   (fixture-based, mirroring `volume_test.go`'s pattern, no real
   cluster), the `providers/secrets/openbao` adapter's
   `resource.Secret`→`(mount,path)` translation, and the new value-
   read/write API handler(s) (tested against a fake `SecretsProvider`,
   mirroring `api/`'s existing handler test pattern) are all
   table-driven-tested without a live OpenBao instance.
2. Against a real cluster (`kind`, per this session's established
   preference — see `docs/PROGRESS.md`): creating a `Secret` through
   the ordinary resource API succeeds and persists with no value present
   anywhere in its stored `Spec`/`Status`; writing a value through the
   new value endpoint, then reading it back through the same endpoint,
   returns byte-for-byte identical data; the value independently exists
   at the exact mount/path `SecretSpec`'s reference names, confirmed
   directly against OpenBao's own API (bypassing Serendip, same
   verification discipline as spec 008's own acceptance criteria).
3. **Security property, verified structurally and empirically**: no
   field on `SecretSpec` or `SecretStatus` is capable of holding the
   secret's plaintext value (confirmed by inspection of the Go types,
   not just by not populating one in a test); the `core/audit` row(s)
   generated by the value-write call in (2), inspected directly, do not
   contain the plaintext value anywhere in their `Before`/`After` JSON.
4. Deleting a `Secret` through the API removes the real value from
   OpenBao too (confirmed directly against OpenBao's API) — applying
   spec 007/`provider-model.md`'s documented lesson that a capability
   whose provider holds real external state must have its Reconciler's
   `ErrNotFound`/resource-gone branch actually call `Delete`, not a
   silent no-op copied from a passive-observer Reconciler.
5. Idempotency: reconciling an already-synced, unchanged `Secret` again
   produces no additional `Update` call, audit row, or `ResourceVersion`
   bump (mirrors specs 003/007's identical criterion for their own
   Reconcilers).
6. `go vet ./...`, `gofmt -l .`, `.claude/scripts/check-imports.sh`
   clean; `core/` (including the new `core/resource.Secret` and
   `core/reconcile.SecretsProvider`/`SecretReconciler`) imports nothing
   under `providers/`, and no OpenBao-specific (or any other product)
   name appears in any `core/` identifier, comment, or Condition
   reason/message string.

## Open questions (resolve during plan review)

- **Exact `SecretSpec`/`SecretStatus` field shape** — what the
  reference concept is called at the `core/resource` level (a path? a
  mount+path pair? something more generic that doesn't presuppose KV
  v2's two-part addressing?), and the exact Condition type name (e.g.
  `"Reachable"` vs. `"Synced"` — `"Synced"` may wrongly imply a value
  comparison that structurally can't happen here).
- **Exact `SecretsProvider` method signatures** — how many methods,
  whether "ensure reachable" and "write value" are the same call or
  two, and whether `ReadValue` belongs on this interface at all or
  should be a narrower, read-only interface the API handler depends on
  directly (smaller blast radius if a future provider can write but,
  for some reason, can't support arbitrary reads).
- **Exact value-read/write API shape** — candidate:
  `POST`/`GET /v1/secrets/{id}/value`, separate from the generic
  `GET/PUT/DELETE /v1/secrets/{id}` the `ResourceHandler[S,St]` pattern
  already provides for `Spec`/`Status`/metadata — confirm this split is
  right, and whether it needs its own `api/` handler type or can extend
  an existing one without entangling the two very different semantics
  (whole-resource CRUD vs. a provider pass-through with no store
  mutation of the value).
- **Whether reading a secret's value needs its own audit record** —
  writes clearly do (constitution rule 8); most secrets managers also
  audit reads (who accessed what, when) as a security-relevant event in
  its own right. Decide explicitly rather than defaulting to "only
  mutations are audited" without considering whether a read counts as
  one here.
- **Whether `core/audit.Record` needs a real extension** (e.g. an
  explicit "value redacted" marker) or whether passing `nil`/a fixed
  placeholder for `Before`/`After` on these specific calls is
  sufficient — resolve with a concrete decision in plan.md, informed by
  whichever API/audit shape the prior two questions land on.
- **Resync interval for `Secret`** — reuse `cmd/serendipd`'s existing
  30s/10s constants (specs 003/007's precedent) unless a concrete
  reason not to surfaces during planning.
