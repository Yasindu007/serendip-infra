package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/api/catalog"
	"github.com/serendip-infra/serendip-infra/mcp"
)

const tokenExchangeGrant = "urn:ietf:params:oauth:grant-type:token-exchange"

// delegation keeps a token exchanged for the assistant client: the person
// signed in to the CLI, acting through an AI assistant (spec 021 plan
// decision 1). Serendip decides its calls with the person's roles and an
// agent's guardrails. Exchanged tokens are short-lived and come without a
// refresh token, so it exchanges again shortly before each one expires;
// after the person signs out the exchange is refused, so the assistant
// stops within one token's life.
type delegation struct {
	hc     *http.Client
	dir    string
	server string

	mu      sync.Mutex
	token   string
	expires time.Time
}

func (d *delegation) bearer(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.token != "" && time.Until(d.expires) > 30*time.Second {
		return d.token, nil
	}
	c, err := loadCredentials(d.dir)
	if err != nil {
		return "", err
	}
	if err := usable(c, d.server); err != nil {
		return "", err
	}
	if c.AssistantID == "" {
		return "", errors.New("this server names no assistant client; sign in again with serendip login")
	}
	subject, err := freshToken(ctx, d.hc, d.dir, d.server)
	if err != nil {
		return "", err
	}
	t, err := tokenRequest(ctx, d.hc, c.TokenURL, url.Values{
		"grant_type": {tokenExchangeGrant}, "client_id": {c.AssistantID},
		"subject_token": {subject}, "subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
	})
	if err != nil {
		return "", fmt.Errorf("act for the person through the assistant client: %w", err)
	}
	d.token = t.AccessToken
	life := time.Duration(t.ExpiresIn) * time.Second
	if life <= 0 {
		life = time.Minute
	}
	d.expires = time.Now().Add(life)
	return d.token, nil
}

// mcpOptions are serendip mcp's flags.
type mcpOptions struct {
	noSecretValues bool
}

func parseMCPArgs(args []string) (mcpOptions, error) {
	var o mcpOptions
	for _, a := range args {
		switch a {
		case "--no-secret-values":
			o.noSecretValues = true
		default:
			return o, usageError{fmt.Sprintf("mcp has no %s", a)}
		}
	}
	return o, nil
}

// newMCPServer builds the stdio MCP server for an AI assistant on the
// person's machine. It refuses environment credentials: an API key or a
// token there would give the assistant a full credential of its own, and
// an AI is never anyone's full authority (spec 021 scope item 13). Hosted
// agents with their own tokens use serendipd's /mcp instead.
func newMCPServer(ctx context.Context, server string, e env, hc *http.Client, dir string, o mcpOptions) (*mcp.Server, error) {
	if e.getenv("SERENDIP_API_KEY") != "" || e.getenv("SERENDIP_TOKEN") != "" {
		return nil, errors.New("serendip mcp acts for the person signed in to the CLI; unset SERENDIP_API_KEY and SERENDIP_TOKEN (hosted agents use serendipd's /mcp)")
	}
	d := &delegation{hc: hc, dir: dir, server: server}
	// Fail at start, not at the first tool call, when the exchange can't work.
	if _, err := d.bearer(ctx); err != nil {
		return nil, err
	}
	api := &apiClient{server: server, hc: hc, clientName: "mcp", bearer: d.bearer}
	call := func(ctx context.Context, _ string, r catalog.Request) (mcp.Response, error) {
		resp, err := api.do(ctx, r)
		if err != nil {
			return mcp.Response{}, err
		}
		return mcp.Response{Status: resp.Status, Header: resp.Header, Body: resp.Body}, nil
	}
	// Standard output carries the protocol; logs go to standard error.
	logger := slog.New(slog.NewTextHandler(e.stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return mcp.NewServer(call, mcp.Options{Version: "021", NoSecretValues: o.noSecretValues, Logger: logger}), nil
}

// nopCloser lets standard output serve as the transport's writer without
// the transport closing it.
type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
