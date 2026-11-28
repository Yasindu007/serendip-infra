package resource

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	tmplID = "0190a6c2-0000-7000-8000-0000000000aa"
	projID = "0190a6c2-0000-7000-8000-0000000000bb"
)

// goodVersion is a valid two-Component version; cases change one thing.
func goodVersion() (Metadata, TemplateVersionSpec) {
	m := Metadata{Name: "1.0", OwnerRefs: []OwnerRef{{Kind: KindTemplate, ID: tmplID}}}
	s := TemplateVersionSpec{
		Template: tmplID, Version: "1.0",
		Parameters: json.RawMessage(`{"type":"object","required":["apiImage"],"properties":{
			"apiImage":{"type":"string","minLength":1},
			"port":{"type":"integer","minimum":1,"maximum":65535,"default":8080},
			"tag":{"type":"string","default":"1.27-alpine","pattern":"^[a-z0-9.-]+$"},
			"expose":{"type":"boolean","default":true},
			"db":{"type":"string","x-serendip-ref":"Secret"}}}`),
		Body: TemplateBody{Components: []json.RawMessage{
			json.RawMessage(`{"name":"api","image":{"$param":"apiImage"},
				"ports":[{"name":"http","port":{"$param":"port"}}],
				"secretEnv":[{"env":"DB","secret":{"$param":"db"},"key":"password"}]}`),
			json.RawMessage(`{"name":"web","image":"nginx:${tag}",
				"ports":[{"name":"http","port":80,"expose":{"$param":"expose"}}],
				"dependencies":[{"component":"api","port":"http","env":"API_URL"}]}`),
		}},
	}
	return m, s
}

func TestValidateTemplateVersion_Valid(t *testing.T) {
	m, s := goodVersion()
	if err := ValidateTemplateVersion(m, s); err != nil {
		t.Fatalf("valid version refused: %v", err)
	}
	if k := TemplateVersionKey(s); k != tmplID+"/1.0" {
		t.Errorf("key = %q", k)
	}
}

