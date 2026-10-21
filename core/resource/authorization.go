package resource

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Authorization kinds (specs/017-authorization), modelled on Azure RBAC: a
// RoleDefinition says what a role may do; a RoleAssignment grants a role to
// a principal at a scope. Built-in role definitions live in core/policy
// with stable IDs; only custom roles are stored as RoleDefinition resources.
const (
	KindRoleDefinition Kind = "RoleDefinition"
	KindRoleAssignment Kind = "RoleAssignment"
)

// Permission is one block of a role definition, Azure's shape: control-plane
// actions on resources, and data actions on what a resource holds (a
// Secret's value). NotActions and NotDataActions subtract from their
// sibling list; they never deny something another role grants.
type Permission struct {
	Actions        []string `json:"actions,omitempty"`
	NotActions     []string `json:"notActions,omitempty"`
	DataActions    []string `json:"dataActions,omitempty"`
	NotDataActions []string `json:"notDataActions,omitempty"`
}

// Scope is where an assignment applies, or where a role may be assigned:
// the platform root ("/", the zero value) or one resource, which covers
// everything it owns. It references the resource rather than a path, so
// renaming or re-parenting never orphans an assignment.
type Scope struct {
	Kind Kind   `json:"kind,omitempty"`
	ID   string `json:"id,omitempty"`
}

// RootScope is "/", the whole platform.
var RootScope = Scope{}

// IsRoot reports whether s is "/".
func (s Scope) IsRoot() bool { return s.Kind == "" && s.ID == "" }

// String renders "/" or "Kind/id".
func (s Scope) String() string {
	if s.IsRoot() {
		return "/"
	}
	return string(s.Kind) + "/" + s.ID
}

// RoleDefinitionSpec is a role: what it may do, and where it may be
// assigned.
type RoleDefinitionSpec struct {
	RoleName    string       `json:"roleName"`
	Description string       `json:"description,omitempty"`
	Permissions []Permission `json:"permissions"`
	// AssignableScopes limits where the role can be assigned. A custom
	// role must name at least one, and none may be "/" (Azure's rule:
	// changing a role definition must never widen where it applies).
	AssignableScopes []Scope `json:"assignableScopes,omitempty"`
}

type RoleDefinitionStatus struct {
	Status
}

type RoleDefinition = Resource[RoleDefinitionSpec, RoleDefinitionStatus]

// PrincipalType is who an assignment grants to.
type PrincipalType string

const (
	// PrincipalPerson is a person, by the identity provider's stable
	// subject (never the display name).
	PrincipalPerson PrincipalType = "person"
	// PrincipalGroup is a group, by the name the identity provider puts
	// in its group claim.
	PrincipalGroup PrincipalType = "group"
	// PrincipalAPIKey is a static API key, by its configured actor name.
	PrincipalAPIKey PrincipalType = "apiKey"
	// PrincipalAgent is an AI agent identity. Every agent has a human
	// sponsor, as Microsoft Entra Agent ID requires; spec 018 makes agents
	// authenticate.
	PrincipalAgent PrincipalType = "agent"
)

// PrincipalRef names a principal.
type PrincipalRef struct {
	Type PrincipalType `json:"type"`
	ID   string        `json:"id"`
	// Sponsor is the person accountable for an agent. Required for, and
	// only allowed on, an agent.
	Sponsor *PrincipalRef `json:"sponsor,omitempty"`
	// Via marks a person acting through an AI assistant (spec 021): the
	// client they acted through. Recorded on approvals, so the person can
	// decide what their assistant asked for. Never on a role assignment:
	// roles are the person's own.
	Via string `json:"via,omitempty"`
}

// Delegation constrains what the holder of this assignment may assign to
// others (Azure's RBAC Administrator with a condition, without the ABAC
// language): only these roles, only to these principal types.
type Delegation struct {
	RoleDefinitionIDs []string        `json:"roleDefinitionIds"`
	PrincipalTypes    []PrincipalType `json:"principalTypes"`
}

