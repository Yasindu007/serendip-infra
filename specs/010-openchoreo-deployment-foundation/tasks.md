# Tasks: Spec 010 — OpenChoreo deployment foundation

Ordered; `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). Run `contract-guardian` before committing anything under
`providers/` here.

## T001 — providers/application/openchoreo: helm wrapper

- **Do**: `runHelm(ctx, args ...string) error` wrapping
  `exec.CommandContext("helm", args...)`; a constructor-time check that
  `helm` resolves on `$PATH` (`exec.LookPath`), returning a clear error
  naming it as a required host prerequisite if missing; combined
  stdout/stderr captured into the error on non-zero exit, truncated to
  a reasonable length (mirrors `providers/kubernetes.Client`'s own
  `unexpectedStatus` body-truncation convention).
- **Files**: `providers/application/openchoreo/helm.go`,
  `providers/application/openchoreo/helm_test.go`.
- **Verify**: `go test ./providers/application/openchoreo/...` — a
  fake `PATH` pointing at a tiny test script standing in for `helm`
  (mirrors how `agent/bootstrap`'s own tests avoid needing a real K3s
  binary): confirms argument pass-through, confirms a non-zero exit
  surfaces the script's stderr in the returned error, confirms a
  missing `helm` is reported clearly at construction, not deep inside
  a later call.

## T002 — providers/application/openchoreo: Deployer

- **Do**: `Deployer`, `NewDeployer` (fails if `helm` missing, per T001),
  `Ensure(ctx) error` — three sequential `runHelm` calls: cert-manager
  (`oci://quay.io/jetstack/charts/cert-manager`, `1.21.2`,
  `--namespace cert-manager --create-namespace --set crds.enabled=true
  --wait --timeout 5m`), control-plane
  (`oci://ghcr.io/openchoreo/helm-charts/openchoreo-control-plane`,
  `1.3.1`, `--namespace openchoreo-control-plane --create-namespace
  --set security.enabled=false --set backstage.enabled=false --set
  gateway.enabled=false --wait --timeout 10m`), data-plane
  (`oci://ghcr.io/openchoreo/helm-charts/openchoreo-data-plane`,
  `1.3.1`, `--namespace openchoreo-data-plane --create-namespace --set
  gateway.enabled=false --wait --timeout 10m`). Each error wrapped
  naming which release failed.
- **Files**: `providers/application/openchoreo/deploy.go`,
  `providers/application/openchoreo/deploy_test.go`.
- **Verify**: `go test ./providers/application/openchoreo/...` — same
  fake-`helm`-script pattern as T001: confirms the exact three
  invocations happen in order with the exact flags above (a
  table/slice of expected `args` per call, mirroring
  `deploy_test.go`'s existing dependency-order assertions in specs
  007/008), confirms a cert-manager failure stops before the
  control-plane call ever runs (fail fast, don't install on top of a
  broken prerequisite).

## T003 [P] — providers/application/openchoreo: default DataPlane CR

- **Do**: `EnsureDefaultDataPlane(ctx, client *kubernetes.Client)
  error` — idempotent GET-then-Create of the `DataPlane` CR
  (`openchoreo.dev/v1alpha1`, name `default`, namespace
  `openchoreo-control-plane` per plan.md resolution 7 — **confirm this
  namespace against the real cluster in T006; this is plan.md's one
  flagged unknown, not a settled fact**) with
  `spec.clusterAgent.clientCA.secretKeyRef` pointing at
  `{name: cluster-agent-tls, namespace: openchoreo-data-plane, key:
  ca.crt}`.
- **Files**: `providers/application/openchoreo/dataplane.go`,
  `providers/application/openchoreo/dataplane_test.go`.