// TestValidateTemplateVersion_Refusals covers spec criterion 13 and plan
// decisions 2 and 3: each case must be refused naming its field.
func TestValidateTemplateVersion_Refusals(t *testing.T) {
	setParams := func(p string) func(*Metadata, *TemplateVersionSpec) {
		return func(_ *Metadata, s *TemplateVersionSpec) { s.Parameters = json.RawMessage(p) }
	}
	setComp := func(i int, c string) func(*Metadata, *TemplateVersionSpec) {
		return func(_ *Metadata, s *TemplateVersionSpec) { s.Body.Components[i] = json.RawMessage(c) }
	}
	many := func(n int, f func(i int) string) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = f(i)
		}
		return strings.Join(parts, ",")
	}
	cases := []struct {
		name   string
		change func(*Metadata, *TemplateVersionSpec)
		field  string
	}{
		{"unknown $param", setComp(0, `{"name":"api","image":{"$param":"nope"}}`), "body.components[api].image"},
		{"unknown ${} in a string", setComp(1, `{"name":"web","image":"nginx:${nope}","dependencies":[{"component":"api","port":"http","env":"A"}]}`), "body.components[web].image"},
		{"unused parameter", setComp(1, `{"name":"web","image":"nginx","ports":[{"name":"http","port":80,"expose":{"$param":"expose"}}],"dependencies":[{"component":"api","port":"http","env":"A"}]}`), "parameters.tag"},
		{"duplicate parameter", setParams(`{"type":"object","properties":{"a":{"type":"string"},"a":{"type":"integer"}}}`), "parameters.properties.a"},
		{"duplicate key in a Component", setComp(0, `{"name":"api","image":"a","image":"b"}`), "body.components[0].image"},
		{"default fails its own constraint", setParams(`{"type":"object","properties":{"apiImage":{"type":"string"},"port":{"type":"integer","maximum":100,"default":8080},"tag":{"type":"string"},"expose":{"type":"boolean"},"db":{"type":"string","x-serendip-ref":"Secret"}}}`), "parameters.port.default"},
		{"default of the wrong type", setParams(`{"type":"object","properties":{"apiImage":{"type":"string"},"port":{"type":"integer","default":"80"},"tag":{"type":"string"},"expose":{"type":"boolean"},"db":{"type":"string","x-serendip-ref":"Secret"}}}`), "parameters.port.default"},
		{"dependency on a missing Component", setComp(1, `{"name":"web","image":"nginx:${tag}","ports":[{"name":"http","port":80,"expose":{"$param":"expose"}}],"dependencies":[{"component":"db","port":"http","env":"A"}]}`), "body.components[web].dependencies[0].component"},
		{"dependency through a placeholder", setComp(1, `{"name":"web","image":"nginx:${tag}","ports":[{"name":"http","port":80,"expose":{"$param":"expose"}}],"dependencies":[{"component":{"$param":"apiImage"},"port":"http","env":"A"}]}`), "body.components[web].dependencies[0].component"},
		{"too many Components", func(_ *Metadata, s *TemplateVersionSpec) {
			for i := len(s.Body.Components); i <= MaxTemplateComponents; i++ {
				s.Body.Components = append(s.Body.Components, json.RawMessage(fmt.Sprintf(`{"name":"c%d","image":"nginx"}`, i)))
			}
		}, "body.components"},
		{"no Components", func(_ *Metadata, s *TemplateVersionSpec) { s.Body.Components = nil }, "body.components"},
		{"too many parameters", setParams(`{"type":"object","properties":{` +
			many(MaxTemplateParameters+1, func(i int) string { return fmt.Sprintf(`"p%d":{"type":"string"}`, i) }) + `}}`), "parameters.properties"},
		{"duplicate Component name", setComp(1, `{"name":"api","image":"nginx:${tag}","ports":[{"name":"http","port":80,"expose":{"$param":"expose"}}]}`), "body.components[1].name"},
		{"Component name not a DNS label", setComp(0, `{"name":"API_1","image":{"$param":"apiImage"}}`), "body.components[0].name"},
		{"Component name from a placeholder", setComp(0, `{"name":{"$param":"apiImage"},"image":"x"}`), "body.components[0].name"},
		{"a cycle", func(_ *Metadata, s *TemplateVersionSpec) {
			s.Body.Components[0] = json.RawMessage(`{"name":"api","image":{"$param":"apiImage"},"ports":[{"name":"http","port":{"$param":"port"}}],"secretEnv":[{"env":"DB","secret":{"$param":"db"},"key":"k"}],"dependencies":[{"component":"web","port":"http","env":"W"}]}`)
		}, "body.components"},
		{"a nested object parameter", setParams(`{"type":"object","properties":{"o":{"type":"object","properties":{}}}}`), "parameters.o.type"},
		{"an array parameter", setParams(`{"type":"object","properties":{"o":{"type":"array","items":{"type":"string"}}}}`), "parameters.o.type"},
		{"a keyword that doesn't fit the type", setParams(`{"type":"object","properties":{"apiImage":{"type":"string","minimum":1},"port":{"type":"integer"},"tag":{"type":"string"},"expose":{"type":"boolean"},"db":{"type":"string","x-serendip-ref":"Secret"}}}`), "parameters.apiImage.minimum"},
		{"an unknown reference kind", setParams(`{"type":"object","properties":{"apiImage":{"type":"string"},"port":{"type":"integer"},"tag":{"type":"string"},"expose":{"type":"boolean"},"db":{"type":"string","x-serendip-ref":"Volume"}}}`), "parameters.db.x-serendip-ref"},
		{"a parameter name unusable in ${}", setParams(`{"type":"object","properties":{"my-param":{"type":"string"}}}`), "parameters.my-param"},
		{"required names a missing parameter", setParams(`{"type":"object","required":["nope"],"properties":{"apiImage":{"type":"string"},"port":{"type":"integer"},"tag":{"type":"string"},"expose":{"type":"boolean"},"db":{"type":"string","x-serendip-ref":"Secret"}}}`), "parameters.required"},
		{"required isn't a list", setParams(`{"type":"object","required":"apiImage","properties":{}}`), "parameters"},
		{"properties isn't an object", setParams(`{"type":"object","properties":[]}`), "parameters"},
		{"top level isn't an object schema", setParams(`{"type":"string"}`), "parameters.type"},
		{"additionalProperties true", setParams(`{"type":"object","additionalProperties":true,"properties":{}}`), "parameters.additionalProperties"},
		{"a keyword jsonschema doesn't know", setParams(`{"type":"object","properties":{"a":{"type":"string","format2":1}}}`), "parameters"},
		{"a boolean inside a string", setComp(1, `{"name":"web","image":"nginx:${tag}-${expose}","dependencies":[{"component":"api","port":"http","env":"A"}]}`), "body.components[web].image"},
		{"an unclosed ${", setComp(1, `{"name":"web","image":"nginx:${tag","ports":[{"name":"http","port":80,"expose":{"$param":"expose"}}],"dependencies":[{"component":"api","port":"http","env":"A"}]}`), "body.components[web].image"},
		{"a $param with other keys", setComp(0, `{"name":"api","image":{"$param":"apiImage","x":1}}`), "body.components[api].image"},
		{"owned by a Project", func(m *Metadata, _ *TemplateVersionSpec) { m.OwnerRefs = []OwnerRef{{Kind: KindProject, ID: projID}} }, "metadata.ownerRefs"},
		{"template field disagrees with the owner", func(_ *Metadata, s *TemplateVersionSpec) { s.Template = projID }, "template"},
		{"version field disagrees with the name", func(_ *Metadata, s *TemplateVersionSpec) { s.Version = "2.0" }, "version"},
		{"a bad label", func(m *Metadata, s *TemplateVersionSpec) { m.Name, s.Version = "1.0/x", "1.0/x" }, "metadata.name"},
		{"a requires listed twice", func(_ *Metadata, s *TemplateVersionSpec) { s.Body.Requires = []string{"builds", "builds"} }, "body.requires[1]"},
		{"too big", func(_ *Metadata, s *TemplateVersionSpec) {
			s.Body.Components[0] = json.RawMessage(`{"name":"api","image":{"$param":"apiImage"},"ports":[{"name":"http","port":{"$param":"port"}}],"secretEnv":[{"env":"DB","secret":{"$param":"db"},"key":"k"}],"env":{"X":"` + strings.Repeat("x", MaxTemplateVersionLen) + `"}}`)
		}, "spec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, s := goodVersion()
			tc.change(&m, &s)
			err := ValidateTemplateVersion(m, s)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v, want a validation error", err)
			}
			var fe FieldErrors
			if !errors.As(err, &fe) {
				t.Fatalf("err %T isn't FieldErrors", err)
			}
			for _, f := range fe {
				if f.Field == tc.field {
					return
				}
			}
			t.Errorf("no error names %q: %v", tc.field, err)
		})
	}
}

