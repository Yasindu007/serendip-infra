# Progress / session state

**Read this file first in every new session**, before touching code or
docs. It's the single source of truth for "what's actually done, what's in
flight, what's next" — overwritten each session, not appended to. Git
history has the detail if you need archaeology; this file is only ever the
current picture.

Companion docs: `docs/PRD.md` (why/what/roadmap, changes rarely),
`docs/constitution.md` (non-negotiable rules, changes almost never).

Last updated: 2026-10-18

## Current phase

**Spec 017 (Authorization, Azure-RBAC style) — COMPLETE** (T001-T012,
2026-10-18). Model and enforcement: `docs/architecture/authorization.md`,
ADR 0014. Evidence: `specs/017-authorization/verification.md`.

**Spec 018 (agent identities, WSO2 Agent Manager POC, and the
identity-provider management channel): spec.md approved and committed
(`b773818`). plan.md and tasks.md (T001–T014) drafted, awaiting the
user's approval.** Key plan decisions:
- The public identity route becomes an exact-path allowlist. Today the
  `/oauth2` prefix exposes introspect, DCR and auth/callback.
- The channel is an in-process port-forward (kubelet WebSocket
  `v4.channel.k8s.io`, stdlib) plus `serendipd`'s own admin login.
  Delegated mode was rejected.
- `SERENDIP_AGENT_ISSUERS` with an issuer router; agent groups are
  ignored.
- Agents can't hold access-management roles, get needs-approval on
  delete, and have one sponsor each.
- T001 DONE: port-forward works on kind 1.34 and K3s v1.36.4 (our pin,
  `agent/bootstrap/install.go`); findings in plan.md.
- T002 DONE: an Agent Manager 1.0.0 quick start (k3d `amp-local`, 5.1 GiB
  at idle) is still running, with agents `serendip-ext` (Reader role on
  resource server `https://serendip.local/api`) and `serendip-norole`.
  The real token: `sub` = agent ID, `aud` = identifier, 3600 s,
  `grant_type` claim, no groups or owner. Any agent gets a
  Serendip-audience token, so Serendip's assignments alone decide. The
  owner is `amp-system-client`, so the sponsor lives in Serendip.
  T011 needs `serendipd` on `:8088` (Agent Manager holds 8080) and a
  hosts entry for `default-idp.amp.localhost` (ask the user).
- T003 DONE (`9f5ae68`): the exact-path identity route, verified live.
- T004 DONE (`535ecfe`): `providers/kubernetes` `DialPod`,
  `ServiceDialer`, `ReadyPod`, a stdlib WebSocket port-forward. About
  33 ms per request on kind.
- T005 DONE (`6633988`): `core/identity.Directory` and the ThunderID
  directory, read as the admin over the tunnel, with the token cached in
  memory.
- T006 DONE (`b9e3c3f`): `GET /v1/authorization/directory` and
  `.../principals/person/{id}/groups`. `source` is `live` or `signedIn`;
  verified with ThunderID scaled to 0 and back.
- T007 DONE (`979ea44`): `SERENDIP_AGENT_ISSUERS`
  (`alias=issuer|audience[|maxLifetime]`, fatal if malformed),
  `oidc.IssuerRouter`, agent principals `agent:<alias>/<sub>` with no
  groups, `grant_type: client_credentials` required, the `actor_sponsor`
  audit column, and a 503 when the sponsor can't be read. A real Agent
  Manager token verifies.
- T008 DONE: agents never manage access, whatever role they hold. Owner,
  User Access Administrator, RBAC Administrator and any custom role
  that can change access can't be assigned to one. An agent's allowed
  delete gets 403 `urn:serendip:problem:needs-approval` and is audited.
  One sponsor per agent. Role views carry `assignableToAgents`.
- T009 DONE: the console offers Agent as a principal type, with a
  required sponsor and only `assignableToAgents` roles. The pickers
  search the live directory, saying when they fall back to sign-ins.
  Check access reads a person's groups live, says so, and handles 404.
