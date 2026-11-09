# Spec 022 verification (T014, real cluster)

Run 2026-11-09 on `serendip-t018`: kind, `kindest/node:v1.34.0`, with the
spec 017/018 stack (OpenBao, kgateway, OpenChoreo 1.3.1, ThunderID 1.0.1).
Tokens, passwords, keys and the repository credential were never printed.

## Setup

- **`serendipd`:** built from `3d597ec` (T013), with the console rebuilt
  and embedded, on `:8088`, with a **fresh store**. API keys:
  - `verifier`: Owner at `/`;
  - `t014-reader`: Reader at `/`;
  - `t014-contrib`: Contributor at Project `t014-builds`. It was first
    Contributor at `/`; see criterion 3.
- **OpenBao was re-initialised.** It was sealed after a pod restart, and
  the unseal file from spec 018 T001 had been lost with that session's
  temporary scratch folder. With the user's agreement, the `openbao-data`
  volume was deleted (losing only test Secrets from specs 018–021). On
  start, `serendipd` recreated it, initialised OpenBao and wrote the new
  credentials to `C:\Users\malit\.serendip-t018\openbao-init.json`
  (outside temp, so it isn't cleaned up again).
- **Organisation:** `t014-org`, then `t014-team`, then Project
  `t014-builds`, with pipeline `t014-dev → t014-prod` and Application
  `t014-app`.
- **Repositories** (created for this run on GitHub, under `serendipinfra`):
  - `https://github.com/serendipinfra/serendip-t014-public`, with
    `Dockerfile` (nginx with an `index.html`), `Dockerfile.broken` (`RUN
    exit 1`) and `Dockerfile.slow` (`RUN sleep 600`);
  - `https://github.com/serendipinfra/serendip-t014-private` (private),
    with a fine-grained, read-only token for it alone.
- **The person:** `admin` in ThunderID. The console was signed in through
  ThunderID's own sign-in page, in headless Chrome driven over the
  DevTools protocol (a scratch driver using Node 22's built-in WebSocket,
  since the earlier one was lost with its scratch folder). Screenshots
  were taken and looked at; they aren't committed.

## Evidence, by acceptance criterion

1. **Cost before install:**
   - **`GET /v1/packages`** (and `serendip packages list` as the Reader):
     `openchoreo-workflow-plane` shows idle 453 MiB and 19 mcores, peak
     1129 MiB and 1787 mcores, "Measured on edge-1 simulated T470 …
     2026-11-09 (profiles/edge-1/bench/results/2026-11-06-spec022-simulated-t470/package)",
     the fit note, and `installable: true`.
     `openchoreo-observability-plane` has `requirements: null`; the CLI
     reads "not measured" for both idle and in use.
   - **The console's Packages page** shows the same: "Idle: 453 MiB of
     memory and 0.02 CPU cores. While a build runs: up to 1129 MiB of
     memory and 1.79 CPU cores", where it was measured, how it fits, and
     what removing it loses. Observability reads "Not measured. Serendip
     states no number for a package until its cost has been measured on
     the reference machine."
3. **Install:**
   - **First, a Contributor at `/`** (the key's first assignment) created
     the ProviderInstance: 201. That is the intended rule, not a bug:
     installing is decided at `/` (`api/authz.go`), and a Contributor at
     `/` may do anything but manage access, as in Azure. The criterion's
     Contributor is a Project's, as in T012's test. That install reached
     Ready in about 80 s, and was removed by the Owner (an `Executed`
     Approval).
   - **A Project's Contributor:** `serendip provider-instances create`
     answered "forbidden: missing serendip/provider-instances/write at /",
     exit 3. Audit: `t014-contrib`, `cli (declared)`, `deny: missing
     serendip/provider-instances/write at /`.
   - **The Owner installed from the console** (Packages → Install package).
     The console asked first, restating the cost ("Install
     openchoreo-workflow-plane for the whole platform? It runs, and uses
     memory and CPU, until it's removed."). The Builds tab of Component
     `t014-image` (an image Component) read, from the click:
     - 4 s: "Not installed. This needs the openchoreo-workflow-plane
       package, which isn't installed." (the next poll hadn't come yet);
     - 18 s: "Installing. The openchoreo-workflow-plane package is
       installing. This section works once it's ready.";
     - 93 s: available ("This component runs a ready-made image, so it
       has no builds. Edit it to build from source in Git instead.").
   - **Audited:** `person:admin`, `http`, `allow: Owner (…) at /`, then
     the reconciler's status writes.
5. **Cheap resync** (resync every 30 s):
   - **Idle, installed, nothing changed:** over 206 s (about 6 resyncs)
     the Helm release stayed at revision 1, no `helm` process appeared in
     36 samples, and `serendipd` used 250 ms of CPU (1.2 mcores, every
     reconciler included).
   - **Repair:** `kubectl delete deploy serendip-registry` was repaired by
     the next resync, recreated after 18 s and available after 19 s.
     `build` read Installing ("waiting for the build registry to be
     ready") for about 15 s, then Ready. Helm stayed at revision 1: only
     the missing piece was applied.
6. **Build from a public repository:**
   - **Created in the console** (Create component → "Built from source in
     Git", the public repository, port 80 exposed). The first build
     started on its own (`auto-build`, plan decision 6), and the Component
     read `WaitingForBuild`, "waiting for build … (Queued)". It succeeded
     in 52 s: commit `d6798a8b7113`, image
     `localhost:5000/serendip/cmp-2abd6ba0c4:d6798a8b7113`, steps
     `checkout` and `build-push`, with 32 events.
   - **Triggered by the person** (Builds → Start build): the list showed
     `build-20261109-092245` Queued ("Branch head, Not yet", with Cancel),
     then Running, then Succeeded in 40 s.
   - **Followed live:** `serendip builds logs <id> --follow` printed each
     line within about 1 s of its timestamp (checkout at 14:52:53,
     build-push from 14:53:09 to "pushed …" at 14:53:17), then "Stream
     ended: the build ended." It gave the same 33 lines as a plain read
     afterwards. Following is CLI-only by design (T011).
   - **Served in the first Environment:** Deploy tab → Deploy to
     `t014-dev`: Ready in 16 s, release `cmp-2abd6ba0c4-f011981342`. Its
     endpoint answered 200 with `<h1>serendip t014 public v1</h1>`, from a
     pod running the built image.
   - **Promoted** to `t014-prod` from the Deploy tab: Ready in 16 s, and
     its endpoint answered the same page.

7. **Private repository:**
   - **The credential:** the token was read from a local file into Secret
     `t014-git` (`{username, password}`) by `person:admin` (Owner and
     Secrets Officer), with `serendip secrets value set`. Only its prefix
     and length were ever printed.
   - **A Project Contributor can't name it:** creating `t014-priv` with
     `credentialSecret` answered "forbidden: missing
     serendip/secrets/value/read at Secret/…: a build reads its
     credential's value, so naming it needs reading that value", exit 3.
   - **The person created it in the console**, picking `t014-git` from the
     Credential list (its only entries were "None: a public repository"
     and `t014-git`). The page shows "Credential: Secret <id>", never the
     value. The first build succeeded on the private repository's commit
     `aa7296b2dbb7`. Deployed to `t014-dev` by the Contributor, its
     endpoint answered 200 with `<h1>serendip t014 private v1</h1>`.
   - **The credential appears nowhere:** the whole token, and a 22-
     character piece of it, were searched for (counts only) in the CLI's
     responses (Component, Components list, builds list, build, logs,
     events, Secret), `GET /v1/components/{id}/builds`, the approvals
     list, Claude Code's MCP reads (`ev/c7/mcp.txt`), `serendipd.log`, the
     store file with its WAL and shared memory, and all 157 `audit_log`
     rows: **0 matches each**.
   - **But the per-run copy outlives the run:** finding F3.
8. **Failure and cancel** (on `t014-pub`, changed by the Project
   Contributor through the CLI):
   - **Failure:** `source.dockerfile: Dockerfile.broken` started a build
     on its own, which Failed in 40 s. Its step reads `build-push: Failed,
     main: Error (exit code 1)`, and its logs end with `t014: this build
     fails on purpose` and `Error: building at STEP "RUN …": … exit status
     1`, in the console and through `serendip builds logs`. Both
     Environments kept serving v1 from release `cmp-2abd6ba0c4-f011981342`.
     **But the status around it is wrong:** finding F2.
   - **Cancel:** with `Dockerfile.slow`, the build reached `t014: slow
     build, to be cancelled`. In the console, Follow on the running build
     read "Following: new lines appear as they're written."
     (`GET /v1/builds/{id}/logs?follow=true&tailLines=100`). Cancel build
     asked "Stop this build? What's running now is unchanged." Once
     confirmed, the follow ended ("The build finished."), the build read
     **Cancelled** 7 s later, and its pod was gone.
     Small finding U2: its `build-push` step still reads Running.
   - **Back to `Dockerfile`:** the Component took its earlier successful
     build of that source at once (Ready, the same release), with no new
     build.
9. **Auto-build:**
   - **On:** turning `autoBuild` on (the Contributor, `components/write`)
     started no build, and added `AutoBuild: True, Watching, "watching
     main for new commits"`.
   - **A pushed commit was built:** v2 (`8612eada620b`) was pushed at
     15:00:08. The poller started `auto-build-8612eada620b` at 15:04:56,
     created by `system:auto-build`, which succeeded in 40 s. Development
     moved to v2 by itself; production stayed on v1 until promoted (the
     pipeline).
   - **Where a commit can't be noticed:** `t014-unseen`, on the private
     repository with no credential and auto-build on, reads `AutoBuild:
     False, NeedsCredential, "auto-build can't read the repository: it
     needs a credential, refused the one given, or doesn't exist"`. Its
     first build failed at checkout (exit code 128).
   - **Off:** auto-build was turned off at 15:06:18, and v3 (`23d9050`)
     was pushed at 15:06:21. By 15:13:07 no auto-build had started. The
     one newer build, `build-20261109-093736` at 15:07:36, was Claude
     Code's own trigger (criterion 11).
4. **Uniqueness** (after the first removal, nothing installed): two
   `POST /v1/provider-instances` sent at once gave **one 201 and one
   409**. The 409 came from the store's unique key, not the API's
   check: "store: a resource with the same unique key already exists:
   capabilityId: build already has a package (ProviderInstance …);
   remove it first". One ProviderInstance remained. That install was
   Ready in 74 s, and was then removed by the person
   (`serendip provider-instances delete`: "Done. Recorded as approval
   …", approved by policy).
10. **Removal:**
    - **The console states what's lost before the request.** Packages →
      Remove package: "Delete package openchoreo-workflow-plane? This
      removes it from Serendip. The delete is recorded as an approval,
      decided by your roles, and can't be undone." It lists the
      ProviderInstance, and adds "Build history stays, but run logs and
      the stored images built from source are deleted. Components running
      a built image keep running, but can't be rebuilt, …". This was
      cancelled; the removal below was the agent's.
    - **It needs an Approval:** the agent's request waited for the person
      (criterion 11). Approved in the console with a comment, it was
      Executed at once, then read "Done, cleaning up" while the uninstall
      ran as a package job.
    - **Afterwards:**
      - `build` read NotInstalled.
      - The Builds tab was greyed out again ("Not installed. This needs
        the openchoreo-workflow-plane package, which isn't installed.
        See packages"), with no Start build.
      - Build history still lists.
      - The app kept serving v3 from the node's cached image.
      - A build's logs answer 503: U4.
    - **The package's workloads:** after both removals, no Deployment,
      StatefulSet, DaemonSet or running pod of the package remains, the
      Helm release is gone, and so are the credential copies. The Argo
      CRDs stay, by design (VERSIONS.md, RP-039).
    - **But it doesn't leave the cluster clean: F4 (and F1).**
11. **Authorization and parity:**
    - **The route table:** every new route has a rule, which `go test
      ./api/...` (T008, T012) enforces.
    - **A Reader** (`t014-reader`, Reader at `/`) reads runs: `builds
      list`, `get` and `events` exit 0. But logs are refused:
      "forbidden: missing serendip/components/logs/read at Build/…".
      Trigger and cancel are refused as well (`missing
      serendip/builds/write`), and the refused cancel is audited as a
      `deny`.
    - **Claude Code through `serendip mcp`** (Claude Code 2.1.319, `claude
      -p --strict-mcp-config`, only `mcp__serendip__*` allowed, signed in
      as `admin`), with `packages_list`, `builds_list`, `builds_trigger`,
      `builds_get` and `builds_logs`:
      - It gave the package's measured cost and source.
      - It listed `t014-pub`'s five builds.
      - It triggered a build and read it to Succeeded (commit `23d9050`).
      - It read its logs.
      - The trigger is audited as
        `agent:serendip-mcp/01900000-0000-7000-8000-000000000030`, over
        `mcp (declared)`, with the admin as sponsor and `allow: Owner`.
    - **An agent's removal waits for a person.** Asked to remove the
      package, Claude Code filed `approvals_request`. Approval
      `01a1bfb2-efc8-76d7-b477-04c46685616e` is **Pending**
      (`AwaitingDecision`, "an agent's delete needs an approval"). Asked
      to approve it itself, it reported that no tool it has can.
      - **The console** listed it under "Waiting for a decision", as
        requested by `agent:serendip-mcp/…` with its sponsor.
      - **`admin` approved it,** with a comment, and it was Executed.
      - **Audit:** the agent's request (`needsApproval:
        serendip/provider-instances/delete at ProviderInstance/…`); the
        person's decision (`person:admin`, `allow: Owner`); then the
        reconciler's delete, "approved … by person:admin; requested by
        agent:serendip-mcp/…".
    - **Parity:** every console action used here (install, create,
      start, follow, cancel, deploy, promote, remove, approve) has its
      CLI command. All but approve and the infrastructure writes are
      MCP tools; those two are refused to agents by design (spec 021).

## The cluster at the end

- **No package workloads run,** and the Helm release is gone.
- **Left on `serendip-t018`:**
  - **F4:** Argo Workflows and their pods and volumes, in
    `workflows-serendip-builds`. These are 7 Workflows, 13 finished pods,
    and the 2 GiB workspace volumes of the two failed builds.
  - **F1:** `openchoreo-workflow-plane/cluster-agent-tls`.
  - **Namespaces:** `openchoreo-workflow-plane`, `serendip-builds` and
    `workflows-serendip-builds`.
  - **The 8 Argo CRDs,** kept by design.
- **These are kept on purpose,** as evidence for T015's fix. Its removal
  has to be re-run, and has to end with none of them.
- **Also left, test data:**
  - the `t014-*` objects in the scratch store;
  - `t014-pub` (v3 in dev, v1 in prod) and `t014-priv` (in dev),
    running from images cached on the node.

## The re-run after T015 (2026-11-09)

A `serendipd` built from `d0a4933` (T015's fixes), on `serendip-t018`,
with T014's leftovers still there:

- **15:56:44** `serendipd` started, the same store and keys.
- **15:57:12** the Owner (`verifier`) created ProviderInstance `builds`
  (`POST /v1/provider-instances`). It read Installing ("waiting for the
  build registry to be ready"), and the build provider was active at
  **15:58:45**, about 93 s later.
- **15:58:52** `serendip builds trigger` on `t014-pub`: build
  `build-20261109-102852` went Running, then **Succeeded**.
- **Before the removal:** `workflows-serendip-builds` held 8 Workflows
  (T014's 7 and this one), 15 pods and 2 PVCs, and **no credential
  copies** (F3's fix: the copy goes when the run ends).
- **15:59:44** the Owner deleted the ProviderInstance (`serendip
  provider-instances delete`): "Done. Recorded as approval
  01a1bfdb-…", which reads `Executed`.
- **16:00:24** (40 s later), polled every 5 s: 0 Workflows, 0 pods,
  0 PVCs in `workflows-serendip-builds`, no
  `openchoreo-workflow-plane/cluster-agent-tls`, and no workflow-plane
  Helm release.
- **The end check:**
  - `openchoreo-workflow-plane`, `serendip-builds` and
    `workflows-serendip-builds` hold nothing (`get all,pvc,secrets,configmaps`,
    besides `kube-root-ca.crt`); no PersistentVolume remains for them.
  - **Left by design:** those three empty namespaces, and the 8 Argo
    CRDs (RP-039).
  - No ProviderInstance remains; the build provider reads not ready.

**F4 and F1 are fixed live, and the cluster ends clean.**

## Outcome

**Criteria 1, 3–9 and 11 pass. Criterion 10 passes except "the cluster
holds none of the package's workloads":** finished build pods, their
Workflows and workspace volumes remain (F4). T014's "the cluster ends
clean" therefore isn't met yet. T015 fixes F4 and F1, then re-runs a
removal after a build.

**After T015 (the re-run above): criterion 10 passes too.** A removal
after a build, with T014's leftovers on the cluster, left no Workflows,
pods, PVCs, credential copies or `cluster-agent-tls`. All criteria
pass, and the cluster ends clean.

## Findings

- **F1 (removal leaves a private key):** removing the package leaves
  namespaces `openchoreo-workflow-plane` and `serendip-builds`, and in the
  first, Secret `cluster-agent-tls`: the workflow plane agent's TLS key,
  issued by cert-manager. cert-manager doesn't delete a Secret with its
  Certificate, and `Installer.Uninstall` doesn't delete it either. No
  workloads remain. For T015.
- **U1 (the console's build logs go stale):** the logs panel loads once.
  Opened while a build ran, it still showed only `checkout`'s line after
  the build succeeded, until Refresh showed all 33. It should reload when
  the build's phase changes. For T015.
- **F2 (a failed build after a source change claims nothing runs):** by
  design, a source Component runs the newest successful build of its
  *current* source. So once `source.dockerfile` changed and that build
  failed:
  - `status.image` was emptied;
  - the Component read "Running image: None yet" and `Reachable: False,
    "workload is not deployed"`;
  - both Deployments read `WaitingForComponent`.

  Yet development and production kept serving the earlier release, as
  they should. T012 only tested a failure on an unchanged source. The
  status should name what's still running. For T015.
- **F3 (the credential copy outlives its run):** plan decision 3 says
  the per-run copy is "deleted when the run ends or is removed". The copy
  (`workflows-serendip-builds/build-<hash>-git`) has no owner reference,
  and nothing deletes it when the build ends. `RemoveBuild` (a removed
  Build, or a cancel) and the package's removal do. It was still there
  after the private build succeeded; the removal deleted it. The plan's
  risks section says "up to an hour after it ends, with the run", but
  the run's expiry doesn't take it either. For T015, with RP-044's
  neighbour in the risk register.
- **F4 (removal leaves finished builds behind):** `Uninstall` deletes the
  WorkflowRuns, but the Argo Workflows they started (in
  `workflows-serendip-builds`) stay. Their pods and workspace volumes are
  owned by them, so they stay too. A reinstall doesn't clean them up,
  and a second removal doesn't either. Nothing will, once the plane is
  gone. For T015: delete the run namespace's Workflows (or the namespace)
  in `Uninstall`, after the runs and before the plane.
- **U2:** a cancelled build's running step still reads Running.
- **U3:** `tailLines` is applied before Argo's own `argo=true` lines are
  filtered out. With `tailLines=5`, a build-push step returned 2 lines
  (the pod's last 5 included 3 of Argo's). The filter itself works live,
  which settles T006's open recheck.
- **U4:** with the package removed, a build's logs answer 503 "the builds
  package is still being set up; retry shortly". It should say that it
  isn't installed, as `source` does with 422.
