package policy

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
)

// Principal is the caller a decision is about.
type Principal struct {
	Type resource.PrincipalType
	ID   string
	// Groups are the group names the caller belongs to, from the identity
	// provider. Assignments to any of them apply to the caller.
	Groups []string
}

// Action is what the caller wants to do. Data marks a data-plane action
// (a Secret's value), checked only against dataActions.
type Action struct {
	Name string
	Data bool
}

// Effect is a decision's outcome.
type Effect string

const (
	Allow Effect = "allow"
	Deny  Effect = "deny"
	// NeedsApproval is reserved for the approvals spec (019); nothing in
	// this spec returns it.
	NeedsApproval Effect = "needsApproval"
)

// Evidence names the assignment that granted an allowed action (Azure's
// "evidence"), recorded in the audit log's policy decision.
type Evidence struct {
	AssignmentID     string
	RoleDefinitionID string
	RoleName         string
	Scope            resource.Scope
}

// Decision is the answer to "may this principal do this here?".
type Decision struct {
	Effect   Effect
	Evidence *Evidence
}

// String renders the decision for the audit log: "allow: Owner (<id>) at
// <scope>" or "deny: missing <action> at <target>".
func (d Decision) String() string { return d.describe("", resource.RootScope) }

func (d Decision) describe(action string, target resource.Scope) string {
	if d.Effect == Allow && d.Evidence != nil {
		return fmt.Sprintf("allow: %s (%s) at %s", d.Evidence.RoleName, d.Evidence.AssignmentID, d.Evidence.Scope)
	}
	if action == "" {
		return string(d.Effect)
	}
	return fmt.Sprintf("%s: missing %s at %s", d.Effect, action, target)
}

// Describe renders the decision with the action and target, for audit rows
// and 403 responses.
func (d Decision) Describe(a Action, target resource.Scope) string { return d.describe(a.Name, target) }

// Errors from the assignment checks. Each wraps ErrForbidden or
// resource.ErrValidation so the API can map it to 403 or 400.
var (
	ErrForbidden     = errors.New("policy: forbidden")
	ErrLastOwner     = fmt.Errorf("%w: the last Owner at \"/\" can't be removed", ErrForbidden)
	ErrUnknownRole   = fmt.Errorf("%w: no such role definition", resource.ErrValidation)
	ErrNotAssignable = fmt.Errorf("%w: the role can't be assigned at this scope", resource.ErrValidation)
)

// AncestorFunc returns a resource's owners transitively, nearest first.
type AncestorFunc func(ctx context.Context, ref graph.ResourceRef) ([]graph.ResourceRef, error)

// Engine evaluates authorization decisions. Safe for concurrent use.
type Engine struct {
	assignments store.Store[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus]
	definitions store.Store[resource.RoleDefinitionSpec, resource.RoleDefinitionStatus]
	ancestors   AncestorFunc

	stale atomic.Bool

	// loadMu serialises reloads; the store reads happen under it, outside
	// mu, so decisions keep reading the current index meanwhile.
	loadMu sync.Mutex

	mu        sync.RWMutex
	byID      map[string]resource.RoleAssignment
	customDef map[string]resource.RoleDefinition

	// mutateMu serialises "check, then write" for role assignments, so two
	// concurrent removals of different root Owners can't both pass the
	// last-Owner check (LockAssignments).
	mutateMu sync.Mutex
}

// NewEngine returns an engine reading assignments and custom roles from
// the given stores and walking ownership with ancestors. It loads lazily,
// on the first decision.
func NewEngine(assignments store.Store[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus],
	definitions store.Store[resource.RoleDefinitionSpec, resource.RoleDefinitionStatus], ancestors AncestorFunc) *Engine {
	e := &Engine{assignments: assignments, definitions: definitions, ancestors: ancestors}
	e.stale.Store(true)
	return e
}

// Watch marks the index stale whenever an assignment or a custom role
// changes, so the next decision sees the change. It returns a function
// that stops watching.
func (e *Engine) Watch(bus event.Bus) (stop func()) {
	invalidate := func(event.Event) { e.stale.Store(true) }
	stopA := bus.Subscribe(resource.KindRoleAssignment, invalidate)
	stopD := bus.Subscribe(resource.KindRoleDefinition, invalidate)
	return func() { stopA(); stopD() }
}

