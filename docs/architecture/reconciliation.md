# Reconciliation

This document describes how `serendipd` keeps real state in line with desired
state, as it exists after spec 014: the reconcile engine's triggers, early
re-checks while a resource converges, the foundations re-ensure loop, startup
that degrades instead of dying, and the readiness API. Constitution rule 3
(observe, diff, apply, verify) and rule 6 (honest status) are the rules this
implements. Per-capability detail is in `application-platform.md`,
`gateway.md`, `storage.md` and `secrets.md`; how providers write to the
cluster is in `provider-model.md`.

Code: `core/reconcile/engine.go` and `requeue.go` (engine),
`core/provider/registry.go` (readiness), `api/readiness_handler.go`,
`cmd/serendipd/{wiring,setup,lazy}.go` (composition root).

## Engine model

One goroutine (`Engine.Run`), sequential dispatch, a 10s timeout per
reconcile. Not concurrent: that is a later-scale concern, not a measured
problem at edge-1. A reconcile of a ref is triggered by:

| Trigger | Source | Notes |
|---|---|---|
| Event | A store write publishes on the bus | The bus handler only enqueues (non-blocking send, dropped if the queue is full); `Reconcile` never runs on the publisher's goroutine |
| Resync | Every 30s, re-lists each registered kind | Catches state no event announces; also re-enqueues the failed-retry set |
| Early re-check | The engine's own timer, set by `RequeueAfter` | Below |

### Failed-retry set

A `Reconcile` that returns a plain error puts its ref in a `failed` set. Every
resync retries the set alongside the listed refs, because a ref whose record
was deleted never appears in a list again: without this, a teardown that
fails once (provider unreachable during a delete) would orphan the external
state forever. Success removes the ref. `Reconcile` calls for deleted refs
run the provider's `Delete*`, so deletes that fail are retried until they
succeed.

### Early re-check: `RequeueAfter`

Upstream progress (a Pod becoming Ready, a route being accepted) is neither a
Serendip event nor a resync, so before spec 014 status caught up only at the
next 30s resync. A Reconciler whose resource is still converging now returns
`reconcile.RequeueAfter{}` (a typed error, found with `errors.As`):

- **Not a failure.** The ref is not added to the failed-retry set and nothing
  is logged as an error.
- **The engine owns the backoff**, per ref: 2s, then 4, 8, then 8s steps
  (the wait doubles but is capped at 8s), so re-checks land about 2, 6, 14,
  22 and 30s after the resource started converging. Once one resync
  interval (30s) has passed since then, the ref stops being scheduled early
  and simply rides the normal resync. Before the 8s cap the waits were 2, 4,
  8, 16 (re-checks at 2, 6, 14, 30s), and anything ready between 14s and 30s
  (e.g. a workload whose image isn't cached) was reported at 30s (spec 014's
  benchmark re-run). The reconciler's `Delay` is only a minimum hint; the
  engine uses the larger of its backoff and the hint.
- **What moves the backoff depends on the trigger of the reconcile that asked:**
  - an early re-check doubles it (a resource that keeps converging without
    change backs off);
  - an event resets it to the base and restarts the 30s window, because
    events come only from real store changes (reconcilers write only on
    change, or a user edited the resource), so the resource is moving and
    fast re-checks start over;
  - resync leaves it alone.

  Event and resync reconciles only ever pull a schedule earlier, never push
  it later. The reconciler's own status write is itself an event; resetting
  on it would have kept the backoff at 2s, and doubling on it (the first
  implementation) inflated it, which is why only early re-checks advance it.
- **Cleared** when a reconcile returns nil, or fails with a plain error (the
  failed-retry path takes over).
- Everything is on `Run`'s goroutine: one timer, re-armed each loop for the
  earliest due ref. Due refs are reconciled directly, not through the queue,
  so a full queue cannot drop a schedule.

A resource that never converges costs a handful of extra reconciles in its
first ~30s, then nothing beyond normal resync.

**What counts as converging** (decided per kind in `core/reconcile`, using
provider-neutral reasons):

