package graph

import (
	"context"
	"database/sql"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// ResourceRef identifies a resource kind-agnostically -- a (Kind, ID)
// pair. The resource graph is inherently cross-kind (an Organization owns
// Projects owns Applications owns Components, docs/architecture/resource-model.md),
// so graph operations can't be pinned to one Go Spec/Status type the way
// core/store's generic Store[S, St] is.
type ResourceRef struct {
	Kind resource.Kind
	ID   string
}

// Queryer is satisfied by both *sql.DB and *sql.Tx, so graph operations
// can run standalone (Graph, for read-only external use) or as part of a
// caller's transaction (the package-level functions below, used by
// core/store/sqlite to sync edges and check for children inside the same
// transaction as a mutation -- specs/001-resource-model/plan.md).
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Graph answers relationship queries over the edges every resource
// mutation maintains from its Metadata.OwnerRefs/.Relationships.
type Graph interface {
	// Children returns resources with an "owns" edge from ref (ref is
	// their owner).
	Children(ctx context.Context, ref ResourceRef) ([]ResourceRef, error)
	// Dependents returns resources with a "depends-on" edge pointing at
	// ref -- what declares a dependency on ref.
	Dependents(ctx context.Context, ref ResourceRef) ([]ResourceRef, error)
	// BlastRadius returns every resource reachable by following edges
	// backward from ref across all edge types (owns, runs-on, depends-on,
	// provided-by) -- what would be affected if ref became unavailable.
	BlastRadius(ctx context.Context, ref ResourceRef) ([]ResourceRef, error)
}

// SQLiteGraph is the default Graph implementation, backed by the edges
// table (core/store/sqlite/migrations/0001_init.sql).
type SQLiteGraph struct {
	db *sql.DB
}

func NewSQLiteGraph(db *sql.DB) *SQLiteGraph {
	return &SQLiteGraph{db: db}
}

var _ Graph = (*SQLiteGraph)(nil)

func (g *SQLiteGraph) Children(ctx context.Context, ref ResourceRef) ([]ResourceRef, error) {
	return Children(ctx, g.db, ref)
}

func (g *SQLiteGraph) Dependents(ctx context.Context, ref ResourceRef) ([]ResourceRef, error) {
	return Dependents(ctx, g.db, ref)
}

func (g *SQLiteGraph) BlastRadius(ctx context.Context, ref ResourceRef) ([]ResourceRef, error) {
	return BlastRadius(ctx, g.db, ref)
}
