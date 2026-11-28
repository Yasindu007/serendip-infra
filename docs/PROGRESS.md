# Progress / session state

**Read this file first in every new session**, before touching code or
docs. It's the single source of truth for "what's actually done, what's in
flight, what's next" — overwritten each session, not appended to. Git
history has the detail if you need archaeology; this file is only ever the
current picture.

Companion docs: `docs/PRD.md` (why/what/roadmap, changes rarely),
`docs/constitution.md` (non-negotiable rules, changes almost never),
`docs/risk-register.md` (known risks, performance costs and trade-offs,
with when to revisit each), `docs/build-vs-adopt.md` (what Serendip
builds, adopts or rejected, and why).

Last updated: 2026-11-28 (spec 027 T001 to T004 done; T005 next)

## Current phase

**Spec 026 (Secrets into Components; epic #67): DONE 2026-11-24.** PR
#381 merged (`c5d6888`, merge commit), and #67 closed with it. Every
task issue (#369–#380) is closed. #381 was opened by `serendipinfra`
(the active `gh` account then), not Yasindu007 as CLAUDE.md says. Switch
with `gh auth switch -u Yasindu007` to open a PR, then switch back.
- **After the merge:** PR #383 (`fix-bench-ignore-logs`) adds
  `harness.log` and `serendipd.*.log` to the bench results'
  `.gitignore` (only `result.json` and `summary.md` are results), plus
  this file.
- **Roadmap change (the user, 2026-11-24):** a new v1 GA epic, #382,
  **Spec 036: Workload identity**. It runs after spec 031 (small-3) and
  before 032 (Longhorn), but takes the next free number, so the planned
  epics keep theirs (the user's choice). Each Component gets an identity
  (a ServiceAccount with OpenBao Kubernetes auth, under the Secrets
  roles), plus an opt-in CSI file mount and application fetches,
  mirroring Azure. It's the answer to RP-096. A first renumbering script
  had shifted #73–#76 to 033–036. They were put back to 032–035 on
  2026-11-28 (checked in each issue's timeline). The milestone
  description says 024–036.

**Resume here (after the `/clear` of 2026-11-28 evening):**
1. ~~Benchmark run 2~~: **done 12:55, compare PASS against run 1.**
   `run2/{result.json,summary.md}` are copied into
   `profiles/edge-1/bench/results/2026-11-28-l480/run2/` (run1 and run2
   still untracked).
   - Cold start 158 s, warm restart 37 s, node at idle 1972 MiB / 524
     mcores, Cilium 246 MiB, `serendipd` 27.8 MiB, Components to
     serving 34, 34, 40 s, 16 GB verdict 15%.
   - The volume table shows the harness disk bug again (every volume
     "12 GB", "+469 MiB a day").
2. **Optional package run** (observability and tracing) with `bench-edge1
   --package …`. Each run needs a fresh cluster first: the user runs
   `sudo /usr/local/bin/k3s-uninstall.sh && sudo rm -f
   /etc/cni/net.d/05-cilium.conflist /opt/cni/bin/cilium-cni
   /opt/cni/bin/loopback && sudo reboot`, then `sudo ./serendip-agent
   bootstrap --cni cilium --confirm`, then copies the kubeconfig to
   `~/.kube/config`.
3. **Step 6, record everything** on branch `spec-027` or a small branch:
   - the L480 results;
   - RP-089 (Cilium's cost is measured now);
   - RP-083, closed with its clean-up;
   - RP-096, its bug below;
   - the harness disk bug;
   - `operations.md`'s encryption steps, corrected;
   - `002-edge1-footprint.md`.
4. **Step 7, `bug` issues:**
   - the console can't create Projects or Environments;
   - RP-095 (a Component delete leaves an orphaned Deployment) was hit
     on real hardware;
   - Serendip reports encryption `on` while K3s has it disabled
     (below);
   - the harness's disk probe measures the whole filesystem on K3s.
5. **Reinstall for daily use:** a fresh cluster, `sudo systemctl enable
   --now serendipd` (move the old `~/.serendip/serendipd.db` and
   `openbao-init.json` aside first, because they belong to a cluster
   that's gone), then recreate Project `demo`, Environment `development`,
   Application `web` and nginx with the CLI.
   - The JSON files are in `~/.serendip-l480/` on the desktop. The IDs in
     them are stale, so refill them.
   - The new `admin` password is in the Secret
     `serendip-identity/serendip-identity-admin`.
6. **Spec 027 (Templates and a scaffolder, #68): IN PROGRESS on
   `spec-027` (pushed).** Spec, plan and tasks approved 2026-11-28 and
   committed (`3575691`).
   - **T001 done** (`1a5b569`): spec rewording, ADR 0025, the
     provider's unmapped-field test, RP-104 to RP-108.
   - **T002 done** (`46dad0d`): `core/resource/template.go` (the kinds,
     `TemplateOrigin`, `FieldErrors`, `ValidateTemplateVersion`), and
     `createdFrom` on Applications and Components.
   - **T003 done** (`747a430`): `core/template` (`Render`,
     `ValidatePublish`, the three built-ins with fixed IDs
     `0190d1c0-0027-…`). Parameters may give `examples` (the publish
     stand-in; plan decision 3 updated). The reconciler's port checks are
     now `resource.ValidateComponentPorts`, beside a new
     `ValidateComponentImage` (RP-109: the API checks it at write from
     T005/T006).
   - **T004 done:** `store.Tx`, `store.Atomic` and `store.TxCreator`;
     `sqlite.NewDB(db).Atomically` and `Store.CreateIn` (events after
     commit, in order; a foreign or finished Tx gives `ErrForeignTx`).
     `Create` now shares `createTx`. Inside `fn`, write only through the
     Tx: one connection, so an own-transaction read would wait for ctx.
     RP-108 accepted: 41 creates take 13–14 ms on the L480.
   - **Next: T005** (reusable admission in `api/`, the pending-batch
     overlay, the `createdFrom` guards).
   - **GitHub:** task issues #385 to #398 are all open and linked under
     #68. Epic #68's body is in epic #1's layout with the `## Tasks`
     checklist (T001–T004 ticked), and #385–#388 are closed, by a paced
     script (`gh027.sh`, log `gh027.log` in this session's scratchpad).
     If its log doesn't end in `DONE`, check each issue before
     re-running it.

**Results on the L480 today (evidence in `~/.serendip-l480/rp096/` and
`rp083-after-uninstall.txt` on the desktop):**
- **Step 9, the encryption off/on check (RP-096): a real bug.**
  - The node annotation `k3s.io/encryption-config-hash` (`start-<hash>`)
    is set once and **never changes**: not on `secrets-encrypt disable`,
    not on a reencrypt, and not even its hash.
  - So `/v1/secrets/status` said `on` while K3s reported `Encryption
    Status: Disabled`.
  - **A fix signal that works read-only:** the API server's `/metrics`
    `apiserver_storage_transformation_operations_total{resource="secrets",
    transformation_type="to_storage", transformer_prefix=…}`.
    `k8s:enc:aescbc:v1:` means encrypted writes and `identity` means
    plain. There is also
    `apiserver_encryption_config_controller_last_config_info{hash}`.
    Decide what to report when there have been no writes since a restart.
  - **The K3s procedure that worked:** `disable` or `enable`, restart, wait
    for `/readyz`, then `reencrypt --force --skip` (37 Secrets each time).
    `status` fails with "runtime core not ready" right after a restart.
    The stage stayed `start`, never `reencrypt_finished`.
  - A backup of the datastore and key before the check is in
    `~/k3s-backup-before-rp096.tgz` on the node (0600).
- **Step 10, what `k3s-uninstall.sh` leaves of Cilium (RP-083):**
  - the links `cilium_host`, `cilium_net` and `cilium_vxlan`;
  - the route `10.42.0.0/24 via cilium_host`;
  - `/run/cilium` with its cgroup2 mount;
  - BPF maps pinned in `/sys/fs/bpf`;
  - 44 iptables rules (no ip6tables rules);
  - `/etc/cni/net.d/05-cilium.conflist`, `/opt/cni/bin/{cilium-cni,loopback}`.
  - **The clean-up that worked:** delete those three files, then reboot.
    The host was then clean. The uninstall's closing "Failed to reset
    failed state of unit k3s.service" is harmless.
- **Benchmark run 1** (fresh cluster, `bench-edge1` with no `--sim-*` flags):

  | Measure | Result |
  |---|---|
  | Cold start | **148 s** |
  | Warm restart | 36 s |
  | Node at idle | **1965 MiB**, 527 mcores |
  | With 3 Components | 2045 MiB |
  | Component to serving | 52, 34, 34 s |
  | `serendipd` | **28.2 MiB**, 0.01% CPU |
  | Rule 4 | PASS (and with 3 console tabs) |
  | 16 GB verdict | fits comfortably, 15% |

  - Cilium is 252 MiB, inside the simulated 236–302. Node overhead
    outside the pods is 1427 MiB: K3s's server process, containerd and
    cache.
  - **The harness disk bug:** on K3s, local-path volumes are on `/`, and
    the du probe reported the whole filesystem (about 11.9 GB) as each
    volume's use. That gave "5 GB a day" of growth. Memory and CPU are
    unaffected.
- **Node notes:**
  - The link is now **1000 Mb/s**, after a reboot.
  - The battery threshold files read inconsistently: 75/75 and 80/75 after
    the last writes, and they swapped after a reboot. Judge by behaviour
    (whether it charges below ~75%); it isn't charging at 91%.
  - The `serendipd` service is **disabled** during the benchmarks.
  - The desktop tunnel `ssh.exe` may still hold 8088 and 30080, but
    nothing answers on the node's 8088 until the service is back.

**The plan agreed 2026-11-28 (in order; steps 1–4, 9 and 10 are done; the user reordered so the benchmark runs on fresh clusters after step 10):**
1. ~~Save the state here~~ (this update).
2. `/clear`.
3. The user deletes the old project-less `nginx` Application in the
   console (it needs an Approval).
4. The user installs the tracing package; I measure.
5. `bench-edge1` twice on the L480 (labelled L480).
6. Record the numbers (RP-089, the packages' measured costs) and decide
   whether 8 GB is enough.
7. A `bug` issue for the console gap: **no screen creates a Project or an
   Environment**, so the first Application was made outside any Project
   and couldn't deploy. The CLI does it. Fix it in spec 035, or sooner if
   the user asks.
8. **Spec 027, Templates and a scaffolder (#68),** on the desktop: spec →
   plan → tasks on a `spec-027` branch, with the plan approved before code.
   After it: 028, 029, 030, 031, 036 Workload identity, 032, 033, 034,
   035.
9. With the user's OK: the secrets-encryption off/on check (RP-096; it
   restarts K3s).
10. With the user's OK, last: `k3s-uninstall.sh` and what Cilium leaves
    (RP-083; it wipes the cluster), then reinstall.

Any time: a better Ethernet cable (the link is 100 Mb/s), optionally a
second 8 GB stick, and a check that the battery limit survives a reboot.

**The first real node: a ThinkPad L480, set up 2026-11-28.**
- **Hardware:**
  - CPU: i5-8250U, 4c/8t, more than a T470's 2c/4t.
  - Memory: 7.5 GiB usable, with two SO-DIMM slots, so 16 GB is one
    stick away.
  - Disk: Samsung PM971 128 GB NVMe.
  - Battery: 1005 cycles, 79% health. Its charge limit is 75–80%; the
    `charge_control_*` files read swapped, while
    `charge_start/stop_threshold` read 75/80.
  - Ethernet: Intel I219-V, at only 100 Mb/s (the cable or the port).
- **OS:** Ubuntu Server 24.04.5 at **192.168.1.22**, reserved for it on the
  home router (DHCP binding by its Ethernet MAC).
  - User `serendip`. Key login works from the desktop's
    `~/.ssh/id_ed25519`; password login over SSH is still allowed.
  - Lid and sleep are disabled; there's a 4 GB swap file.
  - `sudo` needs the user's password, so the user runs every root step.
- **K3s:** `serendip-agent bootstrap --cni cilium --confirm` installed
  K3s v1.36.4+k3s1 with Cilium 1.20.5.
  - Secrets encryption is on: the node carries
    `k3s.io/encryption-config-hash`.
  - The swap file didn't stop K3s.
  - The user's kubeconfig is `~/.kube/config` (`KUBECONFIG` is set in
    `~/.bashrc`).
- **`serendipd`:** a systemd service, `/etc/systemd/system/serendipd.service`,
  running as `serendip`.
  - Its state is in `~/.serendip/` (the store and `openbao-init.json`,
    both 0600). The binary is `~/.serendip/bin/serendipd`, built from
    `main` `ae70292`, and `helm` v3.16.4 is in `~/bin`.
  - It listens on 127.0.0.1:8088 only. Logs: `journalctl -u serendipd`.
  - Every capability was ready **3 min 12 s** after the start. The
    observability package installed in ~3.5 min; its logs adapter
    restarted 3 times while OpenObserve started.
- **Reaching it from the desktop:** `ssh -N -L 8088:localhost:8088 -L
  30080:localhost:30080 serendip@192.168.1.22`, then
  http://localhost:8088.
  - The desktop's kind `serendip-t018` must be stopped, because it holds
    30080. It was stopped 2026-11-28; `docker start
    serendip-t018-control-plane` brings it back.
  - Sign in as `admin`. The password is in the Secret
    `serendip-identity/serendip-identity-admin`, read on the node.
  - The CLI is `~/.serendip-l480/serendip.exe --server
    http://localhost:8088` on the desktop, signed in as `admin`.
- **What's on it:**
  - the observability package;
  - Project `demo` with Environment `development`, Application `web`,
    and Component and Deployment `nginx` (`nginx:1.27`). nginx answered
    200 through its endpoint;
  - the first, project-less Application `nginx` (to delete, step 3).
- **Spot readings, not settled numbers:**

  | Phase | Node working set | Host used (`free`) | Notes |
  |---|---|---|---|
  | Bare K3s and Cilium | 1096 MiB | — | Cilium's pods 233 MiB (the simulation said 236–302) |
  | The whole platform | 1783–1801 MiB | 2313 MiB | `serendipd` ~16 MiB |
  | Plus observability and nginx | 3121 MiB | 2744 MiB | Observability's pods 264 MiB (the simulation said ~275). The working set includes cache from new images; `free` shows +431 MiB |

**Spec 026, for reference** (tasks.md's "Done as" entries have the
detail):
- **T011, done:** ADR 0024; `application-platform.md` "Secret variables
  (spec 026)"; `secrets.md` (re-unseal, values reaching workloads, the
  status route); `authorization.md`, `operations.md` ("Running secret
  variables", with the upgrade's roll and the downgrade), and
  `bootstrap-agent.md`; build-vs-adopt; and RP-096 to RP-103.
  docs-keeper's two findings are fixed.
- **T010, done** (`c849380`; criterion 12 in `verification.md`). It was
  a same-host A/B on `serendip-t018`: `main`'s `serendipd` against
  `spec-026`'s, run A, B, A, B on copies of the t026 store. B − A was
  −0.1 MiB and +1.0 MiB, and no new pods ran. The evidence is in
  `profiles/edge-1/bench/results/2026-11-24-spec026-same-host/`.

- **T001–T008, done:** tasks.md's "Done as" entries have the detail
  (core, reconciler, provider, encryption at rest, API, CLI/MCP, console).
- **T009, done** (`a0690b9`; `verification.md`). Criteria 1–11 PASS:
  - on `serendip-t018`;
  - criterion 8 also on a `rancher/k3s` container with the agent's
    flags, where it reported `encryptionAtRest: on`;
  - the evidence is in `~/.serendip-t018/t026/evidence/`.
  - **Fixed:**
    - F1: no-value and not-found messages name the variable and key;
    - F2: an unreachable or sealed OpenBao is `ErrProviderNotReady`
      (503, with a fixed text and no upstream answer);
    - F3: `/v1/secrets/status`'s `ready` follows OpenBao's health;
    - F4: `DeleteComponent` deletes `<cmp>-secret-env` first.
  - **Recorded, both older than 026:**
    - RP-094: OpenBao stayed sealed after its pod restarts. **Closed by
      T012.**
    - RP-095: a Component delete leaves its Deployments behind (still
      open).
- **T012, done** (`10273ec`; added at the user's request, plan decision
  13, spec criterion 13 and scope item 11).
  - The OpenBao provider unseals OpenBao again with the stored key when a
    call or the status read finds it sealed, then retries once
    (`providers/secrets/openbao/unseal.go`).
  - It tries at most once every 10 s, and never initializes.
  - Live: the resync unsealed a restarted OpenBao within seconds.
  - ADR 0009 is amended. **T011's docs should mention this** in
    `secrets.md` and `operations.md`.
  - Decision 12 was confirmed: on the upgrade, Deployments following the
    latest release rolled once, and a pinned one didn't.

**State left (2026-11-28):**
- **On the desktop, no `serendipd` runs.** The user's dev one
  (`dev-up.ps1`, `t023.db`) is stopped. Start it with
  `.\scripts\dev-up.ps1 -Restart`, after `docker start
  serendip-t018-control-plane`; that clashes with the L480 tunnel's port
  30080. The kind clusters `t025` and `serendip-t025` were still running.
- In the t026 store, `verifier` has Secrets Officer at Project
  `t014-builds`.
- Five ReleaseBindings from older test stores (`t020-web`, `t020-api`,
  `rp018-check`, `t019-*`) are still `state: Undeploy` to free CPU.
  Revert with `Active`.
- **Not allowed from this session (auto-mode classifier):**
  - running `dev-up.ps1` with `-ExecutionPolicy Bypass`;
  - starting Docker Desktop;
  - editing `.claude/settings*.json`;
  - listing every Windows process (use Git Bash's `ps -ef`).

### Spec 026's design (plan.md; decisions are numbered there)

- **Delivery (decision 1).**
  - Serendip writes one plain Kubernetes Secret, `<cmp>-secret-env`,
    per Deployment into its data-plane namespace. The component type
    adds it with `envFrom`.
  - Only its name and a metadata-only version reach OpenChoreo, in the
    ReleaseBinding's
    `componentTypeEnvironmentConfigs.serendipSecretEnv {name, version}`.
  - The version is a pod annotation, so a new version rolls the pods.
  - Rejected: ESO, a rendered Secret (it would sit in plaintext in
    RenderedRelease), OpenBao's injector and CSI driver.
- **Core, done.**
  - `ComponentSpec.SecretEnv` and `DeploymentOverrides.SecretEnv` (`{env,
    secret, key}`), with validation and clash rules
    (`core/resource/secretenv.go`).
  - `DeploymentStatus.SecretEnvVersion`.
  - The resolver is `core/reconcile/secretenv.go`. Values are read only
    on a version mismatch. A False result means no platform change.
    `held` tracks what the platform holds.
  - `Deploy(..., secretEnvVersion)`.
  - The optional `SecretEnvProvider`, with `ErrSecretEnvPending` and
    `ErrSecretEnvUnsupported`.
  - `SecretReconciler.OnValueWritten` and `EnqueueSecretUsers`.
- **Provider, done (T004, `providers/application/openchoreo/secretenv.go`;
  tasks.md's "Done as").**
  - `SecretEnvVersion` and `WriteSecretEnv` work on `<cmp>-secret-env`.
    The namespace comes from `ProjectReleaseBinding.status.namespace`,
    and pending covers both a missing namespace and an apply 404.
  - `Deploy` sets `serendipSecretEnv` only when there's a version. It
    returns `NeedsNewRelease` before the binding apply when the release's
    type lacks the property.
  - `Undeploy` deletes the Secret first.
  - `ConfigSchemas` strips the property.
  - **Not yet live:** the real template change, checked first in T009.
    Every running Deployment rolls once on upgrade, because the type is
    in the release hash (decision 12). Confirm that in T009 too.
- **For the risk register (T011):**
  - encryption at rest: the annotation shows it was configured at
    start, not that older Secrets were re-encrypted; `secrets-encrypt
    disable` may leave it (not checked); the key is on the same disk;
  - the operations.md enable steps haven't been run yet;
  - values rest in the cluster as plain Secrets;
  - references aren't release-bound;
  - delivery reads aren't audited;
  - a rotation rolls every user;
  - two platform GETs per Deployment with secret variables per pass
    (the project binding and the Secret);
  - an apply 404 reads as pending for good if the path were ever wrong;
  - clearing value buffers is best effort;
  - every Deployment rolls once on the upgrade (a new type, so a new
    release);
  - a clash with platform-injected variables (spec 025's tracing ones);
  - `GET /v1/secrets/status` is read at `/`, so a Project-only user's
    console shows no encryption banner (T006, by decision 9).

### Test beds

- **`serendip-t018`** (kindnet) is **stopped** (since 2026-11-28, for
  the L480 tunnel's port 30080), with no `serendipd` on it.
  - The kubectl default context is `kind-serendip-t018`. The repo's
    kube-context guard reads the default context, so mutations need it.
  - CPU requests were at 96% before T009 undeployed the five older
    Components. A rolling update's surge pod can stay Pending when the
    node is that full.
- `serendip-t025` and `t025` are stopped (`docker start
  <name>-control-plane`).
- **Not allowed from this session (auto-mode classifier):** starting
  Docker Desktop, and editing `.claude/settings*.json`. The user does
  those.

### What spec 025 left behind (done 2026-11-23, PR #368 merged)

- ADR 0023 (Cilium stays edge-1's default; `--cni default` for tight
  machines), `docs/architecture/networking.md`, and RP-081–RP-093.
- **Re-measure on the L470** (the user's first real T470-class node):
  - Cilium's cost (+443 MiB idle on the simulated T470);
  - `TracingRequirements`;
  - what `k3s-uninstall.sh` leaves of Cilium (RP-083).
- **Watch items:**
  - RP-093: Hubble series grow with pod pairs;
  - RP-092: no per-pod OTLP rate limit;
  - RP-091: the node guard keys on two namespace names.
- **The t025 test bed:** `docker start serendip-t025-control-plane`, then
  `bash ~/.serendip-t025-dev/t012/start.sh` as a background run.
- **Gotchas kept:**
  - `kind load` fails here: use docker save, then ctr import into the
    node.
  - In Git Bash, set `MSYS_NO_PATHCONV=1` for `docker run --tmpfs /run`.
  - Long runs go from the user's own terminal, because the memory reaper
    kills background shells.

**Spec 024 (alert rules and notification channels; epic #65): DONE
2026-11-20.** PR #352 merged; T013 ticked (77f068b), #351 and #65 closed
(board Done). The user took the scratch credentials as revoked; the
local folder `~/.serendip-t018/t024/secrets/` was still there (asked
whether to delete it). The task
issues (#339–#351) exist, and `main..spec-024` was rewritten so that
T001–T012's commits carry `Closes #N` (T013 left out; tree unchanged,
checked).
- **T013:** see tasks.md's "Done as" note. ADR 0022, `operations.md`
  "Alerts (spec 024)", the new `docs/guides/alert-integrations.md`, the
  architecture docs, build-vs-adopt, and RP-069/070/072/073/074 (RP-062,
  066 and 068 updated). docs-keeper's one finding is fixed. **Found:**
  the heartbeat can't send headers, so Gatus (the plan's suggestion for
  `small-3`) needs a relay; the guide suggests healthchecks.io or Uptime
  Kuma, and RP-074 records it. An optional header from a Secret would be
  a small follow-up if wanted.
- **T012:** see tasks.md's "Done as" note.
  - **RP-080:** option a (the user's choice). The lastEvaluated refresh
    is hourly, and the API shows the evaluator's live time.
  - **RP-079:** unknown `metadata` fields get a 400.
  - **Security fix:** an SMTP server's Secrets now need value read, on
    naming and on a host or port change. Before this, a Contributor at
    `/` could exfiltrate any Secret through a relay pointed at its own
    host.
  - The go-reviewer shutdown fixes are in.
  - contract-guardian passed.
- **T011:** `profiles/edge-1/bench/results/2026-11-15-spec024-simulated-t470/`.
  - **Passed:** rule 4 (79 MiB idle; a same-host A/B puts spec 024 at
    +1 MiB over spec 023) and criterion 12 (every group).
  - **Latency:** plateaued over 2.5 h (p95 0.8–1.2 s), which stands for
    3 days by the user's method.
  - **Cost:** 200 rules cost `serendipd` +4.2 mcores and the node +149
    to +234 mcores (OpenObserve), recorded as RP-080.
  - **Fixed after the run, not re-measured:** the 66 s cadence, lateness
    counted from disabled time, and noData counted as failed.
  - **Harness:** keeps `serendipd`'s log across restarts and validates
    bench rules before a run.
  - The run was started from the user's own Git Bash terminal, because
    Claude Code's memory-pressure reaper killed two runs.
  - `serendip-t018` is stopped (`docker start serendip-t018-control-plane`,
    then `.\scripts\dev-up.ps1 -Restart -NoBuild`).
  - `bench-t470` is still up; delete it with
    `bash profiles/edge-1/bench/kind-t470-setup.sh down bench-t470`.
- **T010:** `specs/024-alerts/verification.md`, criteria 1–12 and 14
  live on `serendip-t018` (7's measurements and 13 are T011's).
  - **Fixed:** F1 `core/notify`'s dialer gave one unreachable address
    the whole 10 s (a broken IPv6 route timed out every send): families
    now alternate and each attempt gets a share; F2 the Alerts page
    showed IDs; F3 the catalogue's `--resource` description named
    `ownerId`/`metadata.version` (real: `ownerRefs`,
    `resourceVersion`); F4 alerting times now UTC; F5 the allowance
    hint; F6 the CLI rule list's STATUS; F7 spec criterion 5 now says an
    in-house LAN receiver (the cluster's ranges are always refused);
    F8 the unused 15 min backoff step.
  - **Not live:** a separate SMTP test server (an SMTP server can't be
    private, and creating an ethereal.email inbox was denied by the
    session), Telegram's own 429. Healthchecks.io going back UP after
    the restart wasn't confirmed by the user (Serendip's beats resumed).
  - **Tooling** in `~/.serendip-t018/t024/` (helpers, console scripts,
    the `t024-alerty` image, the receiver, evidence; `secrets/` holds the
    user's Telegram, Gmail and healthchecks values, to delete).
  - **State left:** the test Component, receiver namespace, n8n,
    tunnels and images removed; the heartbeat deleted; the observability
    package reinstalled. **This session's `serendipd` (T010 build, on
    `t024/t024.db`) runs on :8088**; the user's dev one (`dev-up.ps1`,
    `t023.db`) was stopped for the run: restart it with
    `.\scripts\dev-up.ps1 -Restart -NoBuild` (or `-Restart` for a fresh
    build).
  - **The user should** revoke the scratch bot's token (BotFather
    `/revoke`), delete the Gmail app password and the healthchecks.io
    check, then delete `~/.serendip-t018/t024/secrets/`.
- **T009:** `ui/src/screens/Alerts.tsx` (Component and Application
  sections, `/alerts`, `/alerts/{id}`), `Notifications.tsx`
  (`/settings/notifications`), `lib/alerts.ts`, a top bar with the bell
  in `Shell`, and Settings in the rail. **New route** `POST
  /v1/secrets/{id}/telegramchats` for "Find chat" (Secret value read;
  CLI `channels find-chat`; off MCP). Acknowledge is checked at the
  rule's scope (the permissions route refuses `Alert/…`). Rule deletes
  cascade to their Alerts. Package removal wording extended. Gzipped JS
  104.1 → 118.7 KiB. The screens haven't been seen in a browser yet:
  T010 does that.
- **T008:** the commands and tools came from T007's catalogue; T008
  added `Param.MCPMax` (an MCP-only bound and default: `alerts_list`
  answers at most 50), Alert state in CLI tables,
  `cmd/serendip/alerts_test.go` (every command's request and usage
  errors, the cap, and the bot token in no CLI or MCP output) and
  `docs/cli-and-mcp.md`'s "Alerts and notifications".
- **T007:**
  - `api/alerts_handler.go` and `api/notifications_handler.go`. Every
    plan-decision-12 and -15 route is registered, and `pendingRoutes` is
    gone.
  - Secrets show only as `secretSet`. Platform default channels show as
    `platformChannel` below `/`.
  - Destinations are checked at save, after authorization, through
    `api.Notifier`/`WithNotifier` (`core/notify`'s Client).
  - Test sends answer 200, 422 or 502, two at a time.
  - **Status is the server's** on rules, channels and SMTP servers
    (`WithServerStatus`).
  - **The heartbeat loop** is new (`core/reconcile/heartbeat.go`), started
    with the evaluator. `wireAlerting` now runs before `NewServer`.
  - Catalogue: CLI groups `alerts`, `alertrules`, `channels`, `smtp` and
    `heartbeat`, with `get`, not `show`. Rule writes, Alert reads and ack
    are MCP tools; channel, SMTP and heartbeat writes are CLI-only.
  - RP-078 is new.
  - Both reviews are in, no blockers, and the cheap findings are fixed.
    tasks.md lists what's left as is, with reasons.
- **T006:**
  - `providers/observability/openchoreo/conditions.go` and
    `prometheus.go` evaluate all four conditions, replayed against
    T001's recordings. The restart counter is in the allowlist, and in
    the bench copy.
  - The built-in `observability-disk` rule (warning, 85%) is declared in
    the catalogue and created on Ready. A delete aimed at it gets 409;
    it goes by cascade. **Package removal now needs `cascade=true`**
    (the console sends it), and the API refuses changing `builtIn`.
  - The cluster's ranges reach `core/notify` every 5 minutes, and the
    Checker refuses private destinations until the first read.
  - New `ConditionRefusedError` → reason `ConditionRefused`. A metric
    `value` is in millicores or MiB.
  - Plan decisions 3 and 4 amended. RP-076 and RP-077 are new; RP-068's
    residual is closed. Both reviews are in: contract-guardian pass;
    go-reviewer's findings fixed, one left with its reason.
- **T005:** `core/reconcile/alerts.go` (evaluator) and `delivery.go`
  (delivery loop), wired in `cmd/serendipd/alerts.go`. **Retention
  records Approvals** (one per rule per pass, by policy; contract-guardian
  blocked the plan's "no Approval" under constitution 5, plan amended,
  RP-071). go-reviewer's 9 findings fixed.
- **T003:** actions in `core/policy/builtins.go`
  (`allowPrivateDestination/action` Owner-only); `api/authz.go`
  `alertingRoutes()` has every decision-12/15 route's rule, the
  unregistered ones in `authz_routes_test.go`'s `pendingRoutes` (**T007
  empties it**). Unsilence is now `POST …/unsilence` (plan amended: a
  DELETE route is a delete with an Approval). `DELETE /v1/heartbeat` uses
  `targetSingleton`. Body checks via the generic handler's
  `WithAuthorize` (`api/alerting_authz.go`): a rule's channels need read
  (platform defaults excepted), a channel's Secrets need
  `secrets/value/read`, allowances (and changing an allowed channel's
  destination) need the Owner action. **T007 must wire `allowancesAt`
  for the heartbeat's allowance at `/`.** RP-075 accepted.
- **T002:** the five kinds in `core/resource/alerting.go`; `store.ErrInUse`
  (409) for a Secret, channel or SMTP server still in use. Paths
  `alertrules`, `notificationchannels`, `smtpservers`, so **T003's actions
  are `serendip/alertrules/*` etc.** (path-derived), not decision 9's
  camelCase. Generic routes serve them until T007.
- **T004:** `core/notify`, stdlib only; card matches T001's recording.
  go-reviewer's keep-alive finding fixed (pooling would have skipped the
  address check). contract-guardian: pass.
- **T001** (plan "Spike findings" 1–6; recordings in
  `providers/observability/openchoreo/testdata/condition-*.json` and
  `core/notify/testdata/`):
  - metric query as planned (a `for` of N min reads N + 1 points);
  - the logs answer has an exact `total`, so `logMatch` queries
    `limit: 1` and reads it;
  - **restart counter wasn't collected:** T006 must add
    `kube_pod_container_status_restarts_total` to
    `prometheus-values.yaml`'s `metricAllowlist`. Pods match by
    `kube_pod_labels` UIDs; `max − min` over the window, not `increase`;
    lag ~40 s;
  - latencies 10–460 ms. Scratch namespace deleted, kube-state-metrics
    args restored.
  - **Telegram:** card and URL button accepted; only a `localhost` host
    is refused in a button (plain http is fine); threading via
    `reply_parameters` works; no 429 at 70 messages (T004 uses the
    documented shape); `getUpdates` finds the chat only after the person
    messages the bot.
  - **Email:** Gmail app password delivered to the inbox on 587 and 465
    (the free default). Brevo with a `@gmail.com` sender: one inbox, one
    Spam (DMARC unaligned), so a relay needs an owned, authenticated
    domain. **Brevo offers AUTH before STARTTLS on 587**: auth only after
    TLS is required, and T004 tests it.
  - All scratch credentials revoked (bot `getMe` 401, SMTP key and app
    password deleted). The probe lives in that session's scratchpad only.
  - **No public domain exists yet.** The user may register one
    (`serendipinfra.com` or `serendip-infra.dev` suggested) for email
    sending with DKIM; not decided.
- **`backfill.sh` is DONE** (22:47 UTC 2026-11-15). GitHub for spec 024
  is now unblocked: epic #65's body to IN PROGRESS, and T001–T013's task
  issues as one paced batch, created with assignee, milestone, a short
  title and Priority/Area/Size set.
  - **Deferred cleanup (the user, 2026-11-16: "later but keep in
    mind"):** #283 is an orphan duplicate of #284 (Spec 019 T006), to be
    closed. About 245 backfilled tasks lack an assignee and a milestone,
    and 59 titles carry the whole tasks.md heading (up to 927 chars).
    The fix is one paced PATCH per task (assignee, milestone, short
    title), about 13 h. Board Priority/Area/Size are empty on finished
    work; the plan is to skip them, Area not decided. Epic #65 → "Spec 024: Alert rules and notification channels" and
#66 → "Spec 025: Networking: Cilium, Hubble and traces" (two paced
writes, done or pending: check the epics).
  - **Then link the finished tasks' commits (the user, 2026-11-15:
    "neat and tidy").** They were committed before their issues existed,
    so none says `Closes #N`. `spec-024` isn't pushed, so before its
    first push, rewrite only `main..spec-024` to append `Closes #<T00N's
    issue>` to the body of each task's commit. Match each commit by
    `T00N` in its subject:
    - T001 `214da35`, T002 `ee9cc97`, T003 `d8f35a1`, T004 `1f2d65c`,
      T005 `c79f460`, T006 `2c0b616`, T007 `92ac3a1`, T008 `e107b49`, T009 `da3484a`, T010 `cb54de9`, T011 `66435ca`, T012 (its `feat(api): spec 024 T012` commit), T013 (its `docs: spec 024 T013` commit)
      and the two `spec 024 T010` fix commits after it (hashes change
      after the rewrite);
    - non-interactively: `git filter-branch --msg-filter` over
      `main..spec-024`, or `git rebase --exec`.

    Check `git log main..spec-024` afterwards: every task commit should
    carry its line, and the tree should match the pre-rewrite tip
    (`git diff <old-tip> spec-024` is empty). The PR merge then closes
    each issue. Later tasks put `Closes #N` in their own commit as usual.
- **Traces moved to spec 025** (the user agreed, 2026-11-15), with
  trace-based alert conditions. The research for them is in spec 024's
  plan, "Carried to spec 025"; the earlier draft with full trace
  decisions is commit `02c86b4`.
- **The plan's main decision:** `serendipd` evaluates every rule itself
  (polling the metrics and logs adapters, and Prometheus's
  kube-state-metrics for restarts) and sends notifications through one
  validating stdlib client, `core/notify`. OpenChoreo's alerting path
  isn't used (its metric rules need the Prometheus Operator and
  Alertmanager ADR 0021 removed; its Observer would send to person-typed
  URLs, RP-062). Recorded as ADR 0022.
- **Channels (all free, outbound only), settled 2026-11-15:** Telegram
  first (cards with a "View in console" URL button, resolution threaded
  as a reply), email through any SMTP relay (Gmail app password or Brevo;
  no SES, no mail server on the laptop), and the signed webhook as the
  integration point (n8n, Node-RED, Apprise API). Each channel has a
  `minSeverity`. Slack, Discord, Teams and ntfy formats are deferred and
  reached through the webhook for now. Phone calls and SMS go through a
  webhook tool (Twilio, about 3 US cents a minute; none are free).
  Unofficial WhatsApp clients are rejected.
- **Heartbeat (dead man's switch)** added: `serendipd` pings an outside
  monitor every minute, only while it can alert. Suggested monitors:
  healthchecks.io (free, Telegram), or Gatus on a second device. All the
  outside tools are suggestions, not dependencies (plan "Suggested
  outside tools").
- **ThunderID (checked at v1.1.0, repo now `thunder-id/thunderid`):**
  email and SMS OTP as a second factor, yes; TOTP, no; passkeys exist but
  aren't documented. A Telegram OTP relay through its Custom SMS sender is
  possible but a workaround (plan "ThunderID OTP"). **ThunderID released
  v1.0.2, v1.1.0 and v2.0.0-m1 (Oct–Nov 2026): ADR 0006's re-check and a
  version bump are due.**
- **Prior art:** WSO2 cs-tools' alert services (design adopted, code not).
- **Next:** T012 (reviews), then T013.
- **Later, after spec 024 (agreed 2026-11-15):** composite alert
  conditions, like AWS CloudWatch composite alarms (plan "Explicitly NOT
  done").
- **After spec 024 (agreed 2026-11-15): a PIM spec, before spec 025.**
  Numbered 026 (so #66 keeps 025), done first. Azure PIM-style eligible
  role assignments and time-bound activation (reason, duration cap,
  optional approver, per-role settings like Azure's role management
  policies). Only principals eligible (usually through a group, like
  Azure's role-assignable groups) can activate. **Also in that spec
  (agreed 2026-11-15): permission ceilings**, like AWS IAM permission
  boundaries and organizations' service control policies: a cap on what
  a principal's roles can ever grant, whatever is assigned or activated,
  mainly to bound agents (Azure has no direct equivalent; the nearest are
  deny assignments). Approval once per
  activation, not per delete; deletes under an activation stay
  policy-approved Approvals, so constitution 5 holds. Mainly for agents,
  whose deletes each wait for a person today. Delete locks (Azure's
  `CanNotDelete`) possibly later. **Standing rule:** follow Azure's
  procedures and structures whenever possible, and name the Azure
  analogue each time.

**Spec 023 (observability package): done and closed out on GitHub
(2026-11-15): PR #197 merged (`b568806`), epic #64 closed.**
- **After the close-out (2026-11-15), upstream `main` = `5bb071d`:**
  - **#197's merge conflict:** `spec-023` had been cut while upstream
    `main` was stale, and `CLAUDE.md` conflicted. The cause: the
    `github.com-org` SSH alias couldn't fetch, because `serendipinfra`
    had no SSH key on GitHub. `~/.ssh/id_ed25519_org.pub` is now on that
    account, and `git fetch upstream` works. The fork's `main` is synced
    too (it was 302 commits behind).
  - **Dependabot #78–#81 merged:** `actions/github-script` 9,
    `actions/setup-go` 7, `modernc.org/sqlite` 1.61.1, `golang.org/x/sys`
    0.50.0. **`go.mod` is now `go 1.26.0`** (both Go bumps need it). CI
    reads the version from `go.mod`, and Go fetches the 1.26 toolchain
    itself on older installs.
  - **PR #210 (`4eedba7`), console bug behind a flaky test:** the Logs
    panel's first recent-lines read could start after a quick click on
    Follow and wipe the streamed lines (`log follow reconnects` failed
    4/20). Fixed in `ui/src/screens/Runtime.tsx`, with two new tests
    (late read, Refresh after a follow). Now passes 40/40. No-spec fixes
    go as a small branch and PR, like this one, not straight to `main`.
- **T015 (closes #90):** docs and close-out (tasks.md T015).
  - **Docs:** `application-platform.md` "Observability (spec 023)" and
    the package readiness reasons (`Removing`); `provider-model.md`
    (`Disk`, `PackageUsage`); `authorization.md` (stored logs and
    `serendip/observability/read`).
  - **Risk register:** RP-066 (alert-rule CRD left), RP-067 (secrets in
    stored logs), RP-068 (logs volume filling before retention). Every
    RP the plan names is indexed.
  - **docs-keeper:** four drift items fixed, one of them in code: the
    catalogue's description no longer promises traces and alerts.
- **T014 (`3f81280` and the commit after it, closes #89):** reviews done
  (tasks.md T014).
  - **RP-064 fixed:** Fluent Bit is off the host network, on Serendip's
    copy of the chart's config (`fluentbit.go`, `fluent-bit/`). The node
    rule is gone. Checked live on `serendip-t018`.
  - **RP-065:** the copy must be re-rendered on every logs-chart upgrade.
    The API-server lookup wasn't re-measured; re-run T012's benchmark in
    the post-v1 test pass.
  - **go-reviewer:** fixed: drift restarts Fluent Bit, and a moved chart
    line fails `Install`.
  - **contract-guardian:** pass. **security-review:** no findings at
    confidence 8 or above.
  - **`serendip-t018` now:** the package is installed (ProviderInstance
    `observability`), and `serendipd` runs from this session's
    scratchpad build on :8088. The T013 binary `serendipd-t013c.exe` was
    stopped to free the port.
- **T013 (`d4c7a9e`, closes #88):** `specs/023-observability/verification.md`
  has evidence for criteria 1, 3–11 and 13 on `serendip-t018`, through
  the console (headless Chrome), CLI and MCP (CLI signed in as `admin`).
  - **Fixed:**
    - F1: log times UTC beside local-time charts;
    - F2: a finished build whose pods were gone gave no lines, not its
      stored ones;
    - F3: Argo executor lines in stored build logs;
    - F4: policy Approvals' `decision.at` was year 1 (spec 019's bug);
    - F5: new readiness reason `Removing` during the uninstall.
  - **RP-064 (fixed in T014; T013's plan for it):** Fluent Bit's `:2020`
    answered counters to any pod. **The user chose option 1:** run the
    logs module's Fluent Bit without `hostNetwork`, so the plane's
    NetworkPolicies cover `:2020`.
    - **Check first:** that the chart exposes the setting (oss-researcher),
      and that nothing needs the host network (the kubernetes filter's
      `Use_Kubelet`, DNS policy).
    - **Then:** set it in `values.go` with a test. Drop
      `serendip-allow-node-to-openobserve` if it's no longer needed
      (`policies.go`).
    - **Verify live on `serendip-t018`:** logs still flow, the
      DaemonSet's probes pass, and `:2020` times out from a workload pod.
    - **Then** close RP-064 and update `verification.md` criterion 9.
    - **Rejected:** binding to 127.0.0.1 and dropping the HTTP probes
      (loses hang detection); exec probes (the image has no shell); a
      node firewall.
  - **Tooling, kept outside the repo** in `C:/Users/malit/.serendip-t018/`:
    `cdp.mjs` (headless Chrome driver, signs in on ThunderID), `t023-*.sh`
    (start script, API helper), `t023api.py`, `t023-mcp.py`, the keys
    file (`t023-keys.env`), the store (`t023.db`) and `t023-evidence/`.
  - **The ThunderID admin password showed once in a screenshot** (the
    first script clicked "show password"). The image was deleted.
    Rotating it is offered to the user, not done.
  - **Commits not pushed;** #87 and #88 close when spec 023's PR merges.
- **T012 (`3024f1c`, closes #87):** `providers/observability/openchoreo/requirements.go`
  from `profiles/edge-1/bench/results/2026-11-14-spec023-simulated-t470/`
  (README there): idle 608 MiB / 21 mcores, peak 967 MiB / 372 mcores,
  disk 496 MiB a day, 3 days kept, budget 8192 MiB (`capacityMiB`). The
  catalogue points at it, so the package is installable;
  `GET /v1/packages` checked live. Resync +0.87 mcores. No regression
  without the package (vs spec 022).
  - **Found and fixed:** a fresh install never finished, because
    OpenObserve's SSRF guard refused the logs chart's in-cluster webhook
    (`serendip-t018` hid it). Now `ZO_SSRF_ALLOW_LOOPBACK=true`
    (**RP-062**). **Spec 024 must validate alert destinations itself**
    (put it in its tasks when it's written; contract-guardian asked).
  - **RP-063:** the daily disk growth is a 2-hour slope, extrapolated.
  - **Harness:** observability load and disk phases, new
    `--package-load-*`/`--package-disk-*` flags, `--package` refuses an
    unknown provider, an interrupted phase marks the summary incomplete.
    go-reviewer's findings fixed, including a test that the load is
    stopped however a run ends (`TestRunLoadAlwaysStopsTheLoad`).
- **T011 (the console):** `ui/src/screens/Observability.tsx` plus a
  shared `ui/src/components/MetricChart.tsx`.
  - **Component screen:** the Observability tab shows CPU and memory
    charts against request and limit, "not available" items, a stored-log
    search with "Show older lines", and traces and alerts as "Not
    available yet".
  - **Application screen:** a log search per Environment, with the
    `omitted` notice.
  - **Packages page:** the disk line (`requirements.disk`) and disk in
    use (`usage`).
  - **Charts** follow the dataviz skill: the `--chart-series` token
    passes the validator and `check-contrast.mjs`, and the charts were
    rendered in headless Edge in both themes at 360 and 560 px. The live
    screenshot is still T013's.
  - **`apiFetch`** takes a timeout: 25 s for these queries, which the
    server gives 20 s.
  - **Checks:** 150 UI tests pass, and the build is 103.9 KiB gz
    (tasks.md T011).
- **GitHub runs:** see "GitHub (2026-11-14)" below.
- **T010 (CLI and MCP tests):** `cmd/serendip/observability_test.go`
  (via a new `newWorldWith`): the three commands' requests and usage
  errors, real calls, and the MCP metrics tool on a 24-hour range
  giving at most 60 points a series plus a summary over all 288. An
  assistant's `summary: false` is refused (tasks.md T010).
- **T009 (edge-case tests):** cursors across ranges, an empty ready
  package, not ready mid-request, and the query caps. No bug found;
  `begin`'s release is now idempotent (tasks.md T009).
- **T008 (the API):** `api/observability_handler.go` serves
  `GET /v1/deployments/{id}/logs/search`,
  `GET /v1/applications/{id}/logs/search?environment=` and
  `GET /v1/deployments/{id}/metrics`, wired by `api.WithObservability`.
  - **Readiness is checked first** (503 with `Retry-After`, saying not
    installed, installing or not ready). Then come 3 slots per caller
    and 20 in all, and a 20 s deadline per query.
  - **An Application's search** reads only the Deployments whose logs
    the caller may read; `omitted` counts the rest.
  - **Metrics** come with per-series summaries, and `summary=true` keeps
    60 points. The MCP tool always sends it (`catalog.Operation.MCPQuery`).
  - **Expired build logs** come from the store (`"stored": true`).
  - **`GET /v1/packages`** shows `usage`.
  - **CLI commands:** `serendip logs search`,
    `serendip logs search-application` and `serendip metrics`.
  - **Tests:** `api/spec023_acceptance_test.go` (criteria 3, 5–8 and 11)
    and `observability_internal_test.go`. go-reviewer's findings are
    fixed (tasks.md T008).
- **T007 (wiring in `serendipd`):** the `openchoreo-observability-plane`
  catalogue entry has its installer and decision 13's removal wording.
  `Requirements` stays nil until T012, so the package is offered but
  can't be installed. A generic `packageHandle[T]` now backs both
  `buildHandle` and `observabilityHandle`. `lazyObservability` is in
  `lazy.go`, and `kubeWiring.observability` is the handle T008's API
  reads. Disk use: the catalogue's `Installer` is `*obsplane.Installer`,
  which implements `reconcile.PackageUsage`, so T008 reads it by type
  assertion. Tested through the real engine
  (`TestObservabilityInstallReachesHandle`).
- **T006 (the client and the provider):** `providers/observability/openchoreo/`
  `observer.go` (the adapters through the Service proxy), `scope.go`,
  `provider.go` (`SearchLogs`, `BuildLogs`, `Metrics`) and `usage.go`,
  tested against an `httptest` API server, then **checked live on
  `serendip-t018`** with the opt-in `live_test.go` (`OBS_LIVE_KUBECONFIG`,
  `OBS_LIVE_DB`, `OBS_LIVE_BUILD`). That run installed, removed and
  re-installed the package through the real installer, and confirmed
  scopes, answer shapes, paging, units, build-run scope and disk use.
  - **Scopes** are UIDs read from the live OpenChoreo objects (names
    repeated from the application and build providers), cached for a
    minute. A target must hang together, and a gone one is skipped,
    never widened. Returned lines are checked against all three UIDs.
  - **Paging** is newest first by time and a seen count, up to 4 rounds a
    page. A target that answered in full isn't trusted below its oldest
    line. A page can be short or empty with `Next` set (noted on
    `reconcile.LogPage`), so the API and UI must read on until `Next` is
    empty.
  - **Metrics:** millicores and MiB, whole-minute steps, at most 300
    points; request metrics always "not available".
  - **Disk use:** OpenObserve's streams API (sizes in MiB, matched to
    the byte) over a **port-forward** with the admin Secret, because the
    API server's Service proxy strips `Authorization`; Prometheus'
    `/metrics` through the proxy. OpenObserve's stats lag and leave out
    its WAL and metadata (RP-061).
  - **The live check found and fixed:** OpenObserve downloading GeoIP
    databases and LLM pricing even with telemetry off (now
    `ZO_MMDB_DISABLE_DOWNLOAD`, and `ZO_MODEL_PRICING_ENABLED` through
    `extraEnv`); a bare 500 from an empty store (clearer error); an
    alarming message before Prometheus' first scrape; the end bound is
    exclusive (already covered). Fluent Bit's position file stays on
    the node after removal (RP-060).
  - **Outside the package:** `kubernetes.ObjectMeta.UID` and
    `kubernetes.ErrBodyTooLarge`.
  - **Records:** plan.md "Findings from T006", VERSIONS.md (query shapes,
    disk-use metrics), **RP-058** (tied timestamps) and **RP-059** (a
    re-created object hides earlier logs), **RP-060** and **RP-061**.
  - **Reviews:** `go-reviewer`'s findings are fixed, except sequential
    scope resolution (cached; left as is). `contract-guardian` passed.
- **T005 (the installer, `eacba73`):** `installer.go`, `values.go`,
  `policies.go`, embedded `prometheus-values.yaml`, plus
  `kubernetes.Client.PostRaw` and `ServiceProxyPath`.
  - It installs in ADR 0021's order. An unchanged resync costs 6 GETs and
    one metrics query, with no Helm run.
  - Repairs run Helm only for the release at fault. Uninstall is
    idempotent.
  - **RP-057:** ThunderID's proxy calls drop a bearer token (latent).
- **T004 (`1ebe9d8`):**
  - `provider.Requirements.Disk`;
  - `core/reconcile/observability.go`: the types, the caps as
    constants, and `LogCursor`, which is versioned base64url, refuses any
    other spelling, and encodes an empty cursor as the first page;
  - `PackageUsage` and `DiskUsage`;
  - `policy.ActionObservabilityRead`.
  - **A test flake, not from T004:**
    `TestEngine_RequeueAfter_BackoffCapsAtResync` (timing) fails about 1
    run in 5 on unchanged code.
- **ADR 0021** (`docs/decisions/0021-observability-backend.md`) chose
  **OpenObserve for logs** and **plain Prometheus for metrics**, both
  read through OpenChoreo's adapters. It was measured on the simulated
  T470 (`bench-t470`) against OpenSearch and SigNoz. On top of the
  platform, at idle and under load:

  | Config | Idle | Under load |
  |---|---|---|
  | B: OpenObserve, metrics module as shipped | +832 MiB | +1,028 MiB |
  | A: OpenSearch | +1,609 MiB | +1,659 MiB |
  | C: SigNoz | +1,801 MiB | +2,068 MiB |

  With plain Prometheus, B should be about 350–400 MiB at idle; T013
  confirms it.
  - **Results:**
    `profiles/edge-1/bench/results/2026-11-12-spec023-backends/`.
  - **Plan amended:** decisions 1–3 and 13, and a new section,
    "Findings from T003":
    - the node rule goes in before the logs module's install;
    - passwords need a symbol;
    - removing the metrics module's CRDs leaves kube-apiserver about
      390 MiB higher.
  - **The metrics module is installed for its adapter only**
    (`kube-prometheus-stack.enabled=false`). Prometheus comes from
    `prometheus-community/prometheus` 29.47.2, with
    `profiles/edge-1/bench/observability-load/prometheus-plain-values.yaml`
    (RP-056: it must keep matching the adapter).
  - **New accepted risks:** RP-055 (OpenObserve's search slows as data
    grows) and RP-056.
  - **Docs updated:** `capability-model.md`, `overview.md`,
    `build-vs-adopt.md` and `VERSIONS.md`.
- **Telemetry off:**
  - OpenObserve sends a heartbeat home by default; the installer sets
    `ZO_TELEMETRY=false` (T005).
  - SigNoz, not adopted, reports usage by default as well.
- **Bench additions:**
  - `observability-load/backends.sh`, `measure.sh`,
    `signoz-query.env` and `prometheus-plain-values.yaml`.
  - `load.sh` now passes the driver's env inside `--overrides`.
  - `registry-cache.sh`: local pull-through image caches, wired into
    new kind clusters by `kind-t470-setup.sh`. **Verified** by T012's
    two fresh-cluster runs. **Stopped after T013** (`down`, volumes
    kept; `down --purge` frees the disk).
- **Clusters:**
  - **`bench-t470`** (kind, simulated T470) is **stopped** (its
    container), with T012's stack and the observability package. Delete
    it with `kind-t470-setup.sh down bench-t470`; nothing needs it now.
  - **`serendip-t018` is running,** with the builds package and **without
    the observability package** (T013 removed it last). `serendipd` from
    T013's last build runs on `127.0.0.1:8088` against
    `C:/Users/malit/.serendip-t018/t023.db`
    (`t023-start.sh <binary>`). It was started by this session, so it
    stops when the session's shell does.
  - **Spec 022 T014's store** (the Serendip IDs for `serendip-t018`'s
    T014 app) is copied to `C:\Users\malit\.serendip-t018\t014.db`. Pass
    a copy of it as `OBS_LIVE_DB`. The repo's `serendip.db` holds no
    Deployments.
  - **The console isn't served by Docker:** after `docker start`, the UI
    needs `serendipd` running on the host, and login needs ThunderID via
    `identity.localhost:30080`. The user will look at it later.
- **Next:** T014 (reviews), then T015.
- **GitHub (2026-11-14):** paced background scripts in the previous
  session's scratchpad `prs/` folder, plus `epics/` in session
  `d193d427-...`'s scratchpad. Check their `*.log` files before any
  other GitHub write. Never two such runs at once.
  - **Done:** `run.sh` (PRs #26–#45); `track.sh` (epics #46–#74, #21–#23
    closed); `setup.sh` (setup PR #77, merged by hand on the website
    since serendipinfra's token lacks the `workflow` scope; labels; the
    board's fields; spec 023's tasks #82–#90 under epic #64, T007–T011
    closed); `epics/epics.sh` (all 22 epics for specs 002–023 rewritten
    in epic #1's layout, each in its phase milestone; #64 open in "v1
    GA"). Spec 023's task issues: T012 #87 (closed by `3024f1c` once
    it reaches GitHub), T013 #88, T014 #89, T015 #90.
  - **Running: `backfill.sh`**, started 2026-11-14 09:17 UTC: about 770
    writes (35 Epic types, then 245 older tasks × create, link and
    close), one every 90 s, with a 6-hour rest after every 200, ending
    around early Monday. At 14:21 UTC it reached 200 writes and began
    its first 6-hour rest, as designed. It stops at the first failed write and resumes from
    `backfill.state` if re-run (`bash <prs>/backfill.sh`), after
    checking the log's last line. A keep-awake process runs beside it.
    The user set the lid action to "do nothing" and idle sleep to never
    for this run. When it finishes, remind them to restore sleep:
    `powercfg /change standby-timeout-ac 15` and
    `standby-timeout-dc 3` (prefix `MSYS_NO_PATHCONV=1` in Git Bash).
- **Maybe, to do (not scheduled):**
  - **Hardware tiers.** Propose how the user's low, medium, high and
    custom spec installs fit with the topology profiles, as a spec or
    ADR (`deployment-profiles.md`, open questions).
  - **The full test pass after v1:** every feature, on real hardware,
    with upgrades and long runs.
  - **After v1 GA (the user's choice, 2026-11-14):** releases (git
    tags, semver, a changelog) and branch protection on `main`
    (required CI checks, no direct pushes). Not before.
- **Plan leanings carried forward:**
  - `provider.Requirements` gains an optional disk part;
  - a core `ObservabilityProvider` with a lazy handle like builds';
  - queries are narrowed to the caller's permitted Deployments, and
    always carry all three UIDs;
  - no side door: NetworkPolicies, with Serendip using the API server's
    Service proxy (RP-054).

**Spec 022 (builds, the workflow-plane package): COMPLETE** (T001–T016,
2026-10-24 → 2026-11-09).
- **Records:**
  - `specs/022-builds/`: spec, plan (decisions, T001 spike findings, two
    T013 amendments), tasks (a "Done as" note per task), and
    `verification.md` for `serendip-t018`;
  - **ADR 0020** (`docs/decisions/0020-builds-from-source.md`);
  - `docs/architecture/application-platform.md` ("Builds (spec 022)"),
    `resource-model.md` (Build, Component `source`), `provider-model.md`
    ("Measured requirements, required to install"), `authorization.md`
    (Builds; the credential check);
  - the risk register (RP-033 closed, RP-034's cost part closed, RP-037
    mitigated, RP-038–RP-052 new; the index gained RP-030–RP-037's rows),
    `build-vs-adopt.md`, `docs/cli-and-mcp.md` ("Packages and builds");
  - footprint: `profiles/edge-1/bench/results/2026-11-06-spec022-simulated-t470/`.
- **What it built:**
  - **The optional `build` package** (`openchoreo-workflow-plane`,
    `providers/build/openchoreo`): the workflow-plane chart 1.3.1 with
    minimal Argo CRDs, `ClusterWorkflowPlane/default`, a `registry:2`
    (`hostNetwork`, pulled as `localhost:5000`), and Serendip's own
    `serendip-dockerfile` workflow (podman). A fingerprint in
    `status.applied` makes an unchanged resync ~5 GETs.
  - **Measured cost, required to install** (`core/provider.Requirements`):
    idle 453 MiB / 19 mcores, peak 1129 MiB / 1787 mcores at the node
    level. An unmeasured package can't be installed.
  - **Installs and removals run as background package jobs**
    (`core/reconcile/packagejob.go`, RP-045).
  - **Store uniqueness** (`unique_key`, migration 0009), closing RP-033.
  - **The `Build` kind,** owned by its Component; a Component has one of
    `image` or `source`, runs its newest successful build, builds first
    on its own, and can auto-build by polling the head over smart HTTP
    (`providers/internal/gitremote`) every 5 minutes.
  - **API, CLI, MCP and console:** `/v1/components/{id}/builds`,
    `/v1/builds/{id}[/cancel|/events|/logs]`, `builds_*` tools, a Builds
    tab, the source form, and cost on the Packages page.
  - **Removal** through an Approval leaves the cluster clean except the
    empty namespaces and the Argo CRDs (RP-039), checked live.
- **Deferred to a builds-hardening follow-up:** digest deploys and
  registry auth (RP-041), a build NetworkPolicy (RP-049), step limits
  (RP-052), and `hostUsers: false` / no service-account token (RP-038).
  Also not done (plan.md): buildpacks, webhooks, external registries,
  registry GC, multi-node pulls, pruning old Builds.
- **Next spec candidates** (the user decides; propose, don't start):
  - **Spec 023, the observability package** (the other optional package;
    it must declare measured `Requirements` before it can be installed);
  - **builds hardening** (the list above);
  - the older candidates in "Next up" below.
- **Left running / on disk:**
  - **`serendip-t018`** is up. After T014's re-run it holds no build
    workloads, only the empty build namespaces and the Argo CRDs.
  - **A scratch `serendipd` from T014's re-run was still on `:8088`**
    at T016 (PID 23744; stopping it needed the user's permission). Stop
    it with `taskkill /PID 23744 /F` if it's still there.
  - **OpenBao on `serendip-t018` was re-initialised;** its credentials
    are in `C:\Users\malit\.serendip-t018\openbao-init.json` (pass
    `SERENDIP_OPENBAO_CREDENTIALS_PATH`).
  - **GitHub repositories for T014:**
    `serendipinfra/serendip-t014-public` and `-private` (the token is in
    `C:\Users\malit\.serendip-t018\t014-private-token.txt`).
  - **`bench-t470` is deleted.**
  - **Network:** port 22 to GitHub is blocked here; pushes go over SSH on
    443 (the GitHub pacing memory).
- **Committed** on local `main`, **not pushed:** every spec 022 commit,
  from `docs(spec): spec 022 plan and T001 spike` to the T016 close-out.
  By the pacing rule, spec 022 goes up as a branch (`spec-022`) when its
  turn comes.
- **GitHub (2026-11-09):** branches `spec-010` … `spec-021` are pushed to
  the fork. PR #25 (spec 002) is open on `Serendip-Infra/serendip-infra`.
  The PRs are cross-fork, so they can't stack: open them one or two at a
  time in order, with `--no-maintainer-edit`, and merge with "Create a
  merge commit". The spec 003 PR body is written; the user opens the PRs.
- **Key decisions** (plan.md, ADR 0020): our own workflow, not the
  samples; `registry:2` through `localhost`; no ESO, a per-run Secret
  copy; no install without measured `Requirements`; a `Build` kind;
  polling, not webhooks; a resync fingerprint; a `unique_key` column.
  **One change from the spec:** turning auto-build on or off is
  `components/write`. **One decision in T015:** a source Component keeps
  an earlier source's build running until its current source builds
  (RP-048).

Previous phase:

**Spec 020 (OpenChoreo's portal features in Serendip's console):
COMPLETE** (T001–T017, 2026-10-23).
- **Commits:** `fd01e8c` … `7ea6a2d`, on local `main`, **not pushed**. By
  the GitHub pacing rule they go up as a branch, a spec or two at a time.
- **Records:**
  - `specs/020-openchoreo-console-features/`: spec, plan, tasks (with
    "Done as" notes), and `verification.md` for the real cluster;
  - **ADR 0019:** the environment model, and direct Kubernetes access to
    OpenChoreo;
  - `docs/architecture/application-platform.md` ("Environments and
    Deployments (spec 020)"), `resource-model.md`, `authorization.md` and
    `ai-operations.md`;
  - the risk register (RP-028 to RP-037), `build-vs-adopt.md`, and
    `docs/cli-and-mcp.md`.

**What exists now:**
- **Environments, Azure-style:** a Project is the subscription and an
  Environment the resource group.
  - Applications sit under their Project; migration 0008 moved existing
    ones up.
  - A **Deployment** runs one Component in one Environment.
  - **Promotion** follows the Project's pipeline, which Serendip
    enforces. Each Environment keeps its own overrides.
  - **Rollback** is a `PUT` naming a release from `history`.
  - **Undeploy** goes through an Approval.
- **The platform side:** Serendip writes every release and binding (the
  platform's auto-deploy is off), straight through the Kubernetes API.
- **Runtime view:** objects, events, and logs (recent lines, or followed
  as NDJSON), all capped. Logs need their own permission (Log Reader).
- **Configuration:** checked against the platform's schemas, failing
  closed; the console builds its form from them.
- **Topology:** declared dependencies within an Application. A
  Deployment waiting for a Component it calls says which one.
- **Platform view and packages:** read-only platform; packages are
  platform-wide `ProviderInstance`s, decided at `/`.
- **Every surface:**
  - **the console:** Deploy, Runtime, Builds and Observability tabs,
    Configure, Topology, Platform, Packages;
  - **the CLI,** with `--follow`;
  - **MCP tools,** where an assistant's promotion is immediate and its
    undeploy waits for a person.
- **Proven:**
  - **In process:** T013's acceptance tests.
  - **On `serendip-t018`:** T015, including Claude Code through
    `serendip mcp`.
  - **Footprint:** T014, with no regression against a same-day baseline;
    `serendipd` idles at 35.6 MiB.
  - **Reviews:** T016. Four passed with no blockers, and their findings
    were fixed or recorded.

**Open from spec 020** (risk register):
- **RP-034:** the console can't state a package's measured cost until
  the catalogue carries it. That's needed before 022's installer ships.
- **RP-035:** the first deploy into a new Environment took 70 s on the
  simulated T470, but 18–24 s on `serendip-t018`.
- **RP-036:** a brief not-ready flicker after a restart.
- **RP-037:** no provider declares its RAM and CPU in code.
- **Also:** RP-028 to RP-033, all accepted.
- **Deferred:**
  - a rollout restart (shown "not available yet");
  - editing dependencies in the console (the API and CLI do it);
  - remote data planes.

**Next:**
- **Spec 022: builds.** The workflow-plane package, with its first real
  installer. Its catalogue entry must carry measured cost (RP-034, and
  the registry's requirements field, RP-037). Installs must be cheap
  no-ops on resync (T016 review note).
- **Then spec 023: observability.**
- **Also queued:**
  - the self-service autonomous agents spec;
  - the `occ` admin escape hatch;
  - a real browser sign-in for the CLI, still to be shown (from spec
    021).

**Leftovers on this machine:**
- **`serendip-t018` is running.** It carries T015's `t020-staging`,
  `t020-production`, `t020-web` and `t020-api`.
- **`serendipd` (spec 020) is stopped.** Claude Code reaped its
  background shell under memory pressure after T015.
  - **Its scratch:** the T015 store copy, `run.ps1` (API keys `verifier`,
    Owner, and `t015-reader`, Reader at `t019-agents`), the CLI config,
    the console driver and the screenshots are in
    `%TEMP%/claude/D--serendip-infra/5c3ed502-4ff6-4bd9-94ad-ac55cfc89763/scratchpad/t015/`.
  - **When running it again:** start the keep-awake beside long
    background jobs.

Previous phase:

**Spec 021 (Serendip's MCP server and CLI): COMPLETE** (T001–T013,
2026-10-22).
- **Records:** spec, plan (with "Spike findings" and "As built" notes) and
  tasks are in `specs/021-mcp-and-cli/`, and the live evidence is in
  `verification.md`.
- **ADRs:** 0017 (the official MCP Go SDK, confined to `mcp/`) and 0018 (a
  person's AI acts through token exchange; `/mcp` inside serendipd).
- **Reference:** `docs/cli-and-mcp.md`, with its tables generated from
  `api/catalog` and checked by a test.

**What exists now:**
- **`api/catalog`:** one list of every API action. A test fails if a
  route has neither a CLI command nor an MCP tool, or a reason it's left
  off.
- **`cmd/serendip`, the CLI:**
  - **Sign-in:** PKCE on loopback 8250 or paste-back. The identity
    provider is found through serendipd's RFC 9728 document, and the first
    sign-in to a server is confirmed.
  - **Credentials:** an owner-only folder (a protected ACL on Windows), a
    nonce refresh lock, logout that revokes, and a sign-in only ever sent
    to its own server.
  - **Commands:** every catalogue command, with exit codes, a dry run,
    hidden Secret values, and `--reason` to file a delete's approval
    request.
- **`serendip mcp`:** stdio MCP for a person's AI assistant, acting as the
  person through the `serendip-mcp` token exchange (5-minute tokens).
  - **Policy:** the person's roles, plus agent guardrails
    (`Principal.guarded()`, `Via`).
  - **Audit:** `agent:serendip-mcp/<sub>`, with the person as sponsor.
  - **Approvals:** the person may approve what their assistant asked for;
    the assistant never decides.
- **`/mcp` in serendipd:** agents and delegated assistants only, dispatched
  in process to `/v1`, audited as `mcp`. `X-Serendip-Client` is audited as
  `cli (declared)` or `mcp (declared)`.
- **ThunderID's `serendip-mcp` client:** from bootstrap file
  `71-serendip-mcp-client.yaml`, or the operator tool
  `cmd/serendip-identity-redirects`. It exists on `serendip-t018`.
  serendipd gained no write path.

**Proven live on `serendip-t018`** (`verification.md`):
- **Claude Code 2.1.304 through `serendip mcp`** listed and read; its
  delete answered "needs approval"; it filed the request. The person
  approved it from the CLI, and the approval reconciler executed it.
- **`/mcp`** refused a person's own token and API keys.
- **Footprint:** serendipd +0.7 MiB idle (rule 4 PASS); `serendip mcp`
  17.8 MiB idle.

**Open from spec 021** (risk register):
- **RP-025, medium:** an exchanged token can be re-exchanged after
  sign-out. The fix is ThunderID's `token_family: inherit`, which its
  chart doesn't expose.
- **RP-026, low:** other ThunderID clients' tokens may be exchangeable;
  confirm DCR needs auth.
- **RP-027, mitigated:** tokens aren't bound to the server's audience.
  Confirm-on-first-use is in place; RFC 8707 is the full fix.
- **RP-022:** the fresh-install bootstrap file is unverified.
- **Accepted:** RP-019, RP-020, RP-021, RP-023 and RP-024.
- **A person signing in through a real browser** (not the paste-back
  stand-in) is still to be shown. The user can run `serendip login`
  themselves.

**Next (as of 021):**
- **Spec 020** (now in progress, above).
- **Then** 022 (builds) and 023 (observability).
- **Also queued:** the self-service autonomous agents spec, and the `occ`
  admin escape hatch.

**Leftovers on this machine:**
- `serendip-t018` is still running.
- Spec 021's scratch is under this session's scratchpad: `t011-021` holds
  the store copy and the CLI config.

Previous phase, kept for context:

**Spec 019 (Approvals): COMPLETE** (T001–T014 plus T007a, 2026-10-20).
The spec, plan (with an "As built" section) and tasks are in
`specs/019-approvals/`, and the live record is `verification.md`. ADR 0016
records the decision; `docs/architecture/approvals.md` is the model.
Accepted trade-offs live in the new `docs/risk-register.md` (RP-001 to
RP-017), which CLAUDE.md now asks every spec to keep current.

What it built:
- **Every delete records an Approval** in the delete's own transaction.
  - The authorizer attaches an approval intent to every DELETE route,
    the response names it (`Serendip-Approval-Id`), and its audit rows
    say `approved: <approval> by policy (…)`.
  - **The store fails closed** (`store.ErrNoApproval`, no bypass), so a
    delete path that forgets its Approval can't delete.
  - **Policy approves instantly** when roles allow. Only `NeedsApproval`
    (agents' deletes) waits for a person.
- **`/v1/approvals`:** request, list (indexed, paged with
  `limit`/`after`/`Serendip-Next-After`), get, approve, reject.
  - Deciders need the delete themselves. `policy.MayDecide` refuses
    agents, `system:` actors and the requester.
- **The approval reconciler:**
  - expires requests after 24 h;
  - executes them through the store's stale checks (target version and
    the exact blast radius);
  - erases deleted Secrets' values from the record, retrying until done;
  - verifies, then marks the approval `settled`.

  This replaced Secret delete's erase-first order and fixed cascades
  that left Secret values in OpenBao for good.
- **Migrations 0006 and 0007:** approvals-only partial indexes, pinned by
  query-plan tests, so no read scans every approval. Timestamps are now
  written fixed-width.
- **The console:**
  - Delete with blast radius on the Application, Component and Secret
    pages;
  - a Secrets page (metadata only, never values);
  - an Approvals page with the waiting count in the rail.
- **Measured:**
  - Approval reads with 20,000 approvals: at most 22 ms p99.
  - A DELETE costs +0.6 ms mean and +3 ms p99 of commit I/O (accepted;
    `synchronous` stays `FULL`, RP-001 and RP-002).
  - The simulated-T470 harness: the edge-1 stack idles at 1.29 GiB and
    `serendipd` at 27.4 MiB, every group within tolerance of spec 018.
- **Live on kind with the real Agent Manager agent** (T011): Secret
  erasure on delete, on cascade, and across an OpenBao outage and a
  restart; an agent's delete approved in the console; reject; stale
  target; the full audit trail.
- **Reviews (T012):** contract-guardian, go-reviewer and
  `/security-review` (nothing at confidence 8 or above) passed, with
  their findings fixed. One process slip: `a85a659` (T012 fixes, touching
  `core/`) was committed before its contract-guardian run, which ran
  post-hoc and passed.

Leftovers on this machine (scratch only, all reversible):
- **`serendip-t018` (kind) is running.** Its OpenBao unseal file is
  spec 018 T001's scratch `openbao-init.json`. Pass it with
  `SERENDIP_OPENBAO_CREDENTIALS_PATH` when running `serendipd` against
  this cluster.
- **Agent Manager (`amp-local`) is stopped.** `k3d` isn't installed on
  the host, and plain `docker start` gives the server a new IP, which
  makes K3s crash-loop ("failed to find interface with specified node
  ip"). To restart it, first free host port 8080 (`serendipd` must use
  another port, e.g. `:8088`), then:
  `docker network disconnect k3d-amp-local k3d-amp-local-server-0`
  (and the same for `-serverlb`), then
  `docker network connect --ip 172.27.0.3 k3d-amp-local k3d-amp-local-server-0`
  and `docker network connect --ip 172.27.0.2 k3d-amp-local k3d-amp-local-serverlb`,
  then start the server first and the load balancer about 20 s later.
- **The `serendip-ext` agent's client secret was regenerated** in T011.
  The old one no longer works, and the new one is only in session
  scratch.
- **The user's own `go run ./cmd/serendipd` on :8080 was stopped** in
  T011 to free the port. `serendip.db*` in the repo root is now
  git-ignored.

**Next: spec 021, Serendip's own MCP server and CLI** (the user,
2026-10-20). It comes before spec 020. Constitution rule 7 already
requires both, as clients of the same API, RBAC and approvals, but
neither exists yet in `cmd/`. MCP is the AI side of "an AI and human
platform": agents use their spec 018 identities, and destructive tools
become Approval requests (spec 019).

**Then spec 020, OpenChoreo's portal features in our console** (no
Backstage). The spec and plan are drafted in
`specs/020-openchoreo-console-features/`; the plan awaits approval and is
on hold until 021. It covers:
- the Azure-style environment model (a Project as the subscription, an
  Environment as the resource group), with per-environment overrides;
- direct Kubernetes access to OpenChoreo, not its API;
- a runtime view, promotion, schema forms, topology and a platform view;
- an optional-package framework.

Builds are spec 022 and observability (OpenSearch and Prometheus for
now) is spec 023.
- **RP-018 is fixed** (`96f285d`): OpenChoreo's API was open to every pod.
- **Remember, later:** an admin-only escape hatch so OpenChoreo's `occ`
  works (see memory and plan.md "What we give up").

**On hold** (the user, 2026-10-20), none started:
- **Small follow-ups from the register:**
  - RP-014: a new Secret's value write can answer 409 after writing;
  - RP-015: show an agent's sponsor by name;
  - RP-017: an opaque paging cursor.
- **A retention spec for approvals** (RP-003). The audit log already
  keeps every snapshot.
- **Approval policies for people,** e.g. "deletes in production need a
  second person" or "not the sponsor" (RP-007). The engine's
  `NeedsApproval` is the hook.
- **Other destructive verbs** (scale-to-zero, drain, volume operations)
  once the API has them. `ApprovalVerb` is designed for them.
- **A real T470 run** (RP-008), before any profile default changes.
- **Pushing to GitHub, slowly** (the account was blocked once for bulk
  issue creation): one branch per spec. Pushed on 2026-10-20: `spec-002`
  (up to `1533b8a`) and `spec-003` (up to `f13ac45`; it contains 002's
  commits, since the history is linear). No PRs yet. Specs 004–019 are
  local only. Push a spec or two at a time, only when the user asks.

**Spec 018 (agent identities, a WSO2 Agent Manager POC, and the
identity-provider management channel): COMPLETE** (T001–T014,
2026-10-19). The spec, plan and tasks are in `specs/018-agent-manager-poc/`.
Evidence: `verification.md` (live run), `findings.md` (POC findings and
recommendation), and the benches in
`profiles/edge-1/bench/results/2026-10-18-spec018-agents/` and
`…/2026-10-19-spec018-reuse/`. ADR 0015 records the channel. The docs are
`identity.md` (live directory, agent identities), `authorization.md`
(agent guardrails) and `gateway.md` (exact-path identity route).

What it built:
- **The public identity route is an exact-path allowlist.** T001 found
  the `/oauth2` prefix exposed introspect, DCR and PAR.
- **The management channel** (ADR 0015) is a stdlib in-process
  port-forward, using the kubelet WebSocket protocol `v4.channel.k8s.io`
  (`providers/kubernetes`), plus `serendipd`'s own admin login. It's
  read-only so far.
  - It feeds `core/identity.Directory`.
  - The API: `GET /v1/authorization/directory`, and
    `GET /v1/authorization/principals/person/{id}/groups`, which answers
    `source: live` or `signedIn` and falls back after 10 s.
  - The console's pickers and Check access use it live.
- **Agent issuers:**
  - configured in `SERENDIP_AGENT_ISSUERS`
    (`alias=issuer|audience[|maxLifetime]`, fatal if malformed);
  - `oidc.IssuerRouter` routes by exact `iss`;
  - agent tokens must carry `grant_type: client_credentials` and `iat`,
    live at most 1 h by default, and have their groups ignored;
  - the principal is `agent:<alias>/<sub>`, and `actor_sponsor` is in
    audit.
- **Agent guardrails:**
  - no access-management roles or actions, ever;
  - an allowed delete gets 403 `urn:serendip:problem:needs-approval`;
  - one sponsor per agent;
  - `assignableToAgents` on role views.
- **The POC:** Agent Manager 1.0.0 runs beside Serendip; its environment
  ThunderID is the agent issuer. It works end to end with real tokens.
  - The reuse variant fails PR #679's own preflight 7 times on edge-1, so
    it's recorded, not built.
  - Edge-1 uses 1.32 GiB, against Agent Manager's 5.13 GiB.
  - The recommendation is to keep it beside Serendip, not in edge-1, for
    now.
- **Cost:** an agent request costs about the same as a person's (+56 µs
  p99 on the simulated T470). The directory takes about 7 ms warm.
  `serendipd` is +0.2 MiB idle and 31.5 MiB on the T470 (rule 4: PASS).
- **Reviews (T013):** contract-guardian, go-reviewer and
  /security-review (nothing at confidence 8 or above) passed. One process
  slip: `ebef37e` was committed before its contract-guardian run, which
  ran post-hoc and passed.

Leftovers on this machine (scratch only, all reversible):
- **Stopped containers:** `amp-local` (Agent Manager's k3d) and
  `serendip-t018` (kind). `docker start` resumes them.
- **The hosts-file line** `127.0.0.1 default-idp.amp.localhost` was added
  by the user and can be removed.
- **Test resources in ThunderID on `serendip-t018`:** the agents
  `serendip-ext` and `serendip-norole`, and `t011-*` items. They're
  harmless.

**Spec 017 (Authorization, Azure-RBAC style): COMPLETE** (T001–T012,
2026-10-18). Model and enforcement: `docs/architecture/authorization.md`,
ADR 0014. Evidence: `specs/017-authorization/verification.md`. Upgrade
note from T011: Secret values written before the per-Secret value
storage fix must be written again.

Other candidates are in "Next up" below.

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

`go.mod`'s `go` directive is `1.26.0` (since Dependabot #80/#81,
2026-11-15; `modernc.org/sqlite` v1.56.0 first raised it to `1.25.0` in
T010). Still satisfies "Go 1.22+" as a floor (ADR 0003 addendum).

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
