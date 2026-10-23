// Package jsonschema validates JSON values against the subset of JSON
// Schema the platform's configuration schemas use (specs/020 T008).
//
// It's deliberately small and fails closed: a schema using a keyword it
// doesn't know is refused rather than half-checked. Two choices differ
// from JSON Schema's defaults, because the platform prunes what its
// schemas don't declare:
//   - an object schema with "properties" and no "additionalProperties"
//     refuses unknown properties (a typo would otherwise vanish);
//   - "$ref" resolves only within the schema's "$defs" or "definitions".
//
// It reads OpenAPI 3.0's forms too ("nullable", and a boolean
// "exclusiveMinimum"/"exclusiveMaximum" modifying "minimum"/"maximum"),
// since the platform's schemas are openAPIV3Schema. Numbers are float64,
// so integers beyond 2^53 lose precision. $ref hops and nesting share
// one depth limit of 32.
package jsonschema

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// ErrUnsupported marks a schema this package can't check.
var ErrUnsupported = errors.New("jsonschema: unsupported schema")

// maxDepth bounds $ref chains and nesting, so a cyclic schema can't loop.
const maxDepth = 32

// annotations are keywords that describe without constraining.
var annotations = map[string]bool{
	"default": true, "description": true, "title": true, "examples": true,
	"format": true, "$schema": true, "$id": true, "$comment": true,
	"deprecated": true, "readOnly": true, "writeOnly": true, "example": true,
}

// constraints are the keywords Validate checks.
var constraints = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true,
	"enum": true, "minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true,
	"minLength": true, "maxLength": true, "pattern": true,
	"items": true, "minItems": true, "maxItems": true,
	"$ref": true, "$defs": true, "definitions": true, "nullable": true,
}

// numeric are the constraint keywords whose value is a number.
var numeric = map[string]bool{
	"minimum": true, "maximum": true, "minLength": true, "maxLength": true,
	"minItems": true, "maxItems": true,
}

// Schema is a parsed schema, ready to validate values.
type Schema struct {
	root     map[string]any
	patterns map[string]*regexp.Regexp
}

// Parse reads a schema and checks it uses only supported keywords.
func Parse(raw []byte) (*Schema, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if root == nil {
		return nil, fmt.Errorf("%w: the schema is null", ErrUnsupported)
	}
	s := &Schema{root: root, patterns: map[string]*regexp.Regexp{}}
	if err := s.check(root, "#", 0); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Schema) check(node map[string]any, at string, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: nested too deeply at %s", ErrUnsupported, at)
	}
	for k, v := range node {
		if strings.HasPrefix(k, "x-") || annotations[k] {
			continue
		}
		if !constraints[k] {
			return fmt.Errorf("%w: keyword %q at %s", ErrUnsupported, k, at)
		}
		switch k {
		case "properties", "$defs", "definitions":
			m, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("%w: %s at %s isn't an object", ErrUnsupported, k, at)
			}
			for name, sub := range m {
				if err := s.checkSub(sub, at+"/"+k+"/"+name, depth); err != nil {
					return err
				}
			}
		case "items":
			if err := s.checkSub(v, at+"/items", depth); err != nil {
				return err
			}
		case "additionalProperties":
			if _, ok := v.(bool); !ok {
				if err := s.checkSub(v, at+"/additionalProperties", depth); err != nil {
					return err
				}
			}
		case "pattern":
			p, ok := v.(string)
			if !ok {
				return fmt.Errorf("%w: pattern at %s isn't a string", ErrUnsupported, at)
			}
			re, err := regexp.Compile(p)
			if err != nil {
				return fmt.Errorf("%w: pattern at %s: %v", ErrUnsupported, at, err)
			}
			s.patterns[p] = re
		case "$ref":
			ref, ok := v.(string)
			if !ok {
				return fmt.Errorf("%w: $ref at %s isn't a string", ErrUnsupported, at)
			}
			if _, err := s.resolve(ref); err != nil {
				return err
			}
		case "type":
			if _, err := types(v); err != nil {
				return fmt.Errorf("%w: %v at %s", ErrUnsupported, err, at)
			}
		case "enum":
			if _, ok := v.([]any); !ok {
				return fmt.Errorf("%w: enum at %s isn't a list", ErrUnsupported, at)
			}
		case "required":
			list, ok := v.([]any)
			if !ok {
				return fmt.Errorf("%w: required at %s isn't a list", ErrUnsupported, at)
			}
			for _, x := range list {
				if _, ok := x.(string); !ok {
					return fmt.Errorf("%w: required at %s holds a non-string", ErrUnsupported, at)
				}
			}
		case "exclusiveMinimum", "exclusiveMaximum":
			switch v.(type) {
			case float64, bool:
			default:
				return fmt.Errorf("%w: %s at %s isn't a number or boolean", ErrUnsupported, k, at)
			}
		case "nullable":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("%w: nullable at %s isn't a boolean", ErrUnsupported, at)
			}
		default:
			if _, ok := v.(float64); numeric[k] && !ok {
				return fmt.Errorf("%w: %s at %s isn't a number", ErrUnsupported, k, at)
			}
		}
	}
	return nil
}

