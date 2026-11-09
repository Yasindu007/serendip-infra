# Plan 022: Builds — the workflow-plane package

Status: Approved 2026-10-24. Spec approved 2026-10-23.

## What the research found (OpenChoreo 1.3.1, 2026-10-23)

From the `oss-researcher` (v1.3.1 tag source and the v1.3.x docs).
"Inferred" items are checked by T001's spike.

- **The chart:** `oci://ghcr.io/openchoreo/helm-charts/openchoreo-workflow-plane`
  `1.3.1` installs two pods:
  - the Argo Workflows controller only (subchart, `server.enabled=false`,
    workflows run in `argo-build`);
  - `cluster-agent-workflowplane`, with a cert-manager Certificate.
  - **Documented requests** are about 64–128 MiB for Argo and 128–256 MiB
    for the agent, so 200–400 MiB idle. That's inferred from limits; T013
    measures it.
- **Registration** is like the data plane's:
  - copy `cluster-gateway-ca` into the workflow plane's namespace, the same
    gap spec 010 found;
  - apply `ClusterWorkflowPlane/default` with `planeID` and
    `clusterAgent.clientCA` (the agent's `ca.crt`, read once its
    Certificate is Ready).
- **No registry is bundled.** The docs' default pushes to `ttl.sh`, which
  is public with a 24 h expiry; the k3d path installs its own registry.
- **The sample `ClusterWorkflow`s** (`dockerfile-builder`, Paketo, GCP,
  Ballerina) are samples to apply, not installed. Two parts don't fit
  Serendip:
  - **ExternalSecrets.** They always create one for the registry push, and
    one for Git when `repository.secretRef` is set. The chart itself
    doesn't need ESO.
  - **`generate-workload`.** It writes the built image back into the
    `Workload` through `openchoreo-api`, with a Thunder client-credentials
    token and hard-coded `host.k3d.internal` URLs. Serendip runs with
    `security.enabled=false` and writes Workloads itself.
- **A build run is a `WorkflowRun`.**
  - **Its spec** is `workflow{kind,name,parameters}` and
    `ttlAfterCompletion`, plus component and project labels.
  - **Its status** has conditions (`WorkflowRunning`, `WorkflowSucceeded`,
    `WorkflowFailed`, `WorkflowCompleted`), `tasks[]` with phases and
    times, and `runReference` (the Argo Workflow).
  - **No image or commit in status:** the image is an output of the publish
    step.
  - **No cancel:** deleting the run removes its plane resources through a
    finalizer.
  - **Pods** are in `workflows-<namespace>`, labelled
    `workflows.argoproj.io/workflow=<run>` (inferred).
