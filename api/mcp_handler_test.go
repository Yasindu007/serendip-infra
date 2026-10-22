package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/policy"
)

const mcpMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"agent","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}`

// mcpCall sends one tools/call to /mcp as a 2026-07-28 client does.
func (w *authzWorld) mcpCall(token, tool, args string) *httptest.ResponseRecorder {
	w.t.Helper()
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s,%s}}`, tool, args, mcpMeta)
	req := httptest.NewRequest(http.MethodPost, api.MCPPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", tool)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, req)
	return rec
}

// toolResult decodes a tools/call answer.
func toolResult(t *testing.T, rec *httptest.ResponseRecorder) (string, bool) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var msg struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil || msg.Error != nil || len(msg.Result.Content) != 1 {
		t.Fatalf("answer = %s", rec.Body)
	}
	return msg.Result.Content[0].Text, msg.Result.IsError
}

func TestMCPEndpoint_Agents(t *testing.T) {
	withAgentTokens(t)
	people["tok-owner-ai"] = identity.Principal{Subject: "sub-owner", Name: "owner", Via: "serendip-mcp"}
	t.Cleanup(func() { delete(people, "tok-owner-ai") })
	w := newAuthzWorld(t, func(w *authzWorld) {
		grantAgent(t, w, "am-dev/agent-1", policy.ContributorRoleID, "projA", "sub-owner")
		w.grant("tok-owner", policy.OwnerRoleID, "/")
	}, api.WithMCP())

	// A hosted agent: its own roles decide.
	text, isErr := toolResult(t, w.mcpCall("tok-agent", "applications_get", fmt.Sprintf(`{"id":%q}`, w.id["app"])))
	if isErr || !strings.Contains(text, w.id["app"]) {
		t.Errorf("agent read in scope: %v %q", isErr, text)
	}
	if text, isErr := toolResult(t, w.mcpCall("tok-agent", "applications_get", fmt.Sprintf(`{"id":%q}`, w.id["appB"]))); !isErr || !strings.Contains(text, "403") {
		t.Errorf("agent read out of scope: %v %q", isErr, text)
	}
	// Its delete needs a person, and it can file the request.
	if text, isErr := toolResult(t, w.mcpCall("tok-agent", "components_delete", fmt.Sprintf(`{"id":%q}`, w.id["web"]))); !isErr || !strings.Contains(text, "approvals_request") {
		t.Errorf("agent delete: %v %q", isErr, text)
	}
	text, isErr = toolResult(t, w.mcpCall("tok-agent", "approvals_request", fmt.Sprintf(`{"kind":"Component","id":%q,"reason":"unused"}`, w.id["web"])))
	if isErr || !strings.Contains(text, `"Pending"`) {
		t.Fatalf("agent files a request: %v %q", isErr, text)
	}
	w.expect("tok-owner", http.MethodGet, "/v1/components/"+w.id["web"], nil, http.StatusOK)

	// Every tool call is audited as mcp, set by the server itself.
	var iface, actor, sponsor string
	if err := w.db.QueryRow(`SELECT interface, actor, COALESCE(actor_sponsor,'') FROM audit_log WHERE kind = 'Approval' ORDER BY at DESC LIMIT 1`).Scan(&iface, &actor, &sponsor); err != nil {
		t.Fatal(err)
	}
	if iface != api.MCPInterface || actor != "agent:am-dev/agent-1" || sponsor != "sub-owner" {
		t.Errorf("audit = interface %q, actor %q, sponsor %q", iface, actor, sponsor)
	}

	// A person acting through an AI assistant also works.
	if text, isErr := toolResult(t, w.mcpCall("tok-owner-ai", "applications_get", fmt.Sprintf(`{"id":%q}`, w.id["appB"]))); isErr {
		t.Errorf("delegated read: %q", text)
	}
}

func TestMCPEndpoint_Refusals(t *testing.T) {
	withAgentTokens(t)
	w := newAuthzWorld(t, func(w *authzWorld) {
		w.grant("tok-owner", policy.OwnerRoleID, "/")
	}, api.WithMCP(), api.WithResourceMetadata(api.ResourceMetadata{Issuer: func() string { return "https://idp.example" }}))
	args := fmt.Sprintf(`{"id":%q}`, w.id["app"])

	rec := w.mcpCall("", "applications_get", args)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"), `resource_metadata="http://example.com/.well-known/oauth-protected-resource"`) {
		t.Errorf("no token: %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	if rec := w.mcpCall("tok-unknown", "applications_get", args); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("a bad token: %d", rec.Code)
	}
	if rec := w.mcpCall("tok-owner", "applications_get", args); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "serendip mcp") {
		t.Errorf("a person's own token: %d %s", rec.Code, rec.Body)
	}
	if rec := w.mcpCall("key-ci", "applications_get", args); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "API key") {
		t.Errorf("an API key: %d %s", rec.Code, rec.Body)
	}
	// A cross-origin browser request is refused, even with a valid agent
	// token that passes the gate.
	req := httptest.NewRequest(http.MethodPost, api.MCPPath, strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Authorization", "Bearer tok-agent")
	got := httptest.NewRecorder()
	w.h.ServeHTTP(got, req)
	if got.Code != http.StatusForbidden {
		t.Errorf("cross-origin: %d", got.Code)
	}
	// Without the option there's no /mcp.
	plain := newAuthzWorld(t, func(w *authzWorld) {})
	if rec := plain.mcpCall("tok-owner", "applications_get", args); rec.Code != http.StatusNotFound {
		t.Errorf("without WithMCP: %d", rec.Code)
	}
	// Without resource metadata the challenge names no document.
	bare := newAuthzWorld(t, func(w *authzWorld) {}, api.WithMCP())
	if rec := bare.mcpCall("", "applications_get", args); rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("challenge without metadata = %q", rec.Header().Get("WWW-Authenticate"))
	}
}

// A client can't relabel its MCP calls: X-Serendip-Client on /mcp never
// reaches the API, and the interface stays the server's "mcp".
func TestMCPEndpoint_InterfaceCantBeSpoofed(t *testing.T) {
	withAgentTokens(t)
	w := newAuthzWorld(t, func(w *authzWorld) {
		grantAgent(t, w, "am-dev/agent-1", policy.ContributorRoleID, "projA", "sub-owner")
	}, api.WithMCP())
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"approvals_request","arguments":{"kind":"Component","id":%q,"reason":"x"},%s}}`, w.id["web"], mcpMeta)
	req := httptest.NewRequest(http.MethodPost, api.MCPPath, strings.NewReader(body))
	for k, v := range map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream",
		"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/call", "Mcp-Name": "approvals_request",
		"Authorization": "Bearer tok-agent", "X-Serendip-Client": "cli"} {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, req)
	if text, isErr := toolResult(t, rec); isErr {
		t.Fatalf("request: %s", text)
	}
	var iface string
	if err := w.db.QueryRow(`SELECT interface FROM audit_log WHERE kind = 'Approval' ORDER BY at DESC LIMIT 1`).Scan(&iface); err != nil {
		t.Fatal(err)
	}
	if iface != api.MCPInterface {
		t.Errorf("interface = %q, want %q whatever the client declares", iface, api.MCPInterface)
	}
}

