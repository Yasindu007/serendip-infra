package policy

import (
	"maps"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// Built-in role IDs. They never change, so scripts and stored assignments
// can reference them across installs (Azure gives its built-in roles the
// same GUID everywhere for the same reason).
const (
	OwnerRoleID                   = "0190d1c0-0000-7000-8000-000000000001"
	ContributorRoleID             = "0190d1c0-0000-7000-8000-000000000002"
	ReaderRoleID                  = "0190d1c0-0000-7000-8000-000000000003"
	UserAccessAdministratorRoleID = "0190d1c0-0000-7000-8000-000000000004"
	RBACAdministratorRoleID       = "0190d1c0-0000-7000-8000-000000000005"
	SecretsOfficerRoleID          = "0190d1c0-0000-7000-8000-000000000006"
	SecretsUserRoleID             = "0190d1c0-0000-7000-8000-000000000007"
)

// Actions the policy itself relies on.
const (
	ActionRoleAssignmentsWrite  = "serendip/authorization/roleAssignments/write"
	ActionRoleAssignmentsDelete = "serendip/authorization/roleAssignments/delete"
)

// builtInRoles mirror Azure's built-in roles of the same names and meaning
// (spec.md "Built-in roles").
var builtInRoles = map[string]resource.RoleDefinitionSpec{
	OwnerRoleID: {
		RoleName:    "Owner",
		Description: "Full access to resources at the scope, including assigning roles. No data actions: reading a Secret's value needs a Secrets role.",
		Permissions: []resource.Permission{{Actions: []string{"*"}}},
	},
	ContributorRoleID: {
		RoleName:    "Contributor",
		Description: "Create, change and delete resources at the scope. Can't assign roles or read Secret values.",
		Permissions: []resource.Permission{{
			Actions:    []string{"*"},
			NotActions: []string{"serendip/authorization/*/write", "serendip/authorization/*/delete"},
		}},
	},
	ReaderRoleID: {
		RoleName:    "Reader",
		Description: "Read resources at the scope, including Secret metadata. No changes, no Secret values.",
		Permissions: []resource.Permission{{Actions: []string{"*/read"}}},
	},
	UserAccessAdministratorRoleID: {
		RoleName:    "User Access Administrator",
		Description: "Read everything and manage access (role assignments and custom roles) at the scope. Can't change resources.",
		Permissions: []resource.Permission{{Actions: []string{"*/read", "serendip/authorization/*"}}},
	},
	RBACAdministratorRoleID: {
		RoleName:    "Role Based Access Control Administrator",
		Description: "Create and remove role assignments at the scope, limited by the delegation on its own assignment.",
		Permissions: []resource.Permission{{Actions: []string{"*/read", ActionRoleAssignmentsWrite, ActionRoleAssignmentsDelete}}},
	},
	SecretsOfficerRoleID: {
		RoleName:    "Secrets Officer",
		Description: "Everything on Secrets at the scope, including reading and writing their values.",
		Permissions: []resource.Permission{{
			Actions:     []string{"serendip/secrets/*"},
			DataActions: []string{"serendip/secrets/value/*"},
		}},
	},
	SecretsUserRoleID: {
		RoleName:    "Secrets User",
		Description: "Read Secrets and their values at the scope. Can't change or delete them.",
		Permissions: []resource.Permission{{
			Actions:     []string{"serendip/secrets/read"},
			DataActions: []string{"serendip/secrets/value/read"},
		}},
	},
}

// BuiltInRoles returns the built-in role definitions by ID. The map is a
// copy; callers may not change the built-ins.
func BuiltInRoles() map[string]resource.RoleDefinitionSpec {
	return maps.Clone(builtInRoles)
}

// IsBuiltIn reports whether id names a built-in role.
func IsBuiltIn(id string) bool {
	_, ok := builtInRoles[id]
	return ok
}
