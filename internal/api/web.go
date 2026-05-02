package api

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:web_dist
var webFS embed.FS

// dashboardHandler serves the embedded React build, falling back to
// index.html for client-side routing. If the build hasn't been produced yet
// (e.g. fresh checkout, `go run` without `make web`) we return a small HTML
// stub explaining how to build the dashboard.
func dashboardHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web_dist")
	if err != nil {
		return placeholderHandler()
	}
	if entries, err := fs.ReadDir(sub, "."); err != nil || len(entries) == 0 {
		return placeholderHandler()
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		f, err := sub.Open(path[1:])
		if err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fallback to index for client-side routes.
		index, err := sub.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer index.Close()
		w.Header().Set("content-type", "text/html; charset=utf-8")
		buf := make([]byte, 64*1024)
		n, _ := index.Read(buf)
		_, _ = w.Write(buf[:n])
	})
}

func placeholderHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html><head><meta charset="utf-8"><title>selfCloud</title>
<style>body{font-family:ui-sans-serif,system-ui;background:#0a0e14;color:#e6edf3;margin:0;padding:48px;line-height:1.6}code{background:#161b22;padding:2px 6px;border-radius:4px}h1{margin:0 0 12px}a{color:#58a6ff}</style>
</head><body>
<h1>selfCloud</h1>
<p>The dashboard hasn't been built yet. From the project root run:</p>
<pre><code>make web</code></pre>
<p>Then refresh this page. The REST API is already serving on <code>/api/v1</code>.</p>
</body></html>`))
	})
}
