// Package template renders template versions (specs/027-templates-and-
// scaffolder, ADR 0025) into Components. Rendering is substitution only:
// parameter values replace placeholders on the decoded JSON tree, and are
// never parsed again (plan decision 3).
package template

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/serendip-infra/serendip-infra/core/jsonschema"
	"github.com/serendip-infra/serendip-infra/core/resource"
)

// Component is one rendered Component: its template-local name, which
// becomes its resource name, and its spec, whose dependencies still name
// other Components by local name (the caller maps them to IDs).
type Component struct {
	Name string
	Spec resource.ComponentSpec
}

// Rendered is a version rendered with one set of parameters.
type Rendered struct {
	// Requires names the optional packages the Components need.
	Requires []string
	// Components are in creation order: each after the ones it depends on.
	Components []Component
	// Parameters are the values used, defaults included. An optional
	// parameter given no value and no default isn't here.
	Parameters map[string]any
}

// Render checks params against v's schema (every problem, after the
// defaults are applied), then renders v's body with them. Errors name
// their field: "parameters.port", "components[web].ports[0].port".
func Render(v resource.TemplateVersionSpec, params map[string]any) (Rendered, resource.FieldErrors) {
	defs, errs := resource.TemplateParams(v.Parameters)
	if errs != nil {
		return Rendered{}, errs
	}
	values, errs := resolve(v.Parameters, defs, params)
	if errs != nil {
		return Rendered{}, errs
	}
	return render(v, defs, values, "")
}

// ValidatePublish checks a version as it's published: the checks of
// resource.ValidateTemplateVersion, then its body rendered twice, so a
// version that could never make valid Components is refused now rather
// than when it's first used. Each rendering gives the parameters their
// defaults; required ones without a default get a stand-in (standIn).
// The first leaves the other optional parameters out, and the second
// fills them with stand-ins too, so the fields they fill are checked.
func ValidatePublish(m resource.Metadata, v resource.TemplateVersionSpec) error {
	if err := resource.ValidateTemplateVersion(m, v); err != nil {
		return err
	}
	defs, _ := resource.TemplateParams(v.Parameters)
	// A stand-in that breaks its own schema would check the body with a
	// value no user could give.
	var errs resource.FieldErrors
	for _, p := range defs {
		if p.Default == nil && len(p.Examples) == 0 && !fits(p, standIn(p)) {
			errs = append(errs, resource.FieldError{Field: "parameters." + p.Name + ".examples",
				Detail: "needed: the body is checked at publish with this parameter's first example, and the built-in stand-in doesn't fit its schema"})
		}
	}
	if errs != nil {
		return errs
	}
	for _, all := range []bool{false, true} {
		values := map[string]any{}
		var stood []string
		for _, p := range defs {
			switch {
			case p.Default != nil:
				values[p.Name] = p.Default
			case p.Required || all:
				values[p.Name] = standIn(p)
				stood = append(stood, p.Name)
			}
		}
		if _, errs := render(v, defs, values, "body."); errs != nil {
			if len(stood) > 0 {
				which := "the required parameters without a default"
				if all {
					which = "every parameter without a default"
				}
				errs = append(errs, resource.FieldError{Field: "parameters", Detail: fmt.Sprintf(
					"the body was checked with stand-ins for %s (%s): a parameter's first example, else its first enum value; give examples that fit the fields they fill",
					which, strings.Join(stood, ", "))})
			}
			return errs
		}
	}
	return nil
}

// standInSecretID is a well-formed Secret ID used only to check a body at
// publish; it's never stored.
const standInSecretID = "00000000-0000-7000-8000-000000000000"

// standIn is a value for p that a body can be checked with at publish.
func standIn(p resource.TemplateParam) any {
	if len(p.Examples) > 0 {
		return p.Examples[0]
	}
	if enum, ok := p.Schema["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	switch p.Type {
	case "string":
		if p.Ref != "" {
			return standInSecretID
		}
		return "example"
	case "integer", "number":
		n := 1.0
		if lo, ok := p.Schema["minimum"].(float64); ok && lo > n {
			n = lo
		}
		if hi, ok := p.Schema["maximum"].(float64); ok && hi < n {
			n = hi
		}
		if p.Type == "integer" {
			n = math.Ceil(n)
		}
		return n
	}
	return false
}

// fits reports whether x satisfies p's own schema.
func fits(p resource.TemplateParam, x any) bool {
	raw, err := json.Marshal(p.Schema)
	if err != nil {
		return false
	}
	s, err := jsonschema.Parse(raw)
	return err == nil && len(s.Validate(x)) == 0
}

