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

## T008 — The API

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

## T009 — serendipd wiring

- **Do:** the catalogue's builds entry gets the installer and its
  `Requirements`; the lazy `build` handle is registered once Ready; the
  Build reconciler is registered; the uninstall clean-up is wired.
- **Files:** `cmd/serendipd/packages.go` and `main.go`.
- **Verify:** `go build ./...` and `go test ./cmd/...` pass. `serendipd`
  starts against `serendip-t018`, and `GET /v1/packages` shows the builds
  package as not installable while `Requirements` is nil.

## T010 [P] [A] — CLI, MCP and docs

- **Do:** catalogue-generated commands and tools (`serendip builds
  list|get|trigger|cancel|logs [--follow]`, and the package
  `requirements` in `packages list`); `docs/cli-and-mcp.md` regenerated.
- **Files:** `cmd/serendip/`, `mcp/`, `docs/cli-and-mcp.md`.
- **Verify:** `go test ./cmd/serendip/... ./mcp/... ./api/catalog/...`
  passes, including the docs-table check.

## T011 [P] — The console

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

## T012 [A] — In-process acceptance tests

- **Do:** `api/spec022_acceptance_test.go` for criteria 1, 3, 4, 6–8,
  10 and 11, on a fake build provider and installer.
- **Files:** `api/spec022_acceptance_test.go`.
- **Verify:** `go test ./api/ -run Spec022` passes, and `go test ./...`
  passes.

## T013 — Footprint on the simulated T470, and the stated cost

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
