# Progress / session state

**Read this file first in every new session**, before touching code or
docs. It's the single source of truth for "what's actually done, what's in
flight, what's next" — overwritten each session, not appended to. Git
history has the detail if you need archaeology; this file is only ever the
current picture.

Companion docs: `docs/PRD.md` (why/what/roadmap, changes rarely),
`docs/constitution.md` (non-negotiable rules, changes almost never).

Last updated: 2026-10-11

## Current phase

**Spec 001 (Foundation) merged to Serendip-Infra/serendip-infra:main via
PR #24.** Specs 002-011 are committed locally only (spec 012 not yet) — decided explicitly
with the user to keep working locally and push everything together at
a later checkpoint, now that GitHub auth works again (see GitHub note
below) — not pushing yet is a choice, not a blocker.

**Spec 012 (Gateway capability, kgateway) — COMPLETE, T001-T011 all done,
both final reviews (`contract-guardian`, `go-reviewer`) PASS, NOT yet
committed** (working tree only). Components can now be exposed outside the
cluster. Full detail below, under "Spec 012 — what actually got built."

**Spec 011 (Application Platform capability) — COMPLETE, T001-T010 all
done, both final reviews (`contract-guardian`, `go-reviewer`) PASS,
committed (`7d255dd`).** The real `Application`/`Component` capability on
top of spec 010's foundation. Full detail below, under "Spec 011 — what
actually got built."

**Spec 010 (OpenChoreo deployment foundation) — COMPLETE and committed**
(`436380c`, T001-T008). A real OpenChoreo control plane + data plane
running and reachable on top of specs 005/006's Kubernetes-access
foundation — same foundation/capability split as specs 005+006→007 and
008→009; spec 011 is its capability. Full detail below, under "Spec 010 —
what actually got built."

**Spec 009 (the real Secrets capability) — COMPLETE and committed**
(`9343d30`). Builds the actual `Secret` resource kind /
`SecretsProvider` / `SecretReconciler` / `api.SecretHandler` on top of
spec 008's OpenBao foundation — same split as specs 006→007. All 9
tasks done, both reviews PASS, real-cluster verification confirmed on
a `kind` cluster. Full detail below, under "Spec 009 — what actually
got built."

**Spec 008 (OpenBao deployment foundation) — COMPLETE and committed**
(`d32877e` T001, `80e99e1` T002-T006, `88de5d7` T007-T009). All nine
tasks done: T001-T006 built the deployment/init/unseal/KV-v2
foundation, T007 found and fixed three real bugs on a real `kind`
cluster, T008 passed both `contract-guardian` and `go-reviewer`, T009
wrote `docs/architecture/secrets.md`. User picked Secrets/OpenBao as
the second real capability (confirmed 2026-10-10) over Identity/Thunder
(still flagged immature, ADR 0006), Application Platform/OpenChoreo
(biggest lift), Gateway/kgateway (nothing to route to yet).

