package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// signIn stores a sign-in as serendip login would.
func (w *world) signIn(t *testing.T) {
	t.Helper()
	if err := saveCredentials(w.dir, &credentials{
		Server: w.api.URL, Issuer: w.issuer.srv.URL, ClientID: "serendip", AssistantID: "serendip-mcp",
		TokenURL: w.issuer.srv.URL + "/oauth2/token", RevocationURL: w.issuer.srv.URL + "/oauth2/revoke",
		AccessToken: "at-1", RefreshToken: "rt-1", Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

const meta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"claude-code","version":"2.1.304"},"io.modelcontextprotocol/clientCapabilities":{}}`

func call(id int, tool, args string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s,%s}}`, id, tool, args, meta)
}

// lines collects what serendip mcp writes, a line at a time.
type lines struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	got  []string
	more chan struct{}
}

func (l *lines) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.Write(b)
	for {
		line, err := l.buf.ReadString('\n')
		if err != nil {
			l.buf.WriteString(line)
			break
		}
		l.got = append(l.got, strings.TrimSpace(line))
		select {
		case l.more <- struct{}{}:
		default:
		}
	}
	return len(b), nil
}

func (l *lines) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.got)
}

// session runs serendip mcp as an AI client on the person's machine
// would: it sends the requests, keeps standard input open until every
// answer has come, then closes it. Answers are returned by ID.
func (w *world) session(t *testing.T, extraEnv map[string]string, args []string, requests ...string) (result, map[int]map[string]any) {
	t.Helper()
	pr, pw := io.Pipe()
	out := &lines{more: make(chan struct{}, 1)}
	var errOut bytes.Buffer
	getenv := func(k string) string {
		switch k {
		case "SERENDIP_CONFIG_DIR":
			return w.dir
		case "SERENDIP_SERVER":
			return w.api.URL
		}
		return extraEnv[k]
	}
	codeCh := make(chan int, 1)
	go func() {
		codeCh <- runWith(context.Background(), append([]string{"mcp"}, args...), env{getenv: getenv, stdin: pr, stdout: out, stderr: &errOut})
	}()
	go func() {
		for _, r := range requests {
			fmt.Fprintln(pw, r)
		}
	}()
	deadline := time.After(10 * time.Second)
	for out.count() < len(requests) {
		select {
		case <-out.more:
		case code := <-codeCh:
			pw.Close()
			return result{code: code, stderr: errOut.String()}, nil
		case <-deadline:
			t.Fatalf("only %d of %d answers (stderr %s)", out.count(), len(requests), errOut.String())
		}
	}
	pw.Close()
	code := <-codeCh
	answers := map[int]map[string]any{}
	out.mu.Lock()
	defer out.mu.Unlock()
	for _, line := range out.got {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdout carries something that isn't JSON-RPC: %q", line)
		}
		if id, ok := m["id"].(float64); ok {
			answers[int(id)] = m
		}
	}
	return result{code: code, stdout: strings.Join(out.got, "\n"), stderr: errOut.String()}, answers
}

func toolText(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	res, _ := m["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("no content in %v", m)
	}
	isError, _ := res["isError"].(bool)
	return content[0].(map[string]any)["text"].(string), isError
}

func TestMCPActsForThePersonWithGuardrails(t *testing.T) {
	w := newWorld(t)
	w.signIn(t)
	r, got := w.session(t, nil, nil,
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{`+meta+`}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{`+meta+`}}`,
		call(3, "applications_get", fmt.Sprintf(`{"id":%q}`, w.appID)),
		call(4, "applications_delete", fmt.Sprintf(`{"id":%q}`, w.appID)),
		call(5, "approvals_request", fmt.Sprintf(`{"kind":"Application","id":%q,"reason":"retired"}`, w.appID)),
	)
	if r.code != exitOK {
		t.Fatalf("serendip mcp: %d %s", r.code, r.stderr)
	}
	if len(got) != 5 {
		t.Fatalf("answers = %v (stderr %s)", got, r.stderr)
	}
	tools := got[2]["result"].(map[string]any)["tools"].([]any)
	if len(tools) < 20 {
		t.Errorf("only %d tools listed", len(tools))
	}
	if text, isErr := toolText(t, got[3]); isErr || !strings.Contains(text, `"shop"`) {
		t.Errorf("read with the person's roles: %v %q", isErr, text)
	}
	if text, isErr := toolText(t, got[4]); !isErr || !strings.Contains(text, "approvals_request") {
		t.Errorf("a delete through the assistant: %v %q", isErr, text)
	}
	if text, isErr := toolText(t, got[5]); isErr || !strings.Contains(text, `"Pending"`) {
		t.Errorf("filing the request: %v %q", isErr, text)
	}
	for _, s := range w.sent() {
		if !strings.HasSuffix(s, " mcp") {
			t.Errorf("request %q didn't declare mcp", s)
		}
	}
	// The application is still there: the person decides.
	if r := w.cli(ops, "", "applications", "get", w.appID); r.code != exitOK {
		t.Errorf("deleted without approval: %d", r.code)
	}
}

func TestMCPNoSecretValues(t *testing.T) {
	w := newWorld(t)
	w.signIn(t)
	_, got := w.session(t, nil, []string{"--no-secret-values"}, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{`+meta+`}}`)
	for _, tl := range got[1]["result"].(map[string]any)["tools"].([]any) {
		if name := tl.(map[string]any)["name"].(string); strings.HasSuffix(name, "_value") {
			t.Errorf("tool %s listed with --no-secret-values", name)
		}
	}
}

func TestMCPRefusals(t *testing.T) {
	w := newWorld(t)
	if r := w.cli(nil, "", "mcp"); r.code != exitUnauthorized {
		t.Errorf("not signed in: %d %s", r.code, r.stderr)
	}
	w.signIn(t)
	for _, k := range []string{"SERENDIP_API_KEY", "SERENDIP_TOKEN"} {
		if r := w.cli(map[string]string{k: "x"}, "", "mcp"); r.code != exitError || !strings.Contains(r.stderr, "unset SERENDIP_API_KEY") {
			t.Errorf("with %s: %d %s", k, r.code, r.stderr)
		}
	}
	if r := w.cli(nil, "", "mcp", "--everything"); r.code != exitUsage {
		t.Errorf("unknown flag: %d", r.code)
	}
	// After logout, the assistant can't act for the person any more.
	if r := w.cli(nil, "", "logout"); r.code != exitOK {
		t.Fatalf("logout: %s", r.stderr)
	}
	w.signIn(t) // a stale copy of the revoked sign-in, as a second machine might hold
	if r := w.cli(nil, "", "mcp"); r.code != exitError || !strings.Contains(r.stderr, "act for the person") {
		t.Errorf("after the sign-in was revoked: %d %s", r.code, r.stderr)
	}
}
