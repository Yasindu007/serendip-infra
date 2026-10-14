# Plan: Spec 014 — Reconciliation hardening

Status: Draft — human approval required before implementation

No upstream product research is needed. Server-side apply is a stable,
documented core Kubernetes API (GA since 1.22), so nothing here depends on
OpenChoreo- or kgateway-specific behavior.

## Resolved design decisions

### 1. Early re-check: a typed "requeue after" result from `Reconcile`

- `Reconcile` keeps returning `error`. A new type in `core/reconcile`
  carries the request:

  ```go
  // RequeueAfter asks the engine to reconcile ref again after Delay. It is
  // not a failure: the ref is not added to the failed-retry set.
  type RequeueAfter struct{ Delay time.Duration }
  func (r RequeueAfter) Error() string
  ```

  Reconcilers return `RequeueAfter{...}` (which `errors.As` can find) when
  their resource is still converging. This needs no interface change and no
  new engine API. Every existing Reconciler keeps compiling and behaving the
  same, because they never return it.
- **The engine schedules the re-check itself.** It keeps a `due` map from ref
  to time, and `Run`'s `select` gains a timer for the earliest due ref, so
  everything stays on Run's single goroutine (`core/reconcile/CLAUDE.md`).
  An event or resync for the same ref doesn't cancel the schedule. A
  successful reconcile that doesn't ask again clears it.
- **Backoff is the engine's, not the reconciler's.** The first early re-check
  comes after 2 s, then 4, 8, 16, then capped at the resync interval, where
  it simply merges into normal resync. The reconciler's `Delay` is a
  minimum hint; the engine applies whichever is larger, its backoff or the
  hint. Backoff resets when the ref settles (a reconcile returns nil).

  A resource that never converges costs at most one extra reconcile every
  few seconds for about 30 s, then nothing beyond normal resync. It can't
  hammer the API.
- **What counts as converging:**

  | Kind | Converging |
  |---|---|
  | Component | `Ready=Progressing`, or `Reachable=Pending` |
  | Application | `Ready=Progressing` |
  | Volume | `Bound` False with a not-yet reason |

  Terminal reasons (`DeployFailed`, `InvalidSpec`, `Unsupported`,
  `RouteRejected`) and settled ones (`Deployed`, `Serving`, `Bound`) don't
  requeue. Secret has nothing to converge (its status is existence only), so
  it's unchanged.
- **The write-only-on-change idempotency rule is unaffected.** `RequeueAfter`
  is returned *after* `setState`, which still skips unchanged writes. An
  early re-check that sees no change makes no store write.

### 2. Periodic re-ensure: a foundations loop owned by the composition root

- `cmd/serendipd` runs a `foundations` loop, one goroutine beside the engine,
  only touching the cluster API. It re-runs the same ensure steps startup
  runs, in the same order:
  1. Gateway API CRDs;
  2. kgateway Gateway;
  3. default DataPlane;
  4. DataPlane gateway wiring;
  5. platform defaults.
- It runs every **5 minutes** by default (`SERENDIP_REENSURE_INTERVAL`; the
  verification uses 30 s). It's drift repair, not convergence, so minutes
  are right.
- **No-drift passes are reads only.** That's already true of every ensure
  step, and it becomes even simpler with server-side apply (decision 3).
- **Failures don't stop anything.** A failed pass is logged, and the affected
  capability's readiness is marked not-ready with the reason (decision 4).
  The next pass retries.
- **Not in the engine.** These aren't resources in the store. Putting them in
  `core/reconcile` would invent a fake resource kind or make the engine
  aware of providers. The composition root already owns exactly these calls.

### 3. Server-side apply for Serendip-owned objects

- **Client.** `providers/kubernetes.Client` gains:

  ```go
  // Apply server-side-applies obj at its item path under fieldManager.
  // force=true: Serendip is the source of truth for every field it sets on
  // objects it owns.
  func (c *Client) Apply(ctx context.Context, path string, obj Object, fieldManager string) (Object, error)
  ```

  It sends `PATCH <path>?fieldManager=serendip&force=true` with
  `Content-Type: application/apply-patch+yaml`. JSON is valid YAML, so there
  is no new dependency. The body must carry `apiVersion`, `kind`, and
  `metadata.name`/`namespace`, which `Object` already has.
- **Field manager:** `serendip` — one stable name for all providers.
- **Force on conflict:** yes. Serendip applies only fields it means to own on
  objects it created, and a conflict there means something else changed
  Serendip's field, which drift repair should undo. Fields set only by other
  managers (an operator's labels, controllers' status) aren't in Serendip's
  apply, so they're never touched. That is the whole point of server-side
  apply over merge patch.