- **With auto-deploy off** (Serendip's setting since spec 020), a Workload
  image change creates no release. Serendip already makes releases itself,
  so nothing changes there.
- **Auto-build** is a webhook on `openchoreo-api`
  (`/api/v1alpha1/autobuild`, HMAC secret). It needs a public URL; a
  laptop behind NAT has none. Issue #4409: nothing builds on creation.
- **Builds run privileged `podman`** with `hostUsers: false` (user
  namespaces), with a 10 Gi emptyDir and a 2 Gi workspace PVC.

## Decisions

1. **Serendip ships its own build workflow; it doesn't use the
   samples.** The package applies one `ClusterWorkflow`,
   `serendip-dockerfile`, with its own templates: check out, build with
   `podman`, push to the package's registry, then output the image
   reference and commit.
   - **No `generate-workload` step.** Serendip reads the image from the
     run's outputs, and the Component's next release uses it (decision 6).
   - **No ExternalSecret resources** (decision 4).
   - **Dockerfile builds only, first** (spec open question 2). It's the
     cheapest on 2 cores and the clearest to state. Buildpacks are a later
     spec, listed in "NOT done".
   - **Pinned images,** in `VERSIONS.md`.
   - **`hostUsers: false` is kept if the spike shows user namespaces work**
     on the node image; otherwise privileged without it, recorded as a
     risk.

2. **What the package installs** (spec open question 1). It's installed
   in this order and checked:
   1. the workflow-plane chart `1.3.1`, through ADR 0010's Helm path;
   2. the CA trust copy, and `ClusterWorkflowPlane/default`, hand-built
      with `providers/kubernetes.Client` like `DataPlane`;
   3. **a registry:** `registry:2` (`distribution`, pinned), as a
      hand-built Deployment, Service and PVC in `serendip-builds`. Its
      storage comes from the Storage capability's default class
      (local-path).
   4. the `serendip-dockerfile` `ClusterWorkflow` and its templates.

   **How the node pulls built images** (inferred; the spike settles it):
   - **Default:** the registry pod uses `hostNetwork` on one node, and
     workloads pull `localhost:<port>/...`. containerd allows plain HTTP to
     `localhost`. Builds push to the registry's Service over HTTP.
   - **Fallback,** if the spike shows otherwise: a containerd mirror in
     the node's registry config. That's node configuration, so the
     serendip-agent's job; it would become an extra task.
   - **Either way it's single-node.** On several nodes, other nodes can't
     pull `localhost`. That's recorded as a risk with the mirror as its
     fix.

3. **ESO isn't installed** (spec open question 1).
   - The Secrets capability's `ReadValue` already gives `serendipd` a Git
     credential.
   - The build reconciler copies it into a plain Kubernetes Secret, in the
     build's namespace, for that run only. It's deleted when the run ends
     or is removed.
   - **That saves a fourth component to keep current,** and its CRDs and
     pods. The cost is a short-lived copy in the cluster's own store,
     which is what ESO would have made anyway. It's recorded in the risk
     register.

4. **Measured cost is part of the catalogue, and required to install**
   (RP-034, RP-037).
   - **`core/provider` gains `Requirements`:** idle memory and CPU; peak
     memory and CPU while working (for builds, while a build runs); where
     and when it was measured (the results folder); and a plain note on
     fit, written from the benchmark.
   - **Each provider package declares its own `Requirements` value,** in
     one place. The catalogue entry points at that value instead of
     copying it. `GET /v1/packages` returns it, or `null` for "not
     measured".
   - **`Catalogue.Validate` refuses a package without `Requirements`,**
     like one without an installer. So no package can be installed until
     its cost is stated: the user's rule, enforced in code, not only
     documented.
   - **Until T013 measures the numbers,** the builds package stays
     uninstallable in a real `serendipd`. Tests use their own catalogues.
   - **Existing providers** stay undeclared in this spec (spec "OUT").

5. **Builds are a resource kind, `Build`, owned by its Component**
   (spec open question 4).
   - **Spec:** `commit` (optional; empty means the branch's head) and
     `cancel` (bool).
   - **Status:** phase (`Queued`, `Running`, `Succeeded`, `Failed`,
     `Cancelled`), the commit built, the image, started and finished
     times, steps (name, phase, times), and a Ready-style condition.
   - **Why a kind, not live reads:**
     - triggering is a store create, so audited by core and reconciled
       like every other change (constitution 3, 8);
     - history outlives the plane's runs;
     - an agent's trigger takes the same policy path.
   - **Run records are kept** (they're small). The plane's `WorkflowRun`
     uses `ttlAfterCompletion: 1h`. So logs and events are readable for an
     hour after a run ends, then the Build says "logs expired". Completed
     pods hold their emptyDir until removed, which is why it's short.
   - **No `DELETE` route for a Build.** Builds go with their Component,
     whose deletion already needs an Approval.
   - **Cancel** sets `spec.cancel`. The reconciler deletes the
     `WorkflowRun`, which stops the run (checked in T001), and marks the
     Build `Cancelled`.
   - **One run at a time per Component.** A new Build waits `Queued` while
     another runs. Two builds side by side on 2 cores would only both be
     slow.

6. **How a built image reaches a release** (spec open question 7).
   - **`ComponentSpec` gains `source`:** `repository`, `branch` (default
     `main`), `path` (default `.`), `dockerfile` (default `Dockerfile`),
     `credentialSecret` (a Secret's ID) and `autoBuild`.
     - **A Component has exactly one of `image` or `source`.**
     - **Switching is allowed both ways.** Image → source waits for its
       first build; source → image is an ordinary edit.
   - **`ComponentReconciler` resolves the image.** For a source Component
     it uses the newest `Succeeded` Build's image, and hands the provider a
     spec with that `image`. So the application platform provider doesn't
     change, and the release hash covers the image, giving a new release
     per successful build.
     - **A Build's success re-queues its Component** (owner edge).
     - **Deployments on "the latest"** then run it, and promotion carries
       it (spec 020, unchanged).
   - **No successful build yet:** the Component reads
     `WaitingForBuild`.
   - **The first build is automatic.** Creating a source Component, or
     changing its `source`, creates a Build, attributed to
     `system:auto-build`. Otherwise a new source Component would sit inert
     (OpenChoreo's issue #4409).
   - **A failed build changes nothing that's running.**
   - **`source` needs the build capability ready.** The API refuses it
     otherwise, naming the package.

7. **Auto-build polls; it doesn't use webhooks** (spec open question 3).
   - **How:** with `autoBuild` on, the build reconciler asks the provider
     for the branch's head commit every 5 minutes. A new commit since the
     last Build creates one (`system:auto-build`).
   - **The provider reads the head over Git's smart HTTP:** `GET
     <repo>/info/refs?service=git-upload-pack`, with pkt-line parsing, in
     about 100 lines of stdlib, `providers/internal/gitremote`. No Git
     binary and no new dependency.
   - **Can't be noticed, said plainly:**
     - `ssh://` and `git@` URLs can't be polled over HTTP, so the
       Component reads "auto-build can't see new commits for SSH URLs; use
       HTTPS".
     - A failing poll reads "auto-build couldn't reach the repository
       since <time>".
   - **Why not webhooks:** a laptop behind NAT has no public URL, and
     OpenChoreo's endpoint sits behind `openchoreo-api`, which Serendip
     doesn't use. That's recorded in `build-vs-adopt.md`, to revisit with
     public ingress.
   - **Cost:** one small HTTPS request per auto-build Component every 5
     minutes, measured in T013.
   - **Auto-build is a Component field,** so turning it on or off is
     `serendip/components/write`, not `serendip/builds/write` as spec
     item 14 had it. It's an edit of the Component like any other.

8. **A cheap resync** (spec open question 5).
   - **`PackageState` and `ProviderInstanceStatus` gain `applied`:** a
     fingerprint of what the last full install applied. That's the chart
     versions, the values, and hashes of the hand-built objects. The
     reconciler writes it with the Ready condition.
   - **`Install` gets it back.** When it equals the desired fingerprint,
     the installer skips Helm and only checks a few things: the two plane
     Deployments and the registry are ready, the `ClusterWorkflowPlane`
     agent is connected, and the `ClusterWorkflow` exists. That's about 5
     GETs. If any check fails, or the fingerprint differs, it runs the
     full install.
   - **Persisted, not in memory,** so a restart doesn't re-run Helm.
     Helm's `upgrade --install` pulls the chart over OCI every time; it
     isn't the cheap no-op `deploy.go`'s comment assumes. T013 measures
     both.

9. **Store-level uniqueness** (RP-033, spec open question 6).
   - **A generic mechanism:** the `resources` table gains a nullable
     `unique_key`, with `UNIQUE (kind, unique_key)`, in migration 0009.
     - **A typed store may set a key function,** applied on Create only.
       `ProviderInstance`'s is `capabilityId` + `/` + `environmentId`
       (empty for packages).
     - **The key is fixed after Create,** since the API already refuses
       changing a package's capability.
     - **A violation returns `store.ErrDuplicate`,** which the API answers
       with 409.
   - **Existing duplicates:** the migration keys only the oldest per
     group, using a window function. Newer ones keep a NULL key; the
     reconciler already marks them `Duplicate`, and their removal goes
     through an Approval as before. The migration leaves a note for each.
     (None should exist: no package could be installed before now.)
   - **The API's check stays,** for its clearer message.

10. **The build capability's interface** (`core/reconcile`, no product
    names):

    ```go
    type BuildProvider interface {
        StartBuild(ctx, app Application, comp Component, b Build, cred map[string]any) error
        ObserveBuild(ctx, b Build) (BuildObservation, error) // phase, steps, image, commit
        CancelBuild(ctx, b Build) error                      // idempotent
        RemoveBuild(ctx, b Build) error                      // the run and its credential copy
        BuildLogs(ctx, b Build, opts LogOptions) (LogStream, error)
        BuildEvents(ctx, b Build, since time.Time) ([]RuntimeEvent, error)
        HeadCommit(ctx, src ComponentSource, cred map[string]any) (string, error)
    }
    ```

    - It's registered under `build` once the package is Ready, through a
      lazy handle that returns `ErrProviderNotReady` until then (spec
      014's pattern).
    - `LogStream`, `LogOptions` and `RuntimeEvent` are spec 020's types,
      reused. Build logs get the same caps and NDJSON follow.

11. **Authorization and routes** (actions named in spec 020):

    | Route | Action |
    |---|---|
    | `POST /v1/components/{id}/builds` (body: `commit`) | `serendip/builds/write` |
    | `GET /v1/components/{id}/builds`, `GET /v1/builds/{id}` | `serendip/builds/read` |
    | `POST /v1/builds/{id}/cancel` | `serendip/builds/write` |
    | `GET /v1/builds/{id}/events` | `serendip/builds/read` |
    | `GET /v1/builds/{id}/logs[?follow=true]` | `serendip/components/logs/read` |

    - **Scope:** a Build sits under its Component, so roles at the
      Project, Application or Component apply. Reader sees runs but not
      logs; Log Reader adds logs.
    - **Agents** trigger and cancel as ordinary writes. Removing the
      package stays a delete through an Approval.
    - **`api/catalog`** gains each route, so CLI commands and MCP tools
      follow (spec 021's test fails without them).

12. **Removal says what's lost.**
    - **The catalogue entry gains `removal`:** a plain sentence the
      console shows before the delete request, and the CLI and MCP can
      show it too. For builds: "Build history stays, but run logs and the
      stored images built from source are deleted. Components running a
      built image keep running, but can't be rebuilt, or started on a node
      that doesn't have the image, until the package is back."
    - **Uninstall removes, in order:** the `ClusterWorkflow`, any
      `WorkflowRun`s, `ClusterWorkflowPlane/default`, the chart, and the
      registry with its PVC. Every step is idempotent.
    - **Build records stay.** Their phases are kept, and their logs read
      "expired".

13. **ADR 0020** records decisions 1–3, 5 and 7: our own workflow, the
    registry, no ESO, `Build` as a kind, and polling. **Argo Workflows** is
    a cluster component the chart brings, not a Go dependency. **No new Go
    dependency** comes in: `gitremote` is stdlib.

## Spike findings (T001, 2026-10-24, `serendip-t018`)

Observed on `serendip-t018` (kind, `kindest/node:v1.34.0`, containerd
2.1.3, WSL2 kernel 6.6). The spike's workflow is in
`spike/serendip-dockerfile.yaml`, and the recorded `info/refs` bodies are
in `spike/inforefs/`; T005 and T006 start from them. Every scratch object
was removed afterwards, including the Argo CRDs.

**Caveat on timings:** twice during the spike the whole Docker VM stopped
for minutes. Every pod's logs (kubelet, `controller-manager`) went silent,
once for ~5 min and once for ~16 min, even with the keep-awake running.
Wall times that span those gaps aren't build costs; T013 measures them
properly.

1. **Install and registration** (decision 2 holds).
   - **The chart** `1.3.1` exists (digest `sha256:5f570ab1…`). It brings
     Argo Workflows chart `1.0.23` (app `v4.0.8`), installed into
     `openchoreo-workflow-plane`, and was Ready in 57 s.
   - **Trust:** like the data plane, the chart's Certificate comes from its
     own self-signed Issuer. The agent mounts a `cluster-gateway-ca`
     ConfigMap, which has to be copied in before install. The data plane's
     copy code (`ensureDataPlaneTrustsControlPlane`) applies unchanged.
   - **The agent connects** about 3 s after `ClusterWorkflowPlane/default`
     exists. Before that it logs "bad handshake" every 5 s, harmlessly.
     `status.agentConnection.connected` only turns true at the
     controller's next poll, about 60 s later, so the readiness check has
     to tolerate that delay.
   - **`observabilityPlaneRef`:** none is needed. The CRD's text says a
     missing `ClusterObservabilityPlane/default` fails reconciliation, but
     it didn't.
   - **Idle memory** is far below the research's inferred 200–400 MiB.
     Fresh: Argo controller 13 MB, agent 12 MB. After four builds:
     20 MB and 16 MB, plus the registry at 10 MB, so ~46 MB in all.
2. **The registry** (decision 2's default holds; no containerd mirror).
   - `registry:2.8.3` with `hostNetwork` on port 5000. Pushes go over
     `registry.serendip-builds.svc.cluster.local:5000` (plain HTTP,
     `--tls-verify=false`).
   - Workload pods pulled `localhost:5000/...` with no containerd
     configuration at all (the node has none): 896 ms for a 4.6 MB
     image. containerd falls back to plain HTTP for `localhost`.
   - **The port is a host port.** T005 checks that it's free and fails
     clearly if it isn't.
3. **Our own `serendip-dockerfile` workflow** works end to end: a public
   repository built, pushed and ran, and the commit output equalled the
   head read in item 7.
   - **Two steps, not three.** Checkout (`alpine/git:v2.52.0`), then
     build-and-push (`ghcr.io/openchoreo/podman-runner:v1.2`). This drops
     upstream's `podman save`/`load` through the workspace volume. The
     templates are inline in the `ClusterWorkflow`, so no
     `ClusterWorkflowTemplate`s are needed.
   - **`hostUsers: false` doesn't work on kind.** kind's own OCI hook
     (`/kind/bin/mount-product-files.sh`) fails inside a user namespace,
     for any pod, privileged or not. K3s doesn't have that hook, but it's
     unverified there. **Decision 1 amended:** the build step runs
     `privileged` without `hostUsers: false`. That goes in the risk
     register, with user namespaces rechecked on the first K3s run (T013).
   - **Runs must be standalone. A labelled run is refused.** A run with
     `openchoreo.dev/component` and `openchoreo.dev/project` labels failed
     `ComponentValidationFailed`: "no workflows are allowed by component
     type "serendip-service"". With neither label, there's no component
     validation, and `Component.spec.workflow` and `allowedWorkflows` are
     never consulted. **Decision 5 amended:**
     - every `WorkflowRun` goes in `serendip-builds`, without OpenChoreo's
       labels;
     - the Build-to-Component link lives on Serendip's `Build`, plus a
       `serendip.dev/build` label on the run;
     - the ComponentType is untouched.
   - **Where things land:**
     - the Argo `Workflow` and its pods are in
       `workflows-serendip-builds`;
     - the controller creates that namespace, plus `workflow-sa` and its
       Role, on the first run only;
     - the workspace PVC (2 Gi, local-path) comes from
       `volumeClaimTemplates` and goes with the Workflow.
   - **Outputs aren't in `WorkflowRun.status`.** They're on the Argo
     Workflow named by `status.runReference`, at
     `status.nodes[*].outputs.parameters`. That means `commit` on the
     `checkout` node and `image` on the `build-push` node. Workflow-level
     `status.outputs` is empty.
     - The provider reads `argoproj.io/v1alpha1` Workflows through the
       same `kubernetes.Client`. That's possible because the plane is
       co-located. A remote workflow plane would need another path,
       which goes in "NOT done".
   - **Task status** is in `status.tasks[]` (`name`, `phase`,
     `startedAt`, `completedAt`, `message`), and it lags. Checkout still
     read `Pending` after its pod had completed. A pod-creation failure
     shows up as the task's `message`, with phase `Error` (see the next
     point).
   - **Templating:**
     - `${...}` is OpenChoreo CEL, and `{{...}}` is Argo;
     - a literal `{{` inside a script isn't unescaped, so the scripts
       mustn't use Go templates;
     - an empty `secretName` makes the pod invalid even with
       `optional: true`.
     - **Decision 3 amended:** the Git volume is always
       `<run>-git`, with `optional: true`, and the reconciler creates
       that Secret only when there's a credential.
4. **Logs and events** work through `providers/kubernetes.Client`'s
   `List` and `Stream`, which is `serendipd`'s path.
   - **Pods:** select them with `workflows.argoproj.io/workflow=<run>`.
     The containers are `init`, `wait` and `main`, and the build's output
     is in `main`, which spec 020's `logPath` already prefers.
   - **Follow** streamed live and ended cleanly when the run was
     cancelled.
   - **Ordering and noise:**
     - pods list alphabetically, so steps are ordered by the Workflow's
       nodes;
     - Argo's emissary writes its own `argo=true` lines into `main`, and
       the provider filters them out.
   - **Events:** 11–12 per pod, listed with
     `fieldSelector=involvedObject.name=<pod>`.
5. **Cancel** works by deleting the `WorkflowRun`. The running step got
   SIGTERM (exit 143) within ~1 s, and the pod, Workflow and PVC were gone
   within 6 s. Only our credential copy remained, which the reconciler
   deletes (decision 3).
6. **A private repository, with basic auth.** GitHub has no private
   repository to hand, and creating one is outward-facing, so the spike
   used an in-cluster smart-HTTP server (stdlib Go around
   `git-http-backend`) that requires basic auth.
   - The `<run>-git` Secret (`username`, `password`) was created in
     `workflows-serendip-builds` before the run, then mounted and fed
     through `GIT_ASKPASS`, and the build succeeded.
   - Git's first request is unauthenticated (401), then retried with the
     credential, which is normal.
   - **The Secret must exist before the run starts.** With
     `optional: true`, a late Secret means an anonymous clone. The
     namespace may not exist yet before the first run, so the reconciler
     ensures it.
7. **The head commit over smart HTTP** works with pkt-line, protocol v0.
   - **Public GitHub:** one 2.5 KB `GET`. The first line carries
     capabilities after a NUL, including `symref=HEAD:refs/heads/main`,
     and then `<sha> refs/heads/main`.
   - **Private (basic auth):** 401 without a credential or with a wrong
     one, and the same format, 372 bytes, with the right one.
   - **A missing branch** just has no matching line.
   - **GitHub answers 401 `Basic realm="GitHub"` for a repository that
     doesn't exist.** "Not found" and "private" can't be told apart, so
     the message says "the repository needs a credential, or doesn't
     exist".
   - A `User-Agent` starting `git/` was sent.
8. **Helm's cost (decision 8 holds, more strongly).**
   - A no-change `upgrade --install` took 23.9 s and then 19.0 s. A local
     chart directory took 18.5 s. So the OCI pull isn't the cost. The cost
     is the apply, `--wait`, and the subchart's CRD hook Job.
   - **Each run** writes a new release revision.
   - **The CRD hook Job:** the subchart defaults to `crds.full=true`. On
     every install and upgrade, a pre-install/pre-upgrade Job
     (`registry.k8s.io/kubectl:v1.36.3`) downloads the full CRDs from
     GitHub and server-side-applies them. So each resync is also a GitHub
     download.
   - **Helm never owns those CRDs,** so `crds.keep` has no effect, and
     they survive `helm uninstall`.
   - **Decision 12 amended:** Uninstall leaves the eight
     `argoproj.io` CRDs, as definitions only, the same stance as Gateway
     API. Deleting them could break a person's own Argo. That goes in the
     risk register, and the removal sentence doesn't promise otherwise.
   - **The build's in-progress cost** was sampled at ~232 MB and ~1.6
     cores for the `service-go-greeter` Go build. That's only a sample.
     T013 measures it.

## Amendment (T013, 2026-11-06): installs and removals run in the background

T013's first package run found that the package couldn't be installed.
The engine caps each reconcile at 10 s, and the installer ran Helm
(`--wait`, up to 10 min) inside it, so Helm was killed and the release
was left locked (RP-045). The user chose to fix it in core, for every
package:
- **`core/reconcile/packagejob.go`:** each package's install or removal
  runs as a background job, at most one per package at a time, bounded
  at 30 minutes. A reconcile waits 3 s, so a resync's quick checks still
  finish in the same reconcile. Otherwise it reads Installing and
  requeues, and an installed package keeps reading Ready while a slow
  check finishes.
- **The uninstall clean-up** is now a method,
  `ProviderInstanceReconciler.UninstallCleanup()`, sharing those jobs.
  It answers `ErrPackageBusy`, and the approval retries it, until the
  removal has finished.
- **The builds installer** recovers a release left `pending-*`, then
  installs again.
- **A deliberate exception** to `core/reconcile`'s single-goroutine
  rule: dispatch stays sequential, and the job's goroutine only runs the
  installer. It's noted in `core/reconcile/CLAUDE.md`.

## Amendment (T013, 2026-11-09): minimal Argo CRDs, and cost at the node level

The package run showed that the package's cost hides outside its own
pods (RP-046). With the chart's default full Argo CRDs (4.66 MiB of
schema), kube-apiserver idled ~550 MiB higher and cert-manager's
cainjector ~60 MiB higher. The package's own pods were 34 MiB. The user
chose to try the minimal CRDs first:
- **The installer sets `argo-workflows.crds.full=false` and
  `crds.keep=true`.** The CRDs ship inside the chart and are owned by
  Helm. They still outlive removal (RP-039 stands), and no install
  downloads from GitHub. Full CRDs left by an earlier install must be
  deleted by hand first (`VERSIONS.md`).
- **The stated cost (decision 4)** is the node's use beyond the same
  run's platform phase, never below the package's own pods.
- **Result:** idle 453 MiB and 19 mcores, peak 1129 MiB and 1787 mcores,
  against 954 MiB idle with the full CRDs. A full Helm run takes 5 s, not
  23 s.

## Interfaces and types (sketch; settled in T002)

- **`core/provider`:** `Requirements{IdleMemoryMiB, IdleCPUMillicores,
  PeakMemoryMiB, PeakCPUMillicores int; MeasuredOn, Results, Note string}`.
- **`core/reconcile`:**
  - `Package` gains `Requirements *provider.Requirements` and `Removal
    string`;
  - `PackageState` gains `Applied string`;
  - `PackageInstaller.Install` reads `inst.Status.Applied`;
  - `BuildProvider`, `BuildReconciler`, and `ComponentReconciler`'s image
    resolution.
- **`core/resource`:**
  - `KindBuild`, `BuildSpec` and `BuildStatus`;
  - `ComponentSource`, with validation (exactly one of image and source,
    an HTTPS or SSH URL, a known Secret);
  - `ProviderInstanceStatus.Applied`.
- **`core/store`:** `ErrDuplicate`. `sqlite.Store.SetUniqueKey(func(S)
  string)`.
- **`core/graph`:** a Build is owned by its Component.

## Files

- **`core`:**
  - `core/provider/requirements.go`;
  - `core/resource/build.go` and `app.go` (`source`), and
    `capability.go` (`Applied`);
  - `core/reconcile/build.go` (new), `providerinstance.go` and the
    Component reconciler;
  - `core/store/store.go`, `core/store/sqlite/store.go` and
    `migrations/0009_unique_key.sql`.
- **`providers/build/openchoreo/`** (new):
  - `installer.go` (Install, Uninstall, fingerprint, checks);
  - `registry.go`;
  - `workflow.go` (the `ClusterWorkflow` and its templates);
  - `builds.go` (the `BuildProvider`);
  - `requirements.go`;
  - `VERSIONS.md`, `doc.go` and tests.
- **`providers/internal/gitremote/`** (new): the smart-HTTP head lookup.
- **`api`:**
  - `build_handler.go` (new);
  - `package_handler.go`;
  - `authz.go` and the route table;
  - `catalog/`;
  - `spec022_acceptance_test.go`.
- **`cmd/serendipd`:** `packages.go` (the installer, the lazy build
  handle and the Build reconciler wiring).
- **`cmd/serendip`:** generated from the catalogue; `docs/cli-and-mcp.md`.
- **`ui/`:**
  - the Builds tab (runs, logs with follow, cancel, trigger);
  - the Component form's source fields;
  - the Packages page's cost and removal wording.
- **`profiles/edge-1/bench/`:** a package footprint run (idle, in-build
  peak, resync) and its results.
- **Docs:**
  - ADR 0020;
  - `application-platform.md` (a "Builds (spec 022)" section),
    `resource-model.md`, `provider-model.md` and `authorization.md`;
  - the risk register, `build-vs-adopt.md` and `PROGRESS.md`.

## Risks

- **Builds on 2 cores.** One build can pin the machine for minutes and
  slow everything beside it. The plan states it (the in-build peak), runs
  one at a time per Component, and gives the build pod CPU limits from
  T013.
- **Privileged build pods.** `podman` needs privilege, and user
  namespaces may not work on every node image. Builds run in their own
  namespace, on Serendip's own template only: no person-supplied
  workflows.
- **The registry is single-node and never cleaned.** Images pile up on
  the local-path volume. That's recorded as accepted; garbage collection
  is a later spec. The console's package view could show its size later.
- **The credential copy.** Git credentials sit in a Kubernetes Secret for
  a run's lifetime (up to an hour after it ends, with the run). The risk
  is recorded.
- **Upstream drift.** Serendip owns its workflow templates, so OpenChoreo's
  template changes don't reach us, but neither do their fixes. Pinned in
  `VERSIONS.md`.
- **Helm on resync.** If the fingerprint misses a change, a
  hand-modified plane isn't repaired until a check fails. The checks
  cover the things that break builds.

## Explicitly NOT done

- **Buildpacks** (Paketo, GCP, Ballerina) and other build methods.
- **Webhooks** for auto-build, and Git host integrations.
- **External registries,** and private base images.
- **Registry garbage collection** and quotas.
- **Multi-node image pulls** (the containerd mirror through the agent),
  unless T001 forces it. It didn't.
- **A remote workflow plane.** Run outputs are read from the Argo Workflow
  directly, which needs the plane in the same cluster (T001 item 3).
- **Signing, scanning and SBOMs.**
- **Declared `Requirements` for existing providers** (RP-037 stays open
  for them).
- **Pruning old Build records.**
- **The observability package** (spec 023).
