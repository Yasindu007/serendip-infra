package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/graph"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// Mux is where a handler registers its routes: a plain *http.ServeMux in
// unit tests, or NewServer's authorizing mux, which refuses any route
// without an entry in the route table.
type Mux interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// targetKind says how a route finds the scope it's decided at
// (specs/017-authorization plan.md decision 3).
type targetKind int

const (
	// targetResource is the resource named by {id}.
	targetResource targetKind = iota
	// targetParent is the would-be parent of a created resource: each of
	// the body's ownerRefs, or "/" when there are none.
	targetParent
	// targetRoot is "/": platform-wide reads and settings.
	targetRoot
	// targetGraph is the resource named by {kind} and {id}.
	targetGraph
	// targetAssignmentBody is a role assignment's scope, from the body.
	targetAssignmentBody
	// targetRoleBody is each assignable scope of a custom role, from the
	// body: creating a role needs write wherever it could be assigned.
	targetRoleBody
	// targetAssignment is the scope of the role assignment named by {id}.
	targetAssignment
	// targetRole is each assignable scope of the custom role named by
	// {id}.
	targetRole
	// targetApprovalBody is the delete an approval request asks for, from
	// the body: the same checks as the DELETE itself (spec 019).
	targetApprovalBody
	// targetApprovalDecide is the delete the approval named by {id} asks
	// for, as it stands now: an approver must be allowed it themselves.
	targetApprovalDecide
	// targetApprovalRead is the approval named by {id}: read on its
	// target, or on the nearest owner that still exists.
	targetApprovalRead
)

// rule is one route's authorization requirement.
type rule struct {
	// authOnly routes need a caller and nothing more: anyone may ask who
	// they are (GET /v1/session).
	authOnly bool
	// list routes are fetched unchecked and filtered item by item to what
	// the caller may read (decision 4).
	list   bool
	action policy.Action
	target targetKind
	// kind is the resource kind a targetResource or targetParent route
	// is about.
	kind resource.Kind
	// allowNeedsApproval lets a NeedsApproval decision through, marked on
	// the context (needsApprovalFrom): an approval request is how such an
	// action is asked for. Everywhere else it is refused.
	allowNeedsApproval bool
}

// resourcePaths maps each resource kind served under /v1 to its path
// segment, which is also its action namespace (decision 2):
// serendip/{path}/{read|write|delete}.
var resourcePaths = map[resource.Kind]string{
	resource.KindOrganization:     "organizations",
	resource.KindTeam:             "teams",
	resource.KindProject:          "projects",
	resource.KindEnvironment:      "environments",
	resource.KindSite:             "sites",
	resource.KindCluster:          "clusters",
	resource.KindNode:             "nodes",
	resource.KindApplication:      "applications",
	resource.KindComponent:        "components",
	resource.KindCapability:       "capabilities",
	resource.KindProvider:         "providers",
	resource.KindProviderInstance: "provider-instances",
	resource.KindIncident:         "incidents",
	resource.KindAlert:            "alerts",
	resource.KindVolume:           "volumes",
	resource.KindSecret:           "secrets",
}

func authzAction(what, verb string) policy.Action {
	return policy.Action{Name: "serendip/authorization/" + what + "/" + verb}
}

// approvalsAction is decided at "/" when the approval a route names doesn't
// exist: only a "*" role holds it, so anyone else can't tell a missing
// approval from a forbidden one.
func approvalsAction(verb string) policy.Action {
	return policy.Action{Name: "serendip/approvals/" + verb}
}

func action(kind resource.Kind, verb string) policy.Action {
	return policy.Action{Name: "serendip/" + resourcePaths[kind] + "/" + verb}
}

