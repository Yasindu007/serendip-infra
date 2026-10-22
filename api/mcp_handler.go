package api

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/serendip-infra/serendip-infra/api/catalog"
	"github.com/serendip-infra/serendip-infra/core/audit"
	"github.com/serendip-infra/serendip-infra/core/id"
	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/mcp"
)

// MCPPath is where serendipd serves MCP over streamable HTTP, for hosted
// agents (spec 021 plan decision 3).
const MCPPath = "/mcp"

// MCPInterface is the audit interface of a call made through /mcp. It is
// set by the server itself, so unlike a client's declared interface it is
// authoritative.
const MCPInterface = "mcp"

// WithMCP serves Serendip's MCP tools at /mcp, for hosted agents.
func WithMCP() ServerOption {
	return func(o *serverOptions) { o.mcp = true }
}

// mcpEndpoint guards /mcp and dispatches each tool call to the /v1 API in
// process, with the caller's own credential, so RBAC, approvals and audit
// decide it exactly as for any other client and no token is ever passed
// to another service.
//
// Only an AI may use it, never a person's full authority (spec 021 scope
// item 13): an agent from an agent issuer, or a person acting through an
// AI assistant (a delegated token). API keys and a person's own token are
// refused. The console's session cookie is never a credential here.
func mcpEndpoint(v1 http.Handler, apiKeys map[string]string, authn identity.Authenticator, metadataURL func(*http.Request) string) http.Handler {
	call := func(ctx context.Context, authorization string, r catalog.Request) (mcp.Response, error) {
		// Each tool call is its own audited request: a new request ID, the
		// interface set here, and a deadline of its own.
		ctx, cancel := context.WithTimeout(ctx, mcpCallTimeout)
		defer cancel()
		ctx = audit.WithInterface(audit.WithRequestID(ctx, id.New()), MCPInterface)
		req, err := http.NewRequestWithContext(ctx, r.Method, r.Path, bytes.NewReader(r.Body))
		if err != nil {
			return mcp.Response{}, err
		}
		req.Header.Set("Authorization", authorization)
		if r.Body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := &recorder{header: http.Header{}, status: http.StatusOK}
		v1.ServeHTTP(rec, req)
		return mcp.Response{Status: rec.status, Header: rec.header, Body: rec.body.Bytes(), Size: rec.total}, nil
	}
	server := mcp.NewServer(call, mcp.Options{Version: "021"}).Handler()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := audit.RequestIDFrom(r.Context())
		challenge := func() {
			if metadataURL == nil {
				w.Header().Set("WWW-Authenticate", "Bearer")
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+metadataURL(r)+`"`)
		}
		token, ok := bearerToken(r)
		if !ok {
			challenge()
			WriteProblem(w, http.StatusUnauthorized, "unauthorized", "MCP needs an agent's bearer token", reqID)
			return
		}
		if _, isKey := apiKeys[token]; isKey {
			WriteProblem(w, http.StatusForbidden, "forbidden", "an API key can't be used through MCP: an AI acts as an agent, never with a full credential", reqID)
			return
		}
		if authn == nil {
			challenge()
			WriteProblem(w, http.StatusUnauthorized, "unauthorized", "no identity provider is configured", reqID)
			return
		}
		p, err := authn.Authenticate(r.Context(), token)
		switch {
		case errors.Is(err, reconcile.ErrProviderNotReady):
			WriteProblem(w, http.StatusServiceUnavailable, "identity provider not ready", "the bearer token can't be verified yet; retry later", reqID)
			return
		case err != nil:
			log.Printf("api: MCP bearer token rejected (request %s): %v", reqID, err)
			challenge()
			WriteProblem(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", reqID)
			return
		case p.Kind != identity.KindAgent && p.Via == "":
			// 403, not 401: the token is valid; it's the wrong kind for MCP,
			// and asking the client to sign in again wouldn't help.
			WriteProblem(w, http.StatusForbidden, "forbidden",
				"a person's own token can't be used through MCP; an AI acts for you with an agent's guardrails through serendip mcp", reqID)
			return
		}
		server.ServeHTTP(w, r)
	})
}

// resourceMetadataURL is where a client reads how to sign in: the
// console's origin when known, or the address the client used.
func resourceMetadataURL(origin string) func(*http.Request) string {
	return func(r *http.Request) string {
		if origin != "" {
			return origin + resourceMetadataPath
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		return scheme + "://" + r.Host + resourceMetadataPath
	}
}

// mcpCallTimeout bounds one tool call's in-process request.
const mcpCallTimeout = 30 * time.Second

// recorder captures an in-process /v1 answer for a tool result. It is
// minimal on purpose: no /v1 route streams (Flush is a no-op), hijacks, or
// sets headers after writing.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
	total  int
	wrote  bool
}

func (r *recorder) Flush() {}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if !r.wrote {
		r.status, r.wrote = status, true
	}
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	r.total += len(b)
	// A tool result is capped anyway (mcp.MaxResult); keep a little more,
	// so the truncation note is right, and no more.
	if room := mcp.MaxResult + 1 - r.body.Len(); room > 0 {
		if len(b) > room {
			r.body.Write(b[:room])
		} else {
			r.body.Write(b)
		}
	}
	return len(b), nil
}

// declaredInterfaces are the clients that may name themselves in
// X-Serendip-Client. The label is recorded as declared: identity, not the
// label, is the security boundary, and a client that lies only mislabels
// its own rows (spec 021 plan decision 7).
var declaredInterfaces = map[string]bool{"cli": true, "mcp": true}

// declaredInterface is the audit interface a request declares, or "".
func declaredInterface(r *http.Request) string {
	name := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Serendip-Client")))
	if !declaredInterfaces[name] {
		return ""
	}
	return name + " (declared)"
}
