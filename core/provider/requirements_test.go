package provider_test

import (
	"testing"

	"github.com/serendip-infra/serendip-infra/core/provider"
)

func TestRequirementsValidate(t *testing.T) {
	ok := provider.Requirements{IdleMemoryMiB: 46, IdleCPUMillicores: 10, PeakMemoryMiB: 300, PeakCPUMillicores: 1600,
		MeasuredOn: "edge-1, 2026-10-30", Results: "profiles/edge-1/bench/results/x"}
	for _, tc := range []struct {
		name  string
		edit  func(*provider.Requirements)
		valid bool
	}{
		{"measured", func(*provider.Requirements) {}, true},
		{"zero value", func(r *provider.Requirements) { *r = provider.Requirements{} }, false},
		{"negative", func(r *provider.Requirements) { r.IdleCPUMillicores = -1 }, false},
		{"peak below idle", func(r *provider.Requirements) { r.PeakMemoryMiB = 10 }, false},
		{"no place", func(r *provider.Requirements) { r.MeasuredOn = "" }, false},
		{"no results", func(r *provider.Requirements) { r.Results = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := ok
			tc.edit(&r)
			if err := r.Validate(); (err == nil) != tc.valid {
				t.Errorf("Validate() = %v, want valid %v", err, tc.valid)
			}
		})
	}
}
