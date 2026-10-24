# Spec 022: Builds — the workflow-plane package

Status: Draft, 2026-10-23. Awaiting review.

Carved out of spec 020 (plan decision 10, agreed 2026-10-20): 020 built the
package framework and the greyed-out Builds tab; this spec delivers the
builds package itself, its features, and its first real installer.

## Problem

Spec 020 gave Serendip optional packages: a `ProviderInstance` names one,
its reconciler installs it, removal goes through an Approval, and readiness
greys features in or out. But the framework has never installed anything:
both catalogue entries have no installer, so `POST /v1/packages` refuses
them, and the console's Builds tab reads "unavailable".

Today a Component is only ever a ready-made container image. A person with
source code in Git has to build and push an image somewhere else first.
OpenChoreo can build from source in the cluster through its **workflow
plane** (Argo Workflows), and spec 020's spec item 7 asked for that in
Serendip: trigger a build, watch it, read its logs, turn auto-build on or
off.

Three things recorded in the risk register must be settled before the
first real install can ship:

- **RP-034 — cost before install.** The console must say what a package
  costs before anyone installs it. Today `GET /v1/packages` carries no
  measured memory or CPU, and the console reads "Not measured yet".
- **RP-037 — providers don't declare requirements.** The provider model
  says a provider declares its RAM and CPU on reference hardware. None does
  in code; the numbers live only in benchmark results.
- **RP-033 — one package per capability isn't enforced by the store.** Two
  simultaneous installs could both pass the API's check. Harmless while
  nothing installs; not once something does.

And one from spec 020's T016 review: an installed package is re-reconciled
on every resync, so its install must be a cheap no-op when nothing has
changed, not a full re-install every 30 seconds.

## Scope — IN

### The package's cost, stated before install

1. **Measured cost in the catalogue.** Each package the server offers
   carries its measured cost on reference hardware (the simulated T470):
   - idle memory and idle CPU once installed;
   - for builds, also the cost **while a build runs** (peak memory and CPU),
     since builds are CPU-heavy on 2 cores;
   - the date and benchmark run the numbers came from.

   `GET /v1/packages` returns them, the CLI and MCP show them, and the
   console's Packages page states them before the install button.
2. **No guessed numbers.** A package without a measurement says "not
   measured", never an estimate. A package whose measured cost doesn't fit
   comfortably on the reference machine beside edge-1 says so plainly, with
   the numbers, and an Owner may still install it (constitution 4, 6).
3. **Providers declare their requirements** (RP-037). The provider
   registry gains the resource-requirements field the provider model
   describes. The workflow-plane package fills it from its benchmark; the
   packages' stated cost comes from that one declaration, not a second
   copy. Existing providers may stay undeclared ("not measured") in this
   spec.

### Installing and removing the builds package

4. **A real installer.** An Owner installs the builds package on a running
   Serendip through the API, console, CLI or MCP. It installs everything
   builds need (the workflow plane and its prerequisites, such as somewhere
   to keep built images), waits for it to be ready, and reports progress:
   not installed → installing → ready, or failed with a plain message.
5. **Resync is a cheap no-op.** Once installed, re-reconciling the package
   with nothing changed doesn't re-run the install. Its cost on resync is
   measured and stays near zero. A change that does need re-applying (a
   server upgrade with a new pinned version, or a component deleted by
   hand) is noticed and repaired.
6. **One package per capability, enforced by the store** (RP-033). Two
   simultaneous installs of the builds package leave exactly one; the other
   request is refused with a conflict.
7. **Removal** goes through an Approval (spec 019), as spec 020 set up.
   - **Before the delete, the console says what is lost:** build history
     and logs, and the stored images built from source. Components running
     a built image keep running, but can't be rescheduled onto a node
     without that image or rebuilt until the package is back. The API's
     package record states the same, so the CLI and MCP can too.
   - After removal the Builds sections grey out again, and the package's
     cluster footprint is gone (measured).

### Building from source

