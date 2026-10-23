package catalog

// kind is a resource kind the API serves, with its collection path.
type kind struct {
	Name, Path string
	// AI marks the kinds whose writes are MCP tools in spec 021 (spec
	// scope items 1 and 2). The rest are read through the generic
	// resources tools.
	AI bool
}

// kinds mirrors api's resourcePaths; the parity test checks they agree.
var kinds = []kind{
	{"Organization", "organizations", false},
	{"Team", "teams", false},
	{"Project", "projects", false},
	{"Environment", "environments", false},
	{"Site", "sites", false},
	{"Cluster", "clusters", false},
	{"Node", "nodes", false},
	{"Application", "applications", true},
	{"Component", "components", true},
	{"Capability", "capabilities", false},
	{"Provider", "providers", false},
	{"ProviderInstance", "provider-instances", false},
	{"Incident", "incidents", false},
	{"Alert", "alerts", false},
	{"Volume", "volumes", false},
	{"Secret", "secrets", true},
	{"Deployment", "deployments", true},
}

// KindPaths maps each kind's name to its collection path, for the parity
// test.
func KindPaths() map[string]string {
	out := make(map[string]string, len(kinds))
	for _, k := range kinds {
		out[k.Name] = k.Path
	}
	return out
}

func kindNames() []string {
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = k.Name
	}
	return out
}

func otherPaths() []string {
	var out []string
	for _, k := range kinds {
		if !k.AI {
			out = append(out, k.Path)
		}
	}
	return out
}

func id(what string) Param {
	return Param{Name: "id", In: InPath, Required: true, Description: what + " ID"}
}

var (
	ownerParam   = Param{Name: "ownerId", In: InQuery, Description: "Only resources owned by this resource ID"}
	labelsParam  = Param{Name: "labels", In: InLabels, Type: Object, Description: "Only resources with all of these labels (name to value)"}
	cascadeParam = Param{Name: "cascade", In: InQuery, Type: Boolean,
		Description: "Also delete everything the resource owns. Answered with the blast radius, decided like the delete itself"}
	resourceBody = Param{Name: "resource", In: InBody, Type: Object, Required: true,
		Description: "The resource document: metadata (name, ownerId, labels) and spec. On update, include the metadata.version you read: a stale version is refused as a conflict"}
	principalTypes = []string{"person", "group", "agent", "apiKey"}
	scopeParam     = Param{Name: "scope", In: InQuery,
		Description: `A scope: "/" or "Kind/id", such as "Project/0190…"`}
)

// crud is the five resource operations for one kind, on the CLI, and as
// MCP tools when the kind is one an AI writes in this spec.
func crud(k kind) []Operation {
	what := k.Name
	return []Operation{
		{Name: underscore(k.Path) + "_list", CLI: []string{k.Path, "list"}, MCP: k.AI,
			Summary: "List " + k.Path + " the caller may read", Method: "GET", Path: "/v1/" + k.Path,
			Params: []Param{ownerParam, labelsParam}, Hints: Hints{ReadOnly: true}},
		{Name: underscore(k.Path) + "_get", CLI: []string{k.Path, "get"}, MCP: k.AI,
			Summary: "Get one " + what, Method: "GET", Path: "/v1/" + k.Path + "/{id}",
			Params: []Param{id(what)}, Hints: Hints{ReadOnly: true}},
		{Name: underscore(k.Path) + "_create", CLI: []string{k.Path, "create"}, MCP: k.AI,
			Summary: "Create a " + what, Method: "POST", Path: "/v1/" + k.Path,
			Params: []Param{resourceBody}},
		{Name: underscore(k.Path) + "_update", CLI: []string{k.Path, "update"}, MCP: k.AI,
			Summary: "Replace a " + what + "'s spec (send the version read, for a conflict check)", Method: "PUT", Path: "/v1/" + k.Path + "/{id}",
			Params: []Param{id(what), resourceBody}, Hints: Hints{Idempotent: true}},
		{Name: underscore(k.Path) + "_delete", CLI: []string{k.Path, "delete"}, MCP: k.AI,
			Summary: "Delete a " + what + ". Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request",
			Method:  "DELETE", Path: "/v1/" + k.Path + "/{id}",
			Params: []Param{id(what), cascadeParam}, Hints: Hints{Destructive: true, Idempotent: true}},
	}
}