| Kind | Converging |
|---|---|
| Component | `Ready` = `Progressing`, `WaitingForApplication` or `ProviderNotReady`; or `Ready` true with `Reachable=Pending` (a provider that reports reachability on Components; OpenChoreo's doesn't since spec 020) |
| Application | `Ready` = `Progressing` or `ProviderNotReady` |
| Deployment (spec 020) | `Ready` = `Progressing`, `ProviderNotReady` or `WaitingForComponent` |
| ProviderInstance (spec 020) | installing, or the platform not ready yet (`RequeueAfter` 15 s). Since spec 022 an install or removal runs as a background package job (`packagejob.go`, at most 30 min); a reconcile waits 3 s for it, then reads Installing and requeues |
| Build (spec 022) | phase `Queued` or `Running` (requeued until it finishes). With auto-build on, its Component also re-reads the branch head every 5 min |
| Volume | `Bound` false with reason `Pending`, or `ProviderNotReady` |
| Secret | `ProviderNotReady` (otherwise nothing to converge) |

Settled (`Deployed`, `Released`, `Serving`, `Bound`) and terminal
(`DeployFailed`, `InvalidSpec`, `Unsupported`, `RouteRejected`) outcomes do
not requeue, so the idle footprint is unchanged.

**Idempotency is unchanged.** `RequeueAfter` is returned after `setState`,
which still writes only when `{Status, Reason}` (or the endpoint list)
changes. An early re-check that sees no change makes no store write and no
audit row. If the status write itself fails, that error wins and takes the
failed-retry path.

## Foundations re-ensure loop

Some objects are not resources in the store: Gateway API CRDs, the shared
Gateway, the default DataPlane and its gateway wiring, the platform defaults.
Spec 014 re-ensures them on an interval, so a deletion or drift is repaired
while `serendipd` runs (before: only on restart). The loop belongs to
`cmd/serendipd`, not the engine: putting it in `core/reconcile` would invent
a fake resource kind or make the engine aware of providers.

- Interval: `SERENDIP_REENSURE_INTERVAL` (a Go duration, default `5m`). It is
  drift repair, not convergence, so minutes.
- **Gateway foundations** = Gateway API CRDs, then the Gateway (and its
  GatewayParameters). They start once the gateway's setup has succeeded, even
  while the platform's setup is still retrying.
- **Platform foundations** = DataPlane (including its gateway wiring), then
  the platform defaults. They start once the platform's setup has succeeded.
  Gateway first, since routes attach to it.
- A no-drift pass is reads plus server-side applies that store nothing
  (`provider-model.md`).
- **Failures are never fatal.** A failed pass is logged and the capability's
  readiness becomes `RepairFailed` with the error; the next pass retries, and
  a success returns it to `Ready`.

## Degraded startup

`serendipd` fails fast only on configuration it cannot work around: an
unreadable or invalid kubeconfig, a client build failure, `helm` missing from
`PATH`, and an invalid `SERENDIP_GATEWAY_NODE_PORT` or
`SERENDIP_REENSURE_INTERVAL`. With `SERENDIP_KUBECONFIG` unset it starts with
no Kubernetes-facing capabilities, as before.

Everything cluster-facing is set up in the background, after the HTTP server
and engine are already running:

- **Per-capability setup, independent**: storage, secrets and gateway each
  run in their own goroutine. Each retries on failure with a 5s backoff
  doubling to a 2-minute cap, forever until shutdown. The existing per-step
  waits (Helm `--wait`, OpenBao's 90s readiness, kgateway's 2-minute
  Programmed wait) are unchanged; they bound one attempt rather than the
  daemon's life.
- **Ordering**: the application platform waits for the gateway (it needs the
  `GatewayEndpoint`), then runs its own setup.
- **Lazy provider handles**: reconcilers are registered at startup, before any
  setup finishes, against thin wrappers in `cmd/serendipd/lazy.go` (one per
  `StorageProvider`, `SecretsProvider`, `ApplicationPlatformProvider`, and
  since spec 022 `BuildProvider`, over an atomic pointer). The build handle
  follows the builds package's readiness: set while it reports Ready,
  cleared otherwise. Until setup stores the real provider, every method returns
  `reconcile.ErrProviderNotReady`. `core/` and the providers do not change
  shape for this.