// routeTable is the single source of every /v1 route's requirement. A
// route registered without an entry here panics at startup, and a test
// enumerates the registered routes, so a new route can't slip through
// unchecked (decision 2, acceptance criterion 9).
func routeTable() map[string]rule {
	t := map[string]rule{
		"GET /v1/session": {authOnly: true},
		// The catch-all only ever answers 404.
		"/": {authOnly: true},

		"GET /v1/readiness": {action: policy.Action{Name: "serendip/readiness/read"}, target: targetRoot},
		// A capability's default provider is a platform-wide setting.
		"GET /v1/capabilities/{name}/default": {action: policy.Action{Name: "serendip/capabilities/read"}, target: targetRoot},
		"PUT /v1/capabilities/{name}/default": {action: policy.Action{Name: "serendip/capabilities/write"}, target: targetRoot},

		// Read on the named resource; the action follows its kind.
		"GET /v1/graph/{kind}/{id}/children":     {target: targetGraph},
		"GET /v1/graph/{kind}/{id}/dependents":   {target: targetGraph},
		"GET /v1/graph/{kind}/{id}/blast-radius": {target: targetGraph},

		// Authorization itself (T006). Anyone may read role definitions and
		// ask what they may do: neither is secret, and someone with access
		// to one project still needs the role names to understand it. The
		// principals list is a directory, so its handler also requires
		// being able to assign roles somewhere (plan.md decision 2).
		"GET /v1/authorization/roleDefinitions":         {authOnly: true},
		"GET /v1/authorization/roleDefinitions/{id}":    {authOnly: true},
		"POST /v1/authorization/roleDefinitions":        {action: authzAction("roleDefinitions", "write"), target: targetRoleBody, kind: resource.KindRoleDefinition},
		"DELETE /v1/authorization/roleDefinitions/{id}": {action: authzAction("roleDefinitions", "delete"), target: targetRole, kind: resource.KindRoleDefinition},
		"GET /v1/authorization/roleAssignments":         {list: true, kind: resource.KindRoleAssignment},
		"GET /v1/authorization/roleAssignments/{id}":    {action: authzAction("roleAssignments", "read"), target: targetAssignment, kind: resource.KindRoleAssignment},
		"POST /v1/authorization/roleAssignments":        {action: authzAction("roleAssignments", "write"), target: targetAssignmentBody, kind: resource.KindRoleAssignment},
		"DELETE /v1/authorization/roleAssignments/{id}": {action: authzAction("roleAssignments", "delete"), target: targetAssignment, kind: resource.KindRoleAssignment},
		"GET /v1/authorization/permissions":             {authOnly: true},
		"GET /v1/authorization/principals":              {authOnly: true},
		// Directory data, gated in the handler like the list above.
		"GET /v1/authorization/principals/person/{id}/groups": {authOnly: true},
		"GET /v1/authorization/directory":                     {authOnly: true},

		// Approvals (spec 019). A request is decided like the delete it asks
		// for; deciding needs that delete allowed; reading follows the
		// target. The actions here apply only when the approval is missing.
		"POST /v1/approvals":              {target: targetApprovalBody, allowNeedsApproval: true},
		"GET /v1/approvals":               {list: true, kind: resource.KindApproval},
		"GET /v1/approvals/{id}":          {action: approvalsAction("read"), target: targetApprovalRead, kind: resource.KindApproval},
		"POST /v1/approvals/{id}/approve": {action: approvalsAction("write"), target: targetApprovalDecide, kind: resource.KindApproval},
		"POST /v1/approvals/{id}/reject":  {action: approvalsAction("write"), target: targetApprovalDecide, kind: resource.KindApproval},

		"POST /v1/secrets/{id}/value": {action: policy.Action{Name: "serendip/secrets/value/write", Data: true}, target: targetResource, kind: resource.KindSecret},
		"GET /v1/secrets/{id}/value":  {action: policy.Action{Name: "serendip/secrets/value/read", Data: true}, target: targetResource, kind: resource.KindSecret},
	}
	for kind, path := range resourcePaths {
		t["POST /v1/"+path] = rule{action: action(kind, "write"), target: targetParent, kind: kind}
		t["GET /v1/"+path] = rule{list: true, action: action(kind, "read"), kind: kind}
		t["GET /v1/"+path+"/{id}"] = rule{action: action(kind, "read"), target: targetResource, kind: kind}
		t["PUT /v1/"+path+"/{id}"] = rule{action: action(kind, "write"), target: targetResource, kind: kind}
		t["DELETE /v1/"+path+"/{id}"] = rule{action: action(kind, "delete"), target: targetResource, kind: kind}
	}
	return t
}

