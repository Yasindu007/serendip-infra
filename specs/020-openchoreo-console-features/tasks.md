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

## T004 — Authorization: actions, the Log Reader role, route rules

- **Do:**
  - Add the actions in decision 9.
  - Add the built-in role **Log Reader**.
  - Reader gets the new reads, except logs.
  - Add route-table rules for every new route. Deploying needs write at
    the target Environment *and* read on the Component.
- **Files:** `core/policy/builtins.go`, `api/authz.go`, and tests.
- **Verify:** `go test ./core/policy/ ./api/`. The route-table and
  delete-action tests cover the new routes.

## T005 — The OpenChoreo provider: environments, pipelines, deployments

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

## T006 — The Deployment reconciler and API

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

## T007 [P] — The runtime view: tree, events, pod logs

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

## T008 [P] — Configuration from schemas

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

## T009 [P] — Topology and the platform view

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

## T010 [P] — The package framework

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
  commit.

## T011 — The CLI and MCP surfaces

- **Do:**
  - Add a catalogue entry for every new route: CLI command and MCP tool,
    or excluded with a reason (log follow on MCP).
  - Add `serendip … logs --follow` streaming to the CLI.
  - Regenerate `docs/cli-and-mcp.md`.
- **Files:** `api/catalog/operations.go`, `cmd/serendip/` (streaming),
  `docs/cli-and-mcp.md`, and tests.
- **Verify:** `TestCatalogueCoversEveryRoute`,
  `TestReferenceListsEveryOperation`, and a CLI follow test.

## T012 — The console

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

## T013 [A] — Acceptance tests

- **Do:** in-process tests for spec acceptance criteria 1–7, 10 and 11,
  against a fake platform provider.
- **Verify:** `go test -race ./...` is green.

## T014 — Footprint

- **Do:** the edge-1 harness on the simulated T470, compared with spec
  019's run.
- **Files:** `profiles/edge-1/bench/results/2026-10-xx-spec020-*/`.
- **Verify:** no regression beyond tolerance, and rule 4 passes.

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