func TestDeclaredInterface(t *testing.T) {
	w := newAuthzWorld(t, func(w *authzWorld) {
		w.grant("tok-owner", policy.OwnerRoleID, "/")
	})
	create := func(name, client string) string {
		b, _ := json.Marshal(map[string]any{"metadata": map[string]any{"name": name}})
		req := httptest.NewRequest(http.MethodPost, "/v1/organizations", strings.NewReader(string(b)))
		req.Header.Set("Authorization", "Bearer tok-owner")
		req.Header.Set("Content-Type", "application/json")
		if client != "" {
			req.Header.Set("X-Serendip-Client", client)
		}
		rec := httptest.NewRecorder()
		w.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rec.Code, rec.Body)
		}
		var iface string
		if err := w.db.QueryRow(`SELECT interface FROM audit_log WHERE kind = 'Organization' AND json_extract(after, '$.metadata.name') = ?`, name).Scan(&iface); err != nil {
			t.Fatal(err)
		}
		return iface
	}
	for client, want := range map[string]string{"": api.InterfaceName, "cli": "cli (declared)", "MCP": "mcp (declared)", "console": api.InterfaceName, "cli\nx": api.InterfaceName} {
		if got := create("org-"+strings.ReplaceAll(fmt.Sprintf("%q", client), `"`, ""), client); got != want {
			t.Errorf("X-Serendip-Client %q: interface %q, want %q", client, got, want)
		}
	}
}