// Run marks the index stale every interval until ctx ends, so changes made
// outside this process (the break-glass command) are picked up. Each tick
// costs one reload of a few small tables; an interval under a second is
// raised to one.
func (e *Engine) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(max(interval, time.Second))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.stale.Store(true)
		}
	}
}

// Invalidate marks the index stale.
func (e *Engine) Invalidate() { e.stale.Store(true) }

// refresh rebuilds the index from the stores if it's stale.
func (e *Engine) refresh(ctx context.Context) error {
	if !e.stale.Load() {
		return nil
	}
	e.loadMu.Lock()
	defer e.loadMu.Unlock()
	if !e.stale.Load() {
		return nil // another caller reloaded while this one waited
	}
	// Clear first: a change landing during the reload marks it stale again.
	e.stale.Store(false)
	as, err := e.assignments.List(ctx, store.ListOptions{})
	if err != nil {
		e.stale.Store(true)
		return fmt.Errorf("policy: load role assignments: %w", err)
	}
	ds, err := e.definitions.List(ctx, store.ListOptions{})
	if err != nil {
		e.stale.Store(true)
		return fmt.Errorf("policy: load role definitions: %w", err)
	}
	byID := make(map[string]resource.RoleAssignment, len(as))
	for _, a := range as {
		byID[a.Metadata.ID] = a
	}
	defs := make(map[string]resource.RoleDefinition, len(ds))
	for _, d := range ds {
		defs[d.Metadata.ID] = d
	}
	e.mu.Lock()
	e.byID, e.customDef = byID, defs
	e.mu.Unlock()
	return nil
}

// LockAssignments serialises a role-assignment mutation with its check:
// the caller holds it across CheckAssign or CheckUnassign and the store
// write, then calls the returned unlock. Without it, two concurrent
// removals of different root Owners could each see the other still in
// place and both succeed.
func (e *Engine) LockAssignments() (unlock func()) {
	e.mutateMu.Lock()
	return e.mutateMu.Unlock
}

