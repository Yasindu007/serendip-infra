// Package catalog lists every action Serendip's API offers as an operation
// the CLI and the MCP server both build from (spec 021 plan decision 6).
// An operation knows its route, its inputs and how to turn them into an
// HTTP request; it holds no authority of its own. Every decision stays the
// API's.
//
// A test in package api checks the catalogue against the route table:
// every route is covered by an operation on each surface, or excluded from
// that surface with a reason.
package catalog

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// In is where a parameter goes in the request.
type In int

const (
	// InPath fills the {name} placeholder in the operation's path.
	InPath In = iota
	// InQuery becomes a query parameter.
	InQuery
	// InLabels is an object whose entries become label.<key>=<value> query
	// parameters.
	InLabels
	// InField becomes a top-level field of a JSON object body.
	InField
	// InBody is the whole JSON body.
	InBody
)

// Type is a parameter's JSON type.
type Type string

const (
	String  Type = "string"
	Boolean Type = "boolean"
	Integer Type = "integer"
	Object  Type = "object"
)

// Param is one input of an operation.
type Param struct {
	Name        string
	In          In
	Type        Type // String when empty
	Required    bool
	Description string
	// Enum lists the allowed values of a string parameter.
	Enum []string
	// Max bounds an integer parameter, when non-zero.
	Max int
	// Expand marks a path parameter whose values name separate routes
	// (/v1/{kind}/... for each kind in Enum).
	Expand bool
	// CLIOnly leaves the parameter off MCP, saying why: an MCP tool
	// answers once, so a streaming option has no place there.
	CLIOnly string
}

func (p Param) typ() Type { return cmp.Or(p.Type, String) }

// Hints describe an operation to MCP clients (tool annotations). They are
// hints a client may show, never a safety boundary.
type Hints struct {
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
}

// Operation is one action of the API. Operations returned by this package
// are shared: treat them as read-only.
type Operation struct {
	// Name is the MCP tool name and the operation's stable ID.
	Name string
	// CLI is the command's words, such as ["applications", "list"]; nil
	// when the operation isn't on the CLI.
	CLI []string
	// MCP says whether the operation is an MCP tool.
	MCP bool
	// Summary is one line, for CLI help and the tool description.
	Summary string
	Method  string
	// Path is the request path, with {name} placeholders for InPath
	// parameters.
	Path   string
	Params []Param
	Hints  Hints
	// SecretValue marks an operation that reads or writes a Secret's
	// value: an MCP server can leave these out altogether.
	SecretValue bool
	// body builds the JSON body when fields alone can't express it. Its
	// args have been checked by Request already.
	body func(args map[string]any) (any, error)
}

// Request is the HTTP request an operation's arguments make.
type Request struct {
	Method string
	// Path includes the encoded query, if any.
	Path string
	// Body is JSON, or nil.
	Body []byte
}

// Request builds the HTTP request for args, checking each argument against
// its parameter and refusing unknown ones. A nil argument is absent.
func (o Operation) Request(args map[string]any) (Request, error) {
	present := map[string]any{}
	for name, v := range args {
		if !slices.ContainsFunc(o.Params, func(p Param) bool { return p.Name == name }) {
			return Request{}, fmt.Errorf("%s: unknown parameter %q", o.Name, name)
		}
		if v != nil {
			present[name] = v
		}
	}
	path := o.Path
	query := url.Values{}
	fields := map[string]any{}
	var whole any
	for _, p := range o.Params {
		v, ok := present[p.Name]
		if !ok {
			if p.Required {
				return Request{}, fmt.Errorf("%s: %s is required", o.Name, p.Name)
			}
			continue
		}
		if err := p.check(v); err != nil {
			return Request{}, fmt.Errorf("%s: %s %w", o.Name, p.Name, err)
		}
		switch p.In {
		case InPath:
			s, _ := scalar(v)
			if s == "" || s == "." || s == ".." || strings.ContainsFunc(s, unicode.IsControl) {
				return Request{}, fmt.Errorf("%s: %s must be a non-empty name, not %q", o.Name, p.Name, s)
			}
			path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(s))
		case InQuery:
			s, _ := scalar(v)
			query.Set(p.Name, s)
		case InLabels:
			for k, lv := range v.(map[string]any) {
				s, ok := lv.(string)
				if !ok || k == "" {
					return Request{}, fmt.Errorf("%s: %s must map names to strings", o.Name, p.Name)
				}
				query.Set("label."+k, s)
			}
		case InField:
			fields[p.Name] = v
		case InBody:
			whole = v
		}
	}
	var body any
	switch {
	case o.body != nil:
		b, err := o.body(present)
		if err != nil {
			return Request{}, fmt.Errorf("%s: %w", o.Name, err)
		}
		body = b
	case whole != nil:
		body = whole
	case len(fields) > 0:
		body = fields
	}
	req := Request{Method: o.Method, Path: path}
	if len(query) > 0 {
		req.Path += "?" + query.Encode()
	}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return Request{}, fmt.Errorf("%s: body: %w", o.Name, err)
		}
		req.Body = b
	}
	return req, nil
}

