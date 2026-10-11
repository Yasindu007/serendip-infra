//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func readProcess(pid int) (procSample, error) {
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return procSample{}, err
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return procSample{}, err
	}
	rss, err := parseProcStatus(string(status))
	if err != nil {
		return procSample{}, err
	}
	cpu, err := parseProcStat(string(stat))
	if err != nil {
		return procSample{}, err
	}
	return procSample{At: time.Now(), RSS: rss, CPUTime: cpu}, nil
}

func hostCPUModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "model name") {
			if _, v, ok := strings.Cut(line, ":"); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	return "unknown"
}
