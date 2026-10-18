package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

// AuthorizationHandler serves /v1/authorization (specs/017-authorization
// T006): role definitions, role assignments, the caller's effective
// permissions, and the principals that can be assigned. The authorizing
// mux has already decided the caller may act at the scopes involved; this
// handler adds Azure's other rules: constrained delegation, assignable
// scopes, the last Owner at "/", and no deleting a role still assigned.
type AuthorizationHandler struct {
	engine      *policy.Engine
	assignments store.Store[resource.RoleAssignmentSpec, resource.RoleAssignmentStatus]
	definitions store.Store[resource.RoleDefinitionSpec, resource.RoleDefinitionStatus]
	principals  *sqlite.PrincipalRegistry
	// directory, when set, answers who can be granted a role and a
	// person's current groups live; nil means sign-ins are the only source.
	directory    identity.Directory
	exists       func(ctx context.Context, kind resource.Kind, id string) (bool, error)
	recordDenied dbForAudit
}

func (h *AuthorizationHandler) Register(mux Mux) {
	mux.HandleFunc("GET /v1/authorization/roleDefinitions", h.listDefinitions)
	mux.HandleFunc("GET /v1/authorization/roleDefinitions/{id}", h.getDefinition)
	mux.HandleFunc("POST /v1/authorization/roleDefinitions", h.createDefinition)
	mux.HandleFunc("DELETE /v1/authorization/roleDefinitions/{id}", h.deleteDefinition)
	mux.HandleFunc("GET /v1/authorization/roleAssignments", h.listAssignments)
	mux.HandleFunc("GET /v1/authorization/roleAssignments/{id}", h.getAssignment)
	mux.HandleFunc("POST /v1/authorization/roleAssignments", h.createAssignment)
	mux.HandleFunc("DELETE /v1/authorization/roleAssignments/{id}", h.deleteAssignment)
	mux.HandleFunc("GET /v1/authorization/permissions", h.permissions)
	mux.HandleFunc("GET /v1/authorization/principals", h.listPrincipals)
	mux.HandleFunc("GET /v1/authorization/principals/person/{id}/groups", h.personGroups)
	mux.HandleFunc("GET /v1/authorization/directory", h.searchDirectory)
}

// roleDefinitionView is a role definition as the API shows it: built-in
// roles are marked, and can't be changed or deleted.
type roleDefinitionView struct {
	resource.RoleDefinition
	BuiltIn bool `json:"builtIn"`
}

func builtInView(id string, spec resource.RoleDefinitionSpec) roleDefinitionView {
	return roleDefinitionView{
		RoleDefinition: resource.RoleDefinition{Metadata: resource.Metadata{ID: id, Name: spec.RoleName, Kind: resource.KindRoleDefinition}, Spec: spec},
		BuiltIn:        true,
	}
}

func (h *AuthorizationHandler) listDefinitions(w http.ResponseWriter, r *http.Request) {
	out := []roleDefinitionView{}
	for id, spec := range policy.BuiltInRoles() {
		out = append(out, builtInView(id, spec))
	}
	slices.SortFunc(out, func(a, b roleDefinitionView) int { return strings.Compare(a.Spec.RoleName, b.Spec.RoleName) })
	custom, err := h.definitions.List(r.Context(), store.ListOptions{})
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	for _, d := range custom {
		out = append(out, roleDefinitionView{RoleDefinition: d})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *AuthorizationHandler) getDefinition(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if spec, ok := policy.BuiltInRoles()[id]; ok {
		writeJSON(w, http.StatusOK, builtInView(id, spec))
		return
	}
	d, err := h.definitions.Get(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, roleDefinitionView{RoleDefinition: d})
}

func (h *AuthorizationHandler) createDefinition(w http.ResponseWriter, r *http.Request) {
	var d resource.RoleDefinition
	if !decodeBody(w, r, &d) {
		return
	}
	if err := resource.ValidateRoleDefinition(d.Spec, true); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	// Under the lock, so nothing changes between these checks and the write.
	unlock := h.engine.LockAssignments()
	defer unlock()
	for _, s := range d.Spec.AssignableScopes {
		if !h.scopeExists(w, r, s) {
			return
		}
	}
	// Role names are unique, built-in ones included, compared without case.
	taken := func(name string) bool { return strings.EqualFold(name, d.Spec.RoleName) }
	for _, b := range policy.BuiltInRoles() {
		if taken(b.RoleName) {
			conflict(w, r, fmt.Sprintf("a built-in role is named %q", b.RoleName))
			return
		}
	}
	custom, err := h.definitions.List(r.Context(), store.ListOptions{})
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	for _, c := range custom {
		if taken(c.Spec.RoleName) {
			conflict(w, r, fmt.Sprintf("role %s is already named %q", c.Metadata.ID, c.Spec.RoleName))
			return
		}
	}
	if d.Metadata.Name == "" {
		d.Metadata.Name = d.Spec.RoleName
	}
	// The server names it: a client-chosen ID could shadow a built-in's.
	d.Metadata.ID, d.Metadata.OwnerRefs, d.Metadata.Relationships = "", nil, nil
	created, err := h.definitions.Create(r.Context(), d)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, roleDefinitionView{RoleDefinition: created})
}

