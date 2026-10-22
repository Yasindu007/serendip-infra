package catalog

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var placeholder = regexp.MustCompile(`\{([^}]+)\}`)

func mustLookup(t *testing.T, name string) Operation {
	t.Helper()
	op, ok := Lookup(name)
	if !ok {
		t.Fatalf("no operation %q", name)
	}
	return op
}

func TestRequest(t *testing.T) {
	tests := []struct {
		name     string
		op       string
		args     map[string]any
		method   string
		path     string
		body     string
		errorHas string
	}{
		{name: "list with a filter", op: "applications_list", args: map[string]any{"ownerId": "p1"},
			method: "GET", path: "/v1/applications?ownerId=p1"},
		{name: "labels become label parameters", op: "applications_list", args: map[string]any{"labels": map[string]any{"team": "a", "tier": "web"}},
			method: "GET", path: "/v1/applications?label.team=a&label.tier=web"},
		{name: "label values must be text", op: "applications_list", args: map[string]any{"labels": map[string]any{"n": 1.0}},
			errorHas: "must map names to strings"},
		{name: "get", op: "components_get", args: map[string]any{"id": "c1"},
			method: "GET", path: "/v1/components/c1"},
		{name: "path values are escaped", op: "components_get", args: map[string]any{"id": "a/b"},
			method: "GET", path: "/v1/components/a%2Fb"},
		{name: "dot-dot can't steer the path", op: "secrets_read_value", args: map[string]any{"id": ".."}, errorHas: "non-empty name"},
		{name: "dot can't steer the path", op: "components_get", args: map[string]any{"id": "."}, errorHas: "non-empty name"},
		{name: "control characters are refused", op: "components_get", args: map[string]any{"id": "a\nb"}, errorHas: "non-empty name"},
		{name: "empty path value", op: "components_get", args: map[string]any{"id": ""}, errorHas: "non-empty name"},
		{name: "delete with cascade", op: "applications_delete", args: map[string]any{"id": "a1", "cascade": true},
			method: "DELETE", path: "/v1/applications/a1?cascade=true"},
		{name: "cascade false is sent on a delete", op: "components_delete", args: map[string]any{"id": "c1", "cascade": false},
			method: "DELETE", path: "/v1/components/c1?cascade=false"},
		{name: "create sends the document", op: "applications_create",
			args:   map[string]any{"resource": map[string]any{"metadata": map[string]any{"name": "shop"}}},
			method: "POST", path: "/v1/applications", body: `{"metadata":{"name":"shop"}}`},
		{name: "a nil body document is missing", op: "applications_create", args: map[string]any{"resource": nil}, errorHas: "resource is required"},
		{name: "a body document must be an object", op: "applications_create", args: map[string]any{"resource": "x"}, errorHas: "must be an object"},
		{name: "a generic read names its collection", op: "resources_get", args: map[string]any{"kind": "projects", "id": "p1"},
			method: "GET", path: "/v1/projects/p1"},
		{name: "a generic read refuses a kind outside its list", op: "resources_get", args: map[string]any{"kind": "secrets", "id": "s1"},
			errorHas: "kind must be one of"},
		{name: "approval request nests the target", op: "approvals_request",
			args:   map[string]any{"kind": "Application", "id": "a1", "cascade": true, "reason": "retired"},
			method: "POST", path: "/v1/approvals", body: `{"action":{"cascade":true,"target":{"id":"a1","kind":"Application"},"verb":"delete"},"reason":"retired"}`},
		{name: "approval request without cascade or reason", op: "approvals_request",
			args:   map[string]any{"kind": "Component", "id": "c1"},
			method: "POST", path: "/v1/approvals", body: `{"action":{"target":{"id":"c1","kind":"Component"},"verb":"delete"}}`},
		{name: "a nil cascade is absent", op: "approvals_request", args: map[string]any{"kind": "Component", "id": "c1", "cascade": nil},
			method: "POST", path: "/v1/approvals", body: `{"action":{"target":{"id":"c1","kind":"Component"},"verb":"delete"}}`},
		{name: "cascade false is left out of a request", op: "approvals_request", args: map[string]any{"kind": "Component", "id": "c1", "cascade": false},
			method: "POST", path: "/v1/approvals", body: `{"action":{"target":{"id":"c1","kind":"Component"},"verb":"delete"}}`},
		{name: "cascade must be boolean", op: "approvals_request", args: map[string]any{"kind": "Application", "id": "a1", "cascade": "yes"},
			errorHas: "cascade must be a boolean"},
		{name: "a reason must be text", op: "approvals_request", args: map[string]any{"kind": "Component", "id": "c1", "reason": 3.0},
			errorHas: "reason must be a string"},
		{name: "approval request needs a kind", op: "approvals_request", args: map[string]any{"id": "c1"}, errorHas: "kind is required"},
		{name: "decision comment is a field", op: "approvals_approve", args: map[string]any{"id": "x", "comment": "ok"},
			method: "POST", path: "/v1/approvals/x/approve", body: `{"comment":"ok"}`},
		{name: "decision without a comment has no body", op: "approvals_reject", args: map[string]any{"id": "x"},
			method: "POST", path: "/v1/approvals/x/reject"},
		{name: "a query enum is checked", op: "approvals_list", args: map[string]any{"phase": "Done"}, errorHas: "phase must be one of"},
		{name: "a JSON number is plain decimal", op: "approvals_list", args: map[string]any{"limit": 100.0},
			method: "GET", path: "/v1/approvals?limit=100"},
		{name: "a limit over the API's cap", op: "approvals_list", args: map[string]any{"limit": 501.0}, errorHas: "from 1 to 500"},
		{name: "a fractional limit", op: "approvals_list", args: map[string]any{"limit": 2.5}, errorHas: "whole number"},
		{name: "secret value is wrapped in data", op: "secrets_write_value",
			args:   map[string]any{"id": "s1", "data": map[string]any{"k": "v"}},
			method: "POST", path: "/v1/secrets/s1/value", body: `{"data":{"k":"v"}}`},
		{name: "query values are escaped", op: "access_directory_search", args: map[string]any{"type": "person", "q": "a&b=c#d"},
			method: "GET", path: "/v1/authorization/directory?q=a%26b%3Dc%23d&type=person"},
		{name: "directory search needs a type", op: "access_directory_search", args: map[string]any{"q": "ann"}, errorHas: "type is required"},
		{name: "assignments by principal", op: "access_assignments_list", args: map[string]any{"principalType": "agent", "principalId": "x"},
			method: "GET", path: "/v1/authorization/roleAssignments?principalId=x&principalType=agent"},
		{name: "default provider is a field", op: "capability_default_set", args: map[string]any{"name": "identity", "provider": "thunderid"},
			method: "PUT", path: "/v1/capabilities/identity/default", body: `{"provider":"thunderid"}`},
		{name: "a missing optional parameter is left out", op: "access_permissions", args: map[string]any{},
			method: "GET", path: "/v1/authorization/permissions"},
		{name: "missing required", op: "components_get", args: map[string]any{}, errorHas: "id is required"},
		{name: "unknown parameter", op: "components_get", args: map[string]any{"id": "c1", "force": true}, errorHas: `unknown parameter "force"`},
		{name: "object in a query", op: "applications_list", args: map[string]any{"ownerId": map[string]any{}}, errorHas: "must be a string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := mustLookup(t, tt.op).Request(tt.args)
			if tt.errorHas != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errorHas) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.errorHas)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if req.Method != tt.method || req.Path != tt.path || string(req.Body) != tt.body {
				t.Errorf("got %s %s %s, want %s %s %s", req.Method, req.Path, req.Body, tt.method, tt.path, tt.body)
			}
		})
	}
}

