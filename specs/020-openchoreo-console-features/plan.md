# Plan 020: OpenChoreo's portal features in Serendip's own console

Status: Draft, awaiting approval. Updated 2026-10-22 after spec 021
(see "What changed since spec 021"). Spec: `spec.md` (draft, 2026-10-20).

## What changed since spec 021 (2026-10-22)

- **Parity is mechanical now.** Every new route enters `api/catalog` as a
  CLI command and an MCP tool, or is excluded with a reason, and
  `TestCatalogueCoversEveryRoute` fails otherwise. Spec acceptance
  criterion 11 is met by construction and checked by that test.
  `docs/cli-and-mcp.md` is regenerated.
- **AI callers already exist:** an agent (spec 018) or a person's
  assistant (`Via`, spec 021). Both meet `Principal.guarded()`:
  - **Undeploying and removing a package** are deletes, so through MCP
    they wait for a person.
  - **Deploy, promote and rollback are writes,** decided by roles for AI
    and people alike (spec criterion 3).
- **Live log following is a stream; an MCP tool answers once** (decision
  8, amended):
  - **The CLI** gets `--follow`, printing the NDJSON stream line by line.
  - **MCP** gets the non-following read (recent lines, `since` and
    `tailLines`). `follow=true` is excluded from MCP with that reason.
- **The ADR for the environment model is 0019.** 0017 and 0018 went to
  spec 021.
- **Today's mapping, found while re-reading the code:**
  - Each Serendip Application is an OpenChoreo Project (`app-<hash>`).
  - Each Component auto-deploys to one hard-coded OpenChoreo environment,
    `development` (spec 011 decision 5, `names.go`).
  - **Serendip's Environments aren't mapped to anything in OpenChoreo
    yet.** Decision 2 has to add that mapping, not just re-parent
    Applications.
  - **Nothing enforces which kind may own an Application,** so the
    re-parenting is a data migration of owner references (`0008`, the
    next free number), plus the provider change.

## What the research found (OpenChoreo 1.3.1, 2026-10-20)

This is the basis for every decision below. It was verified from source
unless marked inferred.

- **`openchoreo-api` runs anonymous today.** Spec 010 installs the control
  plane with `security.enabled=false`. That makes `openchoreo-api` accept
  every request with **no authentication and no authorization**. It's a
  ClusterIP Service on port 8080, and the chart ships no NetworkPolicy by
  default.
  - So **any pod in the cluster**, including any workload a user deploys,
    **can create, change or delete OpenChoreo resources**. That bypasses
    Serendip's RBAC, approvals and audit.
  - Serendip itself never calls this API. Webhooks and the cluster gateway
    aren't affected by the flag.
  - Recorded as **RP-018 (High)**. Decision 1 fixes it first.
- **Promotion has no server-side logic.**
  - **Promoting** means creating a `ReleaseBinding` in the target
    environment with the same `releaseName`.
  - **Rolling back** means patching a binding's `releaseName` to an older
    `ComponentRelease`.
  - **Auto-deploy** binds only the pipeline's first environment, and
    overwrites its `releaseName`.
  - **`generate-release`** creates one `ComponentRelease`, named
    `component-YYYYMMDD-n`.
  - **There's no pipeline enforcement.** Nothing upstream stops a
    promotion that skips an environment.
- **Schemas are in the CRs.** Component types and traits carry OpenAPI v3
  schemas in `spec.parameters` and `spec.environmentConfigs`. The API's
  `/schema` endpoint returns only the environment configs.
- **`openchoreo-api` authentication:**
  - **Tokens:** any OIDC issuer; a client's `service_account` subject is
    bound through a `ClusterAuthzRoleBinding`. There's no static-token
    mode.
  - **Routing:** every call goes through the cluster gateway, even on a
    single cluster.
- **Runtime discovery:** a `RenderedRelease`'s `status.resources` lists the
  Kubernetes objects behind it. In a single cluster, those objects, their
  events and their pod logs can be read straight from the Kubernetes API.
- **The workflow plane** bundles Argo Workflows, but also needs the
  **External Secrets Operator**, a `ClusterSecretStore`, and a **container
  registry** for built images.
- **The observability plane chart** installs only the `observer`.
  - OpenSearch, Fluent Bit and Prometheus are separate modules or charts.
  - **Their memory isn't documented upstream.** OpenSearch is a JVM
    service.
