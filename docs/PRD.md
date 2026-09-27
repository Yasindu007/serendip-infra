# Serendip Infra — Product Requirements Document

Living document. This is the "why are we building this and what does done
look like" anchor — read it first in any new session, alongside
`docs/PROGRESS.md` (current state) and `docs/constitution.md`
(non-negotiable rules). Update this file when scope, sequencing, or defaults
change; don't let it drift from reality the way the founding conversation
docs (`docs/raw/`, local-only) did before extraction.

## One-liner

An open-source, self-hostable platform operating system: one control layer
(resource model, capability/provider registries, reconciliation, AI
operations) over composable OSS infra, running on hardware as small as a
single old laptop and scaling to a multi-node private cloud without
reinstalling. Full framing: `docs/architecture/overview.md`.

## Who this is for

- **Right now**: solo builder (the user), validating the architecture and
  building a proof of concept before committing money to hardware.
- **Next**: a small team / early customer pilot (per the original roadmap,
  targeted ~month 6-7 for hands-on-keyboard use, ~month 2 onward for visible
  demos) running real workloads on Serendip-managed infrastructure.
- **Eventually**: anyone who wants cloud-provider-shaped operational
  capability (identity, secrets, storage, observability, incidents, AI ops)
  on hardware they own, without adopting a specific vendor's stack.

## Hardware: development machine vs. reference/target hardware

Two different machines, two different jobs — do not conflate them:

| | Development / POC machine | Reference / target hardware |
|---|---|---|
| Spec | RTX 4060, Ryzen 7, 32GB RAM (current, in use now) | ThinkPad T470-class: i5-6th gen, 16-32GB RAM (constitution rule 4) |
| Role | Build and validate the full stack fast, with headroom to spare, before spending money on laptops | What Serendip must actually ship well on — the honesty check |
| Status | Available today | Not yet purchased — POC on the dev machine is explicitly the gate before buying 1, then 3, then 5 T470s |

This does **not** relax constitution rule 4. Every RSS/CPU budget, every
`profiles/*/bench/` requirement, every "benchmark before this becomes a
default" gate in ADR 0005 (OpenChoreo) and ADR 0006 (Thunder) still targets
T470-class hardware and stays unresolved until measured on one. The dev
machine's abundant headroom is exactly why it's *not* evidence of fitness —
something can run fine on a 4060/32GB box and still be unshippable on a
T470. Its job is to let the full stack (OpenChoreo, Thunder, OpenBao,
Longhorn, CloudNativePG, observability backend, everything) come up
end-to-end and be iterated on quickly, so the T470 purchase is informed by
a working system, not a bet.

