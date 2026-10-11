# Tasks: Spec 011 — Application Platform capability

Ordered. `[P]` = parallel-safe within its block, `[A]` =
subagent-eligible (tests/docs). Run `contract-guardian` before
committing anything under `core/` or `providers/`.

Dependency sketch: T001 ∥ T002 → T003 (needs T002) ∥ T004 (needs T001)
→ T005 (needs T001-T004) → T006 → T007 → T008 → T009 → T010.

## T001 [P] — providers/kubernetes: `Patch` (JSON merge patch)

- **Do**: Add `Patch(ctx, path string, patch any) (Object, error)` to
  `Client`. It sends `PATCH` with `Content-Type:
  application/merge-patch+json` and returns `ErrNotFound` on 404. It
  decodes the returned object the same way `Get` does. This needs the
  shared `do` helper to accept a content type; keep `application/json`
  as the default so existing callers are unchanged (plan.md
  decision 8).
- **Files**: `providers/kubernetes/client.go`,
  `providers/kubernetes/client_test.go`.
- **Verify**: `go test ./providers/kubernetes/...`. The
  `httptest.Server` test asserts the method, the exact content-type
  header, the body pass-through, decoding of the returned object, and
  404→`ErrNotFound`. All existing client tests stay green.

## T002 [P] — core/resource: real `ComponentSpec`

- **Do**: `ComponentSpec{Image string; Ports []ComponentPort; Env
  map[string]string}`, `ComponentPort{Name string; Port int32}`, with
  lowerCamelCase json tags (`omitempty` on ports/env). `ApplicationSpec`
  stays an empty struct. Rewrite both doc comments to describe what the
  fields mean generically. No product names.
- **Files**: `core/resource/app.go`, `core/resource/app_test.go`.
- **Verify**: `go test ./core/...`. A JSON round-trip test confirms the
  exact wire field names (`image`, `ports[].name`, `ports[].port`,
  `env`) and that empty ports/env are omitted. Existing store tests that
  create Components still pass.

## T003 — core/reconcile: `ApplicationPlatformProvider` + two reconcilers

- **Do**: In `core/reconcile/application.go`, add:
  - the `ApplicationPlatformProvider` interface (plan.md decision 6);
  - the reason constants from decision 7;
  - `ApplicationReconciler`, which drives `EnsureApplication` and calls
    `DeleteApplication` in its `ErrNotFound` branch;
  - `ComponentReconciler`, which:
    - validates the spec (empty image → `InvalidSpec`);
    - resolves exactly one `OwnerRef` of kind `Application` (zero or
      multiple → `InvalidSpec`);
    - loads that Application (missing, or its `Ready` not True →
      `WaitingForApplication`, with no provider call);
    - calls `EnsureComponent`;
    - calls `DeleteComponent` in its `ErrNotFound` branch.

  Both reconcilers set one `Ready` Condition through the same
  write-only-on-`{Status,Reason}`-change `setCondition` shape as
  `VolumeReconciler`, reusing the existing `findCondition`/
  `upsertCondition` helpers. A provider error → `EnsureFailed`.
- **Files**: `core/reconcile/application.go` (new),
  `core/reconcile/application_test.go` (new).
- **Verify**: `go test ./core/reconcile/...`. Table-driven, using a fake
  provider and the real SQLite store (the `volume_test.go`/
  `secrets_test.go` fixture pattern). Cases:
  - each reason path for both kinds;
  - a deleted record calls `Delete*` with the right ID;
  - **idempotency**: a second reconcile with no drift produces no
    `ResourceVersion` bump and no new audit row (acceptance criterion
    5);
  - a Component whose Application isn't `Ready` never reaches the
    provider.

  Also: `bash .claude/scripts/check-imports.sh` is clean, and grepping
  `core/` for `openchoreo|OpenChoreo` finds nothing.

## T004 [P] — providers/application/openchoreo: name derivation + platform defaults

- **Do**:
  1. **Names** (`names.go`): `projectName(id)` = `app-<10 hex of
     sha256(id)>`, `componentName(id)` = `cmp-<…>`, plus helpers for
     the derived `ProjectReleaseBinding` (`<project>-development`) and
     `ReleaseBinding` (`<component>-development`) names, and the
     `serendip.dev/id`/`serendip.dev/name` labels (plan.md decision 3).
  2. **Fetch the real sample bodies.** Use **oss-researcher** to pull,
     verbatim, the `ClusterProjectType` and service-type
     `ClusterComponentType` spec bodies from v1.3.1's
     `samples/getting-started/all.yaml`. Also pull the Workload
     `container.env` wire shape from `api/v1alpha1` (plan.md risk 1).
     Do not guess either.
  3. **Defaults** (`defaults.go`): `EnsurePlatformDefaults(ctx, client)`
     idempotently ensures:
     - the `openchoreo.dev/control-plane=true` label on
       `openchoreo-control-plane`, via `Patch` only if it's missing;
     - `ClusterProjectType/serendip-default`;
     - `ClusterComponentType/serendip-service`;
     - `Environment/development` → `DataPlane/default`;
     - `DeploymentPipeline/serendip` (single environment).

     GET-then-Create for each, the same as `EnsureDefaultDataPlane`.
