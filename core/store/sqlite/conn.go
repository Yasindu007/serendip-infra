// Package sqlite is the default core/store implementation (ADR 0003):
// modernc.org/sqlite, a pure-Go, CGO-free driver.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens (creating if necessary) a SQLite database at path and
// applies the pragmas this package depends on: WAL mode, and a busy
// timeout so brief lock contention blocks instead of failing immediately.
// All access is serialized through a single connection
// (SetMaxOpenConns(1)) -- a deliberate simplicity-over-throughput choice
// appropriate for a single-process control plane at this scale
// (specs/001-resource-model/plan.md Risks).
//
// Open does not run migrations -- call Migrate separately.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("sqlite: %s: %w", pragma, err)
		}
	}
	return db, nil
}
