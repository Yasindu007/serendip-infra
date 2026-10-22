package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/serendip-infra/serendip-infra/api/catalog"
)

// Exit codes (spec 021 scope item 9): scripts can tell why a command
// failed without parsing text.
const (
	exitOK            = 0
	exitError         = 1 // anything else: a network failure, a bad answer
	exitUsage         = 2
	exitDenied        = 3
	exitNeedsApproval = 4
	exitNotFound      = 5
	exitConflict      = 6
	exitServer        = 7
	exitUnauthorized  = 8
)

// needsApproval is the API's problem type for a request that waits for a
// person (api.ProblemNeedsApproval).
const needsApproval = "urn:serendip:problem:needs-approval"

// globals are the flags every command accepts.
type globals struct {
	server    string
	json      bool
	dryRun    bool
	showValue bool
	insecure  bool
	reason    string
	help      bool
}

// findOperation matches the leading words of args to the catalogue
// operation with the longest CLI command, and returns the rest.
func findOperation(args []string) (catalog.Operation, []string, bool) {
	var best catalog.Operation
	n := 0
	for _, op := range catalog.Operations() {
		if op.CLI == nil || len(op.CLI) > len(args) || len(op.CLI) <= n {
			continue
		}
		if slices.Equal(op.CLI, args[:len(op.CLI)]) {
			best, n = op, len(op.CLI)
		}
	}
	return best, args[n:], n > 0
}

// usageError is a mistake in how a command was called.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// parseArgs turns a command's remaining arguments into the operation's
// arguments: path parameters by position, in order (or as flags), and
// every other parameter as --name value (--name=value; a bare --name for a
// boolean). --file names the JSON body document; --file - reads it from
// standard input.
func parseArgs(op catalog.Operation, rest []string, stdin io.Reader) (map[string]any, error) {
	args := map[string]any{}
	var path []catalog.Param
	byName := map[string]catalog.Param{}
	body := ""
	for _, p := range op.Params {
		if p.In == catalog.InPath {
			path = append(path, p)
		}
		byName[p.Name] = p
		if p.In == catalog.InBody {
			body = p.Name
		}
	}
	positional := 0
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "--") {
			for positional < len(path) && args[path[positional].Name] != nil {
				positional++ // given as a flag
			}
			if positional >= len(path) {
				return nil, usageError{fmt.Sprintf("unexpected argument %q", a)}
			}
			args[path[positional].Name] = a
			positional++
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		file := name == "file" && body != ""
		if file {
			name = body
		}
		p, ok := byName[name]
		if !ok {
			return nil, usageError{fmt.Sprintf("%s has no --%s", strings.Join(op.CLI, " "), name)}
		}
		if _, dup := args[name]; dup {
			return nil, usageError{fmt.Sprintf("--%s is given twice", name)}
		}
		if p.Type == catalog.Boolean && !hasValue {
			args[name] = true
			continue
		}
		if !hasValue {
			if i+1 >= len(rest) {
				return nil, usageError{fmt.Sprintf("--%s needs a value", name)}
			}
			i++
			value = rest[i]
		}
		if file && value != "-" {
			value = "@" + value
		}
		v, err := paramValue(op, p, value, stdin)
		if err != nil {
			return nil, err
		}
		args[name] = v
	}
	for _, p := range path {
		if args[p.Name] == nil {
			return nil, usageError{fmt.Sprintf("missing <%s>", p.Name)}
		}
	}
	return args, nil
}