func TestOperationsAreWellFormed(t *testing.T) {
	names := map[string]bool{}
	commands := map[string]bool{}
	for _, op := range Operations() {
		if names[op.Name] {
			t.Errorf("duplicate operation name %q", op.Name)
		}
		names[op.Name] = true
		if op.CLI == nil && !op.MCP {
			t.Errorf("%s is on neither surface", op.Name)
		}
		if op.CLI != nil {
			c := strings.Join(op.CLI, " ")
			if commands[c] {
				t.Errorf("duplicate CLI command %q", c)
			}
			commands[c] = true
		}
		if op.Summary == "" {
			t.Errorf("%s has no summary", op.Name)
		}
		if op.Hints.ReadOnly != (op.Method == "GET") {
			t.Errorf("%s: read-only hint %v for %s", op.Name, op.Hints.ReadOnly, op.Method)
		}
		if op.Method == "DELETE" && !op.Hints.Destructive {
			t.Errorf("%s deletes but isn't marked destructive", op.Name)
		}
		if strings.ContainsAny(op.Name, "-. ") || len(op.Name) > 64 {
			t.Errorf("%s isn't a valid MCP tool name", op.Name)
		}
		bodies, fields := 0, false
		if op.body != nil {
			bodies++
		}
		for _, p := range op.Params {
			if p.In == InPath && !strings.Contains(op.Path, "{"+p.Name+"}") {
				t.Errorf("%s: path parameter %s isn't in %s", op.Name, p.Name, op.Path)
			}
			if p.Expand && (p.In != InPath || len(p.Enum) == 0) {
				t.Errorf("%s: %s expands but isn't a path parameter with values", op.Name, p.Name)
			}
			switch p.In {
			case InBody:
				bodies++
			case InField:
				fields = true
			}
		}
		if fields && op.body == nil {
			bodies++
		}
		if bodies > 1 {
			t.Errorf("%s has more than one source for its body", op.Name)
		}
		for _, m := range placeholder.FindAllStringSubmatch(op.Path, -1) {
			if !slices.ContainsFunc(op.Params, func(p Param) bool { return p.Name == m[1] && p.In == InPath }) {
				t.Errorf("%s: placeholder {%s} has no path parameter", op.Name, m[1])
			}
		}
		if _, err := json.Marshal(op.InputSchema()); err != nil {
			t.Errorf("%s: input schema: %v", op.Name, err)
		}
	}
}