- `docs/decisions/0009-openbao-as-secrets-provider.md` — **Accepted**,
  since corrected once (see below). Governance/maturity check (Linux
  Foundation + OpenSSF, MPL-2.0, Vault fork, v2.7.1) via oss-researcher.
  Key decisions: standalone mode (real PVC, not dev/ephemeral, not
  HA/Raft — deferred per ADR 0002's precedent); no Helm, construct
  StatefulSet/Service/PVC/ConfigMap directly via the existing
  `providers/kubernetes.Client` (same precedent as spec 007 bypassing a
  StorageClass); root token + 1-of-1 unseal key stored in a root-only
  local file (0600, `/var/lib/serendip/secrets/openbao-init.json`) —
  documented temporary simplification, same tier as spec 002's static
  API keys; hand-roll a minimal OpenBao REST client, **rejecting** the
  official `github.com/openbao/openbao/api/v2` SDK — independently
  verified (throwaway module + `go list -deps`, same method as ADR
  0008) to pull in ~20 real packages — same category of problem as the
  already-rejected `client-go`.
- `providers/secrets/openbao/{client,bootstrap,deploy}.go` + tests —
  hand-rolled OpenBao REST client (health/init/unseal/KV v2),
  `Bootstrap` (init/unseal orchestration + credential-file custody),
  `Deployer` (idempotent Namespace/ConfigMap/PVC/Service/StatefulSet
  creation). `providers/kubernetes.Client` gained `Object.Data` (a real
  gap found implementing T005: ConfigMap's payload lives at a top-level
  `data` key, which `Spec`/`Status` alone couldn't model) and
  `HTTPClient()`/`ProxyURL()` (so this provider reaches OpenBao's
  cluster-internal Service through the Kubernetes API server's
  Service-proxy subresource — `serendipd` runs outside the cluster).
  Wired into `cmd/serendipd`'s `wireKubernetes`, registered under the
  `"secrets"` capability — no reconciler yet, that's spec 009.
- **Real upstream bug found and fixed during T007**: the originally
  planned `storage "file"` OpenBao config stanza crashes with `unknown
  storage type file` — **OpenBao 2.7.0 removed the `file` storage
  backend entirely** (confirmed via oss-researcher against OpenBao's
  own issue #4077/PR #4088: their own packaged default config hit this
  exact crash post-2.7.0, fixed by switching to `pebbledb`). Fixed the
  same way upstream did — `providers/secrets/openbao/deploy.go`'s
  `serverConfigHCL` now uses `pebbledb`. ADR 0009 and `plan.md` both
  updated with this correction. Also added a `startupProbe` (tcpSocket,
  generous threshold for PebbleDB's WAL replay), `preStop` (5s sleep),
  and `terminationGracePeriodSeconds: 30` to the StatefulSet, mirroring
  `openbao-helm`'s own established pattern for container-restart races
  — applied as good practice. (The container-`args` change made at the
  same time, passing `-config=/openbao/config` to match the entrypoint's
  own default "exactly," turned out to be wrong — see T007 below, which
  found this actually double-bound the listener and reverted it.)
- **T007 real-cluster verification is now functionally COMPLETE** —
  after a long detour through what looked like WSL2 host instability
  across two sessions (a disposable clone's disk filling C: to zero
  bytes free, repeated idle-triggered `cni0` bridge deaths, a literal
  WSL service crash, and a full machine reboot), the environment
  turned out to be a red herring: **switching verification to a
  `kind`/Docker cluster reproduced the exact same `openbao-0`
  crash-loop**, proving the real blockers were three genuine,
  deterministic code bugs, not host flakiness. All three were found
  and fixed this session, each with unit-test coverage, then confirmed
  end-to-end against a real `kind` cluster:
  1. **`Deployer.Ensure` didn't wait for the Pod to be reachable**
     before returning — it created the StatefulSet and returned
     immediately, so `wireOpenBao`'s subsequent `Bootstrap` call always
     raced a Pod that was still pulling its image / replaying
     PebbleDB's WAL, hit a genuine "no endpoints available" 503, and
     crashed `serendipd` at startup on every cluster, every time. Fixed:
     `Ensure` now polls the StatefulSet's `status.readyReplicas` (2s
     interval, 90s timeout, both overridable per-`Deployer` for tests)
     and only returns once the Pod is actually ready.
  2. **The container `args` duplicated the image's own entrypoint
     default**, causing a deterministic crash on literally the first
     boot of any Pod, not just a restart race. The official entrypoint
     already runs `bao server -config=/openbao/config "$@"`; passing
     `args: ["server", "-config=/openbao/config"]` (an earlier "fix,"
     reasoned purely from reading the entrypoint source via
     oss-researcher, never verified against a real cluster until now)
     made `-config=/openbao/config` appear twice in the final command
     line. Since `-config` in directory mode scans/merges every config
     file in that directory, processing it twice loaded and bound the
     TCP listener twice within the same process, crashing immediately
     with `listen tcp4 0.0.0.0:8200: bind: address already in use` —
     reproduced byte-for-byte identically on a from-scratch Pod on two
     unrelated clusters (K3s and kind), ruling out any restart-timing
     or host-specific explanation. Fixed: `args` is now just
     `["server"]`, relying entirely on the entrypoint's own default
     with nothing ever duplicated.
  3. **No KV v2 secrets engine was ever mounted.** A production OpenBao
     instance (unlike `-dev` mode) mounts nothing but its built-in
     system backends after `Init` — `ReadSecret`/`WriteSecret` 404'd
     with "no handler for route" until something explicitly enables a
     KV v2 mount, which neither spec.md, plan.md, nor ADR 0009 had
     actually called out despite acceptance criterion 4 requiring a
     working KV v2 round-trip. Fixed: new `Client.EnsureKVv2Mount(ctx,
     mount)` (idempotent — GETs `/v1/sys/mounts` first, only POSTs to
     enable if absent), called unconditionally from `Bootstrap` (not
     just the fresh-init path) specifically so a prior partial failure
     (`Init` succeeds, a later step fails) can self-heal on the next
     call, since "initialized" is permanent OpenBao-side state with no
     other path back to this check.
  All three fixes are covered by new/updated unit tests
  (`deploy_test.go`, `bootstrap_test.go`, `client_test.go`) and were
  independently confirmed end-to-end on a real `kind` cluster: all four
  K8s objects created, `openbao-0` reached `1/1 Running` with **zero**
  restarts, `/v1/sys/health` via the Kubernetes Service-proxy
  subresource reported `initialized=true,sealed=false` (confirming
  plan.md's open-question-5 mechanism works end-to-end), a simulated
  restart-while-sealed (sealed via the real API, then `serendipd`
  killed and re-run) correctly unsealed from the existing credentials
  file with **no** re-`Init` (file checksum unchanged, acceptance
  criterion 3), and a KV v2 write/read round-trip through the real
  client code returned byte-for-byte identical data (acceptance
  criterion 4) — all automatically, no manual intervention, on a second
  from-scratch run.
  **One gap deliberately left unverified**: the credentials file's
  mode-0600 requirement can't be meaningfully checked on this Windows
  dev machine (NTFS has no POSIX permission bits) — `bootstrap_test.go`
  already gates that specific assertion behind `runtime.GOOS ==
  "linux"` for the same reason. Needs a real Linux run (the eventual
  T470/WSL target, or any Linux CI) to close out for real, but isn't
  blocking — the file-writing code path (`os.WriteFile(path, buf,
  0o600)`) is untouched by anything found this session.
  **Environment note for future sessions**: `kind` (Docker-based, via
  Docker Desktop) is now the proven, lower-friction real-cluster
  verification path for this spec — it avoided every WSL2-specific
  symptom entirely. Prefer it over hand-rolled `wsl --export`/`--import`
  clones for specs 009+ unless something specifically needs a real K3s
  node (e.g. agent/bootstrap work). The `.wslconfig` changes from this
  session (`memory=8GB`, `processors=4`, `vmIdleTimeout=-1`) are still
  in place and harmless either way.
- **T008 (holistic `contract-guardian` + `go-reviewer` review) is now
  done: PASS.** T009 (docs-sync, `docs/architecture/secrets.md`) done;
  everything committed (`88de5d7`).
- A process note worth keeping: the Service-proxy mechanism itself
  (the biggest flagged unknown in plan.md) is **already confirmed
  working** — `serendipd` reached it and got real, meaningful API
  responses (503/502 reflecting real upstream state) rather than a
  connection error, well before the WSL instability derailed the rest
  of the run. Dynamic PVC provisioning via `WaitForFirstConsumer` also
  already confirmed working exactly as designed (open-question 6).

**Spec 007 (Storage provider — local-path) — COMPLETE. All 10 tasks
committed locally**, holistic contract-guardian review PASS (one
non-blocking stale-doc-comment finding fixed same-session). This is the
**first real, working, end-to-end capability** — the first spec that
actually provisions real external infrastructure, not just observes it.
Full picture:

- **Resolves the capability-specific interface question spec 006
  deliberately left open**: `StorageProvider` (`core/reconcile/storage.go`)
  — `Ensure`/`Delete`, using only `core/resource.Volume`, no Kubernetes
  concept leaking into `core/`. New `Volume` resource kind
  (`core/resource/storage.go`) — named `Volume`, not `Storage` as
  `resource-model.md` literally says, specifically to avoid colliding
  with "Storage" already meaning the *capability* throughout
  `capability-model.md`/`storage.md`.
- **Two real design corrections, both discovered via actual real-cluster
  testing (T008), not caught in planning**:
  1. **The originally-planned mechanism (an `Immediate`-binding-mode
     `StorageClass` driving `rancher.io/local-path-provisioner`'s
     dynamic provisioning) genuinely doesn't work.** Real testing hit
     `"configuration error, no node was specified"` — confirmed against
     the provisioner's own source (oss-researcher): its default
     per-node hostPath mode hard-requires a target node, supplied only
     via the `volume.kubernetes.io/selected-node` annotation the
     Kubernetes scheduler sets as part of `WaitForFirstConsumer`'s
     delayed-binding flow, which `Immediate` mode never populates.
     Reverting to `WaitForFirstConsumer` doesn't help either — it just
     restores the original problem (no consuming Pod exists to trigger
     scheduling). **Fix, confirmed with the user before implementing
     since it changed the spec's core mechanism**: bypass the
     provisioner's dynamic-provisioning controller entirely — create a
     `PersistentVolume` directly (hostPath-backed) and a
     `PersistentVolumeClaim` that statically binds to it via
     `spec.volumeName`, standard Kubernetes mechanics that predate
     dynamic provisioning and need no provisioner or consuming Pod at
     all. Still "integrate, don't rebuild" (constitution rule 10):
     Serendip declares standard API objects, kubelet does the real work.
  2. **Deleting a `Volume` via the API left the real PV/PVC behind** —
     `VolumeReconciler.Reconcile`'s `store.ErrNotFound` branch (hit on
     the delete event) just returned `nil`, copying `ClusterReconciler`'s
     identical branch, correct there (nothing external to clean up for a
     passive health-checker) but wrong here, the first Reconciler whose
     provider actually provisions state needing explicit teardown.
     Fixed: that branch now calls `provider.Delete` with a minimal
     `Volume` carrying just the deleted ref's ID.
- **T008's real-cluster verification, run twice** (once before each
  fix, confirming both problems for real before fixing either) on a
  disposable WSL2 clone (never the persistent dev distro, same pattern
  as specs 005/006): after both fixes, a `Volume` created through the
  live HTTP API reached `Bound` in ~8.7s, confirmed both via the API and
  directly via `kubectl get pv/pvc`; a genuine 35-second real-time wait
  past the 30s resync interval with zero drift confirmed
  `ResourceVersion` stayed unchanged (idempotency, acceptance criterion
  3, proven through the real `Engine`, not just a unit test); deleting
  the `Volume` via the API then genuinely removed both the real PVC and
  PV, confirmed via `kubectl`. Clone fully destroyed afterward
  (`serendip-agent bootstrap --uninstall --confirm`, then
  `wsl --unregister`), zero trace confirmed in the persistent dev distro.
- **Worth remembering for every future Kubernetes-facing provider**:
  real cluster behavior can invalidate a design that looked sound on
  paper — this spec's whole mechanism changed after T008, not before.
  Budget for this when planning Identity/Secrets/Application-Platform/
  Gateway providers; don't assume plan.md's Approach section is final
  until real-cluster verification confirms it.

**Specs 002-006 — COMPLETE.** See their own "what actually got built"
sections below; condensed here now that spec 007 is current. Spec 006's
one notable lesson worth carrying forward: cross-referencing adjacent
docs (not just the spec being worked on) is what caught the stale
`provider-model.md` open-question framing spec 006 then resolved — same
"check the actual commitment, don't assume" discipline spec 007's own
T008 corrections above now also exemplify, just discovered via runtime
testing instead of doc cross-referencing.

**GitHub note (updated 2026-09-06, auth issue resolved)**: `serendipinfra`
GitHub auth is now working. The PAT-generation flow had a real,
unexplained problem (manually-created tokens repeatedly came back
"invalid" from GitHub itself, tried via masked interactive prompt, a
file piped via `--with-token`, and a `$env:GH_TOKEN` env var — all three
independently rejected the same copied token) — resolved by using `gh
auth login`'s **browser device-flow** option instead of pasting a PAT,
which worked immediately. If a PAT is ever needed again and manual
paste keeps failing, try device-flow login first rather than
re-debugging the paste path. Current token scopes:
`admin:public_key`, `gist`, `read:org`, `repo` — **missing `project` and
`workflow`**; add with `gh auth refresh -h github.com -s
project,workflow` only when Projects v2 board work or a
`.github/workflows/*.yml` push is actually needed, not preemptively.
`Yasindu007` remains a separate logged-in account, correctly inactive —
standing instruction not to use it for this project stands.

## Spec 012 — what actually got built

`core/reconcile/gateway.go` + `providers/gateway/kgateway` +
`providers/internal/{helm,gatewayapi}` + OpenChoreo provider changes +
`cmd/serendipd` `wireGateway`, T001-T011 done, uncommitted. ADR 0011
(Accepted) and `docs/architecture/gateway.md` hold the detail.

- **Capability split**: `GatewayProvider.EnsureGateway` returns a
  `GatewayEndpoint{Name,Namespace,Host,Port}`; the composition root hands it
  to the OpenChoreo provider, which writes
  `DataPlane/default.spec.gateway.ingress.external`. OpenChoreo renders the
  HTTPRoutes. Neither provider imports the other. No Gateway resource kind;
  ensured at startup, not reconciled.
- **kgateway v2.4.6** via Helm (experimental features off), one Gateway
  `serendip-external` on a **NodePort (default 30080**,
  `SERENDIP_GATEWAY_NODE_PORT`). Traefik/ServiceLB untouched, so no migration
  for existing clusters. `SERENDIP_GATEWAY_HOST` defaults to `localhost`
  (works only on the node; use `<node-ip>.nip.io` elsewhere). HTTP only.
- **Gateway API pin moved v1.3.0 to v1.6.1**, now in
  `providers/internal/gatewayapi`, **upgrade-only** (never downgrades) plus
  upstream's safe-upgrades policy. The in-place cluster-wide CRD upgrade is
  intentional. Corrects spec 011's "never upgrade".
- **Core**: `ComponentPort.Expose`, `ComponentStatus.Endpoints`,
  `ComponentObservation`/`Reachability`, a `Reachable` Condition
  (`Serving`/`Pending`/`RouteRejected`); URLs published only at `Serving`.
  `serendip-service` regained `httproute-external` (CORS dropped), and the
  ClusterComponentType now has an update-if-contained path.
- **Real-cluster findings (kind, k8s 1.34, CRDs pre-seeded at v1.3.0)**:
  (1) OpenChoreo's per-component NetworkPolicy only admits pods labeled
  `openchoreo.dev/system-component`, so every request 503'd until the proxy
  pods got that label (fixed via `Gateway.spec.infrastructure.labels`,
  supplied by `cmd/serendipd`); (2) OpenChoreo 1.3.1 reports
  `externalURLs.http {scheme,host,port,path}`, not `invokeURL`; the provider
  assembles the URL from those parts.
- **Verified**: Reachable Pending to Serving about 28s; `curl` from the
  Windows host returned 200 nginx; only the exposed port got a route;
  idempotent over 2 resyncs; un-expose removed the route in about 3s (curl
  404, status cleaned, pod kept), re-expose 200, delete removed the route;
  nothing exposed deploys exactly as spec 011; restart patched the Gateway
  once (labels).
- **Footprint** (dev machine, not T470): controller about 50 MB, Envoy about
  19 MB, about 70 MB and about 1% CPU idle; kind node about 1.67 GiB full
  stack; `serendipd` about 21 MB.
- **Explicitly deferred**: TLS, custom domains, internal visibility, API
  management, multiple/BYO gateways, non-HTTP, gateway HA, disabling Traefik
  to serve on 80/443, a Gateway resource kind and live reconciliation, an HTTP
  reachability probe, removing labels via merge patch.

## Spec 011 — what actually got built

`core/resource/app.go` + `core/reconcile/application.go` +
`providers/application/openchoreo/{names,defaults,provider,gatewayapi}.go`
+ `providers/kubernetes` `Patch` + `cmd/serendipd` wiring — the real
Application Platform capability, T001-T010 all done (`7d255dd`). Detail:
`docs/architecture/application-platform.md`.

- **Resource kinds**: `ComponentSpec{Image, Ports[{Name,Port}], Env}`;
  `ApplicationSpec` still empty (pure grouping). No `api/` change (generic
  handlers pick the fields up via JSON).
- **`ApplicationPlatformProvider`** (`core/reconcile`): `EnsureApplication`/
  `DeleteApplication`/`EnsureComponent`/`DeleteComponent`, plus
  `ApplicationReconciler` and `ComponentReconciler`. Both not-found
  branches call `Delete*` (spec 007 lesson). One `Ready` Condition per kind
  with neutral reasons `Deployed`/`Progressing`/`DeployFailed`/
  `Unsupported`/`InvalidSpec`/`WaitingForApplication`/`EnsureFailed`;
  written only when `{Status, Reason}` changes. Resolves provider-model.md's
  `ApplicationProvider` naming question (third narrow-interface data point).
- **Mapping**: Application ↔ OpenChoreo `Project` + `ProjectReleaseBinding`
  (1:1); Component ↔ `Component` (autoDeploy) + `Workload`; OpenChoreo makes
  ComponentRelease/ReleaseBinding/RenderedRelease itself. Names are
  `app-`/`cmp-` + 10 hex of `sha256(Serendip ID)` so delete works from the
  ID alone and cell-namespace names stay short.
- **Corrected upstream fact**: OpenChoreo 1.3.1 has no Organization CRD; a
  namespace labeled `openchoreo.dev/control-plane=true` is the org boundary.
  `openchoreo-control-plane` is reused (DataPlane/default lives there).
  `EnsurePlatformDefaults` (startup, not reconciled) ensures the label,
  `ClusterProjectType/serendip-default`, trimmed
  `ClusterComponentType/serendip-service`, `Environment/development`, and
  single-environment `DeploymentPipeline/serendip` (accepted on a real
  cluster).
- **Real-cluster finding (kind, k8s 1.34.0)**: OpenChoreo's RenderedRelease
  controller lists Gateway/HTTPRoute and fails every reconcile without
  Gateway API CRDs even with `gateway.enabled=false`, so nothing became
  Ready. Fixed by `EnsureGatewayAPICRDs` (v1.3.0 standard CRDs only,
  sha256-verified download, skipped if all 5 exist, never upgrades; spec 012
  superseded this with a v1.6.1 upgrade-only pin). A prerequisite, not
  ingress. Known limitation: fresh installs need
  github.com access.
- **Review-driven**: `observedGeneration` staleness check, `Progressing`
  right after a Workload patch, plus the bound-ComponentRelease workload
  snapshot check (a real-cluster rerun showed the patch rule alone races a
  self-triggered reconcile), port validation to `InvalidSpec`,
  CRD-download skip.
- **Verified on kind**: Application Ready ~30s; Component Progressing to
  Deployed ~30s; nginx Pod 1/1, env delivered, HTTP served; idempotent over
  2 resyncs (ResourceVersion, audit rows, Workload generation unchanged);
  image update patched the Workload once and rolled out (old Pod serves
  meanwhile); Component delete cleaned everything in ~7s; Application
  cascade delete removed Project and cell namespace in ~20s. Footprint: kind
  node ~1.06 GiB full stack idle, `serendipd` ~30 MB (not the ADR 0005 T470
  benchmark).
- **Explicitly deferred**: port-less (worker) Components, ingress
  visibility (done in spec 012), Serendip Organization/Project to OpenChoreo namespace mapping,
  Secret injection, multi-environment promotion, retrying a failed DELETE
  after the Serendip record is gone (engine-level, same as Volume), live
  reconciliation of platform defaults (restart repairs), ~30s status lag
  (no requeue).

## Spec 010 — what actually got built

`providers/application/openchoreo` + `cmd/serendipd` wiring — the
OpenChoreo deployment foundation, 7 implementation/review tasks plus this
docs-sync, all complete and committed (`436380c`):

- **A deliberate, ADR-recorded reversal of specs 007/008/009's "no Helm,
  build Kubernetes objects directly" precedent, scoped to this capability
  only**: `Deployer.Ensure` shells out to the real `helm` CLI
  (`os/exec.CommandContext`) to install cert-manager and both OpenChoreo
  OCI charts, rather than hand-constructing their object graph —
  OpenChoreo's own first-party distribution mechanism is Helm, and the
  real surface (CRDs, controller-manager, api-server, a bundled
  cert-manager dependency) is too large to hand-build safely. New
  `docs/decisions/0010-helm-shellout-for-openchoreo.md` formalizes this as
  a per-capability call, not a blanket policy change — Storage/Secrets
  stay direct-object-construction.
- **Three real corrections found during real-cluster (`kind`)
  verification, same "plan.md is a hypothesis" discipline specs 007/008/
  009 already proved necessary**:
  1. A Kubernetes CEL-compiler bug (not an OpenChoreo bug) made the
     control-plane chart's `ClusterAuthzRoleBinding` CRD fail to install
     on `kind`'s *default* node image (Kubernetes 1.31.0) — reproduced
     with plain `kubectl apply --dry-run=server`, bypassing Helm entirely,
     and confirmed absent on Kubernetes 1.34.0. Irrelevant to this
     project's real target (K3s is already pinned to `v1.36.4+k3s1`,
     spec 005); fixed by pinning `kind`'s node image to 1.34.0+ for
     verification, no code change needed.
  2. The data-plane chart needs `gateway.tls.enabled=false` set
     explicitly, not just `gateway.enabled=false` — its own
     `validateHostnames` check is gated on `gateway.tls.enabled` alone,
     independent of `gateway.enabled`, unlike the control-plane chart's
     combined gate. Fixed in `Deployer.Ensure`'s data-plane `helm`
     invocation.
  3. A genuine, confirmed upstream gap: the chart's own promised "CA
     extractor hook job" that populates the data plane's trust ConfigMap
     with the control plane's real CA does not exist anywhere in the
     chart (confirmed by grepping every template for `kind: Job` — only
     the unrelated authz bootstrap job exists). The real CA sits in a
     `cluster-gateway-ca` Secret the chart never reads back out. Fixed
     with a new `Deployer.ensureDataPlaneTrustsControlPlane` step,
     inserted between the control-plane and data-plane `helm` calls,
     that reads the real CA directly from the Secret and writes it into
     the ConfigMap the data plane's `cluster-agent` actually mounts.
     **Confirmed working end-to-end**: the data plane's `cluster-agent`
     logged "server CA certificate loaded successfully" and "connected to
     control plane," and the `DataPlane` CR's own `status.agentConnection`
     reported `connected: true, connectedAgents: 1` — the final proof
     this foundation's control-plane/data-plane connection genuinely
     works, not just that both Helm installs exited 0.
  Also corrected: the real control-plane/data-plane Deployment names
  carry **no Helm-release-name prefix at all** (bare `cluster-gateway`,
  `controller-manager`, `event-forwarder`, `openchoreo-api`,
  `cluster-agent-dataplane`), fixed in `readiness.go`'s
  `expectedDeployments` from an initial guessed-prefixed set.