// check reports whether v fits the parameter's type, enum and bound. The
// error reads after the parameter's name.
func (p Param) check(v any) error {
	switch p.typ() {
	case String:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("must be a string, not %T", v)
		}
		if p.Enum != nil && !slices.Contains(p.Enum, s) {
			return fmt.Errorf("must be one of %s", strings.Join(p.Enum, ", "))
		}
	case Boolean:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("must be a boolean, not %T", v)
		}
	case Integer:
		s, err := scalar(v)
		n, perr := strconv.ParseInt(s, 10, 64)
		if err != nil || perr != nil {
			return fmt.Errorf("must be a whole number")
		}
		if n < 1 || (p.Max > 0 && n > int64(p.Max)) {
			return fmt.Errorf("must be from 1 to %d", p.Max)
		}
	case Object:
		if _, ok := v.(map[string]any); !ok {
			return fmt.Errorf("must be an object, not %T", v)
		}
	}
	return nil
}

// scalar renders a path or query value: numbers in plain decimal, never
// exponent form (JSON numbers arrive as float64).
func scalar(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case json.Number:
		return t.String(), nil
	default:
		return "", fmt.Errorf("want a string, number or boolean, got %T", v)
	}
}

// ForMCP is the operation as an MCP tool offers it: without CLI-only
// parameters, so neither its schema nor its requests can use them.
func (o Operation) ForMCP() Operation {
	o.Params = slices.DeleteFunc(slices.Clone(o.Params), func(p Param) bool { return p.CLIOnly != "" })
	return o
}

// InputSchema is the JSON Schema of the operation's arguments, an object
// with one property per parameter. Each call builds a new map.
func (o Operation) InputSchema() map[string]any {
	props := map[string]any{}
	var required []string
	for _, p := range o.Params {
		s := map[string]any{"type": string(p.typ())}
		if p.Enum != nil {
			s["enum"] = slices.Clone(p.Enum)
		}
		if p.typ() == Integer {
			s["minimum"] = 1
			if p.Max > 0 {
				s["maximum"] = p.Max
			}
		}
		if p.In == InLabels {
			s["additionalProperties"] = map[string]any{"type": "string"}
		}
		if p.Description != "" {
			s["description"] = p.Description
		}
		props[p.Name] = s
		if p.Required {
			required = append(required, p.Name)
		}
	}
	out := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// Routes are the route patterns ("METHOD /path") the operation covers, in
// the route table's spelling: an expanded parameter names one route per
// value, and every other placeholder stays as written.
func (o Operation) Routes() []string {
	paths := []string{o.Path}
	for _, p := range o.Params {
		if !p.Expand {
			continue
		}
		var next []string
		for _, path := range paths {
			for _, v := range p.Enum {
				next = append(next, strings.ReplaceAll(path, "{"+p.Name+"}", v))
			}
		}
		paths = next
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = o.Method + " " + p
	}
	slices.Sort(out)
	return out
}

// Exclusion says why a route isn't on a surface. An empty reason means the
// route is on that surface.
type Exclusion struct {
	CLI string
	MCP string
}

// Operations returns every operation, sorted by name. The slice is the
// caller's; the operations in it are shared and read-only.
func Operations() []Operation {
	return slices.Clone(sorted)
}

// Excluded returns the routes left off a surface on purpose, with why.
func Excluded() map[string]Exclusion {
	return maps.Clone(excluded)
}

// Lookup finds an operation by name.
func Lookup(name string) (Operation, bool) {
	o, ok := byName[name]
	return o, ok
}

var (
	sorted = func() []Operation {
		out := slices.Clone(operations)
		slices.SortFunc(out, func(a, b Operation) int { return cmp.Compare(a.Name, b.Name) })
		return out
	}()
	byName = func() map[string]Operation {
		m := make(map[string]Operation, len(operations))
		for _, o := range operations {
			m[o.Name] = o
		}
		return m
	}()
)
