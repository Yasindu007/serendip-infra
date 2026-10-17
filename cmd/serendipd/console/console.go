// Package console serves the web console's built files from the serendipd
// binary (specs/016-ui-mvp plan.md decision 1). The files come from
// `pnpm -C ui build`, which writes dist/app here; without a build, Handler
// serves a page saying so, and `go build ./...` still works because
// dist/.gitkeep is committed.
//
// Security headers are not set here: api wraps every non-API response with
// them, so the console and the login routes share one policy.
package console

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the console. A path naming a built file is served as that
// file; any other path gets index.html, so client-side routes load the app.
// Hashed assets under /assets/ are cached for a year; everything else is
// revalidated every time.
func Handler() http.Handler {
	app, err := fs.Sub(dist, "dist/app")
	if err != nil {
		return notBuilt()
	}
	return handlerFor(app)
}

// handlerFor serves app, a built console's root directory.
func handlerFor(app fs.FS) http.Handler {
	index, err := fs.ReadFile(app, "index.html")
	if err != nil {
		return notBuilt()
	}
	files := http.FileServerFS(app)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(app, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(name, "assets/") {
				// A missing asset is a 404, never the app shell: serving HTML
				// as a script would only confuse the browser.
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}

const notBuiltPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Serendip</title></head>
<body><h1>Serendip</h1><p>The console isn't built into this binary.
Run <code>pnpm -C ui install --frozen-lockfile</code> and
<code>pnpm -C ui build</code>, then rebuild serendipd. The API is
available at <code>/v1</code> either way.</p></body></html>
`

func notBuilt() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(notBuiltPage))
	})
}