// RoleAssignmentSpec grants one role to one principal at one scope.
type RoleAssignmentSpec struct {
	Principal        PrincipalRef `json:"principal"`
	RoleDefinitionID string       `json:"roleDefinitionId"`
	Scope            Scope        `json:"scope"`
	// Description is the justification, recorded with the assignment.
	Description string      `json:"description,omitempty"`
	Delegation  *Delegation `json:"delegation,omitempty"`
}

type RoleAssignmentStatus struct {
	Status
}

type RoleAssignment = Resource[RoleAssignmentSpec, RoleAssignmentStatus]

// Limits on authorization data, so nothing unbounded reaches the store or
// the in-memory index.
const (
	maxActionLen      = 256
	maxActionsPerList = 256
	maxPermissions    = 32
	maxScopes         = 64
	maxRoleNameLen    = 128
	maxDescriptionLen = 1024
	maxPrincipalIDLen = 256
	maxDelegationList = 64
)

// actionSegmentRE is one path segment of an action: a name, or "*".
var actionSegmentRE = regexp.MustCompile(`^(\*|[A-Za-z0-9][A-Za-z0-9.-]*)$`)

// ValidateAction checks an action string: "serendip/{type}/{verb}"-shaped
// segments, Azure's wildcard rule (at most one "*"), or "*" alone. Actions
// are case-insensitive; the caller compares them lower-cased.
func ValidateAction(a string) error {
	if a == "" || len(a) > maxActionLen {
		return fmt.Errorf("%w: action %q: must be 1-%d characters", ErrValidation, a, maxActionLen)
	}
	if strings.Count(a, "*") > 1 {
		return fmt.Errorf("%w: action %q: at most one wildcard", ErrValidation, a)
	}
	segs := strings.Split(a, "/")
	if a != "*" && segs[0] != "*" && !strings.EqualFold(segs[0], "serendip") {
		return fmt.Errorf("%w: action %q: must start with serendip/ (or be a wildcard)", ErrValidation, a)
	}
	for _, s := range segs {
		if !actionSegmentRE.MatchString(s) {
			return fmt.Errorf("%w: action %q: invalid segment %q", ErrValidation, a, s)
		}
	}
	return nil
}

