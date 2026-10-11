package helm

import (
	"context"
	"strings"
	"testing"
)

// TestRun_PropagatesExitError exercises Run's own error-wrapping format.
// Needs a real `helm` binary on PATH; skips cleanly if it's absent rather
// than silently passing on a different code path.
func TestRun_PropagatesExitError(t *testing.T) {
	if err := CheckAvailable(); err != nil {
		t.Skip("helm not available in this environment to exercise a real failure against")
	}

	_, err := Run(context.Background(), "--this-flag-does-not-exist-anywhere")
	if err == nil {
		t.Fatal("Run() = nil error, want helm's own unknown-flag failure to propagate")
	}
	if !strings.Contains(err.Error(), "helm") {
		t.Errorf("error = %q, want it to name the command", err.Error())
	}
	if !strings.Contains(err.Error(), "--this-flag-does-not-exist-anywhere") {
		t.Errorf("error = %q, want it to include the args that were passed", err.Error())
	}
}

func TestWithKubeconfig_PrependsFlag(t *testing.T) {
	var got []string
	base := func(ctx context.Context, args ...string) ([]byte, error) {
		got = args
		return nil, nil
	}
	if _, err := WithKubeconfig(base, "/etc/serendip/kubeconfig")(context.Background(), "upgrade", "--install", "x"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "--kubeconfig /etc/serendip/kubeconfig upgrade --install x" {
		t.Errorf("args = %v", got)
	}

	got = nil
	if _, err := WithKubeconfig(base, "")(context.Background(), "version"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "version" {
		t.Errorf("empty path args = %v, want unchanged", got)
	}
}

func TestCheckAvailable_MissingBinary(t *testing.T) {
	t.Setenv("PATH", "")
	if err := CheckAvailable(); err == nil {
		t.Fatal("CheckAvailable() = nil, want an error when PATH is empty")
	} else if !strings.Contains(err.Error(), "helm not found on PATH") {
		t.Errorf("CheckAvailable() error = %q, want it to name the missing binary clearly", err.Error())
	}
}

func TestTruncateOutput_ShortPassesThrough(t *testing.T) {
	if got := truncateOutput([]byte("short output")); got != "short output" {
		t.Errorf("truncateOutput(short) = %q, want unchanged", got)
	}
}

func TestTruncateOutput_LongIsTruncated(t *testing.T) {
	long := strings.Repeat("x", maxOutputInError+100)
	got := truncateOutput([]byte(long))
	if len(got) >= len(long) {
		t.Errorf("truncateOutput(long) length = %d, want shorter than input %d", len(got), len(long))
	}
	if !strings.HasSuffix(got, "... (truncated)") {
		t.Errorf("truncateOutput(long) = %q, want it to end with a truncation marker", got)
	}
}
