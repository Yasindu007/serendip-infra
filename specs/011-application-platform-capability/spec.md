# Spec 011: Application Platform capability (Application/Component resource kinds, ApplicationPlatformProvider, reconciler)

Status: Draft — human review required before plan.md

## Problem

Spec 010 built the OpenChoreo deployment foundation — a real, reachable
control plane and data plane, a registered `DataPlane` CR, and a real
`controller-manager`/`cluster-agent` connection confirmed end-to-end on a
`kind` cluster. But OpenChoreo is registered under the
`"application-platform"` capability in `core/provider.Registry` as
`openchoreo.Marker{}` — a placeholder with no methods, reachable only to
confirm the foundation is alive (same spirit as spec 006's `nodeLister`
before spec 007, or spec 008's bare `*openbao.Client` before spec 009).
`ApplicationSpec`/`ComponentSpec` (`core/resource/app.go`, since spec 001)
are empty structs — nothing a human or API caller sets today actually
describes a deployable workload, and nothing reconciles either resource
kind against anything real. This spec is spec 010's declared follow-on,
same split as specs 006→007 and 008→009: foundation, then the real
capability.

**This spec's shape differs from specs 007/009 in one structural way,
not just a naming choice.** Every other capability so far maps one
Serendip resource kind onto one piece of real external state (a
`Volume`→a PV/PVC pair, a `Secret`→a KV v2 path). OpenChoreo's own object
model sits a Component inside a multi-level hierarchy of its own CRDs
(at minimum `Organization` and `Project`, confirmed to exist as real CRDs
installed by spec 010's control-plane chart — see Open Questions for what
else that hierarchy requires) that doesn't disappear just because
Serendip's `resource-model.md` already has an `Organization`→`Project`→
`Environment`→`Application`→`Component` tree of its own. Whether this
spec's provider creates OpenChoreo's hierarchy objects implicitly
(one default `Organization`/`Project` per Serendip `Application`, hidden
from the user) or requires an explicit mapping is a real design decision,
not a detail — see Scope-IN and Open Questions below.

## Scope — IN

- **Real `ApplicationSpec`/`ComponentSpec` fields** (`core/resource/
  app.go`), replacing today's empty structs. `ApplicationSpec` stays
  minimal — a grouping concept, same as `Project`'s own empty Spec, since
  OpenChoreo's own `Project` CRD is itself close to a pure grouping
  object. `ComponentSpec` gains the minimum needed to describe one
  already-built, already-pushed container image as a deployable
  workload: an image reference, the port(s) it listens on, and any
  env vars — no build/CI configuration (Build Plane stays out per ADR
  0005), no multi-container/sidecar modeling, no resource
  requests/limits (not needed to prove the mechanism works). Exact field
  names/shape are plan.md's job.
- **`ApplicationPlatformProvider` Go interface**, defined in
  `core/reconcile` alongside its Reconciler(s) (mirrors `StorageProvider`/
  `SecretsProvider`'s placement) — capability-shaped, using only
  `core/resource` types, no OpenChoreo-specific concept (an `Organization`
  CR, a `Workload` trait, ...) leaking into `core/`. Exact method
  shape is plan.md's job (Open Questions) — likely `Ensure`/`Delete` per
  resource kind it drives, following `StorageProvider`'s precedent that a
  provider which provisions real external state needs an explicit
  teardown method (`provider-model.md`'s documented lesson from spec
  007), but confirm this during planning rather than assuming it applies
  unchanged.
- **A real adapter in `providers/application/openchoreo`** implementing
  `ApplicationPlatformProvider` by constructing OpenChoreo custom
  resources directly via the existing `providers/kubernetes.Client` —
  not via `helm` (spec 010's shellout covers installing the platform
  itself; creating *instances* of its CRDs is ordinary Kubernetes object
  construction, the same precedent spec 010's own `EnsureDefaultDataPlane`
  already set for the `DataPlane` CR). Translates a Serendip
  `Application`/`Component` into whatever real OpenChoreo CRs its object
  model actually requires (Open Questions).
- **Reconciler(s)** in `core/reconcile` driving `ApplicationPlatformProvider`
  for `Application` and/or `Component` (whether these are one Reconciler
  or two is plan.md's job), looking up `"application-platform"` from
  `core/provider.Registry` the same way every prior capability's
  composition-root wiring does, and correctly calling the provider's
  delete path when the Serendip-side record is gone (same lesson as
  spec 007/009, re-confirmed each time per `provider-model.md`'s
  breadcrumb since it's easy to copy a passive-observer Reconciler's
  no-op branch by mistake).
- **`cmd/serendipd` wiring**: register the new Reconciler(s) with the
  reconcile engine, gated the same way every `SERENDIP_KUBECONFIG`-
  dependent wiring already is; replace `openchoreo.Marker{}`'s
  registration under `"application-platform"` with the real provider
  implementation.
- **One proof-of-concept flow through the whole stack**, against a real
  cluster (`kind`, this project's established preference per
  `docs/PROGRESS.md`): create an `Application` and a `Component` (with a
  real, publicly-pullable container image) through the ordinary resource
  API; confirm the real OpenChoreo CR(s) this spec's provider creates
  exist on the cluster (via `kubectl`, bypassing Serendip, same
  verification discipline as specs 008/009); confirm an actual Pod comes
  up in the data plane and reaches `Ready`; confirm the Component's own
  Status/Conditions (observed through Serendip's API) honestly reflect
  that real state; delete the `Component` and confirm the real Pod/CR(s)
  are gone too.

## Scope — OUT (explicitly)

- **The Build Plane / any CI mechanism** — ADR 0005's own deferral,
  unchanged; this spec only ever deploys an already-built,
  already-pushed image, never builds one from source.
- **Multi-Environment promotion (`DeploymentPipeline`) and the
  Observability Plane** — spec 010 registered exactly one default
  `DataPlane`; this spec targets exactly that one environment/data plane,
  not a promotion pipeline across several. A later spec's job once more
  than one `Environment` resource actually needs distinct capability
  configuration (`resource-model.md`'s own `EnvironmentSpec.
  CapabilityOverrides` already modeled this generally, but wiring it
  through OpenChoreo's own multi-environment CRDs is new work, not this
  spec's).
- **External traffic routing / kgateway / Gateway API objects** — spec
  010 left `gateway.enabled=false` on both charts deliberately (nothing
  to route to yet). This spec proves a Component's workload actually
  runs (a real Pod, `Ready`), not that it is reachable from outside the
  cluster. Deploying kgateway and wiring ingress is a future spec's job
  once an Application Platform capability exists for it to serve
  (exactly the condition `provider-model.md`'s open questions already
  named). Clarification (found in T008): Gateway API CRDs (definitions
  only) are installed as an OpenChoreo prerequisite; there is still no
  gateway controller and no Gateway/HTTPRoute objects.
- **`Secret` injection into a running Component** — spec 009 already
  flagged this as its own Scope-OUT item, waiting on the Application
  Platform provider existing; it exists after this spec, but wiring a
  `Secret`'s value into a Component's environment is new scope, not
  silently bundled in here.
- **AI-initiated deploys/scales** (constitution rule 5) — no AI
  operations engine exists yet to be the caller.
- **UI/CLI/MCP surfaces** — API only, same precedent as every prior spec.
- **Multiple concurrent `ApplicationPlatformProvider` instances /
  per-Environment provider selection** — one default provider via the
  Registry, same as every capability so far.
- **A policy engine gating who may deploy what** — `core/policy` doesn't
  exist yet, same standing deferral as spec 009's identical item.
- **Scaling, rollout/rollback strategy, health-check tuning beyond "the
  Pod reached Ready"** — real future work once the basic create/observe/
  delete loop this spec proves is in place.

## Acceptance criteria

1. `go test ./core/reconcile/... ./providers/application/openchoreo/...
   ./api/...` green: the new Reconciler(s)' existence/Condition/delete-
   cascade logic (fixture-based, mirroring `secrets_test.go`'s pattern,
   no real cluster), and the `providers/application/openchoreo` adapter's
   `resource.Application`/`resource.Component`→real-CR translation, are
   table-driven-tested without a live cluster.
2. Against a real cluster (`kind`): creating an `Application` and a
   `Component` (real image) through the ordinary resource API results in
   the real OpenChoreo CR(s) this spec's provider is responsible for
   existing on the cluster, confirmed directly via `kubectl` — and a real
   Pod for that Component reaching `Ready` in the data plane namespace,
   also confirmed directly via `kubectl`.
3. The Component's Status/Conditions, read back through Serendip's API,
   honestly report that real state (constitution rule 6) — not a
   Condition that's `True` while the real Pod is still pending/crashing,
   nor a silently-stale Condition once the real state changes.
4. Deleting the `Component` through the API removes the real OpenChoreo
   CR(s) and the real Pod too (confirmed directly via `kubectl`) —
   applying spec 007/009's documented lesson (`provider-model.md`) that a
   capability whose provider holds real external state must have its
   Reconciler's resource-gone branch actually call delete, not a silent
   no-op.
5. Idempotency: reconciling an already-synced, unchanged `Component`
   again produces no additional `Update` call, audit row, or
   `ResourceVersion` bump (mirrors specs 003/007/009's identical
   criterion for their own Reconcilers).
6. `go vet ./...`, `gofmt -l .`, `.claude/scripts/check-imports.sh`
   clean; `core/` (including the updated `core/resource.Application`/
   `Component` and the new `core/reconcile.ApplicationPlatformProvider`/
   Reconciler(s)) imports nothing under `providers/`, and no
   OpenChoreo-specific (or any other product) name appears in any
   `core/` identifier, comment, or Condition reason/message string.

## Open questions (resolve during plan review)

- **What OpenChoreo's object model actually, minimally requires to run
  one Component**, confirmed against a real cluster's installed CRDs
  (`kubectl get crds`) and `kubectl explain` on each, not assumed from
  docs or from `docs/architecture/application-platform.md`'s own
  unverified list (that doc's "open questions" carried this exact caveat
  forward from spec 010's planning and it was never resolved against a
  real install — do not treat it as settled). At minimum this needs
  confirming: whether `Organization` is a real standalone CRD or implicit
  in `Project`'s own spec; whether a `Component` CR alone is deployable
  or needs a separate `Workload`/trait object; whether anything here
  needs `ComponentType` (the application-platform.md CRD list flags
  this as unconfirmed-exhaustive); and how a `Component` actually
  attaches to the already-registered `DataPlane`/`Environment`.
- **Whether this spec creates OpenChoreo's `Organization`/`Project`
  hierarchy objects implicitly (one default pair, hidden from the
  Serendip user) or maps them explicitly onto Serendip's own
  `Organization`/`Project` resource kinds** — the structural question
  this spec's Problem section opens with. Resolve with a concrete
  decision, not left implicit in the provider's code.
- **Exact `ComponentSpec` field shape** — image reference format,
  whether ports are a list or a single value, whether env vars are
  plain key/value or need a richer shape anticipating `Secret`
  injection later (without actually building that injection now —
  constitution rule 10 still applies; don't speculatively build the
  shape for a feature that's explicitly Scope-OUT).
- **Exact `ApplicationPlatformProvider` method shape** — one Reconciler
  or two (`Application` and `Component` may not need symmetric
  treatment, the same way `SecretsProvider`'s shape genuinely differed
  from `StorageProvider`'s per `provider-model.md`); whether `Application`
  needs its own real external CR at all if it turns out to be pure
  grouping, or whether only `Component` ever calls into the provider.
- **Readiness/Condition semantics** — what Condition type name and
  reason values honestly describe "CR created but Pod not yet
  scheduled" vs. "Pod Ready" vs. a genuine failure, and how `CheckReady`-
  style retry logic (spec 010's own `go-reviewer` finding) does or
  doesn't apply to a per-Component reconcile loop running on the
  engine's existing resync interval rather than a one-shot startup check.
- **Resync interval** — reuse the existing 30s/10s constants
  (specs 003/007/009's precedent) unless a concrete reason not to
  surfaces during planning.
