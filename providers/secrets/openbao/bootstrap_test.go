package openbao

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeBootstrapClient is an in-process bootstrapClient fake --
// Bootstrap's orchestration logic (which call happens when) is what
// these tests cover, not wire-level HTTP behavior (already covered by
// client_test.go against a real httptest.Server).
type fakeBootstrapClient struct {
	health HealthStatus

	initCalled   bool
	initKeys     []string
	initToken    string
	initErr      error
	unsealCalled bool
	unsealKey    string
	unsealErr    error
	setToken     string

	ensureKVv2MountCalled bool
	ensureKVv2MountMount  string
	ensureKVv2MountErr    error
}

func (f *fakeBootstrapClient) Health(ctx context.Context) (HealthStatus, error) {
	return f.health, nil
}

func (f *fakeBootstrapClient) Init(ctx context.Context, shares, threshold int) ([]string, string, error) {
	f.initCalled = true
	if f.initErr != nil {
		return nil, "", f.initErr
	}
	return f.initKeys, f.initToken, nil
}

func (f *fakeBootstrapClient) Unseal(ctx context.Context, key string) (bool, error) {
	f.unsealCalled = true
	f.unsealKey = key
	if f.unsealErr != nil {
		return false, f.unsealErr
	}
	return false, nil
}

func (f *fakeBootstrapClient) SetToken(token string) {
	f.setToken = token
}

func (f *fakeBootstrapClient) EnsureKVv2Mount(ctx context.Context, mount string) error {
	f.ensureKVv2MountCalled = true
	f.ensureKVv2MountMount = mount
	return f.ensureKVv2MountErr
}

func TestBootstrap_FreshInstance_InitsPersistsAndUnseals(t *testing.T) {
	client := &fakeBootstrapClient{
		health:    HealthStatus{Initialized: false},
		initKeys:  []string{"unseal-key-1"},
		initToken: "root-token-1",
	}
	credPath := filepath.Join(t.TempDir(), "secrets", "openbao-init.json")

	if err := Bootstrap(context.Background(), client, credPath); err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}
	if !client.initCalled {
		t.Error("Init was not called for a fresh (uninitialized) instance")
	}
	if !client.unsealCalled || client.unsealKey != "unseal-key-1" {
		t.Errorf("Unseal called=%v key=%q, want called with unseal-key-1", client.unsealCalled, client.unsealKey)
	}
	if client.setToken != "root-token-1" {
		t.Errorf("SetToken = %q, want %q", client.setToken, "root-token-1")
	}
	if !client.ensureKVv2MountCalled || client.ensureKVv2MountMount != DefaultKVMount {
		t.Errorf("EnsureKVv2Mount called=%v mount=%q, want called with %q", client.ensureKVv2MountCalled, client.ensureKVv2MountMount, DefaultKVMount)
	}

	info, err := os.Stat(credPath)
	if err != nil {
		t.Fatalf("credentials file not written: %v", err)
	}
	if runtime.GOOS == "linux" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("credentials file mode = %o, want 0600", perm)
		}
		dirInfo, err := os.Stat(filepath.Dir(credPath))
		if err != nil {
			t.Fatalf("credentials directory not created: %v", err)
		}
		if perm := dirInfo.Mode().Perm(); perm != 0o700 {
			t.Errorf("credentials directory mode = %o, want 0700", perm)
		}
	}

	buf, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("read credentials file: %v", err)
	}
	var creds credentials
	if err := json.Unmarshal(buf, &creds); err != nil {
		t.Fatalf("unmarshal credentials file: %v", err)
	}
	if creds.RootToken != "root-token-1" || creds.UnsealKey != "unseal-key-1" {
		t.Errorf("persisted credentials = %+v, want RootToken=root-token-1 UnsealKey=unseal-key-1", creds)
	}
}

