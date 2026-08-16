package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/id"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

const timeLayout = time.RFC3339Nano

// Store is the SQLite implementation of store.Store[S, St] for one
// resource kind, fixed at construction (kind is a struct field, not a
// per-call parameter, because Get/Update/Delete in the store.Store
// interface don't take a kind argument). Every mutation (Create/Update/
// Delete) writes its audit_log row AND syncs the edges table inside the
// same transaction as the data change -- neither is an optional wrapper a
// caller could forget (constitution rule 8, resource-model.md's graph,
// specs/001-resource-model/plan.md). Cascade-delete of "owns" children
// may span multiple resource kinds (e.g. deleting an Organization cascades
// into Projects, Applications, ...), so it operates on raw table rows via
// core/graph.ResourceRef rather than this type's own S, St -- see
// cascadeDeleteChild below. After a successful commit, one event.Event is
// published per mutation if a Bus was set via SetBus -- publishing is
// deliberately outside the transaction (an event about a mutation nobody
// can prove committed yet is worse than a slightly-delayed one).
type Store[S, St any] struct {
	db   *sql.DB
	kind resource.Kind
	bus  event.Bus // nil is valid: Publish becomes a no-op
}

// NewStore returns a Store scoped to one resource kind, e.g.
// NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization).
// No event.Bus is attached; call SetBus if you want mutation events.
func NewStore[S, St any](db *sql.DB, kind resource.Kind) *Store[S, St] {
	return &Store[S, St]{db: db, kind: kind}
}

// SetBus attaches an event.Bus so subsequent mutations publish to it.
// Returns s for chaining, e.g. NewStore[...](db, kind).SetBus(bus).
func (s *Store[S, St]) SetBus(bus event.Bus) *Store[S, St] {
	s.bus = bus
	return s
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

	ref := graph.ResourceRef{Kind: s.kind, ID: r.Metadata.ID}
	if err := graph.SyncEdges(ctx, tx, ref, r.Metadata.OwnerRefs, r.Metadata.Relationships); err != nil {
		return resource.Resource[S, St]{}, err
	}

	afterJSON, err := json.Marshal(r)
	if err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: marshal audit after: %w", err)
	}
	actor := audit.ActorFrom(ctx)
	if err := writeAudit(ctx, tx, audit.Record{
		ID:         id.New(),
		Actor:      actor,
		Interface:  audit.InterfaceFrom(ctx),
		Kind:       string(s.kind),
		ResourceID: r.Metadata.ID,
		Before:     nil, // nothing existed before a Create
		After:      afterJSON,
		At:         now,
		RequestID:  audit.RequestIDFrom(ctx),
	}); err != nil {
		return resource.Resource[S, St]{}, err
	}

	if err := tx.Commit(); err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: create %s %s: commit: %w", s.kind, r.Metadata.ID, err)
	}
	s.publish(event.EventCreated, r.Metadata.ID, actor, now)
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

	ref := graph.ResourceRef{Kind: s.kind, ID: r.Metadata.ID}
	if err := graph.SyncEdges(ctx, tx, ref, r.Metadata.OwnerRefs, r.Metadata.Relationships); err != nil {
		return resource.Resource[S, St]{}, err
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

	actor := audit.ActorFrom(ctx)
	if err := writeAudit(ctx, tx, audit.Record{
		ID:         id.New(),
		Actor:      actor,
		Interface:  audit.InterfaceFrom(ctx),
		Kind:       string(s.kind),
		ResourceID: r.Metadata.ID,
		Before:     beforeJSON,
		After:      afterJSON,
		At:         now,
		RequestID:  audit.RequestIDFrom(ctx),
	}); err != nil {
		return resource.Resource[S, St]{}, err
	}

	if err := tx.Commit(); err != nil {
		return resource.Resource[S, St]{}, fmt.Errorf("sqlite: update %s %s: commit: %w", s.kind, r.Metadata.ID, err)
	}
	s.publish(event.EventUpdated, r.Metadata.ID, actor, now)
	// Return the authoritative post-commit row (CreatedAt preserved,
	// incremented ResourceVersion, new UpdatedAt) rather than
	// reconstructing it from what the caller passed in.
	return after, nil
}

