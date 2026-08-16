package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies every embedded migration not yet recorded in
// schema_migrations, in filename order (0001_init.sql, 0002_..., ...).
// Safe to call repeatedly: already-applied migrations are skipped, so a
// second call against an already-migrated database is a no-op.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("sqlite: create schema_migrations: %w", err)
	}

	names, err := migrationFilenames()
	if err != nil {
		return err
	}

	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return fmt.Errorf("sqlite: migration filename %q: %w", name, err)
		}

		var applied int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("sqlite: check migration %d applied: %w", version, err)
		}
		if applied > 0 {
			continue
		}

		if err := applyMigration(ctx, db, version, name); err != nil {
			return err
		}
	}
	return nil
}

func migrationFilenames() ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("sqlite: read embedded migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func applyMigration(ctx context.Context, db *sql.DB, version int, name string) error {
	sqlBytes, err := migrationsFS.ReadFile(path.Join("migrations", name))
	if err != nil {
		return fmt.Errorf("sqlite: read migration %s: %w", name, err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin migration %d: %w", version, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
		return fmt.Errorf("sqlite: apply migration %d (%s): %w", version, name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		version, time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("sqlite: record migration %d: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit migration %d: %w", version, err)
	}
	return nil
}

// migrationVersion extracts the leading integer from a migration filename
// like "0001_init.sql" -> 1.
func migrationVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("expected NNNN_description.sql, got %q", name)
	}
	return strconv.Atoi(prefix)
}