var operations = func() []Operation {
	var ops []Operation
	for _, k := range kinds {
		ops = append(ops, crud(k)...)
	}
	others := otherPaths()
	ops = append(ops,
		// The other kinds, for an AI: read-only, one tool each for list and
		// get, so the tool list stays short.
		Operation{Name: "resources_list", MCP: true,
			Summary: "List resources of one kind (infrastructure, organisation, operations)", Method: "GET", Path: "/v1/{kind}",
			Params: []Param{{Name: "kind", In: InPath, Required: true, Enum: others, Expand: true, Description: "Collection"}, ownerParam, labelsParam},
			Hints:  Hints{ReadOnly: true}},
		Operation{Name: "resources_get", MCP: true,
			Summary: "Get one resource of one kind", Method: "GET", Path: "/v1/{kind}/{id}",
			Params: []Param{{Name: "kind", In: InPath, Required: true, Enum: others, Expand: true, Description: "Collection"}, id("Resource")},
			Hints:  Hints{ReadOnly: true}},

		Operation{Name: "session_get", CLI: []string{"whoami"}, MCP: true,
			Summary: "Who the caller is, and how they signed in", Method: "GET", Path: "/v1/session", Hints: Hints{ReadOnly: true}},
		Operation{Name: "readiness_get", CLI: []string{"readiness"}, MCP: true,
			Summary: "Readiness of each capability", Method: "GET", Path: "/v1/readiness", Hints: Hints{ReadOnly: true}},
		Operation{Name: "capability_default_get", CLI: []string{"capabilities", "default", "get"}, MCP: true,
			Summary: "A capability's default provider", Method: "GET", Path: "/v1/capabilities/{name}/default",
			Params: []Param{{Name: "name", In: InPath, Required: true, Description: "Capability name"}}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "capability_default_set", CLI: []string{"capabilities", "default", "set"},
			Summary: "Set a capability's default provider", Method: "PUT", Path: "/v1/capabilities/{name}/default",
			Params: []Param{{Name: "name", In: InPath, Required: true, Description: "Capability name"},
				{Name: "provider", In: InField, Required: true, Description: "Provider name"}},
			Hints: Hints{Idempotent: true}},
	)

	graphKind := Param{Name: "kind", In: InPath, Required: true, Enum: kindNames(), Description: "Resource kind, such as Application"}
	for _, g := range []struct{ name, summary string }{
		{"children", "What a resource directly owns"},
		{"dependents", "What depends on a resource"},
		{"blast-radius", "Everything deleting a resource would affect"},
	} {
		ops = append(ops, Operation{Name: "graph_" + underscore(g.name), CLI: []string{"graph", g.name}, MCP: true,
			Summary: g.summary, Method: "GET", Path: "/v1/graph/{kind}/{id}/" + g.name,
			Params: []Param{graphKind, id("Resource")}, Hints: Hints{ReadOnly: true}})
	}

	roleID := Param{Name: "id", In: InPath, Required: true, Description: "Role definition ID or built-in role name"}
	ops = append(ops,
		Operation{Name: "access_roles_list", CLI: []string{"access", "roles", "list"}, MCP: true,
			Summary: "Role definitions, built-in and custom", Method: "GET", Path: "/v1/authorization/roleDefinitions", Hints: Hints{ReadOnly: true}},
		Operation{Name: "access_roles_get", CLI: []string{"access", "roles", "get"}, MCP: true,
			Summary: "One role definition", Method: "GET", Path: "/v1/authorization/roleDefinitions/{id}",
			Params: []Param{roleID}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "access_roles_create", CLI: []string{"access", "roles", "create"},
			Summary: "Create a custom role definition", Method: "POST", Path: "/v1/authorization/roleDefinitions",
			Params: []Param{{Name: "definition", In: InBody, Type: Object, Required: true,
				Description: "The role definition: metadata.name and spec (actions, dataActions, assignableScopes). The server chooses the ID"}}},
		Operation{Name: "access_roles_delete", CLI: []string{"access", "roles", "delete"},
			Summary: "Delete a custom role definition", Method: "DELETE", Path: "/v1/authorization/roleDefinitions/{id}",
			Params: []Param{roleID}, Hints: Hints{Destructive: true, Idempotent: true}},
		Operation{Name: "access_assignments_list", CLI: []string{"access", "assignments", "list"}, MCP: true,
			Summary: "Role assignments the caller may read, optionally those that apply at a scope", Method: "GET", Path: "/v1/authorization/roleAssignments",
			Params: []Param{scopeParam,
				{Name: "principalType", In: InQuery, Enum: principalTypes, Description: "Only assignments to this type of principal"},
				{Name: "principalId", In: InQuery, Description: "Only assignments to this principal"}},
			Hints: Hints{ReadOnly: true}},
		Operation{Name: "access_assignments_get", CLI: []string{"access", "assignments", "get"}, MCP: true,
			Summary: "One role assignment", Method: "GET", Path: "/v1/authorization/roleAssignments/{id}",
			Params: []Param{id("Role assignment")}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "access_assignments_create", CLI: []string{"access", "assignments", "create"},
			Summary: "Assign a role to a principal at a scope", Method: "POST", Path: "/v1/authorization/roleAssignments",
			Params: []Param{{Name: "assignment", In: InBody, Type: Object, Required: true,
				Description: "The assignment: spec.principal (type, id), spec.roleDefinitionId and spec.scope. The server chooses the ID"}}},
		Operation{Name: "access_assignments_delete", CLI: []string{"access", "assignments", "delete"},
			Summary: "Remove a role assignment", Method: "DELETE", Path: "/v1/authorization/roleAssignments/{id}",
			Params: []Param{id("Role assignment")}, Hints: Hints{Destructive: true, Idempotent: true}},
		Operation{Name: "access_permissions", CLI: []string{"access", "permissions"}, MCP: true,
			Summary: "What the caller may do at a scope (default \"/\")", Method: "GET", Path: "/v1/authorization/permissions",
			Params: []Param{scopeParam}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "access_principals_list", CLI: []string{"access", "principals", "list"},
			Summary: "Principals known to Serendip", Method: "GET", Path: "/v1/authorization/principals",
			Params: []Param{{Name: "type", In: InQuery, Enum: principalTypes, Description: "Principal type"}},
			Hints:  Hints{ReadOnly: true}},
		Operation{Name: "access_principals_groups", CLI: []string{"access", "principals", "groups"},
			Summary: "The groups a person belongs to", Method: "GET", Path: "/v1/authorization/principals/person/{id}/groups",
			Params: []Param{id("Person")}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "access_directory_search", CLI: []string{"access", "directory"},
			Summary: "Search the identity provider's directory", Method: "GET", Path: "/v1/authorization/directory",
			Params: []Param{{Name: "q", In: InQuery, Description: "Search text"},
				{Name: "type", In: InQuery, Required: true, Enum: []string{"person", "group"}, Description: "Principal type"}},
			Hints: Hints{ReadOnly: true}},
	)

	approvalID := id("Approval")
	comment := Param{Name: "comment", In: InField, Description: "Why, recorded with the decision"}
	ops = append(ops,
		Operation{Name: "approvals_list", CLI: []string{"approvals", "list"}, MCP: true,
			Summary: "Approvals the caller may read, newest first, a page at a time", Method: "GET", Path: "/v1/approvals",
			Params: []Param{
				{Name: "phase", In: InQuery, Enum: []string{"Pending", "Approved", "Rejected", "Expired", "Executed", "Failed"}, Description: "Only this phase"},
				{Name: "targetKind", In: InQuery, Enum: kindNames(), Description: "Only approvals for this kind"},
				{Name: "targetId", In: InQuery, Description: "Only approvals for this resource"},
				{Name: "requester", In: InQuery, Description: "Only approvals this principal requested"},
				{Name: "limit", In: InQuery, Type: Integer, Max: 500, Description: "Page size"},
				{Name: "after", In: InQuery, Description: "The next-page cursor from the previous page"},
			}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "approvals_get", CLI: []string{"approvals", "get"}, MCP: true,
			Summary: "One approval, with its decision and what it removed", Method: "GET", Path: "/v1/approvals/{id}",
			Params: []Param{approvalID}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "approvals_request", CLI: []string{"approvals", "request"}, MCP: true,
			Summary: "Ask for a delete that needs a person's approval. A person decides it; the requester never can",
			Method:  "POST", Path: "/v1/approvals",
			Params: []Param{
				{Name: "kind", In: InField, Required: true, Enum: kindNames(), Description: "Kind of the resource to delete"},
				{Name: "id", In: InField, Required: true, Description: "ID of the resource to delete"},
				{Name: "cascade", In: InField, Type: Boolean, Description: "Also delete everything it owns"},
				{Name: "reason", In: InField, Description: "Why; required when a person must decide"},
			},
			body: approvalRequestBody},
		Operation{Name: "approvals_approve", CLI: []string{"approvals", "approve"},
			Summary: "Approve a request; the approval reconciler then carries it out", Method: "POST", Path: "/v1/approvals/{id}/approve",
			Params: []Param{approvalID, comment}},
		Operation{Name: "approvals_reject", CLI: []string{"approvals", "reject"},
			Summary: "Reject a request", Method: "POST", Path: "/v1/approvals/{id}/reject",
			Params: []Param{approvalID, comment}},

		Operation{Name: "components_dependencies", CLI: []string{"components", "dependencies"}, MCP: true,
			Summary: "A Component's declared dependencies: the Components it calls, and those of its Application that call it",
			Method:  "GET", Path: "/v1/components/{id}/dependencies",
			Params: []Param{id("Component")}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "applications_topology", CLI: []string{"applications", "topology"}, MCP: true,
			Summary: "An Application's declared topology: its Components and the calls between them (not live traffic)",
			Method:  "GET", Path: "/v1/applications/{id}/topology",
			Params: []Param{id("Application")}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "packages_list", CLI: []string{"packages", "list"}, MCP: true,
			Summary: "The optional packages this server offers (builds, observability) and whether each is available. Install one by creating a ProviderInstance naming it",
			Method:  "GET", Path: "/v1/packages", Hints: Hints{ReadOnly: true}},
		Operation{Name: "platform_get", CLI: []string{"platform"}, MCP: true,
			Summary: "The application platform, read-only: where workloads run, and the component types and traits installed with what each allows",
			Method:  "GET", Path: "/v1/platform", Hints: Hints{ReadOnly: true}},
		Operation{Name: "components_schemas", CLI: []string{"components", "schemas"}, MCP: true,
			Summary: "What can be configured on a Component, as JSON Schemas: its per-environment configuration (a Deployment's overrides.config) and each trait's",
			Method:  "GET", Path: "/v1/components/{id}/schemas",
			Params: []Param{id("Component")}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "deployments_runtime", CLI: []string{"deployments", "runtime"}, MCP: true,
			Summary: "What a Deployment runs as on the platform: its objects with their health, then each running instance",
			Method:  "GET", Path: "/v1/deployments/{id}/runtime",
			Params: []Param{id("Deployment")}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "deployments_events", CLI: []string{"deployments", "events"}, MCP: true,
			Summary: "Recent platform events about what a Deployment runs as, newest first",
			Method:  "GET", Path: "/v1/deployments/{id}/events",
			Params: []Param{id("Deployment"), {Name: "since", In: InQuery, Description: "Only events after this RFC 3339 time"}},
			Hints:  Hints{ReadOnly: true}},
		Operation{Name: "deployments_logs", CLI: []string{"deployments", "logs"}, MCP: true,
			Summary: "Recent log lines from a Deployment's running instances, oldest first. Logs can hold sensitive data, so they need their own permission",
			Method:  "GET", Path: "/v1/deployments/{id}/logs",
			Params: []Param{id("Deployment"),
				{Name: "tailLines", In: InQuery, Type: Integer, Max: 1000, Description: "Recent lines to read from each instance (default 100)"},
				{Name: "sinceSeconds", In: InQuery, Type: Integer, Description: "Only lines from the last this many seconds"},
				{Name: "previous", In: InQuery, Type: Boolean, Description: "Read the previous instance of a restarted container"},
				{Name: "follow", In: InQuery, Type: Boolean, Description: "Keep printing new lines as they're written, until interrupted or the server ends the stream (idle 5 minutes, or 10 in all)",
					CLIOnly: "an MCP tool answers once; read recent lines instead"}},
			Hints: Hints{ReadOnly: true}},
		Operation{Name: "deployments_promote", CLI: []string{"deployments", "promote"}, MCP: true,
			Summary: "Run the release a Deployment runs now in the next Environment of the Project's pipeline. The target keeps its own overrides",
			Method:  "POST", Path: "/v1/deployments/{id}/promote",
			Params: []Param{id("Deployment"), {Name: "to", In: InField, Required: true, Description: "ID of the Environment to promote to"}},
			Hints:  Hints{Destructive: true, Idempotent: true}},

		Operation{Name: "secrets_read_value", CLI: []string{"secrets", "value", "get"}, MCP: true, SecretValue: true,
			Summary: "Read a Secret's value. Needs the secrets value data permission", Method: "GET", Path: "/v1/secrets/{id}/value",
			Params: []Param{id("Secret")}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "secrets_write_value", CLI: []string{"secrets", "value", "set"}, MCP: true, SecretValue: true,
			Summary: "Write a Secret's value, replacing the old one. Needs the secrets value data permission", Method: "POST", Path: "/v1/secrets/{id}/value",
			Params: []Param{id("Secret"), {Name: "data", In: InField, Type: Object, Required: true, Description: "Key/value pairs"}},
			Hints:  Hints{Destructive: true, Idempotent: true}},
	)
	return ops
}()

