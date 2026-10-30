// Package web embeds and serves the PWA from the same origin as the API.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Asset returns one embedded PWA file, so a caller can use the app's own assets without a second
// copy of them: the launcher icon a Linux desktop shows IS the icon the installed PWA shows.
func Asset(name string) ([]byte, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(sub, name)
}

// Handler serves the embedded PWA. Assets are served from dist; any other path
// falls back to index.html so client-side routes work. When the UI has not been
// built (dist holds only the placeholder) it serves a short notice instead of
// failing, so the Go binary still builds and runs.
// setCacheHeaders tells the browser how to treat a path: never cache the shell, cache the
// hashed assets forever. Without this, index.html could be reused from the HTTP cache and a
// rebuilt UI would never show up.
func setCacheHeaders(w http.ResponseWriter, path string) {
	switch {
	case strings.HasPrefix(path, "/assets/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case path == "/sw.js":
		// The service worker itself must never go stale.
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	default:
		w.Header().Set("Cache-Control", "no-cache")
	}
}

func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Should never happen: dist is embedded at build time.
		panic(err)
	}
	index, indexErr := fs.ReadFile(sub, "index.html")
	if indexErr != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			serveHTML(w, http.StatusOK, []byte(notBuiltPage))
		})
	}

	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			files.ServeHTTP(w, r)
			return
		}
		// Caching: the shell must be revalidated on every load (a rebuilt binary ships new
		// asset names, and a cached index.html would keep the browser on the old bundle), while
		// Vite's content-hashed assets are immutable.
		setCacheHeaders(w, r.URL.Path)
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			serveHTML(w, http.StatusOK, index)
			return
		}
		if exists(sub, name) {
			files.ServeHTTP(w, r)
			return
		}
		// Unknown path with a file extension is an honest 404; anything else is a
		// client-side route → hand back the app shell.
		if strings.Contains(lastSegment(name), ".") {
			http.NotFound(w, r)
			return
		}
		serveHTML(w, http.StatusOK, index)
	})
}

func exists(sub fs.FS, name string) bool {
	f, err := sub.Open(name)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func lastSegment(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func serveHTML(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

const notBuiltPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Thought Keeper</title></head>
<body style="font-family:system-ui;margin:3rem;color:#111">
<h1>UI not built</h1>
<p>The PWA assets are missing from this binary. Build them with <code>task web:build</code>
(or <code>cd web &amp;&amp; pnpm install &amp;&amp; pnpm run build</code>) and rebuild the binary.</p>
</body></html>`