func TestBootstrap_AlreadyInitializedAndSealed_ReadsExistingFileDoesNotReinit(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "openbao-init.json")
	existing := credentials{RootToken: "existing-root-token", UnsealKey: "existing-unseal-key"}
	buf, _ := json.Marshal(existing)
	if err := os.WriteFile(credPath, buf, 0o600); err != nil {
		t.Fatalf("seed credentials file: %v", err)
	}

	client := &fakeBootstrapClient{health: HealthStatus{Initialized: true, Sealed: true}}

	if err := Bootstrap(context.Background(), client, credPath); err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}
	if client.initCalled {
		t.Error("Init was called for an already-initialized instance")
	}
	if !client.unsealCalled || client.unsealKey != "existing-unseal-key" {
		t.Errorf("Unseal called=%v key=%q, want called with existing-unseal-key", client.unsealCalled, client.unsealKey)
	}
	if client.setToken != "existing-root-token" {
		t.Errorf("SetToken = %q, want %q", client.setToken, "existing-root-token")
	}
	if !client.ensureKVv2MountCalled {
		t.Error("EnsureKVv2Mount was not called on the already-initialized-and-sealed path")
	}
}

func TestBootstrap_AlreadyInitializedAndUnsealed_CallsNeitherInitNorUnseal(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "openbao-init.json")
	existing := credentials{RootToken: "existing-root-token", UnsealKey: "existing-unseal-key"}
	buf, _ := json.Marshal(existing)
	if err := os.WriteFile(credPath, buf, 0o600); err != nil {
		t.Fatalf("seed credentials file: %v", err)
	}

	client := &fakeBootstrapClient{health: HealthStatus{Initialized: true, Sealed: false}}

	if err := Bootstrap(context.Background(), client, credPath); err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}
	if client.initCalled {
		t.Error("Init was called for an already-unsealed instance")
	}
	if client.unsealCalled {
		t.Error("Unseal was called for an already-unsealed instance")
	}
	if client.setToken != "existing-root-token" {
		t.Errorf("SetToken = %q, want %q", client.setToken, "existing-root-token")
	}
	if !client.ensureKVv2MountCalled {
		t.Error("EnsureKVv2Mount was not called on the already-initialized-and-unsealed path -- it must run unconditionally so a prior partial failure can self-heal")
	}
}

func TestBootstrap_EnsureKVv2MountFailure_Propagates(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "openbao-init.json")
	existing := credentials{RootToken: "existing-root-token", UnsealKey: "existing-unseal-key"}
	buf, _ := json.Marshal(existing)
	if err := os.WriteFile(credPath, buf, 0o600); err != nil {
		t.Fatalf("seed credentials file: %v", err)
	}

	client := &fakeBootstrapClient{
		health:             HealthStatus{Initialized: true, Sealed: false},
		ensureKVv2MountErr: errors.New("simulated mount failure"),
	}

	err := Bootstrap(context.Background(), client, credPath)
	if err == nil {
		t.Fatal("Bootstrap() = nil error, want the simulated EnsureKVv2Mount failure to propagate")
	}
}

func TestBootstrap_AlreadyInitializedCredentialsFileMissing_ReturnsErrorCallsNothingDestructive(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "does-not-exist", "openbao-init.json")
	client := &fakeBootstrapClient{health: HealthStatus{Initialized: true, Sealed: true}}

	err := Bootstrap(context.Background(), client, credPath)
	if err == nil {
		t.Fatal("Bootstrap() = nil error, want an error when credentials file is missing for an already-initialized instance")
	}
	if client.initCalled {
		t.Error("Init was called despite the instance already being initialized -- would conflict with existing state")
	}
	if client.unsealCalled {
		t.Error("Unseal was called despite having no credentials to unseal with")
	}
	if client.ensureKVv2MountCalled {
		t.Error("EnsureKVv2Mount was called despite Bootstrap having already failed reading credentials")
	}
}

func TestBootstrap_InitFailure_PropagatesErrorWritesNoFile(t *testing.T) {
	client := &fakeBootstrapClient{
		health:  HealthStatus{Initialized: false},
		initErr: errors.New("simulated init failure"),
	}
	credPath := filepath.Join(t.TempDir(), "openbao-init.json")

	if err := Bootstrap(context.Background(), client, credPath); err == nil {
		t.Fatal("Bootstrap() = nil error, want the simulated init failure to propagate")
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Error("credentials file was written despite Init failing")
	}
}