- **What moves to apply.** The provider code creates these with
  create-if-missing or create-then-patch today:

  | Provider | Objects |
  |---|---|
  | `providers/storage/localpath` | PV, PVC |
  | `providers/secrets/openbao` | Namespace, ConfigMap, PVC, Service, StatefulSet |
  | `providers/application/openchoreo` | DataPlane (whole object incl. gateway wiring), cluster-gateway-ca ConfigMap, the 4 platform defaults, Project, ProjectReleaseBinding, Component, Workload |
  | `providers/gateway/kgateway` | GatewayParameters, Gateway |

  - Each ensure becomes "build the desired object, `Apply` it".
  - The hand-written drift checks and merge-patch builders go away:
    `workloadsEqual`/`workloadPatch`, `ensureObject[S]`, `containsJSON`, and
    `updateSpecIfDifferent`. The visibility-null special case goes too.
  - That's net code deletion. The API server does the diff.
- **"Did I change something?"** The Component's
  Progressing-right-after-a-change logic needs this. It compares
  `metadata.generation` before (a GET it already does) and after (the apply
  response). A spec-only change bumps generation; a no-op apply doesn't.
- **Not moving:**
  - Namespace-only label patching of `openchoreo-control-plane` stays a
    merge patch. Helm created that namespace, and Serendip owns one label.
    A single-field apply would also work, but this keeps Helm's metadata
    untouched in an obvious way.
  - The Gateway API CRD installer keeps its merge-patch, upgrade-only logic.
    It's version-gated by bundle annotation, not a desired-state object
    Serendip owns field by field. Upstream's own install docs use client-side
    apply.
  - Anything Helm installs stays Helm's.