// authorizer decides every /v1 request against the policy engine.
type authorizer struct {
	engine *policy.Engine
	tree   ownership
	db     dbForAudit
	table  map[string]rule
	// approvals reads the approval an approval route names; exists says
	// whether a resource is still there (an approval outlives its target).
	approvals approvalReader
	exists    func(ctx context.Context, kind resource.Kind, id string) (bool, error)
}

// approvalReader reads one approval.
type approvalReader interface {
	Get(ctx context.Context, id string) (resource.Approval, error)
}

// ownership is what the authorizer reads from the resource graph.
type ownership interface {
	// Owners returns a resource's direct owners.
	Owners(ctx context.Context, ref graph.ResourceRef) ([]graph.ResourceRef, error)
	// Descendants returns everything a resource owns, at any depth.
	Descendants(ctx context.Context, ref graph.ResourceRef) ([]graph.ResourceRef, error)
}

// dbForAudit writes the audit row of a refused mutation.
type dbForAudit func(ctx context.Context, kind resource.Kind, resourceID, decision string) error

func newAuthorizer(engine *policy.Engine, tree ownership, recordDenied dbForAudit, approvals approvalReader, exists func(ctx context.Context, kind resource.Kind, id string) (bool, error)) *authorizer {
	return &authorizer{engine: engine, tree: tree, db: recordDenied, table: routeTable(), approvals: approvals, exists: exists}
}

// authzMux registers routes on mux wrapped in their authorization check.
type authzMux struct {
	mux      *http.ServeMux
	a        *authorizer
	patterns []string
}

func (m *authzMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	ru, ok := m.a.table[pattern]
	if !ok {
		panic(fmt.Sprintf("api: route %q has no authorization rule (api/authz.go routeTable)", pattern))
	}
	m.patterns = append(m.patterns, pattern)
	m.mux.HandleFunc(pattern, m.a.wrap(ru, h))
}

// callerKey carries the authenticated caller as the policy sees it.
type callerKey struct{}

func withCaller(ctx context.Context, p policy.Principal) context.Context {
	return context.WithValue(ctx, callerKey{}, p)
}

func callerFrom(ctx context.Context) (policy.Principal, bool) {
	p, ok := ctx.Value(callerKey{}).(policy.Principal)
	return p, ok && p.ID != ""
}

// readFilterKey carries the per-request "may the caller read this?" check
// that lists and graph results are filtered with.
type readFilterKey struct{}

type readFilter func(ctx context.Context, kind resource.Kind, id string) (bool, error)

// deciderKey carries a "may the caller do this there?" check for list
// handlers whose items aren't ordinary resources (role assignments).
type deciderKey struct{}

type decider func(ctx context.Context, act policy.Action, at resource.Scope) (bool, error)

// allowedAt answers through the request's decider. Without one (a handler
// on a plain mux, in unit tests) everything is allowed, as for lists.
func allowedAt(ctx context.Context, act policy.Action, at resource.Scope) (bool, error) {
	d, ok := ctx.Value(deciderKey{}).(decider)
	if !ok {
		return true, nil
	}
	return d(ctx, act, at)
}