// Role returns a role definition by ID, built-in or custom.
func (e *Engine) Role(ctx context.Context, id string) (resource.RoleDefinitionSpec, bool, error) {
	if r, ok := builtInRoles[id]; ok {
		return r, true, nil
	}
	if err := e.refresh(ctx); err != nil {
		return resource.RoleDefinitionSpec{}, false, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	d, ok := e.customDef[id]
	return d.Spec, ok, nil
}

// scopeChain is the target, its ancestors nearest first, then "/".
func (e *Engine) scopeChain(ctx context.Context, target resource.Scope) ([]resource.Scope, error) {
	chain := []resource.Scope{}
	if !target.IsRoot() {
		chain = append(chain, target)
		up, err := e.ancestorsCached(ctx, graph.ResourceRef{Kind: target.Kind, ID: target.ID})
		if err != nil {
			return nil, err
		}
		for _, r := range up {
			chain = append(chain, resource.Scope{Kind: r.Kind, ID: r.ID})
		}
	}
	return append(chain, resource.RootScope), nil
}

// appliesTo reports whether an assignment's principal is p or one of p's
// groups.
func appliesTo(a resource.RoleAssignmentSpec, p Principal) bool {
	if a.Principal.Type == resource.PrincipalGroup {
		return slices.Contains(p.Groups, a.Principal.ID)
	}
	return a.Principal.Type == p.Type && a.Principal.ID == p.ID
}

// candidate is one assignment that applies to the caller at some level of
// the scope chain.
type candidate struct {
	id    string
	spec  resource.RoleAssignmentSpec
	role  resource.RoleDefinitionSpec
	depth int // 0 = the target itself
}

// applicable returns the assignments that apply to p anywhere on target's
// scope chain, nearest scope first, then by assignment ID, so evidence is
// deterministic.
func (e *Engine) applicable(ctx context.Context, p Principal, target resource.Scope) ([]candidate, error) {
	if err := e.refresh(ctx); err != nil {
		return nil, err
	}
	chain, err := e.scopeChain(ctx, target)
	if err != nil {
		return nil, err
	}
	depth := make(map[resource.Scope]int, len(chain))
	for i, s := range chain {
		if _, seen := depth[s]; !seen {
			depth[s] = i
		}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []candidate
	for id, a := range e.byID {
		d, onChain := depth[a.Spec.Scope]
		if !onChain || !appliesTo(a.Spec, p) {
			continue
		}
		role, ok := builtInRoles[a.Spec.RoleDefinitionID]
		if !ok {
			def, found := e.customDef[a.Spec.RoleDefinitionID]
			if !found {
				continue // a dangling assignment grants nothing
			}
			role = def.Spec
		}
		out = append(out, candidate{id: id, spec: a.Spec, role: role, depth: d})
	}
	slices.SortFunc(out, func(x, y candidate) int {
		if x.depth != y.depth {
			return x.depth - y.depth
		}
		if x.id < y.id {
			return -1
		}
		if x.id > y.id {
			return 1
		}
		return 0
	})
	return out, nil
}

// Decide answers whether p may perform a on target. It is Azure's
// evaluation: allowed when any applicable role's permission block grants
// the action; the evidence is the nearest such assignment.
func (e *Engine) Decide(ctx context.Context, p Principal, a Action, target resource.Scope) (Decision, error) {
	cands, err := e.applicable(ctx, p, target)
	if err != nil {
		return Decision{Effect: Deny}, err
	}
	for _, c := range cands {
		for _, perm := range c.role.Permissions {
			if grants(perm, a) {
				return Decision{Effect: Allow, Evidence: &Evidence{
					AssignmentID: c.id, RoleDefinitionID: c.spec.RoleDefinitionID, RoleName: c.role.RoleName, Scope: c.spec.Scope,
				}}, nil
			}
		}
	}
	return Decision{Effect: Deny}, nil
}

// EffectivePermissions returns the permission blocks that apply to p at
// target (Azure's permissions API). A client decides what to offer from
// them; the server still decides every request.
func (e *Engine) EffectivePermissions(ctx context.Context, p Principal, target resource.Scope) ([]resource.Permission, error) {
	cands, err := e.applicable(ctx, p, target)
	if err != nil {
		return nil, err
	}
	var out []resource.Permission
	for _, c := range cands {
		out = append(out, c.role.Permissions...)
	}
	return out, nil
}

// Allows reports whether a set of effective permissions grants a.
func Allows(perms []resource.Permission, a Action) bool {
	for _, p := range perms {
		if grants(p, a) {
			return true
		}
	}
	return false
}

// CheckAssign decides whether caller may create assignment spec (plan.md
// decision 10). The caller needs roleAssignments/write at the assignment's
// scope. If every assignment granting that carries a delegation, the role
// and principal type must be on one of those delegations' allow-lists, and
// the caller may not assign to themselves. The role must exist and, for a
// custom role, be assignable at that scope or one of its ancestors.
func (e *Engine) CheckAssign(ctx context.Context, caller Principal, spec resource.RoleAssignmentSpec) error {
	role, ok, err := e.Role(ctx, spec.RoleDefinitionID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrUnknownRole
	}
	if !IsBuiltIn(spec.RoleDefinitionID) {
		if err := e.checkAssignable(ctx, role, spec.Scope); err != nil {
			return err
		}
	}
	return e.checkDelegated(ctx, caller, Action{Name: ActionRoleAssignmentsWrite}, spec, true)
}

// CheckUnassign decides whether caller may delete the assignment with id:
// first whether they may remove it at all, then the last-Owner guard.
// Hold LockAssignments across this check and the delete.
func (e *Engine) CheckUnassign(ctx context.Context, caller Principal, id string) error {
	if err := e.refresh(ctx); err != nil {
		return err
	}
	e.mu.RLock()
	a, ok := e.byID[id]
	e.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: no role assignment %s", store.ErrNotFound, id)
	}
	if err := e.checkDelegated(ctx, caller, Action{Name: ActionRoleAssignmentsDelete}, a.Spec, false); err != nil {
		return err
	}
	last, err := e.IsLastRootOwner(ctx, id)
	if err != nil {
		return err
	}
	if last {
		return ErrLastOwner
	}
	return nil
}

// isSelf reports whether an assignment's principal is the caller or one of
// the caller's groups: through a constrained delegation, a caller may not
// grant to either, or they could raise their own access via a group.
func isSelf(p resource.PrincipalRef, caller Principal) bool {
	if p.Type == resource.PrincipalGroup {
		return slices.Contains(caller.Groups, p.ID)
	}
	return p.Type == caller.Type && p.ID == caller.ID
}

func (e *Engine) checkDelegated(ctx context.Context, caller Principal, a Action, spec resource.RoleAssignmentSpec, creating bool) error {
	cands, err := e.applicable(ctx, caller, spec.Scope)
	if err != nil {
		return err
	}
	granted, constrainedOK := false, false
	for _, c := range cands {
		if !slices.ContainsFunc(c.role.Permissions, func(p resource.Permission) bool { return grants(p, a) }) {
			continue
		}
		granted = true
		d := c.spec.Delegation
		if d == nil {
			return nil // unconstrained, as an Owner or User Access Administrator is in Azure
		}
		if slices.Contains(d.RoleDefinitionIDs, spec.RoleDefinitionID) && slices.Contains(d.PrincipalTypes, spec.Principal.Type) &&
			!(creating && isSelf(spec.Principal, caller)) {
			constrainedOK = true
		}
	}
	switch {
	case !granted:
		return fmt.Errorf("%w: missing %s at %s", ErrForbidden, a.Name, spec.Scope)
	case !constrainedOK && creating:
		return fmt.Errorf("%w: your delegation doesn't allow this role or principal type, or granting to yourself or your own groups", ErrForbidden)
	case !constrainedOK:
		return fmt.Errorf("%w: your delegation doesn't cover this role or principal type", ErrForbidden)
	}
	return nil
}

func (e *Engine) checkAssignable(ctx context.Context, role resource.RoleDefinitionSpec, at resource.Scope) error {
	chain, err := e.scopeChain(ctx, at)
	if err != nil {
		return err
	}
	for _, s := range chain {
		if slices.Contains(role.AssignableScopes, s) {
			return nil
		}
	}
	return ErrNotAssignable
}

// IsLastRootOwner reports whether the assignment with id is the only Owner
// assignment at "/". When the index can't be loaded it returns true with
// the error, so a caller that ignores the error still refuses rather than
// risk a lockout.
func (e *Engine) IsLastRootOwner(ctx context.Context, id string) (bool, error) {
	if err := e.refresh(ctx); err != nil {
		return true, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	a, ok := e.byID[id]
	if !ok || a.Spec.RoleDefinitionID != OwnerRoleID || !a.Spec.Scope.IsRoot() {
		return false, nil
	}
	owners := 0
	for _, x := range e.byID {
		if x.Spec.RoleDefinitionID == OwnerRoleID && x.Spec.Scope.IsRoot() {
			owners++
		}
	}
	return owners <= 1, nil
}

// RoleInUse reports whether any assignment references the role, so a custom
// role can't be deleted while assigned (Azure's rule).
func (e *Engine) RoleInUse(ctx context.Context, roleID string) (bool, error) {
	if err := e.refresh(ctx); err != nil {
		return false, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, a := range e.byID {
		if a.Spec.RoleDefinitionID == roleID {
			return true, nil
		}
	}
	return false, nil
}

// Assignments returns every assignment sorted by ID, for listing and
// filtering by callers that already decided read access. The values share
// slices with the index: callers must not modify them.
func (e *Engine) Assignments(ctx context.Context) ([]resource.RoleAssignment, error) {
	if err := e.refresh(ctx); err != nil {
		return nil, err
	}
	e.mu.RLock()
	out := slices.Collect(maps.Values(e.byID))
	e.mu.RUnlock()
	slices.SortFunc(out, func(a, b resource.RoleAssignment) int { return strings.Compare(a.Metadata.ID, b.Metadata.ID) })
	return out, nil
}

// ancestorCacheKey memoises ancestor walks for the life of one request.
type ancestorCacheKey struct{}

type ancestorCache struct {
	mu sync.Mutex
	m  map[graph.ResourceRef][]graph.ResourceRef
}

// WithAncestorCache returns a context in which ancestor walks are memoised,
// so filtering a list doesn't walk the same chain once per item.
func WithAncestorCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, ancestorCacheKey{}, &ancestorCache{m: map[graph.ResourceRef][]graph.ResourceRef{}})
}

func (e *Engine) ancestorsCached(ctx context.Context, ref graph.ResourceRef) ([]graph.ResourceRef, error) {
	c, _ := ctx.Value(ancestorCacheKey{}).(*ancestorCache)
	if c != nil {
		c.mu.Lock()
		got, ok := c.m[ref]
		c.mu.Unlock()
		if ok {
			return got, nil
		}
	}
	up, err := e.ancestors(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("policy: resolve scope of %s %s: %w", ref.Kind, ref.ID, err)
	}
	if c != nil {
		c.mu.Lock()
		c.m[ref] = up
		c.mu.Unlock()
	}
	return up, nil
}
