package openchoreo

import (
	"regexp"
	"strings"
	"testing"
)

var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func TestNames_DeterministicShortAndValid(t *testing.T) {
	ids := []string{
		"0192f1a2-7b3c-7d4e-8f90-123456789abc",
		"0192f1a2-7b3c-7d4e-8f90-123456789abd",
		"x",
	}
	seen := map[string]bool{}
	for _, id := range ids {
		for _, fn := range []func(string) string{projectName, componentName} {
			got := fn(id)
			if got != fn(id) {
				t.Errorf("name for %q not deterministic", id)
			}
			if len(got) > 14 {
				t.Errorf("name %q is %d chars, want <= 14", got, len(got))
			}
			if !dns1123Label.MatchString(got) {
				t.Errorf("name %q is not a valid DNS-1123 label", got)
			}
			if seen[got] {
				t.Errorf("name %q collides", got)
			}
			seen[got] = true
		}
	}
}

func TestNames_BindingNamesFollowUpstreamConvention(t *testing.T) {
	id := "abc"
	if got, want := releaseBindingName(id), componentName(id)+"-development"; got != want {
		t.Errorf("releaseBindingName = %q, want %q", got, want)
	}
	if got, want := projectReleaseBindingName(id), projectName(id)+"-development"; got != want {
		t.Errorf("projectReleaseBindingName = %q, want %q", got, want)
	}
}

func TestSerendipLabels(t *testing.T) {
	if got := serendipLabels("id-1", ""); len(got) != 1 || got[labelSerendipID] != "id-1" {
		t.Errorf("serendipLabels without name = %v", got)
	}
	if got := serendipLabels("id-1", "shop"); got[labelSerendipName] != "shop" {
		t.Errorf("serendipLabels with name = %v", got)
	}
}

// Spec 020's names are valid object names for any Serendip IDs.
func TestDeploymentNamesAreObjectNames(t *testing.T) {
	ids := []string{"01a1645c-bc2e-72fa-bcdc-bab12e0a62a4", "x", strings.Repeat("z", 128)}
	label := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	for _, a := range ids {
		for _, b := range ids {
			for _, n := range []string{
				environmentObjectName(a), pipelineName(a),
				projectEnvBindingName(projectName(a), b), deploymentBindingName(a, b),
				releaseObjectName(a, []byte(b)),
			} {
				if len(n) > 63 || !label.MatchString(n) {
					t.Errorf("%q isn't a valid object name", n)
				}
			}
		}
	}
	if releaseObjectName("c", []byte("one")) == releaseObjectName("c", []byte("two")) {
		t.Error("different content gave the same release name")
	}
	if deploymentBindingName("c", "dev") == deploymentBindingName("c", "prod") {
		t.Error("two Environments gave one binding name")
	}
}
