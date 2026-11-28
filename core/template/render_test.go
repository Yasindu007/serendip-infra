package template

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

const testTemplateID = "0190a6c2-0000-7000-8000-0000000000aa"

// version builds a version from its parameters schema and Components.
func version(params string, comps ...string) (resource.Metadata, resource.TemplateVersionSpec) {
	m := resource.Metadata{Name: "1.0", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindTemplate, ID: testTemplateID}}}
	s := resource.TemplateVersionSpec{Template: testTemplateID, Version: "1.0"}
	if params != "" {
		s.Parameters = json.RawMessage(params)
	}
	for _, c := range comps {
		s.Body.Components = append(s.Body.Components, json.RawMessage(c))
	}
	return m, s
}

// fields returns the errors' field names.
func fields(errs resource.FieldErrors) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Field
	}
	return out
}

// twoTier is a version with a dependency, both kinds of placeholder, and
// an optional parameter.
func twoTier() (resource.Metadata, resource.TemplateVersionSpec) {
	return version(`{"type":"object","required":["name"],"properties":{
			"name":{"type":"string","maxLength":200},
			"image":{"type":"string","default":"nginx:1.27-alpine"},
			"port":{"type":"integer","minimum":1,"maximum":65535,"default":8080},
			"expose":{"type":"boolean","default":true},
			"note":{"type":"string"}}}`,
		// web comes first, but depends on api.
		`{"name":"web","image":{"$param":"image"},
			"ports":[{"name":"http","port":80,"expose":{"$param":"expose"}}],
			"env":{"GREETING":"hello ${name}","NOTE":"${note}","LITERAL":"$${name}","PORT":"${port}"},
			"dependencies":[{"component":"api","port":"http","env":"API_URL"}]}`,
		`{"name":"api","image":"nginx:1.27-alpine",
			"ports":[{"name":"http","port":{"$param":"port"}}],
			"env":{"OWNER":{"$param":"name"}}}`)
}

func TestRender(t *testing.T) {
	_, v := twoTier()
	r, errs := Render(v, map[string]any{"name": "shop"})
	if errs != nil {
		t.Fatalf("Render: %v", errs)
	}
	if got := []string{r.Components[0].Name, r.Components[1].Name}; !slices.Equal(got, []string{"api", "web"}) {
		t.Errorf("order = %v, want api before web (its dependency)", got)
	}
	api, web := r.Components[0].Spec, r.Components[1].Spec
	if api.Ports[0].Port != 8080 {
		t.Errorf("api port = %d, want the default 8080 as a number", api.Ports[0].Port)
	}
	if api.Env["OWNER"] != "shop" {
		t.Errorf("api OWNER = %q", api.Env["OWNER"])
	}
	if web.Image != "nginx:1.27-alpine" || !web.Ports[0].Expose {
		t.Errorf("web = %+v, want the defaults", web)
	}
	want := map[string]string{"GREETING": "hello shop", "LITERAL": "${name}", "PORT": "8080"}
	for k, x := range want {
		if web.Env[k] != x {
			t.Errorf("web env %s = %q, want %q", k, web.Env[k], x)
		}
	}
	if _, has := web.Env["NOTE"]; has {
		t.Errorf("NOTE is set from an omitted optional parameter: %q", web.Env["NOTE"])
	}
	if web.Dependencies[0].Component != "api" {
		t.Errorf("dependency = %+v, want the local name", web.Dependencies[0])
	}
	wantParams := map[string]any{"name": "shop", "image": "nginx:1.27-alpine", "port": 8080.0, "expose": true}
	if len(r.Parameters) != len(wantParams) {
		t.Errorf("parameters = %v, want %v", r.Parameters, wantParams)
	}
	for k, x := range wantParams {
		if r.Parameters[k] != x {
			t.Errorf("parameters[%s] = %v, want %v", k, r.Parameters[k], x)
		}
	}
}

// TestRender_ParameterErrors checks every problem is reported at once,
// each naming its parameter (spec criterion 4).
func TestRender_ParameterErrors(t *testing.T) {
	_, v := twoTier()
	_, errs := Render(v, map[string]any{"port": 70000, "expose": "yes", "extra": 1, "image": 3})
	got := fields(errs)
	for _, f := range []string{"parameters.name", "parameters.port", "parameters.expose", "parameters.extra", "parameters.image"} {
		if !slices.Contains(got, f) {
			t.Errorf("no error for %s: %v", f, errs)
		}
	}
	if !errors.Is(errs, resource.ErrValidation) {
		t.Errorf("errs isn't a validation error")
	}
}

func TestRender_ParameterTypes(t *testing.T) {
	_, v := twoTier()
	cases := []struct {
		name   string
		params map[string]any
		field  string
	}{
		{"a fraction for an integer", map[string]any{"name": "a", "port": 80.5}, "parameters.port"},
		{"an object for a string", map[string]any{"name": map[string]any{"a": 1}}, "parameters.name"},
		{"a list for a string", map[string]any{"name": []any{"a"}}, "parameters.name"},
		{"null for a string", map[string]any{"name": nil}, "parameters.name"},
		{"too long", map[string]any{"name": strings.Repeat("x", 201)}, "parameters.name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := Render(v, tc.params)
			if !slices.Contains(fields(errs), tc.field) {
				t.Errorf("errors = %v, want one for %s", errs, tc.field)
			}
		})
	}
}

