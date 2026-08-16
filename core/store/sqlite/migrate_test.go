package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func TestMigrate_AppliesSchemaAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	defer db.Close()

	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() first call = %v, want nil", err)
	}

	for _, table := range []string{"resources", "edges", "audit_log", "schema_migrations"} {
		var name string
		err := db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing after Migrate(): %v", table, err)
		}
	}

	// Re-running must be a no-op: no error, no duplicate-table failure.
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() second call = %v, want nil (idempotent)", err)
	}

	var migrationCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if migrationCount != 1 {
		t.Errorf("schema_migrations rows = %d, want 1 (re-run must not re-insert)", migrationCount)
	}
}

func TestOpen_WALModeAndBusyTimeoutSet(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	defer db.Close()

	var journalMode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}

	var busyTimeout int
	if err := db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busyTimeout)
	}
}
