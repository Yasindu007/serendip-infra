# Tasks: Spec 022 — Builds, the workflow-plane package

Ordered. `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests or docs). Run `contract-guardian` before committing anything under
`core/` or `providers/`, and `go-reviewer` after each non-trivial Go task.

Dependency sketch:
- T001 → T002 → (T003 ∥ T004) → T005 → T006 → T007 → T008 → T009 →
  (T010 ∥ T011) → T012 → T013 → T014 → T015 → T016.
- T011 (the console) can start once T008's routes exist.

## T001 — Spike: the workflow plane on serendip-t018 ✅ 2026-10-24

- **Do,** on `serendip-t018`, removing the scratch objects afterwards:
  1. **Install the plane:** chart `1.3.1` (confirm the OCI artifact
     exists); copy the CA; apply `ClusterWorkflowPlane/default`. Record
     the agent's connection and the pods' idle memory.
  2. **The registry:** `registry:2` with `hostNetwork`. Push from a pod
     over its Service; pull `localhost:<port>/...` as a workload image.
     If the pull fails, try the containerd mirror, and amend decision 2.
  3. **A minimal `serendip-dockerfile` `ClusterWorkflow`:** check out a
     public repository, `podman build`, push, and output the image and
     commit. Find out:
     - whether `hostUsers: false` works on the node image;
     - whether a `WorkflowRun` with component and project labels works
       without `Component.spec.workflow` or `allowedWorkflows`;
     - where the outputs land.
  4. **Logs and events:** the run's pods, namespace, labels, containers;
     follow through `serendipd`'s client path.
  5. **Cancel:** delete a running `WorkflowRun`, and check the pod
     stops.
  6. **A private repository** with a plain Secret mounted as
     `git-secret`, using basic auth.
  7. **The head commit over smart HTTP:** against a public GitHub
     repository, and a private one with basic auth.
  8. **Helm's cost:** a repeated `upgrade --install` with nothing
     changed, for decision 8.
- **Files:** `plan.md` ("Spike findings", with decisions amended if the
  facts differ), and `providers/build/openchoreo/VERSIONS.md`.
- **Verify:** each item has observed evidence in `plan.md`; `kubectl get`
  shows no scratch objects left.

## T002 — ADR 0020 and the core types ✅ 2026-10-24

- **Do:**
  - ADR 0020 (plan decision 13).
  - **`core/provider.Requirements`.**
  - **`core/resource`:** `KindBuild` with its spec, status and
    validation; `ComponentSource` and its validation (one of image and
    source); `ProviderInstanceStatus.Applied`.
  - **`core/graph`:** Build is owned by its Component.
  - **`core/reconcile`:** `BuildProvider`, `BuildObservation`;
    `Package.Requirements` and `Removal`; `PackageState.Applied`; and
    `Catalogue.Validate` refusing a package without `Requirements`.
- **Files:** `docs/decisions/0020-*.md`, `core/provider/requirements.go`,
  `core/resource/build.go`, `app.go`, `capability.go`,
  `core/reconcile/build.go` and `providerinstance.go`, with tests.
- **Verify:** `go test ./core/...` passes; `bash
  .claude/scripts/check-imports.sh` passes; a table test shows
  `Validate` refusing a package with an installer but no `Requirements`.

## T003 [P] — Store-level uniqueness (RP-033) ✅ 2026-10-25

- **Do:**
  - **Migration 0009:** the `unique_key` column and index; the oldest
    instance per group keyed; notes for the rest.
  - **`store.ErrDuplicate`**, and `SetUniqueKey` on the sqlite store,
    applied on Create.
  - The API maps it to 409, and `ProviderInstance`'s key is wired.
- **Files:** `core/store/store.go`, `core/store/sqlite/store.go` and
  `migrations/0009_unique_key.sql`, with tests; `api/` error mapping;
  `api/server.go`.
- **Verify:** `go test ./core/store/... ./api/...` passes, including:
  - 20 concurrent Creates of one capability leave exactly one row, and
    the rest get `ErrDuplicate`;
  - the migration over a store holding two duplicates keys only the
    older one and leaves a note;
  - `POST /v1/provider-instances` racing returns one 201 and the rest
    409.

## T004 [P] — A cheap resync for installed packages ✅ 2026-10-25

- **Do:** `ProviderInstanceReconciler` passes the instance (with
  `status.applied`) to `Install`, and writes `PackageState.Applied` with
  the Ready condition, only when it changes.
- **Files:** `core/reconcile/providerinstance.go` and its tests.
- **Verify:** `go test ./core/reconcile/...` passes. With a fake installer
  counting full installs, 10 resyncs of a Ready instance with an
  unchanged fingerprint make no full install and no store write. A
  changed fingerprint makes one.

## T005 — The workflow-plane installer ✅ 2026-11-01

- **Do:** in `providers/build/openchoreo`:
  - the Helm install and CA copy;
  - `ClusterWorkflowPlane`;
  - the registry (Deployment, Service and PVC, hand-built);
  - the `ClusterWorkflow` and templates from T001;
  - the fingerprint, and the quick checks;
  - Uninstall in decision 12's order, idempotent;
  - `requirements.go` holding a nil value until T013 fills it.
- **Files:** `providers/build/openchoreo/{doc,installer,registry,workflow,requirements}.go`
  with tests, and `VERSIONS.md`.
- **Verify:** `go test ./providers/build/...` passes against the fake
  Helm and client:
  - a first install runs Helm once;
  - a matching fingerprint with healthy checks runs no Helm;
  - a missing registry Deployment triggers a full install;
  - Uninstall twice succeeds;
  - `go build ./core/... ./api/...` passes with the package deleted
    (constitution 2).

## T006 — The `BuildProvider` and `gitremote` ✅ 2026-11-01

- **Do:**
  - **`StartBuild`** (the `WorkflowRun`, plus a credential Secret when one
    is given);
  - **`ObserveBuild`** (conditions and tasks into phases; the image and
    commit from outputs);
  - **`CancelBuild` and `RemoveBuild`;**
  - **`BuildLogs`** (spec 020's caps and follow) and **`BuildEvents`;**
  - **`HeadCommit`,** through `providers/internal/gitremote`.
- **Files:** `providers/build/openchoreo/builds.go`,
  `providers/internal/gitremote/` with tests.
- **Verify:** `go test ./providers/...` passes, including:
  - pkt-line parsing from recorded `info/refs` bodies (public, auth
    required, a missing branch);
  - an `ssh://` URL refused with a clear error;
  - the status-to-phase table covering every T001 status sample.
