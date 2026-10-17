package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// SimulationBanner is printed on every report produced under simulated
// constraints. A simulation never satisfies the reference-hardware gate
// (constitution rules 6 and 9).
const SimulationBanner = "SIMULATION — does not satisfy the reference-hardware gate"

// Rule-4 control-plane budget (constitution rule 4).
const (
	controlPlaneRSSBudgetMiB = 256
	controlPlaneCPUBudgetPct = 5
	hostOSReserveGiB         = 2
	referenceRAMGiB          = 16
	projectedApps            = 10
	comfortableFraction      = 0.50
	tightFraction            = 0.75
)

// MachineProfile records where a result came from.
type MachineProfile struct {
	HostOS             string `json:"hostOS"`
	HostCPU            string `json:"hostCPU"`
	NodeName           string `json:"nodeName"`
	NodeKernel         string `json:"nodeKernel"`
	NodeOSImage        string `json:"nodeOSImage"`
	ContainerRuntime   string `json:"containerRuntime"`
	KubeletVersion     string `json:"kubeletVersion"`
	NodeCPUCapacity    string `json:"nodeCPUCapacity"`
	NodeMemoryCapacity string `json:"nodeMemoryCapacity"`
}

// Simulation describes constraints applied to approximate reference
// hardware. Zero value means "not a simulation".
type Simulation struct {
	CPUs         float64  `json:"cpus,omitempty"`
	CPUSet       string   `json:"cpuset,omitempty"`
	Note         string   `json:"note,omitempty"`
	NotSimulated []string `json:"notSimulated,omitempty"`
}

func (s Simulation) Active() bool { return s.CPUs > 0 || s.CPUSet != "" }

// ProcessStats is serendipd's own host-process footprint over the window.
type ProcessStats struct {
	RSSMiB        Stat `json:"rssMiB"`
	CPUMillicores Stat `json:"cpuMillicores"`
}

// Rule4 is the explicit control-plane budget check.
type Rule4 struct {
	RSSMiB        float64 `json:"rssMiB"`
	CPUPercent    float64 `json:"cpuPercent"`
	RSSPass       bool    `json:"rssPass"`
	CPUPass       bool    `json:"cpuPass"`
	CapacityCores float64 `json:"capacityCores"`
	Note          string  `json:"note"`
}

// Headroom is the 16 GB purchase-question answer (plan.md decision 7).
type Headroom struct {
	PlatformMiB     float64 `json:"platformMiB"`
	PerComponentMiB float64 `json:"perComponentMiB"`
	ProjectedMiB    float64 `json:"projectedMiB"`
	BudgetMiB       float64 `json:"budgetMiB"`
	Fraction        float64 `json:"fraction"`
	Verdict         string  `json:"verdict"`
}

// Timings are wall-clock durations of the run's phases.
type Timings struct {
	ColdStart     Duration   `json:"coldStart"`
	WarmRestart   Duration   `json:"warmRestart"`
	TimeToServing []Duration `json:"timeToServing"`
}

// Duration marshals as seconds for readability.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%.1f", time.Duration(d).Seconds())), nil
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var secs float64
	if _, err := fmt.Sscanf(string(b), "%g", &secs); err != nil {
		return err
	}
	*d = Duration(time.Duration(secs * float64(time.Second)))
	return nil
}

// Result is everything one benchmark run produced.
type Result struct {
	StartedAt       time.Time      `json:"startedAt"`
	Machine         MachineProfile `json:"machine"`
	Simulation      Simulation     `json:"simulation"`
	SettleSeconds   float64        `json:"settleSeconds"`
	SampleInterval  float64        `json:"sampleIntervalSeconds"`
	Components      int            `json:"workloadComponents"`
	Platform        Aggregate      `json:"platform"`
	Workload        Aggregate      `json:"workload"`
	PlatformSamples []Sample       `json:"platformSamples"`
	WorkloadSamples []Sample       `json:"workloadSamples"`
	Serendipd       ProcessStats   `json:"serendipd"`
	Rule4           Rule4          `json:"rule4"`
	Headroom        Headroom       `json:"headroom"`
	Timings         Timings        `json:"timings"`
	Findings        []string       `json:"findings,omitempty"`
}