- Next is T010 (benchmarks), then T011, the real-cluster run. T011 needs
  the user to add a hosts entry for `default-idp.amp.localhost`, and
  `serendipd` on `:8088` with its console URIs registered by the
  operator tool.
- The scratch kind cluster `serendip-t018` is running, with
  `serendipd` stopped. Agent Manager's k3d `amp-local` is running and
  holds 8080.

The notes below are the research behind it.
(Resolved: the user allowed removing role assignments in the console,
since RBAC guards it; it's the one written exception to "no delete".) Plan, decisions and
accepted risks: `specs/017-authorization/plan.md`; task status:
`tasks.md`. Committed so far:
- `0d82624` T001 spike: ThunderID subjects and groups.
- `43fb525` T002 role definition and assignment resources, scope ancestry.
- `cf5aeaf` T003 `core/policy`: built-in roles, evaluation (~4 µs per
  decision), delegation, last-Owner guard, `LockAssignments`.
- `a0ba669` T004 subject and groups on the principal, audit
  `actor_subject`, known-principals registry, ThunderID `groups` token
  attribute (`UpdateClient`, operator tool now optional `--public-url`).
- `7d96f00` T005 enforcement on every `/v1` route: route table +
  authorizing mux (panics on an unmapped route), list/graph filtering,
  403 shape, `policy_decision` and denied-mutation audit, API keys that
  never had an assignment become Owner at `/`.
  - Reviews caught and we fixed: an unreadable-body bypass, cascade
    deletes and owner removal escaping checks, revoked keys regranted.
- `0272efd` T006 `/v1/authorization`: role definitions (custom create
  and delete), role assignments (list with inherited, create, delete),
  `permissions?scope=`, `principals` (gated on assigning somewhere).
  Reviews closed a delegation escalation (decision 10's "no minting wider
  administrators") and client-chosen IDs.
- `9053421` T007 bootstrap administrator (Owner + Secrets Officer at
  `/`, per role, only if never held; `thunderid.AdminSubject`),
  removed-key cleanup (keeps the last Owner), `serendipd break-glass
  grant-owner`. Decision 7's bootstrap condition was amended (plan.md).
- `40e245a` T008 console: `/access?scope=` page (assignments with
  inherited labelled, add assignment, Check access), permission-aware
  controls via `Guarded`; then "Access control (IAM)" laid out like
  Azure's blade, with confirmed removal of assignments made at the scope.
- `39b14e4` T009: the first benchmark failed the list target (+8.8 ms);
  fixed with an ownership cache in `core/policy`. Now +74 µs p99 per
  request and +0.72 ms per 100-item list (Linux), +0.7 MiB idle.
  p99 is measured via a `GOOS=linux` test binary run in WSL Ubuntu
  (no Go install needed there).
- `5f86a2e`, `8fe7120` T010 on kind `serendip-t017` (simulated T470):
  every AC observed or explicitly test-only, in
  `specs/017-authorization/verification.md`; T009's T470 run: serendipd
  36.3 MiB idle, PASS. Three console polish fixes from the real browser.
- `1e42bc6` T011 final reviews. `/security-review` found a HIGH issue:
  Secret path aliasing across scopes. It's fixed with per-Secret value
  storage (Secret ID + path), path validation, immutable paths,
  server-chosen Secret IDs, and plain-token IDs for every kind.
  **Upgrade note:** Secret values written before this must be written
  again. Also: T470 latency measured (PASS), the last-Owner refusal
  audited, and 5xx responses no longer carry internal error text.
- Then T008 console Access control, T009 benchmarks, T010 real cluster,
  T011 reviews, T012 docs/ADR 0014. After 017: spec 018 (WSO2 Agent
  Manager POC, beside Serendip, may reuse our ThunderID/OpenChoreo on a
  throwaway cluster), then 019 approvals (draft in `specs/019-approvals/`,
  uncommitted, on hold).
- **Added to spec 018's scope (user, 2026-10-18): live identity-provider
  lookups.** Today `serendipd` can't call ThunderID's management API
  (the Service proxy strips `Authorization`), so Check access shows a
  person's groups as of their last sign-in, the principal picker only
  knows people who have signed in, and console URIs on an existing
  install need the operator tool (spec 016's option B). 018 decides how
  `serendipd` reaches the management API, since the Agent Manager POC
  needs it too:
  - option A: an in-process port-forward tunnel;
  - a small in-cluster helper holding the admin credential;
  - a cluster-internal route;
  - Azure's pattern: act as the signed-in person. `serendipd` calls the
    management API with a token issued to that admin for the management
    scope, rather than holding a standing admin credential (as the Azure
    portal calls Microsoft Graph as you).
  It weighs privilege, code size and dependencies (rule 4 / ADR). It
  should be settled once as the channel for every later
  identity-provider feature (agent identities, Serendip-managed groups,
  automatic console URIs, credential cleanup), so none of them stays
  manual. Only ThunderID's management API has this problem: OpenBao's
  header passes the proxy, and the rest is driven through the
  Kubernetes API.
- **Research for that decision (oss-researcher, 2026-10-18; ThunderID
  1.0.1, from source and issues, not yet tested on a cluster):**
  - **Delegated mode is likely feasible.** Nothing restricts `system`
    scope to ThunderID's own CONSOLE client. `serendip` asking for
    `scope=openid system` and `resource=https://localhost:8090/mcp` should
    give an admin a management token from their own roles. Refresh
    re-evaluates roles, and token exchange can only narrow. *Spike it
    first.*
  - **Workload identity is mechanically feasible but all-or-nothing.** A
    confidential `client_credentials` app with a role granting `system`
    works (there's a precedent in openndx PR #633). But 1.0.1 has only the
    one coarse `system` permission, so a "narrow" machine identity would
    really be a full-admin secret. Finer permissions exist in code
    (`system:user:view`, …) but aren't grantable yet.
  - **Connection: the management API shares ThunderID's one port with
    OIDC.** So the public Gateway route (which keeps `Authorization`)
    already reaches it, and a separate tunnel isn't needed. **That also
    means the management API is publicly reachable today**, protected
    only by its bearer-token check. 018 should restrict the public route
    to the login paths (`/oauth2`, `/.well-known`, `/flow`, `/auth`,
    `/console`) and give management a restricted route.
  - **Decided (user, 2026-10-18):** the Gateway restriction is folded
    into spec 018, not done sooner. In 018, do it before or together
    with any feature that relies on the management channel.

**Spec 001 (Foundation) merged to Serendip-Infra/serendip-infra:main via
PR #24.** Specs 002-016 are committed locally only — decided explicitly
with the user to keep working locally and push everything together at
a later checkpoint, now that GitHub auth works again (see GitHub note
below) — not pushing yet is a choice, not a blocker.

**Spec 016 (UI MVP: the Serendip console) — COMPLETE** (T001-T012,
2026-10-18). A browser console embedded in `serendipd`, with a server-side
OIDC login (BFF, ADR 0013), built under supply-chain controls (ADR 0012).
Detail below, under "Spec 016 — what actually got built". Evidence:
`specs/016-ui-mvp/verification.md`.

**Spec 015 (Identity foundation: ThunderID + OIDC human login) — COMPLETE**
(T001-T011; final commit noted in "Spec 015 — what actually got built").
Logged-in people can now call the API with a real OIDC access token, alongside
the static API keys. That's the prerequisite for the UI MVP, which is the next
spec (016).

**Spec 014 (reconciliation hardening) — COMPLETE.** Implementation,
real-cluster verification, both final reviews PASS, docs synced
(`993fa54`); benchmark re-run (T010 item 7) done 2026-10-17 — see the
"Spec 014 benchmark re-run" bullet below. Detail below, under "Spec 014 — what actually got built."

**Spec 013 (edge-1 benchmark harness, simulated T470) — COMPLETE** (`3361e46`).

**Spec 012 (Gateway capability, kgateway) — COMPLETE, T001-T011 all done,
both final reviews (`contract-guardian`, `go-reviewer`) PASS, committed
(`7959511`).** Components can now be exposed outside the
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

## Spec 016 — what actually got built

- **Console:** React + TypeScript + Vite in `ui/`, embedded in
  `serendipd` from `cmd/serendipd/console` (a "not built" page without
  Node; `ui/go.mod` fences `node_modules` off from Go tooling). Screens:
  sign-in, Overview, Applications (list, create, detail), Components
  (create, edit, detail with the reconciliation path). Every form shows
  its API request. No delete action anywhere (constitution 5). Design:
  "Combined", `ui/DESIGN.md`; the vendored rule text is in
  `ui/design-rules/`.
- **Login:** `serendipd` is the OIDC relying party (`core/identity.LoginClient`,
  `providers/identity/oidc.RelyingParty`). It provides `/auth/login`,
  `/auth/callback` and `POST /auth/logout`, and an HttpOnly
  SameSite=Strict session cookie as a third credential in
  `AuthMiddleware`. Sessions are in memory (idle 30 min, absolute 12 h).
  Refresh runs once per session at a time (ThunderID revokes the token
  family on replay). CSRF is a custom header plus an exact `Origin`.
  `GET /v1/session`.
- **Hardening for every client:** server timeouts, a 1 MiB body limit
  (413), `no-store`, a strict CSP plus security headers, and a login rate
  limit.
- **ThunderID:** fresh installs register the console URIs and the default
  sign-out flow through the bootstrap file. `serendipd` checks read-only
  without credentials. Existing installs or a changed
  `SERENDIP_PUBLIC_URL`: one operator command,
  `cmd/serendip-identity-redirects`, through a port-forward (option B,
  agreed 2026-10-18; the Service proxy strips `Authorization`). An
  automatic version is deferred to the HTTPS spec.
- **Supply chain:** only react and react-dom at runtime; pnpm 10.34.6 via
  corepack pinned by hash; install scripts off; a 7-day minimum release
  age (verified: a 3-day-old React was refused); a frozen lockfile; a
  credential guard; no third-party origin at runtime.
- **Verified:**
  - The user signed in and created an exposed Component in their own
    browser; it was audited as `person:admin`.
  - An automated BFF login, callback replay, CSRF, and logout ending at
    ThunderID with the old session then at 401.
  - 28 headless captures with 0 CSP violations, 0 console errors, no
    JS-readable cookie and no overflow at 390 px.
  - No tokens in logs.
- **Measured (simulated T470):**
  - `serendipd` 34.5 MiB, 0.00% CPU (rule 4 PASS; +8 MiB from spec 015);
  - 3 polling tabs at 1.28 req/s: 0.04% CPU (PASS);
  - the cookie path costs +2.7 µs; refresh p95 is 48 ms;
  - every cluster group is within tolerance of spec 015.
- **Reviews:** contract-guardian PASS (×4), go-reviewer PASS (findings
  fixed, including a lost-rotated-refresh-token bug with a regression
  test), and /security-review over `b82af3d..HEAD` with no findings.
- **Still open:** a keyboard-only pass by a person (the automated
  keyboard-relevant checks pass).
- **Deferred, on purpose:**
  - an automatic URI update (option A);
  - sign-in and sign-out as audit events;
  - a persistent or shared session store (ADR 0013 triggers);
  - Secrets screens;
  - Simple/Advanced/Expert modes.

## Spec 015 — what actually got built

Spec, plan (with the T001 "Spike findings") and tasks:
`specs/015-identity-foundation/`. Architecture: `docs/architecture/identity.md`
"What's built (spec 015)". ADR 0006 has a dated re-check. Commits: `e202f27`
spec, `27701e2` plan, `2209225` spike, `b0a55c6` code (T002-T006), `4bbb801`
docs and ADR, `235cbc1` hardening, plus the final T008/T009 commit.

- **`core/identity`**: `Principal`, `Authenticator`, `ErrInvalidCredential`,
  and the `person:<name>` actor (`ActorPrefix`). No product names.
- **`api.AuthMiddleware`** replaces `ActorMiddleware`:
  - an API key first, unchanged;
  - otherwise the authenticator: invalid → 401 (generic body, reason only
    in the log), not ready → 503;
  - a nil authenticator (no cluster) means API keys only;
  - `parseAPIKeys` rejects `person:` actors.
- **`providers/identity/oidc`**: a standard-library, provider-neutral JWT
  access-token verifier, with no new dependency.
  - Algorithms: an allow-list (RS/PS/ES/EdDSA); `none`, HMAC and `crit`
    are rejected; key kty, curve and `alg` must match.
  - Claims: `iss`, `aud`, `exp`/`nbf`/`iat` (30 s leeway, range-checked);
    a required `client_id` rejects ID tokens.
  - Keys: JWKS by discovery or an override URL; at most 32 keys, RSA
    2048-8192 bits; an unknown-kid refresh at most once per 30 s, run
    without the caller's cancellation; cached keys survive outages.
- **`providers/identity/thunderid`**: the ThunderID 1.0.1 Helm chart
  (`VERSIONS.md`).
  - One replica, SQLite on three PVCs, plain HTTP, issuer
    `http://identity.<gateway host>:<NodePort>` (`SERENDIP_IDENTITY_ISSUER`
    overrides it).
  - A fixed public PKCE client `serendip`, declared through the chart's
    first-install bootstrap file, with redirect URI
    `http://127.0.0.1:8250/callback`.
  - The admin password is in Secret
    `serendip-identity/serendip-identity-admin`. It's created only on a
    fresh install and never recreated (a new value would lie).
  - The HTTPRoute exposes only the login paths (`/oauth2`, `/.well-known`,
    `/gate`, `/flow/execute`, `/flow/meta`, `/design/resolve`).
- **`cmd/serendipd`**: the `identity` capability sets up after the gateway,
  in parallel with the platform, behind `lazyAuthenticator`. The
  foundations loop re-ensures the route.
- **T007, real kind cluster, every acceptance criterion observed directly:**
  - **AC2**: identity Ready; discovery from the host has the exact issuer;
    the Secret, release, route (Accepted/Programmed) and PVCs are present.
  - **AC3**: a scripted auth-code + PKCE login as `admin` gave 200 on
    `GET /v1/applications` and 201 on a create; the audit actor is
    `person:admin`.
  - **AC4**: these all got a generic 401: no token, garbage, a foreign
    key, a wrong issuer, an ID token, and the real token after expiry
    (log: `expired`).
  - **AC5**: the API-key e2e (deploy, expose, curl 200, delete) is
    unchanged.
  - **AC6**:
    - a `serendipd` restart: no reseed, same password, login works;
    - a pod restart: byte-identical JWKS, the old token still 200;
    - route deleted: recreated in 17 s, no restart.
  - **AC7**: with the node stopped, API keys gave 200, identity was not
    ready with a reason, and a valid token got an honest 503. After the
    node came back, everything recovered with no restart and the token
    got 200.
  - **AC8**: imports clean; spec 015 adds no product names to `core/` or
    `api/`.
  - The scripted login helper (driving ThunderID's `/flow/execute` API) was
    a scratch script; the flow it follows is recorded in plan.md Spike
    finding 4.
- **T008 (simulated T470, two runs)**: ThunderID idles at **about 10.7 MiB
  and 0.2 mcores**, smaller than OpenBao and about a fifth of kgateway.
  - Every existing group PASSes against spec 014, and the two runs agree.
  - `serendipd` 26 MiB (rule 4 PASS); 16 GB fits at 10-11%.
  - The compare tool now reports a group present only in run B as "new"
    instead of FAIL-vs-0.
  - Write-up: `002-edge1-footprint.md` "Spec 015 re-run".
- **Reviews**: `contract-guardian` PASS twice; `go-reviewer` PASS. Its
  hardening findings were fixed in `235cbc1`.
- **Deferred, on purpose**:
  - recording the stable `sub` in audit rows (today the actor is the
    username);
  - rate-limiting the rejected-token log line;
  - splitting identity setup so retries don't re-run Helm;
  - a dedicated `identity.ErrNotReady` sentinel instead of reusing
    `reconcile.ErrProviderNotReady`;
  - ThunderID signing-cert expiry (365 days, untested upstream).

## Spec 014 — what actually got built

Fixes the four weaknesses specs 010-013 left (spec.md "Problem"). Detail:
`docs/architecture/reconciliation.md`, `provider-model.md`.

- **Early re-checks** (`core/reconcile`): `RequeueAfter` typed error (not a
  failure). Engine-owned backoff 2s→4→8→16, capped at the 30s resync; an
  early re-check doubles it, an **event resets it** (events only come from
  real store changes), resync leaves it. Component/Application/Volume return
  it while converging. Two real-cluster bugs fixed on the way: the
  reconciler's own status-write events were doubling the backoff (gaps
  ballooned to 16s), and a resource capped during an outage stayed
  resync-paced after recovery.
- **Server-side apply** (`providers/kubernetes.Client.Apply`, manager
  `serendip`, force=true) for every Serendip-owned object: local-path PV/PVC,
  OpenBao's 5 objects, OpenChoreo DataPlane (one object incl. gateway wiring,
  `EnsureDataPlane` replaced two functions)/CA ConfigMap/4 defaults/
  Project/PRB/Component/Workload, kgateway GatewayParameters/Gateway. Drift
  code deleted (`ensureObject`, `containsJSON`, `updateSpecIfDifferent`,
  `workloadPatch`, `getOrCreate`). Stays merge patch: the org namespace label
  and the version-gated Gateway API CRD installer. One-time migration limit:
  fields an old `Update` manager set and Serendip no longer sets stay once on
  pre-014 clusters.
- **Degraded startup** (`cmd/serendipd/{wiring,setup,lazy}.go`): fail fast
  only on config errors; per-capability background setup (5s→2m forever; app
  platform after gateway); lazy provider handles return
  `ErrProviderNotReady` → `ProviderNotReady` Conditions, secret value calls
  503; `GET /v1/readiness`; log `serendipd: all capabilities ready` (bench
  harness now waits for it).
- **Foundations loop**: gateway (CRDs + Gateway) and platform (DataPlane +
  defaults) re-ensured every `SERENDIP_REENSURE_INTERVAL` (default 5m);
  failures → `RepairFailed` readiness, never fatal.
- **Verified on kind** (`T010`): spec 011/012 regression flow passed after the
  SSA conversion; Component create→`Serving` **14.2s** (4/4, cached image) vs
  OpenChoreo's own Ready at 11-13s (was a flat 30s) — the plan's ≤10s target
  is unreachable because upstream itself takes ~12s; idle 75s: 20 owned
  objects unchanged, 0 store writes; force-deleted Environment recreated in
  12s, deleted Gateway recreated, Component back to Serving; stale
  `serendip`-owned label removed, other manager's label kept; cluster-down
  startup: API up, honest readiness/Conditions, 503, self-recovery with no
  restart (post-outage convergence resync-paced, ≤~1 min). Also live: the
  spec-013 failure (OpenBao 90s wait missed on a slow pull) now just retries.
- **Reviews**: `contract-guardian` PASS (4 non-blocking; fixed: gateway
  repair starts before the platform is up, backoff reset on error; audit gap
  deferred, below). `go-reviewer` PASS (non-blocking; fixed: no spurious
  `RepairFailed` on shutdown, empty-spec guard on the Component owner check,
  looser timing bounds and a `sync.Once` in tests, new `componentConverging`/
  `requeueWhen` tests). Declined on purpose: mapping `ErrProviderNotReady` on
  delete paths to `RequeueAfter` — a requeue leaves the failed set, and a
  deleted ref is never listed again, so its teardown could be lost.
- **Benchmark re-run (T010 item 7, 2026-10-17)**: two fresh simulated-T470
  runs, `profiles/edge-1/bench/results/2026-10-17-spec014-simulated-t470/`.
  Every pod group PASSes vs spec 013 run 1 (memory and CPU) and run-vs-run;
  rule 4 PASS (`serendipd` 34.8 / 26.3 MiB); 16 GB verdict unchanged (10-11%).
  "Node overhead outside pods" drifted 300 → 512 / 370 MiB — kind
  accounting noise (varies 140 MiB between identical runs, no Serendip
  process there). Create → `Serving` still 30/32 s: **explained, not a
  regression** — fresh clusters pull nginx (12-14 s, node events), which
  lands readiness just after the ~14 s early re-check, so the next one is
  at 30 s; re-probed with the image cached on the same throttled node:
  14.9-15.2 s. Write-up: `002-edge1-footprint.md` "Spec 014 re-run";
  harness/README "Serving" caveat updated to describe the re-check schedule.
- **Backoff cliff fixed (2026-10-17)**: early re-check waits now double up
  to an 8 s cap, for one resync window from when the resource started
  converging (re-checks at ~2/6/14/22/30 s). A first version (cap with the
  old re-check count) left a 22-30 s gap; a real cluster caught it (54 s).
  The revised version was verified with upstream transition times: our
  detection lag was 6-8 s after OpenChoreo's Ready. `contract-guardian` PASS.
  Detail is in `002-edge1-footprint.md` finding 1.
- **Deferred**: auditing foundations-loop repairs (they mutate the cluster
  outside the reconcile path; constitution rule 8 — needs an audit actor for
  system repairs); a "resync now" kick when a capability becomes ready (and
  waking Components on Application change) to shorten post-outage recovery;
  watching upstream objects.

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
  Secret injection, multi-environment promotion, live reconciliation of
  platform defaults (restart repairs), ~30s status lag (no requeue). The
  failed-DELETE retry gap was fixed after spec 012 (see "Post-012 fixes").

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
  (since done: spec 017's `serendip/secrets/value/*` data actions), and auditing value reads (needs a
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

### Post-012 fixes (committed after spec 012, `7959511`)

- **Helm targeted the wrong cluster.** `providers/internal/helm` ran `helm`
  without `--kubeconfig`, so Helm used the default kubeconfig context, not
  `SERENDIP_KUBECONFIG` (pre-existing since spec 010). Fixed with
  `helm.WithKubeconfig`; both Deployers now take the kubeconfig path.
  Verified on kind with `KUBECONFIG` pointing at a nonexistent file: all five
  Helm installs still succeeded against `SERENDIP_KUBECONFIG`'s cluster.
- **A failed teardown orphaned external state forever.** A deleted record's
  ref never reappears in a resync list, so a delete that failed once (e.g.
  the cluster unreachable) was never retried. This had been a known gap
  since spec 007. The engine now keeps refs whose `Reconcile` failed and
  re-enqueues them on every resync until they succeed. Verified on kind: a
  Component deleted while the node was stopped was torn down automatically
  on the first resync after the node came back.

### Spec 013 (edge-1 benchmark harness + simulated T470) — complete

- **Harness:** `cmd/bench-edge1`.
  - One command, reading the kubelet Summary API through the API server, so
    nothing is installed in the cluster. It works on kind and K3s, and on
    Windows and Linux.
  - It reports per-group memory/CPU, node overhead, the largest pods,
    `serendipd`'s footprint, the rule-4 line, the 16 GB verdict, and timings.
  - Subcommands: `compare` and `render`.
  - Setup script and README: `profiles/edge-1/bench/`.
- **Simulated-T470 results**, in `profiles/edge-1/bench/002-edge1-footprint.md`.
  These are a kind node at 4 threads / 2.0 CPUs; not the gate.
  - The whole stack idles at **about 1.3 GiB**, of which Serendip-deployed
    components are about 220 MiB.
  - The Kubernetes API server is about 540–580 MiB, the single largest cost,
    because it serves about 60 CRDs.
  - **Rule 4 PASS** (`serendipd` 23–25 MiB).
  - **16 GB fits comfortably** (about 10% of the budget with 10 light apps;
    about 22 apps of 256 MiB would still be comfortable).
  - OpenChoreo measured about 62 MiB against ADR 0005's "~2 CPU/4GB"
    estimate (Backstage, Thunder and observability are off).
  - Memory repeatable within tolerance; one CPU group (Kubernetes system)
    26 mcores apart vs ±25, kept as a FAIL.
- **The real T470 run is still the open gate.** It's one command once the
  hardware exists (README).

1. **Gaps spec 016 found** (pick one next; decide at the start of the next
   session):
   - **Approval objects for destructive operations** (constitution 5).
     `DELETE /v1/{kind}/{id}` takes effect with no approval today, so no
     client (UI, MCP) may offer delete until this exists.
   - **An audit read API.** Audit rows are written, but no API returns
     them, so the console can't show activity.
   - **RBAC / policy** (`core/policy`): done in spec 017 (ADR 0014).
2. **HTTPS:** cert-manager (already installed) issuing certificates for an
   HTTPS listener on the existing gateway. It needs a decision on where
   trusted names come from on an edge-1 laptop with no public DNS. It's
   also the moment for option A, the automatic console-URI update, since
   it changes `SERENDIP_PUBLIC_URL`.
3. **Other candidates:**
   - Secret injection into Components, plus a write-only Secrets screen;
   - an MCP server (the AI side of constitution 7);
   - disabling Traefik to serve on 80/443 (needs a migration story);
   - **Serendip as the only writer of platform objects** (added by the
     user, 2026-10-18). Kubernetes RBAC gives only `serendipd` write on
     OpenChoreo's CRs. A built-in `ValidatingAdmissionPolicy` rejects
     changes by anyone else, so `kubectl` and other tools can't go around
     Serendip's RBAC, audit and approvals; reconciliation stays the
     backstop. Small, with no new component. Note that OpenChoreo runs
     with `security.enabled=false`, so its own API has no auth and must
     stay cluster-internal.
   - **A developer portal** (added by the user, 2026-10-18). This is
     either Backstage as a pure client of Serendip's API (our plugins call
     `/v1` with the signed-in person's token, never a shared service
     token), or Backstage-style features built into the Serendip console.
     Shipping OpenChoreo's Backstage as-is is ruled out: it would bypass
     Serendip, and OpenChoreo's API has no auth. The risks of using
     Backstage itself: the npm supply chain against ADR 0012, a confused
     deputy, its opt-in permissions, the Scaffolder's sandbox and SSRF
     history, catalog/TechDocs SSRF and XSS, another login surface, and a
     few hundred MiB (rule 4). It needs an ADR. After spec 019.
4. **Watch ADR 0006:** no ThunderID release since 2026-08-25. If none
   lands by about 2026-11-15, run the re-check.
5. **specs 002-016 are still unpushed by deliberate choice** (the user
   confirmed: push everything together at a later checkpoint). This is a
   scheduling decision, not a blocker.
6. **The GitHub tracking board** covers spec 001 only; extending it is
   open. CodeRabbit: only `coderabbit auth login` (browser) remains.
7. **A keyboard-only pass of the console by a person** (spec 016 AC 9).

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