- **Files**: `providers/application/openchoreo/names.go`, `names_test.go`,
  `defaults.go`, `defaults_test.go` (all new).
- **Verify**: `go test ./providers/application/openchoreo/...`.
  - Names: deterministic, ≤14 chars, DNS-1123-valid, different for
    different IDs.
  - Defaults, `httptest.Server`-backed:
    - all absent → each created once, with the exact paths/JSON;
    - all present → zero Create/Patch calls;
    - label already set → no Patch;
    - an unexpected error from any step propagates, naming that step.

## T005 — providers/application/openchoreo: `Provider`

- **Do**: `Provider` / `NewProvider(client)` implementing
  `reconcile.ApplicationPlatformProvider` (`var _` compile-time check):
  - `EnsureApplication`: GET-or-create `Project` (`deploymentPipelineRef:
    serendip`, `type: ClusterProjectType/serendip-default`) and its
    `ProjectReleaseBinding` (`environment: development`). Map the
    binding's `Ready` condition.
  - `EnsureComponent`:
    1. If `Ports` is empty → not-ready, reason `Unsupported`.
    2. GET-or-create the `Component` (`owner.projectName`, `autoDeploy:
       true`, `componentType: ClusterComponentType/
       deployment/serendip-service`).
    3. GET-or-create the `Workload`. On an existing one, compare only
       image/env/endpoints and PATCH only on a real difference. Every
       port becomes an endpoint of type `TCP`, default visibility.
    4. GET the `ReleaseBinding`: 404 → `Progressing`; otherwise map its
       `Ready` condition per plan.md decision 7.
  - `DeleteApplication` / `DeleteComponent`: DELETE the Project /
    Component CR, with 404 treated as success.

  Upstream reason/message text is carried in the returned `message`
  only. Returned reasons are only ever the fixed neutral set.
- **Files**: `providers/application/openchoreo/provider.go`,
  `provider_test.go` (new).
- **Verify**: `go test ./providers/application/openchoreo/...`,
  `httptest.Server`-backed, table-driven:
  - fresh create → the exact CR JSON for all four kinds;
  - existing, unchanged Workload → **no PATCH**;
  - changed image → exactly one PATCH carrying only the changed fields;
  - every upstream `Ready` reason listed in plan.md maps to the right
    neutral reason;
  - missing ReleaseBinding → `Progressing`;
  - zero ports → `Unsupported` with no API calls;
  - delete 404 → nil;
  - a genuine API error is returned as `err`.

## T006 — Remove `Marker`, wire into `cmd/serendipd`

- **Do**: Delete `openchoreo.Marker` and its doc comment. Extend
  `wireOpenChoreo(ctx, client, registry, db, bus, engine)` (plan.md
  decision 9):
  1. `EnsurePlatformDefaults` after `EnsureDefaultDataPlane`, before
     `CheckReady`;
  2. `registry.Register("application-platform", openchoreo.NewProvider(client))`;
  3. a single Lookup + type-assert to
     `reconcile.ApplicationPlatformProvider`;
  4. bus-attached Application/Component stores;
  5. `engine.Register` for both kinds, with list funcs.

  Update the doc comment, which no longer describes a placeholder.
- **Files**: `cmd/serendipd/main.go`,
  `providers/application/openchoreo/deploy.go`.
- **Verify**:
  - `go build ./...` and `go vet ./...`.
  - Grep confirms no `Marker` references remain.
  - `serendipd` with `SERENDIP_KUBECONFIG` unset still starts and serves
    `/healthz`.
  - Creating a Component through the API in that mode just persists it,
    with no crash and no reconciler, since none is registered without a
    cluster.

## T007 — Pre-cluster gate: static checks + `contract-guardian` on `core/`

- **Do**: Run the full unit suite, the static checks, and one
  `contract-guardian` review of the T002/T003 `core/` diff and the
  T001/T004/T005 `providers/` diff before spending real-cluster time.
  This catches constitution problems (product names in reason strings,
  import direction) while they're cheap to fix.
