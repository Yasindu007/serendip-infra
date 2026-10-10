# Secrets

This document describes the Secrets & PKI capability
(`capability-model.md:40`) as it actually exists today: spec 008's
OpenBao deployment foundation (init/unseal, credential custody, KV v2
mount lifecycle, Service-proxy reachability) plus spec 009's real
capability built on top of it (the `Secret` resource kind, the
`SecretsProvider` interface, `SecretReconciler`, and the `api/`
value-read/write surface). ADR 0009 remains the authoritative decision
record for *why* OpenBao and *why* the foundation's implementation
constraints — this doc adds the supporting detail ADR 0009
intentionally kept out of scope (exact manifests, lifecycle mechanics,
file paths, and now the real capability's design).

The split below ("Deployment model" through "Service-proxy
reachability") is spec 008's foundation — same shape as specs 005+006
(Kubernetes access foundation) vs. spec 007 (the real Storage
capability). "The `Secret` resource kind" onward is spec 009, the real
capability.

## Deployment model

OpenBao runs as a **standalone** instance (ADR 0009: not `-dev`, not
HA/Raft) in the `serendip-system` namespace (shared with
`providers/storage/localpath`), built directly via the existing generic
`providers/kubernetes.Client` — **no Helm**, same precedent spec 007
set. `providers/secrets/openbao.Deployer.Ensure` idempotently
GET-then-Creates, in dependency order:

1. **Namespace** `serendip-system`.
2. **ConfigMap** `openbao-config` — one key, `server.hcl`:
   ```hcl
   storage "pebbledb" {
     path = "/openbao/data"
   }

   listener "tcp" {
     address     = "0.0.0.0:8200"
     tls_disable = "true"
   }
   ```
   Storage backend is **`pebbledb`, not `file`** — OpenBao 2.7.0
   removed the `file` backend entirely (`unknown storage type file` is
   a genuine fatal startup error, confirmed upstream via issue #4077/PR
   #4088). `pebbledb` is the documented non-HA, single-node durable
   replacement; nothing else about the deployment shape changes.
   `tls_disable = "true"` is safe here because the Service stays
   cluster-internal (below). **No `disable_mlock` directive at all** —
   OpenBao 2.7+ removed `mlock` entirely, and that directive is now a
   fatal startup error, not a no-op.
3. **PVC** `openbao-data` — 1Gi, `ReadWriteOnce`, no
   `storageClassName` set (picks the cluster's default, K3s's bundled
   `local-path`, standard `WaitForFirstConsumer` dynamic provisioning —
   untouched). This works here, unlike `providers/storage/localpath`
   bypassing the provisioner entirely: the StatefulSet's Pod is a real
   consuming Pod that triggers scheduling-driven binding, so no
   workaround is needed.
4. **Service** `openbao` — ClusterIP, port 8200. **Cluster-internal
   only** — no NodePort, no Ingress, nothing external in this spec.
5. **StatefulSet** `openbao` — 1 replica, image
   `quay.io/openbao/openbao:2.7.1`, mounts the ConfigMap at
   `/openbao/config` and the PVC at `/openbao/data`. Container `args`
   is just `["server"]` — relying entirely on the official image's
   entrypoint default (`bao server -config=/openbao/config "$@"`).
   This looks redundant but is load-bearing: an earlier version also
   passed `-config=/openbao/config` explicitly to "match the
   entrypoint exactly," which instead made the flag appear twice in
   the final command line and, since `-config` in directory mode
   scans/merges every config file in the directory, bound the TCP
   listener twice in the same process — a deterministic crash on first
   boot (`bind: address already in use`), reproduced identically on
   two unrelated clusters (K3s and `kind`). A `startupProbe`
   (`tcpSocket`, 2s period, 30 failure threshold — generous for
   PebbleDB's WAL replay on cold hardware) and a `preStop` (5s sleep,
   so an in-Pod container restart fully releases port 8200 before
   rebinding) mirror `openbao-helm`'s own convention for this class of
   restart race. Resource requests/limits (`100m`/`128Mi` requests,
   `250m`/`512Mi` limits) are a **conservative estimate, not an
   OpenBao-official minimum** — no such minimum exists for
   standalone/pebbledb mode; revisit once real T470 measurement exists
   (`profiles/*/bench/`), same "measure, don't assume" discipline ADR
   0006 applied to Thunder.

`Ensure` does not return once the StatefulSet object exists — it polls
`status.readyReplicas` (2s interval, 90s timeout) until the Pod is
actually ready. Without this, the caller's subsequent bootstrap call
always raced a Pod still pulling its image or replaying PebbleDB's WAL
and crashed on a "no endpoints available" 503.

## Init/unseal lifecycle

`providers/secrets/openbao.Bootstrap(ctx, client, credentialsPath)`
runs on every `serendipd` startup (`cmd/serendipd`'s `wireOpenBao`,
after `Deployer.Ensure`). It checks `/v1/sys/health` first, then
branches:

- **Fresh instance** (`!Initialized`): calls `Init(ctx, 1, 1)` — a
  **1-of-1 Shamir split** (ADR 0009's documented custody
  simplification: one unseal key share, one root token, no
  multi-party threshold). Writes the resulting credentials to
  `credentialsPath`, then unseals with the key just received.
- **Already-initialized instance**: reads `credentialsPath` instead of
  calling `Init` again. **A missing file here is a hard error, not a
  fallback** — there is deliberately no "reset and re-init" escape
  hatch; a human operator would need to wipe the PVC by hand to
  recover. This is the correct behavior for ADR 0009's custody model,
  not an oversight.

Either branch then calls `SetToken` and, if the instance currently
reports sealed, calls `Unseal` with the stored key. **Re-sealed after
a restart is the normal, expected case** — OpenBao always starts
sealed after a process/Pod restart unless auto-unseal is configured
(explicitly out of scope, ADR 0009) — not an error path. This is what
lets a restarted `serendipd` detect "already initialized, currently
sealed," skip `Init`, and unseal from the existing file; confirmed
end-to-end against a real `kind` cluster (kill-and-rerun `serendipd`
against the same OpenBao instance: no re-init, credentials file
unchanged, successful unseal).

## Credential custody

Root token + unseal key are written as JSON to a **root-only, mode
`0600`** file at a Serendip-owned path (not reused from K3s's own
`/etc/rancher/k3s/`):

- **Env var**: `SERENDIP_OPENBAO_CREDENTIALS_PATH`
- **Default**: `/var/lib/serendip/secrets/openbao-init.json`
- Parent directory is created mode `0700` if missing.

This follows the same `/var/lib/serendip/` state-root convention
`serendip-agent` already established (spec 004,
`SERENDIP_AGENT_STATE_DIR`) rather than inventing a new one. This is
**explicitly a temporary, documented simplification** (ADR 0009), the
same tier as spec 002's static API keys — not a production-grade
secret-custody story. ADR 0009's revisit triggers: once spec 009's
Secrets capability can represent its own bootstrap credential, or once
an auto-unseal mechanism (cloud KMS, or a future TPM-backed story) is
in scope, replace the local-file approach rather than let it age into
a permanent pattern.

The credential file's content must never be logged or echoed in an
error message anywhere in `providers/secrets/openbao` — `client.go`'s
`unexpectedStatus` only ever embeds OpenBao's own `{"errors": [...]}`
response body, which cannot contain a token or key (those only appear
in a successful `200` body from `Init`, never passed through that
helper).

**Known verification gap**: the file's mode-`0600` requirement is
exercised by `bootstrap_test.go` but gated behind
`runtime.GOOS == "linux"`, since NTFS (the current dev machine) has no
POSIX permission bits to check. The write path itself
(`os.WriteFile(path, buf, 0o600)`) is simple and untouched by anything
found during real-cluster testing, but a real Linux run (the eventual
T470/WSL target, or Linux CI) would close this out for certain.

## KV v2 mount lifecycle

A production (`Init`'d, non-`-dev`) OpenBao instance mounts nothing
but its built-in system backends (`sys/`, `cubbyhole/`, `identity/`)
— unlike `-dev` mode, which auto-mounts a KV v2 engine at `secret/`.
Without an explicit mount step, `ReadSecret`/`WriteSecret` 404 with
"no handler for route" despite matching the documented wire protocol
exactly.

`Client.EnsureKVv2Mount(ctx, mount)` (idempotent — `GET
/v1/sys/mounts` first, only `POST /v1/sys/mounts/<mount>` to enable if
absent) covers this. It is called **unconditionally from `Bootstrap`**,
on every startup, not just the fresh-init path — "initialized" is
permanent, OpenBao-side state, so an instance that reached
"initialized" on a prior `Bootstrap` call whose mount step then failed
would otherwise have no path back to this check on a later restart.
`DefaultKVMount = "secret"` is the only mount point this spec uses.

## Service-proxy reachability

`serendipd` runs outside Kubernetes (same model as `providers/
kubernetes` since spec 002) and OpenBao's Service is deliberately
cluster-internal only (no NodePort, no Ingress — spec 008's explicit
Scope-OUT). The two are reconciled by routing every OpenBao REST call
through the **Kubernetes API server's Service-proxy subresource**
(`/api/v1/namespaces/<ns>/services/<name>:<port>/proxy/<path>`) — a
stable, long-standing core Kubernetes API feature, not a new exposure
surface, since it rides the same authenticated connection
`wireKubernetes` already holds.

`providers/kubernetes.Client` exposes two accessors for this:

```go
func (c *Client) HTTPClient() *http.Client
func (c *Client) ProxyURL(namespace, service string, port int) string
```

`wireOpenBao` builds the OpenBao `Client` as
`openbao.NewClient(client.ProxyURL(openbao.Namespace,
openbao.ServiceName, openbao.ListenPort), client.HTTPClient())` —
reusing the already-TLS-configured, already-authenticated transport
instead of reconstructing kubeconfig-derived TLS config a second time.

This mechanism was the single biggest unverified assumption going into
spec 008 (nothing in this codebase had exercised a Service-proxy call
before). **Confirmed working end-to-end against a real `kind`
cluster**: `/v1/sys/health` via this path reported
`initialized=true, sealed=false`, and a KV v2 write/read round-trip
through the real client returned byte-for-byte identical data.

## Registration

`wireOpenBao` now registers a `*providers/secrets/openbao.Provider`
(not the bare `*openbao.Client` spec 008 registered — superseded, not
left alongside it) under `"secrets"` in `core/provider.Registry`.
`cmd/serendipd`'s composition root looks this up, type-asserts it to
`core/reconcile.SecretsProvider`, and injects it into
`SecretReconciler` — the same registry pattern every capability since
spec 006 uses, now with a real consumer.

## The `Secret` resource kind

`core/resource/secrets.go` adds `KindSecret` with one field:

```go
type SecretSpec struct {
    Path string `json:"path"`
}
```

`SecretSpec`/`SecretStatus` structurally cannot hold the secret's
value — not a style choice. Every `core/store` mutation writes an
**unconditional, whole-resource JSON snapshot** into the audit log's
`Before`/`After` columns, with no per-field redaction anywhere in
`core/audit`/`core/store` (confirmed directly against
`core/store/sqlite/store.go`). A value in `Spec`/`Status` would be
captured verbatim, forever, in the audit trail — exactly what a
dedicated secrets engine is supposed to prevent. `Path` is a
user-chosen, human-meaningful reference (e.g. `"myapp/db-password"`),
not derived from `Metadata.ID` the way `Volume`'s PVC name is. The KV
v2 "mount" concept stays out of `core/` entirely — it's a single
deployment-wide constant (`openbao.DefaultKVMount`), not a per-`Secret`
choice.

`SecretStatus` adds one field to the usual `Status`/`Conditions`:
`LastSyncedAt *time.Time`, set only by a successful value write so
that write has real `Status` content to audit (see "Audit" below).

## `SecretsProvider` and `SecretReconciler`

```go
type SecretsProvider interface {
    Exists(ctx context.Context, sec resource.Secret) (bool, error)
    WriteValue(ctx context.Context, sec resource.Secret, value map[string]any) error
    ReadValue(ctx context.Context, sec resource.Secret) (map[string]any, error)
    Delete(ctx context.Context, sec resource.Secret) error
}
```

**`Exists`, not `Ensure`** — a deliberate departure from
`StorageProvider`'s shape (`provider-model.md`): there's nothing
analogous to provision here (the KV v2 mount already exists, set up
once by spec 008's `Bootstrap`), so all a reconcile loop can honestly
do is observe whether a value exists and report that (constitution
rule 6). `Reconcile` is thin: `Exists` → one Condition, `"Reachable"`
(`True`/`"ValueExists"`, `False`/`"NoValueWritten"`, or
`Unknown`/`"CheckFailed"`) — named `"Reachable"`, not `"Synced"`,
since there's no desired value in `Spec` to compare against.

**`WriteValue`, `ReadValue`, `DeleteWithValue` are three separate,
synchronous methods on `SecretReconciler` — called directly by `api/`,
never from `Reconcile` or the async resync loop.** A secret's value
has no "desired state" to reconcile toward; routing it through the
async loop would only add latency and eventual-consistency surprise
for a caller who just wrote a value and expects to read it back
immediately. They live on `SecretReconciler` as a convenience (reuse
its `store`/`provider` fields), doc-commented as synchronous, not
reconcile-loop, concerns.

**Delete bypasses the reconcile loop entirely, unlike
`VolumeReconciler`'s.** `VolumeReconciler`'s `ErrNotFound` branch can
call `provider.Delete` with just the ref's ID because the PVC name is
*derived* from it; a `Secret`'s `Path` is not derived from anything,
so once the record is gone, `Path` is gone with it. External cleanup
must happen *before* the store record is deleted:
`DeleteWithValue` fetches the `Secret`, calls `provider.Delete`, then
`store.Delete` — so a failed provider delete leaves the record in
place for a retry rather than orphaning a live value in OpenBao.
`Reconcile`'s own `ErrNotFound` branch is consequently a plain no-op.

## `providers/secrets/openbao.Provider`

A thin adapter (`providers/secrets/openbao/provider.go`) wrapping spec
008's `*Client` — no new wire-protocol code. `Exists` calls
`ReadSecret`, mapping `ErrSecretNotFound` to `(false, nil)`;
`WriteValue`/`ReadValue`/`Delete` call `WriteSecret`/`ReadSecret`/
`DeleteSecret` against the single `DefaultKVMount`. Mirrors
`providers/storage/localpath.Provider`'s naming/placement exactly.

## `api.SecretHandler` and the value routes

A value cannot live in `Spec`/`Status`, so the generic
`ResourceHandler[S,St]` pattern can't set, read, or clean one up.
`api/secret_handler.go` adds `SecretHandler`: Create/Get/List/Update
delegate to an embedded `*ResourceHandler[...]` (reuse, not
reimplementation), plus:

```
POST   /v1/secrets/{id}/value   -> SecretReconciler.WriteValue
GET    /v1/secrets/{id}/value   -> SecretReconciler.ReadValue
DELETE /v1/secrets/{id}         -> SecretReconciler.DeleteWithValue (overridden)
```

**This is the first `api/` handler depending on a `core/reconcile`
type instead of only `core/store`** (`api/CLAUDE.md`'s prior status
quo) — because a value's read/write/delete lifecycle can't go through
the generic whole-resource CRUD pattern at all. Documented as a
one-off for `Secret`'s specific shape, not a new general rule; the
next capability should default back to `core/store` directly unless
it hits the same structural wall.

### A deliberate, bounded exception to constitution rule 3

Constitution rule 3 requires state changes to flow desired state ->
reconciliation engine -> provider -> verification, with "no imperative
side-channels that mutate infrastructure directly from an API
handler." `SecretHandler.WriteValue`/`.Delete` (via `DeleteWithValue`)
call the `SecretsProvider` synchronously from the HTTP request path,
bypassing the async reconcile engine — a `contract-guardian` review
flagged this directly, and it is real.

It is accepted as a **deliberate, narrow exception**, not a violation
to fix later: a secret's value has no "desired state" to reconcile
toward, so there's nothing for the engine to diff or converge, and
routing it through the async loop would add latency and
eventual-consistency surprise for a caller expecting to read back a
value they just wrote. The exception is scoped to exactly three
methods — `SecretReconciler.WriteValue`/`.ReadValue`/
`.DeleteWithValue` — not a general pattern other handlers should
follow. A future capability facing the same "no desired state" shape
should make the same case explicitly in its own plan.md, not assume
this precedent extends automatically.

## Audit: writes piggyback, reads don't

`WriteValue` sets `Status.LastSyncedAt` and the `"Reachable"`
Condition, then calls ordinary `store.Update` — that call is the
entire audit mechanism for value writes, no `core/audit` change
needed. `Before`/`After` are whole-`Secret` snapshots, safe because
the value never lives in `Spec`/`Status`; verified directly against
real `audit_log` rows on a `kind` cluster, no plaintext present.

**Value reads are explicitly not audited.** Piggybacking a `Status`
bump the same way would bump `ResourceVersion` on every `GET
.../value`, breaking optimistic concurrency for an unrelated
read-then-`PUT` metadata edit (a spurious `ErrConflict`). A correct
read-audit trail needs a genuinely separate, non-`ResourceVersion`-
affecting append-only path in `core/audit` that doesn't exist today,
with no concrete consumer asking for it — building it speculatively
would violate constitution rule 10. Recorded below as future work,
not a silent gap.

## Explicitly deferred

Spec 008's foundation-level deferrals, now narrowed by spec 009, plus
spec 009's own Scope-OUT:

- **Auditing value reads** — needs a genuine `core/audit` extension
  (see above); not built speculatively.
- **Secret rotation, versioning, leasing, or expiry** — KV v2 keeps
  multiple versions server-side regardless; this capability only ever
  reads/writes the latest version.
- **A policy engine gating who may read/write which secret** —
  `core/policy` doesn't exist yet; `ActorMiddleware`'s existing
  (intentionally weak) actor resolution is the only access control,
  same as every other resource kind today.
- **Kubernetes auth method / workload-identity integration** —
  relevant to a future Identity/Thunder story.
- **PKI secrets engine, dynamic database credentials, or any OpenBao
  engine beyond KV v2.**
- **`Application`/`Component` wiring that injects a Secret's value
  into a running workload** — needs the Application Platform provider
  (OpenChoreo), which doesn't exist yet.
- **AI-initiated secret access** — no AI operations engine exists yet
  to be the caller.
- **UI/CLI/MCP surfaces** — API only, same precedent as every prior
  capability.
- **Multiple concurrent `SecretsProvider` instances / per-Environment
  provider selection** — one default provider via the registry, same
  as every capability so far.
- **HA/Raft mode, multi-replica, auto-unseal** — deferred per ADR 0009
  until a real multi-node profile justifies it.

## Open questions

- **Resource requests/limits are an unverified guess**, not an
  OpenBao-official minimum — watch real RSS on T470-class hardware
  once `profiles/*/bench/` exists, not just "it started."
- **Mode-`0600` file permission is unverified on this Windows dev
  machine** (NTFS has no POSIX bits) — needs a real Linux run to close
  out `bootstrap_test.go`'s `runtime.GOOS`-gated assertion for real.
- **`DeleteWithValue`'s provider-delete-then-store-delete ordering is
  not atomic.** If the provider delete succeeds but the subsequent
  `store.Delete` fails (e.g. a future `graph.ErrHasChildren` once
  something can own a `Secret`, which nothing does today), OpenBao's
  value is gone while the Serendip-side record remains, pointing at
  nothing. Accepted as a real, narrow gap rather than a two-phase
  commit across two unrelated systems (constitution rule 10) — revisit
  if `Secret` ever gains children.
- **A concurrent edit racing `WriteValue`'s internal fetch-then-update**
  surfaces as an ordinary `ErrConflict`, same as any other optimistic-
  concurrency race in this codebase — not a new risk class, just a new
  call site for an existing, already-accepted one.
