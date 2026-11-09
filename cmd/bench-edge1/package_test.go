package main

import (
	"strings"
	"testing"
)

func TestPackageCost(t *testing.T) {
	agg := func(memMean, memMax, cpuMean, cpuMax, nodeMem, nodeMemMax, nodeCPU, nodeCPUMax float64) Aggregate {
		return Aggregate{
			Groups: map[string]GroupAggregate{GroupBuilds: {
				MemoryMiB: Stat{Mean: memMean, Max: memMax}, CPUMillicores: Stat{Mean: cpuMean, Max: cpuMax},
			}},
			NodeMemoryMiB: Stat{Mean: nodeMem, Max: nodeMemMax}, NodeCPUMillicores: Stat{Mean: nodeCPU, Max: nodeCPUMax},
		}
	}
	platform := Aggregate{NodeMemoryMiB: Stat{Mean: 1600}, NodeCPUMillicores: Stat{Mean: 250}}
	tests := []struct {
		name string
		run  PackageRun
		want PackageCost
	}{
		{
			name: "the node's use beyond platform, idle the larger of before and after the builds, rounded up",
			run: PackageRun{
				Idle:   agg(40.2, 41, 1.1, 3, 2300.5, 2400, 266, 400),
				Poller: &PollerRun{Cluster: agg(46.5, 47, 0.9, 2, 2550.2, 2600, 260, 400)},
				Builds: []BuildRun{
					{Cluster: agg(150, 230.4, 900, 1600.2, 2700, 3250.3, 900, 1990)},
					{Cluster: agg(140, 210, 800, 1700, 2800, 3300, 950, 2000)},
				},
			},
			want: PackageCost{
				CostFigures: CostFigures{IdleMemoryMiB: 951, IdleCPUMillicores: 16, PeakMemoryMiB: 1700, PeakCPUMillicores: 1750},
				Own:         CostFigures{IdleMemoryMiB: 47, IdleCPUMillicores: 2, PeakMemoryMiB: 231, PeakCPUMillicores: 1700},
			},
		},
		{
			name: "never below the package's own pods, and peak never below idle, even with no builds",
			run:  PackageRun{Idle: agg(40, 41, 2, 3, 1610, 1620, 240, 300)},
			want: PackageCost{
				CostFigures: CostFigures{IdleMemoryMiB: 40, IdleCPUMillicores: 2, PeakMemoryMiB: 40, PeakCPUMillicores: 2},
				Own:         CostFigures{IdleMemoryMiB: 40, IdleCPUMillicores: 2, PeakMemoryMiB: 40, PeakCPUMillicores: 2},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := packageCost(tc.run, platform); got != tc.want {
				t.Errorf("packageCost = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRenderSummaryIncludesThePackage(t *testing.T) {
	if strings.Contains(renderSummary(Result{}), "## Package:") {
		t.Fatal("a run without a package rendered a package section")
	}
	r := Result{Package: &PackageRun{Provider: "openchoreo-workflow-plane", Cost: PackageCost{CostFigures: CostFigures{IdleMemoryMiB: 34, PeakMemoryMiB: 313}}}}
	got := renderSummary(r)
	for _, want := range []string{"## Package: openchoreo-workflow-plane", "### Stated cost (Requirements)", "| 34 MiB |", "| 313 MiB |"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q", want)
		}
	}
}