// Delete removes a resource by id+kind. If it has "owns" children
// (docs/architecture/resource-model.md "Relationships"), cascade=false
// rejects with store.ErrHasChildren; cascade=true deletes every child
// first (post-order, deepest first), each with its own audit record,
// entirely within this one transaction.
func (s *Store[S, St]) Delete(ctx context.Context, resourceID string, cascade bool) error {
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

	ref := graph.ResourceRef{Kind: s.kind, ID: resourceID}
	children, err := graph.Children(ctx, tx, ref)
	if err != nil {
		return err
	}
	actor := audit.ActorFrom(ctx)
	var cascaded []graph.ResourceRef
	if len(children) > 0 {
		if !cascade {
			return store.ErrHasChildren
		}
		visited := map[graph.ResourceRef]bool{ref: true}
		for _, child := range children {
			if err := cascadeDeleteChild(ctx, tx, child, actor, visited, &cascaded); err != nil {
				return err
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM resources WHERE id = ? AND kind = ?`, resourceID, string(s.kind)); err != nil {
		return fmt.Errorf("sqlite: delete %s %s: %w", s.kind, resourceID, err)
	}
	if err := graph.DeleteEdgesFor(ctx, tx, ref); err != nil {
		return err
	}

	now := time.Now().UTC()
	if err := writeAudit(ctx, tx, audit.Record{
		ID:         id.New(),
		Actor:      actor,
		Interface:  audit.InterfaceFrom(ctx),
		Kind:       string(s.kind),
		ResourceID: resourceID,
		Before:     beforeJSON,
		After:      nil, // nothing exists after a Delete
		At:         now,
		RequestID:  audit.RequestIDFrom(ctx),
	}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: delete %s %s: commit: %w", s.kind, resourceID, err)
	}
	for _, child := range cascaded {
		s.publishKind(event.EventDeleted, child.Kind, child.ID, actor, now)
	}
	s.publish(event.EventDeleted, resourceID, actor, now)
	return nil
}

// cascadeDeleteChild recursively deletes ref and everything it
// transitively owns, post-order (grandchildren before children before
// ref), each deletion writing its own audit record and appending to
// *deleted so the caller can publish an event per cascaded resource after
// the whole transaction commits (events are never published mid-
// transaction -- see the Store doc comment). Kind-agnostic because a
// cascade can span multiple resource kinds (an Organization's children
// are Projects, whose children are Applications, ...) that this Store's
// own S, St type parameters can't represent.
//
// visited guards against a cyclic owns-graph (nothing in resource.Validate
// currently rejects one) causing unbounded recursion -- a malformed
// OwnerRefs cycle is rejected with an error instead of a stack overflow.
func cascadeDeleteChild(ctx context.Context, tx *sql.Tx, ref graph.ResourceRef, actor string, visited map[graph.ResourceRef]bool, deleted *[]graph.ResourceRef) error {
	if visited[ref] {
		return fmt.Errorf("sqlite: cascade delete: cycle detected in owns-graph at %s %s", ref.Kind, ref.ID)
	}
	visited[ref] = true

	children, err := graph.Children(ctx, tx, ref)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := cascadeDeleteChild(ctx, tx, child, actor, visited, deleted); err != nil {
			return err
		}
	}

	beforeJSON, err := rawSnapshotJSON(ctx, tx, ref)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM resources WHERE id = ? AND kind = ?`, ref.ID, string(ref.Kind)); err != nil {
		return fmt.Errorf("sqlite: cascade delete %s %s: %w", ref.Kind, ref.ID, err)
	}
	if err := graph.DeleteEdgesFor(ctx, tx, ref); err != nil {
		return err
	}
	*deleted = append(*deleted, ref)

	return writeAudit(ctx, tx, audit.Record{
		ID:         id.New(),
		Actor:      actor,
		Interface:  audit.InterfaceFrom(ctx),
		Kind:       string(ref.Kind),
		ResourceID: ref.ID,
		Before:     beforeJSON,
		After:      nil,
		At:         time.Now().UTC(),
		RequestID:  audit.RequestIDFrom(ctx),
	})
}

// rawSnapshotJSON marshals a resources row into an audit "before"
// snapshot without needing the row's Spec/Status Go types -- Spec/Status
// are re-wrapped verbatim via json.RawMessage (their shape is unknown
// here, that's the whole point), but Labels/OwnerRefs/Relationships are
// unmarshaled into their real typed values (map/slice), not RawMessage --
// wrapping raw DB column text (which is the literal 4-byte string "null"
// for an empty map/slice) defeats omitempty, since RawMessage("null") has
// len()==4 and never counts as empty. Matches what scanResource already
// does for exactly this reason (needed by cascadeDeleteChild, which
// encounters kinds this Store[S, St] instance was never constructed for).
//
// Shape and key casing deliberately mirror resource.Resource[S, St]'s own
// marshaled form ({"metadata": {...}, "spec": ..., "status": ...},
// lowerCamelCase per core/resource's json tags, T005-T010) -- a cascaded
// child's audit snapshot must look like any other resource's, not a
// different ad hoc shape, or the audit trail would be inconsistent
// depending on whether a deletion happened to cascade (constitution
// rule 8: audit records' "what" should be uniform).
func rawSnapshotJSON(ctx context.Context, tx *sql.Tx, ref graph.ResourceRef) (json.RawMessage, error) {
	var snap struct {
		Metadata struct {
			ID              string                  `json:"id"`
			Name            string                  `json:"name"`
			Kind            string                  `json:"kind"`
			Labels          map[string]string       `json:"labels,omitempty"`
			OwnerRefs       []resource.OwnerRef     `json:"ownerRefs,omitempty"`
			Relationships   []resource.Relationship `json:"relationships,omitempty"`
			CreatedAt       string                  `json:"createdAt"`
			UpdatedAt       string                  `json:"updatedAt"`
			ResourceVersion int64                   `json:"resourceVersion"`
		} `json:"metadata"`
		Spec   json.RawMessage `json:"spec"`
		Status json.RawMessage `json:"status"`
	}
	snap.Metadata.Kind = string(ref.Kind)

	var labelsJSON, ownerRefsJSON, relationshipsJSON, spec, status string
	err := tx.QueryRowContext(ctx, `
		SELECT id, name, labels, owner_refs, relationships, spec, status,
		       resource_version, created_at, updated_at
		FROM resources WHERE id = ? AND kind = ?`, ref.ID, string(ref.Kind),
	).Scan(&snap.Metadata.ID, &snap.Metadata.Name, &labelsJSON, &ownerRefsJSON, &relationshipsJSON, &spec, &status,
		&snap.Metadata.ResourceVersion, &snap.Metadata.CreatedAt, &snap.Metadata.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: raw snapshot %s %s: %w", ref.Kind, ref.ID, err)
	}
	if err := json.Unmarshal([]byte(labelsJSON), &snap.Metadata.Labels); err != nil {
		return nil, fmt.Errorf("sqlite: unmarshal raw snapshot labels: %w", err)
	}
	if err := json.Unmarshal([]byte(ownerRefsJSON), &snap.Metadata.OwnerRefs); err != nil {
		return nil, fmt.Errorf("sqlite: unmarshal raw snapshot owner_refs: %w", err)
	}
	if err := json.Unmarshal([]byte(relationshipsJSON), &snap.Metadata.Relationships); err != nil {
		return nil, fmt.Errorf("sqlite: unmarshal raw snapshot relationships: %w", err)
	}
	snap.Spec = json.RawMessage(spec)
	snap.Status = json.RawMessage(status)

	out, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("sqlite: marshal raw snapshot %s %s: %w", ref.Kind, ref.ID, err)
	}
	return out, nil
}

// publish sends an event.Event for this Store's own kind to s.bus, if one
// is set (SetBus) -- a no-op otherwise. Always called after tx.Commit()
// has already succeeded, never before (see the Store doc comment).
func (s *Store[S, St]) publish(eventType event.EventType, resourceID, actor string, at time.Time) {
	s.publishKind(eventType, s.kind, resourceID, actor, at)
}

// publishKind is like publish but for an arbitrary kind -- used for
// cascade-deleted children, which may be a different kind than this
// Store was constructed for.
func (s *Store[S, St]) publishKind(eventType event.EventType, kind resource.Kind, resourceID, actor string, at time.Time) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(event.Event{Type: eventType, Kind: kind, ID: resourceID, Actor: actor, At: at})
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
		INSERT INTO audit_log (id, actor, interface, kind, resource_id, before, after, at, policy_decision, request_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.Actor, rec.Interface, rec.Kind, rec.ResourceID,
		nullableJSON(rec.Before), nullableJSON(rec.After), rec.At.Format(timeLayout), policyDecision,
		nullableString(rec.RequestID))
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

// nullableString maps an empty string to SQL NULL -- used for RequestID,
// which is empty for any mutation not made through an HTTP request
// (audit.RequestIDFrom's documented zero value).
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
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
