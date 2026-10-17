package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/serendip-infra/serendip-infra/providers/application/openchoreo"
	"github.com/serendip-infra/serendip-infra/providers/gateway/kgateway"
	"github.com/serendip-infra/serendip-infra/providers/identity/thunderid"
	"github.com/serendip-infra/serendip-infra/providers/secrets/openbao"
)

// Summary is the subset of the kubelet Summary API
// (/api/v1/nodes/{node}/proxy/stats/summary) the benchmark reads.
// workingSetBytes is the kubelet's own "memory in use" figure (what it
// evicts on), so it's the honest number to report.
type Summary struct {
	Node struct {
		NodeName string      `json:"nodeName"`
		CPU      cpuStats    `json:"cpu"`
		Memory   memoryStats `json:"memory"`
		Fs       fsStats     `json:"fs"`
		Runtime  struct {
			ImageFs fsStats `json:"imageFs"`
		} `json:"runtime"`
	} `json:"node"`
	Pods []podStats `json:"pods"`
}

type cpuStats struct {
	UsageNanoCores *uint64 `json:"usageNanoCores"`
}

type memoryStats struct {
	WorkingSetBytes *uint64 `json:"workingSetBytes"`
}

type fsStats struct {
	UsedBytes     *uint64 `json:"usedBytes"`
	CapacityBytes *uint64 `json:"capacityBytes"`
}

