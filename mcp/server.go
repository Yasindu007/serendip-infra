// Package mcp serves api/catalog's operations as MCP tools (spec 021): over
// stdio for an assistant on a person's machine (`serendip mcp`), and over
// streamable HTTP for `serendipd`'s /mcp. It is the only package that
// imports the MCP Go SDK (ADR 0017).
//
// Nothing here decides anything. Every tool call becomes one request to
// Serendip's API through a Caller, and the API's RBAC, approvals and audit
// decide it, exactly as for the console and the CLI (constitution 5, 7).
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/serendip-infra/serendip-infra/api/catalog"
)

// Caller sends one request to Serendip's API on the tool caller's behalf.
//
// authorization is the Authorization header of the MCP request when the
// tool call arrived over HTTP, so an in-process caller can act as whoever
// sent it; it is empty over stdio, where the caller has its own
// credential. Nothing else from the MCP request is passed on.
//
// A Caller's error is logged with the operation's name, never shown to the
// model, but must still never contain a credential.
type Caller func(ctx context.Context, authorization string, r catalog.Request) (Response, error)

// Response is the API's answer.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Options configure a server.
type Options struct {
	// Version is reported to clients.
	Version string
	// NoSecretValues leaves the Secret value tools out entirely, for
	// clients that must never see a value (spec 021 scope item 15).
	NoSecretValues bool
	// Logger receives protocol and transport errors, and failed API calls
	// by operation name. Never arguments, results or headers. Nil discards.
	Logger *slog.Logger
}

// MaxResult bounds the API body put into one tool result: a model can't
// use more, and the hardware this runs on shouldn't copy more.
const MaxResult = 256 << 10

// needsApproval is the API's problem type for a request that waits for a
// person (api.ProblemNeedsApproval; api imports this package, so it can't
// be imported here).
const needsApproval = "urn:serendip:problem:needs-approval"

const instructions = `Serendip is a platform for applications, their components and secrets.
You act as an AI agent: your calls are decided by the roles of the person you act for, with agent guardrails.
Deletes always need an approval: when a delete answers that it needs a person, file it with approvals_request and a reason, then follow it with approvals_get.
You can never approve or reject a request.`

// Server is an MCP server whose tools are the catalogue's MCP operations.
// Safe for concurrent use.
type Server struct {
	sdk *sdk.Server
	log *slog.Logger
}

// NewServer returns a server that sends each tool call to the API through
// call. It panics if a catalogue schema is invalid, which a test rules out.
func NewServer(call Caller, o Options) *Server {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := sdk.NewServer(&sdk.Implementation{Name: "serendip", Title: "Serendip", Version: o.Version},
		&sdk.ServerOptions{Instructions: instructions, Logger: log})
	for _, op := range Tools(o) {
		s.AddTool(tool(op), handler(op, call, log))
	}
	return &Server{sdk: s, log: log}
}

// Tools returns the operations a server with these options offers.
func Tools(o Options) []catalog.Operation {
	var out []catalog.Operation
	for _, op := range catalog.Operations() {
		if op.MCP && !(o.NoSecretValues && op.SecretValue) {
			out = append(out, op)
		}
	}
	return out
}

// ServeStdio serves on standard input and output until the client
// disconnects or ctx ends.
func (s *Server) ServeStdio(ctx context.Context) error {
	return s.sdk.Run(ctx, &sdk.StdioTransport{})
}

// ServeIO serves newline-delimited JSON-RPC on r and w until r ends or ctx
// ends: stdio, with the streams given.
func (s *Server) ServeIO(ctx context.Context, r io.ReadCloser, w io.WriteCloser) error {
	return s.sdk.Run(ctx, &sdk.IOTransport{Reader: r, Writer: w})
}

// Connect serves one client over t, such as an in-memory transport.
func (s *Server) Connect(ctx context.Context, t sdk.Transport) (*sdk.ServerSession, error) {
	return s.sdk.Connect(ctx, t, nil)
}