- **No upstream statement** says "OpenChoreo without Backstage is
  supported". The chart does have `backstage.enabled=false`, and the API
  and CLI are first-class.

## Decisions

1. **Close RP-018 first.** **Done** before spec 020, at the user's
   request (`96f285d`). The NetworkPolicy is in the provider's platform
   defaults, and was verified live on kind. What follows is the rationale
   as decided.
   - Serendip doesn't use `openchoreo-api` (decision 3), so it shouldn't
     answer anyone.
   - **The fix:** a NetworkPolicy, applied by the OpenChoreo provider's
     foundations pass, that denies all ingress to `openchoreo-api` except
     from OpenChoreo's own control-plane components, if any need it. The
     T001 spike checks which do.
   - **K3s enforces NetworkPolicy** with its built-in controller. kind's
     default CNI, kindnet, does too in recent versions; T001 verifies it on
     our kind node image.
   - **If neither enforces reliably,** the fallback is scaling
     `openchoreo-api` to zero, since nothing in Serendip needs it.
   - **Turning on its OIDC security instead was rejected for now.** It
     would need an issuer, a client, and a role binding for a credential
     nothing uses.

2. **Environments, Azure-style** (spec open question 1, the user's
   proposal).
   - **The mapping:** a **Project is the subscription**, and an
     **Environment is the resource group** you deploy into.
     - Permission inside your own environment lets you deploy there.
     - Another project is another subscription: no reach.
   - **The resource model changes** so this is true:
     - **Applications move up to their Project:** Project → Application →
       Component, as today minus the Environment level.
     - **Environments stay under the Project,** as siblings of
       Applications.
     - **A new kind, `Deployment`:** one Component, in one Environment,
       running one release.
       - **It's owned by the Environment,** so the environment scope
         governs it.
       - **It references the Component** with a `depends-on` edge.
     - **Releases** (immutable snapshots of a Component's workload) are
       read-only status from the provider, not a writable kind.
     - **Per-environment overrides** live on the `Deployment` (its spec
       gains `overrides`: environment variables, replicas, resource
       limits, and the parameters the component type marks as
       per-environment, validated against its `environmentConfigs`
       schema). They map to the ReleaseBinding's overrides. Promotion
       copies the release, not the source environment's overrides.
   - **Authorization falls out of spec 017's inheritance unchanged:**
     - **Deploying or promoting into staging** writes a `Deployment` owned
       by `Environment/staging`. That needs `deployments/write` *there*,
       plus read on the Component being deployed: "read the source, write
       the target", like Azure's cross-resource-group operations.
     - **A Contributor on `Environment/dev`** can deploy to dev but not to
       prod.
     - **Promotion only moves along the Project's own pipeline.** A
       pipeline never spans projects (subscriptions).
     - **Undeploying** (deleting a `Deployment`) is a delete, so it goes
       through an Approval (spec 019). Agents' undeploys wait for a
       person.
   - **The mapping to OpenChoreo** (settled by T001 and T002):
     - **A Serendip Environment becomes an OpenChoreo `Environment`,** on
       the existing data plane.
     - **A Project's promotion order** (its spec gains a `pipeline`: the
       Environment IDs in order, or promotion paths) **becomes an
       OpenChoreo `DeploymentPipeline`.**
     - **A `Deployment` becomes a `ReleaseBinding`,** with its overrides.
     - **Components stop auto-deploying** to the hard-coded `development`.
       The first Deployment in the pipeline's first environment replaces
       it, and existing Components get one in their former Environment
       during migration, so nothing running stops.
   - **Pipeline enforcement is Serendip's.** Upstream has none, so
     Serendip checks that a promotion follows the pipeline's
     `promotionPaths`. Skipping an environment is refused (400).
   - **Migration cost (honest):**
     - Existing Applications sit under an Environment today. Migration
       `0008` re-parents each one to that Environment's Project.
     - A role assignment at an Environment used to cover the
       Applications under it. After migration it covers only
       deployments into that environment.
     - **People who could edit Applications through an Environment-scope
       assignment lose that.** The migration lists each affected
       assignment in its log, and the upgrade note says so.
     - Everything is local and pre-release today, so the cost is small,
       but it's real and stated.
   - **Rejected: one Application per Environment, linked by promotion.**
     It fights OpenChoreo's model (one component, many bindings),
     duplicates configuration across copies, and makes "the same app in
     prod" a naming convention instead of a fact.

