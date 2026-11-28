package resource

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/serendip-infra/serendip-infra/core/jsonschema"
)

// Templates (specs/027-templates-and-scaffolder, ADR 0025) mirror Azure
// Template Specs: a Template (templateSpecs) has immutable versions
// (templateSpecs/versions), each holding a parameters schema and a body of
// Components with placeholders. Using one creates ordinary resources.
const (
	KindTemplate        Kind = "Template"
	KindTemplateVersion Kind = "TemplateVersion"
)

// TemplateSpec describes a Template. It's owned by one Project, or by
// nothing (platform-wide, at "/").
type TemplateSpec struct {
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
}

type TemplateStatus struct {
	Status
}

type Template = Resource[TemplateSpec, TemplateStatus]

// TemplateVersionSpec is one published version. Template and Version are
// set by the server from the owner and the name, so the store's unique
// key (TemplateVersionKey) can see them; ValidateTemplateVersion checks
// they agree.
type TemplateVersionSpec struct {
	// Template is the owning Template's ID.
	Template string `json:"template"`
	// Version is the version's label, the same as its name.
	Version string `json:"version"`
	// Parameters is a JSON Schema object (plan decision 2).
	Parameters json.RawMessage `json:"parameters,omitempty"`
	// Body lists the Components to create (plan decision 3).
	Body TemplateBody `json:"body"`
}

// TemplateBody is a version's body. Each Component is kept as JSON
// because placeholders stand where typed values go until it's rendered.
type TemplateBody struct {
	// Requires names the optional packages the Components need, such as
	// "builds".
	Requires   []string          `json:"requires,omitempty"`
	Components []json.RawMessage `json:"components"`
}

type TemplateVersionStatus struct {
	Status
}

type TemplateVersion = Resource[TemplateVersionSpec, TemplateVersionStatus]

// TemplateVersionKey is a version's unique key: its label within its
// Template.
func TemplateVersionKey(s TemplateVersionSpec) string {
	if s.Template == "" || s.Version == "" {
		return ""
	}
	return s.Template + "/" + s.Version
}

// TemplateOrigin records which template a resource was made from (plan
// decision 9). It's set only by the server.
type TemplateOrigin struct {
	Template     string    `json:"template"`
	TemplateName string    `json:"templateName"`
	Version      string    `json:"version"`
	VersionID    string    `json:"versionId"`
	By           string    `json:"by"`
	At           time.Time `json:"at"`
	// Parameters are the values used: on the Application, or on each
	// Component when they were added to an existing Application.
	Parameters map[string]any `json:"parameters,omitempty"`
}

