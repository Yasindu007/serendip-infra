# Spec 014: Reconciliation hardening

Status: Draft — human review required before plan.md

## Problem

Specs 010–013 left four known weaknesses in how Serendip keeps real state
in line with desired state. Each was documented as a deliberate trade-off or
found by spec 013's benchmark. None is a capability gap; each makes the
existing capabilities slower, more fragile, or less correct than
constitution rule 3 ("observe, diff, apply, verify") and rule 6 (honest
status) promise.

1. **Status lags reality by up to 30 seconds.** The reconcile engine
   re-checks a resource only on a Serendip-side event or the 30 s resync.
   Upstream progress (a Pod becoming Ready, a route being accepted) is
   neither, so status catches up only at the next resync. Spec 013 measured
   it: Component create → `Serving` took exactly 30 s on every run, all of
   it waiting, none of it work.
2. **Platform defaults and the shared gateway are repaired only on restart.**
   OpenChoreo's platform defaults (project type, component type, Environment,
   pipeline), the default DataPlane and its gateway wiring, the Gateway API
   CRDs, and the shared kgateway Gateway are ensured once when `serendipd`
   starts. If one is deleted or drifts later, Components report honest
   failures, but nothing repairs the cause until someone restarts
   `serendipd`.
3. **Fields Serendip stops setting are never removed.** Provider writes use
   create-then-merge-patch. A merge patch can't delete a field it doesn't
   mention, so a label, annotation, or map key that Serendip set in one
   version and dropped in the next stays on the live object forever. This
   was documented as a known limitation in specs 011–012 (kgateway proxy
   labels, the component type's update path, the Gateway API CRD upgrade).
4. **One slow dependency kills `serendipd`'s startup.** Kubernetes-facing
   setup (storage, OpenBao, kgateway, OpenChoreo) runs inside startup. If any
   step doesn't become ready within its wait, the whole daemon exits and the
   API never comes up. Spec 013 hit this for real: a transient image-pull
   `EOF` from quay.io outlasted OpenBao's 90 s readiness wait, and
   `serendipd` exited, although the image pulled fine 16 s later. On an
   edge-1 laptop with an unreliable network, that's a daemon that sometimes
   just doesn't start.

## Scope — IN

- **Faster convergence while work is in flight.** A Reconciler can ask to
  be re-checked sooner than the resync interval while its resource is
  converging (for example a Component `Progressing` or `Reachable=Pending`,
  an Application `Progressing`), and the engine honors it. Settled resources
  (`Deployed`, `Serving`, or terminal failures) stay on the normal resync,
  so the idle footprint is unchanged. Re-checks are bounded and back off, so
  a resource that never converges can't hammer the cluster API.
- **Periodic re-ensure of platform foundations.** Everything ensured once at
  startup today is re-ensured on a fixed interval while `serendipd` runs:
  - the Gateway API CRDs;
  - the shared gateway;
  - the default DataPlane and its gateway wiring;
  - the application platform's defaults.

  The ensures are already idempotent and make no writes when nothing has
  drifted, so a no-drift pass is reads only. A failed pass is logged and
  retried at the next interval; it doesn't stop `serendipd`.
- **Server-side apply for Serendip-owned objects.** Kubernetes objects that
  Serendip's providers create directly (not those Helm owns) are written
  with server-side apply under a single, stable field manager. Fields
  Serendip previously set and no longer sets are then removed by the API
  server. This covers the local-path PV/PVC, the OpenBao objects, the
  OpenChoreo custom resources (Project, ProjectReleaseBinding, Component,
  Workload, DataPlane, platform defaults), and kgateway's GatewayParameters
  and Gateway. Whether the Gateway API CRD installer moves to server-side
  apply too is plan.md's call. Conflicts with other field managers are
  handled deliberately, never silently.
- **Startup that degrades instead of dying.** If a Kubernetes-facing provider
  isn't ready within its wait at startup, `serendipd`:
  - stays up and serves its API;
  - keeps retrying that provider's setup with backoff;
  - reports honestly, through the API, which capabilities are not ready yet
    and why.

  Resources that depend on a not-yet-ready capability get an honest
  Condition saying so, rather than nothing or a false failure. Once setup
  succeeds, everything proceeds as today with no restart. A genuinely broken
  configuration (an unreadable kubeconfig, a missing `helm` binary) still
  fails fast, with a clear error.
- **Real-cluster verification of every item** on kind, plus a re-run of the
  spec 013 benchmark harness to confirm idle footprint and rule 4 haven't
  regressed, and to measure the new time-to-`Serving`.

## Scope — OUT (explicitly)

- **Watching upstream objects** (Kubernetes watch/informers on OpenChoreo,
  Gateway API, or Pods) as an event source. Faster re-checks while
  converging are the scoped fix. A watch-based design is a larger change to
  the engine's model and isn't needed at edge-1 scale.
- **Concurrent reconciliation.** The engine stays single-goroutine and
  sequential (`core/reconcile/CLAUDE.md`).
- **Server-side apply for Helm-managed objects** (cert-manager, OpenChoreo
  and kgateway chart contents). Helm owns them.
- **Changing what gets deployed.** No component trimmed or added; Traefik
  stays.
- **A per-capability health dashboard or new resource kind** beyond what's
  needed to report "not ready yet" honestly through the existing API.
- **Timeout tuning as a fix in itself.** Raising the OpenBao wait from 90 s
  would hide the fragility, not remove it. Startup resilience is the fix.
- **Multi-node, HA, or upgrade orchestration.**

## Acceptance criteria

1. `go test ./...` green. New engine behavior (early re-check, bounded
   backoff, settled resources not re-checked early), periodic re-ensure,
   the server-side apply request shape, and degraded startup are covered by
   table-driven tests without a live cluster. Existing reconciler
   idempotency tests still pass.
2. **Faster status, measured.** On a real kind cluster, Component create →
   `Serving` is measured by the spec 013 harness. It must be **well under
   the 30 s resync**; plan.md sets the target. A settled Component is still
   re-checked only at the normal interval: no extra reads or writes at idle.
3. **Repair without restart.** On a real cluster, deleting a platform default
   by hand (for example the OpenChoreo `Environment`), or the shared Gateway,
   is repaired within the re-ensure interval while `serendipd` keeps running.
   Components return to `Serving` without a restart.
4. **Removed fields actually disappear.** On a real cluster, a field Serendip
   set on an owned object and then stops setting (for example a label or a
   proxy label) is gone from the live object after the next reconcile or
   re-ensure. Fields set by other managers on the same object are left
   alone.
5. **Startup survives a slow dependency.** On a real cluster, `serendipd`
   started while the cluster is unreachable, or while a provider can't
   become ready:
   - stays up and serves its API (including `/healthz`);
   - reports through the API which capabilities aren't ready yet and why;
   - gives dependent resources an honest not-ready Condition;
   - completes setup on its own once the cluster recovers, after which
     resources converge as normal. No restart is needed.

   A bad kubeconfig path still fails fast.
6. **No idle regression.** The spec 013 harness, re-run under the same
   simulated-T470 constraints, shows idle memory per group within the spec
   013 tolerance and `serendipd` still passing rule 4. The re-run is
   recorded alongside spec 013's results.
7. `go vet ./...`, `gofmt -l .`, and `.claude/scripts/check-imports.sh` are
   clean. `core/` gains no product names, and engine changes keep its rules
   (single goroutine; the event handler only enqueues).

## Open questions (resolve during plan review)

- **How a Reconciler asks for an early re-check.** Options:
  - a return value or typed result from `Reconcile` (it returns only `error`
    today);
  - a sentinel error carrying a delay;
  - a separate engine method.

  Also: what the delay and backoff are (for example 2 s doubling to the
  resync interval), and which Conditions count as "converging". Decide
  generically, so it isn't Component-specific.
- **Re-ensure interval and placement.** How often (minutes, not seconds; it's
  drift repair, not convergence), where it runs (an engine-scheduled task,
  or a loop owned by the composition root), and how failures surface beyond
  logs.
- **Server-side apply mechanics.**
  - The field manager name, and whether to force on conflict.
  - How the generic Kubernetes client gains an apply call (a PATCH with the
    apply content type, no new dependency).
  - How "fields Serendip owns" maps onto each provider's object
    construction.
  - Migration: objects created before this spec have no Serendip-managed
    fields recorded for apply. Decide how the first apply takes ownership
    without wiping fields.
- **Where "capability not ready" is reported.** Candidates:
  - extending `/healthz`;
  - status on the existing `Capability` or `ProviderInstance` resources
    (spec 001 kinds nobody has used for runtime status yet);
  - a small read-only endpoint.

  This must stay API-first (constitution rule 7: UI = CLI = API).
- **How reconcilers for a not-yet-ready capability behave.** Register them
  at startup with a provider that reports "not ready", or register them only
  once setup succeeds? Also how the Secret API handler, which depends on the
  Secrets reconciler today, behaves before OpenBao is ready.
- **Retry policy for startup setup.** The backoff schedule, whether each
  capability retries independently (so one stuck provider doesn't block the
  others), and how ordering dependencies are kept: gateway before the
  application platform, CRDs before both.
