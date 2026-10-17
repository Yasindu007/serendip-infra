package main

import (
	"math"
	"reflect"
	"testing"
)

const fixtureSummary = `{
  "node": {
    "nodeName": "serendip-012-control-plane",
    "cpu": {"usageNanoCores": 250000000},
    "memory": {"workingSetBytes": 2147483648},
    "fs": {"usedBytes": 10737418240, "capacityBytes": 107374182400},
    "runtime": {"imageFs": {"usedBytes": 5368709120}}
  },
  "pods": [
    {"podRef": {"name": "kgateway-abc", "namespace": "kgateway-system"},
     "cpu": {"usageNanoCores": 2000000}, "memory": {"workingSetBytes": 52428800}},
    {"podRef": {"name": "serendip-external-xyz", "namespace": "kgateway-system"},
     "cpu": {"usageNanoCores": 8000000}, "memory": {"workingSetBytes": 20971520}},
    {"podRef": {"name": "openbao-0", "namespace": "serendip-system"},
     "containers": [
       {"name": "openbao", "cpu": {"usageNanoCores": 3000000}, "memory": {"workingSetBytes": 31457280}},
       {"name": "sidecar", "cpu": {"usageNanoCores": 1000000}, "memory": {"workingSetBytes": 1048576}}
     ]},
    {"podRef": {"name": "cmp-x", "namespace": "dp-openchoreo-co-app-1-development-2"},
     "cpu": {"usageNanoCores": 1000000}, "memory": {"workingSetBytes": 4194304}},
    {"podRef": {"name": "mystery", "namespace": "surprise"},
     "cpu": {"usageNanoCores": 0}, "memory": {"workingSetBytes": 1048576}}
  ]
}`

func TestParseSummaryAndSample(t *testing.T) {
	s, err := parseSummary([]byte(fixtureSummary))
	if err != nil {
		t.Fatal(err)
	}
	smp := sampleFrom(s)

	gw := smp.Groups[GroupGateway]
	if gw.NanoCores != 10000000 || gw.WorkingSetBytes != 73400320 || len(gw.Pods) != 2 {
		t.Errorf("gateway group = %+v", gw)
	}
	// Pod without pod-level stats falls back to summing its containers.
	if sec := smp.Groups[GroupSecrets]; sec.NanoCores != 4000000 || sec.WorkingSetBytes != 32505856 {
		t.Errorf("secrets group = %+v, want container sums", sec)
	}
	if _, ok := smp.Groups[GroupWorkloads]; !ok {
		t.Error("dp-* namespace not grouped as workloads")
	}
	if _, ok := smp.Groups["Other: surprise"]; !ok {
		t.Errorf("unknown namespace not reported by name; groups = %v", smp.Groups)
	}
	if smp.NodeNanoCores != 250000000 || smp.NodeWorkingSetBytes != 2147483648 || smp.ImageFsUsedBytes != 5368709120 {
		t.Errorf("node totals = %+v", smp)
	}
}

func TestGroupFor(t *testing.T) {
	tests := map[string]string{
		"kube-system":              GroupKubernetes,
		"local-path-storage":       GroupKubernetes,
		"serendip-system":          GroupSecrets,
		"cert-manager":             GroupCertificates,
		"openchoreo-control-plane": GroupPlatformCtrl,
		"openchoreo-data-plane":    GroupPlatformData,
		"kgateway-system":          GroupGateway,
		"serendip-identity":        GroupIdentity,
		"dp-anything":              GroupWorkloads,
		"default":                  "Other: default",
	}
	for ns, want := range tests {
		if got := groupFor(ns); got != want {
			t.Errorf("groupFor(%q) = %q, want %q", ns, got, want)
		}
	}
}

func TestAggregate(t *testing.T) {
	mk := func(cpuNano, memBytes uint64) Sample {
		return Sample{
			Groups:              map[string]GroupUsage{GroupGateway: {NanoCores: cpuNano, WorkingSetBytes: memBytes}},
			NodeNanoCores:       cpuNano * 10,
			NodeWorkingSetBytes: memBytes * 10,
		}
	}
	samples := []Sample{mk(10_000_000, 100<<20), mk(30_000_000, 200<<20)}
	samples = append(samples, Sample{Groups: map[string]GroupUsage{}}) // group absent: counts as zero

	agg := aggregate(samples)
	gw := agg.Groups[GroupGateway]
	if !approxEqual(gw.CPUMillicores.Mean, 40.0/3) || gw.CPUMillicores.Max != 30 {
		t.Errorf("cpu = %+v, want mean 13.33, max 30", gw.CPUMillicores)
	}
	if !approxEqual(gw.MemoryMiB.Mean, 100) || gw.MemoryMiB.Max != 200 {
		t.Errorf("mem = %+v, want mean 100, max 200", gw.MemoryMiB)
	}
	if agg.NodeCPUMillicores.Max != 300 {
		t.Errorf("node cpu max = %v", agg.NodeCPUMillicores.Max)
	}
}

func TestSortedGroups(t *testing.T) {
	groups := map[string]GroupAggregate{"Other: z": {}, GroupGateway: {}, "Other: a": {}, GroupKubernetes: {}}
	want := []string{GroupKubernetes, GroupGateway, "Other: a", "Other: z"}
	if got := sortedGroups(groups); !reflect.DeepEqual(got, want) {
		t.Errorf("sortedGroups = %v, want %v", got, want)
	}
}

func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
