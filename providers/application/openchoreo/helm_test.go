package openchoreo

import (
	"context"
	"strings"
	"testing"
)

// TestDefaultHelmRunner_PropagatesExitError actually exercises
// defaultHelmRunner's own error-wrapping format ("helm %v: %w: %s",
// helm.go) -- go-reviewer's T007 finding on the original version of
// this test: it only asserted err != nil without checking the
// wrapping ever did anything. Needs a real `helm` binary on PATH
// (this is the one test in the package that does); skips cleanly if
// it's genuinely absent rather than silently passing on a different
// code path.
func TestDefaultHelmRunner_PropagatesExitError(t *testing.T) {
	if err := checkHelmAvailable(); err != nil {
		t.Skip("helm not available in this environment to exercise a real failure against -- covered by fake-runner tests elsewhere")
	}

	_, err := defaultHelmRunner(context.Background(), "--this-flag-does-not-exist-anywhere")
	if err == nil {
		t.Fatal("defaultHelmRunner() = nil error, want helm's own unknown-flag failure to propagate")
	}
	if !strings.Contains(err.Error(), "helm") {
		t.Errorf("error = %q, want it to name the command (helm.go's \"helm %%v: %%w: %%s\" format)", err.Error())
	}
	if !strings.Contains(err.Error(), "--this-flag-does-not-exist-anywhere") {
		t.Errorf("error = %q, want it to include the args that were passed", err.Error())
	}
}

func TestCheckHelmAvailable_MissingBinary(t *testing.T) {
	t.Setenv("PATH", "")
	if err := checkHelmAvailable(); err == nil {
		t.Fatal("checkHelmAvailable() = nil, want an error when PATH is empty")
	} else if !strings.Contains(err.Error(), "helm not found on PATH") {
		t.Errorf("checkHelmAvailable() error = %q, want it to name the missing binary clearly", err.Error())
	}
}

func TestTruncateOutput_ShortPassesThrough(t *testing.T) {
	got := truncateOutput([]byte("short output"))
	if got != "short output" {
		t.Errorf("truncateOutput(short) = %q, want unchanged", got)
	}
}

func TestTruncateOutput_LongIsTruncated(t *testing.T) {
	long := strings.Repeat("x", maxHelmOutputInError+100)
	got := truncateOutput([]byte(long))
	if len(got) >= len(long) {
		t.Errorf("truncateOutput(long) length = %d, want shorter than input %d", len(got), len(long))
	}
	if !strings.HasSuffix(got, "... (truncated)") {
		t.Errorf("truncateOutput(long) = %q, want it to end with a truncation marker", got)
	}
}

// fakeHelmRunner records every call's args and returns a scripted
// response per call index -- used by deploy_test.go to assert exact
// invocation order/flags without a real helm binary.
type fakeHelmRunner struct {
	calls   [][]string
	outputs [][]byte
	errs    []error
}

func (f *fakeHelmRunner) run(ctx context.Context, args ...string) ([]byte, error) {
	i := len(f.calls)
	f.calls = append(f.calls, args)
	var out []byte
	if i < len(f.outputs) {
		out = f.outputs[i]
	}
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return out, err
}