func (a *authorizer) wrap(ru rule, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID := audit.RequestIDFrom(r.Context())
		caller, ok := callerFrom(r.Context())
		if !ok {
			// Every /v1 route sits behind authMiddleware; no caller here
			// means a wiring mistake, so refuse rather than guess.
			WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", reqID)
			return
		}
		ctx := r.Context()
		switch {
		case ru.authOnly:
			h(w, r.WithContext(ctx))
			return
		case ru.list:
			ctx = context.WithValue(ctx, readFilterKey{}, readFilter(a.readable(caller)))
			ctx = context.WithValue(ctx, approvalFilterKey{}, approvalFilter(func(ctx context.Context, ap resource.Approval) (bool, error) {
				return a.approvalReadableLoaded(ctx, caller, ap)
			}))
			ctx = context.WithValue(ctx, deciderKey{}, decider(func(ctx context.Context, act policy.Action, at resource.Scope) (bool, error) {
				d, err := a.engine.Decide(ctx, caller, act, at)
				return d.Effect == policy.Allow, err
			}))
			h(w, r.WithContext(ctx))
			return
		}

		checks, resourceID, err := a.checks(ctx, r, ru)
		var bad *badBody
		if errors.As(err, &bad) {
			// Refused here, never handed on: a body the check couldn't
			// read is a body whose owners were never checked.
			bad.write(w, r)
			return
		}
		var refused *refusal
		if errors.As(err, &refused) {
			a.refuse(w, r, ru, resourceID, refused.err)
			return
		}
		if err != nil {
			a.fail(w, r, err)
			return
		}
		decisions := make([]string, 0, len(checks))
		needsApproval := false
		for _, c := range checks {
			d, err := a.engine.Decide(ctx, caller, c.action, c.scope)
			if err != nil {
				a.fail(w, r, err)
				return
			}
			if d.Effect == policy.NeedsApproval && ru.allowNeedsApproval {
				needsApproval = true
			} else if d.Effect != policy.Allow {
				a.deny(w, r.WithContext(ctx), ru, c, resourceID, d)
				return
			}
			decisions = append(decisions, d.Describe(c.action, c.scope))
		}
		if needsApproval {
			ctx = context.WithValue(ctx, needsApprovalKey{}, true)
		}
		evidence := strings.Join(decisions, "; ")
		ctx = audit.WithPolicyDecision(ctx, evidence)
		if ru.target == targetGraph {
			ctx = context.WithValue(ctx, readFilterKey{}, readFilter(a.readable(caller)))
		}
		if r.Method == http.MethodDelete {
			// Every delete records an Approval (constitution 5, spec 019):
			// policy allowed it, so policy approves it, and the store
			// writes it in the delete's own transaction.
			in := policyApproval(ctx, caller, ru, resourceID, r.URL.Query().Get("cascade") == "true", evidence)
			ctx = store.WithApproval(ctx, in)
			w = &approvalHeaderWriter{ResponseWriter: w, id: in.Approval.Metadata.ID}
		}
		h(w, r.WithContext(ctx))
	}
}

// badBody is a create or update body that can't be read for its owners.
// The request is refused with the same answer decodeBody gives: 413 when
// too large, otherwise 400.
type badBody struct{ err error }

func (b *badBody) Error() string { return b.err.Error() }

func (b *badBody) write(w http.ResponseWriter, r *http.Request) {
	var tooBig *http.MaxBytesError
	if errors.As(b.err, &tooBig) {
		writeTooLarge(w, r)
		return
	}
	WriteProblem(w, http.StatusBadRequest, "malformed request body", b.err.Error(), audit.RequestIDFrom(r.Context()))
}

// errUnknownKind is a graph route naming a kind the API doesn't serve.
var errUnknownKind = errors.New("unknown resource kind")

// check is one action that must be allowed at one scope.
type check struct {
	action policy.Action
	scope  resource.Scope
}

