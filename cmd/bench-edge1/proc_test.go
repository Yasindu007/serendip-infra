package main

import (
	"os"
	"testing"
	"time"
)

func TestParseProcStatus(t *testing.T) {
	status := "Name:\tserendipd\nVmPeak:\t  900000 kB\nVmRSS:\t   24576 kB\nThreads:\t12\n"
	rss, err := parseProcStatus(status)
	if err != nil || rss != 24576*1024 {
		t.Errorf("parseProcStatus = (%d, %v), want %d", rss, err, 24576*1024)
	}
	if _, err := parseProcStatus("Name:\tx\n"); err == nil {
		t.Error("missing VmRSS = nil error")
	}
}

func TestParseProcStat(t *testing.T) {
	// comm contains spaces and parens; utime=150, stime=50 ticks -> 2s.
	stat := "1234 (serendip (d) x) S 1 1234 1234 0 -1 4194560 100 0 0 0 150 50 0 0 20 0 12 0 5000 900000000 6000"
	got, err := parseProcStat(stat)
	if err != nil || got != 2*time.Second {
		t.Errorf("parseProcStat = (%v, %v), want 2s", got, err)
	}
	if _, err := parseProcStat("1234 S 1"); err == nil {
		t.Error("malformed stat = nil error")
	}
}

func TestParseWindowsProcess(t *testing.T) {
	rss, cpu, err := parseWindowsProcess("22020096 1500\r\n")
	if err != nil || rss != 22020096 || cpu != 1500*time.Millisecond {
		t.Errorf("= (%d, %v, %v)", rss, cpu, err)
	}
	if _, _, err := parseWindowsProcess("garbage"); err == nil {
		t.Error("garbage = nil error")
	}
}

func TestCPUMillicores(t *testing.T) {
	t0 := time.Now()
	a := procSample{At: t0, CPUTime: time.Second}
	b := procSample{At: t0.Add(10 * time.Second), CPUTime: 1500 * time.Millisecond}
	if got := cpuMillicores(a, b); !approxEqual(got, 50) {
		t.Errorf("cpuMillicores = %v, want 50 (0.5s CPU over 10s)", got)
	}
	if got := cpuMillicores(b, a); got != 0 && got > 0 {
		t.Errorf("reversed interval = %v, want 0", got)
	}
}

func TestReadProcess_Self(t *testing.T) {
	first, err := readProcess(os.Getpid())
	if err != nil {
		t.Fatalf("readProcess(self) = %v", err)
	}
	if first.RSS == 0 {
		t.Error("RSS = 0 for a running process")
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	x := 0
	for time.Now().Before(deadline) {
		x++
	}
	second, err := readProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if second.CPUTime < first.CPUTime {
		t.Errorf("CPU time went backwards: %v -> %v (x=%d)", first.CPUTime, second.CPUTime, x)
	}
}
