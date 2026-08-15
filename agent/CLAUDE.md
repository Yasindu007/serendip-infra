# agent/ — serendip-agent (node bootstrap agent)

Single static Go binary that lives on the host OUTSIDE Kubernetes.
Responsibilities: hardware discovery (CPU/RAM/disks/network/virt), node
identity, K3s bootstrap/join (ADR 0001), health reporting, emergency
diagnostics, low-level upgrades.

Hard rules:
- Zero heavy deps; must build CGO-free and run on a bare Debian/Ubuntu host.
- Must function when the cluster is down — never assume the API server exists.
- Target: <30MB binary, <50MB RSS idle.
- All host mutations (installing K3s, formatting a disk) are explicit
  subcommands with --confirm; discovery is always safe/read-only.
