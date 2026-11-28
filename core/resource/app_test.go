package resource

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateComponentImage(t *testing.T) {
	cases := []struct {
		image string
		ok    bool
	}{
		{"", true},
		{"nginx", true},
		{"nginx:1.27-alpine", true},
		{"registry.local:5000/team/app:v1", true},
		{"[fd00::1]:5000/app:v1", true},
		{"ghcr.io/a/b@sha256:0123abcd", true},
		{"app@sha256+b64u:abc", true},
		{"nginx\nevil", false},
		{"nginx:1 --privileged", false},
		{`{"$param":"x"}`, false},
		{"${name}", false},
		{"-nginx", false},
		{strings.Repeat("a", maxImageLen+1), false},
	}
	for _, tc := range cases {
		err := ValidateComponentImage(ComponentSpec{Image: tc.image})
		if (err == nil) != tc.ok {
			t.Errorf("%q: err = %v, want ok=%v", tc.image, err, tc.ok)
		}
		if err != nil && !errors.Is(err, ErrValidation) {
			t.Errorf("%q: err isn't a validation error", tc.image)
		}
	}
}

func TestValidateComponentPorts(t *testing.T) {
	cases := []struct {
		name  string
		ports []ComponentPort
		want  string
	}{
		{"valid", []ComponentPort{{Name: "http", Port: 80}, {Name: "grpc", Port: 65535}}, ""},
		{"not a DNS label", []ComponentPort{{Name: "HTTP_Main", Port: 80}}, "ports[0].name"},
		{"too long", []ComponentPort{{Name: strings.Repeat("a", 64), Port: 80}}, "ports[0].name"},
		{"zero", []ComponentPort{{Name: "http", Port: 0}}, "ports[0].port"},
		{"too big", []ComponentPort{{Name: "http", Port: 65536}}, "ports[0].port"},
		{"twice", []ComponentPort{{Name: "http", Port: 80}, {Name: "http", Port: 81}}, "ports[1].name"},
	}
	for _, tc := range cases {
		err := ValidateComponentPorts(ComponentSpec{Ports: tc.ports})
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.want)
		}
	}
}
