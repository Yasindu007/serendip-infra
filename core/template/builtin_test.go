package template

import (
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

// TestBuiltIns checks each starter is a valid Template whose versions pass
// the publish checks, and renders with only its required parameters
// (spec criterion 1, as far as a unit test goes).
func TestBuiltIns(t *testing.T) {
	required := map[string]map[string]any{
		ServiceFromSourceID: {"repository": "https://github.com/example/app.git"},
	}
	ids := map[string]bool{}
	for _, b := range BuiltIns() {
		t.Run(b.Template.Metadata.Name, func(t *testing.T) {
			if ids[b.Template.Metadata.ID] {
				t.Fatalf("ID %s used twice", b.Template.Metadata.ID)
			}
			ids[b.Template.Metadata.ID] = true
			if !IsBuiltIn(b.Template.Metadata.ID) {
				t.Errorf("IsBuiltIn(%s) = false", b.Template.Metadata.ID)
			}
			if err := resource.ValidateTemplate(b.Template.Metadata, b.Template.Spec); err != nil {
				t.Errorf("ValidateTemplate: %v", err)
			}
			if len(b.Versions) == 0 {
				t.Fatal("no versions")
			}
			for _, v := range b.Versions {
				if ids[v.Metadata.ID] {
					t.Fatalf("ID %s used twice", v.Metadata.ID)
				}
				ids[v.Metadata.ID] = true
				if err := ValidatePublish(v.Metadata, v.Spec); err != nil {
					t.Errorf("version %s: ValidatePublish: %v", v.Metadata.Name, err)
				}
				r, errs := Render(v.Spec, required[b.Template.Metadata.ID])
				if errs != nil {
					t.Fatalf("version %s: Render with only required parameters: %v", v.Metadata.Name, errs)
				}
				for _, c := range r.Components {
					if c.Spec.Image != "" && c.Spec.Image != starterImage {
						t.Errorf("%s runs %s, want the cached %s", c.Name, c.Spec.Image, starterImage)
					}
				}
			}
		})
	}
	if len(ids) != 6 {
		t.Errorf("%d IDs, want 3 templates and 3 versions", len(ids))
	}
}

func TestBuiltIns_Shapes(t *testing.T) {
	b, ok := LookupBuiltIn(WebAndAPIID)
	if !ok {
		t.Fatal("web-and-api not found")
	}
	r, errs := Render(b.Versions[0].Spec, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	if len(r.Components) != 2 || r.Components[0].Name != "api" || r.Components[1].Name != "web" {
		t.Fatalf("components = %+v, want api then web", r.Components)
	}
	web := r.Components[1].Spec
	if len(web.Dependencies) != 1 || web.Dependencies[0] != (resource.ComponentDependency{Component: "api", Port: "http", Env: "API_URL"}) {
		t.Errorf("web dependencies = %+v", web.Dependencies)
	}
	if !web.Ports[0].Expose || r.Components[0].Spec.Ports[0].Expose {
		t.Errorf("only web should be exposed")
	}

	src, _ := LookupBuiltIn(ServiceFromSourceID)
	if got := src.Versions[0].Spec.Body.Requires; len(got) != 1 || got[0] != "builds" {
		t.Errorf("service-from-source requires %v, want builds", got)
	}
	r, errs = Render(src.Versions[0].Spec, map[string]any{"repository": "https://github.com/example/app.git"})
	if errs != nil {
		t.Fatal(errs)
	}
	s := r.Components[0].Spec
	if s.Source == nil || s.Source.Branch != "main" || s.Source.Path != "." || s.Source.CredentialSecret != "" || s.Ports[0].Port != 8080 {
		t.Errorf("service = %+v, source %+v", s, s.Source)
	}
	if _, ok := r.Parameters["credentialSecret"]; ok {
		t.Errorf("an omitted optional Secret is in the parameters: %v", r.Parameters)
	}
}

// TestBuiltIns_Fresh checks a caller changing a built-in can't change the
// next caller's.
func TestBuiltIns_Fresh(t *testing.T) {
	a, _ := LookupBuiltIn(WebServerID)
	a.Template.Spec.DisplayName = "changed"
	a.Versions[0].Spec.Parameters[0] = 'X'
	b, _ := LookupBuiltIn(WebServerID)
	if b.Template.Spec.DisplayName == "changed" || b.Versions[0].Spec.Parameters[0] == 'X' {
		t.Error("a change to one copy reached another")
	}
	if _, ok := LookupBuiltIn("nope"); ok || IsBuiltIn("nope") {
		t.Error("an unknown ID is a built-in")
	}
}