// TestRender_HostileValues covers spec criterion 6: a value that looks
// like a placeholder, YAML or JSON lands as literal text in the field it
// fills, and is validated as that field.
func TestRender_HostileValues(t *testing.T) {
	_, v := twoTier()
	hostile := []string{
		"${image}",
		"$${name}",
		`{"$param":"image"}`,
		"a: b\nimage: evil\n",
		`{"image":"evil","ports":[{"name":"x","port":1}]}`,
		`", "image": "evil`,
		"line one\nline two",
	}
	for _, h := range hostile {
		t.Run(h, func(t *testing.T) {
			r, errs := Render(v, map[string]any{"name": h})
			if errs != nil {
				t.Fatalf("Render: %v", errs)
			}
			api, web := r.Components[0].Spec, r.Components[1].Spec
			if api.Env["OWNER"] != h || web.Env["GREETING"] != "hello "+h {
				t.Errorf("value not copied literally: OWNER %q, GREETING %q", api.Env["OWNER"], web.Env["GREETING"])
			}
			if api.Image != "nginx:1.27-alpine" || web.Image != "nginx:1.27-alpine" || len(api.Env) != 1 || len(web.Env) != 3 {
				t.Errorf("a value reached another field: api %+v, web %+v", api, web)
			}
		})
	}
}

// TestRender_HostileImage checks a value is validated as the field it
// fills: an image with a newline, or that is JSON or YAML, isn't an image.
func TestRender_HostileImage(t *testing.T) {
	_, v := twoTier()
	for _, h := range []string{"nginx\nevil", "nginx:1 --privileged", `{"$param":"name"}`, "a: b", "${name}"} {
		t.Run(h, func(t *testing.T) {
			_, errs := Render(v, map[string]any{"name": "a", "image": h})
			if !slices.Contains(fields(errs), "components[web]") {
				t.Fatalf("errors = %v, want components[web] refused", errs)
			}
			if !strings.Contains(errs.Error(), "image") {
				t.Errorf("errors = %v, want them to name the image", errs)
			}
		})
	}
}

func TestRender_Refusals(t *testing.T) {
	params := `{"type":"object","properties":{"p":{"type":"integer","default":80}}}`
	cases := []struct {
		name  string
		comps []string
		field string
		text  string
	}{
		{"an unknown field", []string{`{"name":"a","image":"x","ports":[{"name":"h","port":{"$param":"p"}}],"privileged":true}`}, "components[a]", "privileged"},
		{"createdFrom in the body", []string{`{"name":"a","image":"x","ports":[{"name":"h","port":{"$param":"p"}}],"createdFrom":{"template":"x"}}`}, "components[a].createdFrom", ""},
		{"text for a port", []string{`{"name":"a","image":"x","ports":[{"name":"h","port":"${p}"}]}`}, "components[a]", "ports.port"},
		{"a dependency on a missing port", []string{
			`{"name":"a","image":"x","ports":[{"name":"h","port":{"$param":"p"}}]}`,
			`{"name":"b","image":"x","dependencies":[{"component":"a","port":"grpc","env":"A"}]}`}, "components[b].dependencies[0].port", "grpc"},
		{"an undeclared $param reports only itself", []string{`{"name":"a","image":{"$param":"nope"},"ports":[{"name":"h","port":{"$param":"p"}}]}`}, "components[a].image", "nope"},
		{"no image and no source", []string{`{"name":"a","ports":[{"name":"h","port":{"$param":"p"}}]}`}, "components[a]", "image or source"},
		{"a bad port name", []string{`{"name":"a","image":"x","ports":[{"name":"HTTP","port":{"$param":"p"}}]}`}, "components[a]", "ports[0].name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, v := version(params, tc.comps...)
			_, errs := Render(v, nil)
			i := slices.Index(fields(errs), tc.field)
			if i < 0 {
				t.Fatalf("errors = %v, want one for %s", errs, tc.field)
			}
			if !strings.Contains(errs[i].Detail, tc.text) {
				t.Errorf("detail %q doesn't mention %q", errs[i].Detail, tc.text)
			}
			if strings.HasPrefix(tc.name, "an undeclared") && len(errs) != 1 {
				t.Errorf("errors = %v, want only the placeholder's", errs)
			}
		})
	}
}

