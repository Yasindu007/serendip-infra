package resource_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestValidateAction(t *testing.T) {
	cases := []struct {
		action string
		ok     bool
	}{
		{"*", true},
		{"*/read", true},
		{"serendip/*", true},
		{"serendip/components/delete", true},
		{"serendip/components/*", true},
		{"Serendip/Components/Read", true},
		{"serendip/secrets/value/read", true},
		{"", false},
		{"serendip/*/components/*", false},
		{"other/components/read", false},
		{"serendip//read", false},
		{"serendip/comp onents/read", false},
		{"serendip/components/read/", false},
		{strings.Repeat("a", 300), false},
	}
	for _, tc := range cases {
		err := resource.ValidateAction(tc.action)
		if (err == nil) != tc.ok {
			t.Errorf("ValidateAction(%q) = %v, want ok=%v", tc.action, err, tc.ok)
		}
		if err != nil && !errors.Is(err, resource.ErrValidation) {
			t.Errorf("ValidateAction(%q): error doesn't wrap ErrValidation", tc.action)
		}
	}
}

func TestValidateRoleDefinition(t *testing.T) {
	app := resource.Scope{Kind: resource.KindApplication, ID: "a1"}
	valid := resource.RoleDefinitionSpec{
		RoleName:         "Restarter",
		Permissions:      []resource.Permission{{Actions: []string{"serendip/components/read", "serendip/components/restart/action"}}},
		AssignableScopes: []resource.Scope{app},
	}
	mut := func(f func(*resource.RoleDefinitionSpec)) resource.RoleDefinitionSpec {
		s := valid
		s.Permissions = append([]resource.Permission(nil), valid.Permissions...)
		s.AssignableScopes = append([]resource.Scope(nil), valid.AssignableScopes...)
		f(&s)
		return s
	}
	cases := []struct {
		name   string
		spec   resource.RoleDefinitionSpec
		custom bool
		ok     bool
	}{
		{"valid custom role", valid, true, true},
		{"built-in may be assignable at /", mut(func(s *resource.RoleDefinitionSpec) { s.AssignableScopes = []resource.Scope{resource.RootScope} }), false, true},
		{"built-in may omit assignable scopes", mut(func(s *resource.RoleDefinitionSpec) { s.AssignableScopes = nil }), false, true},
		{"custom role needs assignable scopes", mut(func(s *resource.RoleDefinitionSpec) { s.AssignableScopes = nil }), true, false},
		{"custom role can't be assignable at /", mut(func(s *resource.RoleDefinitionSpec) { s.AssignableScopes = []resource.Scope{resource.RootScope} }), true, false},
		{"half-set scope", mut(func(s *resource.RoleDefinitionSpec) {
			s.AssignableScopes = []resource.Scope{{Kind: resource.KindApplication}}
		}), true, false},
		{"wildcard in scope id", mut(func(s *resource.RoleDefinitionSpec) {
			s.AssignableScopes = []resource.Scope{{Kind: resource.KindApplication, ID: "*"}}
		}), true, false},
		{"no name", mut(func(s *resource.RoleDefinitionSpec) { s.RoleName = " " }), true, false},
		{"control char in name", mut(func(s *resource.RoleDefinitionSpec) { s.RoleName = "a\x00b" }), true, false},
		{"no permissions", mut(func(s *resource.RoleDefinitionSpec) { s.Permissions = nil }), true, false},
		{"grants nothing", mut(func(s *resource.RoleDefinitionSpec) {
			s.Permissions = []resource.Permission{{NotActions: []string{"serendip/components/delete"}}}
		}), true, false},
		{"data actions only", mut(func(s *resource.RoleDefinitionSpec) {
			s.Permissions = []resource.Permission{{DataActions: []string{"serendip/secrets/value/read"}}}
		}), true, true},
		{"bad action", mut(func(s *resource.RoleDefinitionSpec) { s.Permissions[0].Actions = []string{"serendip/*/x/*"} }), true, false},
		{"bad notDataAction", mut(func(s *resource.RoleDefinitionSpec) {
			s.Permissions[0].NotDataActions = []string{"nope/x"}
		}), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := resource.ValidateRoleDefinition(tc.spec, tc.custom)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if err != nil && !errors.Is(err, resource.ErrValidation) {
				t.Error("error doesn't wrap ErrValidation")
			}
		})
	}
}