func TestSecretValueOperations(t *testing.T) {
	var got []string
	for _, op := range Operations() {
		if op.SecretValue {
			got = append(got, op.Name)
			if !strings.HasPrefix(op.Path, "/v1/secrets/{id}/value") {
				t.Errorf("%s is marked as a secret value but its path is %s", op.Name, op.Path)
			}
		} else if strings.HasSuffix(op.Path, "/value") {
			t.Errorf("%s reaches a value but isn't marked", op.Name)
		}
	}
	if want := []string{"secrets_read_value", "secrets_write_value"}; !slices.Equal(got, want) {
		t.Errorf("secret value operations = %v, want %v", got, want)
	}
}

func TestRoutesExpandOnlyMarkedParameters(t *testing.T) {
	got := mustLookup(t, "resources_get").Routes()
	if len(got) != len(otherPaths()) {
		t.Errorf("resources_get covers %d routes, want %d", len(got), len(otherPaths()))
	}
	for _, k := range kinds {
		if k.AI && slices.Contains(got, "GET /v1/"+k.Path+"/{id}") {
			t.Errorf("resources_get covers %s, which has its own tools", k.Path)
		}
	}
	if !slices.Contains(got, "GET /v1/projects/{id}") {
		t.Errorf("resources_get routes = %v", got)
	}
	graph := mustLookup(t, "graph_children").Routes()
	if want := []string{"GET /v1/graph/{kind}/{id}/children"}; !slices.Equal(graph, want) {
		t.Errorf("graph_children routes = %v, want %v", graph, want)
	}
}

func TestInputSchema(t *testing.T) {
	s := mustLookup(t, "approvals_request").InputSchema()
	if s["type"] != "object" || s["additionalProperties"] != false {
		t.Errorf("schema = %v", s)
	}
	if req, _ := s["required"].([]string); !slices.Equal(req, []string{"kind", "id"}) {
		t.Errorf("required = %v", s["required"])
	}
	props := s["properties"].(map[string]any)
	if props["reason"].(map[string]any)["type"] != "string" {
		t.Errorf("reason schema = %v", props["reason"])
	}
	limit := mustLookup(t, "approvals_list").InputSchema()["properties"].(map[string]any)["limit"].(map[string]any)
	if limit["type"] != "integer" || limit["maximum"] != 500 {
		t.Errorf("limit schema = %v", limit)
	}
}

func TestKindsAreDistinct(t *testing.T) {
	names, paths := map[string]bool{}, map[string]bool{}
	for _, k := range kinds {
		if names[k.Name] || paths[k.Path] {
			t.Errorf("duplicate kind %s (%s)", k.Name, k.Path)
		}
		names[k.Name], paths[k.Path] = true, true
	}
}

func TestExclusionsGiveAReason(t *testing.T) {
	for r, e := range Excluded() {
		if e.CLI == "" && e.MCP == "" {
			t.Errorf("exclusion for %s gives no reason on either surface", r)
		}
	}
}

func TestReturnedCollectionsAreCopies(t *testing.T) {
	ops := Operations()
	ops[0] = Operation{}
	if Operations()[0].Name == "" {
		t.Error("changing the returned slice changed the catalogue")
	}
	ex := Excluded()
	clear(ex)
	if len(Excluded()) == 0 {
		t.Error("changing the returned map changed the catalogue")
	}
	s := mustLookup(t, "approvals_list").InputSchema()
	s["properties"].(map[string]any)["phase"].(map[string]any)["enum"].([]string)[0] = "x"
	if mustLookup(t, "approvals_list").Params[0].Enum[0] == "x" {
		t.Error("changing a returned schema changed the catalogue")
	}
}

// The CLI and MCP reference lists every operation (spec 021 T013).
func TestReferenceListsEveryOperation(t *testing.T) {
	b, err := os.ReadFile("../../docs/cli-and-mcp.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, op := range Operations() {
		if !strings.Contains(doc, "`"+op.Method+" "+op.Path+"`") {
			t.Errorf("docs/cli-and-mcp.md doesn't list %s (%s %s)", op.Name, op.Method, op.Path)
		}
	}
}
