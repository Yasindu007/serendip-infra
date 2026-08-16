package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/serendip-infra/serendip-infra/core/id"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

const timeLayout = time.RFC3339Nano

// Store is the SQLite implementation of store.Store[S, St] for one
// resource kind, fixed at construction (kind is a struct field, not a
// per-call parameter, because Get/Update/Delete in the store.Store
// interface don't take a kind argument). List/audit/graph-edge/event
// integration are added on top of this in T012-T014 -- this file is
// deliberately just resources-table CRUD with optimistic concurrency.
type Store[S, St any] struct {
	db   *sql.DB
	kind resource.Kind
}

// NewStore returns a Store scoped to one resource kind, e.g.
// NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization).
func NewStore[S, St any](db *sql.DB, kind resource.Kind) *Store[S, St] {
	return &Store[S, St]{db: db, kind: kind}
}

var _ store.Store[resource.OrganizationSpec, resource.OrganizationStatus] = (*Store[resource.OrganizationSpec, resource.OrganizationStatus])(nil)

func (s *Store[S, St]) Get(ctx context.Context, resourceID string) (resource.Resource[S, St], error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, labels, owner_refs, relationships, spec, status,
		       resource_version, created_at, updated_at
		FROM resources WHERE id = ? AND kind = ?`, resourceID, string(s.kind))
	return scanResource[S, St](row, s.kind)
}

// List always scopes to this Store's own constructor-bound kind;
// opts.Kind is part of the store.Store interface but unused here since a
// Store[S, St] is single-kind by construction (see the Store doc comment)
// -- it exists for a hypothetical cross-kind implementation, not this one.
func (s *Store[S, St]) List(ctx context.Context, opts store.ListOptions) ([]resource.Resource[S, St], error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, labels, owner_refs, relationships, spec, status,
		       resource_version, created_at, updated_at
		FROM resources WHERE kind = ? ORDER BY created_at`, string(s.kind))
	if err != nil {
		return nil, fmt.Errorf("sqlite: list %s: %w", s.kind, err)
	}
	defer rows.Close()

	var out []resource.Resource[S, St]
	for rows.Next() {
		r, err := scanResource[S, St](rows, s.kind)
		if err != nil {
			return nil, err
		}
		if !matchesListOptions(r.Metadata, opts) {
			continue
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list %s: %w", s.kind, err)
	}
	return out, nil
}