3. **How `serendipd` reaches OpenChoreo: directly, through the Kubernetes
   API.** Not through `openchoreo-api` (spec open question 2).
   - **Security:**
     - **No new credential.** `serendipd` already holds the kubeconfig it
       uses for everything. `openchoreo-api` would add an OIDC client,
       its secret, and an OpenChoreo role binding: another
       administrator-grade credential to hold and rotate (compare
       RP-004).
     - **One authorization layer.** Serendip's RBAC decides every
       request, instead of two policy engines that can disagree.
     - **`openchoreo-api` can stay locked down** (decision 1).
   - **Performance:**
     - **One hop.** Promotion is a single CR write, schemas are read from
       the CRs, and runtime objects come from `RenderedRelease.status.resources`.
     - Going through `openchoreo-api` adds its HTTP hop *and* its cluster
       gateway on every call, plus the port-forward tunnel `serendipd`
       would need from outside the cluster (about 7–40 ms per call,
       measured for ThunderID in spec 018).
   - **What we give up:**
     - **Remote data planes.** The API reaches them through the cluster
       gateway. Every current profile runs the data plane in the same
       cluster, so this waits for the multi-cluster profiles, which can
       add a gateway-backed provider behind the same interface.
     - **A few conveniences to re-implement:** release naming, and the
       environment-config schema extraction. Both are small.
   - **Where it lives:** in the OpenChoreo provider
     (`providers/application/openchoreo`). `core/` sees only
     capability-shaped interfaces (decision 4).

4. **Capability interfaces in `core/`** (constitution 1). Their shape is
   settled in T002:
   - **`ApplicationPlatformProvider` grows:**
     - `Releases(component)`;
     - `Deploy(component, environment, release)`, the same call for
       promote and rollback;
     - `Undeploy`;
     - `RuntimeObjects(component, environment)`;
     - `Events(...)`;
     - `PodLogs(..., follow)`;
     - `ConfigSchema(componentType or trait)`;
     - `Pipeline(project)`.
   - **A new `BuildProvider`** (optional capability `build`): `Trigger`,
     `Runs`, `RunLogs`, `SetAutoBuild`.
   - **The `ObservabilityProvider`** (capability `observability`, its
     first real provider): `QueryLogs`, `QueryMetrics`, `Traces`, `Spans`,
     `AlertRules`, `FiringAlerts`, `Traffic`.
   - **No OpenChoreo, Argo, OpenSearch or Prometheus names** in `core/`.

