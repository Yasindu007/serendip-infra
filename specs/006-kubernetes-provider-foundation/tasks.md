# Tasks: Spec 006 — Kubernetes access foundation + Provider interface pattern

Ordered; `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests/docs). Run `contract-guardian` before committing anything under
`core/` (CLAUDE.md workflow rule) — `providers/` is not yet covered by
that rule textually but this spec treats it the same way, since it's
new, security-relevant (TLS client cert handling) surface.

## T001 — ADR 0008 + package scaffolds

- **Do**: Write `docs/decisions/0008-sigs-k8s-yaml-for-kubeconfig-parsing.md`
  (plan.md's resolved reasoning, formalized). Create
  `providers/kubernetes/doc.go` and `core/provider/{doc.go,CLAUDE.md}`
  (mirrors `core/reconcile/CLAUDE.md`'s precedent: conventions before
  code).
- **Files**: `docs/decisions/0008-...md`, `providers/kubernetes/doc.go`,
  `core/provider/doc.go`, `core/provider/CLAUDE.md`.
- **Verify**: `go build ./...` succeeds.

## T002 — Kubeconfig parsing

- **Do**: `Config`, `LoadConfig` — parse a standard kubeconfig YAML
  (clusters/users/contexts/current-context) via `sigs.k8s.io/yaml`,
  resolve the current context into a flat `Config`. Commit a redacted
  real-shaped sample kubeconfig as a test fixture (client-cert-based
  auth, matching what K3s's installer actually produces — confirm the
  real shape against a freshly-generated one rather than guessing the
  field names).
- **Files**: `providers/kubernetes/config.go`,
  `providers/kubernetes/config_test.go`,
  `providers/kubernetes/testdata/kubeconfig-sample.yaml`.
- **Verify**: `go test ./providers/kubernetes/...` — table-driven:
  client-cert auth (K3s's default shape), bearer-token auth (a second
  fixture, proving the parser isn't hardcoded to one auth style),
  missing current-context, malformed YAML.

## T003 — Generic REST client

- **Do**: `Object`, `ObjectMeta`, `Client`, `NewClient`,
  `Get`/`Create`/`Delete`/`List` — `net/http` with TLS configured from
  `Config`'s cert/key/CA material (or bearer token). No hardcoded
  per-kind knowledge (plan.md's resolved generic-envelope design).
- **Files**: `providers/kubernetes/client.go`,
  `providers/kubernetes/client_test.go`.
- **Verify**: `go test ./providers/kubernetes/...` — `httptest.Server`-
  backed tests (not a real cluster, that's T006) covering each of the 4
  operations' request construction (method, path, auth header/TLS
  config applied) and response decoding into `Object`.

## T004 — core/provider registry

- **Do**: `Registry`, `NewRegistry`, `Register`, `Lookup` — in-memory,
  mutex-protected map, `any`-typed values (plan.md's resolved design).
- **Files**: `core/provider/registry.go`, `core/provider/registry_test.go`.
- **Verify**: `go test ./core/provider/...` — register then look up
  returns the same value; looking up an unregistered capability returns
  `(nil, false)`, not a panic or error; concurrent Register/Lookup calls
  don't race (`go test -race`).

## T005 — cmd/serendipd wiring (conditional)

- **Do**: If `SERENDIP_KUBECONFIG` is set: `kubernetes.LoadConfig` +
  `kubernetes.NewClient`, construct a `provider.Registry`, register a
  trivial proof-of-concept "list nodes once at startup, log the count"
  capability (this spec's acceptance criterion 2/3 mechanism — replaced
  by the real Storage provider in spec 007, not meant to survive as
  production code). If unset, skip entirely and log that no
  Kubernetes-facing providers are active.
- **Files**: `cmd/serendipd/main.go`.
- **Verify**: `go build ./...`; `SERENDIP_KUBECONFIG` unset ->
  `serendipd` starts exactly as before (acceptance criterion 4, run for
  real, not assumed from reading the code).

## T006 — Import boundary check

- **Do**: Extend `.claude/scripts/check-imports.sh` to confirm `core/`
  (specifically the new `core/provider/`) imports no `providers/`
  package.
- **Files**: `.claude/scripts/check-imports.sh`.
- **Verify**: the check passes against the real code, and — same
  discipline as every prior spec's import-boundary work — confirmed to
  actually catch a violation by temporarily adding one, running the
  check, then reverting.

## T007 — Real-cluster verification (disposable WSL2 clone): acceptance
criteria 2, 3, 4

- **Do**: Same `wsl --export`/`--import` disposable-clone approach as
  spec 005's T007 (never the persistent dev distro) — bootstrap a fresh
  K3s cluster using spec 005's own `serendip-agent bootstrap --confirm`
  (this spec's verification depends on spec 005's already-proven
  mechanism, not a new bootstrap path), then run `serendipd` with
  `SERENDIP_KUBECONFIG` pointed at the real
  `/etc/rancher/k3s/k3s.yaml`, confirm the real Node-listing
  proof-of-concept logs a real node count matching `k3s kubectl get
  nodes` directly. Also confirm `SERENDIP_KUBECONFIG` unset still starts
  cleanly (criterion 4) in the same or a plain WSL2 environment (no K3s
  needed for that half). Tear down the disposable clone completely
  afterward, confirm zero trace in the persistent dev distro.
- **Files**: none (verification task; a scratch shell script is fine).
- **Verify**: real node count observed matching reality, not assumed;
  disposable clone fully destroyed afterward.

## T008 — Final acceptance pass + contract-guardian

- **Do**: `go test ./providers/kubernetes/... ./core/provider/... -v`
  full run, re-check all 5 acceptance criteria against actual test
  output + T007's real-cluster results, `go vet ./...`, `gofmt -l .`,
  the T006 import-boundary check. One holistic `contract-guardian`
  review across `providers/kubernetes`, `core/provider`, and the
  `cmd/serendipd` diff, explicitly re-reading spec.md's Scope-IN line by
  line against the actual code (specs 002-005's established lesson) —
  pay particular attention to whether `core/` genuinely never imports
  `providers/` anywhere in the diff (this spec's core architectural
  property, the same category of thing spec 005's "`--confirm` must be
  airtight" was for its spec), and whether the TLS/client-cert handling
  in `providers/kubernetes/client.go` has any obvious security
  shortcuts (e.g. `InsecureSkipVerify` left on by accident).
- **Files**: none (review/gate task).
- **Verify**: all green, contract-guardian PASS, all 5 acceptance
  criteria individually confirmed.

## T009 [A] — Docs sync

- **Do**: Update `docs/PROGRESS.md` (spec 006 complete, spec 007 next).
  Update `docs/architecture/provider-model.md` to resolve its own
  long-standing open question with this spec's actual decision (the
  `core/provider.Registry` pattern, composition-root wiring in `cmd/`) —
  this is the one piece of architecture docs that was explicitly
  waiting on this spec, make sure it actually gets closed out, not just
  mentioned in PROGRESS.md.
- **Files**: `docs/PROGRESS.md`, `docs/architecture/provider-model.md`.
- **Verify**: no stale language remains describing spec 006 as
  not-started, and `provider-model.md`'s open question section reflects
  the real resolution, not the old "undecided" framing.
