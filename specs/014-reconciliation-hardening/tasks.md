# Tasks: Spec 014 — Reconciliation hardening

Ordered. `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). Run `contract-guardian` before committing anything under
`core/` or `providers/`.

Dependency sketch: T001 ∥ T002 ∥ T003 → T004 → T005 ∥ T006 ∥ T007 → T008 →
T009 → T010 → T011.

## T001 [P] — Engine: `RequeueAfter` + early re-check scheduling

- **Do**:
  - In `core/reconcile/requeue.go`, add `RequeueAfter{Delay}` and
    `ErrProviderNotReady`.
  - In `engine.go`:
    - add a `due` map and per-ref backoff;
    - make `Run`'s `select` include a timer for the earliest due ref;
    - in `reconcileOne`: on `RequeueAfter`, schedule max(hint, backoff),
      doubling from 2 s to the resync cap, without adding to `failed`; on
      nil, clear the schedule and reset backoff; any other error keeps
      today's failed-retry path.

  Everything stays on Run's goroutine.
- **Files**: `core/reconcile/requeue.go`, `core/reconcile/engine.go`,
  `core/reconcile/engine_requeue_test.go`.
- **Verify**: `go test -race ./core/reconcile/...`.
  - A reconciler returning `RequeueAfter` twice then nil is called at
    ~2 s, then ~4 s, then stops. Use a short test resync and fast backoff,
    injected via unexported test hooks or constructor options.
  - `RequeueAfter` never lands in the failed set.
  - A plain error still retries at resync.
  - The event handler still only enqueues. The existing engine tests pass.

## T002 [P] — Registry readiness + `GET /v1/readiness` + 503 mapping

- **Do**:
  - `core/provider.Registry`: add `Readiness`, `SetReadiness`, and
    `Readiness()`, mutex-guarded, with `Since` set on state change only.
  - `api/readiness_handler.go`: `GET /v1/readiness`, authenticated, returning
    `{capability: {ready, reason, message, since}}` sorted. Wire it in
    `server.go`, and have `NewServer` take the registry.
  - `api/problem.go`: map `reconcile.ErrProviderNotReady` to 503
    problem+json.
- **Files**: `core/provider/registry.go`, `registry_test.go`,
  `api/readiness_handler.go`, `api/readiness_handler_test.go`,
  `api/server.go`, `api/problem.go`, `api/problem_test.go`.
- **Verify**: `go test ./core/provider/... ./api/...`.
  - Readiness round-trips, and `Since` doesn't move on a same-state set.
  - The endpoint returns 401 without a key and the JSON shape with one.
  - `ErrProviderNotReady` maps to 503.
  - Existing API tests still pass.

## T003 [P] — `providers/kubernetes.Client.Apply`

- **Do**: `Apply(ctx, path, obj, fieldManager)` sends PATCH
  `?fieldManager=<m>&force=true` with `Content-Type:
  application/apply-patch+yaml` and a JSON body. It returns the applied
  object, and `ErrNotFound` on 404 (the parent namespace missing).
- **Files**: `providers/kubernetes/client.go`, `client_test.go`.
- **Verify**: `go test ./providers/kubernetes/...`. An `httptest` server
  asserts the method, both query params, the content type, the body
  carrying `apiVersion`/`kind`/`metadata`, and response decoding.

## T004 — Reconcilers: requeue while converging, honest not-ready

- **Do**:
  - `ComponentReconciler` and `ApplicationReconciler` return `RequeueAfter`
    after `setState` when converging, per plan.md decision 1's table.
    `VolumeReconciler` does the same while not yet Bound.
  - All four reconcilers (including Secret) map `ErrProviderNotReady` from
    the provider to Condition reason `ProviderNotReady`, with a neutral
    message, plus `RequeueAfter`.
  - Delete paths return `ErrProviderNotReady` as an error, so the
    failed-retry set handles them.
- **Files**: `core/reconcile/application.go`, `volume.go`, `secrets.go`, and
  their tests.
- **Verify**: `go test ./core/reconcile/...`. Table cases:
  - converging → `RequeueAfter` returned *and* state written once;
  - settled → nil;
  - terminal (`DeployFailed`, `InvalidSpec`, `RouteRejected`) → nil;
  - not-ready → `ProviderNotReady` Condition + `RequeueAfter`;
  - delete while not-ready → error.

  Idempotency tests still show no write on a no-drift re-check. Grep
  confirms no product names in `core/`.

## T005 [P] — Storage + Secrets providers → server-side apply

- **Do**: Convert `providers/storage/localpath` (PV, PVC) and
  `providers/secrets/openbao/deploy.go` (Namespace, ConfigMap, PVC, Service,
  StatefulSet) to build-desired-then-`Apply` with field manager `serendip`.
  Delete the replaced create/compare code. Readiness waits are unchanged.
- **Files**: those packages and their tests.
- **Verify**: `go test ./providers/storage/... ./providers/secrets/...`.
  - Each object is applied with the exact body and field manager.
  - A repeat ensure sends the same apply (idempotent request).
  - Error paths propagate.

## T006 [P] — OpenChoreo provider → server-side apply

- **Do**: Convert `providers/application/openchoreo`:
  - the DataPlane: one desired object including `clusterAgent` and gateway
    wiring, replacing `EnsureDefaultDataPlane` + `EnsureDataPlaneGateway`
    patching;
  - the cluster-gateway-ca ConfigMap;
  - the 4 platform defaults;
  - Project, ProjectReleaseBinding, Component, and Workload.

  Delete `workloadsEqual`, `workloadPatch`, `containsJSON`,
  `updateSpecIfDifferent`, and `getOrCreate`'s create-only use for owned
  objects. The "just changed" signal is generation before vs after.
  The namespace label stays a merge patch.
- **Files**: `providers/application/openchoreo/*.go` and tests.
- **Verify**: `go test ./providers/application/...`.
  - The apply bodies are exact for every object kind.
  - An exposure toggle produces a Workload apply *without* `visibility`
    (removal now happens through apply semantics, not explicit nulls).
  - Generation unchanged → no "rolling out"; generation bumped →
    `Progressing`.
  - All observation, reachability, and status-mapping tests still pass.

## T007 [P] — kgateway provider → server-side apply

- **Do**: Convert GatewayParameters and Gateway to `Apply` and delete
  `ensureObject[S]`. The bounded Programmed wait is unchanged.
- **Files**: `providers/gateway/kgateway/gateway.go` and tests.
- **Verify**: `go test ./providers/gateway/...`. The apply bodies are exact,
  including proxy labels. With no labels configured, the body has no
  `infrastructure.labels`. The Programmed wait tests still pass.

## T008 — `cmd/serendipd`: background setup, lazy providers, foundations loop

- **Do**:
  - Fail fast on configuration errors only: kubeconfig, client, `helm`
    present, `SERENDIP_GATEWAY_NODE_PORT`.
  - Build lazy wrappers for the `StorageProvider`, `SecretsProvider`, and
    `ApplicationPlatformProvider` interfaces (atomic pointer;
    `ErrProviderNotReady` until set).
  - Register every reconciler and the API's Secret reconciler immediately,
    against the wrappers.
  - Start the HTTP server, then per-capability setup goroutines with 5 s →
    2 min backoff. The application platform waits for the gateway's
    endpoint. Each success stores the real provider and sets readiness
    Ready; each failure sets SetupFailed with the error.
  - Log `serendipd: all capabilities ready` once all are Ready.
  - Run the foundations loop every `SERENDIP_REENSURE_INTERVAL` (default
    5m), re-running the ensure steps in order and updating readiness on
    failure.
  - Update `cmd/bench-edge1` to wait for `all capabilities ready`.
- **Files**: `cmd/serendipd/main.go` (and `wiring.go` if split),
  `cmd/serendipd/main_test.go`, `cmd/bench-edge1/serendipd.go`.
- **Verify**: `go build ./...`, `go vet ./...`, `go test -race ./cmd/...`.
  Unit tests cover:
  - the lazy wrapper returning `ErrProviderNotReady`, then delegating once
    set;
  - the backoff schedule;
  - ordering (app platform setup doesn't start before the gateway endpoint
    exists), using fakes.

  With `SERENDIP_KUBECONFIG` unset, `serendipd` still starts and serves
  `/healthz`, and `/v1/readiness` shows no Kubernetes capabilities.

## T009 — Pre-cluster gate

- **Do**: `go test -race ./...`, `go vet`, `gofmt`, `check-imports.sh`, and
  `contract-guardian` on the full diff:
  - `core/` neutrality;
  - the engine's single-goroutine rule;
  - provider isolation (the lazy wrappers are in `cmd/`);
  - the server-side apply force semantics;
  - honest not-ready Conditions.
- **Files**: none (gate).
- **Verify**: All green, `contract-guardian` PASS.

## T010 — Real-cluster verification (kind): acceptance criteria 2–6

- **Do**: On fresh kind clusters (the `urlshortener` cluster untouched):
  1. **Regression first.** Re-run spec 011/012's full e2e (deploy, expose,
     curl from the host, image update, un-expose, delete, cascade). All must
     still pass after the server-side apply refactor.
  2. **Faster status.** Run the spec 013 harness on a simulated-T470 node:
     time-to-`Serving` must be well under 30 s (target ≤ 10 s). Then a
     settled Component's re-checks: confirm with the store's audit rows and
     `resourceVersion` that no extra writes happen at idle.
  3. **Repair without restart**, with `SERENDIP_REENSURE_INTERVAL=30s`:
     `kubectl delete` the OpenChoreo `Environment`, then the shared Gateway.
     Both are recreated within about 30 s, and an exposed Component returns
     to `Serving` and answers curl, with no restart.
  4. **Removed fields.** On a Serendip-owned object, add a label with
     `kubectl apply --server-side --field-manager=serendip`, simulating a
     field an older Serendip set. Add another label with a different field
     manager. After the next reconcile or re-ensure, the first label is gone
     and the second survives.
  5. **Degraded startup.**
     - Stop the kind node container and start `serendipd`. The API serves,
       `/v1/readiness` shows capabilities not ready with reasons, and a
       Component created meanwhile shows `ProviderNotReady`.
     - Start the node again. Setup completes on its own, `all capabilities
       ready` is logged, and the Component converges to `Serving` without a
       restart.
     - Separately, a bad kubeconfig path fails fast.
  6. **Store/apply idempotency.** After a settled period, confirm owned
     objects' `resourceVersion` is unchanged across several reconcile and
     re-ensure passes.
  7. **Benchmark re-run.** Run the spec 013 harness twice under the same
     simulated-T470 constraints. Compare against spec 013's run 1 for idle
     memory per group, and confirm `serendipd` still passes rule 4. Record
     the results under
     `profiles/edge-1/bench/results/<date>-spec014-simulated-t470/`.
- **Files**: code fixes as needed; scratch scripts; bench results.
- **Verify**: Each item observed directly (`kubectl`, curl, API JSON, harness
  output), not inferred.

## T011 — Final reviews + docs

- **Do**:
  - Run `contract-guardian` (holistic) and `go-reviewer`, focused on engine
    scheduling correctness, goroutine safety of the background setup and
    foundations loop, and the server-side apply conversion. Re-read spec.md
    line by line against the code.
  - Docs sync [A]:
    - new `docs/architecture/reconciliation.md` (engine model: events,
      resync, early re-check, failed-retry, foundations loop, degraded
      startup, readiness API);
    - `provider-model.md` (server-side apply, field manager, migration
      note, lazy wrappers);
    - remove the now-false "repaired on restart only" / "merge patch can't
      remove" / "daemon exits if a provider isn't ready" lines from
      `application-platform.md`, `gateway.md`, `storage.md`, `secrets.md`;
    - update the bench write-up (`002-edge1-footprint.md`) findings 1–2 as
      addressed, linking the re-run;
    - `docs/PROGRESS.md`.
- **Files**: docs as listed.
- **Verify**: Both reviews PASS. No doc still describes the four fixed
  weaknesses as current behavior.
