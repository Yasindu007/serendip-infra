# ADR 0019: Environments are resource groups, Deployments run Components in them, and serendipd reaches OpenChoreo through the Kubernetes API

Status: Accepted
Date: 2026-10-23

## Context

Spec 020 brings OpenChoreo's portal features (environments and
promotion, the runtime view, configuration forms, topology, the
platform view) into Serendip's own console, CLI and MCP. Two open
questions shaped everything else:

1. **Where environments sit in the resource model.** Before spec 020 an
   Application was owned by one Environment (Project → Environment →
   Application), and each Component auto-deployed to OpenChoreo's single
   `development` environment. OpenChoreo deploys one Component to many
   environments along a pipeline. RBAC scope inheritance (spec 017,
   ADR 0014) had to stay coherent either way.
2. **How `serendipd` talks to OpenChoreo:** through `openchoreo-api`
   (REST, a token from its OIDC provider, proxying the data plane), or
   straight to its custom resources through the Kubernetes API.

## Decision

### 1. Azure-style environments

**The mapping:** a **Project is the subscription** and an **Environment
is the resource group** you deploy into.

- **Applications are owned by their Project:** Project → Application →
  Component. Environments are siblings of Applications under the Project.
- **A new kind, `Deployment`:** one Component running in one Environment.
  - **It's owned by the Environment,** so that Environment's scope decides
    who may deploy there, and it has a `depends-on` edge to its Component.
  - **Its spec:** `component`, an optional `release` (empty means the
    latest), and `overrides` (environment variables, per-environment
    configuration, trait settings).
  - **Its status:** the running `release`, a bounded `history` (what it may
    roll back to) and `endpoints`.
  - **Deploying, promoting and rolling back** are writes of a Deployment;
    **undeploying** is its delete, and so goes through an Approval
    (ADR 0016).
- **Releases** are read-only, provider-reported snapshots of a Component's
  workload, not a writable kind.
- **A Project's pipeline** (`spec.pipeline.paths`: from one Environment to
  others) orders its Environments.
  - **Serendip enforces it,** because the platform doesn't.
    - **Without a pipeline,** anything goes.
    - **At the first Environment,** any release enters, including "the
      latest".
    - **Elsewhere,** "the latest" is refused, and a release must run in an
      Environment that promotes there, or be one this Environment ran
      before (a rollback).
    - **Skipping an Environment** is refused (400).
  - **A pipeline never spans Projects.**
- **Promotion moves the release, never the source's overrides.** Each
  Environment keeps its own configuration.
- **Authorization falls out of spec 017's inheritance:** "read the source,
  write the target".
  - Deploying needs `serendip/deployments/write` at the Environment, plus
    read on the Component.
  - Promoting needs read on the source Deployment and write at the target
    Environment.
  - Reading runtime state and reading logs are separate actions
    (`serendip/components/runtime/read`, `serendip/components/logs/read`),
    because logs can hold sensitive data. Reader doesn't include logs; the
    built-in Log Reader role does.
- **Migration 0008** re-parents each Application from its Environment to
  that Environment's Project, and gives each of its Components a
  Deployment in the old Environment, so nothing running stops. An
  Environment without a Project keeps its Applications, and the migration
  notes it.

### 2. Serendip creates every release and binding; auto-deploy is off

- **Components are created with `autoDeploy: false`.** Each spec change
  becomes a `ComponentRelease` named by its content, so the same spec is
  the same release.
- **Each Deployment becomes a `ReleaseBinding`** with its overrides, plus
  the Application's `ProjectReleaseBinding` for that Environment (which
  makes the data-plane namespace).
- **Each Serendip Environment becomes an OpenChoreo `Environment`, and
  each Project's pipeline a `DeploymentPipeline`.**
- **A Component's readiness changes meaning:** it's Ready (`Released`)
  once its current spec is a release. Running, health and endpoints belong
  to each Deployment.
- **One writer per binding.** Serendip writes every binding, so the
  platform's auto-deploy and Serendip's overrides never compete.

### 3. Direct Kubernetes API access, not `openchoreo-api`

The OpenChoreo provider reads and writes OpenChoreo's custom resources,
and reads runtime objects, events and pod logs, through the Kubernetes
client `serendipd` already holds.

- **No new credential:** `openchoreo-api` would add an OIDC client, its
  secret and an OpenChoreo role binding, another administrator-grade
  credential to hold and rotate.
- **One authorization layer:** Serendip's RBAC decides every request,
  instead of two policy engines that can disagree. `openchoreo-api` stays
  locked down.
- **One hop:** promotion is one write; schemas come from the CRs; runtime
  objects from `RenderedRelease` status. The API would add its own hop,
  its cluster gateway, and a port-forward from outside the cluster.
- **`core/` stays product-free:** it defines capability-shaped interfaces
  (`DeploymentProvider`, `RuntimeProvider`, `ConfigSchemaProvider`,
  `PlatformViewProvider`). All OpenChoreo knowledge is in
  `providers/application/openchoreo`, with versions in its `VERSIONS.md`.

## Consequences

- **Role assignments at an Environment change meaning.** They used to
  cover the Applications under it; after migration they cover only
  deployments into it. Migration 0008 writes each affected assignment to
  `migration_notes`, which `serendipd` logs. Everything was local and
  pre-release when this landed, so the cost was small but real (RP-028).
- **The provider is tied to OpenChoreo's CR shapes (v1alpha1).** Each
  upgrade needs re-verification against `VERSIONS.md`.
- **Remote data planes wait.** `openchoreo-api` reaches them through its
  cluster gateway; every current profile runs the data plane in the same
  cluster. A gateway-backed provider can be added behind the same
  interfaces for the multi-cluster profiles.
- **A few conveniences are Serendip's to keep:** release naming, schema
  extraction, pipeline enforcement, and explaining why a binding waits
  (for example, for the Components it calls).
- **A Deployment's first run in a new Environment** also creates its data-
  plane namespace (RP-035).

## Alternatives rejected

- **One Application per Environment, linked by promotion.** It fights the
  platform's model (one component, many bindings), duplicates
  configuration across copies, and makes "the same app in prod" a naming
  convention instead of a fact.
- **Environments outside the ownership tree** (deployment targets of an
  Application). Scope inheritance couldn't then answer "may this person
  deploy to prod?" without a second authorization mechanism.
- **Through `openchoreo-api`.** Covered above: a second credential and
  policy engine, and an extra hop on every call, for remote data planes no
  profile has yet.
- **Leaving auto-deploy on.** Two writers on one binding, and an ambiguous
  "first environment" for a pipeline without paths.

## References

- `specs/020-openchoreo-console-features/` (plan decisions 2 and 3; the
  spike findings; `verification.md`)
- ADR 0005 (OpenChoreo as the application platform), ADR 0010 (its Helm
  install), ADR 0014 (Azure-style RBAC), ADR 0016 (approvals)
- `docs/architecture/application-platform.md`, `resource-model.md`,
  `authorization.md`
