package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	app := fstest.MapFS{
		"index.html":          {Data: []byte("<html>shell</html>")},
		"assets/index-abc.js": {Data: []byte("console.log(1)")},
		"favicon.svg":         {Data: []byte("<svg/>")},
	}
	h := handlerFor(app)
	cases := []struct {
		method, path string
		status       int
		body, cache  string
	}{
		{"GET", "/", 200, "shell", "no-cache"},
		{"GET", "/applications/123", 200, "shell", "no-cache"},
		{"GET", "/assets/index-abc.js", 200, "console.log", "public, max-age=31536000, immutable"},
		{"GET", "/favicon.svg", 200, "<svg/>", "no-cache"},
		{"GET", "/assets/missing.js", 404, "", ""},
		{"GET", "/../../etc/passwd", 200, "shell", "no-cache"},
		{"POST", "/", 405, "", ""},
		{"HEAD", "/", 200, "", "no-cache"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, "/", nil)
			req.URL.Path = tc.path
			h.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if tc.body != "" && !strings.Contains(rec.Body.String(), tc.body) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tc.body)
			}
			if tc.cache != "" && rec.Header().Get("Cache-Control") != tc.cache {
				t.Errorf("Cache-Control = %q, want %q", rec.Header().Get("Cache-Control"), tc.cache)
			}
		})
	}
}

func TestHandler_NotBuilt(t *testing.T) {
	rec := httptest.NewRecorder()
	handlerFor(fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "isn't built") {
		t.Errorf("got %d %q, want the not-built page", rec.Code, rec.Body.String())
	}
}
