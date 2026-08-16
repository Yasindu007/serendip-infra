package resource_test

import (
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestInfra_KindConstants(t *testing.T) {
	tests := []struct {
		kind resource.Kind
		want string
	}{
		{resource.KindSite, "Site"},
		{resource.KindCluster, "Cluster"},
		{resource.KindNode, "Node"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.kind) != tt.want {
				t.Errorf("kind = %q, want %q", tt.kind, tt.want)
			}
		})
	}
}

func TestCluster_ReferencesProviderByID(t *testing.T) {
	cluster := resource.Cluster{
		Metadata: resource.Metadata{Name: "primary", Kind: resource.KindCluster},
		Spec:     resource.ClusterSpec{ProviderID: "provider-abc123"},
	}
	if err := resource.Validate(cluster.Metadata); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if cluster.Spec.ProviderID == "" {
		t.Error("ProviderID should not be empty")
	}
}

func TestNode_HardwareFactsShape(t *testing.T) {
	// Nothing populates these in spec 001 (bootstrap-agent's job, spec
	// 004) -- this just pins the shape so a future spec can't silently
	// change field names/types underneath it.
	node := resource.Node{
		Metadata: resource.Metadata{Name: "node-1", Kind: resource.KindNode},
		Status: resource.NodeStatus{
			CPUCores:              4,
			MemoryBytes:           16 << 30,
			DiskType:              "nvme",
			DiskBytes:             512 << 30,
			NetworkMbps:           1000,
			VirtualizationSupport: true,
		},
	}
	if node.Status.CPUCores != 4 {
		t.Errorf("CPUCores = %d, want 4", node.Status.CPUCores)
	}
	if !node.Status.VirtualizationSupport {
		t.Error("VirtualizationSupport = false, want true")
	}
}