type podStats struct {
	PodRef struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"podRef"`
	CPU        cpuStats         `json:"cpu"`
	Memory     memoryStats      `json:"memory"`
	Containers []containerStats `json:"containers"`
}

type containerStats struct {
	Name   string      `json:"name"`
	CPU    cpuStats    `json:"cpu"`
	Memory memoryStats `json:"memory"`
}

func parseSummary(data []byte) (Summary, error) {
	var s Summary
	if err := json.Unmarshal(data, &s); err != nil {
		return Summary{}, fmt.Errorf("decode kubelet summary: %w", err)
	}
	return s, nil
}

// Component groups, by the namespaces Serendip's providers deploy into.
// Namespaces come from the providers' own constants where they're exported,
// so a rename can't silently miscount.
const (
	GroupKubernetes     = "Kubernetes system"
	GroupSecrets        = "Secrets (OpenBao)"
	GroupCertificates   = "Certificates (cert-manager)"
	GroupPlatformCtrl   = "Application platform: control plane"
	GroupPlatformData   = "Application platform: data plane"
	GroupGateway        = "Gateway (kgateway)"
	GroupIdentity       = "Identity (ThunderID)"
	GroupWorkloads      = "Workloads"
	groupOtherPrefix    = "Other: "
	cellNamespacePrefix = "dp-"
)

var namespaceGroups = map[string]string{
	"kube-system":                    GroupKubernetes,
	"kube-node-lease":                GroupKubernetes,
	"kube-public":                    GroupKubernetes,
	"local-path-storage":             GroupKubernetes, // kind's provisioner
	openbao.Namespace:                GroupSecrets,
	"cert-manager":                   GroupCertificates, // openchoreo's constant is unexported
	openchoreo.ControlPlaneNamespace: GroupPlatformCtrl,
	openchoreo.DataPlaneNamespace:    GroupPlatformData,
	kgateway.Namespace:               GroupGateway,
	thunderid.Namespace:              GroupIdentity,
}

// groupFor attributes a namespace to a group. Unknown namespaces are
// reported under their own name rather than hidden in a catch-all.
func groupFor(namespace string) string {
	if g, ok := namespaceGroups[namespace]; ok {
		return g
	}
	if strings.HasPrefix(namespace, cellNamespacePrefix) {
		return GroupWorkloads
	}
	return groupOtherPrefix + namespace
}

// podUsage returns a pod's CPU nanocores and working-set bytes, preferring
// the pod-level figures and falling back to summing its containers.
func podUsage(p podStats) (nanoCores, workingSet uint64) {
	if p.CPU.UsageNanoCores != nil {
		nanoCores = *p.CPU.UsageNanoCores
	} else {
		for _, c := range p.Containers {
			if c.CPU.UsageNanoCores != nil {
				nanoCores += *c.CPU.UsageNanoCores
			}
		}
	}
	if p.Memory.WorkingSetBytes != nil {
		workingSet = *p.Memory.WorkingSetBytes
	} else {
		for _, c := range p.Containers {
			if c.Memory.WorkingSetBytes != nil {
				workingSet += *c.Memory.WorkingSetBytes
			}
		}
	}
	return nanoCores, workingSet
}

// GroupUsage is one group's usage in one sample.
type GroupUsage struct {
	NanoCores       uint64     `json:"nanoCores"`
	WorkingSetBytes uint64     `json:"workingSetBytes"`
	Pods            []PodUsage `json:"pods"`
}

// PodUsage is one pod's usage in one sample, so every group total can be
// traced to the pods behind it.
type PodUsage struct {
	Name            string `json:"name"`
	NanoCores       uint64 `json:"nanoCores"`
	WorkingSetBytes uint64 `json:"workingSetBytes"`
}

// Sample is one point-in-time reading of the cluster.
type Sample struct {
	Groups              map[string]GroupUsage `json:"groups"`
	NodeNanoCores       uint64                `json:"nodeNanoCores"`
	NodeWorkingSetBytes uint64                `json:"nodeWorkingSetBytes"`
	NodeFsUsedBytes     uint64                `json:"nodeFsUsedBytes"`
	ImageFsUsedBytes    uint64                `json:"imageFsUsedBytes"`
}

func sampleFrom(s Summary) Sample {
	out := Sample{Groups: map[string]GroupUsage{}}
	for _, p := range s.Pods {
		cpu, mem := podUsage(p)
		g := groupFor(p.PodRef.Namespace)
		u := out.Groups[g]
		u.NanoCores += cpu
		u.WorkingSetBytes += mem
		u.Pods = append(u.Pods, PodUsage{Name: p.PodRef.Namespace + "/" + p.PodRef.Name, NanoCores: cpu, WorkingSetBytes: mem})
		out.Groups[g] = u
	}
	for g, u := range out.Groups {
		sort.Slice(u.Pods, func(i, j int) bool { return u.Pods[i].Name < u.Pods[j].Name })
		out.Groups[g] = u
	}
	out.NodeNanoCores = deref(s.Node.CPU.UsageNanoCores)
	out.NodeWorkingSetBytes = deref(s.Node.Memory.WorkingSetBytes)
	out.NodeFsUsedBytes = deref(s.Node.Fs.UsedBytes)
	out.ImageFsUsedBytes = deref(s.Node.Runtime.ImageFs.UsedBytes)
	return out
}

func deref(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}

// Stat is a mean and max over samples.
type Stat struct {
	Mean float64 `json:"mean"`
	Max  float64 `json:"max"`
}

func statOf(values []float64) Stat {
	if len(values) == 0 {
		return Stat{}
	}
	var sum, max float64
	for i, v := range values {
		sum += v
		if i == 0 || v > max {
			max = v
		}
	}
	return Stat{Mean: sum / float64(len(values)), Max: max}
}

// GroupAggregate is one group's usage across all samples, in reporting
// units: millicores (1000 = one full CPU thread) and MiB.
type GroupAggregate struct {
	CPUMillicores Stat `json:"cpuMillicores"`
	MemoryMiB     Stat `json:"memoryMiB"`
}

// Aggregate is a set of samples reduced to per-group and node statistics.
// NodeOverhead is the node total minus every pod: kubelet, the container
// runtime, and system processes -- and on K3s also the API server and
// datastore, which run in the k3s process rather than as pods.
type Aggregate struct {
	Groups                map[string]GroupAggregate `json:"groups"`
	Pods                  []PodAggregate            `json:"pods"`
	NodeCPUMillicores     Stat                      `json:"nodeCpuMillicores"`
	NodeMemoryMiB         Stat                      `json:"nodeMemoryMiB"`
	NodeOverheadMiB       Stat                      `json:"nodeOverheadMiB"`
	NodeOverheadMillicore Stat                      `json:"nodeOverheadMillicores"`
	NodeFsUsedMiB         float64                   `json:"nodeFsUsedMiB"`
	ImageFsUsedMiB        float64                   `json:"imageFsUsedMiB"`
}

// PodAggregate is one pod's usage across samples.
type PodAggregate struct {
	Name          string `json:"name"`
	Group         string `json:"group"`
	MemoryMiB     Stat   `json:"memoryMiB"`
	CPUMillicores Stat   `json:"cpuMillicores"`
}

func millicores(nano uint64) float64 { return float64(nano) / 1e6 }
func mib(b uint64) float64           { return float64(b) / (1 << 20) }

// aggregate reduces samples to statistics. A group missing from a sample
// (e.g. pods not yet scheduled) counts as zero for that sample.
func aggregate(samples []Sample) Aggregate {
	groups := map[string]bool{}
	for _, s := range samples {
		for g := range s.Groups {
			groups[g] = true
		}
	}
	out := Aggregate{Groups: map[string]GroupAggregate{}}
	for g := range groups {
		var cpu, mem []float64
		for _, s := range samples {
			u := s.Groups[g]
			cpu = append(cpu, millicores(u.NanoCores))
			mem = append(mem, mib(u.WorkingSetBytes))
		}
		out.Groups[g] = GroupAggregate{CPUMillicores: statOf(cpu), MemoryMiB: statOf(mem)}
	}
	var nodeCPU, nodeMem, overMem, overCPU []float64
	type podSeries struct {
		group    string
		cpu, mem []float64
	}
	pods := map[string]*podSeries{}
	for _, s := range samples {
		if s.NodeNanoCores > 0 { // the kubelet omits CPU on its first sample; a node never idles at exactly zero
			nodeCPU = append(nodeCPU, millicores(s.NodeNanoCores))
		}
		nodeMem = append(nodeMem, mib(s.NodeWorkingSetBytes))
		var podMem, podCPU uint64
		for g, u := range s.Groups {
			podMem += u.WorkingSetBytes
			podCPU += u.NanoCores
			for _, p := range u.Pods {
				ps := pods[p.Name]
				if ps == nil {
					ps = &podSeries{group: g}
					pods[p.Name] = ps
				}
				ps.cpu = append(ps.cpu, millicores(p.NanoCores))
				ps.mem = append(ps.mem, mib(p.WorkingSetBytes))
			}
		}
		overMem = append(overMem, mib(s.NodeWorkingSetBytes)-mib(podMem))
		overCPU = append(overCPU, millicores(s.NodeNanoCores)-millicores(podCPU))
	}
	out.NodeCPUMillicores = statOf(nodeCPU)
	out.NodeMemoryMiB = statOf(nodeMem)
	out.NodeOverheadMiB = statOf(overMem)
	out.NodeOverheadMillicore = statOf(overCPU)
	for name, ps := range pods {
		out.Pods = append(out.Pods, PodAggregate{Name: name, Group: ps.group, MemoryMiB: statOf(ps.mem), CPUMillicores: statOf(ps.cpu)})
	}
	sort.Slice(out.Pods, func(i, j int) bool {
		if out.Pods[i].MemoryMiB.Mean != out.Pods[j].MemoryMiB.Mean {
			return out.Pods[i].MemoryMiB.Mean > out.Pods[j].MemoryMiB.Mean
		}
		return out.Pods[i].Name < out.Pods[j].Name
	})
	if n := len(samples); n > 0 {
		out.NodeFsUsedMiB = mib(samples[n-1].NodeFsUsedBytes)
		out.ImageFsUsedMiB = mib(samples[n-1].ImageFsUsedBytes)
	}
	return out
}

// sortedGroups returns group names in a stable reporting order: known
// groups first in a fixed order, then "Other: ..." alphabetically.
func sortedGroups(groups map[string]GroupAggregate) []string {
	order := []string{GroupKubernetes, GroupSecrets, GroupCertificates, GroupPlatformCtrl, GroupPlatformData, GroupGateway, GroupIdentity, GroupWorkloads}
	var out []string
	seen := map[string]bool{}
	for _, g := range order {
		if _, ok := groups[g]; ok {
			out = append(out, g)
			seen[g] = true
		}
	}
	var rest []string
	for g := range groups {
		if !seen[g] {
			rest = append(rest, g)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}
