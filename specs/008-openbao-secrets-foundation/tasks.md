# Tasks: Spec 008 — OpenBao deployment foundation

Ordered; `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). Run `contract-guardian` before committing anything under
`providers/` here — same elevated scrutiny spec 007 applied to
`providers/storage/localpath`, since this provider also performs real
cluster-mutating writes and additionally handles sensitive credential
material (root token, unseal key).

## T001 — providers/kubernetes: Service-proxy accessors

- **Do**: `HTTPClient() *http.Client` (returns the already-configured,
  already-TLS-authenticated client) and `ProxyURL(namespace, service
  string, port int) string` (builds the
  `/api/v1/namespaces/<ns>/services/<name>:<port>/proxy` path against
  the client's existing `baseURL`) on `providers/kubernetes.Client`.
- **Files**: `providers/kubernetes/client.go`,
  `providers/kubernetes/client_test.go`.
- **Verify**: `go test ./providers/kubernetes/...` — a table-driven test
  confirms `ProxyURL` produces the exact expected path string for a
  few namespace/service/port combinations; `HTTPClient()` returns the
  same `*http.Client` instance the `Client` was constructed with (not a
  copy, not nil).

## T002 — providers/secrets/openbao: REST client (health/init/unseal)

- **Do**: `Client`, `NewClient`, `SetToken`, `HealthStatus`, `Health`
  (normalized-status-code query params), `Init`, `Unseal`.
- **Files**: `providers/secrets/openbao/doc.go`,
  `providers/secrets/openbao/client.go`,
  `providers/secrets/openbao/client_test.go`.
- **Verify**: `go test ./providers/secrets/openbao/...` —
  `httptest.Server`-backed (mirrors `providers/kubernetes/client_test.go`'s
  pattern): `Health` against a fixture server confirms the normalized
  query params are sent and the response is parsed correctly regardless
  of which documented status code the fixture returns (200, 503, 501);
  `Init` sends the exact `{"secret_shares":1,"secret_threshold":1}` body
  and parses `keys`/`root_token` from a fixture response; `Unseal` sends
  `{"key": "..."}` and parses `sealed` from the response.

## T003 — providers/secrets/openbao: KV v2 read/write/delete

- **Do**: `ErrSecretNotFound`, `ReadSecret` (unwraps the double-nested
  KV v2 response shape), `WriteSecret`, `DeleteSecret` (against the
  `metadata` endpoint, full delete).
- **Files**: `providers/secrets/openbao/client.go` (extends T002).
- **Verify**: `go test ./providers/secrets/openbao/...` — fixture-server
  tests: `WriteSecret` sends `{"data": {...}}` to the exact expected
  path; `ReadSecret` against a fixture returning the real double-nested
  `{"data":{"data":{...},"metadata":{...}}}` shape returns just the
  inner data map; `ReadSecret` against a fixture 404 returns
  `ErrSecretNotFound`; `DeleteSecret` hits the `metadata` path, not
  `data`; `DeleteSecret` against a 404 returns nil, not an error
  (matches `providers/kubernetes.Client.Delete`'s existing convention).

## T004 — providers/secrets/openbao: Bootstrap (init/unseal orchestration)

- **Do**: `credentials` struct, `Bootstrap` — fresh-instance path
  (init, persist to `credentialsPath` mode 0600, parent dir mode 0700)
  and already-initialized path (read `credentialsPath`, hard error if
  missing), unseal-if-sealed in both paths.
- **Files**: `providers/secrets/openbao/bootstrap.go`,
  `providers/secrets/openbao/bootstrap_test.go`.
- **Verify**: `go test ./providers/secrets/openbao/...` — against a
  fixture `Client` (fake, in-process, not HTTP — `Bootstrap` only needs
  the `Health`/`Init`/`Unseal`/`SetToken` surface, easiest tested via a
  small interface + fake, not a real server) and a real temp directory:
  fresh instance → `Init` called once, file written with mode 0600
  (checked via `os.Stat`), `Unseal` called with the right key; already-
  initialized + sealed → `Init` NOT called, file read, `Unseal` called;
  already-initialized + unsealed → neither `Init` nor `Unseal` called;
  already-initialized + credentials file missing → returns an error,
  calls nothing destructive.

## T005 — providers/secrets/openbao: Deployer (StatefulSet/Service/PVC/ConfigMap)

- **Do**: `Deployer`, `NewDeployer`, `Ensure` — idempotent GET-then-
  Create for the namespace (shared with `providers/storage/localpath`),
  ConfigMap (server config: file storage, tcp listener 0.0.0.0:8200,
  `tls_disable=true`, no mlock directive), PVC (no `storageClassName`
  set), Service (ClusterIP, port 8200), StatefulSet (1 replica, mounts
  the ConfigMap + PVC, resource requests/limits per plan.md).
- **Files**: `providers/secrets/openbao/deploy.go`,
  `providers/secrets/openbao/deploy_test.go`.
- **Verify**: `go test ./providers/secrets/openbao/...` —
  `httptest.Server`-backed: all four objects absent → all four created,
  in an order that doesn't reference an object before its dependency
  exists (namespace before everything, ConfigMap/PVC before the
  StatefulSet that mounts them); any subset already present → only the
  missing ones created, no duplicate `Create` calls; a genuine
  unexpected error from any GET/Create propagates clearly, naming which
  object failed.

## T006 — cmd/serendipd wiring

- **Do**: Extend `wireKubernetes` (specs 006/007): after the existing
  `localpath` wiring, `openbao.NewDeployer(client).Ensure(ctx)`,
  construct the `openbao.Client` via `client.ProxyURL(...)` +
  `client.HTTPClient()`, `openbao.Bootstrap(ctx, obClient,
  credentialsPath)` (`credentialsPath` from
  `SERENDIP_OPENBAO_CREDENTIALS_PATH`, defaulting to
  `/var/lib/serendip/secrets/openbao-init.json`), `Register("secrets",
  obClient)` in `core/provider.Registry`.
- **Files**: `cmd/serendipd/main.go`.
- **Verify**: `go build ./...`; `serendipd` with `SERENDIP_KUBECONFIG`
  unset still starts successfully (the whole OpenBao wiring sequence is
  skipped, same gating as the rest of `wireKubernetes` — confirms this
  spec didn't accidentally make Kubernetes-facing setup load-bearing for
  a no-cluster startup).

## T007 — Real-cluster verification (disposable WSL2 clone): acceptance criteria 2-4

- **Do**: Same `wsl --export`/`--import` disposable-clone approach as
  specs 005-007 (never the persistent dev distro) — bootstrap a fresh
  K3s cluster, run `serendipd` with `SERENDIP_KUBECONFIG` set, confirm
  via `kubectl -n serendip-system` that the StatefulSet/Service/PVC/
  ConfigMap all exist and the Pod reaches `Running`/`Ready`; confirm
  `/v1/sys/health` (via the service-proxy path — this is the real test
  of open-question 5's unverified mechanism) reports
  `initialized=true, sealed=false`; confirm
  `/var/lib/serendip/secrets/openbao-init.json` exists with mode 0600.
  **Restart `serendipd`** (kill and re-run the same binary — simulates
  the re-seal-on-restart case without needing to actually restart the
  OpenBao Pod) and confirm it detects already-initialized+sealed,
  reads the existing credential file, and unseals successfully without
  re-initializing (acceptance criterion 3). Write one KV v2 value
  through `providers/secrets/openbao`'s client, read it back, confirm
  byte-for-byte equality (acceptance criterion 4). Tear down the
  disposable clone completely afterward, confirm zero trace in the
  persistent dev distro.
- **Files**: none (verification task; a scratch shell script is fine).
- **Verify**: all of the above observed directly against the real
  cluster, not assumed from unit tests alone. If the service-proxy
  mechanism (open-question 5) does NOT work as expected, stop and
  propose the NodePort fallback explicitly before switching approach
  (`propose_shortcuts_before_switching`) — do not silently swap.

## T008 — Final acceptance pass + contract-guardian

- **Do**: `go test ./providers/kubernetes/... ./providers/secrets/openbao/... -v`
  full run, re-check all 6 acceptance criteria against actual test
  output + T007's real-cluster results, `go vet ./...`, `gofmt -l .`,
  `.claude/scripts/check-imports.sh`. One holistic `contract-guardian`
  review across `providers/kubernetes/client.go`'s new accessors, all
  of `providers/secrets/openbao`, and the `cmd/serendipd` diff,
  explicitly re-reading spec.md's Scope-IN line by line against the
  actual code (specs 002-007's established lesson) — pay particular
  attention to: (a) whether the credential file is genuinely created
  with mode 0600 and never logged/exposed anywhere (health-check
  logging, error messages, etc. must never include the root token or
  unseal key), (b) whether `Deployer.Ensure`'s idempotent GET-then-
  Create could ever race-create a duplicate object under any realistic
  condition at this scale, (c) whether any OpenBao-specific type leaks
  into a place it shouldn't (this spec has no `core/` changes at all —
  confirm that's actually true, not just assumed).
- **Files**: none (review/gate task).
- **Verify**: all green, contract-guardian PASS, all 6 acceptance
  criteria individually confirmed.

## T009 [A] — Docs sync

- **Do**: Write `docs/architecture/secrets.md` (new) — deployment model
  (standalone/file-backend, no Helm, direct API object creation),
  init/unseal lifecycle, credential custody (ADR 0009's documented
  simplification, exact file path/mode), the service-proxy reachability
  mechanism, explicitly deferring the `Secret` resource kind/
  `SecretsProvider`/reconciler to spec 009. Update `docs/PROGRESS.md`
  (spec 008 complete — the Secrets capability's foundation is live
  against a real cluster; spec 009 is next). Update
  `docs/architecture/provider-model.md` only if this spec's wiring
  surfaces anything worth recording as a breadcrumb for future
  capability interfaces (likely minimal, since this spec defines no new
  capability-specific `core/` interface) — check before assuming a
  change is needed.
- **Files**: `docs/architecture/secrets.md` (new), `docs/PROGRESS.md`,
  possibly `docs/architecture/provider-model.md`.
- **Verify**: no stale language remains describing spec 008 as
  not-started; `docs/architecture/secrets.md` accurately describes only
  what actually shipped, not the aspirational full-capability shape.