**Sequencing implication**: Phase 3 of the original roadmap ("edge-1 on
real iron") is the trigger to buy the first T470 — not before. Until then,
all `profiles/*/bench/` entries stay empty/TODO and no capability's
resource-footprint claim (Thunder's "lighter than Keycloak", OpenChoreo's
RAM need) is to be reported to a user as verified, per constitution rule 6.

## POC scope — what "done, go buy the T470s" looks like

The POC is the Foundation + Bootstrap Agent phases (roadmap Phases 0-2) plus
enough of Phase 3's provider set to prove the architecture end-to-end on the
dev machine, run as a single-node install (`edge-1` topology, just not on
`edge-1`-class hardware yet):

1. `serendipd` control plane running: resource model (Org→Project→
   Environment→Application), capability/provider registries, reconciliation
   engine, audit — spec 001 (+ 002 API, 003 reconciler).
2. `serendip-agent` bootstraps a K3s node (can be a VM on the dev machine)
   — spec 004 (discovery/identity/self-registration) + spec 005 (the
   actual K3s install/uninstall). Originally attributed to spec 004
   alone; corrected once spec 005 planning found spec 004 had
   explicitly deferred the install step itself.
3. At least one real provider live per required capability so the loop is
   genuine, not stubbed: Identity (Thunder, ADR 0006), Secrets (OpenBao),
   Application Platform (OpenChoreo, ADR 0005), Storage (local-path),
   Gateway (kgateway).
4. A demo application deployed end-to-end through the Serendip API, with
   honest capability reporting (constitution rule 6) throughout.
5. The full loop demonstrated at least once: declare desired state → API →
   reconciliation → provider → verification → audit record.

This POC does **not** need to prove hardware fitness — that's what the
T470 phase is for. It needs to prove the *architecture* (capability/
provider abstraction, reconciliation, provider swap without code change,
per `provider-model.md`'s stated acceptance bar).

## Default providers — chosen, not fixed

The defaults below are the author's picks for the POC and initial ship,
each backed by an ADR. They are **defaults, not the only option** — see
"Setup wizard philosophy" below.

| Capability | Default | ADR |
|---|---|---|
| Kubernetes | K3s | 0001 |
| Storage progression | local-path → Longhorn | 0002 |
| Control-plane stack | Go + embedded SQLite | 0003 |
| Application Platform | OpenChoreo | 0005 |
| Identity | Thunder (ThunderID, `asgardeo/thunder`) | 0006 |

Full capability catalog (including capabilities without a dedicated ADR
yet — Secrets/OpenBao, Networking/Cilium, Gateway/kgateway, etc.):
`docs/architecture/capability-model.md`.

## Setup wizard philosophy

The installer/setup wizard presents each required capability with a
**recommended default, clearly labeled as a recommendation**, plus the
other registered providers as selectable alternatives — never a forced
single choice. E.g.:

```
Identity provider:
  ● Thunder (recommended) — lightweight, Go-native, fits edge-1 hardware
  ○ Keycloak — mature, heavier (JVM); better fit from small-3 up
  ○ External OIDC — bring your own (Entra ID, existing Keycloak, ...)
```

This is a direct product-level expression of constitution rule 1 ("bring
your own capability") and the registry mechanics in `provider-model.md`
("switching a capability's active provider is a registry configuration
change, not a code change") — the wizard UI/UX must not hardcode assumption
that the recommended default is the only path. This applies to every
required capability in the catalog, not just Identity/Application Platform.

## Non-goals (see constitution rule 10 for the authoritative list)

Serendip does not build a database engine, a Kubernetes distribution, a
secrets engine, a metrics TSDB, a service mesh, a Git server, a CI engine,
an API gateway, or a VM hypervisor.

## Roadmap

See `docs/PROGRESS.md` for current phase and status. Phase structure
(original estimates assumed near-full-time solo dev on real hardware from
day one — now adjusted for dev-machine-first POC sequencing, see Hardware
section above):

0. Setup & architecture freeze
1. Foundation (resource model, registries, reconciliation) — specs 001-003
2. Bootstrap agent — spec 004
3. POC validation on dev machine (this replaces "edge-1 on real iron" as
   the immediate next milestone; the T470 phase happens after POC succeeds)
3b. edge-1 on real iron (first T470 purchased here)
4. Identity/secrets hardening + 3-node story (small-3)
5. Observability + operations (AI diagnosis, incident lifecycle)
6. Application platform depth + customer pilot

## Open decisions to keep visible (do not silently resolve)

- Observability backend (OpenObserve vs. SigNoz vs. OpenSearch) — pending
  T470 benchmark, per `overview.md` and `capability-model.md`.
- AI inference backend/model — never committed beyond an illustrative
  example (Ollama + a Qwen-class model). See `ai-operations.md`.
- Thunder re-check: hit v1.0.0 on 2026-08-15 (see ADR 0006) — has zero
  field track record as of POC start. Watch for the fallback triggers ADR
  0006 defines before treating it as settled long-term.
- OpenChoreo RAM budget on T470-class hardware — unverified until Phase 3b
  benchmarking (ADR 0005).
- `private-cloud` profile — design-stage only, not scoped, see
  `deployment-profiles.md` Open questions.
