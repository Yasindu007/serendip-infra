package main

import "testing"

func TestConsoleOrigin(t *testing.T) {
	cases := []struct {
		addr, public, insecure string
		want                   string
	}{
		{":8080", "", "", "http://localhost:8080"},
		{"127.0.0.1:9000", "", "", "http://localhost:9000"},
		{":8080", "https://serendip.example", "", "https://serendip.example"},
		{":8080", "http://192.168.1.5:8080", "", ""},
		{":8080", "http://192.168.1.5:8080", "1", "http://192.168.1.5:8080"},
		{":8080", "http://localhost:8080/console", "", ""},
		{"no-port", "", "", ""},
	}
	for _, tc := range cases {
		t.Setenv("SERENDIP_PUBLIC_URL", tc.public)
		t.Setenv("SERENDIP_ALLOW_INSECURE_ORIGIN", tc.insecure)
		got, err := consoleOrigin(tc.addr)
		if tc.want == "" {
			if err == nil {
				t.Errorf("consoleOrigin(%q) with %q = %v, want an error", tc.addr, tc.public, got)
			}
			continue
		}
		if err != nil || got.String() != tc.want {
			t.Errorf("consoleOrigin(%q) with %q = %v, %v; want %s", tc.addr, tc.public, got, err, tc.want)
		}
	}
}
