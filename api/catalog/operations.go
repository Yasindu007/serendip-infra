package catalog

import "strings"

// kind is a resource kind the API serves, with its collection path.
type kind struct {
	Name, Path string
	// AI marks the kinds whose writes are MCP tools in spec 021 (spec
	// scope items 1 and 2). The rest are read through the generic
	// resources tools. Spec 024 adds AlertRule (plan decision 13).
	AI bool
	// CLI is the command group, when it isn't Path (spec 024: channels,
	// smtp).
	CLI string
}

// cli is k's command group.
func (k kind) cli() string {
	if k.CLI != "" {
		return k.CLI
	}
	return k.Path
}

// kinds mirrors api's resourcePaths; the parity test checks they agree.
var kinds = []kind{
	{"Organization", "organizations", false, ""},
	{"Team", "teams", false, ""},
	{"Project", "projects", false, ""},
	{"Environment", "environments", false, ""},
	{"Site", "sites", false, ""},
	{"Cluster", "clusters", false, ""},
	{"Node", "nodes", false, ""},
	{"Application", "applications", true, ""},
	{"Component", "components", true, ""},
	{"Capability", "capabilities", false, ""},
	{"Provider", "providers", false, ""},
	{"ProviderInstance", "provider-instances", false, ""},
	{"Incident", "incidents", false, ""},
	{"AlertRule", "alertrules", true, ""},
	{"NotificationChannel", "notificationchannels", false, "channels"},
	{"SMTPServer", "smtpservers", false, "smtp"},
	{"Volume", "volumes", false, ""},
	{"Secret", "secrets", true, ""},
	{"Deployment", "deployments", true, ""},
	{"Template", "templates", true, ""},
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
	versionParam = Param{Name: "version", In: InPath, Required: true, Description: "Version label, such as 1.0"}
	cascadeParam = Param{Name: "cascade", In: InQuery, Type: Boolean,
		Description: "Also delete everything the resource owns. Answered with the blast radius, decided like the delete itself"}
	resourceBody = Param{Name: "resource", In: InBody, Type: Object, Required: true,
		Description: "The resource document: metadata (name, ownerRefs as [{\"kind\": \"Project\", \"id\": \"...\"}], labels) and spec. On update, include the metadata.resourceVersion you read: a stale version is refused as a conflict"}
	principalTypes = []string{"person", "group", "agent", "apiKey"}
	scopeParam     = Param{Name: "scope", In: InQuery,
		Description: `A scope: "/" or "Kind/id", such as "Project/0190…"`}
)

// searchParams are a stored-log search's query.
var searchParams = []Param{
	{Name: "q", In: InQuery, Description: "A plain phrase the lines must contain (not a query language)"},
	{Name: "from", In: InQuery, Description: "Start, an RFC 3339 time (default an hour before to)"},
	{Name: "to", In: InQuery, Description: "End, an RFC 3339 time (default now)"},
	{Name: "limit", In: InQuery, Type: Integer, Max: 500, Description: "Lines per page (default 100)"},
	{Name: "before", In: InQuery, Description: "The next value of the previous page, for the page after it"},
}

