package identity_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/serendip-infra/serendip-infra/agent/identity"
)

func TestLoad_GeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	id1, err := identity.Load(dir)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if id1 == "" {
		t.Fatal("Load() returned an empty id")
	}

	id2, err := identity.Load(dir)
	if err != nil {
		t.Fatalf("Load() (second call) = %v", err)
	}
	if id2 != id1 {
		t.Errorf("Load() second call = %q, want %q (same id reused, not regenerated)", id2, id1)
	}
}

func TestLoad_CreatesStateDirIfMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "state", "dir")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("test setup: %s should not exist yet", dir)
	}
	if _, err := identity.Load(dir); err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("state dir %s was not created: %v", dir, err)
	}
}

func TestLoad_EmptyExistingFile_ReturnsClearError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node-id"), []byte(""), 0o600); err != nil {
		t.Fatalf("write empty node-id file: %v", err)
	}
	if _, err := identity.Load(dir); err == nil {
		t.Fatal("Load() with empty existing file = nil error, want an error")
	}
}

func TestLoad_WhitespaceOnlyExistingFile_ReturnsClearError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node-id"), []byte("   \n"), 0o600); err != nil {
		t.Fatalf("write whitespace-only node-id file: %v", err)
	}
	if _, err := identity.Load(dir); err == nil {
		t.Fatal("Load() with whitespace-only existing file = nil error, want an error")
	}
}