// checks resolves what a request must be allowed to do, and where, plus
// the resource ID for a denied-mutation audit row (decision 3).
func (a *authorizer) checks(ctx context.Context, r *http.Request, ru rule) ([]check, string, error) {
	id := r.PathValue("id")
	switch ru.target {
	case targetRoot:
		return []check{{ru.action, resource.RootScope}}, "", nil
	case targetGraph:
		kind := resource.Kind(r.PathValue("kind"))
		if _, ok := resourcePaths[kind]; !ok {
			return nil, "", errUnknownKind
		}
		return []check{{action(kind, "read"), resource.Scope{Kind: kind, ID: id}}}, id, nil
	case targetAssignmentBody:
		doc, err := bodyDoc(r)
		if err != nil {
			return nil, "", err
		}
		return []check{{ru.action, doc.Spec.Scope}}, "", nil
	case targetRoleBody:
		doc, err := bodyDoc(r)
		if err != nil {
			return nil, "", err
		}
		return scopeChecks(ru.action, doc.Spec.AssignableScopes), "", nil
	case targetAssignment:
		// An assignment that doesn't exist is decided at "/": an Owner
		// learns it's missing (404), anyone else gets 403 either way.
		at := resource.RootScope
		all, err := a.engine.Assignments(ctx)
		if err != nil {
			return nil, "", err
		}
		for _, x := range all {
			if x.Metadata.ID == id {
				at = x.Spec.Scope
			}
		}
		return []check{{ru.action, at}}, id, nil
	case targetRole:
		role, ok, err := a.engine.Role(ctx, id)
		if err != nil {
			return nil, "", err
		}
		if !ok || policy.IsBuiltIn(id) {
			return []check{{ru.action, resource.RootScope}}, id, nil
		}
		return scopeChecks(ru.action, role.AssignableScopes), id, nil
	case targetParent:
		owners, err := bodyOwners(r)
		if err != nil {
			return nil, "", err
		}
		if len(owners) == 0 {
			return []check{{ru.action, resource.RootScope}}, "", nil
		}
		// An owner of a kind the API doesn't serve is still checked, with
		// the created kind's action at that scope: only an assignment on
		// it, or above it, can allow that.
		out := make([]check, 0, len(owners))
		for _, o := range owners {
			out = append(out, check{ru.action, o})
		}
		return out, "", nil
	case targetApprovalBody:
		act, err := approvalBodyAction(r)
		if err != nil {
			return nil, "", err
		}
		out, err := a.deleteChecks(ctx, act.Target, act.Cascade)
		return out, act.Target.ID, err
	case targetApprovalDecide:
		ap, err := a.approvals.Get(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return []check{{ru.action, resource.RootScope}}, id, nil
		}
		if err != nil {
			return nil, "", err
		}
		// Who may decide comes before what they may do: an agent or the
		// requester is refused as such, not as a missing permission.
		caller, _ := callerFrom(ctx)
		if err := policy.MayDecide(caller, ap.Status.RequestedBy.Principal); err != nil {
			return nil, id, &refusal{err}
		}
		out, err := a.deleteChecks(ctx, ap.Spec.Action.Target, ap.Spec.Action.Cascade)
		if err != nil {
			return nil, "", err
		}
		// What the requester was shown, too, wherever it still exists: an
		// approver must be allowed everything the approval would remove,
		// as reviewed and as it is now. (A changed tree fails execution as
		// stale anyway.)
		for _, ref := range ap.Status.BlastRadius {
			c := check{deleteAction(ref.Kind), ref.Scope()}
			if slices.Contains(out, c) {
				continue
			}
			ok, err := a.exists(ctx, ref.Kind, ref.ID)
			if err != nil {
				return nil, "", err
			}
			if ok {
				out = append(out, c)
			}
		}
		return out, id, nil
	case targetApprovalRead:
		ap, err := a.approvals.Get(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return []check{{ru.action, resource.RootScope}}, id, nil
		}
		if err != nil {
			return nil, "", err
		}
		at, err := a.approvalScope(ctx, ap)
		if err != nil {
			return nil, "", err
		}
		return []check{{action(ap.Spec.Action.Target.Kind, "read"), at}}, id, nil
	}
	self := resource.Scope{Kind: ru.kind, ID: id}
	ref := graph.ResourceRef{Kind: self.Kind, ID: self.ID}
	if r.Method == http.MethodDelete {
		out, err := a.deleteChecks(ctx, self, ru.target == targetResource && r.URL.Query().Get("cascade") == "true")
		if err != nil {
			return nil, "", err
		}
		// The rule's own action, for access management's own delete verbs.
		out[0].action = ru.action
		return out, id, nil
	}
	out := []check{{ru.action, self}}
	if r.Method == http.MethodPut {
		moves, err := a.moveChecks(ctx, r, ru, ref)
		if err != nil {
			return nil, "", err
		}
		out = append(out, moves...)
	}
	return out, id, nil
}

