//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// readProcess queries the process through PowerShell, the same
// no-dependency approach cmd/bench-resource-model uses on Windows.
func readProcess(pid int) (procSample, error) {
	script := fmt.Sprintf(`$p = Get-Process -Id %d; "{0} {1}" -f $p.WorkingSet64, [math]::Round($p.TotalProcessorTime.TotalMilliseconds)`, pid)
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return procSample{}, fmt.Errorf("query process %d: %w", pid, err)
	}
	rss, cpu, err := parseWindowsProcess(string(out))
	if err != nil {
		return procSample{}, err
	}
	return procSample{At: time.Now(), RSS: rss, CPUTime: cpu}, nil
}

func hostCPUModel() string {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-CimInstance Win32_Processor | Select-Object -First 1).Name").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