func (s *Schema) checkSub(v any, at string, depth int) error {
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: %s isn't a schema object", ErrUnsupported, at)
	}
	return s.check(m, at, depth+1)
}

// resolve follows a local reference: "#/$defs/Name" or "#/definitions/Name".
func (s *Schema) resolve(ref string) (map[string]any, error) {
	rest, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil, fmt.Errorf("%w: $ref %q isn't local", ErrUnsupported, ref)
	}
	// Only into the definitions, which Parse checked; anywhere else (an
	// x- extension, an example) could hold keywords never checked.
	if first, _, _ := strings.Cut(rest, "/"); first != "$defs" && first != "definitions" {
		return nil, fmt.Errorf("%w: $ref %q doesn't point into $defs or definitions", ErrUnsupported, ref)
	}
	var node any = s.root
	for part := range strings.SplitSeq(rest, "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: $ref %q doesn't resolve", ErrUnsupported, ref)
		}
		if node, ok = m[part]; !ok {
			return nil, fmt.Errorf("%w: $ref %q doesn't resolve", ErrUnsupported, ref)
		}
	}
	m, ok := node.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: $ref %q isn't a schema", ErrUnsupported, ref)
	}
	return m, nil
}

func types(v any) ([]string, error) {
	known := []string{"object", "array", "string", "integer", "number", "boolean", "null"}
	var out []string
	switch t := v.(type) {
	case string:
		out = []string{t}
	case []any:
		for _, x := range t {
			s, ok := x.(string)
			if !ok {
				return nil, errors.New("type list holds a non-string")
			}
			out = append(out, s)
		}
	default:
		return nil, errors.New("type isn't a string or list")
	}
	for _, t := range out {
		if !slices.Contains(known, t) {
			return nil, fmt.Errorf("unknown type %q", t)
		}
	}
	return out, nil
}

// Problem is one way a value breaks its schema; Path names where, such as
// "resources.limits.cpu".
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (p Problem) String() string {
	if p.Path == "" {
		return p.Message
	}
	return p.Path + ": " + p.Message
}