// deleteChecks is what deleting target needs: delete on it, and with
// cascade, delete on everything it owns, each by its own kind. A DELETE, an
// approval request and an approval decision all check exactly this.
func (a *authorizer) deleteChecks(ctx context.Context, target resource.Scope, cascade bool) ([]check, error) {
	out := []check{{deleteAction(target.Kind), target}}
	if !cascade {
		return out, nil
	}
	below, err := a.tree.Descendants(ctx, graph.ResourceRef{Kind: target.Kind, ID: target.ID})
	if err != nil {
		return nil, err
	}
	for _, d := range below {
		out = append(out, check{deleteAction(d.Kind), resource.Scope{Kind: d.Kind, ID: d.ID}})
	}
	return out, nil
}

// approvalScope is where reading ap is decided: its target, or once that
// is gone, the nearest owner recorded at request time that still exists,
// or "/" (spec 019 plan.md decision 9).
func (a *authorizer) approvalScope(ctx context.Context, ap resource.Approval) (resource.Scope, error) {
	for _, s := range append([]resource.Scope{ap.Spec.Action.Target}, ap.Status.OwnerChain...) {
		ok, err := a.exists(ctx, s.Kind, s.ID)
		if err != nil {
			return resource.Scope{}, err
		}
		if ok {
			return s, nil
		}
	}
	return resource.RootScope, nil
}

// approvalBodyAction reads an approval request's action, the same way the
// handler decodes it. Only the kinds the API serves may be requested;
// access management is deleted with DELETE.
func approvalBodyAction(r *http.Request) (resource.ApprovalAction, error) {
	body, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return resource.ApprovalAction{}, &badBody{err}
	}
	var spec resource.ApprovalSpec
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&spec); err != nil {
		return resource.ApprovalAction{}, &badBody{err}
	}
	if err := resource.ValidateApprovalSpec(spec); err != nil {
		return resource.ApprovalAction{}, &badBody{err}
	}
	if _, ok := resourcePaths[spec.Action.Target.Kind]; !ok {
		return resource.ApprovalAction{}, &badBody{fmt.Errorf("%w: action.target.kind: %q can't be requested; only the kinds the API serves can",
			resource.ErrValidation, spec.Action.Target.Kind)}
	}
	return spec.Action, nil
}

// needsApprovalKey marks a request let through on a NeedsApproval decision
// (rule.allowNeedsApproval).
type needsApprovalKey struct{}

// needsApprovalFrom reports whether some check on this request answered
// NeedsApproval rather than Allow.
func needsApprovalFrom(ctx context.Context) bool {
	v, _ := ctx.Value(needsApprovalKey{}).(bool)
	return v
}

// refusal is a request refused before any permission check, by a rule
// roles can't express (policy.MayDecide).
type refusal struct{ err error }

func (r *refusal) Error() string { return r.err.Error() }

// refuse answers a refusal with 403 and audits it.
func (a *authorizer) refuse(w http.ResponseWriter, r *http.Request, ru rule, resourceID string, err error) {
	reqID := audit.RequestIDFrom(r.Context())
	if aerr := a.db(r.Context(), ru.kind, resourceID, "deny: "+err.Error()); aerr != nil {
		log.Printf("api: audit a refused %s %s (request %s): %v", r.Method, r.URL.Path, reqID, aerr)
	}
	WriteProblem(w, http.StatusForbidden, "forbidden", strings.TrimPrefix(err.Error(), policy.ErrForbidden.Error()+": "), reqID)
}

// scopeChecks is act at each scope, or at "/" when there are none.
func scopeChecks(act policy.Action, scopes []resource.Scope) []check {
	if len(scopes) == 0 {
		return []check{{act, resource.RootScope}}
	}
	out := make([]check, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, check{act, s})
	}
	return out
}

// deleteAction is delete for a kind, or for a kind the API doesn't serve,
// an action only a "*" role (Owner) holds.
func deleteAction(kind resource.Kind) policy.Action {
	if _, ok := resourcePaths[kind]; ok {
		return action(kind, "delete")
	}
	return policy.Action{Name: "serendip/" + string(kind) + "/delete"}
}

