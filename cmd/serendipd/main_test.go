package main

import (
	"reflect"
	"testing"
)

func TestParseNodePort(t *testing.T) {
	tests := []struct {
		raw     string
		want    int32
		wantErr bool
	}{
		{"", 30080, false},
		{"31000", 31000, false},
		{"30000", 30000, false},
		{"32767", 32767, false},
		{"29999", 0, true},
		{"32768", 0, true},
		{"80", 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		got, err := parseNodePort(tt.raw)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseNodePort(%q) = (%d, %v), want (%d, err=%v)", tt.raw, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestParseAPIKeys(t *testing.T) {
	tests := []struct {
		raw  string
		want map[string]string
	}{
		{"", map[string]string{}},
		{"k1:alice,k2:bench", map[string]string{"k1": "alice", "k2": "bench"}},
		{"k1:alice,bad,:x,k3:", map[string]string{"k1": "alice"}},
		// An API key may not pose as a person the identity provider logged in.
		{"k1:person:admin,k2:alice", map[string]string{"k2": "alice"}},
		// Nor as an agent an agent issuer authenticated.
		{"k1:agent:am-dev/x,k2:alice", map[string]string{"k2": "alice"}},
	}
	for _, tt := range tests {
		if got := parseAPIKeys(tt.raw); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseAPIKeys(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}