- **Verify**: `go test ./providers/application/openchoreo/...` —
  `httptest.Server`-backed (mirrors `providers/secrets/openbao/
  deploy_test.go`'s pattern): absent → created with the exact JSON
  shape above; already present → no duplicate `Create` call; a genuine
  unexpected GET/Create error propagates clearly.

## T004 [P] — providers/application/openchoreo: readiness check

- **Do**: `CheckReady(ctx, client *kubernetes.Client) error` — GETs
  each known Deployment (exact Helm-release-name-prefixed names
  **confirmed against the real installed chart during this task, not
  assumed from plan.md verbatim** — `kubectl get deployments -n
  openchoreo-control-plane` / `-n openchoreo-data-plane` against a
  real install is the source of truth) and confirms
  `status.readyReplicas >= 1` on each, returning a clear error naming
  which Deployment isn't ready.
- **Files**: `providers/application/openchoreo/readiness.go`,
  `providers/application/openchoreo/readiness_test.go`.
- **Verify**: `go test ./providers/application/openchoreo/...` —
  `httptest.Server`-backed: all ready → nil; one not-ready → error
  naming that specific Deployment; a GET error on any Deployment
  propagates clearly.

## T005 — cmd/serendipd wiring

- **Do**: `wireOpenChoreo(ctx, client, registry) error` in
  `cmd/serendipd/main.go` — `Deployer.Ensure`, then
  `EnsureDefaultDataPlane`, then `CheckReady`, then
  `registry.Register("application-platform", <marker value>)`. Called
  from `wireKubernetes` after the existing `wireOpenBao` call.
- **Files**: `cmd/serendipd/main.go`.
- **Verify**: `go build ./...`; `serendipd` with `SERENDIP_KUBECONFIG`
  unset still starts successfully (the whole OpenChoreo wiring
  sequence is skipped, same gating as every other Kubernetes-facing
  piece — mirrors spec 008's T006 acceptance check, now proven a third
  time).

## T006 — Real-cluster verification (kind): acceptance criteria 2-4, 7

- **Do**: Against a real `kind` cluster (this session's established
  preference over WSL2 — see `docs/PROGRESS.md`): run `serendipd` with
  `SERENDIP_KUBECONFIG` set, confirm cert-manager's 3 pods reach
  `Running`, confirm `openchoreo-control-plane`'s and
  `openchoreo-data-plane`'s Deployments (and the `authz` bootstrap
  `Job`, which should report `Complete`, not `Running`) reach ready,
  confirm the `DataPlane` CR exists and carries no error
  condition/status, confirm the pinned versions
  (`helm list -n openchoreo-control-plane -o json` or equivalent)
  match `1.3.1`/`1.21.2`. **Settle the two items plan.md flagged as
  genuinely unverified**: the `DataPlane` CR's real namespace (T003's
  note) and the exact readiness-check Deployment names (T004's note) —
  if either assumption was wrong, fix the code here, don't just note
  the discrepancy. Tear down the `kind` cluster completely afterward.
- **Files**: none (verification task; a scratch shell script is fine).
- **Verify**: all of the above observed directly against the real
  cluster, not assumed from unit tests alone — matches specs 007/008/
  009's established discipline.

## T007 — Final acceptance pass + contract-guardian + go-reviewer

- **Do**: `go test ./providers/application/openchoreo/... -v` full
  run, re-check all 7 acceptance criteria against actual test output +
  T006's real-cluster results, `go vet ./...`, `gofmt -l .`,
  `.claude/scripts/check-imports.sh`. One holistic `contract-guardian`
  review (confirm zero `core/` changes really is true, not just
  assumed; confirm no OpenChoreo-specific name leaks anywhere outside
  `providers/application/openchoreo`; confirm the three-sequential-
  Helm-install ordering in `Deployer.Ensure` can't partially apply in
  a way that leaves the cluster in a confusing half-installed state
  without a clear error) and one `go-reviewer` pass (idiomatic
  `os/exec` usage, context cancellation/timeout handling across three
  sequential long-running subprocess calls, error wrapping), explicitly
  re-reading spec.md's Scope-IN line by line against the actual code
  (specs 002-009's established lesson).
- **Files**: none (review/gate task).
- **Verify**: all green, both reviews PASS, all 7 acceptance criteria
  individually confirmed.

## T008 [A] — Docs sync

- **Do**: Write `docs/architecture/application-platform.md` (new,
  mirrors `storage.md`/`secrets.md`'s pattern) — deployment model
  (three Helm releases: cert-manager, control plane, data plane;
  single-cluster co-location), the disabled-by-default feature set and
  why (security/Thunder, gateway/kgateway, backstage — all deferred,
  not removed), the `DataPlane` CR registration, explicitly deferring
  the `Application`/`Component` resource kinds/`ApplicationPlatformProvider`/
  reconciler to a later spec. Update `docs/PROGRESS.md` (spec 010
  complete). Update `docs/architecture/provider-model.md` only if this
  spec's wiring surfaces a breadcrumb worth recording — check before
  assuming a change is needed (likely minimal, since this spec defines
  no new capability-specific `core/` interface, same as spec 008).
- **Files**: `docs/architecture/application-platform.md` (new),
  `docs/PROGRESS.md`, possibly `docs/architecture/provider-model.md`.
- **Verify**: no stale language remains describing spec 010 as
  not-started; the new doc accurately describes only what actually
  shipped (control plane + data plane reachable, nothing more), not
  the aspirational full-capability shape.