// moveChecks is what a PUT changing a resource's owners also needs: write
// at every owner it gains and every owner it loses, so it can't be moved
// into or out of a scope the caller doesn't control; and write at "/"
// when it loses all its owners.
func (a *authorizer) moveChecks(ctx context.Context, r *http.Request, ru rule, ref graph.ResourceRef) ([]check, error) {
	next, err := bodyOwners(r)
	if err != nil {
		return nil, err
	}
	cur, err := a.tree.Owners(ctx, ref)
	if err != nil {
		return nil, err
	}
	now := make([]resource.Scope, 0, len(cur))
	for _, o := range cur {
		now = append(now, resource.Scope{Kind: o.Kind, ID: o.ID})
	}
	var out []check
	for _, o := range next {
		if !slices.Contains(now, o) {
			out = append(out, check{ru.action, o})
		}
	}
	for _, o := range now {
		if !slices.Contains(next, o) {
			out = append(out, check{ru.action, o})
		}
	}
	if len(next) == 0 && len(now) > 0 {
		out = append(out, check{ru.action, resource.RootScope})
	}
	return out, nil
}

// bodyOwners returns the request body's metadata.ownerRefs as scopes (see
// bodyDoc).
func bodyOwners(r *http.Request) ([]resource.Scope, error) {
	doc, err := bodyDoc(r)
	if err != nil {
		return nil, err
	}
	out := make([]resource.Scope, 0, len(doc.Metadata.OwnerRefs))
	for _, o := range doc.Metadata.OwnerRefs {
		out = append(out, resource.Scope{Kind: o.Kind, ID: o.ID})
	}
	return out, nil
}

// probe is the part of a request body authorization decides on.
type probe struct {
	Metadata struct {
		OwnerRefs []resource.OwnerRef `json:"ownerRefs"`
	} `json:"metadata"`
	Spec struct {
		Scope            resource.Scope   `json:"scope"`
		AssignableScopes []resource.Scope `json:"assignableScopes"`
	} `json:"spec"`
}

// bodyDoc reads the body's probe and puts the body back for the handler.
// It decodes exactly as decodeBody does (the first JSON value, through
// json.Decoder), so what's checked here is what the handler stores.
func bodyDoc(r *http.Request) (probe, error) {
	body, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return probe{}, &badBody{err}
	}
	var doc probe
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&doc); err != nil {
		return probe{}, &badBody{err}
	}
	return doc, nil
}

// readable returns the check list and graph results are filtered with.
func (a *authorizer) readable(caller policy.Principal) func(ctx context.Context, kind resource.Kind, id string) (bool, error) {
	return func(ctx context.Context, kind resource.Kind, id string) (bool, error) {
		if kind == resource.KindApproval {
			return a.approvalReadable(ctx, caller, id)
		}
		if _, ok := resourcePaths[kind]; !ok {
			return false, nil
		}
		d, err := a.engine.Decide(ctx, caller, action(kind, "read"), resource.Scope{Kind: kind, ID: id})
		return d.Effect == policy.Allow, err
	}
}

// approvalReadable reports whether caller may read the approval id: read
// on its target, or on the nearest owner that still exists.
func (a *authorizer) approvalReadable(ctx context.Context, caller policy.Principal, id string) (bool, error) {
	ap, err := a.approvals.Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return a.approvalReadableLoaded(ctx, caller, ap)
}

// approvalReadableLoaded is approvalReadable for an approval already read.
func (a *authorizer) approvalReadableLoaded(ctx context.Context, caller policy.Principal, ap resource.Approval) (bool, error) {
	at, err := a.approvalScope(ctx, ap)
	if err != nil {
		return false, err
	}
	d, err := a.engine.Decide(ctx, caller, action(ap.Spec.Action.Target.Kind, "read"), at)
	return d.Effect == policy.Allow, err
}

// approvalFilterKey carries the list filter for approvals, which decides on
// the approval itself rather than re-reading it by ID.
type approvalFilterKey struct{}

type approvalFilter func(ctx context.Context, ap resource.Approval) (bool, error)

// filterReadableApprovals keeps the approvals the caller may read. Without
// a filter (a handler on a plain mux, in unit tests) everything is kept, as
// for filterReadable.
func filterReadableApprovals(ctx context.Context, items []resource.Approval) ([]resource.Approval, error) {
	f, ok := ctx.Value(approvalFilterKey{}).(approvalFilter)
	if !ok {
		return items, nil
	}
	out := make([]resource.Approval, 0, len(items))
	for _, it := range items {
		ok, err := f(ctx, it)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, it)
		}
	}
	return out, nil
}