- **Migration of existing objects** (created before this spec, by POST and
  merge patch). Their managed fields belong to managers like
  `serendipd`/`Go-http-client` with operation `Update`.
  - The first apply makes `serendip` co-own every field it applies; nothing
    is wiped.
  - Fields set by the old `Update` manager that Serendip no longer applies
    stay put once. They were never applied, so apply can't remove them.
  - That's an honest one-time limitation for clusters created under specs
    007–013. It's documented, with the manual cleanup command. New clusters
    are clean from the start.
  - Rewriting `managedFields` (kubectl's "csaupgrade") isn't worth its risk
    for a dev-stage project with no production clusters.
- **Idempotency at the storage level.** The API server doesn't persist an
  apply that changes nothing: `resourceVersion` and generation are
  unchanged. So "no cluster-side write on no drift" still holds. A request is
  made, but nothing is stored, and T008 verifies this. The request rate is
  one apply per owned object per reconcile, a handful every 30 s at edge-1
  scale. The benchmark re-run (T009) checks idle CPU didn't move.

### 4. Startup that degrades: a readiness registry and background setup

- **Readiness lives in `core/provider.Registry`**, which already maps
  capability names to providers and is in-memory and capability-agnostic.
  It gains:

  ```go
  type Readiness struct {
      Ready   bool
      Reason  string // provider-neutral, e.g. "SettingUp", "SetupFailed", "Ready", "RepairFailed"
      Message string
      Since   time.Time
  }
  func (r *Registry) SetReadiness(capability string, rd Readiness)
  func (r *Registry) Readiness() map[string]Readiness
  ```

- **API:** a new authenticated `GET /v1/readiness` returns per-capability
  readiness. It's a new path, not under `/v1/capabilities/...` or
  `/v1/providers/...`, which the generic resource handlers mount.
  - `/healthz` stays pure liveness. It's unauthenticated and meant for
    process supervisors.
  - The endpoint is read-only, so it raises no audit question.
  - It's API-first (constitution rule 7).
- **Background setup, per capability, with ordering.**
  - `wireKubernetes` fails fast only on configuration errors: an unreadable
    kubeconfig, a client build failure, `helm` missing.
  - Each capability's setup then runs in its own goroutine and retries with
    backoff: 5 s, doubling to a 2-minute cap, forever until shutdown. On each
    failure it logs and updates readiness.
  - Storage, secrets, and gateway are independent.
  - The application platform waits for the gateway (it needs the
    `GatewayEndpoint`), so it starts after the gateway's setup succeeds.
  - The HTTP server starts immediately. When everything is ready,
    `serendipd` logs `serendipd: all capabilities ready`.
- **Reconcilers are registered at startup, before setup finishes,** with a
  provider handle that can be not-ready.
  - `cmd/serendipd` holds one tiny lazy wrapper per capability interface
    (`StorageProvider`, `SecretsProvider`, `ApplicationPlatformProvider`)
    over an atomic pointer.
  - Until setup stores the real provider, every method returns the new
    sentinel `reconcile.ErrProviderNotReady`, a sibling of the existing
    `ErrNotConfigured`.
  - The wrappers live in the composition root, so neither providers nor
    `core/` change shape.
- **Reconciler handling of not-ready.**
  - Each reconciler maps `ErrProviderNotReady` to an honest Condition: reason
    `ProviderNotReady`, with a product-neutral message such as "the storage
    capability is still being set up".
  - It returns `RequeueAfter` so the resource catches up within seconds of
    setup finishing.
  - It's not a failure, so it never enters the failed-retry set.
  - A delete arriving while not-ready returns the error, so the existing
    failed-retry path completes the teardown later.
- **Secret value endpoints:** `api` maps `ErrProviderNotReady` to **503 Service
  Unavailable** (problem+json) for the value read/write calls, so callers
  learn "try again" rather than getting a 500.
- **Benchmark harness:** `cmd/bench-edge1` measures cold start to the new
  `all capabilities ready` line instead of `listening on`, which now comes
  first.

### 5. Retry and timeout values

| Thing | Value |
|---|---|
| Early re-check backoff | 2 s → doubling → capped at resync (30 s) |
| Background setup backoff | 5 s → doubling → capped at 2 min, no limit on attempts |
| Re-ensure interval | 5 min (`SERENDIP_REENSURE_INTERVAL`) |
| Fail-fast | kubeconfig unreadable/invalid, client build failure, `helm` not on PATH, invalid `SERENDIP_GATEWAY_NODE_PORT` |

Existing per-step waits (OpenBao's 90 s readiness, kgateway's 2-minute
programmed wait, Helm `--wait` timeouts) are unchanged. They now bound one
attempt instead of the daemon's life.

## Files touched

- **`core/reconcile`:**
  - `engine.go`: `due` scheduling, backoff, `RequeueAfter` handling.
  - New `requeue.go`: `RequeueAfter`, `ErrProviderNotReady`.
  - `application.go`, `volume.go`, `secrets.go`: return `RequeueAfter` while
    converging; map `ErrProviderNotReady` (`secrets.go`'s `Reconcile` maps it
    to `ProviderNotReady`).
  - Tests for each.
- **`core/provider/registry.go`:** `Readiness`, `SetReadiness`, `Readiness()`,
  plus tests.
- **`api/`:** `readiness_handler.go` (`GET /v1/readiness`), the
  `server.go` route, `problem.go` (`ErrProviderNotReady` → 503), plus tests.
  `api.NewServer` takes the registry.
- **`providers/kubernetes/client.go`:** `Apply`, plus tests.
- **Providers converted to `Apply`:** `providers/storage/localpath`,
  `providers/secrets/openbao/deploy.go`, `providers/application/openchoreo/{defaults,dataplane,provider,deploy}.go`,
  `providers/gateway/kgateway/gateway.go`. Each one's drift-check and patch
  code is deleted, and tests are rewritten against apply request bodies.
- **`cmd/serendipd/main.go`:**
  - fail-fast checks, then per-capability background setup with backoff and
    ordering;
  - lazy provider wrappers;
  - the foundations loop;
  - the `all capabilities ready` log line;
  - `SERENDIP_REENSURE_INTERVAL`.

  The wiring moves to a new `wiring.go` if `main.go` gets unwieldy.
- **`cmd/bench-edge1/`:** cold start waits for `all capabilities ready`.
- **Docs (T011):** `docs/architecture/provider-model.md` (server-side apply,
  readiness, lazy wrappers), `operations.md` or a new
  `docs/architecture/reconciliation.md` (early re-check, re-ensure, degraded
  startup), `application-platform.md`, `gateway.md`, `storage.md`,
  `secrets.md` (each "repaired on restart only" / "merge patch can't remove"
  line), `docs/PROGRESS.md`, and a bench write-up update.
- **No new dependency.**

## Risks

1. **The server-side apply refactor touches every provider.** It's the
   biggest change by line count, mostly deletions. Mitigations: each
   provider converts in its own task with its own unit tests, and T008
   re-runs spec 011/012's full real-cluster flows (deploy, expose, update,
   un-expose, delete, restart) before anything else.
2. **Force-apply takes fields from other managers**, but only fields Serendip
   itself declares. Known cases: Helm doesn't touch Serendip's objects, and
   the `openchoreo-control-plane` namespace stays merge patch (decision 3).
   T008 checks an operator-added label survives a Serendip apply.
3. **A concurrent goroutine beside the engine** (the foundations loop and
   setup goroutines). They touch only the cluster API and the registry
   (mutex-guarded), never the store. Run `go test -race` on the touched
   packages.
4. **Background setup changes the startup contract.** Scripts and tests that
   assumed "listening on" meant "fully installed" (the bench harness, the
   e2e scratch scripts) must wait for `all capabilities ready` or poll
   `/v1/readiness`. The harness is updated here.
5. **One-time migration limitation** (decision 3): fields an older
   `serendipd` set but this one no longer sets stay on pre-existing objects.
   It's documented with a manual cleanup command.

## Explicitly NOT done

Everything in spec.md's Scope-OUT, plus:

- No `managedFields` rewriting for migration (decision 3).
- No server-side apply for the namespace label or the CRD installer
  (decision 3).
- No readiness persisted to the store or exposed as a resource kind
  (decision 4: in-memory, rebuilt on every start, which is exactly what
  readiness is).
- No change to per-step waits (decision 5).