5. **Packages reuse `ProviderInstance`.** It already exists in the model
   ("this provider, for this capability, installed") and is unused, so no
   new kind (the architecture-change guardrails).
   - **Installing** a package creates a `ProviderInstance`, for example
     capability `build` with provider `openchoreo-workflow-plane`.
   - **A new `ProviderInstance` reconciler** runs the Helm install
     (ADR 0010's path), waits for readiness, and reports conditions.
   - **Removing** is a DELETE, so **it goes through an Approval**
     automatically (spec 019). The reconciler's tear-down uninstalls the
     charts. Stored data, like OpenSearch indices, is deleted with them,
     and the console says so before the delete.
   - **Availability:** each optional capability reports readiness through
     the existing `GET /v1/readiness` (`ready`, `installing`, `not
     installed`). The console greys out features from that.
   - **Authorization:** installing or removing is
     `serendip/provider-instances/write` or `/delete` at `/`. Owner has it;
     Contributor doesn't need to.

6. **The builds package's prerequisites** are installed by the package
   itself, as its own components:
   - **External Secrets Operator,** with a `ClusterSecretStore` backed by
     the Secrets capability (OpenBao), so Git credentials stay Secrets;
   - **a small in-cluster registry** (`registry:2`, pinned) for built
     images;
   - **the workflow-plane chart.**

   T001's spike pins versions and measures each on the simulated T470.

7. **The observability package (OpenSearch and Prometheus) is heavy, and
   honest about it.**
   - **What it installs:** the observer chart, plus single-node OpenSearch
     (JVM heap pinned small, for example 512 MiB to 1 GiB), Fluent Bit,
     the OpenTelemetry Collector, and Prometheus with short retention.
   - **Measured by T001 on the simulated T470,** and stated on the install
     screen.
   - **If it doesn't fit a 16 GB machine beside edge-1,** the console says
     "not recommended on this machine" with the numbers, and still lets an
     Owner install it. The lighter edge-1 backend is a later spec.

8. **Live log following streams with `fetch`, not `EventSource`.** (CLI
   and MCP: see "What changed since spec 021".)
   - The console must send `X-Serendip-Csrf: 1` on cookie requests
     (ADR 0013), and `EventSource` can't set headers.
   - So `GET .../logs?follow=true` answers chunked NDJSON, and the
     console reads it through `fetch` with a `ReadableStream`. That's
     same-origin, so `connect-src 'self'` already allows it.
   - **The stream ends** when the browser closes it, after an idle
     timeout, or after a maximum duration (about 10 minutes). Reconnecting
     is the console's job.

9. **Authorization actions** (spec 017's naming):

   | Action | What it allows |
   |---|---|
   | `serendip/deployments/{read,write,delete}` | deployments, at the Environment |
   | `serendip/components/runtime/read` | the resource tree and events |
   | `serendip/components/logs/read` | pod logs and log search; its own action, since logs can hold sensitive data |
   | `serendip/builds/{read,write}` | build runs and triggering builds |
   | `serendip/observability/read` | metrics, traces and alerts |
   | `serendip/observability/alerts/write` | alert rules |
   | `serendip/platform/read` | the platform view |

   - **Built-in roles:** Reader gets the `*/read` actions *except* logs.
     That's deliberate: Reader stays safe for auditors. A new built-in
     role, **Log Reader**, adds logs.
   - **Agents:** spec 018's guardrails apply unchanged. Undeploying is a
     delete, so it needs an Approval.

10. **Delivery: one design, three specs** (spec open question 5).
    **Agreed by the user, 2026-10-20:**
    - **020, this spec, narrowed:**
      - the RP-018 fix;
      - the environment model and migration;
      - deployments and promotion;
      - the runtime view (tree, events, pod logs);
      - schema-driven configuration;
      - dependencies and topology;
      - the platform view;
      - the package framework (`ProviderInstance` reconciler and
        availability), plus the greyed-out console sections.
    - **021 (done before 020): Serendip's own MCP server and CLI** over
      Serendip's API. The user's order, so 020's new actions land in the
      console, CLI and MCP together.
    - **022: the builds package and its features.**
    - **023: the observability package and its features.**

    Each is shippable and testable alone. 022 and 023 then plug into the
    framework 020 builds. One spec for all three would be about 30 tasks,
    with two heavy installs on the critical path.

## Feasibility (honest)

- **Feasible with what's installed (020):**
  - **Promotion:** plain CR writes.
  - **Schemas:** in the CRs.
  - **Runtime objects:** listed in `RenderedRelease` status.
  - **Events and pod logs:** standard Kubernetes API.
  - **The main risk is the resource-model migration** (decision 2). It's
    the largest change to authorization since spec 017, and needs careful
    tests.
- **Feasible, with real weight (022):** the workflow plane needs ESO, a
  registry and Argo. That's three more components to keep current, and
  builds are CPU-heavy on 2 cores. It's not for edge-1 by default.
- **Uncertain (023):**
  - **Fit:** OpenSearch's and Prometheus's real footprint on a T470 isn't
    documented. It may well not fit comfortably beside edge-1 (RP-008
    style). Measure before promising it.
  - **Coupling:** the OpenChoreo observer adds its own API, reached through
    the port-forward channel (ADR 0015's pattern), with RP-004's
    considerations.
- **Unknowns for T001's spike:**
  - whether kindnet enforces NetworkPolicy on our node image;
  - which OpenChoreo control-plane components, if any, call
    `openchoreo-api`;
  - whether a `ReleaseBinding` for a second environment needs that
    Environment, and its DataPlane, created on the same cluster. It
    should: one data plane can serve several environments.

## Interfaces (sketch; settled in T002)

```go
// core/resource: new kind; Environment stays, Application re-parented.
const KindDeployment Kind = "Deployment"
type DeploymentSpec struct {
    Component Scope  // the Component deployed
    Release   string // the release it runs; empty = the latest
}
type DeploymentStatus struct { Status; Release string; Endpoints []ComponentEndpointStatus }

// core/reconcile (capability-shaped; no product names)
type ApplicationPlatformProvider interface {
    // existing methods…
    Releases(ctx, component) ([]Release, error)
    Deploy(ctx, component, environment, release string) error
    Undeploy(ctx, component, environment) error
    RuntimeObjects(ctx, component, environment) ([]RuntimeObject, error)
    Events(ctx, component, environment, since time.Time) ([]RuntimeEvent, error)
    PodLogs(ctx, component, environment, opts LogOptions) (io.ReadCloser, error)
    ConfigSchema(ctx, ref TypeRef) (Schema, error)
    Pipeline(ctx, project) (Pipeline, error)
}
```

## Files (020 only)

- **core:**
  - `core/resource` (Deployment; the Application owner change);
  - `core/reconcile` (Deployment reconciler; `ProviderInstance`
    reconciler; interfaces);
  - `core/policy` (actions; the Log Reader role);
  - `core/store/sqlite/migrations/0008_*` (re-parenting, with the
    affected-assignments log).
- **providers:** `providers/application/openchoreo` (environments,
  bindings, releases, rendered resources, events, logs, schemas,
  pipeline; the NetworkPolicy).
- **api:**
  - deployments, runtime, logs (streaming), schema, pipeline and platform
    routes, each with an `api/catalog` entry (CLI and MCP);
  - `provider-instances` gets real behaviour;
  - readiness for optional capabilities;
  - route-table rules.
- **cmd/serendip:** `--follow` streaming for logs.
- **ui:**
  - a Deploy tab with the environment pipeline, plus promote and
    rollback;
  - a Runtime tab with the tree, events and logs;
  - a Configuration form from schemas;
  - a Topology view;
  - a Platform page;
  - a Packages page;
  - the "unavailable" treatment.
- **docs:**
  - ADR 0019 (the environment model and direct-Kubernetes access);
  - `docs/cli-and-mcp.md`, regenerated from the catalogue;
  - `application-platform.md`, `authorization.md`, `resource-model.md`;
  - the risk register (RP-018 and anything new).

## Risks

- **The re-parenting migration** changes what some role assignments
  grant (decision 2). It needs a test that every pre-migration permission
  either survives or is listed.
- **Direct Kubernetes access ties the provider to OpenChoreo's CR shapes**
  (v1alpha1). Version upgrades need re-verification; `VERSIONS.md`
  records the pins.
- **Long-lived log streams** hold a connection each. Cap the concurrent
  streams per session and per server (about 3 and 20).
- **Pipeline enforcement is ours.** A bug there could allow skipping an
  environment, so it needs table-driven tests.
- **Packages** (022 and 023) are heavy. They're never in the edge-1
  default, and they're measured before anything is promised.

## What we give up (recorded 2026-10-20, at the user's question)

None of this is recovered by installing Backstage. Backstage would bypass
Serendip's RBAC, approvals and audit by calling OpenChoreo's API
directly, and would need that API open (RP-018).

- **From building our own console:**
  - **No free UI updates.** New OpenChoreo portal features aren't free;
    each is our choice and our build.
  - **No Backstage ecosystem:** no plugin marketplace, no TechDocs.
- **From closing OpenChoreo's API and using Kubernetes directly:**
  - **OpenChoreo's own `occ` CLI and MCP can't be used** on a Serendip
    install. Serendip's own CLI and MCP (spec 021) are the supported
    path. An admin-only OIDC escape hatch for `occ` is deferred: the user
    asked to keep it in mind (it's in memory).
  - **Remote data planes** wait for a gateway-backed provider.
  - **Small API conveniences** (release naming, environment-config schema
    extraction) are ours to keep in step.
  - **No buffer against OpenChoreo's CR format.** We're coupled directly
    to its `v1alpha1` shapes, so every upgrade needs a re-check.
  - **OpenChoreo's own roles and role bindings don't apply.** That's
    intended: Serendip's RBAC is the one model.
- **From the Azure-style environment model:**
  - **Environment-scope assignments change meaning,** from "edit the
    Applications under it" to "deploy into it". The migration lists the
    affected assignments.
  - **"An Application is inside an environment" stops being true.**
    Environment-scoped Application lists need rethinking.
- **From direct reads without the observability package:** pod logs exist
  only while the pod (and its previous instance) does, until spec 023.
- **From the split and the role choices:** builds (022) and observability
  (023) come later; Reader doesn't see logs (Log Reader does); and
  promotion order is ours to enforce correctly.

## Explicitly NOT done (020)

- Builds (022) and observability (023), except the framework and the
  greyed-out sections.
- Remote data planes (a gateway-backed provider later).
- OpenChoreo's RBAC, scaffolder, and AI assistant or RCA, as in spec.md.
- An exec terminal.