// approvalRequestBody nests the flat arguments into the API's ApprovalSpec.
// Request has checked their types and that kind and id are there.
func approvalRequestBody(args map[string]any) (any, error) {
	action := map[string]any{"verb": "delete", "target": map[string]any{"kind": args["kind"], "id": args["id"]}}
	if c, _ := args["cascade"].(bool); c {
		action["cascade"] = true
	}
	body := map[string]any{"action": action}
	if r, _ := args["reason"].(string); r != "" {
		body["reason"] = r
	}
	return body, nil
}

func underscore(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c == '-' {
			out[i] = '_'
		}
	}
	return string(out)
}

// excluded lists the routes left off a surface on purpose.
var excluded = func() map[string]Exclusion {
	const (
		agentsNoAccess = "agents never manage access (spec 018 guardrail); the API would refuse every call"
		agentsNoDecide = "only a person or an API key decides an approval (spec 019); an AI never does"
		directory      = "directory data, readable only by those who may assign roles, which agents never may"
		platform       = "a platform-wide setting, changed by people"
		catchAll       = "the catch-all route only ever answers 404"
		infraWrites    = "infrastructure and organisation writes are outside spec 021's MCP scope (applications, components, secrets); read through resources_list and resources_get"
	)
	e := map[string]Exclusion{
		"/":                                      {CLI: catchAll, MCP: catchAll},
		"POST /v1/authorization/roleDefinitions": {MCP: agentsNoAccess},
		"DELETE /v1/authorization/roleDefinitions/{id}":       {MCP: agentsNoAccess},
		"POST /v1/authorization/roleAssignments":              {MCP: agentsNoAccess},
		"DELETE /v1/authorization/roleAssignments/{id}":       {MCP: agentsNoAccess},
		"GET /v1/authorization/principals":                    {MCP: directory},
		"GET /v1/authorization/principals/person/{id}/groups": {MCP: directory},
		"GET /v1/authorization/directory":                     {MCP: directory},
		"POST /v1/approvals/{id}/approve":                     {MCP: agentsNoDecide},
		"POST /v1/approvals/{id}/reject":                      {MCP: agentsNoDecide},
		"PUT /v1/capabilities/{name}/default":                 {MCP: platform},
	}
	for _, k := range kinds {
		if k.AI {
			continue
		}
		for _, r := range []string{"POST /v1/" + k.Path, "PUT /v1/" + k.Path + "/{id}", "DELETE /v1/" + k.Path + "/{id}"} {
			e[r] = Exclusion{MCP: infraWrites}
		}
	}
	return e
}()