// resolve applies the defaults to params and checks the result against
// the schema, returning every problem.
func resolve(schema json.RawMessage, defs []resource.TemplateParam, params map[string]any) (map[string]any, resource.FieldErrors) {
	// A round trip through JSON gives the values the types the schema
	// checker and the substitution expect (float64 for every number).
	values := map[string]any{}
	if len(params) > 0 {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, resource.FieldErrors{{Field: "parameters", Detail: "must be JSON values"}}
		}
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, resource.FieldErrors{{Field: "parameters", Detail: "must be JSON values"}}
		}
	}
	for _, p := range defs {
		if _, given := values[p.Name]; !given && p.Default != nil {
			values[p.Name] = p.Default
		}
	}
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object","additionalProperties":false}`)
	}
	s, err := jsonschema.Parse(schema)
	if err != nil {
		return nil, resource.FieldErrors{{Field: "parameters", Detail: "the version's schema can't be read"}}
	}
	var errs resource.FieldErrors
	for _, pr := range s.Validate(values) {
		field := "parameters"
		if pr.Path != "" {
			field += "." + pr.Path
		}
		errs = append(errs, resource.FieldError{Field: field, Detail: pr.Message})
	}
	if errs != nil {
		return nil, errs
	}
	return values, nil
}

// render substitutes values into v's body and decodes and checks each
// Component. at prefixes every field ("body." at publish).
func render(v resource.TemplateVersionSpec, defs []resource.TemplateParam, values map[string]any, at string) (Rendered, resource.FieldErrors) {
	types := map[string]string{}
	for _, p := range defs {
		types[p.Name] = p.Type
	}
	var errs resource.FieldErrors
	add := func(field, format string, a ...any) {
		errs = append(errs, resource.FieldError{Field: field, Detail: fmt.Sprintf(format, a...)})
	}
	var comps []Component
	byName := map[string]int{}
	// named holds every local name, including those of Components that
	// failed, so a dependency on one isn't reported as missing too.
	named := map[string]bool{}
	for i, raw := range v.Body.Components {
		field := fmt.Sprintf("%scomponents[%d]", at, i)
		var c map[string]any
		if err := json.Unmarshal(raw, &c); err != nil || c == nil {
			add(field, "must be a JSON object")
			continue
		}
		name, ok := resource.TemplateComponentName(c)
		if !ok {
			add(field+".name", "a literal DNS label")
			continue
		}
		if named[name] {
			add(field+".name", "%q is used twice", name)
			continue
		}
		named[name] = true
		field = at + "components[" + name + "]"
		delete(c, "name")
		if _, has := c["createdFrom"]; has {
			add(field+".createdFrom", "set by the server; a template can't set it")
			continue
		}
		before := len(errs)
		s := substituter{values: values, types: types, bad: func(f, d string) { add(f, "%s", d) }}
		out, _ := s.value(c, field)
		if len(errs) > before {
			// A placeholder error left a field out; checking the rest
			// would only report what follows from that.
			named[name] = true
			continue
		}
		spec, err := decodeSpec(out)
		if err != nil {
			add(field, "%s", err)
			continue
		}
		for _, check := range []func(resource.ComponentSpec) error{
			resource.ValidateComponentSource,
			resource.ValidateComponentImage,
			resource.ValidateComponentPorts,
			func(s resource.ComponentSpec) error { return resource.ValidateComponentDependencies(name, s) },
			resource.ValidateComponentSecretEnv,
		} {
			if err := check(spec); err != nil {
				add(field, "%s", detail(err))
			}
		}
		byName[name] = len(comps)
		comps = append(comps, Component{Name: name, Spec: spec})
	}
	// Each dependency names a Component of this template, and one of its
	// ports (checkDependencies' rules, which hold within the batch).
	for _, c := range comps {
		for j, d := range c.Spec.Dependencies {
			field := fmt.Sprintf("%scomponents[%s].dependencies[%d]", at, c.Name, j)
			k, ok := byName[d.Component]
			if !ok {
				if !named[d.Component] {
					add(field+".component", "%q isn't a Component in this template", d.Component)
				}
				continue
			}
			if !slices.ContainsFunc(comps[k].Spec.Ports, func(p resource.ComponentPort) bool { return p.Name == d.Port }) {
				add(field+".port", "Component %s has no port %s", d.Component, d.Port)
			}
		}
	}
	if errs != nil {
		return Rendered{}, errs
	}
	ordered, cycle := order(comps, byName)
	if cycle {
		return Rendered{}, resource.FieldErrors{{Field: at + "components", Detail: "the dependencies form a cycle"}}
	}
	used := map[string]any{}
	for _, p := range defs {
		if x, ok := values[p.Name]; ok {
			used[p.Name] = x
		}
	}
	return Rendered{Requires: slices.Clone(v.Body.Requires), Components: ordered, Parameters: used}, nil
}