// paramValue converts one flag's text to the parameter's type. An object
// is JSON, @file, or - for standard input; a label or data object may also
// be name=value pairs separated by commas. A Secret's value is never taken
// from the command line, where the process list shows it: only from a file
// or standard input.
func paramValue(op catalog.Operation, p catalog.Param, value string, stdin io.Reader) (any, error) {
	switch p.Type {
	case catalog.Boolean:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return nil, usageError{fmt.Sprintf("--%s: want true or false", p.Name)}
		}
		return b, nil
	case catalog.Integer:
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, usageError{fmt.Sprintf("--%s: want a whole number", p.Name)}
		}
		return n, nil
	case catalog.Object:
		var raw []byte
		switch {
		case value == "-":
			b, err := io.ReadAll(io.LimitReader(stdin, maxBody))
			if err != nil {
				return nil, err
			}
			raw = b
		case strings.HasPrefix(value, "@"):
			b, err := os.ReadFile(value[1:])
			if err != nil {
				return nil, usageError{fmt.Sprintf("--%s: %v", p.Name, err)}
			}
			raw = b
		case op.SecretValue && p.Name == "data":
			return nil, usageError{fmt.Sprintf("--%s: a Secret's value is read from a file (@path) or standard input (-), never the command line", p.Name)}
		case strings.HasPrefix(strings.TrimSpace(value), "{"):
			raw = []byte(value)
		default:
			pairs := map[string]any{}
			for _, kv := range strings.Split(value, ",") {
				k, v, ok := strings.Cut(kv, "=")
				if !ok || k == "" {
					return nil, usageError{fmt.Sprintf("--%s: want JSON, @file, - or name=value pairs", p.Name)}
				}
				pairs[k] = v
			}
			return pairs, nil
		}
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, usageError{fmt.Sprintf("--%s: not a JSON object: %v", p.Name, err)}
		}
		return v, nil
	default:
		return value, nil
	}
}

// runOperation sends one catalogue operation and prints the answer,
// returning the exit code.
func runOperation(ctx context.Context, c *apiClient, op catalog.Operation, args map[string]any, g globals, stdout, stderr io.Writer) int {
	req, err := op.Request(args)
	if err != nil {
		fmt.Fprintln(stderr, "serendip:", err)
		return exitUsage
	}
	if g.dryRun {
		// The console's "Same request through the API" (scope item 10).
		fmt.Fprintf(stdout, "%s %s%s\n", req.Method, strings.TrimSuffix(c.server, "/"), req.Path)
		if req.Body != nil {
			var pretty bytes.Buffer
			if op.SecretValue && !g.showValue {
				fmt.Fprintln(stdout, "(body with the Secret's value, hidden; add --show-value to print it)")
			} else if json.Indent(&pretty, req.Body, "", "  ") == nil {
				fmt.Fprintln(stdout, pretty.String())
			}
		}
		return exitOK
	}
	resp, err := c.do(ctx, req)
	if err != nil {
		fmt.Fprintln(stderr, "serendip:", err)
		if errors.Is(err, errSignedOut) {
			return exitUnauthorized
		}
		return exitError
	}
	if resp.Status >= 200 && resp.Status < 300 {
		printSuccess(op, resp, g, stdout, stderr)
		return exitOK
	}
	return printFailure(ctx, c, op, args, resp, g, stdout, stderr)
}

func printSuccess(op catalog.Operation, resp response, g globals, stdout, stderr io.Writer) {
	body := bytes.TrimSpace(resp.Body)
	if op.SecretValue && !g.showValue && len(body) > 0 {
		body = hideValues(body)
	}
	switch {
	case len(body) == 0:
		if !g.json {
			fmt.Fprintln(stdout, "Done.")
		}
	case g.json:
		var pretty bytes.Buffer
		if json.Indent(&pretty, body, "", "  ") == nil {
			fmt.Fprintln(stdout, pretty.String())
		} else {
			stdout.Write(append(body, '\n'))
		}
	default:
		printHuman(body, stdout)
	}
	if id := resp.Header.Get("Serendip-Approval-Id"); id != "" {
		fmt.Fprintf(stderr, "Recorded as approval %s.\n", id)
	}
	if next := resp.Header.Get("Serendip-Next-After"); next != "" {
		fmt.Fprintf(stderr, "More results: add --after %s\n", next)
	}
	if op.SecretValue && !g.showValue && op.Hints.ReadOnly {
		fmt.Fprintln(stderr, "Values hidden; add --show-value to print them.")
	}
}

// hideValues replaces a Secret value answer's values, keeping the keys.
func hideValues(body []byte) []byte {
	var v struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal(body, &v) != nil || v.Data == nil {
		return []byte(`"(hidden)"`)
	}
	for k := range v.Data {
		v.Data[k] = "(hidden)"
	}
	b, _ := json.Marshal(v)
	return b
}