- **Scope corrections vs. spec.md's original assumptions, found by
  pulling and inspecting the real v1.3.1 chart artifacts directly**
  (`helm pull ... --untar`), not by reading docs: cert-manager is not
  bundled (this spec now deploys it itself, pinned `1.21.2`); Thunder and
  kgateway are both confirmed *not* hard prerequisites for the control
  plane to start, via the charts' own `validate.yaml` placeholder-domain
  checks (`security.enabled=false`, `gateway.enabled=false`/
  `gateway.tls.enabled=false`); `backstage.enabled=false` (no `Project`/
  `Component` exists yet for the developer-portal UI to show); the
  control-plane chart has six Deployments, not the two spec.md assumed;
  readiness is Helm's own `--wait`/`--timeout`, not a hand-rolled poll
  loop (a real simplification versus spec 008's `waitStatefulSetReady`).
- **`CheckReady` retries 3 times, 2s apart** — added after a
  `go-reviewer` finding: a single GET immediately after Helm's own
  `--wait` already reported every Deployment ready can still race a
  brief apiserver/informer-cache lag, and this check's error propagates
  all the way to `os.Exit(1)` in `serendipd`'s startup path — a transient
  blip would otherwise kill the whole daemon over one foundation-stage,
  smoke-test-only capability.
- **`providers/application/openchoreo.EnsureDefaultDataPlane`** registers
  the one `DataPlane` CR this single-cluster install needs, in
  `openchoreo-control-plane` (confirmed correct against the real cluster,
  not assumed) — hand-constructed via `providers/kubernetes.Client`, not
  via OpenChoreo's own `add-default-dataplane.sh` script as spec.md
  originally assumed.
- **Registration**: `wireOpenChoreo` (`cmd/serendipd/main.go`, after
  `wireOpenBao`) originally registered a method-less `openchoreo.Marker{}`
  under `"application-platform"`; spec 011 deleted it and registers the
  real provider. Zero `core/` changes in spec 010 itself.
- **T007**: both `contract-guardian` and `go-reviewer` PASS. Full detail
  (including the disabled-by-default feature set's exact reasoning, the
  deployment model, and the CA-trust fix) is in new
  `docs/architecture/application-platform.md`; `docs/architecture/
  provider-model.md` gained one breadcrumb (first provider whose primary
  integration mechanism is shelling out to an external CLI tool rather
  than `providers/kubernetes.Client` alone).
- **Explicitly deferred** (spec.md's own Scope-OUT, not silently
  dropped): the real `Application`/`Component` capability (done in spec
  011); the Build Plane and Observability Plane; Thunder/kgateway
  deployment; satisfying ADR 0005's actual T470 benchmark gate (spec 010
  and 011 each produced a dev-machine data point, not the final
  benchmark).

