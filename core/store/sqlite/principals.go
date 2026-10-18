package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// KnownPrincipal is a principal serendipd has seen authenticate (spec 017
// plan.md decision 6): who can be picked when assigning a role.
type KnownPrincipal struct {
	Type        resource.PrincipalType `json:"type"`
	ID          string                 `json:"id"`
	DisplayName string                 `json:"displayName,omitempty"`
	// Groups are the groups a person belonged to when last seen.
	Groups    []string  `json:"groups,omitempty"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
}

// seenGranularity bounds how often an unchanged principal is rewritten, so
// a busy console doesn't write on every request.
const seenGranularity = time.Minute

// PrincipalRegistry records principals as they authenticate. Safe for
// concurrent use.
type PrincipalRegistry struct {
	db  *sql.DB
	now func() time.Time

	mu     sync.Mutex
	recent map[principalKey]seen
}

type principalKey struct {
	t  resource.PrincipalType
	id string
}

type seen struct {
	at      time.Time
	display string
	groups  []string
}

// NewPrincipalRegistry returns a registry over db, which must be migrated.
func NewPrincipalRegistry(db *sql.DB) *PrincipalRegistry {
	return &PrincipalRegistry{db: db, now: time.Now, recent: map[principalKey]seen{}}
}

// Seen records that p authenticated now, and records each of a person's
// groups as a group principal. An unchanged principal is written at most
// once a minute.
func (r *PrincipalRegistry) Seen(ctx context.Context, p KnownPrincipal) error {
	now := r.now().UTC()
	k := principalKey{p.Type, p.ID}
	groups := slices.Clone(p.Groups)
	slices.Sort(groups)
	r.mu.Lock()
	prev, ok := r.recent[k]
	r.mu.Unlock()
	if ok && now.Sub(prev.at) < seenGranularity && prev.display == p.DisplayName && slices.Equal(prev.groups, groups) {
		return nil
	}

	g, err := json.Marshal(groups)
	if err != nil {
		return err
	}
	// The caller's request may end first; the record should still land.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: record principal: %w", err)
	}
	defer tx.Rollback()
	ts := now.Format(timeLayout)
	if err := upsertPrincipal(ctx, tx, p.Type, p.ID, p.DisplayName, string(g), ts); err != nil {
		return err
	}
	for _, name := range groups {
		if err := upsertPrincipal(ctx, tx, resource.PrincipalGroup, name, name, "[]", ts); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: record principal: %w", err)
	}
	// Remembered only once written, so a failed write is retried on the
	// next request. Two concurrent first sightings both write; the upsert
	// makes that harmless.
	r.mu.Lock()
	r.recent[k] = seen{at: now, display: p.DisplayName, groups: groups}
	r.mu.Unlock()
	return nil
}

func upsertPrincipal(ctx context.Context, tx *sql.Tx, t resource.PrincipalType, id, display, groups, ts string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO principals (type, id, display_name, groups, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (type, id) DO UPDATE SET display_name = excluded.display_name, groups = excluded.groups, last_seen = excluded.last_seen`,
		string(t), id, display, groups, ts, ts)
	if err != nil {
		return fmt.Errorf("sqlite: record principal %s %s: %w", t, id, err)
	}
	return nil
}

func (r *PrincipalRegistry) forget(k principalKey) {
	r.mu.Lock()
	delete(r.recent, k)
	r.mu.Unlock()
}

// List returns known principals, of one type or (with "") of every type,
// most recently seen first.
func (r *PrincipalRegistry) List(ctx context.Context, t resource.PrincipalType) ([]KnownPrincipal, error) {
	q := `SELECT type, id, display_name, groups, first_seen, last_seen FROM principals`
	var args []any
	if t != "" {
		q += ` WHERE type = ?`
		args = append(args, string(t))
	}
	rows, err := r.db.QueryContext(ctx, q+` ORDER BY last_seen DESC, type, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list principals: %w", err)
	}
	defer rows.Close()
	var out []KnownPrincipal
	for rows.Next() {
		var p KnownPrincipal
		var typ, groups, first, last string
		if err := rows.Scan(&typ, &p.ID, &p.DisplayName, &groups, &first, &last); err != nil {
			return nil, fmt.Errorf("sqlite: scan principal: %w", err)
		}
		p.Type = resource.PrincipalType(typ)
		if err := json.Unmarshal([]byte(groups), &p.Groups); err != nil {
			return nil, fmt.Errorf("sqlite: principal %s %s groups: %w", typ, p.ID, err)
		}
		if p.FirstSeen, err = time.Parse(timeLayout, first); err != nil {
			return nil, err
		}
		if p.LastSeen, err = time.Parse(timeLayout, last); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Forget removes a principal, e.g. an API key no longer configured.
func (r *PrincipalRegistry) Forget(ctx context.Context, t resource.PrincipalType, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM principals WHERE type = ? AND id = ?`, string(t), id); err != nil {
		return fmt.Errorf("sqlite: forget principal %s %s: %w", t, id, err)
	}
	r.forget(principalKey{t, id})
	return nil
}

// EverAssigned reports whether the audit log records any role assignment
// ever made to the principal, including since-deleted ones. Startup
// migrations use it so an access an operator revoked isn't granted again
// (spec 017 plan.md decision 7).
func EverAssigned(ctx context.Context, db *sql.DB, t resource.PrincipalType, id string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM audit_log
		WHERE kind = ? AND json_extract(after, '$.spec.principal.type') = ? AND json_extract(after, '$.spec.principal.id') = ?`,
		string(resource.KindRoleAssignment), string(t), id).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("sqlite: look up past assignments of %s %s: %w", t, id, err)
	}
	return n > 0, nil
}
