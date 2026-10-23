package jsonschema_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/jsonschema"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return v
}

// The real schema: serendip-service's per-environment configuration, as
// the platform holds it (spec 020 T001 finding 7).
func TestEnvironmentSchema(t *testing.T) {
	raw, err := os.ReadFile("testdata/serendip-service-environment.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := jsonschema.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, value string
		want        []string // "path: message" fragments, in order
	}{
		{name: "empty", value: `{}`},
		{name: "everything", value: `{"replicas":3,"imagePullPolicy":"Always","resources":{"requests":{"cpu":"250m","memory":"128Mi"},"limits":{"cpu":"1","memory":"512Mi"}}}`},
		{name: "zero replicas", value: `{"replicas":0}`},
		{name: "negative replicas", value: `{"replicas":-1}`, want: []string{"replicas: must be at least 0"}},
		{name: "fractional replicas", value: `{"replicas":1.5}`, want: []string{"replicas: must be integer, not a number"}},
		{name: "replicas as text", value: `{"replicas":"2"}`, want: []string{"replicas: must be integer, not a string"}},
		{name: "bad pull policy", value: `{"imagePullPolicy":"Sometimes"}`, want: []string{`imagePullPolicy: must be one of "Always", "IfNotPresent", "Never"`}},
		{name: "through $ref", value: `{"resources":{"limits":{"cpu":2}}}`, want: []string{"resources.limits.cpu: must be string, not a whole number"}},
		{name: "a typo", value: `{"replica":2}`, want: []string{"replica: isn't a known setting (known: imagePullPolicy, replicas, resources)"}},
		{name: "a typo deep down", value: `{"resources":{"limit":{}}}`, want: []string{"resources.limit: isn't a known setting (known: limits, requests)"}},
		{name: "several", value: `{"replicas":-2,"imagePullPolicy":1}`, want: []string{"imagePullPolicy: must be string", "replicas: must be at least 0"}},
		{name: "not an object", value: `[]`, want: []string{"must be object, not a list"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.Validate(decode(t, tt.value))
			if len(got) != len(tt.want) {
				t.Fatalf("problems = %v, want %v", got, tt.want)
			}
			for i, p := range got {
				if !strings.HasPrefix(p.String(), tt.want[i]) {
					t.Errorf("problem %d = %q, want %q", i, p, tt.want[i])
				}
			}
		})
	}
}

func TestKeywords(t *testing.T) {
	s, err := jsonschema.Parse([]byte(`{
		"type": "object",
		"required": ["name"],
		"additionalProperties": {"type": "string"},
		"properties": {
			"name": {"type": "string", "minLength": 2, "maxLength": 5, "pattern": "^[a-z]+$"},
			"ratio": {"type": "number", "exclusiveMinimum": 0, "maximum": 1, "exclusiveMaximum": 2},
			"tags": {"type": "array", "minItems": 1, "maxItems": 2, "items": {"type": "string"}},
			"on": {"type": ["boolean", "null"]},
			"x": {"x-kubernetes-preserve-unknown-fields": true, "description": "free"}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	for value, want := range map[string]string{
		`{"name":"abc","ratio":0.5,"tags":["a"],"on":null,"x":{"any":1},"extra":"ok"}`: "",
		`{}`:                                 "name: is required",
		`{"name":"a"}`:                       "name: must be at least 2 characters",
		`{"name":"abcdef"}`:                  "name: must be at most 5 characters",
		`{"name":"AB"}`:                      "name: must match ^[a-z]+$",
		`{"name":"ab","ratio":0}`:            "ratio: must be more than 0",
		`{"name":"ab","ratio":1.5}`:          "ratio: must be at most 1",
		`{"name":"ab","tags":[]}`:            "tags: must have at least 1 items",
		`{"name":"ab","tags":[1]}`:           "tags[0]: must be string",
		`{"name":"ab","on":"yes"}`:           "on: must be boolean or null",
		`{"name":"ab","extra":3}`:            "extra: must be string",
		`{"name":"ab","tags":["a","b","c"]}`: "tags: must have at most 2 items",
	} {
		got := s.Validate(decode(t, value))
		switch {
		case want == "" && len(got) != 0:
			t.Errorf("%s: problems %v", value, got)
		case want != "" && (len(got) != 1 || !strings.HasPrefix(got[0].String(), want)):
			t.Errorf("%s: problems %v, want %q", value, got, want)
		}
	}
	strict, _ := jsonschema.Parse([]byte(`{"type":"object","additionalProperties":false}`))
	if got := strict.Validate(decode(t, `{"a":1}`)); len(got) != 1 {
		t.Errorf("additionalProperties false: %v", got)
	}
	open, _ := jsonschema.Parse([]byte(`{"type":"object"}`))
	if got := open.Validate(decode(t, `{"a":1}`)); len(got) != 0 {
		t.Errorf("an object without properties takes anything: %v", got)
	}
}

// A schema it can't fully check is refused, never half-checked.
func TestParseFailsClosed(t *testing.T) {
	for _, schema := range []string{
		`{"oneOf":[{"type":"string"}]}`,
		`{"properties":{"a":{"if":{}}}}`,
		`{"$ref":"https://example.com/s.json"}`,
		`{"$ref":"#/$defs/Missing"}`,
		`{"type":"decimal"}`,
		`{"pattern":"("}`,
		`{"properties":{"a":true}}`,
		`not json`,
		`null`,
		`{"enum":"a"}`,
		`{"required":"a"}`,
		`{"required":[1]}`,
		`{"minimum":"1"}`,
		`{"maxLength":true}`,
		`{"exclusiveMinimum":"x"}`,
		`{"nullable":"yes"}`,
		`{"x-shared":{"T":{"oneOf":[]}},"$ref":"#/x-shared/T"}`,
		`{"default":{"oneOf":[]},"$ref":"#/default"}`,
	} {
		if _, err := jsonschema.Parse([]byte(schema)); !errors.Is(err, jsonschema.ErrUnsupported) {
			t.Errorf("%s: err = %v", schema, err)
		}
	}
}

// OpenAPI 3.0's forms, which openAPIV3Schema uses.
func TestOpenAPIForms(t *testing.T) {
	s, err := jsonschema.Parse([]byte(`{"type":"object","properties":{
		"n": {"type":"integer","minimum":0,"exclusiveMinimum":true,"maximum":10,"exclusiveMaximum":true},
		"s": {"type":"string","nullable":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for value, want := range map[string]int{`{"n":1}`: 0, `{"n":0}`: 1, `{"n":10}`: 1, `{"s":null}`: 0, `{"s":"x"}`: 0, `{"n":null}`: 1} {
		if got := s.Validate(decode(t, value)); len(got) != want {
			t.Errorf("%s: problems %v, want %d", value, got, want)
		}
	}
}

// A self-referencing schema ends instead of looping.
func TestRecursiveRefEnds(t *testing.T) {
	s, err := jsonschema.Parse([]byte(`{"$defs":{"Node":{"type":"object","properties":{"next":{"$ref":"#/$defs/Node"}}}},"$ref":"#/$defs/Node"}`))
	if err != nil {
		t.Fatal(err)
	}
	deep := `{}`
	for range 40 {
		deep = `{"next":` + deep + `}`
	}
	if got := s.Validate(decode(t, deep)); len(got) == 0 || !strings.Contains(got[0].Message, "nested too deeply") {
		t.Errorf("problems = %v", got)
	}
	if got := s.Validate(decode(t, `{"next":{"next":{}}}`)); len(got) != 0 {
		t.Errorf("a short chain: %v", got)
	}
}
