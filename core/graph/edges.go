package graph

import (
	"context"
	"fmt"
	"time"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

const timeLayout = time.RFC3339Nano

// SyncEdges replaces the edges table's record of ref's relationships with
// exactly what ownerRefs/relationships now declare: an "owns" edge from
// each owner to ref, and one edge per relationship from ref to its
// target. Called by core/store/sqlite inside the same transaction as a
// Create/Update (specs/001-resource-model/plan.md), so q is typically a
// *sql.Tx.
//
// This clears and reinserts rather than computing a delta -- simpler,
// same end state, and cheap at spec 001's scale. If edge-table writes
// become a hot path at larger scale, a real diff would reduce write
// volume, but that's not this spec's problem.
func SyncEdges(ctx context.Context, q Queryer, ref ResourceRef, ownerRefs []resource.OwnerRef, relationships []resource.Relationship) error {
	if _, err := q.ExecContext(ctx,
		`DELETE FROM edges WHERE edge_type = ? AND to_kind = ? AND to_id = ?`,
		string(resource.EdgeOwns), string(ref.Kind), ref.ID,
	); err != nil {
		return fmt.Errorf("graph: clear owns edges for %s %s: %w", ref.Kind, ref.ID, err)
	}
	if _, err := q.ExecContext(ctx,
		`DELETE FROM edges WHERE edge_type != ? AND from_kind = ? AND from_id = ?`,
		string(resource.EdgeOwns), string(ref.Kind), ref.ID,
	); err != nil {
		return fmt.Errorf("graph: clear relationship edges for %s %s: %w", ref.Kind, ref.ID, err)
	}

	now := time.Now().UTC().Format(timeLayout)
	for _, owner := range ownerRefs {
		if err := insertEdge(ctx, q, ResourceRef{Kind: owner.Kind, ID: owner.ID}, ref, resource.EdgeOwns, now); err != nil {
			return err
		}
	}
	for _, rel := range relationships {
		target := ResourceRef{Kind: rel.TargetKind, ID: rel.TargetID}
		if err := insertEdge(ctx, q, ref, target, rel.Type, now); err != nil {
			return err
		}
	}
	return nil
}

func insertEdge(ctx context.Context, q Queryer, from, to ResourceRef, edgeType resource.EdgeType, createdAt string) error {
	_, err := q.ExecContext(ctx, `
		INSERT OR IGNORE INTO edges (from_kind, from_id, to_kind, to_id, edge_type, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		string(from.Kind), from.ID, string(to.Kind), to.ID, string(edgeType), createdAt)
	if err != nil {
		return fmt.Errorf("graph: insert edge %s:%s -%s-> %s:%s: %w", from.Kind, from.ID, edgeType, to.Kind, to.ID, err)
	}
	return nil
}

// DeleteEdgesFor removes every edge touching ref, in either direction --
// called when ref itself is deleted, so nothing dangling is left behind.
func DeleteEdgesFor(ctx context.Context, q Queryer, ref ResourceRef) error {
	_, err := q.ExecContext(ctx,
		`DELETE FROM edges WHERE (from_kind = ? AND from_id = ?) OR (to_kind = ? AND to_id = ?)`,
		string(ref.Kind), ref.ID, string(ref.Kind), ref.ID)
	if err != nil {
		return fmt.Errorf("graph: delete edges for %s %s: %w", ref.Kind, ref.ID, err)
	}
	return nil
}

// Children returns resources ref owns (an "owns" edge from ref to them).
func Children(ctx context.Context, q Queryer, ref ResourceRef) ([]ResourceRef, error) {
	return queryEdgeTargets(ctx, q, `
		SELECT to_kind, to_id FROM edges WHERE edge_type = ? AND from_kind = ? AND from_id = ?`,
		string(resource.EdgeOwns), string(ref.Kind), ref.ID)
}

// Dependents returns resources that declare a "depends-on" relationship
// targeting ref -- what would need to know if ref changed or went away.
func Dependents(ctx context.Context, q Queryer, ref ResourceRef) ([]ResourceRef, error) {
	return queryEdgeTargets(ctx, q, `
		SELECT from_kind, from_id FROM edges WHERE edge_type = ? AND to_kind = ? AND to_id = ?`,
		string(resource.EdgeDependsOn), string(ref.Kind), ref.ID)
}

// BlastRadius does a reverse-edge breadth-first traversal from ref across
// every edge type: at each step, find everything pointing at the current
// frontier (any edge where to = frontier node) and add its "from" side,
// until no new nodes are found. This is what makes "what breaks if this
// resource dies" queries possible (docs/architecture/operations.md,
// ai-operations.md) -- nothing calls it yet in spec 001.
func BlastRadius(ctx context.Context, q Queryer, ref ResourceRef) ([]ResourceRef, error) {
	visited := map[ResourceRef]bool{ref: true}
	frontier := []ResourceRef{ref}
	var result []ResourceRef

	for len(frontier) > 0 {
		var next []ResourceRef
		for _, node := range frontier {
			reachedBy, err := queryEdgeTargets(ctx, q, `
				SELECT from_kind, from_id FROM edges WHERE to_kind = ? AND to_id = ?`,
				string(node.Kind), node.ID)
			if err != nil {
				return nil, err
			}
			for _, r := range reachedBy {
				if visited[r] {
					continue
				}
				visited[r] = true
				result = append(result, r)
				next = append(next, r)
			}
		}
		frontier = next
	}
	return result, nil
}

func queryEdgeTargets(ctx context.Context, q Queryer, query string, args ...any) ([]ResourceRef, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("graph: query edges: %w", err)
	}
	defer rows.Close()

	var out []ResourceRef
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			return nil, fmt.Errorf("graph: scan edge: %w", err)
		}
		out = append(out, ResourceRef{Kind: resource.Kind(kind), ID: id})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: query edges: %w", err)
	}
	return out, nil
}