// Handler serves over streamable HTTP, statelessly: every request stands
// alone, as the 2026-07-28 protocol expects, so nothing is kept between
// calls.
//
// It refuses cross-origin browser requests (net/http's
// CrossOriginProtection; clients that send no Origin, such as Claude Code,
// are unaffected), Host names that aren't loopback on a loopback listener
// (the SDK's DNS-rebinding check), and bodies over 1 MiB. Authentication
// and server timeouts are the caller's.
func (s *Server) Handler() http.Handler {
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s.sdk },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20, Logger: s.log})
	return http.NewCrossOriginProtection().Handler(h)
}

func tool(op catalog.Operation) *sdk.Tool {
	no := false
	destructive := op.Hints.Destructive
	return &sdk.Tool{
		Name:        op.Name,
		Description: op.Summary,
		InputSchema: op.InputSchema(),
		Annotations: &sdk.ToolAnnotations{
			ReadOnlyHint:    op.Hints.ReadOnly,
			DestructiveHint: &destructive,
			IdempotentHint:  op.Hints.Idempotent,
			// Serendip's tools act on Serendip only.
			OpenWorldHint: &no,
		},
	}
}

func handler(op catalog.Operation, call Caller, log *slog.Logger) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var args map[string]any
		if raw := bytes.TrimSpace(req.Params.Arguments); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
			if err := json.Unmarshal(raw, &args); err != nil {
				return failed("arguments must be a JSON object"), nil
			}
		}
		r, err := op.Request(args)
		if err != nil {
			return failed(err.Error()), nil
		}
		var authorization string
		if req.Extra != nil && req.Extra.Header != nil {
			authorization = req.Extra.Header.Get("Authorization")
		}
		resp, err := call(ctx, authorization, r)
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return failed("The request to Serendip's API timed out."), nil
		case errors.Is(err, context.Canceled):
			return failed("The request to Serendip's API was cancelled."), nil
		case err != nil:
			log.Error("mcp: API call failed", "tool", op.Name, "error", err)
			return failed("Serendip's API couldn't be reached."), nil
		}
		return result(resp), nil
	}
}

func failed(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

// result turns the API's answer into a tool result: the body for a
// success, with what its headers add, and the problem's title and detail
// for a refusal, with what to do next when it waits for a person.
func result(r Response) *sdk.CallToolResult {
	if r.Status >= 200 && r.Status < 300 {
		body := bytes.TrimSpace(r.Body)
		var b strings.Builder
		switch {
		case len(body) == 0:
			fmt.Fprintf(&b, "Done (HTTP %d).", r.Status)
		case len(body) > MaxResult:
			b.Grow(MaxResult + 128)
			b.Write(body[:MaxResult])
			fmt.Fprintf(&b, "\n\n[Truncated at %d of %d bytes: narrow the request, such as with a filter, limit or after.]", MaxResult, len(body))
		default:
			b.Write(body)
		}
		if id := r.Header.Get("Serendip-Approval-Id"); id != "" {
			fmt.Fprintf(&b, "\n\nRecorded as approval %q.", id)
		}
		if next := r.Header.Get("Serendip-Next-After"); next != "" {
			fmt.Fprintf(&b, "\n\nMore results: call again with after=%q.", next)
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: b.String()}}}
	}
	var p struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	text := fmt.Sprintf("HTTP %d", r.Status)
	if json.Unmarshal(r.Body, &p) == nil && (p.Title != "" || p.Detail != "") {
		text = fmt.Sprintf("HTTP %d %s: %s", r.Status, p.Title, p.Detail)
	}
	if p.Type == needsApproval {
		text += "\n\nThis needs a person's approval. File the request with approvals_request (kind, id, cascade if you asked for it, and a reason); a person decides it. Check it with approvals_get."
	}
	return failed(text)
}
