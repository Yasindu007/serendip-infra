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

	// Count after the first Migrate() -- deliberately not a hardcoded
	// number, since the number of embedded migration files grows over
	// time (e.g. T003 in spec 002 added a second one). What idempotency
	// actually means is "re-running doesn't change this count."
	var firstCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&firstCount); err != nil {
		t.Fatalf("count schema_migrations after first Migrate(): %v", err)
	}
	if firstCount == 0 {
		t.Fatal("schema_migrations is empty after Migrate(), want at least one row")
	}

	// Re-running must be a no-op: no error, no duplicate-table failure,
	// no additional rows.
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() second call = %v, want nil (idempotent)", err)
	}

	var secondCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&secondCount); err != nil {
		t.Fatalf("count schema_migrations after second Migrate(): %v", err)
	}
	if secondCount != firstCount {
		t.Errorf("schema_migrations rows after re-run = %d, want unchanged from %d", secondCount, firstCount)
	}
}

func TestMigrate_RequestIDColumnExists(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	defer db.Close()
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() = %v", err)
	}

	// Inserting a row referencing request_id proves the column exists
	// with the expected (nullable TEXT) type -- migration 0002.
	_, err = db.ExecContext(ctx, `
		INSERT INTO audit_log (id, actor, interface, kind, resource_id, at, request_id)
		VALUES ('a', 'alice', 'http', 'Organization', 'org-1', '2026-08-16T00:00:00Z', 'req-1')`)
	if err != nil {
		t.Fatalf("insert with request_id: %v", err)
	}

	var requestID string
	if err := db.QueryRowContext(ctx, `SELECT request_id FROM audit_log WHERE id = 'a'`).Scan(&requestID); err != nil {
		t.Fatalf("select request_id: %v", err)
	}
	if requestID != "req-1" {
		t.Errorf("request_id = %q, want %q", requestID, "req-1")
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
