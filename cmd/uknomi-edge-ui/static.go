package main

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticFS embeds the Next.js static export from edge-ui/out. The
// `all:` prefix is required so underscore-prefixed names (notably
// _next/static/*) are included — Go's default embed.FS rules exclude
// names starting with `_` or `.`.
//
// The `make edge-ui` target must run before `go build` or `go test`
// on this package; otherwise the embed has nothing to embed and the
// binary serves a 404 for everything.
//
//go:embed all:edge-ui-out
var staticFS embed.FS

// embedRoot is the prefix on every path inside staticFS — it mirrors
// the directory name passed to //go:embed. The directory must exist
// (an empty directory works; `make edge-ui` populates it with the
// Next.js export).
const embedRoot = "edge-ui-out"

// StaticHandler returns an http.Handler that serves files from the
// embedded Next.js static export. Paths that don't resolve to a real
// file fall back to index.html — that's the SPA shape Next.js's
// dynamic /preview/<camera_id> route needs at runtime, because the
// build-time generateStaticParams only emits one placeholder.
//
// The handler matters for two reasons specific to this app:
//
//   1. The Go binary embeds the Next.js export. Without the SPA
//      fallback, /preview/<actual_id> 404s because Next.js generated
//      out/preview/_/index.html, not out/preview/<actual_id>/index.html.
//   2. The API route /preview/<id>/stream must remain owned by
//      PreviewHandler. main.go mounts the API first; this handler
//      sits at "/" and only catches paths the mux didn't route to
//      PreviewHandler. Tests assert this routing.
func StaticHandler(fsys embed.FS) http.Handler {
	// Sub the embedded FS to a normal fs.FS rooted at edge-ui-out so
	// http.FileServer's "/" -> "/index.html" mapping works.
	sub, err := fs.Sub(fsys, embedRoot)
	if err != nil {
		// Bug in the binary, not a runtime condition — bail loudly so
		// CI catches it.
		panic("edgeui: cannot sub embed FS at " + embedRoot + ": " + err.Error())
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urlPath := r.URL.Path
		if urlPath == "" || urlPath == "/" {
			urlPath = "/index.html"
		}
		// Try the exact path first.
		name := strings.TrimPrefix(urlPath, "/")
		if name == "" {
			name = "index.html"
		}
		if exists(sub, name) {
			http.FileServer(http.FS(sub)).ServeHTTP(w, r)
			return
		}
		// Next.js sometimes generates "<route>.html" alongside
		// "<route>/index.html"; try both.
		if exists(sub, name+".html") {
			http.ServeFileFS(w, r, sub, name+".html")
			return
		}
		if exists(sub, path.Join(name, "index.html")) {
			http.ServeFileFS(w, r, sub, path.Join(name, "index.html"))
			return
		}
		// SPA fallback: serve index.html for any non-asset path. Asset
		// requests (anything with an extension) get a real 404 — they
		// shouldn't be rewritten to HTML.
		if hasExt(name) {
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, sub, "index.html")
	})
}

func exists(fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	st, err := f.Stat()
	_ = f.Close()
	if err != nil {
		return false
	}
	return !st.IsDir()
}

// hasExt is true if the URL path's last segment looks like a file
// with an extension (e.g. "foo.js", "_next/static/abc.css"). SPA
// fallback applies to extensionless paths only.
func hasExt(name string) bool {
	base := path.Base(name)
	idx := strings.LastIndex(base, ".")
	return idx > 0 && idx < len(base)-1
}