// printHuman prints a list of resources as a table, and anything else as
// indented JSON.
func printHuman(body []byte, out io.Writer) {
	var list []map[string]any
	if json.Unmarshal(body, &list) == nil {
		if len(list) == 0 {
			fmt.Fprintln(out, "(none)")
			return
		}
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tSTATUS")
		for _, item := range list {
			id, name, status := describe(item)
			fmt.Fprintf(tw, "%s\t%s\t%s\n", id, name, status)
		}
		tw.Flush()
		return
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, body, "", "  ") == nil {
		fmt.Fprintln(out, pretty.String())
		return
	}
	out.Write(append(body, '\n'))
}

// describe picks a list row's ID, name and status from the shapes the API
// returns: resources (metadata, status.phase or a Ready condition),
// approvals (the action), role definitions and assignments.
func describe(item map[string]any) (id, name, status string) {
	str := func(m map[string]any, keys ...string) string {
		var cur any = m
		for _, k := range keys {
			mm, ok := cur.(map[string]any)
			if !ok {
				return ""
			}
			cur = mm[k]
		}
		s, _ := cur.(string)
		return s
	}
	id = cmpOr(str(item, "metadata", "id"), str(item, "id"))
	name = cmpOr(str(item, "metadata", "name"), str(item, "roleName"), str(item, "name"))
	if verb := str(item, "spec", "action", "verb"); verb != "" {
		name = fmt.Sprintf("%s %s/%s", verb, str(item, "spec", "action", "target", "kind"), str(item, "spec", "action", "target", "id"))
	}
	status = str(item, "status", "phase")
	if st, _ := item["status"].(map[string]any); status == "" && st != nil {
		if conds, ok := st["conditions"].([]any); ok {
			for _, c := range conds {
				if m, ok := c.(map[string]any); ok && m["type"] == "Ready" {
					status = "Ready=" + fmt.Sprint(m["status"])
				}
			}
		}
	}
	return id, name, status
}

func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// problem is the API's RFC 7807 answer.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

func printFailure(ctx context.Context, c *apiClient, op catalog.Operation, args map[string]any, resp response, g globals, stdout, stderr io.Writer) int {
	var p problem
	_ = json.Unmarshal(resp.Body, &p)
	if g.json {
		stdout.Write(append(bytes.TrimSpace(resp.Body), '\n'))
	} else if p.Title != "" || p.Detail != "" {
		fmt.Fprintf(stderr, "serendip: %s: %s\n", clean(p.Title), clean(p.Detail))
	} else {
		fmt.Fprintf(stderr, "serendip: HTTP %d\n", resp.Status)
	}
	switch {
	case p.Type == needsApproval:
		return fileApproval(ctx, c, op, args, g, stdout, stderr)
	case resp.Status == 401:
		fmt.Fprintln(stderr, "Sign in with serendip login, or set SERENDIP_API_KEY or SERENDIP_TOKEN.")
		return exitUnauthorized
	case resp.Status == 403:
		return exitDenied
	case resp.Status == 404:
		return exitNotFound
	case resp.Status == 409 || resp.Status == 412:
		return exitConflict
	case resp.Status >= 500:
		return exitServer
	default:
		return exitError
	}
}

// fileApproval handles a delete that waits for a person (scope item 11):
// with --reason it files the request, otherwise it says how to.
func fileApproval(ctx context.Context, c *apiClient, op catalog.Operation, args map[string]any, g globals, stdout, stderr io.Writer) int {
	kind := kindOf(op)
	id, _ := args["id"].(string)
	cascade, _ := args["cascade"].(bool)
	if op.Method != "DELETE" || kind == "" || id == "" {
		return exitNeedsApproval
	}
	if g.reason == "" {
		cmd := fmt.Sprintf("serendip approvals request --kind %s --id %s", kind, id)
		if cascade {
			cmd += " --cascade"
		}
		fmt.Fprintf(stderr, "To ask a person to approve it:\n  %s --reason \"why\"\nor run this delete again with --reason \"why\".\n", cmd)
		return exitNeedsApproval
	}
	req, _ := catalog.Lookup("approvals_request")
	r, err := req.Request(map[string]any{"kind": kind, "id": id, "cascade": cascade, "reason": g.reason})
	if err != nil {
		fmt.Fprintln(stderr, "serendip:", err)
		return exitError
	}
	resp, err := c.do(ctx, r)
	if err != nil {
		fmt.Fprintln(stderr, "serendip: file the approval request:", err)
		return exitError
	}
	if resp.Status != 201 && resp.Status != 200 {
		var p problem
		_ = json.Unmarshal(resp.Body, &p)
		fmt.Fprintf(stderr, "serendip: file the approval request: %s: %s\n", clean(p.Title), clean(p.Detail))
		return exitError
	}
	var a struct {
		Metadata struct {
			ID string `json:"id"`
		} `json:"metadata"`
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
	}
	_ = json.Unmarshal(resp.Body, &a)
	if g.json {
		stdout.Write(append(bytes.TrimSpace(resp.Body), '\n'))
	}
	fmt.Fprintf(stderr, "Approval %s filed (%s); a person decides it. Follow it with: serendip approvals get %s\n", a.Metadata.ID, a.Status.Phase, a.Metadata.ID)
	return exitNeedsApproval
}

