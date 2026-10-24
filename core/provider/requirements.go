package provider

import "errors"

// Requirements is a package's measured cost on reference hardware
// (specs/022-builds plan decision 4, RP-034, RP-037): what it uses idle,
// and at its peak while working (for builds, while a build runs). Each
// provider package declares its own value in one place, and the package
// catalogue points at it. A package can't be installed until it's stated.
type Requirements struct {
	IdleMemoryMiB     int `json:"idleMemoryMiB"`
	IdleCPUMillicores int `json:"idleCPUMillicores"`
	PeakMemoryMiB     int `json:"peakMemoryMiB"`
	PeakCPUMillicores int `json:"peakCPUMillicores"`
	// MeasuredOn is where and when the numbers were taken, such as
	// "edge-1 (ThinkPad T470, 16 GB), 2026-10-30".
	MeasuredOn string `json:"measuredOn"`
	// Results is the benchmark results folder the numbers come from.
	Results string `json:"results"`
	// Note says plainly how the package fits, written from the benchmark.
	Note string `json:"note,omitempty"`
}

// Validate checks the numbers are stated and traceable to a measurement.
func (r Requirements) Validate() error {
	switch {
	case r.IdleMemoryMiB < 0 || r.IdleCPUMillicores < 0 || r.PeakMemoryMiB < 0 || r.PeakCPUMillicores < 0:
		return errors.New("requirements can't be negative")
	case r.PeakMemoryMiB < r.IdleMemoryMiB || r.PeakCPUMillicores < r.IdleCPUMillicores:
		return errors.New("peak requirements can't be below idle")
	case r.PeakMemoryMiB == 0:
		return errors.New("peak memory isn't stated")
	case r.MeasuredOn == "" || r.Results == "":
		return errors.New("requirements must say where they were measured, and name the results")
	}
	return nil
}
