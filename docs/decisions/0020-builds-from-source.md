# ADR 0020: Builds from source: our own workflow, an in-cluster registry, Build as a kind, polling

Status: Accepted
Date: 2026-10-24

## Context

Before spec 022, a Component could only run an image that was already
pullable. The build package (`specs/022-builds`) installs OpenChoreo
1.3.1's workflow plane, which runs builds as Argo Workflows. That leaves
several choices open, each with long-lived consequences:
- **Which workflow runs.** OpenChoreo ships sample `ClusterWorkflow`s,
  built around its own `generate-workload` step, ExternalSecrets and
  labelled runs.
- **Where images go.** No registry comes with the plane, and Serendip has
  none.
- **How the build gets a Git credential.** The samples expect the
  External Secrets Operator (ESO).
- **How Serendip records a build,** and who may trigger one.
- **How a new commit is noticed.**

Constraints:
- **The machine:** a laptop with 2 cores, 16 GB and no public URL.
- **No new Go dependency** without good reason (constitution 4).
- **Every change** is a store write, reconciled and audited (constitution
  3 and 8).

The spike (T001, plan.md "Spike findings") settled the facts this ADR
relies on.

## Decision

### 1. Serendip ships its own build workflow

- **One `ClusterWorkflow`,** `serendip-dockerfile`, with its templates
  inline. It has two steps:
  - check out (`alpine/git`);
  - build and push (`podman`, privileged).
- **It outputs the commit and the image reference,** read from the Argo
  Workflow's nodes. Serendip's next release of the Component uses the
  image, so there's no `generate-workload` step.
- **Runs are standalone `WorkflowRun`s** in `serendip-builds`, without
  OpenChoreo's component labels. A labelled run needs `allowedWorkflows`
  on the ComponentType, so Serendip's `Build` keeps the link to its
  Component instead.
- **Dockerfile builds only,** to start with. Buildpacks are a later spec.
- **Images are pinned** in `providers/build/openchoreo/VERSIONS.md`.
- **Builds run `privileged` without `hostUsers: false`.** User namespaces
  fail on kind (RP-038). That's rechecked on K3s.

### 2. An in-cluster registry

- **`registry:2`** (`distribution`, pinned), as a hand-built Deployment,
  Service and PVC in `serendip-builds`.
- **Its storage** comes from the Storage capability's default class.
- **How images move:**
  - the registry pod uses `hostNetwork`;
  - builds push to its Service over plain HTTP;
  - workloads pull `localhost:5000/...`, which containerd allows over
    plain HTTP without any node configuration.
- **It's single-node.** On more nodes, a containerd mirror through the
  serendip-agent is the fix.

### 3. No External Secrets Operator

- **The build reconciler copies the credential.** It reads the Git
  credential through the Secrets capability. Then it writes a plain
  Kubernetes Secret, `<run>-git`, before the run starts, and deletes it
  when the run ends or is removed.
- **That saves a fourth component** with its CRDs and pods. The cost is a
  short-lived copy in the cluster's own store, which ESO would have made
  anyway. It's recorded in the risk register.

### 4. `Build` is a resource kind, owned by its Component

- **Spec:** `commit` (empty means the branch's head) and `cancel`.
- **Status:** phase, commit, image, times, steps and a Ready condition.
- **Triggering is a store create,** so it's audited, reconciled, and
  takes the same policy path for people and agents. History outlives the
  plane's runs, which expire an hour after they end.
- **Scope:** a Build is owned by its Component, so roles at the
  Project, Application or Component govern it. It's removed with the
  Component, so it has no `DELETE` route of its own.
- **One run at a time per Component.** Two builds side by side on 2
  cores would only both be slow.

### 5. Auto-build polls; it doesn't use webhooks

- **Every 5 minutes,** a Component with `autoBuild` on has its branch's
  head read over Git's smart HTTP: `GET <repo>/info/refs?service=git-upload-pack`.
- **The lookup** is about 100 lines of stdlib pkt-line parsing,
  `providers/internal/gitremote`. No Git binary and no new dependency.
- **SSH URLs can't be polled.** The Component says so.
- **Webhooks need a public URL,** which a laptop behind NAT doesn't have.
  OpenChoreo's webhook endpoint also sits behind `openchoreo-api`, which
  Serendip doesn't use (ADR 0019).

### Dependencies

- **Argo Workflows** is a cluster component the chart brings, not a Go
  dependency.
- **No new Go dependency.**

## Consequences

- **A source Component's image comes from its newest successful Build.**
  The application platform provider doesn't change, and every successful
  build makes a new release.
- **Serendip owns the workflow.** Upstream template changes don't reach
  us, and neither do their fixes, so the pins have to be kept current.
- **Build pods are privileged** (RP-038). They run only Serendip's own
  template, in their own namespace. No person-supplied workflow runs.
- **The registry is single-node and never cleaned.** Garbage collection
  is a later spec.
- **Uninstall leaves the eight `argoproj.io` CRDs** (RP-039). Helm never
  owns them, and deleting them could break a person's own Argo.
- **Reading run outputs from the Argo Workflow** needs the plane in the
  same cluster. A remote workflow plane needs another path.
- **New commits are noticed within 5 minutes,** not at once.

## Alternatives rejected

- **OpenChoreo's sample workflows:** they need ESO, `generate-workload`
  and labelled runs with `allowedWorkflows` on every ComponentType.
- **ESO:** another operator to keep current, for a copy Serendip can make
  itself.
- **An external registry:** it needs an account and credentials. It's
  later, as an option.
- **Builds as live reads of the plane's runs, with no kind:** no audit or
  policy path for triggers, and history would vanish with the runs.
- **Webhooks:** no public URL on the target machine.
- **A Git library or the Git binary** for the head lookup: the smart-HTTP
  read is small enough to own.

## References

- `specs/022-builds/plan.md`, decisions 1–3, 5 and 7, and "Spike
  findings".
- ADR 0010 (Helm shell-out), ADR 0019 (direct platform access).
- `docs/risk-register.md`: RP-038 and RP-039.