## Spec 009 — what actually got built

`core/resource/secrets.go` + `core/reconcile/secrets.go` +
`providers/secrets/openbao/provider.go` + `api/secret_handler.go` +
`cmd/serendipd`/`api/server.go` wiring — the real Secrets capability on
top of spec 008's foundation, 9 tasks, all complete and committed
(`9343d30`):

- **`Secret` resource kind** (`core/resource`): `SecretSpec{Path
  string}` only — no value field, structurally, because every
  `core/store` mutation writes an unconditional, whole-resource audit
  snapshot with no redaction (confirmed directly against
  `core/store/sqlite/store.go`). `Path` is user-chosen, not derived
  from `Metadata.ID` (unlike `Volume`'s PVC name) — nobody wants to
  address secrets by opaque UUID.
- **`SecretsProvider` interface + `SecretReconciler`** (`core/
  reconcile`): `Exists` (not `Ensure` — nothing to provision, only
  existence to observe) drives `Reconcile`, which only ever sets one
  `"Reachable"` existence Condition. `WriteValue`/`ReadValue`/
  `DeleteWithValue` are three separate **synchronous** methods on
  `SecretReconciler`, called directly by `api/`, never by the async
  reconcile loop — a secret's value has no desired state to reconcile
  toward.
- **`providers/secrets/openbao.Provider`**: thin adapter wrapping spec
  008's `*Client`, zero new wire-protocol code.
- **`api.SecretHandler`**: the generic `ResourceHandler[S,St]`/
  `mountResource` pattern handles Create/Get/List/Update; `Delete` is
  overridden to call `DeleteWithValue` (provider delete, then store
  delete — applying spec 007/`provider-model.md`'s documented lesson
  about not copying a passive-observer Reconciler's no-op delete
  branch). Two new routes, `POST`/`GET /v1/secrets/{id}/value`. First
  `api/` handler ever to depend on a `core/reconcile` type instead of
  only `core/store` — documented as a one-off, not a new general rule.
- **A real, named exception to constitution rule 3**: `WriteValue`/
  `DeleteWithValue` call the `SecretsProvider` synchronously from the
  API request path, bypassing the async reconcile engine — flagged
  directly by `contract-guardian`, accepted as deliberate and scoped
  to exactly these three methods (full reasoning in `docs/
  architecture/secrets.md`), not a general pattern other handlers
  should follow.
- **Audit**: value writes are audited for free by piggybacking
  `Status.LastSyncedAt` through an ordinary `store.Update` — verified
  directly against real `audit_log` rows on a `kind` cluster, no
  plaintext present in `Before`/`After`. Value **reads are explicitly
  not audited** — piggybacking the same way would bump
  `ResourceVersion` on every `GET .../value`, breaking optimistic
  concurrency for unrelated metadata edits; a real read-audit trail
  needs a non-`ResourceVersion`-affecting append-only path `core/audit`
  doesn't have yet, recorded as deferred, not dropped.
- **Real-cluster verification (T007), on `kind`, end to end**: created
  a `Secret` with no value anywhere in its stored `Spec`/`Status`;
  wrote and read back a value byte-for-byte equal through the API;
  independently confirmed against OpenBao's own API (bypassing
  Serendip) that the value lives at the exact path `SecretSpec`
  references; inspected real `audit_log` rows directly and confirmed
  no plaintext leak; deleted the `Secret` and confirmed it's gone from
  both Serendip and OpenBao.