func checkRule4(p ProcessStats, capacityCores float64) Rule4 {
	// No process data must never read as "zero usage, PASS".
	if p.RSSMiB.Mean == 0 {
		return Rule4{CapacityCores: capacityCores, Note: "not measured: no serendipd process samples"}
	}
	pct := 0.0
	if capacityCores > 0 {
		pct = p.CPUMillicores.Mean / (capacityCores * 1000) * 100
	}
	return Rule4{
		RSSMiB:        p.RSSMiB.Mean,
		CPUPercent:    pct,
		RSSPass:       p.RSSMiB.Mean < controlPlaneRSSBudgetMiB,
		CPUPass:       pct < controlPlaneCPUBudgetPct,
		CapacityCores: capacityCores,
		Note:          "covers serendipd only: serendip-agent is not a long-running daemon yet and is not measured",
	}
}

// headroom answers whether the idle platform plus projectedApps light
// Components fits a 16 GB machine after a host-OS reserve.
func headroom(platformMiB, perComponentMiB float64) Headroom {
	budget := float64(referenceRAMGiB-hostOSReserveGiB) * 1024
	projected := platformMiB + projectedApps*perComponentMiB
	frac := projected / budget
	verdict := "points to 32 GB"
	switch {
	case frac <= comfortableFraction:
		verdict = "fits 16 GB comfortably"
	case frac <= tightFraction:
		verdict = "fits 16 GB, but tight"
	}
	return Headroom{PlatformMiB: platformMiB, PerComponentMiB: perComponentMiB, ProjectedMiB: projected, BudgetMiB: budget, Fraction: frac, Verdict: verdict}
}

// workloadDeltaMiB is the per-Component memory cost: the workload pods
// themselves plus any growth in the platform groups that serve them
// (gateway routes, controller state), divided by the Component count.
// Kubernetes system is excluded: its garbage-collection swings between
// phases (tens of MiB) dwarf a light workload's real cost, which made the
// node-total difference unusable as a measure.
func workloadDeltaMiB(platform, workload Aggregate, components int) float64 {
	if components <= 0 {
		return 0
	}
	total := workload.Groups[GroupWorkloads].MemoryMiB.Mean
	for g, w := range workload.Groups {
		if g == GroupWorkloads || g == GroupKubernetes {
			continue
		}
		if d := w.MemoryMiB.Mean - platform.Groups[g].MemoryMiB.Mean; d > 0 {
			total += d
		}
	}
	return total / float64(components)
}

// Comparison tolerances (plan.md decision 8).
const (
	memRelTolerance      = 0.10
	memAbsToleranceMiB   = 10
	memRelThresholdMiB   = 50
	cpuAbsToleranceMilli = 25
)

// GroupComparison is one group's run-to-run agreement.
type GroupComparison struct {
	Group string  `json:"group"`
	MemA  float64 `json:"memMiBA"`
	MemB  float64 `json:"memMiBB"`
	CPUA  float64 `json:"cpuMilliA"`
	CPUB  float64 `json:"cpuMilliB"`
	MemOK bool    `json:"memOK"`
	CPUOK bool    `json:"cpuOK"`
	// New marks a group present only in run B: a component added between
	// the two runs (e.g. comparing across specs). It has nothing to agree
	// with, so it is reported but not part of the verdict.
	New bool `json:"new,omitempty"`
}

func memWithinTolerance(a, b float64) bool {
	if math.Max(a, b) >= memRelThresholdMiB {
		return math.Abs(a-b) <= memRelTolerance*math.Max(a, b)
	}
	return math.Abs(a-b) <= memAbsToleranceMiB
}

// compareRuns checks platform-idle agreement between two runs, group by
// group. A group missing from run B fails (something disappeared); a group
// only in run B is marked New and left out of the verdict.
func compareRuns(a, b Result) []GroupComparison {
	names := map[string]bool{}
	for g := range a.Platform.Groups {
		names[g] = true
	}
	for g := range b.Platform.Groups {
		names[g] = true
	}
	merged := map[string]GroupAggregate{}
	for g := range names {
		merged[g] = GroupAggregate{}
	}
	var out []GroupComparison
	for _, g := range sortedGroups(merged) {
		ga, okA := a.Platform.Groups[g]
		gb, okB := b.Platform.Groups[g]
		c := GroupComparison{Group: g, MemA: ga.MemoryMiB.Mean, MemB: gb.MemoryMiB.Mean, CPUA: ga.CPUMillicores.Mean, CPUB: gb.CPUMillicores.Mean}
		c.MemOK = okA && okB && memWithinTolerance(c.MemA, c.MemB)
		c.CPUOK = okA && okB && math.Abs(c.CPUA-c.CPUB) <= cpuAbsToleranceMilli
		c.New = !okA && okB
		out = append(out, c)
	}
	return out
}

