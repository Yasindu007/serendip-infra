package resource

const (
	KindSite    Kind = "Site"
	KindCluster Kind = "Cluster"
	KindNode    Kind = "Node"
)

// SiteSpec describes a physical or logical location (resource-model.md:
// "a room, a cloud region, a colo") that groups Clusters by proximity.
type SiteSpec struct {
	Location string
}

type SiteStatus struct {
	Status
}

type Site = Resource[SiteSpec, SiteStatus]

// ClusterSpec references the Provider (core/resource/capability.go) that
// manages this cluster's compute capability -- by ID through the Provider
// Registry, never a hardcoded vendor name (constitution rule 1).
type ClusterSpec struct {
	ProviderID string
}

type ClusterStatus struct {
	Status
}

type Cluster = Resource[ClusterSpec, ClusterStatus]

// NodeSpec has no fields yet -- a Node's meaningful data is what the
// bootstrap agent observes (NodeStatus), not anything declared up front.
type NodeSpec struct{}

// NodeStatus carries hardware facts reported by serendip-agent
// (docs/architecture/bootstrap-agent.md "Hardware discovery"). Nothing
// populates these fields in spec 001 -- that's spec 004's job; the shape
// exists now so the topology planner and later specs have a stable target.
type NodeStatus struct {
	Status
	CPUCores              int
	MemoryBytes           int64
	DiskType              string // e.g. "nvme", "ssd", "hdd" -- reported value, not a fixed enum
	DiskBytes             int64
	NetworkMbps           int
	VirtualizationSupport bool
}

type Node = Resource[NodeSpec, NodeStatus]
