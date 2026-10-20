# Spec 020: OpenChoreo's portal features in Serendip's own console

Status: Draft, 2026-10-20. Scope reviewed by the user; split agreed
(see "Delivery"). The plan is awaiting approval. **On hold until spec 021
(Serendip's own MCP server and CLI) is done:** the user's order,
2026-10-20, so 020's new actions land in the console, CLI and MCP
together.

## Problem

OpenChoreo, Serendip's application platform (ADR 0005), ships its user
interface as a Backstage portal. Serendip doesn't install Backstage:
- **Dependency weight:** it's a large Node application with hundreds of
  transitive packages, and requests up to 2 GiB.
- **Supply-chain risk:** ADR 0012's bar for what runs in the console's
  toolchain rules it out.

So today a Serendip user sees only what spec 011 and spec 016 built: an
Application's Components, their status and their endpoint, in one
environment. Everything else OpenChoreo can do is invisible:
- deploying across environments and promoting between them;
- what a Component is actually running, its events and its pod logs;
- configuring traits;
- how Components depend on each other;
- builds;
- logs, metrics, traces and alerts.

The research for this spec (2026-10-20, OpenChoreo 1.3.1) found two
things:
- **The portal is a thin layer.** It sits over `openchoreo-api`, a
  documented REST server (Apache-2.0) that the control-plane chart already
  installs. The features can be rebuilt in Serendip's console, in its own
  design language, without Backstage.
- **Some features need planes Serendip doesn't install.** Builds need
  OpenChoreo's **workflow plane** (Argo Workflows). Logs, metrics, traces
  and alerts need its **observability plane** and that plane's backends.

## Goals

- **Rebuild OpenChoreo's portal features in Serendip's console.** They go
  through Serendip's API (constitution 7) and capability-shaped interfaces
  in `core/` (constitution 1). The console never calls OpenChoreo, and no
  product name reaches `core/`.
- **Two optional packages,** builds and observability:
  - each can be installed on a running Serendip;
  - neither is part of the edge-1 default (constitution 4).
- **Honest availability:** a feature whose package isn't installed is
  shown greyed out as unavailable, saying which package it needs, instead
  of hidden or faked (constitution 6).

## Delivery: one design, three specs (agreed 2026-10-20)

This spec is the shared design. Delivery splits:

| Spec | Delivers | Acceptance criteria |
|---|---|---|
| **020 (this spec)** | Items 1–5, 10–13, and the **package framework**: installing and removing an optional package (items 6 and 8, as mechanics), readiness, and the greyed-out treatment | 1–7, 10–13 |
| **022** | The builds package (items 6–7) | 8 |
| **023** | The observability package (items 8–9) | 9 |

Before any of this, RP-018 (OpenChoreo's API open to any pod in the
cluster), found while researching this spec, was fixed on its own
(`96f285d`).

## Scope — IN

### On what's already installed (OpenChoreo control and data planes)

1. **Runtime view of a Component:**
   - **Resource tree:** the Kubernetes objects that make up its running
     workload in each environment, and their state.
   - **Events:** recent Kubernetes events for those objects.
   - **Pod logs:** recent log lines from its running pods, and a live
     follow while the page is open. These come from the running pods, not
     from a log store, so no observability package is needed.
2. **Environments and promotion**, with per-environment settings:
   - **The deployment pipeline:** its environments (for example
     development, staging, production) and the promotion order between
     them.
   - **Per Component:** what release runs in each environment, and its
     state there.
   - **Actions:** deploy a release to the first environment, promote it to
     the next one, roll back to an earlier release, restart a rollout, and
     turn auto-deploy on or off.
   - **Per-environment overrides:** what differs between environments
     for the same release: environment variables, replicas, resource
     limits, and the trait parameters each component type marks as
     per-environment. One Application runs in many environments, so
     without overrides every environment would run identical
     configuration.
   - **Promotion is destructive in one sense:** it replaces what an
     environment runs. Whether a promotion needs an approval is decided
     by policy, as for any action. Roles decide, and an approval is the
     exception (spec 017, spec 019).
3. **Component configuration from schemas:**
   - **What can be set:** the traits and parameters a Component's type
     allows, as forms generated from OpenChoreo's own JSON schemas.
   - **The workload:** containers, endpoints and dependencies.
   - **Changes are desired state** (constitution 3) and go through the
     existing reconcile path.
4. **Dependencies and topology:**
   - **Per Component:** which other Components it calls (its declared
     dependencies), and which call it.
   - **Per Application:** a diagram of its Components and their
     connections, drawn in Serendip's design language. It's static: the
     declared topology, not live traffic. The live overlay is part of the
     observability package.
5. **Platform view (read-only):**
   - the data planes;
   - the environments and the deployment pipeline;
   - the component types and traits available, with what each allows.

### Optional package: builds (OpenChoreo's workflow plane)

6. **Installing it:** a person allowed to manage the platform installs
   it, or removes it, on a running Serendip through the API and the
   console. Installing it is a recorded, reconciled action, never a side
   channel.
7. **Builds:**
   - **Build from source:** for a Component built from a Git repository,
     trigger a build, see its runs and their state, and read a run's logs
     and events.
   - **Auto-build:** turn it on or off.
   - **Without the package,** the Build sections show as unavailable,
     naming the package.

### Optional package: observability (OpenChoreo's observability plane)

8. **Installing it:** the same as the builds package.
   - **Backends:** for this spec, OpenChoreo's documented default modules:
     OpenSearch with Fluent Bit for logs, OpenSearch with an OpenTelemetry
     Collector for traces, and Prometheus for metrics.
   - **It's a convenience pack,** chosen to make the features real. It
     doesn't settle the observability backend default, which
     `capability-model.md` leaves open pending a T470 benchmark. edge-1's
     lighter backend (OpenObserve or similar) is a later spec.
9. **Features:**
   - **Log search** over stored logs, per Component and across an
     Application, filtered by time and text.
   - **Metrics** per Component (CPU, memory, request rate, errors, latency
     where available), over a chosen time range.
   - **Traces:** a list, and one trace's spans.
   - **Alerts:** the alert rules a Component has, its firing alerts, and
     notification channels.
   - **The live traffic overlay** on the topology diagram.
   - **Without the package,** these sections show as unavailable, naming
     the package. Pod logs and events (item 1) still work.

### Across all features

10. **Availability is reported, not guessed:**
    - The API reports which packages are installed and ready, through the
      existing capability readiness (`GET /v1/readiness`).
    - The console greys out what's missing, with the reason. A package
      that's installed but not ready reads "installing" or "not ready",
      not "unavailable".
11. **Authorization:** every new route has an action under Serendip's
    RBAC (spec 017).
    - Reading runtime state, logs and metrics are read actions; logs are
      their own action, since they can carry sensitive data.
    - Promoting, rolling back, restarting, triggering builds and changing
      configuration are write actions.
    - Installing or removing a package is a platform-level action.
    - Agents get the same guardrails as spec 018: deletes and removals
      need approvals (spec 019).
12. **Audit:** every change, including deploys, promotions, rollbacks,
    restarts, build triggers, config changes and package installs or
    removals, is audited with its actor and decision (constitution 8).
13. **Footprint, measured and reported:**
    - edge-1 without packages: no regression on the simulated T470.
    - **Each package's idle memory and CPU is measured** on the simulated
      T470.
    - **The console says plainly what installing a package costs** before
      it's installed. For example, the observability package's
      OpenSearch-based backends are heavy for a 16 GB machine.

## Scope — OUT (explicitly)

- **Backstage itself**, and any OpenChoreo Backstage plugin code.
- **OpenChoreo's AI assistant ("Perch") and RCA or FinOps agents.** A
  separate spec, to discuss later. It overlaps
  `ai-operations.md`.
- **OpenChoreo's RBAC screens** (AuthzRole, AuthzRoleBinding). Serendip's
  Azure-style RBAC (spec 017) stays the model. A separate spec, if ever.
- **The scaffolder and templates.** Creating Components stays on
  Serendip's forms. A separate spec.
- **An in-browser exec terminal** into pods.
- **API management or an API portal.** OpenChoreo offers those only
  through optional community modules.
- **Cost analysis and delivery insights (DORA).**
- **The edge-1 observability backend** (OpenObserve or a lighter
  alternative). A later spec, after the T470 benchmark in
  `capability-model.md`.
- **Changing edge-1's default component list.** Both packages are
  optional.
- **Mapping Serendip's Organization and Team to OpenChoreo.**

## Affected resources and capabilities

- **Application Platform capability** (`core/`, spec 011): grows
  environments and promotion, releases, runtime state (tree, events, pod
  logs), schema-driven configuration and declared dependencies, all as
  capability-shaped interfaces.
- **A build capability** (new, optional): build runs, their logs, and
  auto-build. Constitution 10 holds: Serendip integrates a CI engine, it
  doesn't build one.
- **The Observability capability**: its first real provider, as an
  optional package. It covers stored logs, metrics, traces and alert
  rules.
- **Resource model:** `Environment` gains a real meaning for
  deployments. See "Open questions".
- **The capability and provider registries:** optional packages, with
  install and removal reconciled and readiness reported.
- **Authorization:** new actions and route rules (spec 017).
- **The console:** new screens and sections, the "unavailable"
  treatment, and package management, in the existing design system.
- **Benchmarks:** the per-package footprint on the simulated T470.

## Acceptance criteria

1. **Runtime view.** On a cluster with only the control and data planes,
   a person opens a running Component and sees its resource tree, recent
   events and pod logs, including following new log lines live.
2. **Promotion.** With a pipeline development → staging → production, a
   person deploys a release to development and promotes it to staging,
   then production.
   - Production runs with an override (for example more replicas, or a
     different environment variable) that development doesn't have, and
     promotion carries the release without carrying over the other
     environment's overrides.
   - Each environment shows the release it runs and its state.
   - A rollback returns an environment to the earlier release.
   - Every step is audited.
3. **Policy on promotion.** A Reader can't promote (403). A role that
   allows promotion promotes at once. An agent's promotion follows the
   same policy path as any other action.
4. **Configuration.** A person sets a trait's parameters through a form
   generated from its schema. An invalid value is refused with the
   schema's message, and the change is reconciled and visible on the
   running workload.
5. **Topology.** An Application's diagram shows its Components and their
   declared dependencies, and each Component lists what it calls and what
   calls it.
6. **Platform view.** Data planes, environments, the pipeline, component
   types and traits are listed read-only.
7. **Unavailable, honestly.** Without the builds and observability
   packages:
   - their sections are greyed out, naming the missing package, and
     nothing errors or shows placeholder data;
   - while a package installs, its sections read "installing", not
     "unavailable".
8. **Builds package.**
   - Installed through the API (and the console), it becomes ready.
   - A person triggers a build of a Component from a Git repository and
     sees the run progress, its logs, and the deployed result.
   - Removing the package greys the sections out again, and the removal
     is audited.
9. **Observability package.**
   - Installed (OpenSearch and Prometheus), it becomes ready.
   - Log search finds a Component's past log lines by text and time.
   - Metrics show CPU and memory over a chosen range.
   - A trace lists its spans.
   - An alert rule fires and shows as firing.
   - The topology diagram shows live traffic.
10. **Authorization.** Every new route has a rule; the route-table test
    covers them. Logs need their own action. Package install and removal
    need platform-level actions.
11. **Interface parity.** Everything the console does is an API call
    shown in its "Same request through the API" panel, and is also a
    Serendip CLI command and MCP tool (spec 021's surfaces, extended here)
    (constitution 7).
12. **Footprint.**
    - edge-1 without packages shows no regression against spec 019's
      simulated-T470 run.
    - Each package's idle memory and CPU on the simulated T470 is
      recorded, and the console's install screen states it.
    - Findings go in `docs/risk-register.md`.
13. **Nothing else breaks.** Existing tests, the bench harness and a
    real-cluster run pass.

## Open questions (answered in plan.md decisions 2, 3, 5, 8 and 10)

1. **Environments in the resource model.** Serendip's tree puts an
   Application *under* one Environment (Project → Environment →
   Application). OpenChoreo deploys one Component to *many* environments
   along a pipeline. Options include:
   - Environments as deployment targets of an Application, outside the
     ownership tree;
   - an Application per Environment, linked by promotion.

   The plan must pick one and keep RBAC scope inheritance (spec 017)
   coherent.
2. **How `serendipd` talks to OpenChoreo:**
   - **through `openchoreo-api`** (REST, a token from its OIDC provider),
     which proxies the data plane for trees, events and logs;
   - **or by reading the CRDs directly,** with the data-plane parts some
     other way.

   This includes how that credential is held (compare ADR 0015).
3. **Package mechanics:** where installed packages are recorded, how
   install and removal are reconciled, what happens to running workloads
   and stored data on removal, and whether removal is a destructive
   action needing an approval (spec 019).
4. **Live log following:** the transport from `serendipd` to the browser
   (streaming or polling) under the console's strict CSP and session
   model (ADR 0013).
5. **Is this one spec or several?** The scope is large. The plan may
   split delivery, for example 020a (control and data plane features),
   020b (builds) and 020c (observability), with one design.