// decodeSpec decodes a rendered Component, refusing fields ComponentSpec
// doesn't have.
func decodeSpec(c any) (resource.ComponentSpec, error) {
	var spec resource.ComponentSpec
	raw, err := json.Marshal(c)
	if err != nil {
		return spec, fmt.Errorf("can't be encoded")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		if te := (*json.UnmarshalTypeError)(nil); errors.As(err, &te) {
			return spec, fmt.Errorf("%s: must be %s, not a %s", te.Field, kindOf(te.Type.Kind().String()), te.Value)
		}
		return spec, fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "json: "))
	}
	return spec, nil
}

// kindOf names a Go kind the way the API's messages do.
func kindOf(k string) string {
	switch {
	case strings.HasPrefix(k, "int"), strings.HasPrefix(k, "uint"):
		return "a whole number"
	case strings.HasPrefix(k, "float"):
		return "a number"
	case k == "string":
		return "a string"
	case k == "bool":
		return "true or false"
	case k == "slice":
		return "a list"
	}
	return "an object"
}

// detail is a validator's message without the ErrValidation prefix, since
// the FieldError already says it's a validation problem.
func detail(err error) string {
	return strings.TrimPrefix(err.Error(), resource.ErrValidation.Error()+": ")
}

// order returns comps with each after the Components it depends on,
// otherwise in body order, and reports a cycle.
func order(comps []Component, byName map[string]int) ([]Component, bool) {
	const (
		visiting = 1
		done     = 2
	)
	state := make([]int, len(comps))
	out := make([]Component, 0, len(comps))
	var visit func(i int) bool
	visit = func(i int) bool {
		switch state[i] {
		case visiting:
			return false
		case done:
			return true
		}
		state[i] = visiting
		for _, d := range comps[i].Spec.Dependencies {
			if !visit(byName[d.Component]) {
				return false
			}
		}
		state[i] = done
		out = append(out, comps[i])
		return true
	}
	for i := range comps {
		if !visit(i) {
			return nil, true
		}
	}
	return out, false
}

// substituter replaces placeholders in a decoded tree.
type substituter struct {
	values map[string]any
	types  map[string]string
	bad    func(field, detail string)
}

// value returns v with its placeholders replaced, and false when v is to
// be left out: a placeholder, or a string using one, whose parameter has
// no value. A list drops such an entry, and an object such a field.
func (s substituter) value(v any, field string) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		if name, ok, wrong := resource.ParamPlaceholder(x); ok {
			if _, declared := s.types[name]; !declared {
				s.bad(field, fmt.Sprintf("$param %q isn't declared in parameters", name))
				return nil, false
			}
			val, has := s.values[name]
			return val, has
		} else if wrong {
			s.bad(field, `a placeholder is exactly {"$param": "<name>"}`)
			return nil, false
		}
		out := make(map[string]any, len(x))
		for _, k := range slices.Sorted(maps.Keys(x)) {
			if nv, keep := s.value(x[k], field+"."+k); keep {
				out[k] = nv
			}
		}
		return out, true
	case []any:
		out := make([]any, 0, len(x))
		for i, e := range x {
			if nv, keep := s.value(e, fmt.Sprintf("%s[%d]", field, i)); keep {
				out = append(out, nv)
			}
		}
		return out, true
	case string:
		return s.text(x, field)
	}
	return v, true
}

// text replaces each ${name} in str with its value's text, and "$${" with
// a literal "${". It scans only the template's own text, so a value is
// copied as it is and never scanned for placeholders itself.
func (s substituter) text(str, field string) (any, bool) {
	if !strings.Contains(str, "${") {
		return str, true
	}
	var b strings.Builder
	for i := 0; i < len(str); {
		if strings.HasPrefix(str[i:], "$${") {
			b.WriteString("${")
			i += 3
			continue
		}
		if !strings.HasPrefix(str[i:], "${") {
			b.WriteByte(str[i])
			i++
			continue
		}
		end := strings.IndexByte(str[i+2:], '}')
		if end < 0 {
			s.bad(field, fmt.Sprintf("an unclosed ${ at offset %d", i))
			return nil, false
		}
		name := str[i+2 : i+2+end]
		i += 3 + end
		if _, declared := s.types[name]; !declared {
			s.bad(field, fmt.Sprintf("${%s} isn't declared in parameters", name))
			return nil, false
		}
		val, has := s.values[name]
		if !has {
			return nil, false
		}
		switch x := val.(type) {
		case string:
			b.WriteString(x)
		case float64:
			b.WriteString(strconv.FormatFloat(x, 'f', -1, 64))
		default:
			s.bad(field, fmt.Sprintf("${%s} isn't text or a number; use {\"$param\": %q} for the whole value", name, name))
			return nil, false
		}
	}
	return b.String(), true
}
