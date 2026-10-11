package main

import "testing"

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