func TestValidateRoleAssignment(t *testing.T) {
	person := resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "01900000-0000-7000-8000-000000000030"}
	valid := resource.RoleAssignmentSpec{Principal: person, RoleDefinitionID: "role-1", Scope: resource.RootScope, Description: "bootstrap"}
	mut := func(f func(*resource.RoleAssignmentSpec)) resource.RoleAssignmentSpec { s := valid; f(&s); return s }
	sponsor := &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "sub-1"}
	cases := []struct {
		name string
		spec resource.RoleAssignmentSpec
		ok   bool
	}{
		{"valid at /", valid, true},
		{"valid at a resource", mut(func(s *resource.RoleAssignmentSpec) { s.Scope = resource.Scope{Kind: resource.KindProject, ID: "p1"} }), true},
		{"group", mut(func(s *resource.RoleAssignmentSpec) {
			s.Principal = resource.PrincipalRef{Type: resource.PrincipalGroup, ID: "platform"}
		}), true},
		{"api key", mut(func(s *resource.RoleAssignmentSpec) {
			s.Principal = resource.PrincipalRef{Type: resource.PrincipalAPIKey, ID: "ci"}
		}), true},
		{"agent with sponsor", mut(func(s *resource.RoleAssignmentSpec) {
			s.Principal = resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "agent-1", Sponsor: sponsor}
		}), true},
		{"agent without sponsor", mut(func(s *resource.RoleAssignmentSpec) {
			s.Principal = resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "agent-1"}
		}), false},
		{"agent sponsored by a group", mut(func(s *resource.RoleAssignmentSpec) {
			s.Principal = resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "agent-1", Sponsor: &resource.PrincipalRef{Type: resource.PrincipalGroup, ID: "g"}}
		}), false},
		{"newline in sponsor id", mut(func(s *resource.RoleAssignmentSpec) {
			s.Principal = resource.PrincipalRef{Type: resource.PrincipalAgent, ID: "agent-1", Sponsor: &resource.PrincipalRef{Type: resource.PrincipalPerson, ID: "a\nb"}}
		}), false},
		{"person with sponsor", mut(func(s *resource.RoleAssignmentSpec) { s.Principal.Sponsor = sponsor }), false},
		{"unknown principal type", mut(func(s *resource.RoleAssignmentSpec) { s.Principal.Type = "robot" }), false},
		{"empty principal id", mut(func(s *resource.RoleAssignmentSpec) { s.Principal.ID = " " }), false},
		{"newline in principal id", mut(func(s *resource.RoleAssignmentSpec) { s.Principal.ID = "a\nb" }), false},
		{"no role", mut(func(s *resource.RoleAssignmentSpec) { s.RoleDefinitionID = "" }), false},
		{"half-set scope", mut(func(s *resource.RoleAssignmentSpec) { s.Scope = resource.Scope{ID: "p1"} }), false},
		{"description too long", mut(func(s *resource.RoleAssignmentSpec) { s.Description = strings.Repeat("x", 2000) }), false},
		{"valid delegation", mut(func(s *resource.RoleAssignmentSpec) {
			s.Delegation = &resource.Delegation{RoleDefinitionIDs: []string{"reader"}, PrincipalTypes: []resource.PrincipalType{resource.PrincipalPerson}}
		}), true},
		{"delegation without roles", mut(func(s *resource.RoleAssignmentSpec) {
			s.Delegation = &resource.Delegation{PrincipalTypes: []resource.PrincipalType{resource.PrincipalPerson}}
		}), false},
		{"delegation with a bad principal type", mut(func(s *resource.RoleAssignmentSpec) {
			s.Delegation = &resource.Delegation{RoleDefinitionIDs: []string{"reader"}, PrincipalTypes: []resource.PrincipalType{"robot"}}
		}), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := resource.ValidateRoleAssignment(tc.spec)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if err != nil && !errors.Is(err, resource.ErrValidation) {
				t.Error("error doesn't wrap ErrValidation")
			}
		})
	}
}

func TestScopeString(t *testing.T) {
	if got := resource.RootScope.String(); got != "/" {
		t.Errorf("root = %q", got)
	}
	if got := (resource.Scope{Kind: resource.KindComponent, ID: "c1"}).String(); got != "Component/c1" {
		t.Errorf("component = %q", got)
	}
}