// TestSubstituteOmits checks an omitted optional parameter removes the
// field it fills, and a list entry that is only a placeholder.
func TestSubstituteOmits(t *testing.T) {
	s := substituter{values: map[string]any{"a": "x"}, types: map[string]string{"a": "string", "b": "string"},
		bad: func(f, d string) { t.Errorf("%s: %s", f, d) }}
	var in any
	if err := json.Unmarshal([]byte(`{"keep":{"$param":"a"},"drop":{"$param":"b"},"text":"${b}!",
		"list":[{"$param":"a"},{"$param":"b"},"${b}",{"k":{"$param":"b"},"j":1}]}`), &in); err != nil {
		t.Fatal(err)
	}
	out, _ := s.value(in, "c")
	got, _ := json.Marshal(out)
	if want := `{"keep":"x","list":["x",{"j":1}]}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestValidatePublish(t *testing.T) {
	cases := []struct {
		name   string
		params string
		comps  []string
		field  string // "" for valid
	}{
		{"valid with a stand-in from examples", `{"type":"object","required":["repo"],"properties":{"repo":{"type":"string","examples":["https://example.com/a.git"]}}}`,
			[]string{`{"name":"a","source":{"repository":{"$param":"repo"}}}`}, ""},
		{"no example to stand in", `{"type":"object","required":["repo"],"properties":{"repo":{"type":"string"}}}`,
			[]string{`{"name":"a","source":{"repository":{"$param":"repo"}}}`}, "body.components[a]"},
		{"a stand-in from enum", `{"type":"object","required":["port"],"properties":{"port":{"type":"integer","enum":[80,443]}}}`,
			[]string{`{"name":"a","image":"x","ports":[{"name":"h","port":{"$param":"port"}}]}`}, ""},
		{"a stand-in at the minimum", `{"type":"object","required":["port"],"properties":{"port":{"type":"integer","minimum":1024}}}`,
			[]string{`{"name":"a","image":"x","ports":[{"name":"h","port":{"$param":"port"}}]}`}, ""},
		{"a default that's no valid port", `{"type":"object","properties":{"port":{"type":"integer","default":0}}}`,
			[]string{`{"name":"a","image":"x","ports":[{"name":"h","port":{"$param":"port"}}]}`}, "body.components[a]"},
		{"an optional Secret leaves a variable without one", `{"type":"object","properties":{"db":{"type":"string","x-serendip-ref":"Secret"}}}`,
			[]string{`{"name":"a","image":"x","secretEnv":[{"env":"DB","secret":{"$param":"db"},"key":"password"}]}`}, "body.components[a]"},
		{"a required Secret is fine", `{"type":"object","required":["db"],"properties":{"db":{"type":"string","x-serendip-ref":"Secret"}}}`,
			[]string{`{"name":"a","image":"x","secretEnv":[{"env":"DB","secret":{"$param":"db"},"key":"password"}]}`}, ""},
		{"an optional parameter checked when filled", `{"type":"object","properties":{"branch":{"type":"string","examples":["a..b"]}}}`,
			[]string{`{"name":"a","source":{"repository":"https://example.com/a.git","branch":{"$param":"branch"}}}`}, "body.components[a]"},
		{"an optional parameter left out is fine", `{"type":"object","properties":{"branch":{"type":"string","examples":["dev"]}}}`,
			[]string{`{"name":"a","source":{"repository":"https://example.com/a.git","branch":{"$param":"branch"}}}`}, ""},
		{"resource checks come first", `{"type":"object","properties":{}}`,
			[]string{`{"name":"a","image":{"$param":"nope"}}`}, "body.components[a].image"},
		{"a stand-in that doesn't fit its schema", `{"type":"object","required":["tag"],"properties":{"tag":{"type":"string","maxLength":3}}}`,
			[]string{`{"name":"a","image":"nginx:${tag}"}`}, "parameters.tag.examples"},
		{"a stand-in fitting a pattern via enum", `{"type":"object","required":["tag"],"properties":{"tag":{"type":"string","enum":["1.27"]}}}`,
			[]string{`{"name":"a","image":"nginx:${tag}"}`}, ""},
		{"a missing port on a dependency", "",
			[]string{`{"name":"a","image":"x"}`, `{"name":"b","image":"x","dependencies":[{"component":"a","port":"h","env":"A"}]}`}, "body.components[b].dependencies[0].port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, v := version(tc.params, tc.comps...)
			err := ValidatePublish(m, v)
			if tc.field == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			var fe resource.FieldErrors
			if !errors.As(err, &fe) {
				t.Fatalf("err = %v, want FieldErrors", err)
			}
			if !slices.Contains(fields(fe), tc.field) {
				t.Errorf("errors = %v, want one for %s", fe, tc.field)
			}
		})
	}
}

// TestValidatePublish_NamesStandIns checks a refusal caused by a stand-in
// says which parameters were stood in for.
func TestValidatePublish_NamesStandIns(t *testing.T) {
	m, v := version(`{"type":"object","required":["repo"],"properties":{"repo":{"type":"string"}}}`,
		`{"name":"a","source":{"repository":{"$param":"repo"}}}`)
	err := ValidatePublish(m, v)
	if err == nil || !strings.Contains(err.Error(), "stand-ins") || !strings.Contains(err.Error(), "repo") {
		t.Errorf("err = %v, want it to name the stand-in for repo", err)
	}
}