// Validate checks value (as decoded by encoding/json into any) and
// returns every problem, sorted by path; none means it's valid.
func (s *Schema) Validate(value any) []Problem {
	var out []Problem
	s.validate(s.root, value, "", 0, &out)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func (s *Schema) validate(node map[string]any, v any, path string, depth int, out *[]Problem) {
	add := func(format string, args ...any) {
		*out = append(*out, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if depth > maxDepth {
		add("nested too deeply")
		return
	}
	if ref, ok := node["$ref"].(string); ok {
		target, err := s.resolve(ref)
		if err != nil {
			add("the schema can't be read")
			return
		}
		s.validate(target, v, path, depth+1, out)
	}
	if v == nil && node["nullable"] == true {
		return
	}
	if t, ok := node["type"]; ok {
		want, _ := types(t)
		if !slices.ContainsFunc(want, func(w string) bool { return isType(v, w) }) {
			add("must be %s, not %s", strings.Join(want, " or "), typeOf(v))
			return
		}
	}
	if enum, ok := node["enum"].([]any); ok && !slices.ContainsFunc(enum, func(e any) bool { return equal(e, v) }) {
		var opts []string
		for _, e := range enum {
			b, _ := json.Marshal(e)
			opts = append(opts, string(b))
		}
		add("must be one of %s", strings.Join(opts, ", "))
	}
	switch x := v.(type) {
	case float64:
		s.number(node, x, add)
	case string:
		s.text(node, x, add)
	case []any:
		if n, ok := node["minItems"].(float64); ok && float64(len(x)) < n {
			add("must have at least %v items", n)
		}
		if n, ok := node["maxItems"].(float64); ok && float64(len(x)) > n {
			add("must have at most %v items", n)
		}
		if items, ok := node["items"].(map[string]any); ok {
			for i, item := range x {
				s.validate(items, item, fmt.Sprintf("%s[%d]", path, i), depth+1, out)
			}
		}
	case map[string]any:
		s.object(node, x, path, depth, out)
	}
}

func (s *Schema) number(node map[string]any, x float64, add func(string, ...any)) {
	if n, ok := node["minimum"].(float64); ok && x < n {
		add("must be at least %v", n)
	}
	if n, ok := node["maximum"].(float64); ok && x > n {
		add("must be at most %v", n)
	}
	if n, ok := node["exclusiveMinimum"].(float64); ok && x <= n {
		add("must be more than %v", n)
	}
	if n, ok := node["exclusiveMaximum"].(float64); ok && x >= n {
		add("must be less than %v", n)
	}
	// OpenAPI 3.0: a boolean makes minimum/maximum exclusive.
	if n, ok := node["minimum"].(float64); ok && node["exclusiveMinimum"] == true && x == n {
		add("must be more than %v", n)
	}
	if n, ok := node["maximum"].(float64); ok && node["exclusiveMaximum"] == true && x == n {
		add("must be less than %v", n)
	}
}

func (s *Schema) text(node map[string]any, x string, add func(string, ...any)) {
	n := utf8.RuneCountInString(x)
	if m, ok := node["minLength"].(float64); ok && float64(n) < m {
		add("must be at least %v characters", m)
	}
	if m, ok := node["maxLength"].(float64); ok && float64(n) > m {
		add("must be at most %v characters", m)
	}
	if p, ok := node["pattern"].(string); ok {
		if re := s.patterns[p]; re == nil || !re.MatchString(x) {
			add("must match %s", p)
		}
	}
}

func (s *Schema) object(node map[string]any, x map[string]any, path string, depth int, out *[]Problem) {
	props, _ := node["properties"].(map[string]any)
	if req, ok := node["required"].([]any); ok {
		for _, r := range req {
			if name, ok := r.(string); ok {
				if _, has := x[name]; !has {
					*out = append(*out, Problem{Path: join(path, name), Message: "is required"})
				}
			}
		}
	}
	extra, hasExtra := node["additionalProperties"]
	for _, name := range slices.Sorted(maps.Keys(x)) {
		if sub, ok := props[name].(map[string]any); ok {
			s.validate(sub, x[name], join(path, name), depth+1, out)
			continue
		}
		switch e := extra.(type) {
		case map[string]any:
			s.validate(e, x[name], join(path, name), depth+1, out)
		case bool:
			if !e {
				*out = append(*out, Problem{Path: join(path, name), Message: "isn't a known setting" + known(props)})
			}
		default:
			if !hasExtra && props != nil {
				*out = append(*out, Problem{Path: join(path, name), Message: "isn't a known setting" + known(props)})
			}
		}
	}
}

func known(props map[string]any) string {
	if len(props) == 0 {
		return ""
	}
	return " (known: " + strings.Join(slices.Sorted(maps.Keys(props)), ", ") + ")"
}

func isType(v any, t string) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f) && !math.IsInf(f, 0)
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	}
	return false
}

func typeOf(v any) string {
	switch x := v.(type) {
	case map[string]any:
		return "an object"
	case []any:
		return "a list"
	case string:
		return "a string"
	case float64:
		if x == math.Trunc(x) {
			return "a whole number"
		}
		return "a number"
	case bool:
		return "true or false"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}

func equal(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}
