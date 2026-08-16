package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/id"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

const timeLayout = time.RFC3339Nano

// Store is the SQLite implementation of store.Store[S, St] for one
// resource kind, fixed at construction (kind is a struct field, not a
// per-call parameter, because Get/Update/Delete in the store.Store
// interface don't take a kind argument). Every mutation (Create/Update/
// Delete) writes its audit_log row inside the same transaction as the
// data change -- not an optional wrapper a caller could forget
// (constitution rule 8, specs/001-resource-model/plan.md). Graph-edge
// sync and cascade-delete children-checking are added on top of this in
// T013; event publishing in T014.
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

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: create %s %s: begin: %w", s.kind, r.Metadata.ID, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO resources
			(id, kind, name, labels, owner_refs, relationships, spec, status,
			 resource_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Metadata.ID, string(s.kind), r.Metadata.Name,
		cols.labels, cols.ownerRefs, cols.relationships, cols.spec, cols.status,
		r.Metadata.ResourceVersion,
		now.Format(timeLayout), now.Format(timeLayout)); err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: create %s %s: %w", s.kind, r.Metadata.ID, err)
	}

	afterJSON, err := json.Marshal(r)
	if err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: marshal audit after: %w", err)
	}
	if err := writeAudit(ctx, tx, audit.Record{
		ID:         id.New(),
		Actor:      audit.ActorFrom(ctx),
		Interface:  "core",
		Kind:       string(s.kind),
		ResourceID: r.Metadata.ID,
		Before:     nil, // nothing existed before a Create
		After:      afterJSON,
		At:         now,
	}); err != nil {
		return resource.Resource[S, St]{}, err
	}

	if err := tx.Commit(); err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: create %s %s: commit: %w", s.kind, r.Metadata.ID, err)
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

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: update %s %s: begin: %w", s.kind, r.Metadata.ID, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	// Read the pre-update row inside the same transaction: it's both the
	// audit "before" snapshot and the existence check. SetMaxOpenConns(1)
	// (conn.go) means no other transaction can interleave between this
	// read and the write below, so this is race-safe despite being a
	// read-then-write rather than a single atomic statement.
	before, err := scanResource[S, St](tx.QueryRowContext(ctx, `
		SELECT id, name, labels, owner_refs, relationships, spec, status,
		       resource_version, created_at, updated_at
		FROM resources WHERE id = ? AND kind = ?`, r.Metadata.ID, string(s.kind)), s.kind)
	if err != nil {
		return resource.Resource[S, St]{}, err // ErrNotFound already wrapped by scanResource
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: marshal audit before: %w", err)
	}

	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
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
		// The before-select above already proved the row exists, so zero
		// rows affected here specifically means a stale ResourceVersion.
		return resource.Resource[S, St]{}, store.ErrConflict
	}

	after, err := scanResource[S, St](tx.QueryRowContext(ctx, `
		SELECT id, name, labels, owner_refs, relationships, spec, status,
		       resource_version, created_at, updated_at
		FROM resources WHERE id = ? AND kind = ?`, r.Metadata.ID, string(s.kind)), s.kind)
	if err != nil {
		return resource.Resource[S, St]{}, err
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: marshal audit after: %w", err)
	}

	if err := writeAudit(ctx, tx, audit.Record{
		ID:         id.New(),
		Actor:      audit.ActorFrom(ctx),
		Interface:  "core",
		Kind:       string(s.kind),
		ResourceID: r.Metadata.ID,
		Before:     beforeJSON,
		After:      afterJSON,
		At:         now,
	}); err != nil {
		return resource.Resource[S, St]{}, err
	}

	if err := tx.Commit(); err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: update %s %s: commit: %w", s.kind, r.Metadata.ID, err)
	}
	// Return the authoritative post-commit row (CreatedAt preserved,
	// incremented ResourceVersion, new UpdatedAt) rather than
	// reconstructing it from what the caller passed in.
	return after, nil
}

// Delete removes a resource by id+kind. The cascade parameter is accepted
// for store.Store interface conformance but not yet acted on -- checking
// for "owns" children and rejecting/cascading is core/graph's job, wired
// into this method in T013.
func (s *Store[S, St]) Delete(ctx context.Context, resourceID string, cascade bool) error {
	_ = cascade

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: delete %s %s: begin: %w", s.kind, resourceID, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	before, err := scanResource[S, St](tx.QueryRowContext(ctx, `
		SELECT id, name, labels, owner_refs, relationships, spec, status,
		       resource_version, created_at, updated_at
		FROM resources WHERE id = ? AND kind = ?`, resourceID, string(s.kind)), s.kind)
	if err != nil {
		return err // ErrNotFound already wrapped by scanResource
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("sqlite: marshal audit before: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM resources WHERE id = ? AND kind = ?`, resourceID, string(s.kind)); err != nil {
		return fmt.Errorf("sqlite: delete %s %s: %w", s.kind, resourceID, err)
	}

	now := time.Now().UTC()
	if err := writeAudit(ctx, tx, audit.Record{
		ID:         id.New(),
		Actor:      audit.ActorFrom(ctx),
		Interface:  "core",
		Kind:       string(s.kind),
		ResourceID: resourceID,
		Before:     beforeJSON,
		After:      nil, // nothing exists after a Delete
		At:         now,
	}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: delete %s %s: commit: %w", s.kind, resourceID, err)
	}
	return nil
}

// writeAudit inserts one audit_log row as part of tx -- always called
// inside the same transaction as the data mutation it records, never
// after a separate Commit (constitution rule 8).
func writeAudit(ctx context.Context, tx *sql.Tx, rec audit.Record) error {
	var policyDecision any
	if rec.PolicyDecision != nil {
		policyDecision = *rec.PolicyDecision
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO audit_log (id, actor, interface, kind, resource_id, before, after, at, policy_decision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.Actor, rec.Interface, rec.Kind, rec.ResourceID,
		nullableJSON(rec.Before), nullableJSON(rec.After), rec.At.Format(timeLayout), policyDecision)
	if err != nil {
		return fmt.Errorf("sqlite: write audit record: %w", err)
	}
	return nil
}

func nullableJSON(b json.RawMessage) any {
	if b == nil {
		return nil
	}
	return string(b)
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
