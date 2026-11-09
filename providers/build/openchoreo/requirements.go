package openchoreo

import "github.com/serendip-infra/serendip-infra/core/provider"

// Requirements is this package's measured cost (specs/022-builds plan
// decision 4), which the package catalogue points at. The figures are
// the node's use beyond the same run's platform without the package, not
// only the package's own pods, so they include what the package makes
// kube-apiserver and the container runtime hold (T013, RP-046).
var Requirements = &provider.Requirements{
	IdleMemoryMiB:     453,
	IdleCPUMillicores: 19,
	PeakMemoryMiB:     1129,
	PeakCPUMillicores: 1787,
	MeasuredOn:        "edge-1 simulated T470 (kind, 2 CPUs of quota on 4 threads), 2026-11-09",
	Results:           "profiles/edge-1/bench/results/2026-11-06-spec022-simulated-t470/package",
	Note: "Idle, the builds package adds about 450 MiB to the node: ~35 MiB for its own pods, " +
		"~50 MiB in kube-apiserver for Argo's CRDs, and ~320 MiB of container-runtime memory and cache " +
		"once builds have pulled their images. A build takes both cores of a T470 for 2–4 minutes and " +
		"peaks at about 1.1 GiB on the node, so other workloads slow down while one runs.",
}