// TestValidateTemplateVersion_EveryError checks problems are collected,
// not stopped at the first.
func TestValidateTemplateVersion_EveryError(t *testing.T) {
	m, s := goodVersion()
	s.Version = "2.0"
	s.Body.Components[0] = json.RawMessage(`{"name":"api","image":{"$param":"nope"}}`)
	var fe FieldErrors
	if !errors.As(ValidateTemplateVersion(m, s), &fe) || len(fe) < 3 {
		t.Fatalf("errors = %v, want the version, the unknown and the unused ones", fe)
	}
}

// TestValidateTemplateVersion_NoKnockOnErrors checks a refused parameter
// isn't reported again at each use, nor as unused.
func TestValidateTemplateVersion_NoKnockOnErrors(t *testing.T) {
	m, s := goodVersion()
	s.Parameters = json.RawMessage(`{"type":"object","properties":{"apiImage":{"type":"array"},"port":{"type":"integer"},"tag":{"type":"string"},"expose":{"type":"boolean"},"db":{"type":"string","x-serendip-ref":"Secret"}}}`)
	var fe FieldErrors
	if !errors.As(ValidateTemplateVersion(m, s), &fe) || len(fe) != 1 || fe[0].Field != "parameters.apiImage.type" {
		t.Errorf("errors = %v, want only parameters.apiImage.type", fe)
	}
}