// FieldError is one problem with a named field.
type FieldError struct {
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

// FieldErrors is every problem found; it is an ErrValidation.
type FieldErrors []FieldError

func (e FieldErrors) Error() string {
	parts := make([]string, len(e))
	for i, f := range e {
		parts[i] = f.Field + ": " + f.Detail
	}
	return ErrValidation.Error() + ": " + strings.Join(parts, "; ")
}

func (e FieldErrors) Unwrap() error { return ErrValidation }

// Template limits (plan decisions 2 and 3).
const (
	MaxTemplateParameters = 50
	MaxTemplateComponents = 20
	MaxTemplateVersionLen = 256 << 10
	maxTemplateText       = 1024
)

var (
	// paramNameRE is a parameter's name, usable inside ${...}.
	paramNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
	// versionLabelRE is a version label such as 1.0 or 2026-11.
	versionLabelRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,62}[A-Za-z0-9])?$`)
)

// paramKeys are the keywords a parameter may use, by type.
var paramKeys = map[string][]string{
	"string":  {"pattern", "minLength", "maxLength", "enum", ParamRefKey},
	"integer": {"minimum", "maximum", "enum"},
	"number":  {"minimum", "maximum", "enum"},
	"boolean": {},
}

// ParamRefKey marks a string parameter as a reference to another
// resource; "Secret" is the only kind.
const ParamRefKey = "x-serendip-ref"

// ValidateTemplate checks a Template's own fields.
func ValidateTemplate(m Metadata, s TemplateSpec) error {
	var errs FieldErrors
	switch {
	case len(m.OwnerRefs) == 0:
	case len(m.OwnerRefs) == 1 && m.OwnerRefs[0].Kind == KindProject && validRefID(m.OwnerRefs[0].ID):
	default:
		errs = append(errs, FieldError{"metadata.ownerRefs", "a Template is owned by one Project, or by nothing (platform-wide)"})
	}
	if len(s.DisplayName) > 200 {
		errs = append(errs, FieldError{"displayName", "at most 200 characters"})
	}
	if len(s.Description) > 4000 {
		errs = append(errs, FieldError{"description", "at most 4000 characters"})
	}
	if errs != nil {
		return errs
	}
	return nil
}

// TemplateParam is one parameter, read from a version's schema.
type TemplateParam struct {
	Name     string
	Type     string
	Required bool
	// Default is nil when there's none.
	Default any
	// Ref is ParamRefKey's value ("Secret"), or "".
	Ref string
	// Examples are the schema's examples; the first stands in for a
	// required parameter without a default when a version is checked at
	// publish (core/template).
	Examples []any
	// Schema is the parameter's own JSON Schema property.
	Schema map[string]any
}

// TemplateParams reads a version's parameters schema in name order,
// returning every problem with it as FieldErrors.
func TemplateParams(raw json.RawMessage) ([]TemplateParam, FieldErrors) {
	if len(raw) == 0 {
		return nil, nil
	}
	var errs FieldErrors
	add := func(field, format string, a ...any) {
		errs = append(errs, FieldError{field, fmt.Sprintf(format, a...)})
	}
	if dup := duplicateKey(raw); dup != "" {
		add("parameters"+dup, "named twice")
		return nil, errs
	}
	if _, err := jsonschema.Parse(raw); err != nil {
		add("parameters", "%v", err)
		return nil, errs
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil || root == nil {
		add("parameters", "must be a JSON object")
		return nil, errs
	}
	for k, v := range root {
		switch k {
		case "type":
			if v != "object" {
				add("parameters.type", `must be "object"`)
			}
		case "properties", "required", "title", "description", "$schema":
		case "additionalProperties":
			if v != false {
				add("parameters.additionalProperties", "only false is allowed")
			}
		default:
			add("parameters."+k, "not allowed at the top level")
		}
	}
	if root["type"] == nil {
		add("parameters.type", `must be "object"`)
	}
	props, _ := root["properties"].(map[string]any)
	if len(props) > MaxTemplateParameters {
		add("parameters.properties", "at most %d parameters", MaxTemplateParameters)
	}
	required := map[string]bool{}
	if list, ok := root["required"].([]any); ok {
		for _, x := range list {
			name, _ := x.(string)
			if _, ok := props[name]; !ok {
				add("parameters.required", "%q isn't a parameter", name)
			}
			required[name] = true
		}
	}
	var out []TemplateParam
	for _, name := range slices.Sorted(maps.Keys(props)) {
		field := "parameters." + name
		if !paramNameRE.MatchString(name) {
			add(field, "a name is a letter or '_', then letters, digits or '_' (at most 63)")
			continue
		}
		prop, _ := props[name].(map[string]any)
		typ, _ := prop["type"].(string)
		allowed, ok := paramKeys[typ]
		if !ok {
			add(field+".type", "one of string, integer, number or boolean")
			continue
		}
		for k := range prop {
			switch {
			case k == "type", k == "default", k == "title", k == "description", k == "examples":
			case slices.Contains(allowed, k):
			default:
				add(field+"."+k, "not allowed for a %s parameter", typ)
			}
		}
		p := TemplateParam{Name: name, Type: typ, Required: required[name], Default: prop["default"], Schema: prop}
		if ref, has := prop[ParamRefKey]; has {
			if ref != "Secret" {
				add(field+"."+ParamRefKey, `only "Secret" is allowed`)
			}
			p.Ref, _ = ref.(string)
		}
		if p.Default != nil {
			if problems := validateAgainst(prop, p.Default); len(problems) > 0 {
				add(field+".default", "doesn't fit its own schema: %s", problems[0].String())
			}
			if p.Ref != "" {
				add(field+".default", "a reference parameter has no default")
			}
		}
		if ex, has := prop["examples"]; has {
			list, isList := ex.([]any)
			switch {
			case !isList || len(list) == 0:
				add(field+".examples", "a non-empty list")
			case p.Ref != "":
				add(field+".examples", "a reference parameter has no examples")
			default:
				for i, e := range list {
					if problems := validateAgainst(prop, e); len(problems) > 0 {
						add(fmt.Sprintf("%s.examples[%d]", field, i), "doesn't fit its own schema: %s", problems[0].String())
					}
				}
				p.Examples = list
			}
		}
		out = append(out, p)
	}
	return out, errs
}

// validateAgainst checks v against one property's schema.
func validateAgainst(prop map[string]any, v any) []jsonschema.Problem {
	raw, err := json.Marshal(prop)
	if err != nil {
		return []jsonschema.Problem{{Message: err.Error()}}
	}
	s, err := jsonschema.Parse(raw)
	if err != nil {
		return []jsonschema.Problem{{Message: err.Error()}}
	}
	return s.Validate(v)
}

// ParamPlaceholder reports whether v is a whole-value placeholder,
// exactly {"$param": "<name>"}, and returns the name. ok is false for
// anything else; bad is true for an object that uses "$param" wrongly.
func ParamPlaceholder(v any) (name string, ok, bad bool) {
	m, isMap := v.(map[string]any)
	if !isMap {
		return "", false, false
	}
	p, has := m["$param"]
	if !has {
		return "", false, false
	}
	name, isString := p.(string)
	if len(m) != 1 || !isString {
		return "", false, true
	}
	return name, true, false
}

// StringParams returns the parameter names a string's ${name} references
// use, in order, or an error for a malformed one. "$${" is a literal "${".
func StringParams(s string) ([]string, error) {
	var names []string
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			continue
		}
		if strings.HasPrefix(s[i:], "$${") {
			i += 2
			continue
		}
		if !strings.HasPrefix(s[i:], "${") {
			continue
		}
		end := strings.IndexByte(s[i+2:], '}')
		if end < 0 {
			return nil, fmt.Errorf("an unclosed ${ at offset %d", i)
		}
		name := s[i+2 : i+2+end]
		if !paramNameRE.MatchString(name) {
			return nil, fmt.Errorf("${%s} isn't a parameter name", name)
		}
		names = append(names, name)
		i += 2 + end
	}
	return names, nil
}

// TemplateComponentName is a body Component's template-local name. It
// becomes the Component's resource name, so it's a DNS label.
func TemplateComponentName(c map[string]any) (string, bool) {
	name, ok := c["name"].(string)
	return name, ok && len(name) <= 63 && dnsLabelRE.MatchString(name)
}

// ValidateTemplateVersion checks a version as published (plan decisions 2
// and 3): its owner and label, the parameters, and the body's
// placeholders, names, local dependencies and size. Whether its rendered
// Components are valid Components is checked where they're rendered
// (core/template). Every problem is returned, as FieldErrors.
func ValidateTemplateVersion(m Metadata, s TemplateVersionSpec) error {
	var errs FieldErrors
	add := func(field, format string, a ...any) {
		errs = append(errs, FieldError{field, fmt.Sprintf(format, a...)})
	}
	if len(m.OwnerRefs) != 1 || m.OwnerRefs[0].Kind != KindTemplate || !validRefID(m.OwnerRefs[0].ID) {
		add("metadata.ownerRefs", "a version is owned by exactly one Template")
	} else if s.Template != m.OwnerRefs[0].ID {
		add("template", "must be the owning Template's ID")
	}
	if !versionLabelRE.MatchString(m.Name) {
		add("metadata.name", "a version label: letters and digits, with '.', '_' or '-' inside (at most 64)")
	}
	if s.Version != m.Name {
		add("version", "must be the version's name")
	}
	if raw, err := json.Marshal(s); err == nil && len(raw) > MaxTemplateVersionLen {
		add("spec", "at most %d KiB", MaxTemplateVersionLen>>10)
	}
	params, perrs := TemplateParams(s.Parameters)
	errs = append(errs, perrs...)
	declared := map[string]TemplateParam{}
	for _, p := range params {
		declared[p.Name] = p
	}
	// A parameter refused above is still declared, so its uses aren't
	// reported again as unknown (or it as unused).
	refused := map[string]bool{}
	if perrs != nil {
		var root struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if json.Unmarshal(s.Parameters, &root) == nil {
			for name := range root.Properties {
				if _, ok := declared[name]; !ok {
					refused[name] = true
				}
			}
		}
	}

	for i, r := range s.Body.Requires {
		if !dnsLabelRE.MatchString(r) || len(r) > 63 || slices.Index(s.Body.Requires, r) != i {
			add(fmt.Sprintf("body.requires[%d]", i), "a package name, listed once")
		}
	}
	switch n := len(s.Body.Components); {
	case n == 0:
		add("body.components", "at least one Component")
	case n > MaxTemplateComponents:
		add("body.components", "at most %d Components", MaxTemplateComponents)
	}

	used := map[string]bool{}
	names := map[string]int{}
	deps := map[string][]string{}
	type pendingDep struct {
		field, from, to string
	}
	var pending []pendingDep
	for i, raw := range s.Body.Components {
		at := fmt.Sprintf("body.components[%d]", i)
		if dup := duplicateKey(raw); dup != "" {
			add(at+dup, "named twice")
			continue
		}
		var c map[string]any
		if err := json.Unmarshal(raw, &c); err != nil || c == nil {
			add(at, "must be a JSON object")
			continue
		}
		name, ok := TemplateComponentName(c)
		if !ok {
			add(at+".name", "a literal DNS label (lower-case letters, digits and '-', at most 63)")
		} else if j, dup := names[name]; dup {
			add(at+".name", "%q is also components[%d]", name, j)
			name = "" // its dependencies don't join the graph under that name
		} else {
			names[name] = i
			at = "body.components[" + name + "]"
		}
		walkPlaceholders(c, at, func(field, p string, inString bool) {
			used[p] = true
			dp, ok := declared[p]
			switch {
			case refused[p]:
			case !ok:
				add(field, "$param %q isn't declared in parameters", p)
			case inString && dp.Type == "boolean":
				add(field, "${%s} is a boolean; use {\"$param\": %q} for the whole value", p, p)
			}
		}, func(field, detail string) { add(field, "%s", detail) })
		list, _ := c["dependencies"].([]any)
		for j, d := range list {
			dm, _ := d.(map[string]any)
			to, isString := dm["component"].(string)
			field := fmt.Sprintf("%s.dependencies[%d].component", at, j)
			if !isString {
				add(field, "a literal name of a Component in this template")
				continue
			}
			pending = append(pending, pendingDep{field, name, to})
		}
	}
	for _, d := range pending {
		if _, ok := names[d.to]; !ok {
			add(d.field, "%q isn't a Component in this template", d.to)
			continue
		}
		if d.from != "" {
			deps[d.from] = append(deps[d.from], d.to)
		}
	}
	if cycle := findCycle(deps); cycle != nil {
		add("body.components", "the dependencies form a cycle: %s", strings.Join(cycle, " -> "))
	}
	for _, p := range params {
		if !used[p.Name] {
			add("parameters."+p.Name, "declared but never used in the body")
		}
	}
	if errs != nil {
		return errs
	}
	return nil
}

// walkPlaceholders visits every placeholder in v. A Component's name and
// its dependencies' components must be literal; that's checked where they
// are read, since a placeholder can't be a DNS label or a local name.
func walkPlaceholders(v any, at string, use func(field, name string, inString bool), bad func(field, detail string)) {
	switch x := v.(type) {
	case map[string]any:
		if name, ok, wrong := ParamPlaceholder(x); ok {
			use(at, name, false)
			return
		} else if wrong {
			bad(at, `a placeholder is exactly {"$param": "<name>"}`)
			return
		}
		for _, k := range slices.Sorted(maps.Keys(x)) {
			walkPlaceholders(x[k], at+"."+k, use, bad)
		}
	case []any:
		for i, e := range x {
			walkPlaceholders(e, fmt.Sprintf("%s[%d]", at, i), use, bad)
		}
	case string:
		refs, err := StringParams(x)
		if err != nil {
			bad(at, err.Error())
			return
		}
		for _, r := range refs {
			use(at, r, true)
		}
	}
}

// findCycle returns one dependency cycle among the local names, or nil.
func findCycle(deps map[string][]string) []string {
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var stack []string
	var visit func(n string) []string
	visit = func(n string) []string {
		state[n] = visiting
		stack = append(stack, n)
		for _, m := range deps[n] {
			switch state[m] {
			case visiting:
				i := slices.Index(stack, m)
				return append(slices.Clone(stack[i:]), m)
			case 0:
				if c := visit(m); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = done
		return nil
	}
	keys := slices.Collect(maps.Keys(deps))
	sort.Strings(keys)
	for _, n := range keys {
		if state[n] == 0 {
			if c := visit(n); c != nil {
				return c
			}
		}
	}
	return nil
}

// duplicateKey returns the path (".a.b") of the first key that appears
// twice in one object of raw, or "" if there's none or raw isn't JSON
// (that's reported where it's decoded). encoding/json would keep the last
// silently.
func duplicateKey(raw []byte) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var walk func(path string) (string, bool)
	walk = func(path string) (string, bool) {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		switch tok {
		case json.Delim('{'):
			seen := map[string]bool{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return "", false
				}
				k, _ := kt.(string)
				if seen[k] {
					return path + "." + k, true
				}
				seen[k] = true
				if dup, ok := walk(path + "." + k); !ok || dup != "" {
					return dup, ok
				}
			}
			_, err = dec.Token()
			return "", err == nil
		case json.Delim('['):
			for i := 0; dec.More(); i++ {
				if dup, ok := walk(fmt.Sprintf("%s[%d]", path, i)); !ok || dup != "" {
					return dup, ok
				}
			}
			_, err = dec.Token()
			return "", err == nil
		}
		return "", true
	}
	dup, _ := walk("")
	return dup
}