8. **A Component can come from source.** Besides an image, a Component can
   name a Git repository: URL, branch or commit, and the path within the
   repository to build. A public repository needs nothing more.
   - **Private repositories:** the Component names a Serendip Secret
     (spec 009) holding the Git credential. The credential stays in the
     Secrets capability; it isn't copied into Serendip's store or shown
     back.
   - A source Component can't be saved without the builds package
     installed and ready; the refusal names the package.
9. **Triggering a build.** A person (or agent, with the permission)
   triggers a build of a source Component, optionally at a specific commit.
   It's a recorded, audited action, reconciled like any other change.
10. **Runs.** For each source Component: its build runs, newest first, each
    with its state (queued, running, succeeded, failed, cancelled), the
    commit it built, when it started and how long it took. A running build
    can be cancelled.
11. **Run logs and events.** A run's logs, recent lines and followed live,
    with the same caps and reconnect behaviour as spec 020's pod logs; and
    its events. Build logs can carry secrets, so reading them needs the
    logs permission, not just read.
12. **From build to running.** A successful build becomes a new release of
    the Component. It then follows spec 020's model unchanged: a Deployment
    on "the latest" runs it, and promotion carries it along the pipeline.
    A failed build changes nothing that's running.
13. **Auto-build.** A person turns auto-build on or off per source
    Component. With it on, a new commit on the Component's branch starts a
    build.
    - **Honest about reachability:** if new commits can't be noticed (for
      example, the Git host's webhook can't reach a laptop behind NAT), the
      Component says so plainly instead of silently never building.

### Across all features

14. **Authorization** (spec 017): `serendip/builds/read` for runs,
    `serendip/builds/write` for triggering, cancelling and auto-build; logs
    need `serendip/components/logs/read` (Log Reader). Package install and
    removal stay platform-level, at `/`. Every new route has a rule.
15. **Agents** follow spec 018's guardrails: triggering and cancelling are
    ordinary writes; removing the package needs a person's approval.
16. **Audit** (constitution 8): package installs and removals, source
    changes, build triggers, cancels and auto-build toggles, each with
    actor, interface and decision.
17. **Interface parity** (constitution 7): every console action is an API
    call in the "Same request through the API" panel, a CLI command and an
    MCP tool; `api/catalog` covers them.

## Scope — OUT (explicitly)

- **Building a CI engine** (constitution 10). Serendip integrates the
  workflow plane; it doesn't schedule build steps itself.
- **Custom build pipelines or workflow authoring.** The package's standard
  build methods only; which ones is a plan decision.
- **Pushing to external registries** (Docker Hub, GHCR) and pulling base
  images from private registries.
- **Image signing, scanning, SBOMs.**
- **A remote or separate workflow plane** (builds on another cluster).
  Builds run on the same cluster, like the data plane today.
- **Build caching tuning** beyond what the package does by default.
- **Git provider integrations** (GitHub Apps, OAuth sign-in to a Git
  host). Credentials are a Secret.
- **The observability package** (spec 023), and declaring requirements for
  existing providers beyond the builds package.
- **Changing edge-1's default component list.** The builds package stays
  optional and off by default.

## Affected resources and capabilities

- **A build capability** (new, optional, `core/`): runs, triggering,
  cancelling, run logs and events, auto-build. Capability-shaped; no
  workflow-engine names in `core/`.
- **Its provider** (`providers/`): OpenChoreo's workflow plane, as the
  `openchoreo-workflow-plane` package, with its installer.
- **The provider registry:** a resource-requirements declaration per
  provider (RP-037).
- **The package catalogue and `ProviderInstance`:** stated cost,
  what-is-lost-on-removal, a cheap resync, and store-level uniqueness per
  capability (RP-033; likely a store migration).
- **Resource model:** `Component` gains a source (Git) alternative to an
  image; build runs as a read model. Whether a run is its own resource kind
  is a plan decision.
- **Secrets capability:** Git credentials referenced by Components.
- **Authorization:** routes for the new actions (the actions were named in
  spec 020).
