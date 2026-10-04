# Spec 006: Kubernetes access foundation + Provider interface pattern

Status: Draft — human review required before plan.md

## Problem

`serendipd` (the control plane, spec 002) runs outside Kubernetes with no
way to talk to a K3s cluster's API at all — spec 005 bootstraps K3s and
leaves its kubeconfig on the host, nothing wires `serendipd` up to it.
Separately, `provider-model.md`'s open question — "whether
`ApplicationProvider`/`InfrastructureProvider` end up as the actual
`core/` interface names... resolve during the relevant spec's plan.md"
— has been deliberately left unresolved through 5 specs because no real
provider existed to force the decision. This spec is where both get
resolved, together, since the first real Kubernetes-facing provider
(spec 007: Storage/local-path) needs both to exist first.

This is explicitly a **foundation** spec, not the Storage provider
itself (spec 007 builds that on top of what this spec provides) — same
narrow-slice discipline as specs 001-005.

## Scope — IN

- **`providers/kubernetes`**: a hand-rolled, minimal Kubernetes REST
  client. Not `client-go` — researched and rejected for this spec (see
  plan.md's open-question resolution): `client-go`'s transitive
  dependency weight is a real, upstream-acknowledged problem
  (`kubernetes/kubernetes#127888`), working against constitution rule 4's
  `<256MB` combined-RSS budget and "standard library first" bias, for
  capabilities this spec only needs a handful of REST+JSON calls to
  cover. The client is **generic** (works against any Kubernetes object
  path, not hardcoded to one kind) so it's reusable by every future
  Kubernetes-facing provider (Storage now, Application Platform/Gateway
  later), not storage-specific.
- **kubeconfig loading**: parse the standard kubeconfig YAML format
  (server URL, client cert/key or bearer token, CA cert) from a file
  path — the same file K3s's install already produces at
  `/etc/rancher/k3s/k3s.yaml` (spec 005), no new format invented.
- **The Provider Go-interface registration pattern**: resolves
  `provider-model.md`'s open question. A new, small in-process registry
  (Go interface implementations, not the existing
  `core/registry.{Capability,Provider}Registry`'s data descriptors —
  see plan.md for why these are two different things that happen to
  share the word "registry") that `cmd/serendipd` populates at startup
  by importing both `core/` and the specific `providers/` packages it
  wants active — preserving constitution rule 2 (`core/` imports no
  provider package, ever) since the import happens in `cmd/`, the
  composition root, not inside `core/` itself.
- `cmd/serendipd` wiring: a new `SERENDIP_KUBECONFIG` env var (matching
  the existing `SERENDIP_*` convention), optional — its absence means
  "no Kubernetes-facing providers active," not a startup failure, since
  not every Serendip install necessarily has a cluster yet at first
  boot.
- One proof-of-concept smoke operation through the whole stack (not a
  real capability yet — that's spec 007): the generic REST client lists
  Nodes from the real K3s cluster spec 005's own verification already
  proved works, confirming the kubeconfig-to-API round trip is real, not
  theoretical.

## Scope — OUT (explicitly)

- **The actual Storage/local-path provider** — spec 007, built on this
  spec's foundation.
- **`StorageProvider` (or any other capability-specific) Go interface**
  — that's coupled to a specific capability's operations and resource
  shape; defining it here with no real consumer would be the same
  "guessing a shape with no real consumer" mistake spec 003 explicitly
  avoided for `ApplicationProvider`/`InfrastructureProvider`. This spec
  resolves *how* capability interfaces get registered/wired, not what
  any specific one looks like.
- **Any new resource kind** (e.g. a future `Volume` kind) — spec 007's
  job, once there's a concrete capability interface for it to serve.
- **Watch support** (long-lived streaming connections to the K8s API)
  — polling is sufficient for `core/reconcile`'s existing
  observe-diff-apply-verify loop (constitution rule 3), and matches
  spec 003's own precedent (`ClusterReconciler` polls, doesn't watch).
- **Any change to `api/`** — this is purely `providers/` +
  `cmd/serendipd` wiring, plus the small new in-process registry (likely
  `core/provider/`, a new package — see plan.md).
- **Multi-cluster support** — one kubeconfig, one cluster, matching
  spec 005's single-node scope.

## Acceptance criteria

1. `go test ./providers/kubernetes/...` green: kubeconfig parsing is
   table-driven-tested against a fixture (a real, redacted kubeconfig
   shape, not a live cluster) — portable, no real host/cluster needed
   in CI.
2. Against the real K3s cluster spec 005's own verification builds
   (re-bootstrapped fresh for this spec's own real-host verification,
   same disposable-WSL2-clone approach as spec 005's T007 — never the
   persistent dev distro): the generic REST client successfully lists
   real Nodes from the real cluster, using the real kubeconfig K3s's
   installer produced.
3. The Provider Go-interface registry pattern is proven with a concrete
   (if minimal) example: a trivial interface + one registered
   implementation, wired together in `cmd/serendipd`'s composition
   root, with a test confirming `core/` itself contains no import of
   anything under `providers/` (`.claude/scripts/check-imports.sh`
   extended and verified to still catch a violation, same discipline as
   every prior spec's import-boundary work).
4. `serendipd` starts successfully with `SERENDIP_KUBECONFIG` unset —
   confirms "no cluster configured yet" is a supported, non-fatal
   startup state, not an assumed error path.
5. `go vet ./...`, `gofmt -l .` clean; zero new Go dependency beyond
   what parsing a kubeconfig YAML file strictly requires (a YAML parser
   — `gopkg.in/yaml.v3` or Go's `sigs.k8s.io/yaml`-style wrapper is the
   realistic minimum; resolve the exact choice in plan.md with a short
   ADR-level justification per `CLAUDE.md`'s "any new dependency
   requires an ADR" rule, even though it's a small, narrow addition,
   not a `client-go`-sized one).

## Open questions (resolve during plan review)

- **Exact new-package location and name** for the in-process Go
  interface registry (candidates: `core/provider/`, extending
  `core/registry/` despite the data/live-object distinction, or
  somewhere else) — resolve with a concrete decision in plan.md, not
  left implicit.
- **YAML dependency choice**: `gopkg.in/yaml.v3` (widely used, minimal)
  vs. `sigs.k8s.io/yaml` (translates through JSON, matches Kubernetes'
  own tooling conventions more closely but pulls in more) — pick one
  with reasoning in plan.md.
- **Generic client's shape**: does it operate on `json.RawMessage`/
  `map[string]any` (fully generic, no typed structs at all in this
  package) or does it provide a minimal set of typed envelope structs
  (apiVersion/kind/metadata only, leaving spec/status as raw JSON for
  callers to type themselves)? Resolve with a concrete design in
  plan.md — this shapes how spec 007's provider code will actually look.
