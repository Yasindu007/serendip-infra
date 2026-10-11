# Tasks: Spec 012 — Gateway capability

Ordered. `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). Run `contract-guardian` before committing anything under
`core/` or `providers/`.

Dependency sketch: T001 (ADR, must be Accepted first) → T002 ∥ T003 ∥ T004
→ T005 (needs T002, T003, T004) ∥ T006 (needs T002, T004) → T007 → T008 →
T009 → T010 → T011.

## T001 [A] — ADR 0011: kgateway as the default Gateway provider

- **Do**: Write `docs/decisions/0011-kgateway-as-gateway-provider.md`,
  following ADR 0009's structure. Cover:
  - governance and maturity: CNCF Sandbox 2025-03, Apache-2.0, release
    cadence, two minors patched in parallel;
  - the version pin, kgateway v2.4.6, and the forced Gateway API bump to
    v1.6.1 (with the version matrix);
  - the install mechanism: first-party OCI Helm charts. Argue it on its
    own merits, citing ADR 0010's reasoning, not inheriting it;
  - experimental features disabled;
  - NodePort exposure, and why Traefik is left alone;
  - footprint: unknown upstream, measured in T009 and appended to the ADR;
  - risks: Sandbox maturity, minor churn, Gateway API support slipping per
    minor, vendor concentration (unconfirmed);
  - fallback conditions: Envoy Gateway, or Cilium Gateway API once a
    Cilium provider exists.

  Status: Proposed, then **Accepted only on the user's approval**, together
  with this plan.
- **Files**: `docs/decisions/0011-kgateway-as-gateway-provider.md`.
- **Verify**: the ADR exists, every unconfirmed fact is marked as such, and
  the user has explicitly accepted it before T005 starts.

## T002 [P] — Extract shared `providers/internal/helm`

- **Do**: Move `providers/application/openchoreo/helm.go` and its test to
  `providers/internal/helm`. Rename the package, export the runner type
  and the availability check, and keep behavior byte-for-byte. Update
  `openchoreo/deploy.go` and its tests to use it (the fake-runner
  injection still works).
- **Files**: `providers/internal/helm/{helm.go,helm_test.go}` (moved),
  `providers/application/openchoreo/{deploy.go,deploy_test.go}`.
- **Verify**: `go test ./providers/...` green with unchanged assertions.
  `bash .claude/scripts/check-imports.sh` clean. `go build ./core/...
  ./api/...` still passes with `providers/application` temporarily moved
  aside, to confirm the removability rule.

## T003 [P] — `providers/internal/gatewayapi`: v1.6.1 pin + upgrade-only semantics

- **Do**:
  1. Move `providers/application/openchoreo/gatewayapi.go` and its test
     to `providers/internal/gatewayapi` (export `EnsureCRDs`).
  2. Download Gateway API **v1.6.1** `standard-install.yaml` (fall back to
     the latest 1.6.x if it doesn't exist) into the scratchpad. Record its
     sha256 and its exact CRD name list, then update the constants.
  3. Replace "create if missing" with upgrade-only semantics:
     - read each existing CRD's `gateway.networking.k8s.io/bundle-version`
       annotation;
     - missing → create;
     - older than the pin (semver compare) → merge-patch metadata
       labels/annotations and `spec` to the pinned definition;
     - equal or newer → leave alone.

     The download is skipped only if every pinned CRD exists at ≥ the pin.
  4. Point `cmd/serendipd` at the new package; T007 finalizes the call
     order.
- **Files**: `providers/internal/gatewayapi/{gatewayapi.go,gatewayapi_test.go}`
  (moved), `cmd/serendipd/main.go`.
- **Verify**: `go test ./providers/internal/...`, using the fakeAPI
  pattern. Cases:
  - absent → created;
  - older bundle-version → exactly one PATCH carrying the new
    annotation/spec;
  - equal → no write;
  - newer → no write;
  - all at pin → no download (fetch func not called);
  - one older → download happens;
  - checksum mismatch → error;
  - existing separator/size tests still pass.

## T004 [P] — `core`: exposure field, endpoint status, Gateway interface, observation struct

- **Do**:
  - `core/resource/app.go`: add `ComponentPort.Expose bool`
    (`expose,omitempty`), `ComponentEndpointStatus{Name, URL}`, and
    `ComponentStatus.Endpoints`.
  - New `core/reconcile/gateway.go`: `GatewayEndpoint` and
    `GatewayProvider` (plan.md decision 4).
  - `core/reconcile/application.go`:
    - `EnsureComponent` now returns `(ComponentObservation, error)`, with
      the `Reachability` type;
    - add the `Reachable` Condition and its reasons (`Serving`/`Pending`/
      `RouteRejected`);
    - `ComponentReconciler` writes `Ready` + `Reachable` + `Endpoints`;
      when nothing is exposed it removes `Reachable` and clears
      `Endpoints`;
    - the write happens only when either Condition's `{Status, Reason}` or
      the `Endpoints` list changes.

  Update `ApplicationReconciler`'s test fake and every caller. No product
  names anywhere.
- **Files**: `core/resource/app.go`, `core/resource/app_test.go`,
  `core/reconcile/gateway.go` (new), `core/reconcile/application.go`,
  `core/reconcile/application_test.go`.
- **Verify**: `go test ./core/...`. Table cases:
  - not exposed → no `Reachable`, no endpoints;
  - exposed + serving → `Reachable=True/Serving` + URLs;
  - exposed + pending → `False/Pending`, no URLs;
  - rejected → `False/RouteRejected`;
  - un-exposing → `Reachable` removed, endpoints cleared;
  - **idempotency**: a repeat with identical observation produces no
    `ResourceVersion` bump and no audit row; a changed URL list causes one
    write.

  JSON wire test for `expose` and `endpoints`. `check-imports.sh` clean;
  grep confirms no product names in `core/`.

## T005 — `providers/gateway/kgateway`: Deployer + `EnsureGateway`

- **Do**:
  1. `helm show values oci://cr.kgateway.dev/kgateway-dev/charts/kgateway
     --version v2.4.6`: confirm the extra-env values key and that the chart
     creates GatewayClass `kgateway`. Record both in `VERSIONS.md`.
  2. `Deployer.Ensure`: the two `helm upgrade --install` calls from plan.md
     decision 9, via `internal/helm`, with `--wait`.
  3. `Provider{client, host, nodePort}.EnsureGateway`, implementing
     `reconcile.GatewayProvider` (`var _`):
     - get-or-create, patching on difference, `GatewayParameters/
       serendip-external` (NodePort, `ports: [{port: 80, nodePort}]`);
     - get-or-create `Gateway/serendip-external` (class `kgateway`, HTTP
       listener 80, `allowedRoutes.namespaces.from: All`,
       `infrastructure.parametersRef`);
     - bounded wait (2 min / 2s, both overridable for tests) for
       `Programmed=True`;
     - return `GatewayEndpoint`.

     A wait timeout returns an error naming the condition and, if a
     NodePort conflict is visible in events or status, the
     `SERENDIP_GATEWAY_NODE_PORT` hint.
- **Files**: `providers/gateway/kgateway/{deploy.go,deploy_test.go,gateway.go,gateway_test.go,VERSIONS.md}`
  (new).
- **Verify**: `go test ./providers/gateway/...`. A fake helm runner checks
  exact args and order, and that a CRDs failure stops before the main
  chart. An `httptest`-backed client covers:
  - fresh → exact GatewayParameters and Gateway JSON;
  - present and equal → no writes;
  - changed nodePort → one PATCH;
  - `Programmed` reached → endpoint returned;
  - never programmed → timeout error;
  - GET error propagates.

## T006 — `providers/application/openchoreo`: gateway wiring, exposure, route/URL observation

- **Do**:
  - `dataplane.go`: `EnsureDataPlaneGateway(ctx, client, ep
    reconcile.GatewayEndpoint)` merge-patches
    `DataPlane/default.spec.gateway.ingress.external = {name, namespace,
    http: {port, host}}`, only when it differs.
  - `defaults.go`:
    - add the `httproute-external` resource to `serendip-service`
      (plan.md decision 7: sample template, CORS dropped, HTTP-only
      filter);
    - add an update-if-different path for the ClusterComponentType only
      (decode the stored spec, compare, merge-patch).
  - `provider.go`:
    - exposed port → endpoint `{type: HTTP, port, visibility:
      [external]}`; non-exposed stays `{type: TCP, port}`;
    - `workloadEndpoint` gains `Visibility`, included in equality and
      patch;
    - `EnsureComponent` returns `ComponentObservation`. When any port is
      exposed and the workload is Deployed:
      1. read the cell namespace from the Application's
         ProjectReleaseBinding `status.namespace`;
      2. list HTTPRoutes there by label;
      3. check `status.parents[]` for our Gateway (`Accepted` and
         `ResolvedRefs`);
      4. read `ReleaseBinding.status.endpoints[].invokeURL` per exposed
         port;
      5. map the result to `Serving` / `Pending` / `RouteRejected`
         (upstream detail goes in the message).

  The Provider gains the `GatewayEndpoint` (constructor parameter), so it
  knows which parent to look for.
- **Files**: `providers/application/openchoreo/{dataplane.go,dataplane_test.go,defaults.go,defaults_test.go,provider.go,provider_test.go,VERSIONS.md}`.
- **Verify**: `go test ./providers/application/...`. Cases:
  - DataPlane gateway: absent → patched; equal → no write.
  - Component type: stored old spec → one PATCH; equal → none.
  - Exposure mapping: the exact Workload JSON for mixed exposed and
    non-exposed ports; toggling `expose` → one Workload PATCH including
    the visibility change.
  - Observation:
    - route accepted + URL present → `Serving` + URL;
    - route missing → `Pending`;
    - parent `Accepted=False` → `RouteRejected`;
    - URL missing → `Pending`;
    - nothing exposed → nil `Reachable`, no extra API calls.

  All spec-011 tests still pass.

## T007 — Wiring in `cmd/serendipd`

- **Do**:
  - Add `wireGateway(ctx, client, registry) (reconcile.GatewayEndpoint,
    error)`:
    1. `gatewayapi.EnsureCRDs`;
    2. kgateway `Deployer.Ensure`;
    3. `registry.Register("gateway", kgateway.NewProvider(...))`;
    4. Lookup + type-assert `reconcile.GatewayProvider`;
    5. `EnsureGateway`.

    A not-ready gateway is a startup error.
  - Read `SERENDIP_GATEWAY_HOST` (default `localhost`) and
    `SERENDIP_GATEWAY_NODE_PORT` (default 30080; parse errors fail
    startup).
  - `wireOpenChoreo` takes the endpoint: it calls `EnsureDataPlaneGateway`
    after `EnsureDefaultDataPlane`, and passes the endpoint to
    `openchoreo.NewProvider`.
  - Remove the old `EnsureGatewayAPICRDs` call from `wireOpenChoreo`.
- **Files**: `cmd/serendipd/main.go`.
- **Verify**: `go build ./...` and `go vet ./...`. `serendipd` with
  `SERENDIP_KUBECONFIG` unset still starts and serves `/healthz`. An
  invalid `SERENDIP_GATEWAY_NODE_PORT` with a kubeconfig set fails with a
  clear message (unit-test the parse helper).

## T008 — Pre-cluster gate

- **Do**: run the full test suite, `go vet`, `gofmt -l .`, and
  `check-imports.sh`. Do a `contract-guardian` review of the whole diff:
  `core/` neutrality of the new types/reasons/comments, provider
  isolation (openchoreo and kgateway never import each other; `internal/`
  only imported by providers), idempotency of the extended
  `setCondition`.
- **Files**: none (gate).
- **Verify**: all green, `contract-guardian` PASS, findings fixed.

## T009 — Real-cluster verification (kind): acceptance criteria 2–7

- **Do**:
  1. **Setup.** Create kind cluster `serendip-012` (node image
     `kindest/node:v1.34.0`) with `extraPortMappings: [{containerPort:
     30080, hostPort: 30080, listenAddress: "127.0.0.1"}]`. Leave the
     unrelated `urlshortener` cluster alone.
  2. **Upgrade-path pre-seed.** Before the first `serendipd` start, apply
     Gateway API **v1.3.0** standard CRDs by hand, to simulate a cluster
     set up under spec 011.
  3. **Startup.** Run `serendipd` and confirm:
     - the CRDs now carry bundle-version v1.6.1;
     - the kgateway controller is Running;
     - `Gateway/serendip-external` is `Programmed`, with an Envoy
       Deployment and a NodePort 30080 Service;
     - `DataPlane/default.spec.gateway` is set;
     - `serendip-service` contains `httproute-external`.
  4. **Settle every (unconfirmed) item in plan.md risk 1**, fixing code
     here, not just noting a discrepancy.
  5. **Deploy.** Create an Application, then a Component with
     `nginx:1.27-alpine` and ports `[{name: http, port: 80, expose:
     true}, {name: metrics, port: 9113}]`. Then:
     - watch the status go `Pending` → `Serving`, with an `endpoints[]`
       URL appearing only at `Serving` (criterion 4);
     - **from the Windows host**, `curl` the exact reported URL and get
       nginx's page (criterion 3);
     - confirm via `kubectl` that only one HTTPRoute exists, for `http`,
       and none for `metrics`.
  6. **Idempotency.** Wait past two resyncs and confirm `ResourceVersion`,
     audit rows, and Workload generation are unchanged (criterion 6).
  7. **Un-expose.** PUT `expose: false` and confirm:
     - the HTTPRoute is gone;
     - curl to the old URL fails;
     - status loses `Reachable` and `endpoints`;
     - the workload keeps running.
  8. **Re-expose.** Repeat 5's check briefly, then delete the Component
     and confirm the route is gone (criterion 5).
  9. **Regression.** A Component with no exposed ports, as in spec 011,
     still deploys exactly as before (criterion 5).
  10. **Restart.** Restart `serendipd`. No download, no writes (the
      defaults are equal), and the gateway stays programmed.
  11. **Footprint.** Record the kgateway controller + Envoy memory/CPU at
      idle, and the kind node total. Append them to ADR 0011 and
      `VERSIONS.md`.
  12. Tear down.
- **Files**: code fixes as needed; scratch scripts in the scratchpad.
- **Verify**: all of the above observed directly (curl output, `kubectl`,
  API JSON), not inferred from unit tests.

## T010 — Final acceptance pass + `contract-guardian` + `go-reviewer`

- **Do**: full test run. Re-check all 9 acceptance criteria against the
  tests and T009. Run vet, gofmt, and `check-imports.sh`. Do a holistic
  `contract-guardian` review and a `go-reviewer` review (polling
  loop/timeout handling in `EnsureGateway`, CRD upgrade logic, observation
  mapping, idempotency). Re-read spec.md's Scope-IN line by line against
  the code.
- **Files**: none (gate).
- **Verify**: all green, both reviews PASS, every criterion individually
  confirmed.

## T011 [A] — Docs sync

- **Do**:
  - New `docs/architecture/gateway.md`:
    - the capability split (Gateway provides the endpoint; Application
      Platform routes through it);
    - NodePort exposure and why Traefik is untouched;
    - the addressing scheme and the `localhost` / nip.io guidance;
    - the `Reachable` reasons table;
    - deferred items.
  - `application-platform.md`: the exposure field, the restored
    `httproute-external`, the Gateway API pin moved to v1.6.1 with
    upgrade-only semantics.
  - `provider-model.md`: `GatewayProvider` as the 4th interface data
    point, and the composition-root handoff between two capabilities.
  - `capability-model.md`: link ADR 0011.
  - `VERSIONS.md` files for both providers.
  - `docs/PROGRESS.md`.
- **Files**: the docs above.
- **Verify**: every claim matches the shipped code and T009's observed
  behavior. No doc still says external reachability is out of scope or
  that Gateway API is pinned at v1.3.0.
