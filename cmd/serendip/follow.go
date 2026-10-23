package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/serendip-infra/serendip-infra/api/catalog"
)

// maxStreamLine is the longest line a followed stream may send: the
// server cuts log lines at 16 KiB, and JSON escaping can grow them.
const maxStreamLine = 1 << 20

// streamSilence is how long a followed stream may send nothing before the
// CLI gives up on it: longer than the server's own idle end (5 minutes),
// so only a connection that has silently died hits it.
const streamSilence = 6 * time.Minute

// safe makes text from a workload fit to print on a terminal: control
// characters (escape sequences, carriage returns, bells, C1 codes) are
// shown as \xNN instead of acted on, so a log line can't move the
// cursor, retitle the window, write the clipboard, or fake the CLI's own
// output. Tabs stay.
func safe(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r != '\t' && unicode.IsControl(r) }) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r != '\t' && unicode.IsControl(r) {
			fmt.Fprintf(&b, "\\x%02x", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// following reports whether args ask op to stream (spec 020: logs
// --follow).
func following(op catalog.Operation, args map[string]any) bool {
	for _, p := range op.Params {
		if p.Name == "follow" && p.CLIOnly != "" {
			return args["follow"] == true
		}
	}
	return false
}

// stream sends r and hands back the open answer. Unlike do, it has no
// overall time limit and doesn't read the body: a followed stream lasts
// until the server ends it or ctx (Ctrl-C) does. The token is checked
// when the stream starts; a stream lasts at most 10 minutes, so it isn't
// refreshed during one.
func (c *apiClient) stream(ctx context.Context, r catalog.Request) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, r.Method, strings.TrimSuffix(c.server, "/")+r.Path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/x-ndjson")
	req.Header.Set("X-Serendip-Client", c.clientName)
	tok, err := c.bearer(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	hc := &http.Client{Transport: c.hc.Transport, CheckRedirect: c.hc.CheckRedirect}
	return hc.Do(req)
}

// logLine is one followed line, as the API sends it; End is set on the
// last object, saying why the stream ended.
type logLine struct {
	Time     time.Time `json:"time"`
	Instance string    `json:"instance"`
	Line     string    `json:"line"`
	End      string    `json:"end"`
}

// endReasons say what a stream's end means to the person following it.
var endReasons = map[string]string{
	"idle":        "no new lines for a while; run it again to keep following",
	"maxDuration": "streams last at most 10 minutes; run it again to keep following",
	"ended":       "the instances stopped writing",
	"error":       "the platform broke off the stream; some lines may be missing",
}

// runFollow streams op's answer, printing each line as it comes: as
// "time instance line", or the raw NDJSON with --json.
func runFollow(ctx context.Context, c *apiClient, op catalog.Operation, args map[string]any, g globals, stdout, stderr io.Writer) int {
	req, err := op.Request(args)
	if err != nil {
		fmt.Fprintln(stderr, "serendip:", err)
		return exitUsage
	}
	if g.dryRun {
		fmt.Fprintf(stdout, "%s %s%s\n", req.Method, strings.TrimSuffix(c.server, "/"), req.Path)
		return exitOK
	}
	// A watchdog ends a stream that has gone silent for longer than the
	// server ever lets one idle: the connection died without closing.
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var silent atomic.Bool
	watchdog := time.AfterFunc(streamSilence, func() { silent.Store(true); cancel() })
	defer watchdog.Stop()
	resp, err := c.stream(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return exitOK // interrupted before it started
		}
		fmt.Fprintln(stderr, "serendip:", err)
		if errors.Is(err, errSignedOut) {
			return exitUnauthorized
		}
		return exitError
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, err := readBounded(resp.Body)
		if err != nil {
			fmt.Fprintln(stderr, "serendip:", err)
			return exitError
		}
		return printFailure(ctx, c, op, args, response{Status: resp.StatusCode, Header: resp.Header, Body: body}, g, stdout, stderr)
	}
	lines := bufio.NewScanner(resp.Body)
	lines.Buffer(make([]byte, 64<<10), maxStreamLine)
	for lines.Scan() {
		watchdog.Reset(streamSilence)
		raw := bytes.TrimSpace(lines.Bytes())
		if len(raw) == 0 {
			continue
		}
		var l logLine
		if err := json.Unmarshal(raw, &l); err != nil {
			fmt.Fprintln(stderr, "serendip: the server sent something that isn't a log line")
			return exitError
		}
		if l.End != "" {
			why, ok := endReasons[l.End]
			if !ok {
				why = safe(l.End)
			}
			fmt.Fprintf(stderr, "Stream ended: %s.\n", why)
			if l.End == "error" {
				return exitError
			}
			return exitOK
		}
		if g.json {
			stdout.Write(append(raw, '\n'))
			continue
		}
		when := "-"
		if !l.Time.IsZero() {
			when = l.Time.Local().Format(time.RFC3339)
		}
		fmt.Fprintf(stdout, "%s %s %s\n", when, safe(l.Instance), safe(l.Line))
	}
	if !watchdog.Stop() && silent.Load() {
		fmt.Fprintf(stderr, "serendip: nothing came for %s; the connection seems to have died. Some lines may be missing.\n", streamSilence)
		return exitError
	}
	if parent.Err() != nil {
		return exitOK // Ctrl-C: the person stopped following
	}
	if err := lines.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			fmt.Fprintln(stderr, "serendip: the server sent a log line longer than 1 MiB; stopped")
		} else {
			fmt.Fprintln(stderr, "serendip: the stream broke off:", err)
		}
		return exitError
	}
	fmt.Fprintln(stderr, "serendip: the stream ended without saying why; some lines may be missing")
	return exitError
}