func passFail(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// renderSummary renders one run as markdown.
func renderSummary(r Result) string {
	var b strings.Builder
	if r.Simulation.Active() {
		fmt.Fprintf(&b, "> **%s.**\n\n", SimulationBanner)
	}
	fmt.Fprintf(&b, "# edge-1 footprint — %s\n\n", r.StartedAt.Format("2006-01-02 15:04 MST"))

	b.WriteString("## Machine\n\n")
	m := r.Machine
	fmt.Fprintf(&b, "- Host: %s, %s\n- Node: %s (%s, kernel %s, %s, kubelet %s)\n- Node capacity reported by Kubernetes: %s CPU, %s memory\n",
		m.HostOS, m.HostCPU, m.NodeName, m.NodeOSImage, m.NodeKernel, m.ContainerRuntime, m.KubeletVersion, m.NodeCPUCapacity, m.NodeMemoryCapacity)
	if r.Simulation.Active() {
		fmt.Fprintf(&b, "- Simulated: %.1f CPUs of quota on cpuset %s. %s\n", r.Simulation.CPUs, r.Simulation.CPUSet, r.Simulation.Note)
		for _, n := range r.Simulation.NotSimulated {
			fmt.Fprintf(&b, "- Not simulated: %s\n", n)
		}
	}
	fmt.Fprintf(&b, "- Settle %.0fs, then %d samples every %.0fs\n\n", r.SettleSeconds, len(r.PlatformSamples), r.SampleInterval)

	writeGroupTable(&b, "Platform at idle (no workloads)", r.Platform)
	fmt.Fprintf(&b, "\n### serendipd (host process)\n\n| | Mean | Max |\n|---|---|---|\n| RSS (MiB) | %.1f | %.1f |\n| CPU (millicores) | %.1f | %.1f |\n\n",
		r.Serendipd.RSSMiB.Mean, r.Serendipd.RSSMiB.Max, r.Serendipd.CPUMillicores.Mean, r.Serendipd.CPUMillicores.Max)

	fmt.Fprintf(&b, "### Constitution rule 4 (control plane < %d MiB RSS, < %d%% CPU at idle)\n\n", controlPlaneRSSBudgetMiB, controlPlaneCPUBudgetPct)
	if r.Rule4.RSSMiB == 0 {
		fmt.Fprintf(&b, "- **NOT EVALUATED**: %s\n\n", r.Rule4.Note)
	} else {
		fmt.Fprintf(&b, "- RSS %.1f MiB: **%s**\n- CPU %.2f%% of %.1f cores: **%s**\n- %s\n\n",
			r.Rule4.RSSMiB, passFail(r.Rule4.RSSPass), r.Rule4.CPUPercent, r.Rule4.CapacityCores, passFail(r.Rule4.CPUPass), r.Rule4.Note)
	}

	if len(r.WorkloadSamples) > 0 || len(r.Workload.Groups) > 0 {
		writeGroupTable(&b, fmt.Sprintf("With %d light Components deployed", r.Components), r.Workload)
	}

	h := r.Headroom
	if h.BudgetMiB == 0 {
		fmt.Fprintf(&b, "\n### 16 GB headroom\n\n**Verdict: %s.**\n\n", h.Verdict)
	} else {
		fmt.Fprintf(&b, "\n### 16 GB headroom\n\nBudget = %d GB − %d GB host OS = %.0f MiB. Projected = platform %.0f MiB + %d × %.1f MiB per Component = %.0f MiB (%.0f%% of budget).\n\n**Verdict: %s.**\n\n",
			referenceRAMGiB, hostOSReserveGiB, h.BudgetMiB, h.PlatformMiB, projectedApps, h.PerComponentMiB, h.ProjectedMiB, h.Fraction*100, h.Verdict)
	}

	b.WriteString("### Timings\n\n")
	fmt.Fprintf(&b, "- Cold start (fresh cluster, includes image pulls): %.0fs\n- Warm restart (everything installed): %.0fs\n",
		time.Duration(r.Timings.ColdStart).Seconds(), time.Duration(r.Timings.WarmRestart).Seconds())
	var serving []string
	for _, d := range r.Timings.TimeToServing {
		serving = append(serving, fmt.Sprintf("%.0fs", time.Duration(d).Seconds()))
	}
	fmt.Fprintf(&b, "- Component create → Serving: %s (includes the image pull; status is re-checked about 2, 6, 14, 22 and 30s after create)\n", strings.Join(serving, ", "))

	if len(r.Findings) > 0 {
		b.WriteString("\n### Findings\n\n")
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	return b.String()
}

func writeGroupTable(b *strings.Builder, title string, a Aggregate) {
	fmt.Fprintf(b, "### %s\n\n| Group | Memory mean (MiB) | Memory max | CPU mean (mcores) | CPU max |\n|---|---|---|---|---|\n", title)
	for _, g := range sortedGroups(a.Groups) {
		s := a.Groups[g]
		fmt.Fprintf(b, "| %s | %.1f | %.1f | %.1f | %.1f |\n", g, s.MemoryMiB.Mean, s.MemoryMiB.Max, s.CPUMillicores.Mean, s.CPUMillicores.Max)
	}
	fmt.Fprintf(b, "| Node overhead outside pods (kubelet, container runtime, system) | %.1f | %.1f | %.1f | %.1f |\n", a.NodeOverheadMiB.Mean, a.NodeOverheadMiB.Max, a.NodeOverheadMillicore.Mean, a.NodeOverheadMillicore.Max)
	fmt.Fprintf(b, "| **Node total** | %.1f | %.1f | %.1f | %.1f |\n", a.NodeMemoryMiB.Mean, a.NodeMemoryMiB.Max, a.NodeCPUMillicores.Mean, a.NodeCPUMillicores.Max)
	fmt.Fprintf(b, "\nContainer images on disk: %.0f MiB. (Node filesystem used: %.0f MiB; on kind this is the whole Docker VM disk, not Serendip's.)\n", a.ImageFsUsedMiB, a.NodeFsUsedMiB)
	if len(a.Pods) > 0 {
		b.WriteString("\nLargest pods by memory:\n\n| Pod | Group | Memory mean (MiB) | CPU mean (mcores) |\n|---|---|---|---|\n")
		for i, p := range a.Pods {
			if i == topPods {
				break
			}
			fmt.Fprintf(b, "| %s | %s | %.1f | %.1f |\n", p.Name, p.Group, p.MemoryMiB.Mean, p.CPUMillicores.Mean)
		}
	}
}

const topPods = 10

// renderComparison renders a repeatability check as markdown.
func renderComparison(cs []GroupComparison) string {
	var b strings.Builder
	b.WriteString("| Group | Memory A | Memory B | Memory | CPU A | CPU B | CPU |\n|---|---|---|---|---|---|---|\n")
	allOK := true
	for _, c := range cs {
		if c.New {
			fmt.Fprintf(&b, "| %s | — | %.1f | new | — | %.1f | new |\n", c.Group, c.MemB, c.CPUB)
			continue
		}
		fmt.Fprintf(&b, "| %s | %.1f | %.1f | %s | %.1f | %.1f | %s |\n", c.Group, c.MemA, c.MemB, passFail(c.MemOK), c.CPUA, c.CPUB, passFail(c.CPUOK))
		allOK = allOK && c.MemOK && c.CPUOK
	}
	fmt.Fprintf(&b, "\nTolerance: memory ±%.0f%% for groups ≥ %d MiB, else ±%d MiB; CPU ±%d millicores. \"new\" groups exist only in run B and are not part of the verdict. Overall: **%s**\n",
		memRelTolerance*100, memRelThresholdMiB, memAbsToleranceMiB, cpuAbsToleranceMilli, passFail(allOK))
	return b.String()
}