- **Done as:**
  - **Files:**
    - `builds.go`: Start, Observe, Cancel, Remove and HeadCommit;
    - `buildlogs.go`: logs and events;
    - `providers/internal/gitremote/`, with T001's `info/refs` bodies
      copied into `testdata/`.
  - **Names:**
    - **Runs** are `build-<16 hex of sha256(Build ID)>`. An ID can be 128
      characters, with `:`.
    - **The full ID** is in the `serendip.dev/build-id` annotation, and
      `BuildLabel` carries the run name.
    - **Images** go to `serendip/cmp-<hash>`, the same hash the
      application provider names the Component with.
  - **Start** creates and never updates. An existing run returns at once,
    leaving its credential copy alone. The copy (`username` and
    `password`; `token` is accepted, with the user name defaulting to
    `git`) is applied before the run.
  - **Cancel** deletes the run and the copy, the same as Remove. So after
    a cancel, `ObserveBuild` reads `Gone`, never `Cancelled`. **T007 sets
    `Cancelled` from the Build's own `spec.cancel`.**
  - **Observe:**
    - the phase is the run's conditions, or the Argo Workflow's phase
      when that's further on, because the run lags;
    - steps, commit and image come from the Workflow's `Pod` nodes, and
      the run's tasks before it exists;
    - the failure message is the failed step's own, because OpenChoreo's
      is a fixed sentence;
    - a run that can't start (`WorkflowPlaneNotFound`, and the like)
      reads Queued with that reason;
    - Succeeded without an image reads Failed.
  - **Logs** are read step by step in order, with Argo's own `argo=true`
    lines dropped. With follow, a stream:
    - waits for each step's pod, and for its container to start;
    - ends when the run does.

    A gone run's logs are an error that says "expired". There's no
    sentinel error, because `api/` can't import a provider. **T008 checks
    `ObserveBuild`'s `Gone` first.**
  - **gitremote:**
    - HTTPS only, and it never follows a redirect off HTTPS;
    - an 8 MiB cap, with its own error when it's hit;
    - an advertisement without its final flush is truncated, not "no
      branch";
    - `ErrSSH`, `ErrAuth`, `ErrNotFound` and `ErrNoBranch`;
    - a bad URL is never repeated in the error, since it may hold a
      credential.
  - **Reviews:**
    - **contract-guardian** passed. Its blocker, the URL in an error, is
      fixed.
    - **go-reviewer:** its fixes are in: the open-timeout cause, a follow
      stuck on a container that never starts, the re-list race, the
      gitremote edge cases, and style.
    - **RP-042** was added (a follower's polling).
  - **Still open, for T007:** when a start fails after its credential
    copy is made, the reconciler retries the start, or calls
    `RemoveBuild`, so the copy doesn't stay behind. Otherwise a copy lives
    as long as its run, up to an hour after it ends, which is the plan's
    accepted risk.

## T007 — The Build reconciler, image resolution and auto-build ✅ 2026-11-06

- **Do:**
  - **`BuildReconciler`:**
    - one run at a time per Component;
    - reads the credential through the Secrets capability;
    - start, observe and cancel;
    - writes the status;
    - re-queues the Component on success.
  - **`ComponentReconciler`:** resolves a source Component's image;
    reads `WaitingForBuild`; creates the first Build on create or a
    source change.
  - **Auto-build polling** every 5 minutes, with the "can't be noticed"
    messages.
- **Files:** `core/reconcile/build.go` and the Component reconciler, with
  tests.
- **Verify:** `go test ./core/reconcile/...` passes, with table tests for:
  - queued behind a running build;
  - success → a new release image on the Component;
  - failure → no change;
  - cancel;
  - provider not ready → waiting;
  - a new head commit → one Build, and the same head → none;
  - an SSH URL → the message;
  - deleting a Component removes its runs through the clean-up.
- **Done as:**
  - **Files:**
    - `core/reconcile/build.go`: `BuildReconciler`, `BuildCleanup`, the
      `ErrSource*` errors and `AutoBuildActor`;
    - `componentbuild.go`: image resolution and auto-build;
    - `application.go` and `deployment.go`: both hand the provider the
      built image in place of the source (`runnable`). The Deployment
      side wasn't in the file list, but without it a release has no
      image;
    - `engine.go`: `Engine.Enqueue`;
    - `build_test.go`.
  - **New status fields:**
    - **`BuildStatus.Source`:** the Component's source, recorded when the
      reconciler first sees the Build. A later edit doesn't change what's
      built.
    - **`BuildStatus.Started`.**
    - **`ComponentStatus.Image`:** the resolved image, which the
      Deployment reconciler reads.
  - **The image** is the newest successful build of the *current* source,
    which `SameBuild` compares: repository, branch, path and Dockerfile.
    - Changing the credential or auto-build doesn't rebuild.
    - Changing the source starts a build, and the Component reads
      `WaitingForBuild` until it succeeds. The old workload keeps running
      meanwhile, because the provider isn't called.
    - A failed or cancelled last build is named in that message, and isn't
      retried by itself.
  - **Cancel** always calls `CancelBuild`, even when `Started` isn't
    recorded, because a run may have started in a pass whose write lost
    to the cancel's edit (go-reviewer). A dropped write requeues, and
    doesn't re-queue the Component.
  - **A failed start** removes the run and its credential copy, reads
    `StartFailed` without the provider's text, and is retried each pass,
    with no cap. It holds its Component's queue until it starts or is
    cancelled.
  - **Auto-build:**
    - The `AutoBuild` condition is only set while auto-build is on. Its
      reasons are `Watching`, `SSHUnsupported`, `NeedsCredential`,
      `RepositoryNotFound`, `NoSuchBranch`, `CredentialUnreadable` and
      `Unreachable`. `Unreachable` is dated from the first failure.
    - The head isn't read while a build of the source is unfinished.
    - A commit already asked for or built *for this source*, whatever the
      outcome, isn't built again.
    - When the head was last read is kept in memory. RP-043 covers that,
      and the read on the single reconcile loop.
    - The provider's `HeadCommit` now wraps gitremote's errors in the core
      ones.
  - **The audit actor:** builds that Serendip starts itself are created
    as `system:auto-build`, through `audit.WithActor` on that one Create.
    That's a deliberate exception to core/reconcile/CLAUDE.md's rule that
    the engine sets the actor (contract-guardian: acceptable).
  - **Reviews:**
    - **contract-guardian:** its blocker, duplicate "auto-build" names,
      doesn't apply. Names aren't unique, since T003's key is only set for
      ProviderInstances, and a test covers a second first build. Its
      malformed-head guard and message-contract points are in.
    - **go-reviewer:** its fixes are in:
      - the cancel/start race;
      - no `finished` after a dropped write;
      - an immediate retry after a failed create;
      - `builtCommit` scoped to the current source;
      - a known commit or image kept when a read lacks it;
      - a separate reason for an unreadable credential;
      - a `headError` test.
  - **For T008:** the API must check that whoever sets `credentialSecret`
    may read that Secret. The reconciler reads it as the system.
  - **For T009:** wire `WithBuilds`, `NewBuildReconciler` (with
    `engine.Enqueue`) and `BuildCleanup` for `KindBuild`.

## T008 — The API ✅ 2026-11-06

- **Do:**
  - **The build routes** (decision 11), with route-table rules and
    catalogue entries.
  - **`source` validation:** the build capability must be ready.
  - **`GET /v1/packages`** gains `requirements` and `removal`.
  - **Audit** for triggers and cancels, through the store.
- **Files:** `api/build_handler.go`, `api/package_handler.go`,
  `api/authz.go`, `api/catalog/`, `api/server.go`, with tests.
- **Verify:** `go test ./api/...` passes:
  - the route-table and catalogue tests cover every new route;
  - a Reader gets runs (200) but not logs (403);
  - a Contributor installing a package gets 403;
  - `source` without the package gets 422, naming it.
- **Done as:**
  - **Files:** `api/build_handler.go` (new: `BuildHandler`,
    `buildReadiness`, `sourceChecker`), `authz.go`, `server.go`
    (`WithBuilds`), `topology_handler.go`, `runtime_handler.go`,
    `package_handler.go`, `catalog/operations.go`,
    `docs/cli-and-mcp.md` (its test needs every operation listed), and
    `build_handler_test.go`. No `core/` or `providers/` change.
  - **Actions:** `nestedPaths` gives Build its own namespace,
    `serendip/builds/{read,write,delete}`, without the generic CRUD routes
    (no `POST /v1/builds`, no `DELETE`). Graph routes and a Component's
    cascade delete now name `serendip/builds/delete` for its Builds.
  - **Routes** (decision 11):
    - **Trigger:** `{commit}`, optional. The name is `build-<12 hex>`,
      or `build-<UTC time>` for the head. It answers 409 on an image Component and 422 while
      the capability isn't ready.
    - **List** is newest first.
    - **Cancel** sets `spec.cancel`, retrying a version conflict with the
      reconciler up to 3 times, then 503 with `Retry-After`. Asking twice
      is 200; a finished build is 409.
    - Trigger and cancel are audited by the store, with the caller and the
      policy evidence.
  - **Logs and events:**
    - **A build not yet started** is 409, and one that never ran is 410.
    - **`ObserveBuild().Gone`** gives 410 "logs expired".
    - **No `WithBuilds`** gives 503; the run records still read.
    - **Caps:** build logs share the Deployment logs' limiters.
      `readRecentLogs` and `followLogs` were taken out of
      `RuntimeHandler`, unchanged.
  - **`source` checks** (Component create and update), skipped when the
    source is exactly as stored:
    - `ValidateComponentSource` gives 400;
    - the build capability not ready gives 422, naming the catalogue's
      build package(s) and the readiness reason;
    - **`credentialSecret`, when newly named or the repository changes:**
      the caller needs `serendip/secrets/value/read` (a data action) at
      that Secret, else 403, audited. A missing Secret is 400. The data
      action, not `secrets/read`, because the build sends the value to
      the repository's host: otherwise a Contributor could point someone
      else's credential at a host they control. **Consequence:** Owner
      and Contributor need a Secrets role (Secrets User or Officer) to
      name a credential, as for reading a value.
  - **`GET /v1/packages`** already carried `requirements` (null while
    unmeasured) and `removal` through `Package`. It gains `installable`
    and `notInstallable` (the reason).
  - **Catalogue:** `builds_trigger`, `_list`, `_get`, `_cancel`,
    `_events` and `_logs` (follow is CLI only).
  - **go-reviewer:** no blockers. Its fixes are in: distinct build names,
    and cancel's exhausted retries as 503, not 409. Accepted, not fixed:
    - **Other source fields stay editable without the value permission**
      (branch, path, Dockerfile, auto-build), since they don't send the
      credential to another host, and the build step never sees it.
      **RP-044** records that condition.
  - **Fixed afterwards:** a run that expires between `ObserveBuild` and
    the logs or events read answered 502. A failed read now looks again,
    and answers 410 when the run is gone (`readFailed`). That's why
    `readRecentLogs` and `followLogs` take a failure callback.
  - **For T009:** pass `api.WithBuilds(<the lazy build handle>)`. Readiness
    comes from the registry's `build` capability, which the
    ProviderInstance reconciler already reports.

## T009 — serendipd wiring ✅ 2026-11-06

- **Do:** the catalogue's builds entry gets the installer and its
  `Requirements`; the lazy `build` handle is registered once Ready; the
  Build reconciler is registered; the uninstall clean-up is wired.
- **Files:** `cmd/serendipd/packages.go` and `main.go`.
- **Verify:** `go build ./...` and `go test ./cmd/...` pass. `serendipd`
  starts against `serendip-t018`, and `GET /v1/packages` shows the builds
  package as not installable while `Requirements` is nil.
- **Done as:**
  - **Catalogue:** `packageCatalogue(installer)` gives the builds entry
    `buildplane.NewInstaller` (helm checked at startup, like the other
    deployers), `buildplane.Requirements` (still nil) and decision 12's
    removal sentence. Without a cluster there's no catalogue at all, as
    before.
  - **The lazy handle:** `lazyBuilds` (`lazy.go`), set by `buildHandle`
    when the package reports Ready, and cleared when it reports anything
    else (`lazy.clear`). `registerPackages` takes an `onReadiness` map,
    called from the readiness report. The provider is registered under
    `build` on its first Ready.
  - **Reconcilers:** `registerBuilds`, from
    `registerKubernetesReconcilers`: `ComponentReconciler.WithBuilds` and
    `NewBuildReconciler` with `engine.Enqueue`, both against the lazy
    handle, with the `SecretReconciler` as the credential reader.
  - **Clean-up:** `BuildCleanup` for `KindBuild` uses `NewBuilds(client)`
    directly, not the lazy handle. Removal is idempotent and must still
    work once the package is gone, or deleting a built Component would
    wait for ever as `ProviderNotReady`.
  - **API:** `api.WithBuilds(kube.builds)`.
  - **Verified:** `go build ./...`, `go vet` and `go test ./cmd/...`
    pass, with new tests for the unmeasured refusal and the handle
    following readiness. A scratch `serendipd` (`:8088`, fresh DB) against
    `serendip-t018` answered `GET /v1/packages` with the builds package
    `available: true`, `installable: false`, "can't be installed until its
    resource cost is measured", and `/v1/readiness` `build` `Unavailable`.
    (Secrets setup failed there only because the scratch run had no
    OpenBao credentials path.)

## T010 [P] [A] — CLI, MCP and docs ✅ 2026-11-06

- **Do:** catalogue-generated commands and tools (`serendip builds
  list|get|trigger|cancel|logs [--follow]`, and the package
  `requirements` in `packages list`); `docs/cli-and-mcp.md` regenerated.
- **Files:** `cmd/serendip/`, `mcp/`, `docs/cli-and-mcp.md`.
- **Verify:** `go test ./cmd/serendip/... ./mcp/... ./api/catalog/...`
  passes, including the docs-table check.
- **Done as:**
  - **The six `builds_*` commands and tools** were already generated from
    T008's catalogue entries. `TestBuildsCommands` checks each one's
    request, and the MCP tool-list test now requires them and
    `packages_list`. `builds_logs`'s `follow` is CLI-only, like the
    Deployment logs' (`TestFollowIsCLIOnly`).
  - **`serendip packages list`** prints a table with idle and in-use
    cost, or "not measured" with no number, then each package's
    measurement source, note, why it isn't installable and the removal
    sentence (`printPackages`). `--json` and MCP give the raw record. The
    `packages_list` summary now says `requirements` is null when not
    measured.
  - **A followed build's logs** end with "the build ended", not "the
    instances stopped writing".
  - **`docs/cli-and-mcp.md`** gains "Packages and builds", and the
    `packages_list` row matches the new summary. The table is kept by
    hand; `TestReferenceListsEveryOperation` checks it.

## T011 [P] — The console ✅ 2026-11-06

- **Do:**
  - **The Builds tab:** runs, trigger at a commit, cancel, logs with
    follow and reconnect, and events, with "logs expired" after the TTL.
  - **The Component form:** image or source, the source fields and a
    credential Secret picker, and auto-build with its messages.
  - **The Packages page:** measured cost before the install button, "not
    measured" otherwise, and the removal sentence before the delete
    request. Each with its "Same request through the API" panel.
- **Files:** `ui/src/screens/Runtime.tsx`, `Components.tsx`, the Packages
  screen, with tests.
- **Verify:** `npm test` and `npm run build` in `ui/` pass; a test shows
  the cost text and the "not measured" text, and none shows a number for
  an unmeasured package.
- **Done as:**
  - **The Builds tab** (`Runtime.tsx` `BuildsTab`), inside the
    availability gate: runs newest first (state, commit, started, took),
    the AutoBuild condition's message, "Start build" at the head or a full
    commit (checked before sending), and the chosen run's steps, cancel
    (through `ActionConfirm`), logs and events. Logs reuse the Deployment
    logs' follow and reconnect, now one `Logs` component over a path.
    Follow is offered only while the run is unfinished. A 410 reads "Logs
    expired" (or "never ran"), and 409 "hasn't started yet", in words,
    not as an error. An image Component's tab says it has no builds.
  - **The Component form** chooses a ready-made image or source. The
    source fields mirror `ValidateComponentSource`'s grammar
    (`componentForm.ts`), empty optional fields are left to the server's
    defaults, and `image` is sent as "". The credential picker lists
    `GET /v1/secrets` and keeps an already-named Secret it can't list. The
    form warns, before sending, when the builds package isn't ready and
    the source is new or changed. Auto-build's hint covers the 5-minute
    poll, and SSH URLs that can't be watched. The overview shows the
    source and the running built image.
  - **The Packages page** shows measured idle and peak ("while a build
    runs") cost with where it was measured and the fit note, or "Not
    measured" with no number. Install is offered only when `installable`,
    otherwise disabled with `notInstallable`'s reason, and the install
    confirmation restates the cost. The removal note is the API's
    `removal` sentence.
  - **Tests:** `platform.test.tsx` ("builds tab", "component source
    form", "packages page", where the unmeasured package's text has no
    number in MiB or cores) and `lib.test.ts` (source spec and
    validation). 133 tests pass; the build is 99.2 KiB gz.

## T012 [A] — In-process acceptance tests

- **Do:** `api/spec022_acceptance_test.go` for criteria 1, 3, 4, 6–8,
  10 and 11, on a fake build provider and installer.
- **Files:** `api/spec022_acceptance_test.go`.
- **Verify:** `go test ./api/ -run Spec022` passes, and `go test ./...`
  passes.
- **Done as:**
  - **The fakes:** `builder022` (runs that move only when the test says,
    remembering each run's credential; follow streams from a channel),
    `platform022` (records the image each Component and Environment
    runs), `installer022` and `creds022`. The real Build, Component
    (`WithBuilds`), Deployment, ProviderInstance and Approval reconcilers
    run by hand after each change, with `UninstallCleanup` and
    `BuildCleanup` as the approval's clean-ups.
  - **One test per criterion,** `TestSpec022Criterion{1,3,4,6,7,8,10,11}_*`:
    - 1: measured idle and peak cost with its source; the unmeasured
      package reads `null`, never a number.
    - 3: a Contributor's install is a 403 and audited; the Owner's goes
      NotInstalled → Installing → Ready, audited with the decision.
    - 4: ten concurrent installs give one 201 and nine 409s; a write past
      the API gets `ErrDuplicate` from the store's unique key.
    - 6: create, trigger, queued → running → succeeded, follow over a real
      connection, the built image served in development, then promoted.
    - 7: the credential reaches the build, but no API response, captured
      `log` output or audit column carries it.
    - 8: a failed build shows failed with its logs; the Component and
      development keep the earlier image; a running build cancelled reads
      cancelled and the run is stopped.
    - 10: the removal sentence comes before any request; an agent's delete
      is `needs-approval`, its request stays pending (nothing removed)
      until a person approves; then the workloads are uninstalled and
      builds reads NotInstalled.
    - 11: a Reader sees runs and events but not logs, and can't trigger or
      cancel; every console action is a CLI command and an MCP tool;
      follow is CLI-only.
  - **Left to other tasks:** 2, 5, 9 and 12 (T013, T014 and the
    installer's and reconciler's own tests).

## T013 — Footprint on the simulated T470, and the stated cost ✅ 2026-11-09

- **Do:**
  - **edge-1 without the package:** check for regression against spec
    020's run.
  - **With it installed:** idle memory and CPU over 10 minutes; the peak
    during a build of a small sample repository; resync CPU with the
    fingerprint path (and, once, with full Helm for comparison); and an
    auto-build poller's cost.
  - **Fill `requirements.go`** from the results; write the fit note.
- **Files:** `profiles/edge-1/bench/` (the package run),
  `profiles/edge-1/bench/results/2026-10-*-spec022-simulated-t470/`, and
  `providers/build/openchoreo/requirements.go`.
- **Verify:**
  - the results folder holds raw samples and a summary;
  - `GET /v1/packages` returns the same numbers as the summary;
  - edge-1's idle footprint without the package is within noise of spec
    020's.
- **Done as:**
  - **Results:** `profiles/edge-1/bench/results/2026-11-06-spec022-simulated-t470/`
    (`README.md`; `no-package/`, `package-full-crds/` and `package/`, each
    with `result.json` and `summary.md`; and
    `compare-no-package-vs-spec020.md`).
  - **Without the package: PASS** against spec 020's run. `serendipd`
    idled at 29.2 MiB.
  - **The harness:** `bench-edge1 --package openchoreo-workflow-plane`
    (`cmd/bench-edge1/package.go`). Fixed on the way:
    - `sampleBuild` dropped its results;
    - the package section never reached `summary.md`;
    - process queries now retry;
    - the stated cost is now the node's use beyond the platform, which
      `render` recomputes.
  - **Found and fixed:** the package couldn't be installed (RP-045, the
    background package jobs). The full Argo CRDs held kube-apiserver
    ~550 MiB higher (RP-046), so the installer now uses the minimal set.
    Both are in plan.md's T013 amendments.
  - **Stated cost** (`providers/build/openchoreo/requirements.go`): idle
    453 MiB and 19 mcores, peak 1129 MiB and 1787 mcores, with the fit
    note.
    - The package's own pods: 34 MiB idle, 277 MiB peak.
    - Builds took 2m16s–3m30s running, at the 2-CPU quota.
    - The fingerprint resync costs +0.44 mcores, against a 5 s full Helm
      run.
  - **`GET /v1/packages`** on a `serendipd` built from the source returns
    those numbers, with `installable: true`.

## T014 — Real-cluster verification on serendip-t018

- **Do:** criteria 3–11 live, through the console, the CLI, and Claude
  Code through `serendip mcp`. That includes:
  - installing from the console;
  - a public build, followed live, served and promoted;
  - a private build with a Secret credential (grepping responses, server
    logs and audit for it);
  - a failure and a cancel;
  - auto-build on a pushed commit;
  - a Reader's 403 on logs;
  - an agent's removal waiting for a person;
  - the removal, after which no package workloads remain.
- **Files:** `specs/022-builds/verification.md`.
- **Verify:** each criterion has evidence (commands, outputs and
  screenshots) in `verification.md`, and the cluster ends clean.
- **Run as** (2026-11-09; not ticked yet, because the cluster didn't end
  clean): `verification.md` holds the evidence.
  - **Passed:** criteria 1, 3–9 and 11, including Claude Code through
    `serendip mcp`, with the agent's removal waiting for a person.
  - **Criterion 10 fails one point:** finished builds' Argo Workflows,
    pods and workspace volumes outlive the removal (F4), and so does the
    agent's TLS Secret (F1). They're left on `serendip-t018` for T015.
  - **Findings for T015:** F1–F4, and U1–U4.
  - **Ticked once** T015 has fixed F4 and F1 and a removal after a build
    leaves the cluster clean.

## T015 — Reviews and fixes

- **Do:** `contract-guardian` (core and providers), `go-reviewer`, a
  security review (the privileged pods, the credential copy, the
  registry's exposure, the poller's outbound requests), and
  `/code-review`. Fix the findings or record them.
- **Files:** wherever the findings land; `docs/risk-register.md`.
- **Verify:** every finding is fixed or has a risk-register entry, and
  `go test ./...` and `npm test` pass after the fixes.

## T016 [A] — Docs and close-out

- **Do:**
  - `application-platform.md` ("Builds (spec 022)"), `resource-model.md`
    (Build, source), `provider-model.md` (`Requirements`) and
    `authorization.md`;
  - the risk register: RP-033, RP-034 and RP-037 closed or updated, and
    the new risks;
  - `build-vs-adopt.md`: our own workflow instead of the samples,
    `registry:2`, no ESO, polling instead of webhooks;
  - `tasks.md` "Done as" notes, and `PROGRESS.md`.
- **Files:** those docs.
- **Verify:** `docs-keeper` reports nothing out of sync; every RP touched
  names spec 022.