- **Reconciler handling**: `ErrProviderNotReady` becomes a Condition with
  reason `ProviderNotReady` and a neutral message ("the storage capability is
  still being set up"), plus `RequeueAfter`, so the resource catches up
  within seconds of setup finishing. It is not a failure. A delete that
  arrives while not ready returns the error, so the failed-retry path
  completes the teardown later. A Secret reports `Reachable=Unknown` with this
  reason.
- **API**: the Secret value endpoints map `ErrProviderNotReady` to
  `503 Service Unavailable` (problem+json), "try again" rather than a 500.
  `/healthz` stays 200 throughout.
- When every capability is `Ready`, `serendipd` logs
  `serendipd: all capabilities ready` (once; also logged immediately when no
  kubeconfig is set). `listening on` now comes first, so scripts and the
  edge-1 bench harness wait for the new line, or poll `/v1/readiness`.

## Readiness API

`GET /v1/readiness` (authenticated, read-only, so nothing to audit) returns
every capability's state:

```json
{"gateway": {"ready": false, "reason": "SetupFailed", "message": "attempt 3 failed, retrying in 20s: ...", "since": "..."}}
```

| Reason | Meaning |
|---|---|
| `SettingUp` | Not started (waiting for another capability) or an attempt in progress |
| `SetupFailed` | The last attempt failed; the message has the attempt number, retry delay and real error |
| `Ready` | Set up (and, after that, last re-ensure pass succeeded) |
| `RepairFailed` | Set up earlier, but a foundations re-ensure pass failed |

`since` moves only when `ready` or `reason` changes, so a retry loop
re-reporting the same state does not reset it. State lives in
`core/provider.Registry` (in-memory, mutex-guarded, rebuilt on every start,
which is exactly what readiness is). Capabilities today: `storage`,
`secrets`, `gateway`, `application-platform`. `GET /healthz` is unchanged:
unauthenticated process liveness for supervisors.

## Real-cluster verification (spec 014, kind v1.34.0, unthrottled 4-CPU Docker VM)

Before spec 020: Components deployed themselves then. Since spec 020 a Component is `Released` and its Deployments serve (`application-platform.md`).

- **Time-to-Serving.** Component create to `Deployed`/`Serving` with a cached
  image: 14.2s in 4 of 4 runs. OpenChoreo's own ReleaseBinding `Ready` came
  at 11-13s, so Serendip adds at most about 2.5s (was a flat 30s in spec
  013). A first measurement before an engine fix gave 12.6-28s, because the
  reconciler's own status-write events were doubling the backoff; fixed so
  only early re-checks advance it.
- **Idle.** 75s window, 20 owned objects: generation and `serendip` apply
  time unchanged, Component `resourceVersion` unchanged, 0 new audit rows.
  (DataPlane's `resourceVersion` moves from OpenChoreo's own status writes,
  not Serendip's.)
- **Repair without restart** (`SERENDIP_REENSURE_INTERVAL=30s`): a deleted
  shared Gateway was recreated, the Component returned to `Serving` and curl
  returned 200, no restart. OpenChoreo blocks deleting an Environment that the
  DeploymentPipeline references (finalizer, `DeletionBlocked`), so a plain
  delete never completes; after force-removing the finalizer the Environment
  was recreated in 12s.
- **Degraded startup.** With the cluster node stopped, `serendipd` started:
  `/healthz` 200, `/v1/readiness` showed each capability `SetupFailed` with
  the real error and retry count, Application `ProviderNotReady`, Secret
  `Reachable=Unknown`/`ProviderNotReady`, secret value write 503. Starting the
  node: setup completed on its own, Component `Serving`, curl 200, secret write
  200. Also seen live: OpenBao's 90s wait missed on a slow image pull (the
  spec 013 failure) and was retried after 5s, `serendipd` stayed up;
  cert-manager's Helm install hit the 5m timeout on a slow quay.io pull and
  succeeded on retry.
- **Recovery after an outage is resync-paced.** Resources created while the
  cluster was down for 3 minutes had backed off to the cap; the Component
  reached `Serving` 56s after `all capabilities ready`. The Application's
  first reconcile after setup waits for resync (its first status change then
  resets the backoff), and the Component's `WaitingForApplication` is not
  woken by its Application's event, so up to two resync intervals pass.

## Not done / deferred

- **Watching upstream objects** (informers on OpenChoreo, Gateway API or Pods)
  as an event source. Early re-checks while converging are the fix at edge-1
  scale.
- **Re-checking dependent resources when a capability becomes ready** (a
  "resync now" when setup succeeds) and waking Components when their
  Application changes: would shorten post-outage recovery (above) from up to
  about a minute to seconds. Small follow-up; not needed for correctness.
- **Concurrent reconciliation.**
- **Readiness as a resource kind** or persisted state; it is in-memory only.
- **A per-capability health dashboard** beyond `GET /v1/readiness`.

## Open questions

- None.