// deny answers 403 naming the missing action and scope (spec.md
// "Enforcement"); that's safe, since the caller named the resource. A
// refused mutation is audited; a refused read is only logged (decision 8).
func (a *authorizer) deny(w http.ResponseWriter, r *http.Request, ru rule, c check, resourceID string, d policy.Decision) {
	reqID := audit.RequestIDFrom(r.Context())
	act, target := c.action, c.scope
	desc := d.Describe(act, target)
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		log.Printf("api: %s %s refused for %s (request %s): %s", r.Method, r.URL.Path, audit.ActorFrom(r.Context()), reqID, desc)
	} else {
		kind := ru.kind
		if kind == "" {
			kind = target.Kind
		}
		if err := a.db(r.Context(), kind, resourceID, desc); err != nil {
			log.Printf("api: audit a refused %s %s (request %s): %v", r.Method, r.URL.Path, reqID, err)
		}
	}
	switch {
	case d.Effect == policy.NeedsApproval:
		writeProblemType(w, ProblemNeedsApproval, http.StatusForbidden, "approval required",
			fmt.Sprintf("%s at %s needs an approval (%s), so nothing was changed. Request one with POST /v1/approvals and "+
				`{"action": {"verb": "delete", "target": {"kind": %q, "id": %q}, "cascade": %t}, "reason": "..."}`,
				act.Name, target, d.Reason, approvalTargetKind(ru, target), resourceID, r.URL.Query().Get("cascade") == "true"), reqID)
	case d.Reason != "":
		WriteProblem(w, http.StatusForbidden, "forbidden", fmt.Sprintf("%s at %s: %s", act.Name, target, d.Reason), reqID)
	default:
		WriteProblem(w, http.StatusForbidden, "forbidden", fmt.Sprintf("missing %s at %s", act.Name, target), reqID)
	}
}

func (a *authorizer) fail(w http.ResponseWriter, r *http.Request, err error) {
	reqID := audit.RequestIDFrom(r.Context())
	if errors.Is(err, errUnknownKind) {
		WriteProblem(w, http.StatusNotFound, "resource not found", fmt.Sprintf("%s: %q", err, r.PathValue("kind")), reqID)
		return
	}
	log.Printf("api: authorization check failed for %s %s (request %s): %v", r.Method, r.URL.Path, reqID, err)
	WriteProblem(w, http.StatusInternalServerError, "authorization check failed", "the request couldn't be authorized; retry later", reqID)
}

// filterReadable keeps the list items the caller may read. Without a
// filter in the context (a handler mounted on a plain mux, in unit tests)
// it keeps everything.
func filterReadable[S, St any](ctx context.Context, items []resource.Resource[S, St]) ([]resource.Resource[S, St], error) {
	f, ok := ctx.Value(readFilterKey{}).(readFilter)
	if !ok {
		return items, nil
	}
	out := make([]resource.Resource[S, St], 0, len(items))
	for _, it := range items {
		ok, err := f(ctx, it.Metadata.Kind, it.Metadata.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, it)
		}
	}
	return out, nil
}

// filterReadableRefs is filterReadable for graph results.
func filterReadableRefs(ctx context.Context, refs []graph.ResourceRef) ([]graph.ResourceRef, error) {
	f, ok := ctx.Value(readFilterKey{}).(readFilter)
	if !ok {
		return refs, nil
	}
	out := make([]graph.ResourceRef, 0, len(refs))
	for _, ref := range refs {
		ok, err := f(ctx, ref.Kind, ref.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, ref)
		}
	}
	return out, nil
}

// recordDeniedIn returns the denied-mutation audit writer over db.
func recordDeniedIn(db *sql.DB) dbForAudit {
	return func(ctx context.Context, kind resource.Kind, resourceID, decision string) error {
		return sqlite.RecordDenied(ctx, db, kind, resourceID, decision)
	}
}
