# Bootstrap agent (serendip-agent)

`serendip-agent` is a single static Go binary that lives on the host
**outside Kubernetes**. It is the answer to a specific problem: if the
Serendip control plane itself runs on Kubernetes, and Kubernetes breaks,
nothing running inside the cluster can fix it. Something has to exist below
that layer.

Implementation rules are in `agent/CLAUDE.md`; this document covers the
design rationale and responsibilities.

## Why it must live outside Kubernetes

```
Linux
├── serendip-agent        (outside Kubernetes)
└── Kubernetes
     └── Serendip platform (control plane, providers, ...)
```

The agent assumes only a general-purpose Linux host — this is also why K3s
was chosen over Talos for v1 (ADR 0001): Talos's immutable, API-only model
would remove the general-purpose host the agent needs for diagnostics and
recovery.

## Responsibilities

1. **Hardware discovery** — CPU, RAM, disks (type: NVMe/SSD/HDD, size),
   network interfaces, virtualization support, GPU. Read-only, always safe.
2. **Environment discovery** — OS, IP addresses, DNS, internet connectivity,
   whether a Kubernetes install already exists on the host.
3. **Node identity** — establishes a stable identity for the machine before
   it's part of any cluster.
4. **Bootstrap** — installs and configures K3s (ADR 0001) on a fresh node,
   using hardware/environment discovery to pick a recommended profile
   (`deployment-profiles.md`) rather than assuming one.
5. **Cluster enrollment (join)** — a new node's agent discovers an existing
   Serendip control endpoint and requests to join; the platform reports back
   hardware compatibility and a recommended role (control-plane vs. worker)
   before the user confirms.
6. **Health reporting** — reports node-level health independent of whether
   the Kubernetes API server on that node is reachable.
7. **Low-level upgrades** — K3s version upgrades, OS-level changes.
8. **Emergency diagnostics** — collects logs and state even when the cluster
   control plane is down, so a human (or, later, AI within the safety
   hierarchy below) has something to diagnose with.

## Bootstrap flow (first node)

```
Hardware discovery → Environment discovery → Recommended profile
  → Install K3s → Install Serendip control plane
```

The installer does not blindly apply a fixed set of Helm charts; it
calculates a recommended profile from what it discovers (e.g. a single
16-32GB-RAM machine with one disk recommends `edge-1` with HA, distributed
storage, and VM infrastructure explicitly marked unavailable — constitution
6).

## Enrollment flow (additional nodes)

```
serendip-agent starts on new node
  → discovers existing Serendip cluster (or is pointed at one)
  → reports hardware to control plane
  → control plane returns compatibility + recommended role
  → user approves join
  → topology planner incorporates the node (deployment-profiles.md)
```

## Safety hierarchy

The bootstrap agent is the "physical OS" leaf of Serendip's safety hierarchy
(constitution 5):

```
AI / Console → Serendip API → Policy → Controllers → Provider → Kubernetes/Harvester/AWS
                                                    → Bootstrap Agent → Physical OS
```

AI never gets direct host access. All host mutations (installing K3s,
formatting a disk, joining a node) are explicit agent subcommands guarded by
`--confirm`; discovery/diagnostics are always safe and read-only
(`agent/CLAUDE.md`). This mirrors constitution 5's rule that destructive
operations always require an explicit approval object, even from a human via
the API.

## Constraints (from agent/CLAUDE.md)

- Zero heavy dependencies; CGO-free build; runs on a bare Debian/Ubuntu host.
- Must function when the cluster is down — never assumes the API server
  exists.
- Target: <30MB binary, <50MB RSS idle (consistent with constitution 4's
  lightweight-by-default posture).

## Open questions

- None outstanding beyond implementation detail (exact CLI subcommand
  surface, discovery output schema) — those belong in the agent's own spec,
  not this document.

**Breadcrumb from spec 004** (discovery/identity/self-registration
slice, `agent/discover`+`agent/identity`+`agent/client`): environment
discovery (responsibility #2 above — OS, IP addresses, DNS servers,
internet reachability) has real, tested code now, but **nowhere to send
it** — `core/resource.NodeStatus` only ever modeled hardware facts
(CPU/memory/disk/network), not environment facts. The discovered values
are printed by `serendip-agent discover` for a human to see but aren't
sent by `register`, since there's no `NodeStatus` field for them. This
was flagged as a known gap in spec 004's plan.md, not an oversight —
worth resolving whenever `resource-model.md`'s still-unresolved Network
resource gets designed, or by extending `NodeStatus` directly, whichever
turns out to fit better once there's a real consumer for these facts
(e.g. the topology planner, when a future spec builds full enrollment).

**Breadcrumb from spec 005** (the actual K3s install/uninstall,
`agent/bootstrap`): responsibility #4 ("Bootstrap — installs and
configures K3s") is now real, using K3s's own defaults (Traefik,
ServiceLB, local-storage, Flannel all left enabled) rather than
pre-disabling them in favor of Serendip's eventual choices (kgateway,
Cilium, Longhorn/local-path) — those providers don't exist yet, and
disabling their K3s equivalents first would leave a bootstrapped
cluster with no gateway/storage/networking at all in the meantime.
**Real cost worth knowing about**: swapping Flannel for Cilium later
likely means a fresh cluster, not a live migration — K3s's CNI choice
is effectively fixed at install time. Whichever future spec introduces
the Cilium provider should plan for a re-bootstrap, not assume an
in-place network swap is available. Verified end-to-end on a real
(disposable) Linux host: install, `Ready` node, and the official
uninstall script all behave as K3s's own documentation describes — no
Serendip-specific surprises in either direction.

**Known gap, not an oversight**: no `core/audit` record exists for a
bootstrap/uninstall action — `core/audit` is wired into `api/`
middleware and `core/reconcile`'s engine (request-scoped, DB-backed),
neither of which is architecturally reachable from a standalone,
pre-cluster host action with no control-plane database in reach yet.
Worth resolving whenever a "report bootstrap outcome to the control
plane" flow is designed (a natural extension of spec 004's `register`,
once there's a control plane to report to at bootstrap time) — not a
gap this spec's `api/`/`core/`-untouched scope could have closed.
