package openchoreo

import "context"

// fakeHelmRunner records every call's args and returns a scripted response
// per call index -- used by deploy_test.go to assert exact invocation
// order/flags without a real helm binary.
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
