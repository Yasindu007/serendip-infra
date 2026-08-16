package resource_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		meta    resource.Metadata
		wantErr bool
	}{
		{
			name: "valid minimal",
			meta: resource.Metadata{Name: "my-app"},
		},
		{
			name:    "empty name rejected",
			meta:    resource.Metadata{Name: ""},
			wantErr: true,
		},
		{
			name: "valid labels including prefixed key and empty value",
			meta: resource.Metadata{
				Name: "my-app",
				Labels: map[string]string{
					"team":                     "platform",
					"serendip.dev/environment": "production",
					"empty-value-allowed":      "",
				},
			},
		},
		{
			name: "invalid label key rejected",
			meta: resource.Metadata{
				Name:   "my-app",
				Labels: map[string]string{"bad key with spaces": "x"},
			},
			wantErr: true,
		},
		{
			name: "invalid label value rejected",
			meta: resource.Metadata{
				Name:   "my-app",
				Labels: map[string]string{"team": "not valid!"},
			},
			wantErr: true,
		},
		{
			name: "invalid label prefix rejected",
			meta: resource.Metadata{
				Name:   "my-app",
				Labels: map[string]string{"BadPrefix/team": "platform"},
			},
			wantErr: true,
		},
		{
			name: "relationship with EdgeOwns type rejected",
			meta: resource.Metadata{
				Name: "my-app",
				Relationships: []resource.Relationship{
					{Type: resource.EdgeOwns, TargetKind: resource.KindProject, TargetID: "p1"},
				},
			},
			wantErr: true,
		},
		{
			name: "relationship with non-owns type accepted",
			meta: resource.Metadata{
				Name: "my-app",
				Relationships: []resource.Relationship{
					{Type: resource.EdgeRunsOn, TargetKind: resource.KindCluster, TargetID: "c1"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := resource.Validate(tt.meta)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate(%+v) error = %v, wantErr %v", tt.meta, err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, resource.ErrValidation) {
				t.Errorf("Validate(%+v) error = %v, want it to wrap resource.ErrValidation", tt.meta, err)
			}
		})
	}
}

func TestResource_GenericEnvelope(t *testing.T) {
	type fakeSpec struct{ Foo string }
	type fakeStatus struct{ resource.Status }

	r := resource.Resource[fakeSpec, fakeStatus]{
		Metadata: resource.Metadata{Name: "example"},
		Spec:     fakeSpec{Foo: "bar"},
		Status: fakeStatus{Status: resource.Status{
			Conditions: []resource.Condition{
				{Type: "Ready", Status: resource.ConditionTrue},
			},
		}},
	}

	if r.Spec.Foo != "bar" {
		t.Errorf("Spec.Foo = %q, want %q", r.Spec.Foo, "bar")
	}
	if len(r.Status.Conditions) != 1 {
		t.Errorf("len(Status.Conditions) = %d, want 1", len(r.Status.Conditions))
	}
}

// TestMetadata_JSONTags_AreLowerCamelCase proves the actual point of
// spec 002's T005-T010 (adding json tags to core/resource types): the
// marshaled shape uses lowerCamelCase keys suitable for an HTTP API, not
// Go's default capitalized field names. Round-trips back through
// resource.Organization to confirm unmarshal still works too.
func TestMetadata_JSONTags_AreLowerCamelCase(t *testing.T) {
	org := resource.Organization{
		Metadata: resource.Metadata{
			ID:              "org-1",
			Name:            "acme",
			Kind:            resource.KindOrganization,
			Labels:          map[string]string{"tier": "prod"},
			ResourceVersion: 1,
		},
	}

	data, err := json.Marshal(org)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}

	for _, wantKey := range []string{
		`"id":"org-1"`, `"name":"acme"`, `"kind":"Organization"`,
		`"resourceVersion":1`, `"tier":"prod"`,
	} {
		if !strings.Contains(string(data), wantKey) {
			t.Errorf("marshaled JSON = %s, want it to contain %s", data, wantKey)
		}
	}
	// Confirm capitalized Go field names did NOT leak through unchanged.
	for _, wantAbsent := range []string{`"Name":`, `"ID":`, `"ResourceVersion":`} {
		if strings.Contains(string(data), wantAbsent) {
			t.Errorf("marshaled JSON = %s, want it to NOT contain %s (default Go field name)", data, wantAbsent)
		}
	}

	var roundTripped resource.Organization
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if roundTripped.Metadata.Name != "acme" || roundTripped.Metadata.ID != "org-1" {
		t.Errorf("round-tripped Metadata = %+v, want Name=acme ID=org-1", roundTripped.Metadata)
	}
}
