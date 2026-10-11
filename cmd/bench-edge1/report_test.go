package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHeadroomThresholds(t *testing.T) {
	budget := float64(referenceRAMGiB-hostOSReserveGiB) * 1024 // 14336 MiB
	tests := []struct {
		platform float64
		want     string
	}{
		{budget * 0.50, "fits 16 GB comfortably"},
		{budget*0.50 + 1, "fits 16 GB, but tight"},
		{budget * 0.75, "fits 16 GB, but tight"},
		{budget*0.75 + 1, "points to 32 GB"},
	}
	for _, tt := range tests {
		if got := headroom(tt.platform, 0).Verdict; got != tt.want {
			t.Errorf("headroom(%.0f MiB) = %q, want %q", tt.platform, got, tt.want)
		}
	}
	h := headroom(2000, 30)
	if !approxEqual(h.ProjectedMiB, 2300) || !approxEqual(h.BudgetMiB, 14336) {
		t.Errorf("headroom arithmetic = %+v", h)
	}
}

func TestWorkloadDelta(t *testing.T) {
	g := func(mem float64) GroupAggregate { return GroupAggregate{MemoryMiB: Stat{Mean: mem}} }
	p := Aggregate{Groups: map[string]GroupAggregate{
		GroupKubernetes: g(800), GroupGateway: g(50), GroupPlatformCtrl: g(54), GroupCertificates: g(65),
	}}
	w := Aggregate{Groups: map[string]GroupAggregate{
		GroupKubernetes:   g(750), // GC swing: excluded
		GroupGateway:      g(53),  // +3 counted
		GroupPlatformCtrl: g(60),  // +6 counted
		GroupCertificates: g(62),  // shrank: not counted
		GroupWorkloads:    g(12),
	}}
	if got := workloadDeltaMiB(p, w, 3); !approxEqual(got, 7) {
		t.Errorf("delta = %v, want (12 + 3 + 6) / 3 = 7", got)
	}
	if got := workloadDeltaMiB(p, w, 0); got != 0 {
		t.Errorf("zero components = %v, want 0", got)
	}
}

func TestRenderSummary_OverheadAndTopPods(t *testing.T) {
	agg := aggregate([]Sample{{
		Groups: map[string]GroupUsage{GroupGateway: {
			NanoCores: 2_000_000, WorkingSetBytes: 100 << 20,
			Pods: []PodUsage{{Name: "kgateway-system/proxy", NanoCores: 2_000_000, WorkingSetBytes: 100 << 20}},
		}},
		NodeNanoCores: 10_000_000, NodeWorkingSetBytes: 300 << 20,
	}})
	if !approxEqual(agg.NodeOverheadMiB.Mean, 200) || !approxEqual(agg.NodeOverheadMillicore.Mean, 8) {
		t.Errorf("overhead = %+v / %+v, want 200 MiB / 8 mcores", agg.NodeOverheadMiB, agg.NodeOverheadMillicore)
	}
	if len(agg.Pods) != 1 || agg.Pods[0].Name != "kgateway-system/proxy" || agg.Pods[0].Group != GroupGateway {
		t.Errorf("pods = %+v", agg.Pods)
	}
	out := renderSummary(Result{Platform: agg})
	if !strings.Contains(out, "Node overhead outside pods") || !strings.Contains(out, "| kgateway-system/proxy |") {
		t.Errorf("summary lacks overhead row or top-pods table:\n%s", out)
	}
}

func TestRule4(t *testing.T) {
	r := checkRule4(ProcessStats{RSSMiB: Stat{Mean: 30}, CPUMillicores: Stat{Mean: 20}}, 2)
	if !r.RSSPass || !r.CPUPass || !approxEqual(r.CPUPercent, 1) {
		t.Errorf("rule4 = %+v, want pass at 1%% of 2 cores", r)
	}
	r = checkRule4(ProcessStats{RSSMiB: Stat{Mean: 300}, CPUMillicores: Stat{Mean: 120}}, 2)
	if r.RSSPass || r.CPUPass {
		t.Errorf("rule4 = %+v, want both FAIL", r)
	}
}

func resultWith(groups map[string]GroupAggregate) Result {
	return Result{Platform: Aggregate{Groups: groups}}
}

