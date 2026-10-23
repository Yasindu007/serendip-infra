# Tasks: Spec 020 — OpenChoreo's portal features in Serendip's console

Ordered. `[P]` = parallel-safe within its block, `[A]` = subagent-eligible
(tests or docs). Run `contract-guardian` before committing anything under
`core/` or `providers/`, and `go-reviewer` after each non-trivial Go task.

Dependency sketch:
- T001 → T002 → T003 → T004 → T005 → T006 → (T007 ∥ T008 ∥ T009 ∥ T010)
  → T011 → T012 → T013 → T014 → T015 → T016 → T017.
- T012 (the console) can start once T006's routes exist.

## T001 — Spike: environments, bindings and runtime on serendip-t018 ✅ 2026-10-22

- **Do**, on `serendip-t018`, with scratch objects removed afterwards:
  1. **A second environment:** create an OpenChoreo `Environment`
     (`staging`) on the existing `DataPlane`, plus a `DeploymentPipeline`
     development → staging. Record the CR shapes and what must exist
     first.
  2. **Promotion:** take a running Serendip Component's
     `ComponentRelease`, and create a `ReleaseBinding` for staging with the
     same `releaseName`.
     - Record: where the workload lands (namespace naming); the
       `RenderedRelease` `status.resources`; and how long to Ready.
  3. **Overrides:** set a per-environment override on the staging binding
     (replicas, an environment variable). Record the exact field paths,
     and which ones the component type's `environmentConfigs` schema
     governs.
  4. **Rollback:** patch `releaseName` to an older release. Record
     behaviour and timing.
  5. **Turning auto-deploy off:** what happens to the existing
     `development` binding. It must keep running.
  6. **Runtime reads through serendipd's own Kubernetes client:** the
     objects listed in `status.resources`, their events, pod logs with
     `follow` (through the client's streaming path), and the previous
     container's logs.
  7. **Schemas:** where the parameter and `environmentConfigs` schemas
     sit on `ComponentType` and `Trait` for the installed types, and one
     sample of each.
  8. **Dependencies:** how a Component's dependencies are declared in its
     `Workload` (the endpoint connections), for the topology view.
- **Files:** `plan.md` ("Spike findings"; decisions 2–4 amended if the
  facts differ), `providers/application/openchoreo/VERSIONS.md`.
- **Verify:** each item has observed evidence, and the scratch objects
  are gone.

## T002 — The capability interfaces and the Deployment kind ✅ 2026-10-22

- **Do:**
  - **`core/resource`:**
    - `KindDeployment`, with spec (Component, Release, Overrides) and
      status (Release, phase, endpoints);
    - the Project's pipeline (ordered Environments, or promotion paths);
    - validation.
  - **`core/reconcile`:** `ApplicationPlatformProvider` grows `Releases`,
    `Deploy`, `Undeploy`, `RuntimeObjects`, `Events`, `PodLogs`,
    `ConfigSchema`, `Pipeline` and `Topology`. The shapes follow T001's
    findings; no product names.
  - **`core/graph`:** `Deployment` is owned by its Environment, with a
    depends-on edge to the Component.
- **Files:** `core/resource/deployment.go` and its tests,
  `core/resource/orgtree.go`, `core/reconcile/application.go`, `core/graph`.
- **Verify:** `go test ./core/...`; validation is table-tested.
- **Then:** `contract-guardian`.

## T003 — Migration 0008: Applications move up to their Project ✅ 2026-10-22

- **Do:**
  - **Re-parent** every Application owned by an Environment to that
    Environment's Project.
  - **Keep things running:** for each Component, create a `Deployment`
    in its former Environment running its current release.
  - **Log every affected assignment:** each role assignment at an
    Environment scope that used to cover Applications (decision 2).
- **Files:** `core/store/sqlite/migrations/0008_environments.sql`, Go
  migration code if SQL alone can't, and tests.
- **Verify:**
  - `go test ./core/store/...`, with a pre-migration fixture.
  - Every pre-migration permission either survives or is listed (the
    plan's risk test).
  - The migration is idempotent on a migrated store.

## T004 — Authorization: actions, the Log Reader role, route rules ✅ 2026-10-22

**As built:** the actions and roles are in `core/policy/builtins.go`.
Each route's rule lands with its route (T006, T007, T009), because the
route-table test refuses a rule for a route that doesn't exist yet.
"Write at the target Environment and read on the Component" is
T006's route rule.

- **Do:**
  - Add the actions in decision 9.
  - Add the built-in role **Log Reader**.
  - Reader gets the new reads, except logs.
  - Add route-table rules for every new route. Deploying needs write at
    the target Environment *and* read on the Component.
- **Files:** `core/policy/builtins.go`, `api/authz.go`, and tests.
- **Verify:** `go test ./core/policy/ ./api/`. The route-table and
  delete-action tests cover the new routes.

## T005 — The OpenChoreo provider: environments, pipelines, deployments ✅ 2026-10-23

- **Do:**
  - **Environment** to OpenChoreo `Environment`, on the existing data
    plane.
  - **The Project's pipeline** to a `DeploymentPipeline`.
  - **Deploy / Undeploy:** a `ReleaseBinding` with its overrides.
  - **Releases:** list `ComponentRelease`s, and generate one when needed.
  - **Stop auto-deploying** to the hard-coded `development` (the
    migration's Deployments take over).
  - **The project-level binding:** a `ProjectReleaseBinding` per
    (Application, Environment), created with the first Deployment there
    and removed with the last (spike finding 1).
  - **One `DeploymentPipeline` per Project.**
  - **Creating a release** when auto-deploy is off (spike finding 5).
  - Record the pins and CR shapes in `VERSIONS.md`.
- **Files:** `providers/application/openchoreo/{environments,deployments,releases}.go`
  and tests, `names.go`, `VERSIONS.md`.
- **Verify:** `go test ./providers/...` against the fake Kubernetes
  server, including names within limits and idempotent re-apply.
- **Then:** `contract-guardian`.

## T006 — The Deployment reconciler and API ✅ 2026-10-23

- **Do:**
  - **The reconciler:** drives `Deploy`/`Undeploy` and writes conditions.
    Undeploy is a delete, so it goes through Approvals (the store's intent
    check).
  - **API routes:** deployments under an Environment; promote, which
    creates or updates the target Deployment with the source's release
    (never its overrides); rollback; restart; and the pipeline.
  - **Pipeline enforcement:** skipping an environment is refused (400).
  - **Audit** on every step.
- **Files:** `core/reconcile/deployment.go`, `api/deployment_handler.go`,
  `api/authz.go`, `cmd/serendipd/wiring.go`, and tests.
- **Verify:** `go test ./...`. Table tests for promotion paths (allowed
  and skipped), rollback, overrides not carried over, an agent's
  undeploy waiting for a person, and audit rows.
- **Done as:**
  - `/v1/deployments` (an Environment owns each Deployment) plus
    `POST /v1/deployments/{id}/promote {to}`. Rollback is a `PUT` naming a
    release from `status.history` (the last 10 releases).
  - The pipeline lives on the Project (`PUT /v1/projects/{id}`), checked
    to name only its own Environments.
  - Deploying also needs reading the Component. Promoting needs writing
    at the target Environment.
  - The handler's extra checks join the audit row's policy evidence.
  - `allowedAt` now fails closed with no decider; the authorizer attaches
    one on every route.
  - **Restart is deferred:** it needs a provider call (a rollout restart
    on the data plane), and no acceptance criterion depends on it. It
    lands with T007's runtime work or later.

## T007 [P] — The runtime view: tree, events, pod logs ✅ 2026-10-23

- **Do:**
  - **Kubernetes client additions:** a raw read that keeps every field
    (Events lose theirs through `Object`), and a stream method with no
    request timeout, for following logs (spike finding 6).
  - Provider and API for runtime objects and events.
  - **Pod logs:** recent lines, plus `follow=true` streaming chunked
    NDJSON (decision 8).
  - **Caps:** about 3 streams per session and 20 per server, an idle
    timeout, and about a 10-minute maximum.
- **Files:** `providers/application/openchoreo/runtime.go`,
  `api/runtime_handler.go`, and tests.
- **Verify:** `go test`. Covers stream caps, the stream ending on client
  close, and logs needing `components/logs/read` (a Reader is refused).
- **Done as:**
  - **Routes:** `GET /v1/deployments/{id}/runtime`, `/events?since=` and
    `/logs`.
    - Logs take `tailLines` (at most 1000), `sinceSeconds`, `previous`
      and `follow`.
    - Recent lines come back as `{"lines": [...], "truncated": bool}`:
      the newest 5000 across instances (within 4 MiB), oldest first.
    - `follow=true` streams NDJSON, ending with `{"end": "idle" |
      "maxDuration" | "ended" | "error"}`. A platform failure part-way
      is reported, so a partial answer never looks complete.
  - **Caps:** 3 streams per caller and 20 per server (beyond that, 429
    with `Retry-After`). A stream ends after 5 minutes idle or 10
    minutes in all. Each stream sets its own write deadline past the
    server's 60 s.
  - **The tree:** the rendered objects with their health, then the
    pods. A pod's message names a waiting reason such as
    `CrashLoopBackOff`, and its restarts.
  - **Events:** asked for by exact object name, because the namespace
    holds the whole project's: the rendered objects, the pods, and their
    ReplicaSets. Newest first, at most 200.
  - **Logs:** at most 10 instances are merged, and lines are cut at
    16 KiB.
  - **The client:** `kubernetes.Client` gains `GetRaw` (capped at 8 MiB)
    and `Stream`.
  - **Checked live:** the log format and the previous-container 400.
    The full read path runs in T015.

## T008 [P] — Configuration from schemas ✅ 2026-10-23

- **Do:**
  - `ConfigSchema` for component types and traits.
  - The API serves the schemas.
  - Component trait parameters and Deployment overrides are validated
    against them, refused with the schema's message.
- **Files:** `providers/application/openchoreo/schema.go`, a stdlib JSON
  Schema subset validator (only the keywords T001 finds in use), the API
  and tests.
- **Verify:** `go test`. Table tests of valid and invalid values with
  the schema's messages, over T001's real sample schemas.
- **Done as:**
  - **`core/jsonschema`:**
    - It checks `type`, `properties`, `required`, `additionalProperties`,
      `enum`, the number, length and item limits, `pattern`, `items`, and
      local `$ref`/`$defs`.
    - It also reads OpenAPI 3.0's `nullable` and boolean
      `exclusiveMinimum`/`exclusiveMaximum`.
    - Annotations are ignored. Any other keyword, a mistyped keyword
      value, a `null` schema, or a `$ref` outside `$defs`/`definitions`
      makes the schema unsupported (fail closed). Such a schema is
      refused with a 502.
    - Unknown settings are refused where the schema lists `properties`,
      because the platform would prune them.
    - It's tested over serendip-service's real schema (`testdata/`).
  - **`GET /v1/components/{id}/schemas`** (Component read) answers
    `{environment, traits}`, read live from the component type.
  - **Deployment writes** check `overrides.config` and
    `overrides.traits` when they change.
    - Problems are listed with their paths, for example
      `overrides.config.replicas: must be at least 0`.
    - A trait instance the Component doesn't have is refused.
    - With no platform, or one not ready, a configuration change is a
      503. Promotions and release changes go through, because they leave
      the configuration as it is.
  - **Component trait parameters:** Serendip's Components have no
    trait instances yet (no ClusterTraits are installed, finding 7), so
    the only forms are the per-environment configuration. A trait's
    schema joins `traits` once a Component can carry one.

## T009 [P] — Topology and the platform view ✅ 2026-10-23

- **Do:**
  - **`ComponentSpec` gains `dependencies`:** the Component and the
    endpoint it calls. They map to the Workload's
    `dependencies.endpoints`, with the address in an environment variable
    (spike finding 8).
  - Declared dependencies per Component (what it calls, and what calls
    it), and per Application.
  - **Read-only platform view:** data planes, environments, the pipeline,
    component types and traits.
- **Files:** `providers/application/openchoreo/{topology,platform}.go`,
  the API and tests.
- **Verify:** `go test`.
- **Done as:**
  - **`ComponentSpec.dependencies`:** `[{component, port, env}]`, at most
    50 (the platform's limit).
    - The Component handler checks each target: a Component of the same
      Application, which has that port, and which the caller may read.
    - The server derives the `depends-on` edges.
    - A port another Component calls can't be removed, and a Component
      others call can't be deleted (409). A Component's Application is
      fixed once created.
  - **The provider** writes them to the Workload and into each release
    as `project`-visible connections, with the address in `env` (spike
    finding 8). A Component without dependencies keeps its release
    names.
  - **Within one Application only,** for now. An Application is one
    platform project. Calling across Applications would need the
    target's endpoints opened namespace-wide, which weakens isolation for
    every Component. That's a later, explicit choice.
  - **Routes:**
    - `GET /v1/components/{id}/dependencies` answers `{calls,
      calledBy}`.
    - `GET /v1/applications/{id}/topology` answers `{components,
      dependencies}`, declared rather than live, and checked per item
      with `allowedAt`.
    - `GET /v1/platform` (`serendip/platform/read` at `/`) answers data
      planes, component types (with `allowedTraits`) and traits, with
      their schemas.
  - **Environments and the pipeline** come from their own routes. The
    console (T012) puts the platform page together.

## T010 [P] — The package framework ✅ 2026-10-23

- **Do:**
  - **A `ProviderInstance` reconciler:** install through the provider
    registry's package installer (ADR 0010's helm path), wait for
    readiness, and write conditions. Removal goes through an Approval.
  - **Optional capabilities `build` and `observability`** report `not
    installed`, `installing` or `ready` in `GET /v1/readiness`.
  - **No real package yet:** a test package proves the mechanics; 022
    and 023 bring builds and observability.
- **Files:** `core/reconcile/providerinstance.go`, `core/registry`,
  `api/readiness_handler.go`, `cmd/serendipd/wiring.go`, and tests.
- **Verify:** `go test`. Covers install to ready, removal through an
  Approval, readiness states, and an agent's removal waiting for a
  person.
- **Then:** `contract-guardian` and `go-reviewer` on T005–T010, then
  commit. (Each task was reviewed and committed on its own.)
- **Done as:**
  - **A package** is a `ProviderInstance`: `capabilityId` is the
    capability's name and `providerId` the package's provider name, with
    no `environmentId` (platform-wide).
  - **`core/reconcile`:** a `PackageInstaller` interface (idempotent
    `Install` and `Uninstall`), a `Catalogue`, the
    `ProviderInstanceReconciler` and the `UninstallCleanup` approval
    clean-up. It reports readiness through a function, so it doesn't
    import the registry.
  - **The catalogue lives in `cmd/serendipd`,** because it names
    products. It declares `openchoreo-workflow-plane` (build) and
    `openchoreo-observability-plane` (observability), with no installer
    yet. 022 and 023 add them on ADR 0010's Helm path.
  - **Readiness** (`GET /v1/readiness`) shows `Unavailable`,
    `NotInstalled`, `Installing`, `Ready` or `InstallFailed` per
    optional capability.
  - **The API:** `GET /v1/packages` lists the packages and whether each
    is available. A ProviderInstance must name an offered, available
    package, one per capability (400 otherwise). Install and remove need
    `provider-instances` write and delete at `/`, so a Project's
    Contributor can't. Removing goes through an Approval, and an agent's
    waits for a person.
  - **After review:**
    - Only the oldest instance of a capability installs; others are
      `Duplicate` and leave readiness alone.
    - Which package an instance installs can't change.
    - An invalid instance reports its capability invalid.
    - On restart, an installed package reads `Installing` ("starting"),
      not `NotInstalled`.
    - Installer error text stays in the server log.
    - The uninstall clean-up survives a damaged `LastSpec`, and doesn't
      report `NotInstalled` while another instance provides the
      capability.
  - **A test package** (`core/reconcile/providerinstance_test.go`)
    proves install to ready, failure, not ready, and uninstall.


## T011 — The CLI and MCP surfaces ✅ 2026-10-23

- **Do:**
  - Add a catalogue entry for every new route: CLI command and MCP tool,
    or excluded with a reason (log follow on MCP).
  - Add `serendip … logs --follow` streaming to the CLI.
  - Regenerate `docs/cli-and-mcp.md`.
- **Files:** `api/catalog/operations.go`, `cmd/serendip/` (streaming),
  `docs/cli-and-mcp.md`, and tests.
- **Verify:** `TestCatalogueCoversEveryRoute`,
  `TestReferenceListsEveryOperation`, and a CLI follow test.
- **Done as:**
  - **The catalogue entries** landed with each route (T006–T010),
    because the parity test refuses a route without one.
  - **`catalog.Param.CLIOnly`** leaves a parameter off MCP, with its
    reason. `Operation.ForMCP()` drops such parameters from both the
    tool's schema and its requests. `deployments_logs`'s `follow` is the
    first ("an MCP tool answers once").
  - **`serendip deployments logs <id> --follow`** (`cmd/serendip/follow.go`)
    streams without the client's 30-second limit.
    - It prints `time instance line`, or the raw NDJSON with `--json`.
    - It explains the server's end reason. Ctrl-C exits 0.
    - A stream cut off, or ended by `error`, exits 1 ("some lines may be
      missing").
  - **`docs/cli-and-mcp.md`** gains a Logs section, and the tables are
    regenerated.

## T012 — The console ✅ 2026-10-23

Spec 020 authorises the UI work.

- **Do**, in the existing design system (`ui/DESIGN.md`, the design
  rules):
  - **Component pages:**
    - **a Deploy tab:** the environment pipeline, each environment's
      release and state, promote, rollback and restart, and overrides;
    - **a Runtime tab:** the tree, events, and logs with follow.
  - **A Configuration form** generated from schemas.
  - **An Application Topology view.**
  - **A Platform page.**
  - **A Packages page,** with each package's cost stated.
  - **The "unavailable", "installing" and "not ready" treatment** for
    Build and Observability sections.
  - Every action shows its "Same request through the API".
- **Files:** `ui/src/...` and tests.
- **Verify:** `pnpm test`, `pnpm build`, and the size, contrast and
  design checks.
- **Done as:**
  - **The Component page has tabs** (`?tab=`, so each can be linked):
    Overview, Deploy, Runtime, Builds and Observability. The Overview
    gains the declared dependencies (calls and called by) and a link to
    the topology.
  - **Deploy:** the Project's pipeline in order, each Environment's
    state, running release, a release it's moving to, overrides and
    endpoints.
    - **Deploy** only where new releases enter (anywhere without a
      pipeline). **Promote** to each next Environment, checked against
      write there. **Roll back** to a release from `status.history`
      (a `PUT`). **Undeploy** through `DeleteConfirm` and an Approval.
    - **Restart** is shown disabled, "not available yet" (deferred in
      T006).
    - An Application outside a Project says it has nowhere to deploy.
      Creating an Application can now choose its Project.
  - **Configure** (`…/deployments/{id}/configure`): environment
    variables, plus the settings and each trait's, as a form generated
    from `GET /v1/components/{id}/schemas`.
    - Stored keys the schema doesn't describe are kept.
    - If the schemas can't be read, only variables can change, and the
      rest is sent back unchanged (RP-032).
  - **Runtime:** objects with their health, events, and logs.
    - Logs need the Log Reader permission. Without it, the console says
      so and never asks.
    - **Follow** streams NDJSON through `fetch` (`apiStream`). The
      timeout covers only the wait for headers. It explains how a
      stream ended, and reconnects after the 10-minute maximum without
      repeating lines.
  - **Topology** (`/applications/{id}/topology`): a drawn diagram, plus
    the same calls as a table. It says it shows declared calls, not
    traffic.
  - **Platform** (`/platform`): where workloads run, the component types
    and traits with their settings, and each Project's Environments with
    where each promotes to.
  - **Packages** (`/packages`): each package's state, description and
    cost, with install (`POST /v1/provider-instances`) and removal
    through an Approval, which warns that stored data is deleted.
    - The API states no measured cost, and neither package can be
      installed yet, so cost reads "not measured yet" (RP-034). Before
      022 or 023 ships an installer, the catalogue must carry the
      numbers.
  - **Greyed out:** `AvailabilityGate` shows Builds, Observability and
    the topology's live traffic as not available yet, not installed,
    installing or not ready, from `GET /v1/readiness`, naming the
    package. The Overview's capability table uses the same words.
  - **New shared components,** documented in `ui/DESIGN.md`: `Tabs`,
    `EnvironmentPipeline`, `ActionConfirm`, `SchemaForm`,
    `TextAreaField`, `LogView`, `TopologyDiagram` and
    `AvailabilityGate`.
    - `FormFailure` moved to `components/`.
    - `Guarded` uses `useId`, since one page now repeats an action per
      Environment.
    - `DeleteConfirm` takes a noun and a note.
  - **Tests:** 27 new (`src/lib/platform.test.ts` and
    `src/screens/platform.test.tsx`), 109 in all.
    - **Fixed on the way:** a test (`approvals.test.tsx`) left an
      unhandled error that made `pnpm test` exit 1 on `main`.
  - **Checked:**
    - JS is 94.5 KiB gz of the 160 KiB budget.
    - Contrast passes in both themes. Six pairs on the sunken surface
      were added for the greyed-out sections and logs.
    - No raw HTML and no inline styles. Log text is shown as text.
  - **Not done here:** editing dependencies in the console (declared
    through the API and CLI). It isn't in T012's list.
  - **Seen live in T015,** with a browser against `serendip-t018`.

## T013 [A] — Acceptance tests ✅ 2026-10-23

- **Do:** in-process tests for spec acceptance criteria 1–7, 10 and 11,
  against a fake platform provider.
- **Verify:** `go test -race ./...` is green.
- **Done as:** `api/spec020_acceptance_test.go`, one
  `TestSpec020Criterion<N>_…` per criterion. It runs the whole API stack
  and the real Deployment and ProviderInstance reconcilers, called as the
  engine would. `fakePlatform020` stands in for OpenChoreo: it remembers
  the release and overrides it runs in each Environment, and reports them
  back on the runtime view.
  - **1:** the runtime tree, events and recent logs. Following runs over a
    real connection (`httptest.NewServer`): lines arrive as they're
    written, then `{"end":"ended"}`.
  - **2:** deploy to development, then promote to staging and production.
    - Production has its own replicas and variable; development doesn't.
    - A new release moves along, and each Environment keeps its own
      overrides.
    - Skipping staging is refused.
    - Each Environment shows its release and Ready.
    - Rollback restores the earlier release and keeps production's
      overrides.
    - All 7 changes are audited as the person. A route's decision names
      the role and scope; each promotion also names its write check at
      the target.
  - **3:** a Reader and a Reader agent are refused (403 naming the
    action). A Contributor agent promotes at once. The audit row names
    the agent, its sponsor and the decision.
  - **4:** the trait's schema from `GET …/schemas`. 9 is refused with
    "must be at most 5", and 4 reaches the running workload.
  - **5:** topology and both sides of `…/dependencies`, from a dependency
    declared through the API.
  - **6:** the platform, environments and pipeline. `/v1/platform` takes
    no writes.
  - **7:** readiness reports `NotInstalled` for build and `Unavailable`
    for observability. Deploying and the runtime view still work. An
    install reads `Installing`, then Ready. The greyed-out sections are
    the console's tests (T012).
  - **10:** logs need `serendip/components/logs/read`. Package install
    and removal are decided at `/`, and removal records an Approval.
    That every route has a rule stays `TestEveryRouteHasARule`.
  - **11:** every spec 020 route is a CLI command, and an MCP tool or
    excluded with a reason. `follow` is CLI-only.
  - **Test support:** `authzWorld` gained an optional `registry`, and
    `newDeployWorldWith` sets it.

## T014 — Footprint ✅ 2026-10-23

- **Do:** the edge-1 harness on the simulated T470, compared with spec
  019's run.
- **Files:** `profiles/edge-1/bench/results/2026-10-xx-spec020-*/`.
- **Verify:** no regression beyond tolerance, and rule 4 passes.
- **Done as:** `profiles/edge-1/bench/results/2026-10-23-spec020-simulated-t470/`
  (its README has the full reading).
  - **The harness deploys the spec 020 way now** (`cmd/bench-edge1`).
    Since T005 a Component only becomes Released, so the old workload
    would never serve. It creates a Project, an Environment, an
    Application in the Project, the Components and a Deployment of each.
    "Serving" is the Deployment's first endpoint. Clean-up goes
    Deployments first, then the Application, the Environment and the
    Project.
  - **`kind-t470-setup.sh` takes `KIND_CONFIG`,** because
    `serendip-t018` holds host port 30080. The harness never uses the
    host port.
  - **Against spec 019's run, Kubernetes system fails** (742 → 859 MiB).
    That's the host: a baseline of spec 021's final code (`cecc037`),
    run the same day on the same host, measures 813 MiB. Every
    control-plane pod grew alike with unchanged CPU, and cold starts
    were slower all day.
  - **Against that same-day baseline, every group passes.**
    - `serendipd` idles at 35.6 MiB (the baseline is 34.4). Rule 4
      passes: 0.12% CPU with three console tabs.
    - The 16 GB headroom is 13%.
  - **Create → Serving rose from 16–24 s to 70 s** for the first deploy
    into a new Environment (RP-035). The Deployment reads Progressing
    for about 67 s. Up to 30 s of that may be the engine's resync wait
    after its fast re-checks end. The suspected Ready-before-reachable
    gap didn't occur.


## T015 — Real cluster run

- **Do**, on `serendip-t018`, with the migration run on a copy of the
  spec 019/021 store:
  - **A pipeline:** development → staging → production.
  - **Deploy and promote,** with a production override (replicas or an
    environment variable) development doesn't have.
  - **Rollback.**
  - **The runtime view,** with live log following in the console.
  - **A schema form** change reconciled to the workload.
  - **Topology.**
  - **The platform view.**
  - **Greyed-out Build and Observability sections.**
  - **A Reader refused promotion.**
  - **Claude Code through `serendip mcp`** promotes; its undeploy waits
    for a person.
- **Files:** `specs/020-openchoreo-console-features/verification.md`.
- **Verify:** every acceptance criterion has observed evidence.

## T016 — Final reviews

- **Do:** run `contract-guardian`, `go-reviewer`, a security review scoped
  to the spec diff, and a UI review against the design rules. Fix the
  findings.
- **Verify:** all pass, and the gates are green.

## T017 [A] — Docs, ADR, close

- **Do:**
  - Write ADR 0019 (the environment model; direct Kubernetes access).
  - Update `application-platform.md`, `resource-model.md`,
    `authorization.md` and `ai-operations.md`.
  - Update the risk register, `docs/build-vs-adopt.md` and
    `docs/PROGRESS.md` (next: 022).
- **Verify:** `docs-keeper` reports the docs in sync.
