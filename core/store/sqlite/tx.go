package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// DB opens transactions that span resource kinds (specs/027 plan
// decision 8): a template's Application, Components and Deployments are
// written all or nothing.
type DB struct {
	db *sql.DB
}

// NewDB wraps db, the same handle the kinds' Stores were built on.
func NewDB(db *sql.DB) *DB { return &DB{db: db} }

var _ store.Atomic = (*DB)(nil)

// Tx is a transaction Atomically opened. It collects the events of what
// was created through it, published only after commit.
type Tx struct {
	tx      *sql.Tx
	db      *sql.DB
	pending []pendingEvent
	done    bool
}

// StoreTx marks Tx as a store.Tx.
func (*Tx) StoreTx() {}

type pendingEvent struct {
	bus event.Bus
	e   event.Event
}

// ErrForeignTx is returned by CreateIn when the transaction isn't one
// this package opened on the Store's database, or has already finished.
var ErrForeignTx = errors.New("sqlite: transaction is not open on this store's database")

// Atomically runs fn in one transaction. See store.Atomic.
func (d *DB) Atomically(ctx context.Context, fn func(tx store.Tx) error) error {
	sqlTx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: atomically: begin: %w", err)
	}
	tx := &Tx{tx: sqlTx, db: d.db}
	defer func() {
		tx.done = true
		sqlTx.Rollback() //nolint:errcheck // no-op after a successful Commit
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("sqlite: atomically: commit: %w", err)
	}
	tx.done = true
	for _, p := range tx.pending {
		if p.bus != nil {
			p.bus.Publish(p.e)
		}
	}
	return nil
}

// sqliteTx unwraps t, refusing a Tx from elsewhere, one on another
// database, or one that has finished (fail closed).
func sqliteTx(t store.Tx, db *sql.DB) (*Tx, error) {
	tx, ok := t.(*Tx)
	if !ok || tx == nil || tx.db != db || tx.done {
		return nil, ErrForeignTx
	}
	return tx, nil
}
