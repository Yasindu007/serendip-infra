// Command serendip is Serendip's CLI (spec 021): a client of Serendip's
// API, with the same identity, permissions, approvals and audit as the
// console. Its commands come from api/catalog, so every API action is a
// command. It holds no authority of its own.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/serendip-infra/serendip-infra/api/catalog"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}

// env is the CLI's environment and the parts tests replace.
type env struct {
	getenv func(string) string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	login  loginOptions
}

func run(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWith(ctx, args, env{getenv: getenv, stdin: stdin, stdout: stdout, stderr: stderr, login: loginOptions{
		listen: loopbackAddr, openBrowser: openBrowser, in: stdin, out: stderr, timeout: 5 * time.Minute,
	}})
}

// splitGlobals takes the flags every command accepts out of args.
func splitGlobals(args []string) (globals, []string, error) {
	var g globals
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := strings.Cut(a, "=")
		takeValue := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", usageError{name + " needs a value"}
			}
			i++
			return args[i], nil
		}
		var err error
		switch name {
		case "--server":
			g.server, err = takeValue()
		case "--reason":
			g.reason, err = takeValue()
		case "--json":
			g.json = true
		case "--dry-run":
			g.dryRun = true
		case "--show-value":
			g.showValue = true
		case "--insecure":
			g.insecure = true
		case "--help", "-h":
			g.help = true
		default:
			rest = append(rest, a)
		}
		if err != nil {
			return g, nil, err
		}
	}
	return g, rest, nil
}

func runWith(ctx context.Context, args []string, e env) int {
	g, rest, err := splitGlobals(args)
	if err != nil {
		fmt.Fprintln(e.stderr, "serendip:", err)
		return exitUsage
	}
	if len(rest) == 0 || rest[0] == "help" {
		usage(e.stdout)
		if len(rest) == 0 && !g.help {
			return exitUsage
		}
		return exitOK
	}
	dir, err := configDir(e.getenv)
	if err != nil {
		fmt.Fprintln(e.stderr, "serendip:", err)
		return exitError
	}
	hc := newHTTPClient()
	stored, err := loadCredentials(dir)
	if err != nil {
		fmt.Fprintln(e.stderr, "serendip:", err)
		return exitError
	}
	server := serverAddress(g, e.getenv, stored)
	if _, err := checkURL(server, g.insecure); err != nil {
		fmt.Fprintln(e.stderr, "serendip: server:", err)
		return exitUsage
	}

	switch rest[0] {
	case "login":
		o := e.login
		o.insecure = g.insecure
		o.dir = dir
		for _, a := range rest[1:] {
			switch a {
			case "--no-browser":
				o.noBrowser = true
			case "--yes":
				o.yes = true
			default:
				fmt.Fprintf(e.stderr, "serendip: login has no %s\n", a)
				return exitUsage
			}
		}
		c, err := login(ctx, hc, server, o)
		if err != nil {
			fmt.Fprintln(e.stderr, "serendip: sign-in failed:", err)
			return exitError
		}
		unlock, err := lockCredentials(ctx, dir)
		if err != nil {
			fmt.Fprintln(e.stderr, "serendip: save the sign-in:", err)
			return exitError
		}
		err = saveCredentials(dir, c)
		unlock()
		if err != nil {
			fmt.Fprintln(e.stderr, "serendip: save the sign-in:", err)
			return exitError
		}
		fmt.Fprintf(e.stdout, "Signed in to %s.\n", server)
		return exitOK
	case "mcp":
		o, err := parseMCPArgs(rest[1:])
		if err != nil {
			fmt.Fprintln(e.stderr, "serendip:", err)
			return exitUsage
		}
		s, err := newMCPServer(ctx, server, e, hc, dir, o)
		// (newMCPServer refuses environment credentials.)
		if err != nil {
			fmt.Fprintln(e.stderr, "serendip mcp:", err)
			if errors.Is(err, errSignedOut) {
				return exitUnauthorized
			}
			return exitError
		}
		if err := s.ServeIO(ctx, io.NopCloser(e.stdin), nopCloser{e.stdout}); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
			fmt.Fprintln(e.stderr, "serendip mcp:", err)
			return exitError
		}
		return exitOK
	case "logout":
		if err := logout(ctx, hc, dir, e.stdout); err != nil {
			fmt.Fprintln(e.stderr, "serendip:", err)
			return exitError
		}
		return exitOK
	}

	op, opArgs, ok := findOperation(rest)
	if !ok {
		fmt.Fprintf(e.stderr, "serendip: unknown command %q; see serendip help\n", strings.Join(rest, " "))
		return exitUsage
	}
	if g.help {
		commandHelp(op, e.stdout)
		return exitOK
	}
	parsed, err := parseArgs(op, opArgs, e.stdin)
	if err != nil {
		fmt.Fprintln(e.stderr, "serendip:", err)
		var ue usageError
		if errors.As(err, &ue) {
			return exitUsage
		}
		return exitError
	}
	// --reason is global (it files a delete's approval request), but it's
	// also approvals request's own parameter.
	if g.reason != "" && slices.ContainsFunc(op.Params, func(p catalog.Param) bool { return p.Name == "reason" }) {
		if _, set := parsed["reason"]; !set {
			parsed["reason"] = g.reason
		}
	}
	c := &apiClient{server: server, hc: hc, clientName: "cli", bearer: bearerFrom(e.getenv, hc, dir, server)}
	return runOperation(ctx, c, op, parsed, g, e.stdout, e.stderr)
}

// serverAddress is --server, SERENDIP_SERVER, the server signed in to, or
// a local serendipd.
func serverAddress(g globals, getenv func(string) string, stored *credentials) string {
	switch {
	case g.server != "":
		return g.server
	case getenv("SERENDIP_SERVER") != "":
		return getenv("SERENDIP_SERVER")
	case stored != nil && stored.Server != "":
		return stored.Server
	default:
		return "http://localhost:8080"
	}
}

// bearerFrom picks the credential: an API key or token from the
// environment, for scripts, or the stored sign-in. Never a command-line
// argument, which the process list shows (scope item 8).
func bearerFrom(getenv func(string) string, hc *http.Client, dir, server string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if k := getenv("SERENDIP_API_KEY"); k != "" {
			return k, nil
		}
		if t := getenv("SERENDIP_TOKEN"); t != "" {
			return t, nil
		}
		return freshToken(ctx, hc, dir, server)
	}
}
