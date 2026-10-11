# ADR 0005: OpenChoreo as the default Application Platform provider

Status: Accepted
Date: 2026-08

## Context
The Application Platform capability (component deployment, GitOps,
build/registry — `docs/architecture/capability-model.md`) needs a default
provider. The founding design discussion ranked OpenChoreo #1 against
KubeVela+Crossplane+Argo, Kratix+Crossplane+Argo, Backstage+Crossplane+Argo,
and Rancher+Crossplane+Argo, but the decision was never captured as an ADR
despite being as foundational and hard-to-reverse as ADR 0001 (K3s).

Current-state check (2026-08, via oss-researcher) before accepting:
- OpenChoreo shipped 1.0 GA April 2026, accepted into CNCF Sandbox January
  2026, Apache 2.0, backed by WSO2. Actively developed (v1.1.0 in progress).
- Kubernetes-native: CRDs + controller-manager, no separate control plane,
  runs on any conformant cluster including K3s.
- Already uses Thunder for identity and kgateway for gateway — both already
  Serendip defaults elsewhere (ADR 0006, capability-model.md), so adopting
  it does not add net-new capability surface.
- No competing project has overtaken it for "K8s-native IDP-in-a-box";
  Backstage/KubeVela/Kratix remain framework/plugin-style, not a full
  application platform.

Risks identified, not disqualifying but tracked:
- **Governance**: CNCF Sandbox (not Incubating), ~51%+ of commits from one
  organization (WSO2). Single-vendor concentration is a bus-factor risk.
- **Resource footprint on edge-1**: control plane alone wants ~2 CPU/4GB RAM
  minimum (8GB recommended with observability); local quick-start guidance
  suggests 8GB/16GB min/recommended overall, 4 cores, 20GB disk. Against a
  T470 (16-32GB shared with K3s + every other provider), this is tight and
  was previously assumed fine without a number attached.

## Decision
Accept OpenChoreo as the default Application Platform provider
(`capability-model.md`: `application-platform: default: openchoreo`).

Mitigations for the risks above, as conditions of this default rather than
reasons to reject it:
- Defer the optional Build Plane (Argo Workflows, +8GB/4-core) at `edge-1`;
  rely on pre-built images/external CI until a profile has budget for it.
  This is already consistent with `deployment-profiles.md`.
- `profiles/edge-1/bench/` must record actual OpenChoreo control-plane RSS
  on reference hardware (constitution rule 4) before OpenChoreo is enabled
  by default at `edge-1` specifically — if it doesn't fit, `edge-1` may
  need to ship without Application Platform enabled (degraded, not broken;
  see constitution rule 6) until `small-3`.
- Track OpenChoreo's CNCF maturity level (Sandbox → Incubating) and WSO2
  commit concentration as a standing watch item, not a re-litigation
  trigger by itself.

## Consequences
- Application deployment, GitOps, and build/registry flow through
  OpenChoreo's CRDs behind Serendip's provider abstraction
  (`provider-model.md`); no OpenChoreo-specific type leaks into `core/`
  (constitution rule 1).
- `edge-1` benchmark work (Phase 3 of the roadmap) must produce a real
  RSS/CPU number for OpenChoreo before it ships as an `edge-1` default —
  this is now a named gate, not an assumption.
- If OpenChoreo stalls (no CNCF progression, WSO2 commit share collapses
  without replacement, or the T470 benchmark fails outright), the
  KubeVela+Crossplane+Argo combination is the documented fallback to
  re-evaluate first, per the original comparison.

## Benchmark status (2026-10-11, spec 013)

The gate above is **still open**: there's no reference-hardware number yet.
A simulated-T470 run (`profiles/edge-1/bench/002-edge1-footprint.md`; a kind
node throttled to an i5-6300U-class CPU on the dev machine, explicitly not
the gate) measured OpenChoreo, as Serendip deploys it, at **about 55–58 MiB
for the control plane and about 7 MiB for the data plane**. Backstage,
security/Thunder, the gateway block and observability are all disabled in
that configuration. The whole edge-1 stack idled at about 1.3 GiB.

That is far below the "~2 CPU/4GB" estimate in Risks, which described a
fuller install. Enabling Backstage, Thunder, or observability later needs
re-measuring. The fallback trigger ("the T470 benchmark fails outright") is
not triggered on this evidence, but stays undecidable until the real T470
run.
