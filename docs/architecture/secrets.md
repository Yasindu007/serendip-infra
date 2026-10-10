# Secrets

This document describes what spec 008 actually shipped: the OpenBao
deployment foundation for the Secrets & PKI capability
(`capability-model.md:40`). ADR 0009 remains the authoritative decision
record for *why* OpenBao and *why* these implementation constraints —
this doc adds the supporting detail ADR 0009 intentionally kept out of
scope (exact manifests, lifecycle mechanics, file paths).

**This is foundation only, not the Secrets capability itself** — same
split as specs 005+006 (Kubernetes access foundation) vs. spec 007
(the real Storage capability). There is no `Secret` resource kind, no
`SecretsProvider` core interface, and no reconciler yet. Spec 009
builds those on top of what's described here.

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

## Registration — foundation only

`wireOpenBao` registers the resulting `*openbao.Client` under the
capability name `"secrets"` in `core/provider.Registry`, gated the
same way the rest of `wireKubernetes` is (`SERENDIP_KUBECONFIG` unset
⇒ skipped, not failed). **This is reachable for a direct smoke-test
call only** — same spirit as spec 006's `nodeLister` proof-of-concept
before spec 007 gave it a real consumer. There is no `Secret` resource
kind, no `SecretsProvider` core interface in `core/reconcile`, and no
Reconciler driving it. `core/` has zero changes from this spec.

## Explicitly deferred to spec 009

- The `Secret` resource kind (`core/resource`).
- A `SecretsProvider` core interface (`core/reconcile`), mirroring
  `StorageProvider`'s precedent (`provider-model.md`).
- A `SecretReconciler` actually driving the capability.
- Kubernetes auth method / workload-identity integration (relevant to
  a future Identity/Thunder story).
- PKI secrets engine, dynamic database credentials, or any OpenBao
  engine beyond KV v2.
- HA/Raft mode, multi-replica, auto-unseal — deferred per ADR 0009
  until a real multi-node profile justifies it.

## Open questions

- **Resource requests/limits are an unverified guess**, not an
  OpenBao-official minimum — watch real RSS on T470-class hardware
  once `profiles/*/bench/` exists, not just "it started."
- **Mode-`0600` file permission is unverified on this Windows dev
  machine** (NTFS has no POSIX bits) — needs a real Linux run to close
  out `bootstrap_test.go`'s `runtime.GOOS`-gated assertion for real.
