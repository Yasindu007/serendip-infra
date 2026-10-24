package resource_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/serendip-infra/serendip-infra/core/resource"
)

func TestValidateBuild(t *testing.T) {
	owned := resource.Metadata{Name: "b", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindComponent, ID: "web"}}}
	sha1 := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name  string
		m     resource.Metadata
		s     resource.BuildSpec
		valid bool
	}{
		{"head of the branch", owned, resource.BuildSpec{}, true},
		{"a SHA-1 commit", owned, resource.BuildSpec{Commit: sha1}, true},
		{"a SHA-256 commit", owned, resource.BuildSpec{Commit: strings.Repeat("0", 64)}, true},
		{"abbreviated", owned, resource.BuildSpec{Commit: "abc1234"}, false},
		{"upper case", owned, resource.BuildSpec{Commit: strings.Repeat("A", 40)}, false},
		{"a branch name", owned, resource.BuildSpec{Commit: "main"}, false},
		{"no owner", resource.Metadata{Name: "b"}, resource.BuildSpec{}, false},
		{"owned by an Application", resource.Metadata{Name: "b", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: "a"}}}, resource.BuildSpec{}, false},
		{"two owners", resource.Metadata{Name: "b", OwnerRefs: []resource.OwnerRef{{Kind: resource.KindComponent, ID: "web"}, {Kind: resource.KindComponent, ID: "api"}}}, resource.BuildSpec{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := resource.ValidateBuild(tc.m, tc.s)
			if (err == nil) != tc.valid || (err != nil && !errors.Is(err, resource.ErrValidation)) {
				t.Errorf("ValidateBuild() = %v, want valid %v", err, tc.valid)
			}
		})
	}
	if got := resource.BuildComponent(owned); got != "web" {
		t.Errorf("BuildComponent() = %q", got)
	}
}

func TestBuildPhaseDone(t *testing.T) {
	for p, done := range map[resource.BuildPhase]bool{
		resource.BuildQueued: false, resource.BuildRunning: false,
		resource.BuildSucceeded: true, resource.BuildFailed: true, resource.BuildCancelled: true,
	} {
		if p.Done() != done {
			t.Errorf("%s.Done() = %v", p, !done)
		}
	}
}

func TestValidateComponentSource(t *testing.T) {
	src := func(edit func(*resource.ComponentSource)) resource.ComponentSpec {
		s := resource.ComponentSource{Repository: "https://github.com/org/repo.git"}
		edit(&s)
		return resource.ComponentSpec{Source: &s}
	}
	repo := func(r string) resource.ComponentSpec {
		return src(func(s *resource.ComponentSource) { s.Repository = r })
	}
	for _, tc := range []struct {
		name  string
		spec  resource.ComponentSpec
		valid bool
	}{
		{"an image", resource.ComponentSpec{Image: "nginx:1.27"}, true},
		{"a source with defaults", src(func(*resource.ComponentSource) {}), true},
		{"both", resource.ComponentSpec{Image: "nginx", Source: &resource.ComponentSource{Repository: "https://github.com/o/r"}}, false},
		{"neither", resource.ComponentSpec{}, false},
		{"no repository", repo(""), false},
		{"https with a port", repo("https://git.example.com:8443/team/app"), true},
		{"ssh URL", repo("ssh://git@github.com/org/repo.git"), true},
		{"scp-like", repo("git@github.com:org/repo.git"), true},
		{"scp-like read as an option", repo("-ofoo@h:p"), false},
		{"plain http", repo("http://github.com/org/repo"), false},
		{"credentials in the URL", repo("https://user:token@github.com/org/repo"), false},
		{"a query", repo("https://github.com/org/repo?x=1"), false},
		{"shell characters", repo("https://github.com/org/repo;rm -rf"), false},
		{"command substitution", repo("https://github.com/$(id)/repo"), false},
		{"no path", repo("https://github.com"), false},
		{"file URL", repo("file:///etc"), false},
		{"too long", repo("https://github.com/" + strings.Repeat("a", 2048)), false},
		{"a nested branch", src(func(s *resource.ComponentSource) { s.Branch = "release/1.2" }), true},
		{"branch with ..", src(func(s *resource.ComponentSource) { s.Branch = "a..b" }), false},
		{"branch like an option", src(func(s *resource.ComponentSource) { s.Branch = "-x" }), false},
		{"branch ending .lock", src(func(s *resource.ComponentSource) { s.Branch = "main.lock" }), false},
		{"a .lock component", src(func(s *resource.ComponentSource) { s.Branch = "a.lock/b" }), false},
		{"a hidden component", src(func(s *resource.ComponentSource) { s.Branch = "a/.b" }), false},
		{"an empty component", src(func(s *resource.ComponentSource) { s.Branch = "a//b" }), false},
		{"branch with a space", src(func(s *resource.ComponentSource) { s.Branch = "a b" }), false},
		{"a sub-directory", src(func(s *resource.ComponentSource) { s.Path = "services/api" }), true},
		{"leaving the repository", src(func(s *resource.ComponentSource) { s.Path = "../x" }), false},
		{"an absolute path", src(func(s *resource.ComponentSource) { s.Path = "/etc" }), false},
		{"a nested dockerfile", src(func(s *resource.ComponentSource) { s.Dockerfile = "build/Dockerfile.prod" }), true},
		{"dockerfile as .", src(func(s *resource.ComponentSource) { s.Dockerfile = "." }), false},
		{"dockerfile as a directory", src(func(s *resource.ComponentSource) { s.Dockerfile = "dir/" }), false},
		{"a path with a trailing slash", src(func(s *resource.ComponentSource) { s.Path = "services/api/" }), true},
		{"a . segment", src(func(s *resource.ComponentSource) { s.Path = "a/./b" }), false},
		{"dockerfile escaping", src(func(s *resource.ComponentSource) { s.Dockerfile = "a/../../x" }), false},
		{"a credential Secret", src(func(s *resource.ComponentSource) { s.CredentialSecret = "0192f3e4-aaaa" }), true},
		{"a bad Secret ID", src(func(s *resource.ComponentSource) { s.CredentialSecret = "a/b" }), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := resource.ValidateComponentSource(tc.spec)
			if (err == nil) != tc.valid || (err != nil && !errors.Is(err, resource.ErrValidation)) {
				t.Errorf("ValidateComponentSource() = %v, want valid %v", err, tc.valid)
			}
		})
	}
}

func TestComponentSourceDefaultsAndSSH(t *testing.T) {
	got := resource.ComponentSource{Repository: "https://h/o/r"}.WithDefaults()
	if got.Branch != "main" || got.Path != "." || got.Dockerfile != "Dockerfile" {
		t.Errorf("WithDefaults() = %+v", got)
	}
	kept := resource.ComponentSource{Branch: "dev", Path: "app", Dockerfile: "D"}.WithDefaults()
	if kept.Branch != "dev" || kept.Path != "app" || kept.Dockerfile != "D" {
		t.Errorf("WithDefaults() replaced set fields: %+v", kept)
	}
	for r, ssh := range map[string]bool{
		"https://github.com/o/r":     false,
		"ssh://git@github.com/o/r":   true,
		"git@github.com:o/r.git":     true,
		"https://git@github.com/o/r": false,
	} {
		if got := (resource.ComponentSource{Repository: r}).SSH(); got != ssh {
			t.Errorf("SSH(%q) = %v", r, got)
		}
	}
}