- **The console:** the Builds tab, the source form for Components, and the
  Packages page's cost and removal wording.
- **CLI and MCP:** new commands and tools.
- **Benchmarks:** the package's idle and in-build footprint, and resync
  cost, on the simulated T470.

## Acceptance criteria

1. **Cost before install.** With the builds package not installed,
   `GET /v1/packages`, `serendip packages list`, the MCP tool and the
   console's Packages page all show its measured idle memory and CPU and
   its in-build peak, with where the numbers came from. The observability
   package, unmeasured, reads "not measured", never a number.
2. **The numbers are real.** They match a recorded benchmark run on the
   simulated T470 in `profiles/edge-1/bench/results/`, and come from the
   provider's one requirements declaration.
3. **Install.** An Owner installs the package from the console; the Builds
   tab goes from "unavailable" to "installing" to available, and the
   install is audited. A Contributor's install is refused (403).
4. **Uniqueness.** Two concurrent install requests leave one
   `ProviderInstance`; the other gets 409. A store-level test proves it
   without relying on the API's check.
5. **Cheap resync.** With the package installed and nothing changed, a
   resync doesn't re-run the install, measured as near-zero added CPU over
   an idle period; deleting one of its components by hand is repaired on
   the next resync.
6. **Build from a public repository.** A person creates a Component from a
   public Git repository, triggers a build, watches the run go from queued
   to succeeded, follows its logs live, and sees the Component served
   through its Deployment in the first Environment, then promotes it.
7. **Private repository.** The same works with a private repository whose
   credential is a Serendip Secret; the credential never appears in API
   responses, logs Serendip writes, or audit records.
8. **Failure and cancel.** A build that fails shows failed with its logs,
   and the running release is unchanged. A running build cancelled reads
   cancelled.
9. **Auto-build.** With auto-build on, a new commit starts a build; where
   the commit can't be noticed, the Component says so. Turning it off stops
   new builds.
10. **Removal.** Removing the package needs an Approval; the console states
    what's lost before the request; after removal the Builds sections grey
    out, and the cluster holds none of the package's workloads.
11. **Authorization and parity.** Every new route has a rule in the
    route-table test; a Reader can see runs but not their logs; every
    console action is a CLI command and an MCP tool; an agent's removal
    request waits for a person.
12. **Footprint.**
    - edge-1 without the package: no regression against spec 020's
      simulated-T470 run.
    - With it installed: idle and in-build cost recorded, and the stated
      cost matches.
    - Findings go in `docs/risk-register.md`; RP-033, RP-034 and RP-037 are
      closed or updated.
13. **Nothing else breaks.** Existing tests, the bench harness, and a
    real-cluster run on `serendip-t018` pass.

## Open questions (for plan.md)

1. **What the package installs.** Spec 020's plan decision 6 sketched the
   workflow-plane chart, an in-cluster registry, and External Secrets
   Operator backed by OpenBao for Git credentials. Is ESO needed, or can
   the credential reach the build another way at less weight? The spike
   pins versions and measures each piece.
2. **Build methods.** Which of the workflow plane's standard builds to
   offer first (Dockerfile, buildpacks, ...), by cost on 2 cores.
3. **Auto-build triggers.** Webhooks need the Git host to reach the
   cluster; polling costs requests and idle CPU. Which one, and how
   "can't be noticed" is detected.
4. **Run storage.** Whether build runs are read live from the workflow
   plane (as spec 020 reads runtime state) or kept as Serendip resources,
   and how long history and logs survive.
5. **The cheap resync.** How the installer knows nothing changed (a
   recorded fingerprint of what it applied, a quick read of the installed
   versions) without trusting stale state.
6. **The uniqueness constraint.** Its migration, and what happens to an
   existing duplicate (`ReasonDuplicate`) when it's applied.
7. **The image source on a Component.** How a source Component's image
   field relates to its builds, and whether a Component may switch between
   image and source.
