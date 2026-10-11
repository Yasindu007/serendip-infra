package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// procSample is one reading of a host process: resident memory and the
// total CPU time it has consumed so far. CPU% comes from the delta between
// two readings.
type procSample struct {
	At      time.Time
	RSS     uint64
	CPUTime time.Duration
}

// cpuMillicores converts two readings into average millicores over the
// interval between them (1000 = one full CPU thread busy).
func cpuMillicores(a, b procSample) float64 {
	wall := b.At.Sub(a.At)
	if wall <= 0 {
		return 0
	}
	return float64(b.CPUTime-a.CPUTime) / float64(wall) * 1000
}

// userHZ is the unit of /proc/<pid>/stat CPU times. Linux fixes it at 100
// for the userspace interface regardless of the kernel's internal HZ.
const userHZ = 100

// parseProcStatus extracts VmRSS (bytes) from /proc/<pid>/status.
func parseProcStatus(status string) (uint64, error) {
	for _, line := range strings.Split(status, "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse VmRSS: %w", err)
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("VmRSS not found")
}

// parseProcStat extracts utime+stime from /proc/<pid>/stat. The comm field
// (2nd) is parenthesised and may contain spaces, so fields are counted
// from after its closing parenthesis: utime and stime are fields 14 and 15.
func parseProcStat(stat string) (time.Duration, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, fmt.Errorf("malformed stat: no comm field")
	}
	fields := strings.Fields(stat[end+1:])
	// fields[0] is field 3 (state), so utime (14) is fields[11], stime (15) fields[12].
	if len(fields) < 13 {
		return 0, fmt.Errorf("malformed stat: %d fields after comm", len(fields))
	}
	utime, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse utime: %w", err)
	}
	stime, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse stime: %w", err)
	}
	return time.Duration(utime+stime) * time.Second / userHZ, nil
}

// parseWindowsProcess parses "<WorkingSet64> <TotalProcessorTime ms>" as
// printed by the PowerShell query in proc_windows.go.
func parseWindowsProcess(out string) (uint64, time.Duration, error) {
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected Get-Process output %q", out)
	}
	rss, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse WorkingSet64: %w", err)
	}
	ms, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse TotalProcessorTime: %w", err)
	}
	return rss, time.Duration(ms * float64(time.Millisecond)), nil
}