- **T008**: both `contract-guardian` and `go-reviewer` PASS (the
  constitution-rule-3 exception above is `contract-guardian`'s one
  substantive finding, resolved by documenting it rather than
  reworking the design).
- **Explicitly deferred** (spec.md's own Scope-OUT, not silently
  dropped): secret rotation/versioning/leasing, PKI, the Kubernetes
  auth method / workload-identity integration, `Application`/
  `Component` secret injection (needs OpenChoreo, doesn't exist yet),
  UI/CLI/MCP surfaces, a policy engine gating secret access
  (`core/policy` doesn't exist yet), and auditing value reads (needs a
  `core/audit` extension nothing in this codebase has today).

## Spec 006 — what actually got built

`providers/kubernetes` + `core/provider` — the Kubernetes access
foundation + Provider registry pattern, 9 tasks, all merged locally:

- Hand-rolled, generic REST client (`providers/kubernetes`) — not
  `client-go` (its transitive weight is a documented, upstream-
  acknowledged problem, cited directly in ADR 0008). Kubeconfig parsing
  confirmed field-for-field against K3s's own source. Minimal
  `Object`/`ObjectMeta` envelope, raw-JSON spec/status, zero hardcoded
  per-kind knowledge.
- `core/provider.Registry`: in-memory, capability-agnostic map from
  capability name to live Go value, populated only by `cmd/serendipd`
  (the composition root) — resolves `provider-model.md`'s "how does a
  capability interface get wired without `core/` importing `providers/`"
  question left open since spec 001.
- One new dependency, justified and measured: `sigs.k8s.io/yaml` (ADR
  0008) — real transitive weight verified by building a throwaway
  module and running `go list -deps`, not estimated.
- T007's real-cluster verification: `serendipd` with
  `SERENDIP_KUBECONFIG` set genuinely listed real Nodes from a real K3s
  cluster, matching `kubectl` exactly — proven via a deliberately
  trivial, throwaway `nodeLister` proof-of-concept (replaced entirely by
  spec 007's real `StorageProvider`).

## Spec 005 — what actually got built

`agent/bootstrap` + `cmd/serendip-agent`'s `bootstrap` subcommand — real
K3s single-node install/uninstall, 9 tasks, all merged locally:

- Shells out to the *official* `get.k3s.io` install script and its
  auto-generated `k3s-uninstall.sh` (never reimplementing K3s install
  logic), both gated behind `--confirm`; `--dry-run` is completely
  side-effect-free (traced by hand: returns before any check/exec call).
  Version explicitly pinned (`v1.36.4+k3s1`).
- Cross-checking `docs/PRD.md` against spec 004 found a real staleness
  bug: PRD's POC item 2 attributed K3s bootstrapping to spec 004, which
  had explicitly deferred it — corrected.
- `wsl --install`'s distro-catalog fetch timed out on this dev machine
  (a real, confirmed constraint) — worked around with `wsl --export`/
  `--import`, a pure local clone of the existing WSL Ubuntu distro, now
  the established pattern reused by spec 006.
- T007's real-host verification ran the whole install→verify→uninstall
  cycle for real on a disposable WSL2 clone (never the persistent dev
  distro): K3s genuinely installed, node reported `Ready` in ~70s,
  uninstall genuinely removed it, clone fully destroyed afterward.
- T008's holistic review traced every code path in `runBootstrap` by
  hand (this spec's core safety property) and found one real gap:
  `Install`'s download had no bound except Ctrl-C. Fixed with its own
  separate timeout.
- K3s's own defaults left enabled (Traefik, ServiceLB, local-storage,
  Flannel) since Serendip's eventual replacements don't exist yet.
  Recorded breadcrumb: swapping Flannel for Cilium later likely means a
  fresh cluster, not a live migration.

## Spec 004 — what actually got built

`agent/{discover,identity,client}` + `cmd/serendip-agent` — hardware/
environment discovery, stable node identity, self-registration against
the existing `/v1/nodes` API, 10 tasks, all merged locally:

- `agent/discover`: reads `/proc/meminfo`, `/proc/cpuinfo`,
  `/proc/mounts`, `/sys/block/*`, `/sys/class/net/*` directly (zero
  non-stdlib deps). Verified for real in WSL2 — CPU count matched
  `nproc` exactly, memory matched `/proc/meminfo` exactly.
  Disk-partition-suffix stripping handles both traditional (`sda2`→`sda`)
  and NVMe/eMMC (`nvme0n1p2`→`nvme0n1`) naming.
- `agent/client`: its own wire structs, not an import of
  `core/resource` — confirmed directly against
  `core/store/sqlite/store.go` that `Create` accepts a client-supplied
  `Metadata.ID`. `Upsert` does GET-then-POST-or-PUT keyed by the
  locally-persisted identity.
- T009's holistic review found and fixed two gaps: a documented
  `SERENDIP_AGENT_STATE_DIR` env var that was never wired up, and a
  `Conditions`-wiping bug in `Upsert` (fixed by preserving them across
  updates, with a regression test).
- End-to-end proof in WSL2: `register` run twice against a real
  `serendipd` created a Node then updated the *same* Node
  (`resourceVersion` 1→2), `NodeStatus` fields matching standalone
  `discover` output exactly.

## Spec 002 — what actually got built

`api/{problem,middleware,handler,graph_handler,registry_handler,server}.go`
+ `cmd/serendipd` — the HTTP API over `core/`, 19 tasks, all merged
locally:

- RFC 7807 error mapping, request-ID middleware, actor-extraction
  middleware (static `SERENDIP_API_KEYS` — explicitly weak/temporary,
  `ActorMiddleware`'s doc comment names it as the swap-in point for
  `providers/identity` later), generic `ResourceHandler[S,St]` (14
  instantiations in `server.go`), `GraphHandler`, `RegistryHandler`
  (`GET/PUT /v1/capabilities/{name}/default`), two-mux composition
  (`/healthz` bypasses auth, `/v1/` doesn't, both have a problem+json
  404 catch-all).
- `cmd/serendipd/main.go`: real entrypoint, graceful shutdown on
  SIGINT/SIGTERM, verified via the real compiled binary in WSL (Go
  wasn't installed there at the time — installed to `~/go-sdk`,
  user-space, no sudo — reused by spec 003).
- **T019's holistic review caught one real, undisclosed scope gap**:
  spec.md had committed to registry get/set-default-provider endpoints
  that no task ever built — fixed same-session (`RegistryHandler`,
  `CapabilityRegistry.SetDefault` wrapping `resource.ErrValidation` for
  a proper 400). **This is the lesson spec 003's T010 explicitly applied
  again**: a task list can look complete while quietly dropping a
  spec.md commitment no task ever claimed — the final holistic review
  earns its place by re-reading spec.md's Scope-IN against the actual
  code, not just re-running diffs already reviewed task-by-task.
- `core/resource` gained `json:"lowerCamelCase"` tags throughout — this
  surfaced and fixed a real bug (`core/store/sqlite`'s cascade-delete
  audit-snapshot builder defeated `omitempty` by wrapping raw DB text in
  `json.RawMessage`). `core/resource.ErrValidation` and
  `core/audit`'s `WithRequestID`/`WithInterface` are also new here.

All 5 acceptance criteria satisfied and tested; every task got an
independent `contract-guardian` review before commit (git log has
per-task detail if needed).

## Spec 003 — what actually got built

`core/reconcile/{engine,health,cluster,audit}.go` + `cmd/serendipd`
wiring — the reconciliation engine, 11 tasks, all merged locally:

- `Engine`: event-driven (subscribes to `core/event.Bus`, non-blocking
  enqueue per that bus's own "handlers must be fast" contract) +
  30s-periodic-resync dispatch to per-`resource.Kind` `Reconciler`s.
  Single goroutine, sequential — sufficient at edge-1 scale. Each
  `Reconcile` call gets a bounded timeout and reconciler-specific audit
  identity (`Interface="reconcile"`, `Actor="system:reconciler"`) set on
  its context.
- `HealthChecker`/`TCPHealthChecker`: one implemented
  `HealthCheckContract.Kind` (`"tcp"`), stdlib-only, reusable unchanged
  by any future real provider.
- `ClusterReconciler`: the one concrete `Reconciler` — resolves a
  Cluster's Provider, runs its health check, writes a `"Healthy"`
  Condition, idempotently (`Update` only called when the computed
  `{Status, Reason}` actually differs from what's stored — proven
  directly via `ResourceVersion`/audit-row-count assertions, not just
  "no error").
- `cmd/serendipd/main.go`: engine runs alongside the HTTP server, sharing
  the shutdown-signal `ctx` — a real shutdown race was designed around
  (the engine's error channel only fires on genuine crashes, never on
  plain `ctx` cancellation, so it can't race Go's `select` against the
  HTTP shutdown branch).
- **T010's holistic review caught one real (if trivial) constitution
  violation**: `TCPHealthChecker`'s doc comment named K3s/OpenBao inside
  `core/reconcile`, where rule 1 applies unqualified (unlike spec 002's
  Thunder mention in `api/middleware.go`, correctly outside `core/`).
  Fixed, re-reviewed: PASS.

All 6 acceptance criteria satisfied and tested, including two proven
end-to-end via the real compiled binary in WSL (not just `go test`): a
Cluster created through the live API got marked `Healthy: True` within
~100ms, and a real SIGINT still shuts down cleanly with both the HTTP
server and engine active.

## Spec 001 — what actually got built

`core/{id,resource,store,store/sqlite,graph,audit,event,registry}` —
the full resource model foundation:

- **`core/id`**: `id.New()` — UUIDv7 (ADR 0007), one-package dependency isolation.
- **`core/resource`**: `Metadata`/`Condition`/`Status`/generic `Resource[S,St]`
  envelope, `Validate()` (Kubernetes-style label grammar), all 14 resource
  kinds (Organization, Team, Project, Environment, Site, Cluster, Node,
  Application, Component, Capability, Provider, ProviderInstance, Alert,
  Incident) as `resource.<Kind> = Resource[<Kind>Spec, <Kind>Status]` aliases.
- **`core/store` + `core/store/sqlite`**: generic `Store[S,St]` interface,
  SQLite implementation — one shared `resources` table (JSON-blob columns,
  not per-kind tables), embedded migrations, WAL + `SetMaxOpenConns(1)`.
  Full CRUD with optimistic concurrency (`ErrConflict`/`ErrNotFound`).
- **`core/audit`**: every Create/Update/Delete writes one `audit_log` row
  **inside the same transaction** as the data change — verified with a
  live mutation test (temporarily broke the write, confirmed the test
  catches it, reverted).
- **`core/graph`**: edges table, `SyncEdges`/`Children`/`Dependents`/
  `BlastRadius`. Cross-kind cascade delete (`store.ErrHasChildren`,
  cascade=true recursively deletes post-order) — the one genuinely tricky
  design: a `Store[ProjectSpec,...]` deleting a Project must also delete
  Application-kind children it has no Go type for, so cascade operates on
  kind-agnostic `graph.ResourceRef` + raw-JSON audit snapshots, not the
  typed `S,St`. Has a cycle guard (malformed `OwnerRefs` cycles reject
  with an error, not a stack overflow).
- **`core/event`**: in-process `Bus`, published strictly after
  `tx.Commit()` succeeds (never before — an event for a rolled-back
  mutation would be wrong). Optional per-`Store` via `SetBus`, nil-safe.
- **`core/registry`**: `CapabilityRegistry`/`ProviderRegistry` — thin
  wrappers; `SetDefault` is an ordinary `store.Update`, which is exactly
  what makes acceptance criterion 3 true by construction.
- **`cmd/bench-resource-model`**: 10,011 resources, ~20-23MB real process
  memory on the dev machine (directional — see `docs/PRD.md` Hardware
  section, revalidate on real T470 in Phase 3b).

All 5 acceptance criteria (spec.md) are satisfied and tested. Every task
T001-T016 got an independent `contract-guardian` review before commit —
PASS on all, several real findings caught and fixed along the way (see
git log on `core/` commits for detail per-task if needed; not repeated
here since it's git-archaeology, not current state).

`go.mod`'s `go` directive is `1.25.0`, not `1.22` — `modernc.org/sqlite`
v1.56.0 forced this via Go's toolchain mechanism when it was added in
T010. Still satisfies "Go 1.22+" as a floor (ADR 0003 addendum).

## Tooling set up this session (unrelated to spec 001 — separate track)

- **CodeRabbit**: `.coderabbit.yaml` + GitHub App instructions in
  `GETTING-STARTED.md` (browser-only install, not yet confirmed done by
  user). Auto-review gated to **external contributors only**
  (`.github/workflows/label-external-contributions.yml` labels PRs by
  `author_association`; org members' PRs aren't auto-reviewed since
  internal review covers those already). Local advisory review via
  `.githooks/post-commit` — **blocked on the user running two WSL2
  commands with their sudo password** (exact commands in
  `GETTING-STARTED.md`'s "Local review" section).
- **GitHub tracking board** (Jira-style: Milestone=Phase, Issue=Spec,
  native sub-issues=Task, Project board) — designed, approved, **still
  blocked** on GitHub token permissions (Issues write + Projects, tested
  empirically twice, still 403/GraphQL-permission-denied as of last
  check). User was told to check three specific things (Issues
  Read-vs-Read&write checkbox, Projects org-level permission, a possible
  pending-approval step) — verify with `gh api
  repos/Serendip-Infra/serendip-infra/milestones -f title=test -f
  state=open` before assuming fixed. Once unblocked: 8 milestones
  (Phase 0-6 per `docs/PRD.md`), one Epic issue per spec with
  `--parent`-linked sub-issues T001-T019 under spec 001 (mark done ones
  closed), Project v2 board (Backlog/In Progress/Review/Done).
- **Git remote / SSH identity separation**: fixed by user, confirmed via
  `git remote -v` (aliased hosts in use). No further action.
- **Memory saved**: `architecture_change_guardrails` (feedback-type) —
  don't add a new abstraction when one exists; match existing
  error-handling/observability conventions; **multi-tenant isolation is
  not yet an enforced constitution rule**, flagged for a possible ADR
  once spec 002 (HTTP API/auth) makes it concrete. Also
  `propose_shortcuts_before_switching` (feedback-type, added during spec
  003 planning) — a shortcut that reshapes which spec does what or pulls
  a dependency forward (e.g. integrating a real provider like Thunder
  earlier than the roadmap sequence) needs explicit confirmation before
  switching approach, unlike routine implementation-detail calls.

## Next up

1. **Commit spec 012** (working tree only today; run `contract-guardian`
   first per the standing rule).
2. **Open issue, pre-existing since spec 010, not fixed in 012**:
   `providers/internal/helm` runs `helm` without `--kubeconfig`, so Helm uses
   the default kubeconfig context, not `SERENDIP_KUBECONFIG`. It only worked in
   verification because both pointed at the same cluster. Affects both
   Helm-based providers (OpenChoreo, kgateway). Needs its own small fix.
3. **T470 benchmark now strongly recommended next**: footprint keeps growing
   (kind node about 1.67 GiB) and Traefik still idles unused. ADR 0005's gate
   is still unmet.
4. Other capability candidates (none picked; decide at the start of the next
   session): `Secret` injection into Components (spec 009 meets spec 011);
   disabling Traefik to serve on 80/443 (needs a migration story); TLS;
   Identity/Thunder, still gated by ADR 0006's maturity window.
5. **specs 002-012 are still unpushed by deliberate choice** (user
   confirmed: keep working locally, push everything together at a later
   checkpoint) — GitHub auth itself is no longer the blocker (see
   GitHub note above), so this is a scheduling decision to revisit, not
   a technical one to keep re-litigating.
6. GitHub tracking board (milestones/issues/sub-issues/project) was
   fully built for spec 001 and merged via PR #24 — extending it to
   cover specs 002-012 once they're pushed is still open. CodeRabbit:
   CLI installed, only `coderabbit auth login` (browser) remains,
   previously blocked by the account flag — retry now that auth works,
   not yet re-attempted this session.

## Standing instructions (fold into CLAUDE.md properly if this section
keeps persisting across many sessions unchanged — don't let it live here
forever)

- Setup wizard: every required capability shows a recommended default
  (clearly labeled) plus alternatives — never hardcode the default as the
  only path. See `docs/PRD.md` "Setup wizard philosophy".
- Run `contract-guardian` before every commit touching `core/` or
  `providers/` — established as a hard habit this session, not optional.
- Keep `docs/PROGRESS.md` current at natural checkpoints — this file, not
  conversation memory, is the durable handoff mechanism (CLAUDE.md
  "Session continuity"). Rewrite stale sections, don't just append.
