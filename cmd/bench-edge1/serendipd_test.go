package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWaitForLogLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	os.WriteFile(path, []byte("starting\n"), 0o644)
	go func() {
		time.Sleep(300 * time.Millisecond)
		os.WriteFile(path, []byte("starting\nserendipd: listening on 127.0.0.1:1\nserendipd: all capabilities ready\n"), 0o644)
	}()
	if err := waitForLogLine(context.Background(), path, readyLine, 5*time.Second, make(chan struct{}), func() error { return nil }); err != nil {
		t.Fatalf("waitForLogLine = %v", err)
	}
}

func TestWaitForLogLine_TimeoutAndEarlyExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	os.WriteFile(path, []byte("serendipd: kubernetes: boom\n"), 0o644)

	if err := waitForLogLine(context.Background(), path, "listening on", 600*time.Millisecond, make(chan struct{}), func() error { return nil }); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("timeout error = %v, want it to carry the log tail", err)
	}

	done := make(chan struct{})
	close(done)
	if err := waitForLogLine(context.Background(), path, "listening on", time.Minute, done, func() error { return errors.New("exit status 1") }); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Errorf("early exit error = %v, want process-exited error", err)
	}
}

// A serendipd that exits during start-up must not hang the later stop()
// (the harness defers it): the exit signal has to stay observable after
// the start-up wait consumed it.
func TestDaemon_StopAfterEarlyExitDoesNotHang(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d := &daemon{
		bin: exe,
		env: []string{"BENCH_EDGE1_FAKE_EXIT=1"},
		log: filepath.Join(t.TempDir(), "serendipd.log"),
	}
	if _, err := d.start(context.Background(), 30*time.Second); err == nil {
		t.Fatal("start() = nil, want the early-exit error")
	}
	stopped := make(chan struct{})
	go func() { d.stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop() hung after the process had already exited")
	}
}

// TestMain lets the test binary double as a fake serendipd that fails at
// start-up, for TestDaemon_StopAfterEarlyExitDoesNotHang.
func TestMain(m *testing.M) {
	if os.Getenv("BENCH_EDGE1_FAKE_EXIT") == "1" {
		os.Stdout.WriteString("serendipd: kubernetes: simulated startup failure\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestParseFlags(t *testing.T) {
	if _, err := parseFlags(nil); err == nil {
		t.Error("missing required flags = nil error")
	}
	o, err := parseFlags([]string{"--serendipd", "s", "--kubeconfig", "k", "--sim-cpus", "2", "--sim-cpuset", "0-3", "--not-simulated", "memory limit; disk speed"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.sim.Active() || len(o.sim.NotSimulated) != 2 || o.sim.NotSimulated[1] != "disk speed" {
		t.Errorf("sim = %+v", o.sim)
	}
}

func TestParseCPUQuantity(t *testing.T) {
	for q, want := range map[string]float64{"4": 4, "3500m": 3.5, "": 0} {
		if got := parseCPUQuantity(q); got != want {
			t.Errorf("parseCPUQuantity(%q) = %v, want %v", q, got, want)
		}
	}
}

func TestConditionReasonAndID(t *testing.T) {
	obj := map[string]any{
		"metadata": map[string]any{"id": "abc"},
		"status":   map[string]any{"conditions": []any{map[string]any{"type": "Reachable", "reason": "Serving"}}},
	}
	if idOf(obj) != "abc" || conditionReason(obj, "Reachable") != "Serving" || conditionReason(obj, "Ready") != "" {
		t.Errorf("idOf/conditionReason wrong for %v", obj)
	}
}
