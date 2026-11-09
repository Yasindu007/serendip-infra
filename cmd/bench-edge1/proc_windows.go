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
// Launching PowerShell occasionally fails on its own (0xc0000142, DLL
// init failed, seen under memory pressure on a 50-minute run), so a few
// attempts are made before one bad launch ends the whole run.
func readProcess(pid int) (procSample, error) {
	script := fmt.Sprintf(`$p = Get-Process -Id %d; "{0} {1}" -f $p.WorkingSet64, [math]::Round($p.TotalProcessorTime.TotalMilliseconds)`, pid)
	var out []byte
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Second)
		}
		out, err = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
		if err == nil {
			break
		}
	}
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
