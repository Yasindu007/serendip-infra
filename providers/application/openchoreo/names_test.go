package openchoreo

import (
	"regexp"
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