func TestStringParams(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		bad  bool
	}{
		{"plain", nil, false},
		{"nginx:${tag}", []string{"tag"}, false},
		{"${a}-${b}", []string{"a", "b"}, false},
		{"$${literal}", nil, false},
		{"cost $5 and ${x}", []string{"x"}, false},
		{"${", nil, true},
		{"${a-b}", nil, true},
		{"${}", nil, true},
	}
	for _, tc := range cases {
		got, err := StringParams(tc.in)
		if (err != nil) != tc.bad || fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("StringParams(%q) = %v, %v; want %v, bad=%v", tc.in, got, err, tc.want, tc.bad)
		}
	}
}

func TestTemplateParams(t *testing.T) {
	_, s := goodVersion()
	params, errs := TemplateParams(s.Parameters)
	if errs != nil {
		t.Fatal(errs)
	}
	got := map[string]TemplateParam{}
	for _, p := range params {
		got[p.Name] = p
	}
	if !got["apiImage"].Required || got["port"].Required || got["port"].Default != float64(8080) ||
		got["db"].Ref != "Secret" || got["expose"].Type != "boolean" || len(params) != 5 {
		t.Errorf("params = %+v", params)
	}
	if _, errs := TemplateParams(json.RawMessage(`{"type":"object","properties":{"db":{"type":"string","x-serendip-ref":"Secret","default":"x"}}}`)); errs == nil {
		t.Error("a Secret parameter with a default was accepted")
	}
}

func TestValidateTemplate(t *testing.T) {
	cases := []struct {
		name string
		m    Metadata
		s    TemplateSpec
		ok   bool
	}{
		{"platform-wide", Metadata{Name: "web"}, TemplateSpec{DisplayName: "Web"}, true},
		{"in a Project", Metadata{Name: "web", OwnerRefs: []OwnerRef{{Kind: KindProject, ID: projID}}}, TemplateSpec{}, true},
		{"owned by an Application", Metadata{Name: "web", OwnerRefs: []OwnerRef{{Kind: KindApplication, ID: projID}}}, TemplateSpec{}, false},
		{"two owners", Metadata{Name: "web", OwnerRefs: []OwnerRef{{Kind: KindProject, ID: projID}, {Kind: KindProject, ID: tmplID}}}, TemplateSpec{}, false},
		{"long description", Metadata{Name: "web"}, TemplateSpec{Description: strings.Repeat("x", 4001)}, false},
	}
	for _, tc := range cases {
		if err := ValidateTemplate(tc.m, tc.s); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

// TestCreatedFromRoundTrips checks the provenance field survives JSON and
// is absent when unset, so old rows are unchanged.
func TestCreatedFromRoundTrips(t *testing.T) {
	raw, _ := json.Marshal(ComponentSpec{Image: "nginx"})
	if strings.Contains(string(raw), "createdFrom") {
		t.Errorf("unset createdFrom marshalled: %s", raw)
	}
	raw, _ = json.Marshal(ApplicationSpec{})
	if string(raw) != "{}" {
		t.Errorf("empty ApplicationSpec = %s, want {}", raw)
	}
	in := ApplicationSpec{CreatedFrom: &TemplateOrigin{Template: tmplID, Version: "1.0", Parameters: map[string]any{"port": float64(80)}}}
	raw, _ = json.Marshal(in)
	var out ApplicationSpec
	if err := json.Unmarshal(raw, &out); err != nil || out.CreatedFrom == nil || out.CreatedFrom.Parameters["port"] != float64(80) {
		t.Errorf("round trip = %+v, %v", out.CreatedFrom, err)
	}
}
