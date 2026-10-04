package provider_test

import (
	"sync"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/provider"
)

type fakeStorageProvider struct{ name string }

func TestRegistry_RegisterThenLookup_ReturnsSameValue(t *testing.T) {
	r := provider.NewRegistry()
	impl := &fakeStorageProvider{name: "local-path"}

	r.Register("storage", impl)

	got, ok := r.Lookup("storage")
	if !ok {
		t.Fatal("Lookup(storage) ok = false, want true")
	}
	gotImpl, ok := got.(*fakeStorageProvider)
	if !ok {
		t.Fatalf("Lookup(storage) = %T, want *fakeStorageProvider", got)
	}
	if gotImpl != impl {
		t.Error("Lookup(storage) returned a different value than what was registered")
	}
}

func TestRegistry_Lookup_UnregisteredCapability_ReturnsFalseNotPanic(t *testing.T) {
	r := provider.NewRegistry()
	got, ok := r.Lookup("does-not-exist")
	if ok {
		t.Error("Lookup(unregistered) ok = true, want false")
	}
	if got != nil {
		t.Errorf("Lookup(unregistered) value = %v, want nil", got)
	}
}

func TestRegistry_Register_ReplacesExisting(t *testing.T) {
	r := provider.NewRegistry()
	r.Register("storage", &fakeStorageProvider{name: "first"})
	r.Register("storage", &fakeStorageProvider{name: "second"})

	got, _ := r.Lookup("storage")
	if got.(*fakeStorageProvider).name != "second" {
		t.Errorf("Lookup(storage).name = %q, want %q", got.(*fakeStorageProvider).name, "second")
	}
}

// TestRegistry_ConcurrentAccess proves Register/Lookup are safe to call
// concurrently -- go test -race is what actually validates this, this
// test just creates the contention for the race detector to observe.
func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := provider.NewRegistry()
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			r.Register("storage", &fakeStorageProvider{name: "concurrent"})
		}()
		go func() {
			defer wg.Done()
			r.Lookup("storage")
		}()
	}
	wg.Wait()
}