- **Files**: none (gate).
- **Verify**: `go test ./...` green; `go vet ./...`, `gofmt -l .`, and
  `bash .claude/scripts/check-imports.sh` clean; `contract-guardian`
  PASS, with any findings fixed and re-reviewed.

## T008 — Real-cluster verification (kind): acceptance criteria 2-5

- **Do**: Create a fresh `kind` cluster with node image ≥1.34.0 (spec
  010's CEL-bug finding). **Do not touch the unrelated `urlshortener`
  kind cluster.** Then:
  1. Run `serendipd` with `SERENDIP_KUBECONFIG` set and confirm the
     platform defaults exist (`kubectl get clusterprojecttype,
     clustercomponenttype`, `kubectl -n openchoreo-control-plane get
     environment,deploymentpipeline`).
  2. **Settle every (unconfirmed) item in plan.md risk 1**, fixing the
     code here, not just noting a discrepancy:
     - namespace label;
     - single-environment pipeline accepted;
     - Workload env shape;
     - how to find the data-plane Deployment/Pod;
     - cell-namespace name length with the derived names.
  3. Through the HTTP API, create an `Application`, then a `Component`
     (owner ref → that Application, `image: nginx:1.27-alpine`,
     `ports: [{name: http, port: 80}]`, one env var).
  4. Confirm directly via `kubectl`:
     - the `Project`, `ProjectReleaseBinding`, `Component`, `Workload`,
       and the autoDeploy-created `ReleaseBinding` exist;
     - a real Pod reaches `Ready` in the cell namespace;
     - the env var is present in the Pod spec.
  5. Confirm through the API that the Component's `Ready` Condition is
     `True`/`Deployed`, and that it was `Progressing` (not `True`)
     before the Pod was ready (acceptance criterion 3).
  6. **Idempotency**: wait past a 30s resync and confirm the
     Component's `ResourceVersion` and audit-row count are unchanged
     (criterion 5).
  7. **Update**: change the image tag through the API and confirm the
     Workload is patched and a new Pod rolls out.
  8. **Delete**: delete the Component through the API and confirm the
     Pod, Workload, and Component CR are gone (criterion 4). Then
     delete the Application, cascading a second Component created for
     this step, and confirm the Project and cell namespace are gone.
  9. Record peak memory/CPU of the cluster as one dev-machine data
     point (plan.md risk 3), then tear the kind cluster down.
- **Files**: code fixes as needed; a scratch script in the scratchpad.
- **Verify**: all of the above observed directly against the real
  cluster, not inferred from unit tests (specs 007-010's discipline).

## T009 — Final acceptance pass + `contract-guardian` + `go-reviewer`

- **Do**: Run `go test ./core/reconcile/... ./providers/application/openchoreo/...
  ./providers/kubernetes/... ./api/... -v`. Re-check all 6 acceptance
  criteria against the test output and T008's results. Run `go vet`,
  `gofmt -l .`, and `check-imports.sh`. Then:
  - **`contract-guardian` (holistic):** confirm no product name in
    `core/` identifiers, comments, or Condition reasons/messages.
    Confirm both delete branches really call the provider. Confirm
    `Marker` is fully gone.
  - **`go-reviewer`:** idempotent reconcile patterns, merge-patch
    correctness, error wrapping, context propagation through ≤4
    sequential calls.
  - Re-read spec.md's Scope-IN line by line against the actual code
    (specs 002-010's lesson).
- **Files**: none (gate).
- **Verify**: all green, both reviews PASS, all 6 criteria individually
  confirmed.

## T010 [A] — Docs sync

- **Do**:
  - Rewrite `docs/architecture/application-platform.md`. It currently
    describes the foundation only; update it with:
    - the real object chain and the Application↔Project mapping;
    - the corrected CRD facts (no `Organization` CRD; namespace as org);
    - the neutral-reason table;
    - the deferred items (worker Components, ingress visibility,
      org/tenant mapping, the DELETE-retry gap).
  - `provider-model.md`: resolve the `ApplicationProvider` open question
    with the real `ApplicationPlatformProvider` shape.
  - `resource-model.md`: Component now has real fields.
  - Create `providers/application/openchoreo/VERSIONS.md` with the
    pinned chart versions, cert-manager, the upstream API version
    `openchoreo.dev/v1alpha1`, and which sample the defaults were copied
    from.
  - Update `docs/PROGRESS.md`.
- **Files**: the five docs above.
- **Verify**: no doc still says the Application Platform capability
  "does not exist yet". Every claim matches shipped code and T008's
  observed behavior, not plan.md's pre-verification hypotheses.
