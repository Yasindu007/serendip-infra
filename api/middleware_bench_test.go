package api_test

import (
	"net/http"
	"testing"
)

// BenchmarkAuth compares what each credential costs per request, end to
// end through the server's handler (spec 016 T009, plan.md "Performance
// assessment"). The session cookie adds a SHA-256 of the ID and a map
// lookup, plus the CSRF header check, over the API-key path.
func BenchmarkAuth(b *testing.B) {
	h, _, _, _ := newConsole(b)
	sc := signIn(b, h)
	cases := []struct {
		name  string
		setup func(r *http.Request)
	}{
		{"api-key", func(r *http.Request) { r.Header.Set("Authorization", "Bearer key-alice") }},
		{"session-cookie", func(r *http.Request) {
			r.AddCookie(sc)
			r.Header.Set("X-Serendip-Csrf", "1")
		}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r := req("GET", "/v1/session", nil)
				tc.setup(r)
				if w := serve(h, r); w.Code != http.StatusOK {
					b.Fatalf("status %d", w.Code)
				}
			}
		})
	}
}