// crud is the five resource operations for one kind, on the CLI, and as
// MCP tools when the kind is one an AI writes in this spec.
func crud(k kind) []Operation {
	what := k.Name
	ops := []Operation{
		{Name: underscore(k.Path) + "_list", CLI: []string{k.cli(), "list"}, MCP: k.AI,
			Summary: "List " + k.Path + " the caller may read", Method: "GET", Path: "/v1/" + k.Path,
			Params: []Param{ownerParam, labelsParam}, Hints: Hints{ReadOnly: true}},
		{Name: underscore(k.Path) + "_get", CLI: []string{k.cli(), "get"}, MCP: k.AI,
			Summary: "Get one " + what, Method: "GET", Path: "/v1/" + k.Path + "/{id}",
			Params: []Param{id(what)}, Hints: Hints{ReadOnly: true}},
		{Name: underscore(k.Path) + "_create", CLI: []string{k.cli(), "create"}, MCP: k.AI,
			Summary: "Create a " + what, Method: "POST", Path: "/v1/" + k.Path,
			Params: []Param{resourceBody}},
		{Name: underscore(k.Path) + "_update", CLI: []string{k.cli(), "update"}, MCP: k.AI,
			Summary: "Replace a " + what + "'s spec (send the version read, for a conflict check)", Method: "PUT", Path: "/v1/" + k.Path + "/{id}",
			Params: []Param{id(what), resourceBody}, Hints: Hints{Idempotent: true}},
		{Name: underscore(k.Path) + "_delete", CLI: []string{k.cli(), "delete"}, MCP: k.AI,
			Summary: "Delete a " + what + ". Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request",
			Method:  "DELETE", Path: "/v1/" + k.Path + "/{id}",
			Params: []Param{id(what), cascadeParam}, Hints: Hints{Destructive: true, Idempotent: true}},
	}
	if path, ok := secretEnvPath[k.Name]; ok {
		for _, i := range []int{2, 3} {
			ops[i].Params = append(ops[i].Params, secretEnvParams...)
			ops[i].edit = secretEnvEdit(path)
			ops[i].Summary += ". Secret variables are spec." + strings.Join(path, ".") +
				`: [{"env": NAME, "secret": a Secret ID, "key": a key of its value}]; naming a Secret needs reading its value`
		}
	}
	return ops
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
			Summary: "The optional packages this server offers (builds, observability): whether each is available and installable, its measured cost idle and in use (requirements; null means not measured), and what removing it loses. Install one by creating a ProviderInstance naming it",
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

		// Template versions (spec 027 plan decision 1): published, read and
		// deleted beneath their Template; a version never changes.
		Operation{Name: "template_versions_list", CLI: []string{"template-versions", "list"}, MCP: true,
			Summary: "A template's versions, oldest first; the one published most recently is marked latest",
			Method:  "GET", Path: "/v1/templates/{id}/versions",
			Params: []Param{id("Template")}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "template_versions_get", CLI: []string{"template-versions", "get"}, MCP: true,
			Summary: "One version of a template, by its label: its parameters (a JSON Schema object) and its body",
			Method:  "GET", Path: "/v1/templates/{id}/versions/{version}",
			Params: []Param{id("Template"), versionParam}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "template_versions_create", CLI: []string{"template-versions", "create"}, MCP: true,
			Summary: "Publish a new version of a template. metadata.name is its label (such as 1.0); spec has parameters and body. It's checked by rendering it, and can't be changed afterwards",
			Method:  "POST", Path: "/v1/templates/{id}/versions",
			Params: []Param{id("Template"), resourceBody}},
		Operation{Name: "template_versions_delete", CLI: []string{"template-versions", "delete"}, MCP: true,
			Summary: "Delete one version of a template. Recorded as an Approval; when it needs a person's decision, ask for one with approvals_request",
			Method:  "DELETE", Path: "/v1/templates/{id}/versions/{version}",
			Params: []Param{id("Template"), versionParam}, Hints: Hints{Destructive: true, Idempotent: true}},

		// Builds (spec 022).
		Operation{Name: "builds_trigger", CLI: []string{"builds", "trigger"}, MCP: true,
			Summary: "Build a source Component's image. It waits its turn behind a running build, and a successful build becomes the Component's new release. Needs the builds package",
			Method:  "POST", Path: "/v1/components/{id}/builds",
			Params: []Param{id("Component"), {Name: "commit", In: InField, Description: "Full commit ID to build (40 or 64 hex digits); the branch's head when omitted"}}},
		Operation{Name: "builds_list", CLI: []string{"builds", "list"}, MCP: true,
			Summary: "A source Component's builds, newest first, each with its phase, the commit built, its image and its times",
			Method:  "GET", Path: "/v1/components/{id}/builds",
			Params: []Param{id("Component")}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "builds_get", CLI: []string{"builds", "get"}, MCP: true,
			Summary: "One build: its phase, steps, commit, image and times",
			Method:  "GET", Path: "/v1/builds/{id}",
			Params: []Param{id("Build")}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "builds_cancel", CLI: []string{"builds", "cancel"}, MCP: true,
			Summary: "Stop a build that hasn't finished. What's running is unchanged",
			Method:  "POST", Path: "/v1/builds/{id}/cancel",
			Params: []Param{id("Build")}, Hints: Hints{Destructive: true, Idempotent: true}},
		Operation{Name: "builds_events", CLI: []string{"builds", "events"}, MCP: true,
			Summary: "Platform events about a build's run, newest first. Kept for an hour after the build ends",
			Method:  "GET", Path: "/v1/builds/{id}/events",
			Params: []Param{id("Build"), {Name: "since", In: InQuery, Description: "Only events after this RFC 3339 time"}},
			Hints:  Hints{ReadOnly: true}},
		Operation{Name: "builds_logs", CLI: []string{"builds", "logs"}, MCP: true,
			Summary: "A build's log lines, oldest first, kept for an hour after it ends. Build output can hold secrets, so logs need their own permission",
			Method:  "GET", Path: "/v1/builds/{id}/logs",
			Params: []Param{id("Build"),
				{Name: "tailLines", In: InQuery, Type: Integer, Max: 1000, Description: "Recent lines to read from each step"},
				{Name: "sinceSeconds", In: InQuery, Type: Integer, Description: "Only lines from the last this many seconds"},
				{Name: "follow", In: InQuery, Type: Boolean, Description: "Keep printing new lines as they're written, until the build ends, or the server ends the stream (idle 5 minutes, or 10 in all)",
					CLIOnly: "an MCP tool answers once; read recent lines instead"}},
			Hints: Hints{ReadOnly: true}},

		// Stored logs and metrics (spec 023).
		Operation{Name: "deployments_logs_search", CLI: []string{"logs", "search"}, MCP: true,
			Summary: "Search a Deployment's stored log lines, newest first, including lines from instances that are gone. One page per call: pass next as before for the next page; only an empty next ends a search (a page can be short). Logs can hold sensitive data, so they need their own permission. Needs the observability package",
			Method:  "GET", Path: "/v1/deployments/{id}/logs/search",
			Params: append([]Param{id("Deployment")}, searchParams...), Hints: Hints{ReadOnly: true}},
		Operation{Name: "applications_logs_search", CLI: []string{"logs", "search-application"}, MCP: true,
			Summary: "Search the stored log lines of an Application's Components in one Environment, newest first, paged like deployments_logs_search. Only the Deployments whose logs you may read are searched; omitted counts the others. Needs the observability package",
			Method:  "GET", Path: "/v1/applications/{id}/logs/search",
			Params: append([]Param{id("Application"), {Name: "environment", In: InQuery, Required: true, Description: "ID of the Environment to search in"}}, searchParams...),
			Hints:  Hints{ReadOnly: true}},
		Operation{Name: "deployments_metrics", CLI: []string{"metrics"}, MCP: true,
			Summary: "A Deployment's CPU (millicores) and memory (MiB) over a time range (the last hour by default, at least 15 minutes), against its requests and limits, and its HTTP requests (rate, 5xx rate, p50/p95/p99 latency in ms) where the network layer records them, with min, average, max and latest per series. What can't be measured is listed as unavailable, with why. Needs the observability package",
			Method:  "GET", Path: "/v1/deployments/{id}/metrics",
			Params: []Param{id("Deployment"),
				{Name: "from", In: InQuery, Description: "Start, an RFC 3339 time (default an hour before to)"},
				{Name: "to", In: InQuery, Description: "End, an RFC 3339 time (default now)"},
				{Name: "summary", In: InQuery, Type: Boolean, Description: "At most 60 points a series",
					CLIOnly: "an MCP tool always gets the summary"}},
			MCPQuery: map[string]string{"summary": "true"},
			Hints:    Hints{ReadOnly: true}},

		// Networking and traces (spec 025).
		Operation{Name: "networking_get", CLI: []string{"networking", "show"}, MCP: true,
			Summary: "The cluster's network layer: its name and version, whether network policies are enforced, whether connection records and request metrics can be read, its pod address ranges, what isn't available and why, and when this was read",
			Method:  "GET", Path: "/v1/networking", Hints: Hints{ReadOnly: true}},
		Operation{Name: "deployments_flows", CLI: []string{"flows"}, MCP: true,
			Summary: "A Deployment's connection records, newest first, each counting a minute's connections to or from it (a refused one counts the network layer's refusals, so retries can count more than once); refused ones carry the reason. The other end is named only if it's in the same Environment and you may read it (up to 20 Deployments); otherwise it's marked outside. Paged like deployments_logs_search. Needs the observability package and a network layer that records connections",
			Method:  "GET", Path: "/v1/deployments/{id}/flows",
			Params: []Param{id("Deployment"),
				{Name: "verdict", In: InQuery, Enum: []string{"all", "dropped"}, Description: "dropped keeps only refused connections (default all)"},
				{Name: "from", In: InQuery, Description: "Start, an RFC 3339 time (default an hour before to)"},
				{Name: "to", In: InQuery, Description: "End, an RFC 3339 time (default now)"},
				{Name: "limit", In: InQuery, Type: Integer, Max: 500, Description: "Records per page (default 100)"},
				{Name: "before", In: InQuery, Description: "The next value of the previous page, for the page after it"}},
			Hints: Hints{ReadOnly: true}},
		Operation{Name: "applications_traffic", CLI: []string{"traffic"}, MCP: true,
			Summary: "The HTTP traffic between an Application's Components in one Environment over a time range (the last hour by default, at least a minute): requests and failed (5xx) requests per second on each edge. Only edges whose two ends you may read are shown; omitted counts the Deployments left out. Needs the observability package and a network layer that records requests",
			Method:  "GET", Path: "/v1/applications/{id}/traffic",
			Params: []Param{id("Application"),
				{Name: "environment", In: InQuery, Required: true, Description: "ID of the Environment to read"},
				{Name: "from", In: InQuery, Description: "Start, an RFC 3339 time (default an hour before to)"},
				{Name: "to", In: InQuery, Description: "End, an RFC 3339 time (default now)"}},
			Hints: Hints{ReadOnly: true}},
		Operation{Name: "deployments_traces_list", CLI: []string{"traces", "list"}, MCP: true,
			Summary: "A Deployment's stored traces, newest first: each with its start, duration, root span, span count and whether a span failed. Paged like deployments_logs_search. Span data can hold sensitive values, so traces need their own permission. Needs the tracing package",
			Method:  "GET", Path: "/v1/deployments/{id}/traces",
			Params: []Param{id("Deployment"),
				{Name: "from", In: InQuery, Description: "Start, an RFC 3339 time (default an hour before to)"},
				{Name: "to", In: InQuery, Description: "End, an RFC 3339 time (default now)"},
				{Name: "errorsOnly", In: InQuery, Type: Boolean, Description: "Only traces with a failed span"},
				{Name: "minDuration", In: InQuery, Description: "Only traces at least this long, such as 250ms or 2s"},
				{Name: "limit", In: InQuery, Type: Integer, Max: 200, Description: "Traces per page (default 50)"},
				{Name: "before", In: InQuery, Description: "The next value of the previous page, for the page after it"}},
			Hints: Hints{ReadOnly: true}},
		Operation{Name: "deployments_traces_get", CLI: []string{"traces", "show"}, MCP: true,
			Summary: "One trace this Deployment is part of: its spans in start order, from the Deployments in its Environment whose traces you may read; partial says it has spans you can't see. Needs the tracing package",
			Method:  "GET", Path: "/v1/deployments/{id}/traces/{traceId}",
			Params: []Param{id("Deployment"), {Name: "traceId", In: InPath, Required: true, Description: "The trace ID, 32 hexadecimal digits"}},
			Hints:  Hints{ReadOnly: true}},

		Operation{Name: "secrets_read_value", CLI: []string{"secrets", "value", "get"}, MCP: true, SecretValue: true,
			Summary: "Read a Secret's value. Needs the secrets value data permission", Method: "GET", Path: "/v1/secrets/{id}/value",
			Params: []Param{id("Secret")}, Hints: Hints{ReadOnly: true}},
		Operation{Name: "secrets_write_value", CLI: []string{"secrets", "value", "set"}, MCP: true, SecretValue: true,
			Summary: "Write a Secret's value, replacing the old one. Needs the secrets value data permission", Method: "POST", Path: "/v1/secrets/{id}/value",
			Params: []Param{id("Secret"), {Name: "data", In: InField, Type: Object, Required: true, Description: "Key/value pairs"}},
			Hints:  Hints{Destructive: true, Idempotent: true}},
		Operation{Name: "secrets_status", CLI: []string{"secrets", "status"}, MCP: true,
			Summary: "Whether the Secrets capability is ready, and whether the cluster encrypts Secrets at rest (on, off or unknown), with the reason. Names no Secret or value",
			Method:  "GET", Path: "/v1/secrets/status", Hints: Hints{ReadOnly: true}},
	)
	return append(ops, alertingOperations()...)
}()

