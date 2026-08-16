package registry_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/registry"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() = %v", err)
	}
	return db
}

func newCapabilityRegistry(t *testing.T, db *sql.DB) *registry.CapabilityRegistry {
	t.Helper()
	s := sqlite.NewStore[resource.CapabilitySpec, resource.CapabilityStatus](db, resource.KindCapability)
	return registry.NewCapabilityRegistry(s)
}

func newProviderRegistry(t *testing.T, db *sql.DB) *registry.ProviderRegistry {
	t.Helper()
	s := sqlite.NewStore[resource.ProviderSpec, resource.ProviderStatus](db, resource.KindProvider)
	return registry.NewProviderRegistry(s)
}

// Acceptance criterion 3 (specs/001-resource-model/spec.md): "registering
// two providers for one capability and switching the default requires no
// changes outside registry data." This test is that criterion, literally:
// Register + SetDefault are the only calls made, no other code path.
func TestCapabilityRegistry_RegisterTwoProvidersAndSwitchDefault(t *testing.T) {
	ctx := context.Background()
	r := newCapabilityRegistry(t, newTestDB(t))

	if _, err := r.Register(ctx, "identity", true, []string{"provider-a", "provider-b"}, "provider-a"); err != nil {
		t.Fatalf("Register() = %v", err)
	}

	got, err := r.Default(ctx, "identity")
	if err != nil {
		t.Fatalf("Default() = %v", err)
	}
	if got != "provider-a" {
		t.Errorf("Default() = %q, want %q", got, "provider-a")
	}

	if err := r.SetDefault(ctx, "identity", "provider-b"); err != nil {
		t.Fatalf("SetDefault() = %v", err)
	}

	got, err = r.Default(ctx, "identity")
	if err != nil {
		t.Fatalf("Default() after SetDefault = %v", err)
	}
	if got != "provider-b" {
		t.Errorf("Default() after SetDefault = %q, want %q", got, "provider-b")
	}
}

func TestCapabilityRegistry_SetDefault_RejectsUnregisteredProvider(t *testing.T) {
	ctx := context.Background()
	r := newCapabilityRegistry(t, newTestDB(t))

	if _, err := r.Register(ctx, "identity", true, []string{"provider-a"}, "provider-a"); err != nil {
		t.Fatalf("Register() = %v", err)
	}

	if err := r.SetDefault(ctx, "identity", "provider-nonexistent"); err == nil {
		t.Fatal("SetDefault() with unregistered provider succeeded, want an error")
	}
	// The default must be unchanged after a rejected switch.
	got, err := r.Default(ctx, "identity")
	if err != nil {
		t.Fatalf("Default() = %v", err)
	}
	if got != "provider-a" {
		t.Errorf("Default() after rejected SetDefault = %q, want unchanged %q", got, "provider-a")
	}
}

func TestCapabilityRegistry_Default_UnknownCapability(t *testing.T) {
	ctx := context.Background()
	r := newCapabilityRegistry(t, newTestDB(t))

	if _, err := r.Default(ctx, "does-not-exist"); err == nil {
		t.Fatal("Default() for unregistered capability succeeded, want an error")
	}
}

func TestProviderRegistry_RegisterAndByName(t *testing.T) {
	ctx := context.Background()
	r := newProviderRegistry(t, newTestDB(t))

	_, err := r.Register(ctx, "provider-a", []string{"identity"},
		resource.ResourceRequirements{RSSBytes: 100 << 20, CPUMilliCores: 250},
		resource.HealthCheckContract{Kind: "http", Config: map[string]string{"path": "/health"}})
	if err != nil {
		t.Fatalf("Register() = %v", err)
	}

	got, err := r.ByName(ctx, "provider-a")
	if err != nil {
		t.Fatalf("ByName() = %v", err)
	}
	if len(got.Spec.Capabilities) != 1 || got.Spec.Capabilities[0] != "identity" {
		t.Errorf("Capabilities = %+v, want [identity]", got.Spec.Capabilities)
	}
	if got.Spec.HealthCheck.Kind != "http" {
		t.Errorf("HealthCheck.Kind = %q, want %q", got.Spec.HealthCheck.Kind, "http")
	}
}

func TestProviderRegistry_ByName_NotFound(t *testing.T) {
	ctx := context.Background()
	r := newProviderRegistry(t, newTestDB(t))

	if _, err := r.ByName(ctx, "does-not-exist"); err == nil {
		t.Fatal("ByName() for unregistered provider succeeded, want an error")
	}
}