// kindOf is the resource kind a delete operation acts on.
func kindOf(op catalog.Operation) string {
	path := strings.TrimSuffix(strings.TrimPrefix(op.Path, "/v1/"), "/{id}")
	for kind, p := range catalog.KindPaths() {
		if p == path {
			return kind
		}
	}
	return ""
}

// usage lists the commands, grouped by their first word.
func usage(out io.Writer) {
	fmt.Fprint(out, `Usage: serendip [flags] <command> [arguments] [--parameter value ...]

Sign in:
  login [--no-browser]    sign in through the identity provider
  logout                  sign out, revoking the session
  whoami                  who you are signed in as

AI assistants:
  mcp [--no-secret-values]  serve Serendip's MCP tools on stdio, acting for you with an agent's guardrails

Commands:
`)
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, op := range catalog.Operations() {
		if op.CLI == nil || op.Name == "session_get" {
			continue
		}
		fmt.Fprintf(tw, "  %s\t%s\n", strings.Join(append(slices.Clone(op.CLI), pathArgs(op)...), " "), op.Summary)
	}
	tw.Flush()
	fmt.Fprint(out, `
Flags:
  --server URL     Serendip's address (or SERENDIP_SERVER; default: the one you signed in to)
  --json           print the API's JSON
  --dry-run        print the request instead of sending it
  --show-value     print Secret values (hidden by default)
  --reason TEXT    on a delete that needs a person's approval, file the request with this reason
                   (the exit code is still 4: the delete waits for the person)
  --insecure       allow plain http to another machine (credentials travel in clear text)
  --help           a command's parameters

Credentials: serendip login, or SERENDIP_API_KEY or SERENDIP_TOKEN in the environment
(never on the command line).

Exit codes: 0 ok, 1 error, 2 usage, 3 denied, 4 needs approval, 5 not found,
6 conflict, 7 server error, 8 not signed in.
`)
}

func pathArgs(op catalog.Operation) []string {
	var out []string
	for _, p := range op.Params {
		if p.In == catalog.InPath {
			out = append(out, "<"+p.Name+">")
		}
	}
	return out
}

// commandHelp describes one command's parameters.
func commandHelp(op catalog.Operation, out io.Writer) {
	fmt.Fprintf(out, "serendip %s\n\n%s.\n\n", strings.Join(append(slices.Clone(op.CLI), pathArgs(op)...), " "), op.Summary)
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, p := range op.Params {
		if p.In == catalog.InPath {
			fmt.Fprintf(tw, "  <%s>\t%s\n", p.Name, p.Description)
			continue
		}
		flag := "--" + p.Name
		switch p.Type {
		case catalog.Object:
			flag += " JSON|@file|-"
		case catalog.Boolean:
		case catalog.Integer:
			flag += " N"
		default:
			flag += " VALUE"
		}
		req := ""
		if p.Required {
			req = " (required)"
		}
		desc := p.Description
		if len(p.Enum) > 0 {
			desc += ": " + strings.Join(p.Enum, ", ")
		}
		fmt.Fprintf(tw, "  %s\t%s%s\n", flag, desc, req)
	}
	tw.Flush()
	fmt.Fprintf(out, "\nAPI: %s %s\n", op.Method, op.Path)
}