// maxMCPAlerts caps alerts_list for an AI agent (plan decision 13): 50
// Alerts with their deliveries stay well inside mcp.MaxResult, and an
// agent wanting more narrows by Component, state or severity, or reads
// alerts_summary.
const maxMCPAlerts = 50

// alertingOperations are spec 024's routes beyond the generic CRUD (plan
// decisions 12, 13 and 15).
func alertingOperations() []Operation {
	severities := []string{"info", "warning", "critical"}
	return []Operation{
		{Name: "alerts_list", CLI: []string{"alerts", "list"}, MCP: true,
			Summary: "Alerts the caller may read, newest first: firing and resolved, with their value against the threshold, who acknowledged them and how each notification went",
			Method:  "GET", Path: "/v1/alerts",
			Params: []Param{
				{Name: "componentId", In: InQuery, Description: "Only this Component's Alerts"},
				{Name: "applicationId", In: InQuery, Description: "Only Alerts of this Application's Components"},
				{Name: "environmentId", In: InQuery, Description: "Only Alerts in this Environment"},
				{Name: "state", In: InQuery, Enum: []string{"firing", "resolved"}, Description: "Only firing or only resolved Alerts"},
				{Name: "severity", In: InQuery, Enum: severities, Description: "Only this severity"},
				{Name: "limit", In: InQuery, Type: Integer, Max: 500, MCPMax: maxMCPAlerts,
					Description: "At most this many (default 100 on the CLI; at most and by default 50 for an AI agent)"},
			}, Hints: Hints{ReadOnly: true}},
		{Name: "alerts_get", CLI: []string{"alerts", "get"}, MCP: true,
			Summary: "One Alert", Method: "GET", Path: "/v1/alerts/{id}",
			Params: []Param{id("Alert")}, Hints: Hints{ReadOnly: true}},
		{Name: "alerts_summary", CLI: []string{"alerts", "summary"}, MCP: true,
			Summary: "How many of the caller's Alerts are firing, by severity, and how many of those nobody has acknowledged",
			Method:  "GET", Path: "/v1/alerts/summary", Hints: Hints{ReadOnly: true}},
		{Name: "alerts_acknowledge", CLI: []string{"alerts", "ack"}, MCP: true,
			Summary: "Acknowledge an Alert: records who is looking at it. Notifications still go out",
			Method:  "POST", Path: "/v1/alerts/{id}/acknowledge",
			Params: []Param{id("Alert")}, Hints: Hints{Idempotent: true}},
		{Name: "alertrules_silence", CLI: []string{"alertrules", "silence"}, MCP: true,
			Summary: "Silence a rule's notifications until a time (at most 30 days away). Its Alerts still open and resolve; only sending stops",
			Method:  "POST", Path: "/v1/alertrules/{id}/silence",
			Params: []Param{id("AlertRule"),
				{Name: "until", In: InField, Required: true, Description: "When the silence ends, an RFC 3339 time"},
				{Name: "reason", In: InField, Required: true, Description: "Why, shown with the silence"}},
			Hints: Hints{Idempotent: true}},
		{Name: "alertrules_unsilence", CLI: []string{"alertrules", "unsilence"}, MCP: true,
			Summary: "End a rule's silence now", Method: "POST", Path: "/v1/alertrules/{id}/unsilence",
			Params: []Param{id("AlertRule")}, Hints: Hints{Idempotent: true}},
		{Name: "notificationchannels_test", CLI: []string{"channels", "test"},
			Summary: "Send a test notification to a channel now, whatever its minimum severity, and record the result on it",
			Method:  "POST", Path: "/v1/notificationchannels/{id}/test",
			Params: []Param{id("NotificationChannel")}},
		{Name: "telegram_find_chats", CLI: []string{"channels", "find-chat"},
			Summary: "List the Telegram chats a bot has seen a message from (one getUpdates call), to find a channel's chatId. Send the bot a message first. Needs the Secret's value read",
			Method:  "POST", Path: "/v1/secrets/{id}/telegramchats",
			Params: []Param{id("Secret")}, Hints: Hints{Idempotent: true}},
		{Name: "heartbeat_get", CLI: []string{"heartbeat", "get"},
			Summary: "The heartbeat to an outside monitor: where it goes (the URL is a Secret), how often, and its last result",
			Method:  "GET", Path: "/v1/heartbeat", Hints: Hints{ReadOnly: true}},
		{Name: "heartbeat_set", CLI: []string{"heartbeat", "set"},
			Summary: "Set the heartbeat: a request to an outside monitor every interval, sent only while Serendip can alert",
			Method:  "PUT", Path: "/v1/heartbeat",
			Params: []Param{{Name: "heartbeat", In: InBody, Type: Object, Required: true,
				Description: "The heartbeat document: spec (urlSecretId, host, method, intervalMinutes, allowPrivateDestination)"}},
			Hints: Hints{Idempotent: true}},
		{Name: "heartbeat_test", CLI: []string{"heartbeat", "test"},
			Summary: "Send one heartbeat now, whatever Serendip's health, and record the result",
			Method:  "POST", Path: "/v1/heartbeat/test"},
		{Name: "heartbeat_delete", CLI: []string{"heartbeat", "delete"},
			Summary: "Stop the heartbeat. Recorded as an Approval. The outside monitor will then raise its alarm",
			Method:  "DELETE", Path: "/v1/heartbeat", Hints: Hints{Destructive: true, Idempotent: true}},
	}
}

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
		notifySettings = "notification channels and SMTP relays are set up by people: they name Secrets and where alert content leaves the laptop (RP-070); read through resources_list and resources_get"
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
		"POST /v1/notificationchannels/{id}/test":             {MCP: notifySettings},
		"POST /v1/secrets/{id}/telegramchats":                 {MCP: notifySettings},
		"GET /v1/heartbeat":                                   {MCP: platform},
		"PUT /v1/heartbeat":                                   {MCP: platform},
		"POST /v1/heartbeat/test":                             {MCP: platform},
		"DELETE /v1/heartbeat":                                {MCP: platform},
	}
	for _, k := range kinds {
		if k.AI {
			continue
		}
		reason := infraWrites
		if k.Name == "NotificationChannel" || k.Name == "SMTPServer" {
			reason = notifySettings
		}
		for _, r := range []string{"POST /v1/" + k.Path, "PUT /v1/" + k.Path + "/{id}", "DELETE /v1/" + k.Path + "/{id}"} {
			e[r] = Exclusion{MCP: reason}
		}
	}
	return e
}()