func (h *AuthorizationHandler) deleteDefinition(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if policy.IsBuiltIn(id) {
		WriteProblem(w, http.StatusBadRequest, "validation failed", "built-in roles can't be deleted", audit.RequestIDFrom(r.Context()))
		return
	}
	unlock := h.engine.LockAssignments()
	defer unlock()
	inUse, err := h.engine.RoleInUse(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	if inUse {
		conflict(w, r, "the role is still assigned; remove its assignments first")
		return
	}
	if err := h.definitions.Delete(r.Context(), id, false); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// assignmentView marks an assignment listed at a scope but made at one of
// its ancestors (Azure's "inherited").
type assignmentView struct {
	resource.RoleAssignment
	Inherited bool `json:"inherited,omitempty"`
}

// listAssignments lists role assignments, filtered by ?principalType= and
// ?principalId=. With ?scope=, it lists those that apply at the scope:
// made there or inherited from above, which needs read at that scope.
// Without it, each assignment is shown only to callers who may read
// assignments at its own scope.
func (h *AuthorizationHandler) listAssignments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	read := authzAction("roleAssignments", "read")
	all, err := h.engine.Assignments(ctx)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	var chain []resource.Scope
	if q.Has("scope") {
		at, err := parseScope(q.Get("scope"))
		if err != nil {
			writeStoreErr(w, r, err)
			return
		}
		ok, err := allowedAt(ctx, read, at)
		if err != nil {
			writeStoreErr(w, r, err)
			return
		}
		if !ok {
			WriteProblem(w, http.StatusForbidden, "forbidden", fmt.Sprintf("missing %s at %s", read.Name, at), audit.RequestIDFrom(ctx))
			return
		}
		if chain, err = h.engine.ScopeChain(ctx, at); err != nil {
			writeStoreErr(w, r, err)
			return
		}
	}
	out := []assignmentView{}
	readable := map[resource.Scope]bool{} // one decision per distinct scope
	for _, a := range all {
		p := a.Spec.Principal
		if (q.Has("principalType") && string(p.Type) != q.Get("principalType")) || (q.Has("principalId") && p.ID != q.Get("principalId")) {
			continue
		}
		if chain != nil {
			i := slices.Index(chain, a.Spec.Scope)
			if i < 0 {
				continue
			}
			out = append(out, assignmentView{RoleAssignment: a, Inherited: i > 0})
			continue
		}
		ok, seen := readable[a.Spec.Scope]
		if !seen {
			var err error
			if ok, err = allowedAt(ctx, read, a.Spec.Scope); err != nil {
				writeStoreErr(w, r, err)
				return
			}
			readable[a.Spec.Scope] = ok
		}
		if ok {
			out = append(out, assignmentView{RoleAssignment: a})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *AuthorizationHandler) getAssignment(w http.ResponseWriter, r *http.Request) {
	a, err := h.assignments.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *AuthorizationHandler) createAssignment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	caller, ok := callerFrom(ctx)
	if !ok {
		WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", audit.RequestIDFrom(ctx))
		return
	}
	var a resource.RoleAssignment
	if !decodeBody(w, r, &a) {
		return
	}
	if err := resource.ValidateRoleAssignment(a.Spec); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	unlock := h.engine.LockAssignments()
	defer unlock()
	if !h.scopeExists(w, r, a.Spec.Scope) {
		return
	}
	all, err := h.engine.Assignments(ctx)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	for _, x := range all {
		if x.Spec.Principal.Type == a.Spec.Principal.Type && x.Spec.Principal.ID == a.Spec.Principal.ID &&
			x.Spec.RoleDefinitionID == a.Spec.RoleDefinitionID && x.Spec.Scope == a.Spec.Scope {
			conflict(w, r, fmt.Sprintf("assignment %s already grants this role to this principal at %s", x.Metadata.ID, a.Spec.Scope))
			return
		}
	}
	if err := h.engine.CheckAssign(ctx, caller, a.Spec); err != nil {
		h.refuse(w, r, "", err)
		return
	}
	if a.Metadata.Name == "" {
		a.Metadata.Name = string(a.Spec.Principal.Type) + "-" + a.Spec.Principal.ID
	}
	a.Metadata.ID, a.Metadata.OwnerRefs, a.Metadata.Relationships = "", nil, nil
	created, err := h.assignments.Create(ctx, a)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *AuthorizationHandler) deleteAssignment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	caller, ok := callerFrom(ctx)
	if !ok {
		WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", audit.RequestIDFrom(ctx))
		return
	}
	id := r.PathValue("id")
	unlock := h.engine.LockAssignments()
	defer unlock()
	if err := h.engine.CheckUnassign(ctx, caller, id); err != nil {
		h.refuse(w, r, id, err)
		return
	}
	if err := h.assignments.Delete(ctx, id, false); err != nil {
		writeStoreErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// permissionsView is the caller's effective permissions at a scope, Azure's
// permissions API: the permission blocks of every role that applies there.
type permissionsView struct {
	Scope       string                `json:"scope"`
	Permissions []resource.Permission `json:"permissions"`
}

func (h *AuthorizationHandler) permissions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	caller, ok := callerFrom(ctx)
	if !ok {
		WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", audit.RequestIDFrom(ctx))
		return
	}
	at := resource.RootScope
	if q := r.URL.Query(); q.Has("scope") {
		var err error
		if at, err = parseScope(q.Get("scope")); err != nil {
			writeStoreErr(w, r, err)
			return
		}
	}
	perms, err := h.engine.EffectivePermissions(ctx, caller, at)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	if perms == nil {
		perms = []resource.Permission{}
	}
	writeJSON(w, http.StatusOK, permissionsView{Scope: at.String(), Permissions: perms})
}

// listPrincipals lists the known principals, for picking whom to assign a
// role to. It's a directory of people and groups, so only callers who can
// assign roles somewhere may read it.
func (h *AuthorizationHandler) listPrincipals(w http.ResponseWriter, r *http.Request) {
	if !h.mayListPrincipals(w, r) {
		return
	}
	t := resource.PrincipalType(r.URL.Query().Get("type"))
	if t != "" && !resource.ValidPrincipalType(t) {
		writeStoreErr(w, r, fmt.Errorf("%w: unknown principal type %q", resource.ErrValidation, t))
		return
	}
	list, err := h.principals.List(r.Context(), t)
	if err != nil {
		writeStoreErr(w, r, err)
		return
	}
	if list == nil {
		list = []sqlite.KnownPrincipal{}
	}
	writeJSON(w, http.StatusOK, list)
}

// mayListPrincipals is the gate on directory data (people, groups, and
// who belongs to what): only callers who can assign roles somewhere may
// read it. It answers the refusal itself.
func (h *AuthorizationHandler) mayListPrincipals(w http.ResponseWriter, r *http.Request) bool {
	ctx := r.Context()
	caller, ok := callerFrom(ctx)
	if !ok {
		WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", audit.RequestIDFrom(ctx))
		return false
	}
	write := authzAction("roleAssignments", "write")
	can, err := h.engine.GrantedAnywhere(ctx, caller, write)
	if err != nil {
		writeStoreErr(w, r, err)
		return false
	}
	if !can {
		WriteProblem(w, http.StatusForbidden, "forbidden", "missing "+write.Name+" at any scope", audit.RequestIDFrom(ctx))
		return false
	}
	return true
}

// parseScope reads "/" or "Kind/id" as a scope of a kind the API serves.
func parseScope(s string) (resource.Scope, error) {
	if s == "/" {
		return resource.RootScope, nil
	}
	kind, id, ok := strings.Cut(s, "/")
	if !ok || id == "" || strings.Contains(id, "/") {
		return resource.Scope{}, fmt.Errorf("%w: scope %q: want \"/\" or \"Kind/id\"", resource.ErrValidation, s)
	}
	if _, served := resourcePaths[resource.Kind(kind)]; !served {
		return resource.Scope{}, fmt.Errorf("%w: scope %q: unknown kind %q", resource.ErrValidation, s, kind)
	}
	return resource.Scope{Kind: resource.Kind(kind), ID: id}, nil
}

// scopeExists answers 400 unless s is "/" or an existing resource of a kind
// the API serves, so nothing is granted on a typo.
func (h *AuthorizationHandler) scopeExists(w http.ResponseWriter, r *http.Request, s resource.Scope) bool {
	if s.IsRoot() {
		return true
	}
	if _, served := resourcePaths[s.Kind]; !served {
		writeStoreErr(w, r, fmt.Errorf("%w: scope %s: unknown kind %q", resource.ErrValidation, s, s.Kind))
		return false
	}
	ok, err := h.exists(r.Context(), s.Kind, s.ID)
	if err != nil {
		writeStoreErr(w, r, err)
		return false
	}
	if !ok {
		writeStoreErr(w, r, fmt.Errorf("%w: scope %s doesn't exist", resource.ErrValidation, s))
		return false
	}
	return true
}

// refuse answers a failed assignment check: 409 for the last Owner (the
// caller may, the state forbids it), 403 for a delegation or permission
// refusal, which is audited as a refused mutation, and the store mapping
// for the rest.
func (h *AuthorizationHandler) refuse(w http.ResponseWriter, r *http.Request, id string, err error) {
	ctx := r.Context()
	switch {
	case errors.Is(err, policy.ErrLastOwner):
		// A state refusal, not a missing permission, but an attempt to
		// remove the last Owner is worth a row all the same.
		if aerr := h.recordDenied(ctx, resource.KindRoleAssignment, id, "deny: "+err.Error()); aerr != nil {
			log.Printf("api: audit a refused role assignment change (request %s): %v", audit.RequestIDFrom(ctx), aerr)
		}
		conflict(w, r, err.Error())
	case errors.Is(err, policy.ErrForbidden):
		if aerr := h.recordDenied(ctx, resource.KindRoleAssignment, id, "deny: "+err.Error()); aerr != nil {
			log.Printf("api: audit a refused role assignment change (request %s): %v", audit.RequestIDFrom(ctx), aerr)
		}
		WriteProblem(w, http.StatusForbidden, "forbidden", strings.TrimPrefix(err.Error(), policy.ErrForbidden.Error()+": "), audit.RequestIDFrom(ctx))
	default:
		writeStoreErr(w, r, err)
	}
}

func conflict(w http.ResponseWriter, r *http.Request, detail string) {
	WriteProblem(w, http.StatusConflict, "conflict", detail, audit.RequestIDFrom(r.Context()))
}

// writeStoreErr maps an error to its problem response. A server-side
// failure is logged and answered generically: its text can carry store or
// policy internals.
func writeStoreErr(w http.ResponseWriter, r *http.Request, err error) {
	status, title := MapStoreError(err)
	reqID := audit.RequestIDFrom(r.Context())
	if status >= http.StatusInternalServerError {
		log.Printf("api: %s %s failed (request %s): %v", r.Method, r.URL.Path, reqID, err)
		WriteProblem(w, status, title, "the request couldn't be completed; retry later, quoting the request ID", reqID)
		return
	}
	WriteProblem(w, status, title, err.Error(), reqID)
}