func validateActions(field string, list []string) error {
	if len(list) > maxActionsPerList {
		return fmt.Errorf("%w: %s: at most %d entries", ErrValidation, field, maxActionsPerList)
	}
	for _, a := range list {
		if err := ValidateAction(a); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	return nil
}

func validateScope(s Scope) error {
	if s.IsRoot() {
		return nil
	}
	if s.Kind == "" || s.ID == "" {
		return fmt.Errorf("%w: scope: kind and id must both be set, or neither (for \"/\")", ErrValidation)
	}
	if strings.ContainsAny(s.ID, "*/") {
		return fmt.Errorf("%w: scope %s: no wildcards or paths in a scope id", ErrValidation, s)
	}
	return nil
}

func validText(s string, max int) bool {
	if len(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

// ValidateRoleDefinition checks a role definition. custom is false only for
// the built-in roles core/policy ships, which may be assignable at "/".
func ValidateRoleDefinition(spec RoleDefinitionSpec, custom bool) error {
	if name := strings.TrimSpace(spec.RoleName); name == "" || !validText(name, maxRoleNameLen) {
		return fmt.Errorf("%w: roleName: required, at most %d characters, no control characters", ErrValidation, maxRoleNameLen)
	}
	if !validText(spec.Description, maxDescriptionLen) {
		return fmt.Errorf("%w: description: at most %d characters, no control characters", ErrValidation, maxDescriptionLen)
	}
	if len(spec.Permissions) == 0 || len(spec.Permissions) > maxPermissions {
		return fmt.Errorf("%w: permissions: 1-%d entries required", ErrValidation, maxPermissions)
	}
	for i, p := range spec.Permissions {
		if len(p.Actions)+len(p.DataActions) == 0 {
			return fmt.Errorf("%w: permissions[%d]: grants nothing (no actions or dataActions)", ErrValidation, i)
		}
		for _, f := range []struct {
			name string
			list []string
		}{{"actions", p.Actions}, {"notActions", p.NotActions}, {"dataActions", p.DataActions}, {"notDataActions", p.NotDataActions}} {
			if err := validateActions(fmt.Sprintf("permissions[%d].%s", i, f.name), f.list); err != nil {
				return err
			}
		}
	}
	if len(spec.AssignableScopes) > maxScopes {
		return fmt.Errorf("%w: assignableScopes: at most %d", ErrValidation, maxScopes)
	}
	if custom && len(spec.AssignableScopes) == 0 {
		return fmt.Errorf("%w: assignableScopes: a custom role must name at least one scope", ErrValidation)
	}
	for _, s := range spec.AssignableScopes {
		if custom && s.IsRoot() {
			return fmt.Errorf("%w: assignableScopes: a custom role can't be assignable at \"/\"", ErrValidation)
		}
		if err := validateScope(s); err != nil {
			return err
		}
	}
	return nil
}

// ValidPrincipalType reports whether t is a known principal type.
func ValidPrincipalType(t PrincipalType) bool {
	switch t {
	case PrincipalPerson, PrincipalGroup, PrincipalAPIKey, PrincipalAgent:
		return true
	}
	return false
}

func validatePrincipal(p PrincipalRef) error {
	if !ValidPrincipalType(p.Type) {
		return fmt.Errorf("%w: principal.type %q: must be person, group, apiKey or agent", ErrValidation, p.Type)
	}
	if strings.TrimSpace(p.ID) == "" || !validText(p.ID, maxPrincipalIDLen) || strings.ContainsAny(p.ID, "\n\t") {
		return fmt.Errorf("%w: principal.id: required, at most %d characters, no control characters", ErrValidation, maxPrincipalIDLen)
	}
	switch {
	case p.Via != "":
		return fmt.Errorf("%w: principal.via: roles are assigned to the person, never to their assistant", ErrValidation)
	case p.Type == PrincipalAgent && p.Sponsor == nil:
		return fmt.Errorf("%w: principal: an agent needs a sponsor (the person accountable for it)", ErrValidation)
	case p.Type != PrincipalAgent && p.Sponsor != nil:
		return fmt.Errorf("%w: principal.sponsor: only an agent has a sponsor", ErrValidation)
	case p.Sponsor != nil && (p.Sponsor.Type != PrincipalPerson || p.Sponsor.Sponsor != nil):
		return fmt.Errorf("%w: principal.sponsor: must be a person", ErrValidation)
	case p.Sponsor != nil:
		if strings.TrimSpace(p.Sponsor.ID) == "" || !validText(p.Sponsor.ID, maxPrincipalIDLen) || strings.ContainsAny(p.Sponsor.ID, "\n\t") {
			return fmt.Errorf("%w: principal.sponsor.id: required", ErrValidation)
		}
	}
	return nil
}

// ValidateRoleAssignment checks a role assignment's own fields. Whether the
// role exists, may be assigned at that scope, and may be assigned by the
// caller are policy questions, answered in core/policy.
func ValidateRoleAssignment(spec RoleAssignmentSpec) error {
	if err := validatePrincipal(spec.Principal); err != nil {
		return err
	}
	if strings.TrimSpace(spec.RoleDefinitionID) == "" {
		return fmt.Errorf("%w: roleDefinitionId: required", ErrValidation)
	}
	if err := validateScope(spec.Scope); err != nil {
		return err
	}
	if !validText(spec.Description, maxDescriptionLen) {
		return fmt.Errorf("%w: description: at most %d characters, no control characters", ErrValidation, maxDescriptionLen)
	}
	if d := spec.Delegation; d != nil {
		if len(d.RoleDefinitionIDs) == 0 || len(d.RoleDefinitionIDs) > maxDelegationList {
			return fmt.Errorf("%w: delegation.roleDefinitionIds: 1-%d entries", ErrValidation, maxDelegationList)
		}
		if len(d.PrincipalTypes) == 0 || len(d.PrincipalTypes) > maxDelegationList {
			return fmt.Errorf("%w: delegation.principalTypes: 1-%d entries", ErrValidation, maxDelegationList)
		}
		for _, t := range d.PrincipalTypes {
			if !ValidPrincipalType(t) {
				return fmt.Errorf("%w: delegation.principalTypes: %q is not a principal type", ErrValidation, t)
			}
		}
	}
	return nil
}