func TestCompareRuns(t *testing.T) {
	g := func(mem, cpu float64) GroupAggregate {
		return GroupAggregate{MemoryMiB: Stat{Mean: mem}, CPUMillicores: Stat{Mean: cpu}}
	}
	a := resultWith(map[string]GroupAggregate{
		GroupPlatformCtrl: g(500, 40),
		GroupGateway:      g(70, 10),
		GroupCertificates: g(20, 2),
		"Other: gone":     g(5, 0),
	})
	b := resultWith(map[string]GroupAggregate{
		GroupPlatformCtrl: g(545, 64), // +9% mem OK; +24 mcores OK
		GroupGateway:      g(78, 40),  // +11.4% mem FAIL; +30 mcores FAIL
		GroupCertificates: g(31, 2),   // +11 MiB on a small group FAIL
	})
	got := map[string]GroupComparison{}
	for _, c := range compareRuns(a, b) {
		got[c.Group] = c
	}
	if c := got[GroupPlatformCtrl]; !c.MemOK || !c.CPUOK {
		t.Errorf("platform ctrl = %+v, want both OK", c)
	}
	if c := got[GroupGateway]; c.MemOK || c.CPUOK {
		t.Errorf("gateway = %+v, want both FAIL", c)
	}
	if c := got[GroupCertificates]; c.MemOK {
		t.Errorf("certificates = %+v, want mem FAIL (+11 MiB on a small group)", c)
	}
	if c := got["Other: gone"]; c.MemOK || c.CPUOK {
		t.Errorf("group missing from run B = %+v, want FAIL", c)
	}
	if out := renderComparison(compareRuns(a, b)); !strings.Contains(out, "Overall: **FAIL**") {
		t.Errorf("comparison render missing overall FAIL:\n%s", out)
	}
}

func TestRenderSummary_SimulationBannerAndNoVerifiedClaim(t *testing.T) {
	r := Result{
		StartedAt:  time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC),
		Simulation: Simulation{CPUs: 2, CPUSet: "0-3", NotSimulated: []string{"memory limit"}},
		Platform:   Aggregate{Groups: map[string]GroupAggregate{GroupGateway: {}}},
		Headroom:   headroom(3000, 20),
		Timings:    Timings{ColdStart: Duration(5 * time.Minute), TimeToServing: []Duration{Duration(30 * time.Second)}},
	}
	out := renderSummary(r)
	if !strings.HasPrefix(out, "> **"+SimulationBanner) {
		t.Errorf("simulated summary must open with the banner:\n%.200s", out)
	}
	if strings.Contains(strings.ToLower(out), "verified") {
		t.Error("summary must never claim a footprint is verified")
	}
	if !strings.Contains(out, "Not simulated: memory limit") {
		t.Error("summary omits what was not simulated")
	}

	r.Simulation = Simulation{}
	if strings.Contains(renderSummary(r), SimulationBanner) {
		t.Error("non-simulated summary carries the simulation banner")
	}
}

func TestDurationJSONRoundTrip(t *testing.T) {
	in := Timings{ColdStart: Duration(90500 * time.Millisecond)}
	raw, _ := json.Marshal(in)
	if !strings.Contains(string(raw), `"coldStart":90.5`) {
		t.Errorf("marshal = %s", raw)
	}
	var out Timings
	if err := json.Unmarshal(raw, &out); err != nil || out.ColdStart != in.ColdStart {
		t.Errorf("round trip = %+v, %v", out, err)
	}
}

func TestRule4_NoProcessDataIsNotEvaluatedNotPass(t *testing.T) {
	r := checkRule4(ProcessStats{}, 2)
	if r.RSSPass || r.CPUPass {
		t.Errorf("rule4 with no samples = %+v, must not PASS", r)
	}
	out := renderSummary(Result{Rule4: r, Headroom: Headroom{Verdict: "not computed: no light-workload data"}})
	if !strings.Contains(out, "NOT EVALUATED") || strings.Contains(out, "**PASS**") {
		t.Errorf("summary should say NOT EVALUATED and never PASS:\n%s", out)
	}
	if !strings.Contains(out, "not computed: no light-workload data") {
		t.Errorf("summary should carry the not-computed headroom verdict:\n%s", out)
	}
}