// matchesListOptions applies OwnerID/Labels filtering in Go rather than
// SQL: the resources table stores these as JSON blobs
// (specs/001-resource-model/plan.md Risks: "SQL-level querying on spec/
// status fields is limited"), and at spec 001's target scale (10k
// resources, T016) in-process filtering after an indexed kind lookup is
// simpler and safer than depending on SQLite JSON1 function support.
func matchesListOptions(m resource.Metadata, opts store.ListOptions) bool {
	if opts.OwnerID != "" {
		found := false
		for _, ref := range m.OwnerRefs {
			if ref.ID == opts.OwnerID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for k, v := range opts.Labels {
		if m.Labels[k] != v {
			return false
		}
	}
	return true
}

func (s *Store[S, St]) Create(ctx context.Context, r resource.Resource[S, St]) (resource.Resource[S, St], error) {
	if r.Metadata.ID == "" {
		r.Metadata.ID = id.New()
	}
	r.Metadata.Kind = s.kind

	if err := resource.Validate(r.Metadata); err != nil {
		return resource.Resource[S, St]{}, err
	}

	now := time.Now().UTC()
	r.Metadata.CreatedAt = now
	r.Metadata.UpdatedAt = now
	r.Metadata.ResourceVersion = 1

	cols, err := marshalColumns(r)
	if err != nil {
		return resource.Resource[S, St]{}, err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO resources
			(id, kind, name, labels, owner_refs, relationships, spec, status,
			 resource_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Metadata.ID, string(s.kind), r.Metadata.Name,
		cols.labels, cols.ownerRefs, cols.relationships, cols.spec, cols.status,
		r.Metadata.ResourceVersion,
		now.Format(timeLayout), now.Format(timeLayout))
	if err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: create %s %s: %w", s.kind, r.Metadata.ID, err)
	}
	return r, nil
}

func (s *Store[S, St]) Update(ctx context.Context, r resource.Resource[S, St]) (resource.Resource[S, St], error) {
	if r.Metadata.ID == "" {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: update %s: Metadata.ID is required", s.kind)
	}
	r.Metadata.Kind = s.kind
	if err := resource.Validate(r.Metadata); err != nil {
		return resource.Resource[S, St]{}, err
	}

	cols, err := marshalColumns(r)
	if err != nil {
		return resource.Resource[S, St]{}, err
	}

	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE resources
		SET name = ?, labels = ?, owner_refs = ?, relationships = ?,
		    spec = ?, status = ?, resource_version = resource_version + 1,
		    updated_at = ?
		WHERE id = ? AND kind = ? AND resource_version = ?`,
		r.Metadata.Name, cols.labels, cols.ownerRefs, cols.relationships, cols.spec, cols.status,
		now.Format(timeLayout),
		r.Metadata.ID, string(s.kind), r.Metadata.ResourceVersion)
	if err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: update %s %s: %w", s.kind, r.Metadata.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: update %s %s: %w", s.kind, r.Metadata.ID, err)
	}
	if affected == 0 {
		// Distinguish "doesn't exist" from "version conflict" for a
		// clearer error to the caller.
		if _, getErr := s.Get(ctx, r.Metadata.ID); errors.Is(getErr, store.ErrNotFound) {
			return resource.Resource[S, St]{}, store.ErrNotFound
		}
		return resource.Resource[S, St]{}, store.ErrConflict
	}

	// Return the authoritative row (CreatedAt, incremented ResourceVersion,
	// new UpdatedAt) rather than reconstructing it from what the caller
	// passed in.
	return s.Get(ctx, r.Metadata.ID)
}

// Delete removes a resource unconditionally by id+kind. The cascade
// parameter is accepted for store.Store interface conformance but not
// yet acted on -- checking for "owns" children and rejecting/cascading
// is core/graph's job, wired into this method in T013.
func (s *Store[S, St]) Delete(ctx context.Context, resourceID string, cascade bool) error {
	_ = cascade
	result, err := s.db.ExecContext(ctx, `DELETE FROM resources WHERE id = ? AND kind = ?`, resourceID, string(s.kind))
	if err != nil {
		return fmt.Errorf("sqlite: delete %s %s: %w", s.kind, resourceID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: delete %s %s: %w", s.kind, resourceID, err)
	}
	if affected == 0 {
		return store.ErrNotFound
	}
	return nil
}

type marshaledColumns struct {
	labels, ownerRefs, relationships, spec, status string
}

func marshalColumns[S, St any](r resource.Resource[S, St]) (marshaledColumns, error) {
	labels, err := json.Marshal(r.Metadata.Labels)
	if err != nil {
		return marshaledColumns{}, fmt.Errorf("sqlite: marshal labels: %w", err)
	}
	ownerRefs, err := json.Marshal(r.Metadata.OwnerRefs)
	if err != nil {
		return marshaledColumns{}, fmt.Errorf("sqlite: marshal owner_refs: %w", err)
	}
	relationships, err := json.Marshal(r.Metadata.Relationships)
	if err != nil {
		return marshaledColumns{}, fmt.Errorf("sqlite: marshal relationships: %w", err)
	}
	spec, err := json.Marshal(r.Spec)
	if err != nil {
		return marshaledColumns{}, fmt.Errorf("sqlite: marshal spec: %w", err)
	}
	status, err := json.Marshal(r.Status)
	if err != nil {
		return marshaledColumns{}, fmt.Errorf("sqlite: marshal status: %w", err)
	}
	return marshaledColumns{
		labels:        string(labels),
		ownerRefs:     string(ownerRefs),
		relationships: string(relationships),
		spec:          string(spec),
		status:        string(status),
	}, nil
}

// rowScanner is satisfied by both *sql.Row (Get) and *sql.Rows (List).
type rowScanner interface {
	Scan(dest ...any) error
}

func scanResource[S, St any](row rowScanner, kind resource.Kind) (resource.Resource[S, St], error) {
	var (
		out                                          resource.Resource[S, St]
		labelsJSON, ownerRefsJSON, relationshipsJSON string
		specJSON, statusJSON                         string
		createdAtStr, updatedAtStr                   string
	)
	err := row.Scan(&out.Metadata.ID, &out.Metadata.Name,
		&labelsJSON, &ownerRefsJSON, &relationshipsJSON, &specJSON, &statusJSON,
		&out.Metadata.ResourceVersion, &createdAtStr, &updatedAtStr)
	if errors.Is(err, sql.ErrNoRows) {
		return resource.Resource[S, St]{}, store.ErrNotFound
	}
	if err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: scan %s: %w", kind, err)
	}

	out.Metadata.Kind = kind
	if err := json.Unmarshal([]byte(labelsJSON), &out.Metadata.Labels); err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: unmarshal labels: %w", err)
	}
	if err := json.Unmarshal([]byte(ownerRefsJSON), &out.Metadata.OwnerRefs); err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: unmarshal owner_refs: %w", err)
	}
	if err := json.Unmarshal([]byte(relationshipsJSON), &out.Metadata.Relationships); err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: unmarshal relationships: %w", err)
	}
	if err := json.Unmarshal([]byte(specJSON), &out.Spec); err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: unmarshal spec: %w", err)
	}
	if err := json.Unmarshal([]byte(statusJSON), &out.Status); err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: unmarshal status: %w", err)
	}

	out.Metadata.CreatedAt, err = time.Parse(timeLayout, createdAtStr)
	if err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: parse created_at: %w", err)
	}
	out.Metadata.UpdatedAt, err = time.Parse(timeLayout, updatedAtStr)
	if err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: parse updated_at: %w", err)
	}
	return out, nil
}
